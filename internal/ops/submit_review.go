package ops

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/errors"
	"github.com/liza-mas/liza/internal/git"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
)

// SubmitForReviewResult contains the outcome of submitting a task for review.
type SubmitForReviewResult struct {
	TaskID       string
	ReviewCommit string
	AgentID      string
}

// SubmitForReview validates that commitSHA matches the worktree HEAD before rebase,
// rebases the task branch onto the integration branch to catch conflicts early,
// then atomically transitions the task to READY_FOR_REVIEW.
// No terminal I/O.
func SubmitForReview(projectRoot, taskID, commitSHA, agentID string) (*SubmitForReviewResult, error) {
	if taskID == "" {
		return nil, fmt.Errorf("task ID is required")
	}
	if commitSHA == "" {
		return nil, fmt.Errorf("commit SHA is required")
	}
	if agentID == "" {
		return nil, fmt.Errorf("LIZA_AGENT_ID is required")
	}

	lp := paths.New(projectRoot)
	bb := db.For(lp.StatePath())

	// Phase 1: Read state to get config and validate preconditions
	state, task, err := readTaskState(bb, taskID)
	if err != nil {
		return nil, err
	}

	if task.Status == models.TaskStatusSuperseded {
		replacements := "unknown"
		if len(task.SupersededBy) > 0 {
			replacements = strings.Join(task.SupersededBy, ", ")
		}
		return nil, &PreconditionError{
			Reason: fmt.Sprintf("task %s was SUPERSEDED (replaced by: %s) — stop work on this task and exit with code 0", taskID, replacements),
		}
	}

	if task.Status != models.TaskStatusImplementing {
		return nil, fmt.Errorf("task %s is not IMPLEMENTING (current status: %s)", taskID, task.Status)
	}

	if task.AssignedTo == nil || *task.AssignedTo != agentID {
		currentAgent := "none"
		if task.AssignedTo != nil {
			currentAgent = *task.AssignedTo
		}
		return nil, fmt.Errorf("task %s is not assigned to agent %s (currently assigned to: %s)", taskID, agentID, currentAgent)
	}

	if task.Worktree == nil {
		return nil, fmt.Errorf("task %s has no worktree", taskID)
	}

	// Pre-execution checkpoint required before submission
	if !HasCheckpoint(task.History, agentID) {
		return nil, &PreconditionError{Reason: fmt.Sprintf("task %s: pre-execution checkpoint required before submission (use liza_write_checkpoint)", taskID)}
	}

	// Phase 2: Execute git operations outside the lock
	g := git.New(projectRoot)
	wtPath := g.GetWorktreePath(taskID)

	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("worktree directory does not exist: %s", wtPath)
	}

	wtBranch, err := g.GetWorktreeBranch(wtPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree branch: %w", err)
	}

	expectedBranch := paths.TaskBranchPrefix + taskID
	if wtBranch != expectedBranch {
		if wtBranch == "" {
			return nil, fmt.Errorf("worktree is in detached HEAD state (expected branch: %s)", expectedBranch)
		}
		return nil, fmt.Errorf("worktree is on branch %s (expected: %s)", wtBranch, expectedBranch)
	}

	preRebaseCommit, err := g.GetWorktreeHEAD(taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to get pre-rebase commit SHA: %w", err)
	}
	// Accept both full SHAs and short prefixes (minimum 7 chars)
	if !strings.HasPrefix(preRebaseCommit, commitSHA) {
		return nil, fmt.Errorf("provided commit SHA %s does not match worktree HEAD %s", commitSHA, preRebaseCommit)
	}
	// Normalize to full SHA for downstream storage
	commitSHA = preRebaseCommit

	// TDD enforcement: code tasks must include test files
	if task.EffectiveType() == models.TaskTypeCoding && task.BaseCommit != nil {
		hasTests, err := HasTestFiles(g, taskID, *task.BaseCommit)
		if err != nil {
			return nil, fmt.Errorf("failed to check test files: %w", err)
		}
		if !hasTests && GetTDDWaiver(task.History, agentID) == "" {
			return nil, &PreconditionError{Reason: fmt.Sprintf("task %s: code tasks must include test files (e.g. *_test.go, *.test.ts, test_*.py) — TDD is mandatory", taskID)}
		}
	}

	integrationBranch := state.Config.IntegrationBranch
	if err := g.FetchFromLocal(wtPath, integrationBranch); err != nil {
		return nil, fmt.Errorf("failed to fetch integration branch: %w", err)
	}

	if err := g.RebaseOnto(wtPath, "FETCH_HEAD"); err != nil {
		return nil, fmt.Errorf(`failed to submit task for review: rebase conflict detected

Your task branch has conflicts with the latest integration branch.

Worktree location: %s

To resolve:
  1. cd %s
  2. git status (see conflicting files)
  3. Edit files to resolve conflict markers
  4. git add <resolved-files>
  5. git rebase --continue
  6. COMMIT=$(git -C %s rev-parse HEAD)
  7. Return to project root and retry: liza submit-for-review %s $COMMIT

Alternatively, abort the rebase and ask for help:
  git rebase --abort`, wtPath, wtPath, wtPath, taskID)
	}

	postRebaseCommit, err := g.GetWorktreeHEAD(taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to get post-rebase commit SHA: %w", err)
	}

	// Phase 3: Atomic update with new commit SHA
	now := time.Now().UTC()

	err = bb.Modify(func(state *models.State) error {
		task := state.FindTask(taskID)
		if task == nil {
			return &errors.NotFoundError{Entity: "task", ID: taskID}
		}

		if task.Status != models.TaskStatusImplementing {
			return fmt.Errorf("task %s is not IMPLEMENTING (current status: %s)", taskID, task.Status)
		}

		if task.AssignedTo == nil || *task.AssignedTo != agentID {
			currentAgent := "none"
			if task.AssignedTo != nil {
				currentAgent = *task.AssignedTo
			}
			return fmt.Errorf("task %s is not assigned to agent %s (currently assigned to: %s)", taskID, agentID, currentAgent)
		}

		if err := task.Transition(models.TaskStatusReadyForReview); err != nil {
			return err
		}
		task.ReviewCommit = &postRebaseCommit

		agentPtr := &agentID
		task.History = append(task.History, models.TaskHistoryEntry{
			Time:  now,
			Event: "submitted_for_review",
			Agent: agentPtr,
		})

		if agent, ok := state.Agents[agentID]; ok {
			agent.Status = models.AgentStatusWaiting
			agent.CurrentTask = nil
			agent.LeaseExpires = nil
			state.Agents[agentID] = agent
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to submit task for review: %w", err)
	}

	return &SubmitForReviewResult{
		TaskID:       taskID,
		ReviewCommit: postRebaseCommit,
		AgentID:      agentID,
	}, nil
}
