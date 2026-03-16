package agent

import (
	"errors"
	"fmt"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/ops"
)

// claimCoderTask finds and claims a claimable task.
// If the same coder previously initiated a handoff, it resumes that task first.
func claimCoderTask(projectRoot, agentID string, bb *db.Blackboard) (taskID, worktree string, err error) {
	logger := GetLogger()

	// First, try to resume a handoff task
	handoffResult, err := ops.ResumeHandoff(ops.ResumeHandoffInput{
		ProjectRoot: projectRoot,
		AgentID:     agentID,
	})
	if err != nil {
		return "", "", err
	}
	if handoffResult.Found {
		logger.Info("Resuming claimed task from handoff", "task_id", handoffResult.TaskID, "agent_id", agentID)
		return handoffResult.TaskID, handoffResult.Worktree, nil
	}

	state, err := bb.Read()
	if err != nil {
		return "", "", fmt.Errorf("failed to read state: %w", err)
	}

	// Check for tasks already IMPLEMENTING and assigned to this agent.
	// This handles re-invocation when the CLI exited without completing.
	for i := range state.Tasks {
		task := &state.Tasks[i]
		if task.Status == models.TaskStatusImplementing &&
			task.AssignedTo != nil &&
			*task.AssignedTo == agentID {
			wt := ""
			if task.Worktree != nil {
				wt = *task.Worktree
			}
			logger.Info("Re-claiming own in-progress task", "task_id", task.ID, "agent_id", agentID)
			return task.ID, wt, nil
		}
	}

	var candidates []*models.Task
	for i := range state.Tasks {
		if state.Tasks[i].IsClaimable(models.RoleCoder, state.Tasks) {
			candidates = append(candidates, &state.Tasks[i])
		}
	}
	task := selectHighestPriorityTask(candidates)

	if task == nil {
		return "", "", fmt.Errorf("no claimable tasks found")
	}

	result, err := ops.ClaimTask(projectRoot, task.ID, agentID)
	if err != nil {
		logger.Error("Claim error", "error", err)
		return "", "", err
	}

	return result.TaskID, result.WorktreeRel, nil
}

// selectHighestPriorityTask returns the highest-priority task from candidates,
// using creation time as FIFO tie-breaker. Returns nil if candidates is empty.
func selectHighestPriorityTask(candidates []*models.Task) *models.Task {
	var best *models.Task
	for _, t := range candidates {
		if best == nil || t.Priority < best.Priority {
			best = t
		} else if t.Priority == best.Priority && best.Created.After(t.Created) {
			best = t
		}
	}
	return best
}

// claimReviewerTask finds and claims a reviewable task.
// First checks for own REVIEWING tasks (session ended without verdict) and
// re-claims them. Otherwise delegates to ops.ClaimReviewerTask for new claims.
func claimReviewerTask(projectRoot, agentID string, leaseDuration int, bb *db.Blackboard) (taskID, worktree, reviewCommit string, err error) {
	logger := GetLogger()

	// Fix 42: Check for tasks already REVIEWING and assigned to this reviewer.
	// This handles re-invocation when the CLI session exited without submitting a verdict.
	state, err := bb.Read()
	if err != nil {
		return "", "", "", fmt.Errorf("failed to read state: %w", err)
	}
	for i := range state.Tasks {
		task := &state.Tasks[i]
		if task.Status == models.TaskStatusReviewing &&
			task.ReviewingBy != nil &&
			*task.ReviewingBy == agentID {
			wt := ""
			if task.Worktree != nil {
				wt = *task.Worktree
			}
			rc := ""
			if task.ReviewCommit != nil {
				rc = *task.ReviewCommit
			}
			logger.Info("Re-claiming own in-review task (session ended without verdict)",
				"task_id", task.ID, "agent_id", agentID)
			return task.ID, wt, rc, nil
		}
	}

	result, err := ops.ClaimReviewerTask(ops.ClaimReviewerTaskInput{
		ProjectRoot:   projectRoot,
		AgentID:       agentID,
		LeaseDuration: leaseDuration,
	})
	if err != nil {
		logger.Error("Review claim error", "error", err)
		return "", "", "", err
	}

	return result.TaskID, result.Worktree, result.ReviewCommit, nil
}

