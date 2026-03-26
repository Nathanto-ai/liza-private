package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liza-mas/liza/internal/testhelpers"
)

func TestUpdateRef(t *testing.T) {
	repoDir := setupTestRepo(t)
	g := New(repoDir)

	testhelpers.MustGit(t, repoDir, "checkout", "integration")

	sha1, err := g.GetCommitSHA("HEAD")
	if err != nil {
		t.Fatal(err)
	}

	// Add another commit so we have two distinct SHAs
	testFile := filepath.Join(repoDir, "update-ref-test.txt")
	if writeErr := os.WriteFile(testFile, []byte("v1\n"), 0644); writeErr != nil {
		t.Fatal(writeErr)
	}
	testhelpers.MustGit(t, repoDir, "add", ".")
	testhelpers.MustGit(t, repoDir, "commit", "-m", "for update-ref test")
	sha2, err := g.GetCommitSHA("HEAD")
	if err != nil {
		t.Fatal(err)
	}

	ref := "refs/heads/integration"

	t.Run("unconditional update (empty expectedOldSHA)", func(t *testing.T) {
		// Move ref to sha1 unconditionally
		if err := g.UpdateRef(ref, sha1, ""); err != nil {
			t.Fatalf("UpdateRef unconditional failed: %v", err)
		}
		cur, _ := g.GetCommitSHA(ref)
		if cur != sha1 {
			t.Errorf("ref = %s, want %s", cur, sha1)
		}
	})

	t.Run("CAS success", func(t *testing.T) {
		// Set to sha1 first
		if err := g.UpdateRef(ref, sha1, ""); err != nil {
			t.Fatal(err)
		}
		// CAS: sha1 → sha2 with correct expected
		if err := g.UpdateRef(ref, sha2, sha1); err != nil {
			t.Fatalf("CAS should succeed: %v", err)
		}
		cur, _ := g.GetCommitSHA(ref)
		if cur != sha2 {
			t.Errorf("ref = %s, want %s", cur, sha2)
		}
	})

	t.Run("CAS failure returns RefConflictError", func(t *testing.T) {
		// ref is at sha2, but we claim it's at sha1
		err := g.UpdateRef(ref, sha1, sha1)
		if err == nil {
			t.Fatal("CAS should fail when expected doesn't match actual")
		}
		var casErr *RefConflictError
		if !errors.As(err, &casErr) {
			t.Fatalf("expected *RefConflictError, got %T: %v", err, err)
		}
		if casErr.Ref != ref {
			t.Errorf("RefConflictError.Ref = %q, want %q", casErr.Ref, ref)
		}
		if casErr.Expected != sha1 {
			t.Errorf("RefConflictError.Expected = %q, want %q", casErr.Expected, sha1)
		}
		if casErr.Actual != sha2 {
			t.Errorf("RefConflictError.Actual = %q, want %q (parsed from git error)", casErr.Actual, sha2)
		}
		// Verify ref was NOT moved
		cur, _ := g.GetCommitSHA(ref)
		if cur != sha2 {
			t.Errorf("ref should still be %s after CAS failure, got %s", sha2, cur)
		}
	})
}

