package ops

import (
	"fmt"
	"slices"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/git"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
)

// cleanupPredecessorBranches deletes branches of superseded predecessor tasks
// once all their successors have reached terminal status. This is called after
// any terminal transition (merge, cancel, supersede) to check whether the
// transitioning task was the last active successor of a superseded predecessor.
//
// No state mutation — branch-only cleanup. All errors are non-fatal warnings.
func cleanupPredecessorBranches(bb *db.Blackboard, gw *git.Git, taskID string) []string {
	var warnings []string

	state, err := bb.Read()
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("predecessor cleanup: failed to read state: %v", err))
		return warnings
	}

	for i := range state.Tasks {
		predecessor := &state.Tasks[i]
		if predecessor.Status != models.TaskStatusSuperseded {
			continue
		}
		if !slices.Contains(predecessor.SupersededBy, taskID) {
			continue
		}

		// Check if all successors are terminal
		allTerminal := true
		for _, successorID := range predecessor.SupersededBy {
			successor := state.FindTask(successorID)
			if successor == nil {
				// Unresolved successor — treat as not terminal to be safe
				warnings = append(warnings, fmt.Sprintf("predecessor cleanup: successor %s of predecessor %s not found in state", successorID, predecessor.ID))
				allTerminal = false
				break
			}
			if !successor.Status.IsComplete() {
				allTerminal = false
				break
			}
		}

		if !allTerminal {
			continue
		}

		// All successors terminal — delete predecessor's branch
		branchName := paths.TaskBranchPrefix + predecessor.ID
		exists, err := gw.BranchExists(branchName)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("predecessor cleanup: failed to check branch %s: %v", branchName, err))
			continue
		}
		if exists {
			if err := gw.DeleteBranch(branchName); err != nil {
				warnings = append(warnings, fmt.Sprintf("predecessor cleanup: failed to delete branch %s: %v", branchName, err))
			}
		}
	}

	return warnings
}
