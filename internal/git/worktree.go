// Package git provides git worktree operations for task isolation
package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/liza-mas/liza/internal/paths"
)

// RefConflictError indicates a compare-and-swap failure on git update-ref.
// The ref's current value did not match the expected old SHA.
type RefConflictError struct {
	Ref      string
	Expected string
	Actual   string // may be empty if undetermined
}

func (e *RefConflictError) Error() string {
	return fmt.Sprintf("ref conflict on %s: expected %s, got %s", e.Ref, e.Expected, e.Actual)
}

// Git provides git operations for worktree management
type Git struct {
	projectRoot string
}

// Worktree represents a git worktree entry
type Worktree struct {
	Path   string
	Commit string
	Branch string
}

// New creates a new Git instance for the given project root
func New(projectRoot string) *Git {
	return &Git{
		projectRoot: projectRoot,
	}
}

// exec runs a git command in the project root and returns output
func (g *Git) exec(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.projectRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v failed: %w\nOutput: %s", args, err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

// execInDir runs a git command in a specific directory
func (g *Git) execInDir(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v failed: %w\nOutput: %s", args, err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

// GetCurrentBranch returns the name of the current branch
func (g *Git) GetCurrentBranch() (string, error) {
	return g.exec("branch", "--show-current")
}

// GetCommitSHA returns the commit SHA for a given ref
// If short is true, returns 7-character short SHA
func (g *Git) GetCommitSHA(ref string, short ...bool) (string, error) {
	useShort := len(short) > 0 && short[0]

	if useShort {
		return g.exec("rev-parse", "--short", ref)
	}
	return g.exec("rev-parse", ref)
}

// BranchExists checks if a branch exists
func (g *Git) BranchExists(branch string) (bool, error) {
	_, err := g.exec("rev-parse", "--verify", "refs/heads/"+branch)
	if err != nil {
		// Check if error is because branch doesn't exist
		errMsg := err.Error()
		if strings.Contains(errMsg, "unknown revision") ||
			strings.Contains(errMsg, "not a valid ref") ||
			strings.Contains(errMsg, "Needed a single revision") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// CreateBranch creates a new branch from the current HEAD
func (g *Git) CreateBranch(branch string) error {
	_, err := g.exec("branch", branch)
	return err
}

// DeleteBranch deletes a branch (force delete with -D)
func (g *Git) DeleteBranch(branch string) error {
	_, err := g.exec("branch", "-D", branch)
	return err
}

// CheckoutBranch checks out a branch
func (g *Git) CheckoutBranch(branch string) error {
	_, err := g.exec("checkout", branch)
	return err
}

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

// CreateWorktreeFresh creates a worktree, deleting any existing one first
// This is used for task reassignment scenarios
func (g *Git) CreateWorktreeFresh(taskID, fromBranch string) (string, error) {
	// Remove existing worktree if it exists (ignore errors)
	_ = g.RemoveWorktree(taskID)

	// Create new worktree
	return g.CreateWorktree(taskID, fromBranch)
}

// RemoveWorktree removes a worktree and its associated branch
func (g *Git) RemoveWorktree(taskID string) error {
	if err := paths.ValidateTaskID(taskID); err != nil {
		return fmt.Errorf("invalid task ID: %w", err)
	}
	worktreeRel := filepath.Join(paths.WorktreesDirName, taskID)
	worktreePath := filepath.Join(g.projectRoot, worktreeRel)
	branchName := paths.TaskBranchPrefix + taskID

	// Check if worktree exists
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		// Worktree doesn't exist, clean up branch if it exists
		exists, err := g.BranchExists(branchName)
		if err != nil {
			return err
		}
		if exists {
			return g.DeleteBranch(branchName)
		}
		return nil
	}

	// Remove worktree (force to handle any uncommitted changes)
	_, err := g.exec("worktree", "remove", "--force", worktreePath)
	if err != nil {
		// If git worktree remove fails, try manual cleanup
		if err := os.RemoveAll(worktreePath); err != nil {
			return fmt.Errorf("failed to remove worktree directory: %w", err)
		}
		// Clean up git's internal worktree tracking (.git/worktrees/<name>)
		// to prevent future "worktree add" from failing on dangling entries
		_, _ = g.exec("worktree", "prune")
	}

	// Delete the branch (may not exist, so ignore errors)
	_ = g.DeleteBranch(branchName)

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

// CalculateDrift returns the number of commits between baseCommit and targetBranch
// This indicates how far the integration branch has moved since the task started
func (g *Git) CalculateDrift(baseCommit, targetBranch string) (int, error) {
	// git rev-list --count base..target
	output, err := g.exec("rev-list", "--count", baseCommit+".."+targetBranch)
	if err != nil {
		return 0, fmt.Errorf("failed to calculate drift: %w", err)
	}

	count, err := strconv.Atoi(output)
	if err != nil {
		return 0, fmt.Errorf("failed to parse commit count: %w", err)
	}

	return count, nil
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

// GetWorktreeHEAD returns the commit SHA of the worktree's HEAD
func (g *Git) GetWorktreeHEAD(taskID string) (string, error) {
	wtPath := g.GetWorktreePath(taskID)
	return g.execInDir(wtPath, "rev-parse", "HEAD")
}

// MergeBranch attempts to merge a branch into the current branch
// Returns true if fast-forward was possible, false if merge commit was created
// Returns error if merge fails (e.g., conflicts)
func (g *Git) MergeBranch(branch string) (fastForward bool, mergeCommit string, err error) {
	// Try fast-forward first
	_, err = g.exec("merge", "--ff-only", branch)
	if err == nil {
		// Fast-forward succeeded
		commit, err := g.GetCommitSHA("HEAD")
		return true, commit, err
	}

	// Fast-forward failed, try regular merge
	var output string
	output, err = g.exec("merge", "--no-ff", "-m", "Merge "+branch, branch)
	if err != nil {
		// Check if it's a merge conflict
		if strings.Contains(output, "CONFLICT") || strings.Contains(err.Error(), "conflict") {
			return false, "", fmt.Errorf("merge conflict: %w", err)
		}
		return false, "", err
	}

	// Get the merge commit
	commit, err := g.GetCommitSHA("HEAD")
	return false, commit, err
}

// AbortMerge aborts an in-progress merge
func (g *Git) AbortMerge() error {
	_, err := g.exec("merge", "--abort")
	return err
}

// ResetHard resets the current branch to the given commit, discarding all changes.
func (g *Git) ResetHard(ref string) error {
	_, err := g.exec("reset", "--hard", ref)
	return err
}

// ResetHardInWorktree resets a worktree branch to the given ref, discarding all changes.
func (g *Git) ResetHardInWorktree(wtPath string, ref string) error {
	_, err := g.execInDir(wtPath, "reset", "--hard", ref)
	return err
}

// FetchFromLocal fetches latest commits for a branch from the project root
// Used in worktrees to sync with integration branch
func (g *Git) FetchFromLocal(wtPath string, branch string) error {
	_, err := g.execInDir(wtPath, "fetch", g.projectRoot, branch)
	if err != nil {
		return fmt.Errorf("failed to fetch branch %s from project root: %w", branch, err)
	}
	return nil
}

// RebaseOnto rebases the current branch in a worktree onto the specified base branch
// Must be called from within a worktree context
// Returns error if rebase conflicts occur
func (g *Git) RebaseOnto(wtPath string, baseBranch string) error {
	output, err := g.execInDir(wtPath, "rebase", baseBranch)
	if err != nil {
		// Check if it's a rebase conflict
		if strings.Contains(string(output), "CONFLICT") ||
			strings.Contains(string(output), "could not apply") ||
			strings.Contains(err.Error(), "conflict") {
			return fmt.Errorf("rebase conflict: %w", err)
		}
		return fmt.Errorf("rebase failed: %w", err)
	}
	return nil
}

// AbortRebase aborts an in-progress rebase in a worktree
func (g *Git) AbortRebase(wtPath string) error {
	_, err := g.execInDir(wtPath, "rebase", "--abort")
	if err != nil {
		return fmt.Errorf("failed to abort rebase: %w", err)
	}
	return nil
}

// CherryPick applies a single commit onto the current branch in a worktree.
func (g *Git) CherryPick(wtPath string, commitSHA string) error {
	_, err := g.execInDir(wtPath, "cherry-pick", commitSHA)
	if err != nil {
		return fmt.Errorf("cherry-pick %s failed: %w", commitSHA, err)
	}
	return nil
}

// GetWorktreeBranch returns the current branch name in a worktree
func (g *Git) GetWorktreeBranch(wtPath string) (string, error) {
	branch, err := g.execInDir(wtPath, "branch", "--show-current")
	if err != nil {
		return "", fmt.Errorf("failed to get worktree branch: %w", err)
	}
	return branch, nil
}

// MergeTree computes a merge between two commits without touching the working tree.
// Returns the tree SHA of the merge result, a boolean indicating if it's clean (no conflicts),
// and any error. If not clean, the tree SHA contains the best-effort merge with conflict markers.
func (g *Git) MergeTree(baseCommit, branchCommit string) (treeSHA string, clean bool, err error) {
	// Use git merge-tree --write-tree which returns the tree SHA directly
	// Exit code 0 = clean merge, 1 = has conflicts
	output, err := g.exec("merge-tree", "--write-tree", "--no-messages", baseCommit, branchCommit)
	if err != nil {
		// Check if it's a conflict (exit code 1)
		errStr := err.Error()
		if strings.Contains(errStr, "exit status 1") {
			// Conflicts exist - the output still contains the tree SHA
			// with best-effort merge (with conflict markers)
			return output, false, nil
		}
		return "", false, fmt.Errorf("merge-tree failed: %w", err)
	}
	return output, true, nil
}

// CreateCommitFromTree creates a commit from a tree SHA with given parents and message.
// Returns the new commit SHA. This does not touch the working tree.
func (g *Git) CreateCommitFromTree(treeSHA string, parents []string, message string) (string, error) {
	args := []string{"commit-tree", treeSHA, "-m", message}
	for _, parent := range parents {
		args = append(args, "-p", parent)
	}
	output, err := g.exec(args...)
	if err != nil {
		return "", fmt.Errorf("commit-tree failed: %w", err)
	}
	return output, nil
}

// UpdateRef updates a ref to point to a new commit.
// When expectedOldSHA is non-empty, this is a compare-and-swap (CAS) operation:
// git update-ref <ref> <new> <old>. If the ref's current value doesn't match
// expectedOldSHA, git exits with status 128 and a RefConflictError is returned.
func (g *Git) UpdateRef(ref, commitSHA, expectedOldSHA string) error {
	args := []string{"update-ref", ref, commitSHA}
	if expectedOldSHA != "" {
		args = append(args, expectedOldSHA)
	}
	_, err := g.exec(args...)
	if err != nil {
		if expectedOldSHA != "" {
			errMsg := err.Error()
			// git update-ref CAS mismatch: exit status 128,
			// output contains "is at <actual> but expected <old>"
			if strings.Contains(errMsg, "but expected") {
				actual := extractActualSHA(errMsg)
				return &RefConflictError{Ref: ref, Expected: expectedOldSHA, Actual: actual}
			}
		}
		return fmt.Errorf("update-ref %s failed: %w", ref, err)
	}
	return nil
}

// extractActualSHA parses the actual SHA from a git update-ref CAS error message.
// Format: "...is at <sha> but expected <sha>"
func extractActualSHA(errMsg string) string {
	const marker = "is at "
	idx := strings.Index(errMsg, marker)
	if idx == -1 {
		return ""
	}
	rest := errMsg[idx+len(marker):]
	if spaceIdx := strings.IndexByte(rest, ' '); spaceIdx > 0 {
		return rest[:spaceIdx]
	}
	return ""
}

// DiffFiles returns the list of files changed between two commits in a directory.
// Typically used as DiffFiles(wtPath, baseCommit, "HEAD") to get files changed in a worktree.
func (g *Git) DiffFiles(dir, commitA, commitB string) ([]string, error) {
	output, err := g.execInDir(dir, "diff", "--name-only", commitA+".."+commitB)
	if err != nil {
		return nil, err
	}
	if output == "" {
		return nil, nil
	}
	return strings.Split(output, "\n"), nil
}

// IsAncestor checks if commitA is an ancestor of commitB
func (g *Git) IsAncestor(commitA, commitB string) (bool, error) {
	_, err := g.exec("merge-base", "--is-ancestor", commitA, commitB)
	if err != nil {
		// Check if error is "not an ancestor" (exit code 1)
		if strings.Contains(err.Error(), "exit status 1") {
			return false, nil
		}
		return false, fmt.Errorf("merge-base failed: %w", err)
	}
	return true, nil
}