func TestMergeBranchFastForward(t *testing.T) {
	repoDir := setupTestRepo(t)
	git := New(repoDir)

	// Create a feature branch from main
	testhelpers.MustGit(t, repoDir, "checkout", "main")
	testhelpers.MustGit(t, repoDir, "checkout", "-b", "feature")

	// Add a commit on feature
	testFile := filepath.Join(repoDir, "feature.txt")
	if err := os.WriteFile(testFile, []byte("feature\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, repoDir, "add", "feature.txt")
	testhelpers.MustGit(t, repoDir, "commit", "-m", "Feature commit")

	// Go back to main and merge (should be fast-forward)
	testhelpers.MustGit(t, repoDir, "checkout", "main")

	fastForward, mergeCommit, err := git.MergeBranch("feature")
	if err != nil {
		t.Fatalf("MergeBranch() error = %v", err)
	}
	if !fastForward {
		t.Error("Expected fast-forward merge")
	}
	if mergeCommit == "" {
		t.Error("Expected merge commit SHA")
	}

	// Verify the feature file exists
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("Feature file should exist after merge")
	}
}

func TestMergeBranchNoFastForward(t *testing.T) {
	repoDir := setupTestRepo(t)
	git := New(repoDir)

	// Start from integration branch
	testhelpers.MustGit(t, repoDir, "checkout", "integration")

	// Create feature branch
	testhelpers.MustGit(t, repoDir, "checkout", "-b", "feature")
	featureFile := filepath.Join(repoDir, "feature.txt")
	if err := os.WriteFile(featureFile, []byte("feature\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, repoDir, "add", "feature.txt")
	testhelpers.MustGit(t, repoDir, "commit", "-m", "Feature commit")

	// Go back to integration and make another commit (diverge)
	testhelpers.MustGit(t, repoDir, "checkout", "integration")
	otherFile := filepath.Join(repoDir, "other.txt")
	if err := os.WriteFile(otherFile, []byte("other\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, repoDir, "add", "other.txt")
	testhelpers.MustGit(t, repoDir, "commit", "-m", "Other commit")

	// Now merge feature (cannot be fast-forward)
	fastForward, mergeCommit, err := git.MergeBranch("feature")
	if err != nil {
		t.Fatalf("MergeBranch() error = %v", err)
	}
	if fastForward {
		t.Error("Expected non-fast-forward merge")
	}
	if mergeCommit == "" {
		t.Error("Expected merge commit SHA")
	}

	// Verify both files exist
	if _, err := os.Stat(featureFile); os.IsNotExist(err) {
		t.Error("Feature file should exist after merge")
	}
	if _, err := os.Stat(otherFile); os.IsNotExist(err) {
		t.Error("Other file should exist after merge")
	}
}

func TestMergeBranchConflict(t *testing.T) {
	repoDir := setupTestRepo(t)
	git := New(repoDir)

	// Start from integration branch
	testhelpers.MustGit(t, repoDir, "checkout", "integration")

	// Create feature branch and modify README
	testhelpers.MustGit(t, repoDir, "checkout", "-b", "feature")
	readmeFile := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readmeFile, []byte("# Feature version\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, repoDir, "add", "README.md")
	testhelpers.MustGit(t, repoDir, "commit", "-m", "Feature README")

	// Go back to integration and modify README differently
	testhelpers.MustGit(t, repoDir, "checkout", "integration")
	if err := os.WriteFile(readmeFile, []byte("# Integration version\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, repoDir, "add", "README.md")
	testhelpers.MustGit(t, repoDir, "commit", "-m", "Integration README")

	// Try to merge - should conflict
	_, _, err := git.MergeBranch("feature")
	if err == nil {
		t.Error("Expected merge conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "conflict") && !strings.Contains(err.Error(), "CONFLICT") {
		t.Errorf("Expected conflict error, got: %v", err)
	}

	// Abort the merge
	if err := git.AbortMerge(); err != nil {
		t.Fatalf("AbortMerge() error = %v", err)
	}

	// Verify we're back to clean state
	output := testhelpers.MustGit(t, repoDir, "status", "--porcelain")
	if output != "" {
		t.Error("Expected clean state after merge abort")
	}
}

func TestRestoreSyncedFiles(t *testing.T) {
	t.Run("added file is removed", func(t *testing.T) {
		repoDir := setupTestRepo(t)
		g := New(repoDir)

		testhelpers.MustGit(t, repoDir, "checkout", "integration")
		baseCommit := testhelpers.MustGit(t, repoDir, "rev-parse", "HEAD")

		// Add a new file and commit.
		newFile := filepath.Join(repoDir, "new-feature.txt")
		if err := os.WriteFile(newFile, []byte("feature\n"), 0644); err != nil {
			t.Fatal(err)
		}
		testhelpers.MustGit(t, repoDir, "add", "new-feature.txt")
		testhelpers.MustGit(t, repoDir, "commit", "-m", "Add new feature")
		tipCommit := testhelpers.MustGit(t, repoDir, "rev-parse", "HEAD")

		// Sync forward (simulates what MergeWorktree does after update-ref).
		if err := g.SyncMergedFiles(baseCommit, tipCommit); err != nil {
			t.Fatalf("SyncMergedFiles: %v", err)
		}

		// File exists after sync.
		if _, err := os.Stat(newFile); err != nil {
			t.Fatalf("expected new-feature.txt to exist after sync: %v", err)
		}

		// Restore to baseCommit (the file didn't exist there).
		if err := g.RestoreSyncedFiles(baseCommit, tipCommit, baseCommit); err != nil {
			t.Fatalf("RestoreSyncedFiles: %v", err)
		}

		// File should be gone.
		if _, err := os.Stat(newFile); !os.IsNotExist(err) {
			t.Errorf("expected new-feature.txt to be removed after restore, stat: %v", err)
		}
	})

	t.Run("modified file is restored", func(t *testing.T) {
		repoDir := setupTestRepo(t)
		g := New(repoDir)

		testhelpers.MustGit(t, repoDir, "checkout", "integration")

		// Write a file and commit as the base.
		modFile := filepath.Join(repoDir, "modify-me.txt")
		if err := os.WriteFile(modFile, []byte("original\n"), 0644); err != nil {
			t.Fatal(err)
		}
		testhelpers.MustGit(t, repoDir, "add", "modify-me.txt")
		testhelpers.MustGit(t, repoDir, "commit", "-m", "Add modify-me")
		baseCommit := testhelpers.MustGit(t, repoDir, "rev-parse", "HEAD")

		// Modify and commit.
		if err := os.WriteFile(modFile, []byte("modified\n"), 0644); err != nil {
			t.Fatal(err)
		}
		testhelpers.MustGit(t, repoDir, "add", "modify-me.txt")
		testhelpers.MustGit(t, repoDir, "commit", "-m", "Modify file")
		tipCommit := testhelpers.MustGit(t, repoDir, "rev-parse", "HEAD")

		// Sync forward.
		if err := g.SyncMergedFiles(baseCommit, tipCommit); err != nil {
			t.Fatalf("SyncMergedFiles: %v", err)
		}

		// After sync, file has modified content.
		content, _ := os.ReadFile(modFile)
		if string(content) != "modified\n" {
			t.Fatalf("expected modified content after sync, got %q", content)
		}

		// Restore to baseCommit.
		if err := g.RestoreSyncedFiles(baseCommit, tipCommit, baseCommit); err != nil {
			t.Fatalf("RestoreSyncedFiles: %v", err)
		}

		content, _ = os.ReadFile(modFile)
		if normalizeLineEndings(string(content)) != "original\n" {
			t.Errorf("expected original content after restore, got %q", content)
		}
	})

	t.Run("unaffected files untouched", func(t *testing.T) {
		repoDir := setupTestRepo(t)
		g := New(repoDir)

		testhelpers.MustGit(t, repoDir, "checkout", "integration")

		baseCommit := testhelpers.MustGit(t, repoDir, "rev-parse", "HEAD")

		// Add a file and commit (this is the "affected" file).
		affected := filepath.Join(repoDir, "affected.txt")
		if err := os.WriteFile(affected, []byte("affected\n"), 0644); err != nil {
			t.Fatal(err)
		}
		testhelpers.MustGit(t, repoDir, "add", "affected.txt")
		testhelpers.MustGit(t, repoDir, "commit", "-m", "Add affected")
		tipCommit := testhelpers.MustGit(t, repoDir, "rev-parse", "HEAD")

		// Create an unrelated untracked file.
		unrelated := filepath.Join(repoDir, "unrelated.txt")
		if err := os.WriteFile(unrelated, []byte("don't touch me\n"), 0644); err != nil {
			t.Fatal(err)
		}

		if err := g.RestoreSyncedFiles(baseCommit, tipCommit, baseCommit); err != nil {
			t.Fatalf("RestoreSyncedFiles: %v", err)
		}

		// Unrelated file should still exist with original content.
		content, err := os.ReadFile(unrelated)
		if err != nil {
			t.Fatalf("unrelated file should still exist: %v", err)
		}
		if string(content) != "don't touch me\n" {
			t.Errorf("unrelated file content changed: %q", content)
		}
	})

	t.Run("subdirectory file is restored correctly", func(t *testing.T) {
		repoDir := setupTestRepo(t)
		g := New(repoDir)

		testhelpers.MustGit(t, repoDir, "checkout", "integration")

		// Create a subdirectory file and commit as base.
		subDir := filepath.Join(repoDir, "src")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatal(err)
		}
		subFile := filepath.Join(subDir, "app.go")
		if err := os.WriteFile(subFile, []byte("package main\n"), 0644); err != nil {
			t.Fatal(err)
		}
		testhelpers.MustGit(t, repoDir, "add", "src/app.go")
		testhelpers.MustGit(t, repoDir, "commit", "-m", "Add src/app.go")
		baseCommit := testhelpers.MustGit(t, repoDir, "rev-parse", "HEAD")

		// Modify the subdirectory file and commit.
		if err := os.WriteFile(subFile, []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
			t.Fatal(err)
		}
		testhelpers.MustGit(t, repoDir, "add", "src/app.go")
		testhelpers.MustGit(t, repoDir, "commit", "-m", "Modify src/app.go")
		tipCommit := testhelpers.MustGit(t, repoDir, "rev-parse", "HEAD")

		// Sync forward.
		if err := g.SyncMergedFiles(baseCommit, tipCommit); err != nil {
			t.Fatalf("SyncMergedFiles: %v", err)
		}

		// Restore to baseCommit — should get original content.
		if err := g.RestoreSyncedFiles(baseCommit, tipCommit, baseCommit); err != nil {
			t.Fatalf("RestoreSyncedFiles: %v", err)
		}

		content, _ := os.ReadFile(subFile)
		if normalizeLineEndings(string(content)) != "package main\n" {
			t.Errorf("expected original content after restore, got %q", content)
		}
	})

	t.Run("subdirectory added file is removed", func(t *testing.T) {
		repoDir := setupTestRepo(t)
		g := New(repoDir)

		testhelpers.MustGit(t, repoDir, "checkout", "integration")
		baseCommit := testhelpers.MustGit(t, repoDir, "rev-parse", "HEAD")

		// Add a file in a subdirectory.
		subDir := filepath.Join(repoDir, "pkg", "util")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatal(err)
		}
		subFile := filepath.Join(subDir, "helper.go")
		if err := os.WriteFile(subFile, []byte("package util\n"), 0644); err != nil {
			t.Fatal(err)
		}
		testhelpers.MustGit(t, repoDir, "add", "pkg/util/helper.go")
		testhelpers.MustGit(t, repoDir, "commit", "-m", "Add pkg/util/helper.go")
		tipCommit := testhelpers.MustGit(t, repoDir, "rev-parse", "HEAD")

		// Sync forward.
		if err := g.SyncMergedFiles(baseCommit, tipCommit); err != nil {
			t.Fatalf("SyncMergedFiles: %v", err)
		}

		// File should exist after sync.
		if _, err := os.Stat(subFile); err != nil {
			t.Fatalf("expected pkg/util/helper.go after sync: %v", err)
		}

		// Restore to baseCommit — file didn't exist there.
		if err := g.RestoreSyncedFiles(baseCommit, tipCommit, baseCommit); err != nil {
			t.Fatalf("RestoreSyncedFiles: %v", err)
		}

		if _, err := os.Stat(subFile); !os.IsNotExist(err) {
			t.Errorf("expected pkg/util/helper.go to be removed, stat: %v", err)
		}
	})

	t.Run("empty diff is no-op", func(t *testing.T) {
		repoDir := setupTestRepo(t)
		g := New(repoDir)

		testhelpers.MustGit(t, repoDir, "checkout", "integration")
		commit := testhelpers.MustGit(t, repoDir, "rev-parse", "HEAD")

		// Same commit for both — no diff, should be a no-op.
		if err := g.RestoreSyncedFiles(commit, commit, commit); err != nil {
			t.Fatalf("RestoreSyncedFiles with empty diff should succeed: %v", err)
		}
	})
}

func normalizeLineEndings(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}
