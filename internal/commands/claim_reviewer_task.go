package commands

import (
	"fmt"
	"time"

	"github.com/liza-mas/liza/internal/ops"
)

// ClaimReviewerTaskCommand claims a reviewable task for a code-reviewer agent.
// It selects the highest-priority READY_FOR_REVIEW task and transitions it to REVIEWING.
func ClaimReviewerTaskCommand(projectRoot, agentID string) error {
	result, err := ops.ClaimReviewerTask(ops.ClaimReviewerTaskInput{
		ProjectRoot: projectRoot,
		AgentID:     agentID,
	})
	if err != nil {
		return err
	}

	fmt.Printf("REVIEWING: %s by %s\n", result.TaskID, agentID)
	fmt.Printf("  worktree: %s\n", result.Worktree)
	fmt.Printf("  review_commit: %s\n", result.ReviewCommit)
	fmt.Printf("  lease_expires: %s\n", result.LeaseExpires.Format(time.RFC3339))
	return nil
}
