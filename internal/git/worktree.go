package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/liza-mas/liza/internal/paths"
)

// CreateWorktree creates a worktree for the given task from the specified branch.
// Returns the base commit (full SHA) for drift tracking.
func (g *Git) CreateWorktree(taskID, fromBranch string) (string, error) {
	if err := paths.ValidateTaskID(taskID); err != nil {
		return "", fmt.Errorf("invalid task ID: %w", err)
	}
	worktreeRel := filepath.Join(paths.WorktreesDirName, taskID)
	worktreePath := filepath.Join(g.projectRoot, worktreeRel)
	branchName := paths.TaskBranchPrefix + taskID

	// Check if worktree already exists
	if _, err := os.Stat(worktreePath); err == nil {
		return "", fmt.Errorf("worktree already exists: %s", worktreePath)
	}

	// Ensure .worktrees directory exists
	worktreesDir := filepath.Join(g.projectRoot, paths.WorktreesDirName)
	if err := os.MkdirAll(worktreesDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create .worktrees directory: %w", err)
	}

	// Get base commit before creating worktree
	baseCommit, err := g.GetCommitSHA(fromBranch)
	if err != nil {
		return "", fmt.Errorf("failed to get base commit: %w", err)
	}

	// Create worktree with new branch
	_, err = g.exec("worktree", "add", worktreePath, fromBranch, "-b", branchName)
	if err != nil {
		return "", fmt.Errorf("failed to create worktree: %w", err)
	}

	return baseCommit, nil
}

// AttachWorktree creates a worktree for an already-existing branch.
// Unlike CreateWorktree, this does not create a new branch with -b.
func (g *Git) AttachWorktree(taskID, existingBranch string) error {
	if err := paths.ValidateTaskID(taskID); err != nil {
		return fmt.Errorf("invalid task ID: %w", err)
	}
	worktreePath := filepath.Join(g.projectRoot, paths.WorktreesDirName, taskID)

	if _, err := os.Stat(worktreePath); err == nil {
		return fmt.Errorf("worktree already exists: %s", worktreePath)
	}

	worktreesDir := filepath.Join(g.projectRoot, paths.WorktreesDirName)
	if err := os.MkdirAll(worktreesDir, 0755); err != nil {
		return fmt.Errorf("failed to create .worktrees directory: %w", err)
	}

	_, err := g.exec("worktree", "add", worktreePath, existingBranch)
	if err != nil {
		return fmt.Errorf("failed to attach worktree: %w", err)
	}

	return nil
}

// CreateWorktreeFresh creates a worktree, deleting any existing one first
// This is used for task reassignment scenarios
func (g *Git) CreateWorktreeFresh(taskID, fromBranch string) (string, error) {
	// Remove existing worktree if it exists (ignore errors)
	_ = g.RemoveWorktree(taskID)

	// Create new worktree
	return g.CreateWorktree(taskID, fromBranch)
}

// RemoveWorktreeDir removes a worktree directory and git metadata without
// deleting the associated branch. Use this when the branch must survive
// (e.g. superseded tasks whose branch successors still need).
func (g *Git) RemoveWorktreeDir(taskID string) error {
	if err := paths.ValidateTaskID(taskID); err != nil {
		return fmt.Errorf("invalid task ID: %w", err)
	}
	worktreePath := filepath.Join(g.projectRoot, paths.WorktreesDirName, taskID)

	// Nothing to do if worktree directory doesn't exist
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		return nil
	}

	// Remove worktree (force to handle any uncommitted changes)
	_, err := g.exec("worktree", "remove", "--force", worktreePath)
	if err != nil {
		// If git worktree remove fails, try manual cleanup
		if err := os.RemoveAll(worktreePath); err != nil {
			return fmt.Errorf("failed to remove worktree directory: %w", err)
		}
		// Clean up git's internal worktree tracking for this specific task.
		// Targeted removal instead of global "git worktree prune" to prevent
		// interference with concurrent worktree operations (global prune can
		// corrupt in-flight "git worktree add" for other tasks).
		metadataDir := filepath.Join(g.projectRoot, ".git", "worktrees", taskID)
		_ = os.RemoveAll(metadataDir)
	}

	return nil
}

