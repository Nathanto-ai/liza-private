package git

import (
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liza-mas/liza/internal/testhelpers"
)

func TestFetchFromLocal_Success(t *testing.T) {
	repoDir := setupTestRepo(t)
	git := New(repoDir)

	// Create a worktree
	taskID := "task-fetch-test"
	if _, err := git.CreateWorktree(taskID, "integration"); err != nil {
		t.Fatalf("CreateWorktree() error = %v", err)
	}
	wtPath := git.GetWorktreePath(taskID)

	// Add a commit to integration in project root
	testFile := filepath.Join(repoDir, "new-file.txt")
	if err := os.WriteFile(testFile, []byte("new content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, repoDir, "add", "new-file.txt")
	testhelpers.MustGit(t, repoDir, "commit", "-m", "New commit on integration")

	// Fetch integration from project root into worktree
	err := git.FetchFromLocal(wtPath, "integration")
	if err != nil {
		t.Fatalf("FetchFromLocal() error = %v", err)
	}

	// Verify FETCH_HEAD exists in worktree
	// In a git worktree, .git is a file (not a directory), so we must
	// use rev-parse --git-dir to find the actual git directory.
	gitDirOut := testhelpers.MustGit(t, wtPath, "rev-parse", "--git-dir")
	fetchHeadPath := filepath.Join(strings.TrimSpace(gitDirOut), "FETCH_HEAD")
	if _, err := os.Stat(fetchHeadPath); os.IsNotExist(err) {
		t.Error("FETCH_HEAD was not created after fetch")
	}
}

func TestFetchFromLocal_InvalidBranch(t *testing.T) {
	repoDir := setupTestRepo(t)
	git := New(repoDir)

	// Create a worktree
	taskID := "task-invalid-fetch"
	if _, err := git.CreateWorktree(taskID, "integration"); err != nil {
		t.Fatalf("CreateWorktree() error = %v", err)
	}
	wtPath := git.GetWorktreePath(taskID)

	// Try to fetch a non-existent branch
	err := git.FetchFromLocal(wtPath, "nonexistent-branch")
	if err == nil {
		t.Error("Expected error when fetching non-existent branch, got nil")
	}
}

func TestRebaseOnto_Success(t *testing.T) {
	repoDir := setupTestRepo(t)
	git := New(repoDir)

	// Create a worktree from integration
	taskID := "task-rebase-success"
	if _, err := git.CreateWorktree(taskID, "integration"); err != nil {
		t.Fatalf("CreateWorktree() error = %v", err)
	}
	wtPath := git.GetWorktreePath(taskID)

	// Make a commit in the worktree
	wtFile := filepath.Join(wtPath, "worktree-file.txt")
	if err := os.WriteFile(wtFile, []byte("worktree change\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, wtPath, "add", "worktree-file.txt")
	testhelpers.MustGit(t, wtPath, "commit", "-m", "Worktree commit")

	// Make a commit on integration (in project root)
	integrationFile := filepath.Join(repoDir, "integration-file.txt")
	if err := os.WriteFile(integrationFile, []byte("integration change\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, repoDir, "add", "integration-file.txt")
	testhelpers.MustGit(t, repoDir, "commit", "-m", "Integration commit")

	// Fetch latest integration into worktree
	if err := git.FetchFromLocal(wtPath, "integration"); err != nil {
		t.Fatalf("FetchFromLocal() error = %v", err)
	}

	// Rebase worktree onto FETCH_HEAD
	err := git.RebaseOnto(wtPath, "FETCH_HEAD")
	if err != nil {
		t.Fatalf("RebaseOnto() error = %v", err)
	}

	// Verify both files exist in worktree
	if _, err := os.Stat(wtFile); os.IsNotExist(err) {
		t.Error("Worktree file should exist after rebase")
	}
	if _, err := os.Stat(filepath.Join(wtPath, "integration-file.txt")); os.IsNotExist(err) {
		t.Error("Integration file should exist in worktree after rebase")
	}
}

func TestRebaseOnto_Conflict(t *testing.T) {
	repoDir := setupTestRepo(t)
	git := New(repoDir)

	// Create a worktree from integration
	taskID := "task-rebase-conflict"
	if _, err := git.CreateWorktree(taskID, "integration"); err != nil {
		t.Fatalf("CreateWorktree() error = %v", err)
	}
	wtPath := git.GetWorktreePath(taskID)

	// Modify README in worktree
	readmeFile := filepath.Join(wtPath, "README.md")
	if err := os.WriteFile(readmeFile, []byte("# Worktree version\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, wtPath, "add", "README.md")
	testhelpers.MustGit(t, wtPath, "commit", "-m", "Worktree README")

	// Modify README differently in integration (project root)
	readmeRoot := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readmeRoot, []byte("# Integration version\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, repoDir, "add", "README.md")
	testhelpers.MustGit(t, repoDir, "commit", "-m", "Integration README")

	// Fetch latest integration into worktree
	if err := git.FetchFromLocal(wtPath, "integration"); err != nil {
		t.Fatalf("FetchFromLocal() error = %v", err)
	}

	// Try to rebase - should detect conflict and return typed error
	err := git.RebaseOnto(wtPath, "FETCH_HEAD")
	if err == nil {
		t.Fatal("Expected rebase conflict error, got nil")
	}

	var rebaseConflict *RebaseConflictError
	if !stderrors.As(err, &rebaseConflict) {
		t.Fatalf("Expected *RebaseConflictError, got %T: %v", err, err)
	}
	if rebaseConflict.Output == "" {
		t.Error("Expected non-empty Output in RebaseConflictError")
	}

	// Verify rebase left repository in conflict state
	status := testhelpers.MustGit(t, wtPath, "status")
	if !strings.Contains(status, "rebase") {
		t.Error("Expected worktree to be in rebase state after conflict")
	}
}

func TestAbortRebase_Success(t *testing.T) {
	repoDir := setupTestRepo(t)
	git := New(repoDir)

	// Create a worktree and trigger a conflict (same as TestRebaseOnto_Conflict)
	taskID := "task-abort-rebase"
	if _, err := git.CreateWorktree(taskID, "integration"); err != nil {
		t.Fatalf("CreateWorktree() error = %v", err)
	}
	wtPath := git.GetWorktreePath(taskID)

	// Modify README in worktree
	readmeFile := filepath.Join(wtPath, "README.md")
	if err := os.WriteFile(readmeFile, []byte("# Worktree version\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, wtPath, "add", "README.md")
	testhelpers.MustGit(t, wtPath, "commit", "-m", "Worktree README")

	// Modify README differently in integration
	readmeRoot := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readmeRoot, []byte("# Integration version\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, repoDir, "add", "README.md")
	testhelpers.MustGit(t, repoDir, "commit", "-m", "Integration README")

	// Fetch and attempt rebase (will conflict)
	if err := git.FetchFromLocal(wtPath, "integration"); err != nil {
		t.Fatalf("FetchFromLocal() error = %v", err)
	}
	_ = git.RebaseOnto(wtPath, "FETCH_HEAD") // Expected to fail

	// Abort the rebase
	err := git.AbortRebase(wtPath)
	if err != nil {
		t.Fatalf("AbortRebase() error = %v", err)
	}

	// Verify clean state
	status := testhelpers.MustGit(t, wtPath, "status", "--porcelain")
	if status != "" {
		t.Error("Expected clean state after rebase abort")
	}
}

func TestRebaseOnto_AlreadyInProgress(t *testing.T) {
	repoDir := setupTestRepo(t)
	git := New(repoDir)

	// Create a worktree and trigger a conflict
	taskID := "task-rebase-in-progress"
	if _, err := git.CreateWorktree(taskID, "integration"); err != nil {
		t.Fatalf("CreateWorktree() error = %v", err)
	}
	wtPath := git.GetWorktreePath(taskID)

	// Modify README in worktree
	readmeFile := filepath.Join(wtPath, "README.md")
	if err := os.WriteFile(readmeFile, []byte("# Worktree version\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, wtPath, "add", "README.md")
	testhelpers.MustGit(t, wtPath, "commit", "-m", "Worktree README")

	// Modify README differently in integration
	readmeRoot := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readmeRoot, []byte("# Integration version\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, repoDir, "add", "README.md")
	testhelpers.MustGit(t, repoDir, "commit", "-m", "Integration README")

	// Fetch and attempt rebase (will conflict)
	if err := git.FetchFromLocal(wtPath, "integration"); err != nil {
		t.Fatalf("FetchFromLocal() error = %v", err)
	}
	_ = git.RebaseOnto(wtPath, "FETCH_HEAD") // Expected to fail

	// Try another rebase while one is in progress — this is a generic failure,
	// NOT a merge conflict, so it must NOT return *RebaseConflictError.
	err := git.RebaseOnto(wtPath, "FETCH_HEAD")
	if err == nil {
		t.Fatal("Expected error when rebase already in progress, got nil")
	}

	var rebaseConflict *RebaseConflictError
	if stderrors.As(err, &rebaseConflict) {
		t.Errorf("Expected generic rebase error, got *RebaseConflictError: %v", err)
	}
	if !strings.Contains(err.Error(), "rebase failed") {
		t.Errorf("Expected wrapped 'rebase failed' error, got: %v", err)
	}

	// Clean up
	_ = git.AbortRebase(wtPath)
}
