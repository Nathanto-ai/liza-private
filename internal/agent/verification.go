package agent

import (
	"context"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/git"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/verify"
)

// runPostSubmissionVerification runs deterministic verification commands
// after a coder submits work. If the task has VerifyCommands and is in
// READY_FOR_REVIEW status, the commands are executed in the task's worktree.
// Results are logged but do not block the workflow — the reviewer still
// decides the final verdict. Returns the verify.Result for observability
// event emission, or nil if verification was skipped.
func runPostSubmissionVerification(ctx context.Context, bb *db.Blackboard, projectRoot, taskID string) *verify.Result {
	state, err := bb.Read()
	if err != nil {
		GetLogger().Warn("Failed to read state for verification", "error", err, "task_id", taskID)
		return nil
	}

	task := state.FindTask(taskID)
	if task == nil {
		return nil
	}

	// Only verify tasks that reached READY_FOR_REVIEW
	if task.Status != models.TaskStatusReadyForReview {
		return nil
	}

	// Nothing to do without verify commands
	if len(task.VerifyCommands) == 0 {
		GetLogger().Info("Task has no verify_commands, skipping verification", "task_id", taskID)
		return nil
	}

	// Determine the worktree path for this task
	g := git.New(projectRoot)
	workdir := g.GetWorktreePath(taskID)

	GetLogger().Info("Running post-submission verification",
		"task_id", taskID,
		"commands", len(task.VerifyCommands),
		"workdir", workdir)

	cfg := verify.DefaultConfig()
	result := verify.RunVerification(ctx, task.VerifyCommands, workdir, cfg)

	if result.Passed {
		GetLogger().Info("Verification PASSED",
			"task_id", taskID,
			"commands_run", len(result.Results))
	} else {
		// Log each failed command for debugging
		for _, r := range result.Results {
			if r.ExitCode != 0 {
				GetLogger().Warn("Verification command FAILED",
					"task_id", taskID,
					"command", r.Command,
					"exit_code", r.ExitCode,
					"output", truncateOutput(r.Output, 500))
			}
		}
		GetLogger().Warn("Verification FAILED — reviewer should check verify_commands output",
			"task_id", taskID,
			"commands_total", len(task.VerifyCommands),
			"commands_run", len(result.Results))
	}
	return result
}

// truncateOutput truncates a string to maxLen characters, adding an ellipsis if truncated.
func truncateOutput(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