// RemoveWorktree removes a worktree and its associated branch.
func (g *Git) RemoveWorktree(taskID string) error {
	if err := g.RemoveWorktreeDir(taskID); err != nil {
		return err
	}

	branchName := paths.TaskBranchPrefix + taskID
	exists, err := g.BranchExists(branchName)
	if err != nil {
		return err
	}
	if exists {
		return g.DeleteBranch(branchName)
	}
	return nil
}

// ListWorktrees returns a list of all worktrees
func (g *Git) ListWorktrees() ([]Worktree, error) {
	// git worktree list --porcelain gives us machine-readable output
	output, err := g.exec("worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("failed to list worktrees: %w", err)
	}

	var worktrees []Worktree
	var current Worktree

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			// Empty line separates worktree entries
			if current.Path != "" {
				worktrees = append(worktrees, current)
				current = Worktree{}
			}
			continue
		}

		if strings.HasPrefix(line, "worktree ") {
			current.Path = strings.TrimPrefix(line, "worktree ")
		} else if strings.HasPrefix(line, "HEAD ") {
			current.Commit = strings.TrimPrefix(line, "HEAD ")
		} else if strings.HasPrefix(line, "branch ") {
			branchRef := strings.TrimPrefix(line, "branch ")
			// Extract branch name from refs/heads/...
			current.Branch = strings.TrimPrefix(branchRef, "refs/heads/")
		}
	}

	// Add the last entry if present
	if current.Path != "" {
		worktrees = append(worktrees, current)
	}

	return worktrees, nil
}

// GetWorktreePath returns the absolute path for a task's worktree
// Callers must validate taskID before calling this — see paths.ValidateTaskID.
func (g *Git) GetWorktreePath(taskID string) string {
	return filepath.Join(g.projectRoot, paths.WorktreesDirName, taskID)
}

// GetWorktreeRelPath returns the relative path for a task's worktree
func (g *Git) GetWorktreeRelPath(taskID string) string {
	return filepath.Join(paths.WorktreesDirName, taskID)
}

// ValidateWorktreeHealth checks that a worktree directory and its .git link
// file both exist and are accessible. A worktree without its .git file is an
// orphan that git cannot operate on — this can happen when concurrent
// RemoveWorktree operations interfere with in-flight worktree creation.
// Callers must validate taskID before calling this — see paths.ValidateTaskID.
func (g *Git) ValidateWorktreeHealth(taskID string) error {
	worktreePath := g.GetWorktreePath(taskID)

	if _, err := os.Stat(worktreePath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("worktree directory missing: %s", worktreePath)
		}
		return fmt.Errorf("worktree directory inaccessible: %w", err)
	}

	gitFile := filepath.Join(worktreePath, ".git")
	if _, err := os.Stat(gitFile); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("worktree .git link file missing: %s (orphaned worktree)", gitFile)
		}
		return fmt.Errorf("worktree .git link file inaccessible: %w", err)
	}

	return nil
}

// MergeIntoBranch merges the task branch into a target branch.
// Returns (hadConflicts bool, mergeCommit string, err error)
func (g *Git) MergeIntoBranch(taskID, targetBranch string) (bool, string, error) {
	branchName := paths.TaskBranchPrefix + taskID

	// Merge the task branch
	_, err := g.exec("merge", "--no-ff", "-m", fmt.Sprintf("Merge %s into %s", branchName, targetBranch), branchName)
	if err != nil {
		// Check if it's a merge conflict
		errStr := err.Error()
		if strings.Contains(errStr, "CONFLICT") || strings.Contains(errStr, "Automatic merge failed") {
			return true, "", nil
		}
		return false, "", fmt.Errorf("merge failed: %w", err)
	}

	// Get the merge commit
	commit, err := g.GetCommitSHA("HEAD")
	return false, commit, err
}

// AbortMerge aborts an in-progress merge
// ResetHardInWorktree resets a worktree branch to the given ref, discarding all changes.
func (g *Git) ResetHardInWorktree(wtPath string, ref string) error {
	_, err := g.execInDir(wtPath, "reset", "--hard", ref)
	return err
}

// CherryPick applies a single commit onto the current branch in a worktree.
func (g *Git) CherryPick(wtPath string, commitSHA string) error {
	_, err := g.execInDir(wtPath, "cherry-pick", commitSHA)
	if err != nil {
		return fmt.Errorf("cherry-pick %s failed: %w", commitSHA, err)
	}
	return nil
}


