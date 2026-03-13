// Package testhelpers provides shared test utilities for the liza test suite.
//
// This package consolidates duplicated test setup code found across multiple test files,
// improving maintainability and consistency. It includes helpers for:
//   - Git repository initialization
//   - Liza directory structure creation
//   - Test worktree management
//   - Spec file creation
//
// Usage Example:
//
//	func TestSomething(t *testing.T) {
//	    tmpDir := t.TempDir()
//	    testhelpers.SetupTestGitRepo(t, tmpDir)
//	    statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
//	    // ... continue with test
//	}
package testhelpers

// setup.go contains test helpers for setting up test environments including
// git repositories, liza directory structures, worktrees, and spec files.
//
// Functions in this file handle the physical filesystem and git operations
// required to create realistic test environments that mirror production usage.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// ValidVisionSpec is a minimal vision spec that passes specvalidate validation.
// Use this in tests that go through InitCommand or planner prompt building.
const ValidVisionSpec = `# Vision: Test

## Problem Statement
Test problem.

## Target Users
Developers.

## MVP Scope
- Feature A

## Explicit Out of Scope
- Feature B

## Success Criteria
All tests pass.

## Risks and Assumptions
None significant.
`

// SetupTestGitRepo initializes a git repository with basic configuration.
// It performs the following:
//   - Initializes a git repo in tmpDir
//   - Sets user.email to "test@example.com"
//   - Sets user.name to "Test User"
//   - Creates a README.md file with "# Test\n"
//   - Creates an initial commit
//   - Creates an "integration" branch
//
// This helper eliminates ~25 lines of duplicated code that appears 8-10 times
// across test files (claim_task_test.go, wt_create_test.go, wt_delete_test.go, etc.)
func SetupTestGitRepo(t *testing.T, tmpDir string) {
	t.Helper()

	// Initialize git repo with "main" as default branch
	if err := exec.Command("git", "init", "-b", "main", tmpDir).Run(); err != nil {
		t.Fatalf("Failed to init git repo: %v", err)
	}

	// Configure git user
	if err := exec.Command("git", "-C", tmpDir, "config", "user.email", "test@example.com").Run(); err != nil {
		t.Fatalf("Failed to set git email: %v", err)
	}
	if err := exec.Command("git", "-C", tmpDir, "config", "user.name", "Test User").Run(); err != nil {
		t.Fatalf("Failed to set git name: %v", err)
	}

	// Create initial commit
	readmePath := filepath.Join(tmpDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Test\n"), 0644); err != nil {
		t.Fatalf("Failed to create README: %v", err)
	}
	if err := exec.Command("git", "-C", tmpDir, "add", "README.md").Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}
	if err := exec.Command("git", "-C", tmpDir, "commit", "-m", "Initial commit").Run(); err != nil {
		t.Fatalf("Failed to git commit: %v", err)
	}

	// Create integration branch
	if err := exec.Command("git", "-C", tmpDir, "branch", "integration").Run(); err != nil {
		t.Fatalf("Failed to create integration branch: %v", err)
	}
}

// SetupLizaDir creates the .liza directory structure and returns paths to the state file and lock file.
// It performs the following:
//   - Creates .liza directory with 0755 permissions
//   - Creates state.yaml.lock file (empty)
//   - Returns (stateFile path, lockFile path)
//
// This helper eliminates ~8-10 lines of duplicated code that appears 15-18 times
// across test files.
func SetupLizaDir(t *testing.T, tmpDir string) (statePath, lockPath string) {
	t.Helper()

	lizaDir := filepath.Join(tmpDir, ".liza")
	if err := os.MkdirAll(lizaDir, 0755); err != nil {
		t.Fatalf("Failed to create .liza directory: %v", err)
	}

	statePath = filepath.Join(lizaDir, "state.yaml")
	lockPath = filepath.Join(lizaDir, "state.yaml.lock")

	// Create lock file
	if err := os.WriteFile(lockPath, []byte{}, 0644); err != nil {
		t.Fatalf("Failed to create lock file: %v", err)
	}

	return statePath, lockPath
}

// SetupGlobalLiza creates a fake ~/.liza/CORE.md so that commands requiring
// 'liza setup' to have been run (like InitCommand) pass their prerequisite check.
// It overrides $HOME via t.Setenv (auto-reverted on test cleanup).
// Returns the fake home directory path.
func SetupGlobalLiza(t *testing.T) string {
	t.Helper()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	// On Windows, os.UserHomeDir() uses USERPROFILE, not HOME.
	t.Setenv("USERPROFILE", fakeHome)
	globalLiza := filepath.Join(fakeHome, ".liza")
	if err := os.MkdirAll(globalLiza, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalLiza, "CORE.md"), []byte("# CORE\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return fakeHome
}

// CreateTestWorktree creates a test worktree directory for a task.
// It creates the .worktrees/<taskID> directory structure.
// Note: This only creates the directory; it does NOT run git worktree commands
// as those require more complex git state setup.
//
// This helper eliminates ~6-8 lines of duplicated code that appears 6-8 times
// in claim_task_test.go, wt_create_test.go, and wt_delete_test.go.
func CreateTestWorktree(t *testing.T, tmpDir, taskID string) {
	t.Helper()

	wtDir := filepath.Join(tmpDir, ".worktrees", taskID)
	if err := os.MkdirAll(wtDir, 0755); err != nil {
		t.Fatalf("Failed to create worktree directory: %v", err)
	}
}

// CreateSpecFile creates a spec file in the specs/ directory.
// It creates the specs directory if needed and writes the content to the specified filename.
// Returns the full path to the created spec file.
//
// This helper eliminates ~5-6 lines of duplicated code that appears 3-4 times
// in add_task_test.go, init_test.go, and validate_test.go.
func CreateSpecFile(t *testing.T, tmpDir, filename, content string) string {
	t.Helper()

	specsDir := filepath.Join(tmpDir, "specs")
	if err := os.MkdirAll(specsDir, 0755); err != nil {
		t.Fatalf("Failed to create specs directory: %v", err)
	}

	specFile := filepath.Join(specsDir, filename)
	if err := os.WriteFile(specFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create spec file: %v", err)
	}

	return specFile
}
