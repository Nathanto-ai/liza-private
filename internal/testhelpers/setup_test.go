package testhelpers

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSetupTestGitRepo(t *testing.T) {
	tmpDir := t.TempDir()

	// Run the setup
	SetupTestGitRepo(t, tmpDir)

	// Verify git repo was initialized
	gitDir := filepath.Join(tmpDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		t.Error(".git directory does not exist")
	}

	// Verify git config
	cmd := exec.Command("git", "-C", tmpDir, "config", "user.email")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get git config: %v", err)
	}
	if string(output) != "test@example.com\n" {
		t.Errorf("Expected user.email=test@example.com, got %q", string(output))
	}

	cmd = exec.Command("git", "-C", tmpDir, "config", "user.name")
	output, err = cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get git config: %v", err)
	}
	if string(output) != "Test User\n" {
		t.Errorf("Expected user.name=Test User, got %q", string(output))
	}

	// Verify README was created
	readmePath := filepath.Join(tmpDir, "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		t.Error("README.md does not exist")
	}

	content, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("Failed to read README.md: %v", err)
	}
	if string(content) != "# Test\n" {
		t.Errorf("Expected README.md content '# Test\\n', got %q", string(content))
	}

	// Verify initial commit exists
	cmd = exec.Command("git", "-C", tmpDir, "log", "--oneline")
	output, err = cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get git log: %v", err)
	}
	if len(output) == 0 {
		t.Error("No commits found")
	}

	// Verify integration branch exists
	cmd = exec.Command("git", "-C", tmpDir, "branch", "--list", "integration")
	output, err = cmd.Output()
	if err != nil {
		t.Fatalf("Failed to list branches: %v", err)
	}
	if string(output) != "  integration\n" {
		t.Errorf("integration branch not found, got %q", string(output))
	}
}

func TestSetupLizaDir(t *testing.T) {
	tmpDir := t.TempDir()

	// Run the setup
	statePath, lockPath := SetupLizaDir(t, tmpDir)

	// Verify paths are correct
	expectedStatePath := filepath.Join(tmpDir, ".liza", "state.yaml")
	if statePath != expectedStatePath {
		t.Errorf("Expected statePath=%q, got %q", expectedStatePath, statePath)
	}

	expectedLockPath := filepath.Join(tmpDir, ".liza", "state.yaml.lock")
	if lockPath != expectedLockPath {
		t.Errorf("Expected lockPath=%q, got %q", expectedLockPath, lockPath)
	}

	// Verify .liza directory exists
	lizaDir := filepath.Join(tmpDir, ".liza")
	info, err := os.Stat(lizaDir)
	if os.IsNotExist(err) {
		t.Fatal(".liza directory does not exist")
	}
	if !info.IsDir() {
		t.Error(".liza is not a directory")
	}

	// Verify lock file exists
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		t.Error("Lock file does not exist")
	}

	// Verify lock file is empty
	content, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("Failed to read lock file: %v", err)
	}
	if len(content) != 0 {
		t.Errorf("Expected empty lock file, got %d bytes", len(content))
	}
}

func TestCreateTestWorktree(t *testing.T) {
	tmpDir := t.TempDir()
	SetupTestGitRepo(t, tmpDir)

	// Create worktree for a task
	taskID := "task-123"
	CreateTestWorktree(t, tmpDir, taskID)

	// Verify worktree directory exists
	wtDir := filepath.Join(tmpDir, ".worktrees", taskID)
	info, err := os.Stat(wtDir)
	if os.IsNotExist(err) {
		t.Fatal("Worktree directory does not exist")
	}
	if !info.IsDir() {
		t.Error("Worktree path is not a directory")
	}

	// Verify .git link file exists (real worktree, not bare directory)
	gitFile := filepath.Join(wtDir, ".git")
	if _, err := os.Stat(gitFile); os.IsNotExist(err) {
		t.Error(".git link file does not exist in worktree")
	}

	// Verify permissions (0755) — Windows reports 0777
	mode := info.Mode().Perm()
	if runtime.GOOS != "windows" && mode != 0755 {
		t.Errorf("Expected permissions 0755, got %o", mode)
	}
}

func TestCreateSpecFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a spec file
	filename := "vision.md"
	content := "# Vision\n\nThis is the vision.\n"
	specPath := CreateSpecFile(t, tmpDir, filename, content)

	// Verify returned path is correct
	expectedPath := filepath.Join(tmpDir, "specs", filename)
	if specPath != expectedPath {
		t.Errorf("Expected path=%q, got %q", expectedPath, specPath)
	}

	// Verify specs directory exists
	specsDir := filepath.Join(tmpDir, "specs")
	info, err := os.Stat(specsDir)
	if os.IsNotExist(err) {
		t.Fatal("specs directory does not exist")
	}
	if !info.IsDir() {
		t.Error("specs path is not a directory")
	}

	// Verify file exists and has correct content
	fileContent, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("Failed to read spec file: %v", err)
	}
	if string(fileContent) != content {
		t.Errorf("Expected content=%q, got %q", content, string(fileContent))
	}
}

func TestCreateSpecFile_MultipleFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create multiple spec files
	file1 := CreateSpecFile(t, tmpDir, "vision.md", "Vision content")
	file2 := CreateSpecFile(t, tmpDir, "architecture.md", "Architecture content")

	// Verify both files exist
	if _, err := os.Stat(file1); os.IsNotExist(err) {
		t.Error("First spec file does not exist")
	}
	if _, err := os.Stat(file2); os.IsNotExist(err) {
		t.Error("Second spec file does not exist")
	}

	// Verify contents
	content1, _ := os.ReadFile(file1)
	if string(content1) != "Vision content" {
		t.Errorf("First file has incorrect content: %q", string(content1))
	}

	content2, _ := os.ReadFile(file2)
	if string(content2) != "Architecture content" {
		t.Errorf("Second file has incorrect content: %q", string(content2))
	}
}
