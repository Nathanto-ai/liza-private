package ops

import (
	"fmt"
	"strings"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/errors"
	"github.com/liza-mas/liza/internal/git"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
)

// SupersedeResult contains the outcome of superseding a task.
type SupersedeResult struct {
	TaskID         string
	OriginalStatus models.TaskStatus
	ReplacementIDs []string
	Warnings       []string
}

// SupersedeTask transitions a BLOCKED, REJECTED, or READY task to SUPERSEDED,
// linking it to one or more replacement task IDs. No terminal I/O.
func SupersedeTask(projectRoot, taskID string, replacementIDs []string, reason, agentID string) (*SupersedeResult, error) {
	if taskID == "" {
		return nil, &PreconditionError{Reason: "task ID is required"}
	}
	if len(replacementIDs) == 0 {
		return nil, &PreconditionError{Reason: "at least one replacement task ID is required"}
	}
	if reason == "" {
		return nil, &PreconditionError{Reason: "rescope reason is required"}
	}
	if agentID == "" {
		return nil, &PreconditionError{Reason: "orchestrator agent ID is required"}
	}

	lp := paths.New(projectRoot)
	bb := db.For(lp.StatePath())

	pb, err := loadPipelineBundle(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to load pipeline config: %w", err)
	}

	// Phase 1: Read and Validate (no lock held)
	_, task, err := readTaskState(bb, taskID)
	if err != nil {
		return nil, err
	}

	originalStatus := task.Status
	if originalStatus != models.TaskStatusBlocked &&
		originalStatus != models.TaskStatusRejected &&
		originalStatus != models.TaskStatusReady {
		return nil, &PreconditionError{Reason: fmt.Sprintf("cannot supersede task %s in status %s (must be BLOCKED, REJECTED, or READY)", taskID, originalStatus)}
	}

	// Phase 2: Atomic State Update
	hadWorktree := task.Worktree != nil
	err = bb.Modify(func(state *models.State) error {
		currentTask := state.FindTask(taskID)
		if currentTask == nil {
			return &errors.NotFoundError{Entity: "task", ID: taskID}
		}

		if currentTask.Status != originalStatus {
			return &PreconditionError{Reason: fmt.Sprintf("cannot supersede task %s: status changed from %s to %s", taskID, originalStatus, currentTask.Status)}
		}

		if err := currentTask.TransitionWith(models.TaskStatusSuperseded, pb.transitions); err != nil {
			return err
		}
		currentTask.SupersededBy = replacementIDs
		currentTask.RescopeReason = &reason

		currentTask.AssignedTo = nil
		currentTask.LeaseExpires = nil
		currentTask.ReviewingBy = nil
		currentTask.ReviewLeaseExpires = nil
		currentTask.Worktree = nil

		// Migrate dependency references: any task depending on the
		// superseded task now depends on its replacement(s).
		for i := range state.Tasks {
			t := &state.Tasks[i]
			if t.ID == taskID {
				continue
			}
			newDeps := make([]string, 0, len(t.DependsOn))
			changed := false
			for _, dep := range t.DependsOn {
				if dep == taskID {
					newDeps = append(newDeps, replacementIDs...)
					changed = true
				} else {
					newDeps = append(newDeps, dep)
				}
			}
			if changed {
				t.DependsOn = newDeps
			}
		}

		now := time.Now().UTC()
		note := fmt.Sprintf("replaced by: %s", strings.Join(replacementIDs, ", "))
		currentTask.History = append(currentTask.History, models.TaskHistoryEntry{
			Time:   now,
			Event:  models.TaskEventSuperseded,
			Agent:  &agentID,
			Reason: &reason,
			Note:   &note,
		})

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to supersede task: %w", err)
	}

	// Best-effort worktree cleanup (after state commit — safe to lose worktree now).
	// Branch is intentionally preserved — successors may need it via git show.
	var warnings []string
	gw := git.New(projectRoot)
	if hadWorktree {
		if rmErr := gw.RemoveWorktreeDir(taskID); rmErr != nil {
			warnings = append(warnings, fmt.Sprintf("failed to remove worktree directory: %v", rmErr))
		}
	}

	// This superseded task may itself be a successor — check if its terminal
	// transition releases an older predecessor's branch.
	warnings = append(warnings, cleanupPredecessorBranches(bb, gw, taskID)...)

	return &SupersedeResult{
		TaskID:         taskID,
		OriginalStatus: originalStatus,
		ReplacementIDs: replacementIDs,
		Warnings:       warnings,
	}, nil
}