// handleApprovedMerges handles merging approved tasks
func handleApprovedMerges(projectRoot, agentID string, bb *db.Blackboard) error {
	logger := GetLogger()
	state, err := bb.Read()
	if err != nil {
		return err
	}

	// Find APPROVED tasks where approved_by = agentID.
	// Note: MergeCommit may be non-nil from a prior INTEGRATION_FAILED cycle;
	// Status == APPROVED guarantees the task hasn't been successfully merged.
	for i := range state.Tasks {
		task := &state.Tasks[i]
		if task.Status == models.TaskStatusApproved &&
			task.ApprovedBy != nil && *task.ApprovedBy == agentID {

			GetLogger().Info("Merging approved task", "task_id", task.ID)

			// Execute merge - ops.MergeWorktree handles all validation and state updates
			result, err := ops.MergeWorktree(projectRoot, task.ID, agentID)
			if err != nil {
				// Check if this is an integration failure (merge conflict or test failure)
				var integrationErr *ops.IntegrationFailedError
				if errors.As(err, &integrationErr) {
					// Integration failed - state already updated
					logArgs := []any{
						"task_id", task.ID,
						"reason", integrationErr.Reason,
					}
					if integrationErr.TestOutput != "" {
						logArgs = append(logArgs, "test_output", integrationErr.TestOutput)
					}
					if integrationErr.RollbackError != nil {
						logArgs = append(logArgs, "rollback_error", integrationErr.RollbackError)
					}
					logger.Warn("Integration failed", logArgs...)
					continue
				}
				// Other error - log and continue
				logger.Warn("Failed to merge task, will retry",
					"task_id", task.ID,
					"error", err)
				continue
			}

			// Log non-fatal warnings from cleanup
			for _, w := range result.Warnings {
				logger.Warn("Merge cleanup warning", "task_id", task.ID, "warning", w)
			}

			// Merge succeeded
			GetLogger().Info("Successfully merged task", "task_id", task.ID)
		}
	}

	return nil
}

// hasPendingMerges checks if there are APPROVED tasks awaiting merge by this agent
func hasPendingMerges(bb *db.Blackboard, agentID string) bool {
	state, err := bb.ReadCached()
	if err != nil {
		return false // Safe default: proceed to normal wait
	}

	for i := range state.Tasks {
		task := &state.Tasks[i]
		if task.Status == models.TaskStatusApproved &&
			task.ApprovedBy != nil && *task.ApprovedBy == agentID {
			return true
		}
	}
	return false
}

// logTaskSubmissionIfCompleted checks if a claimed task was submitted for review
// and logs this transition for visibility in agent logs.
// If the task is still IMPLEMENTING at exit, releases the claim so another coder
// can pick it up (the worktree is preserved for the next coder to resume from).
func logTaskSubmissionIfCompleted(bb *db.Blackboard, taskID, agentID string) error {
	state, err := bb.Read()
	if err != nil {
		return fmt.Errorf("failed to read state: %w", err)
	}

	// Find the task
	if task := state.FindTask(taskID); task != nil {
		// Check if it's now READY_FOR_REVIEW
		if task.Status == models.TaskStatusReadyForReview {
			// Log the successful submission
			reviewCommit := "unknown"
			if task.ReviewCommit != nil {
				reviewCommit = *task.ReviewCommit
			}

			GetLogger().Info("Task submitted for review",
				"task_id", task.ID,
				"review_commit", reviewCommit,
				"agent_id", agentID,
				"integration_fix", task.IntegrationFix)

			return nil
		}

		// If task is still IMPLEMENTING, release the claim so another coder
		// can pick it up. The worktree is preserved for the next coder.
		if task.Status == models.TaskStatusImplementing {
			GetLogger().Warn("Agent exited with task still IMPLEMENTING, releasing claim",
				"task_id", task.ID,
				"agent_id", agentID)

			releaseErr := bb.Modify(func(s *models.State) error {
				t := s.FindTask(taskID)
				if t == nil || t.Status != models.TaskStatusImplementing {
					return nil // task gone or status changed, nothing to do
				}
				if err := t.Transition(models.TaskStatusReady); err != nil {
					return err
				}
				t.AssignedTo = nil
				t.LeaseExpires = nil
				return nil
			})
			if releaseErr != nil {
				GetLogger().Warn("Failed to release stale coding claim on exit",
					"task_id", task.ID, "error", releaseErr)
			}
			return nil
		}

		// If task is BLOCKED, agent discovered a dependency issue
		if task.Status == models.TaskStatusBlocked {
			GetLogger().Info("Agent blocked task due to dependency issue",
				"task_id", task.ID,
				"agent_id", agentID)
			return nil
		}

		// Task exists but wasn't submitted (still in other status)
		// This is normal if agent exited for other reasons (context switch, failure, etc.)
		return nil
	}

	// Task not found - unusual but not an error
	return nil
}
