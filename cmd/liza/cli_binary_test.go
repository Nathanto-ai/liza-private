package main

// cli_binary_test.go — Integration tests that invoke the built liza binary
// as a subprocess to validate CLI behavior end-to-end. These tests ensure
// the binary:
//   - Builds and runs without errors
//   - Responds to --help and version flags
//   - Validates invalid inputs correctly
//   - Returns structured output for key commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// validVisionContent is a minimal spec that passes specvalidate for CLI binary tests.
const validVisionContent = `# Vision: Test

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

// lizaBinary returns the path to the built liza binary.
// Tests that use this should call buildLiza first.
func lizaBinary(t *testing.T) string {
	t.Helper()
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	bin := filepath.Join(t.TempDir(), "liza"+ext)
	return bin
}

// buildLiza compiles the liza binary into a temp directory.
func buildLiza(t *testing.T) string {
	t.Helper()
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "liza"+ext)

	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = filepath.Join(findModuleRoot(t), "cmd", "liza")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to build liza binary: %v\nOutput: %s", err, string(out))
	}
	return bin
}

// findModuleRoot walks up from the current file to find the go.mod directory.
func findModuleRoot(t *testing.T) string {
	t.Helper()
	// Start from the directory of this test file
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("Could not find go.mod in any parent directory")
		}
		dir = parent
	}
}

// TestCLI_Version verifies `liza version` (or `liza --help` for now).
func TestCLI_Version(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary test in short mode")
	}

	bin := buildLiza(t)

	cmd := exec.Command(bin, "--help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// --help might return non-zero on some cobra setups
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 0 {
			// Some cobra versions return 0 for --help, some don't
			t.Logf("--help exit code: %d", exitErr.ExitCode())
		}
	}

	output := string(out)
	if !strings.Contains(output, "Liza") && !strings.Contains(output, "liza") {
		t.Errorf("--help output should mention Liza, got: %s", truncate(output, 200))
	}

	// Should contain at least some known commands
	for _, sub := range []string{"agent", "status", "init"} {
		if !strings.Contains(output, sub) {
			t.Errorf("--help output should mention %q command", sub)
		}
	}
}

// TestCLI_InvalidRole verifies `liza agent invalid-role` fails.
func TestCLI_InvalidRole(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary test in short mode")
	}

	bin := buildLiza(t)

	cmd := exec.Command(bin, "agent", "invalid-role", "--agent-id", "test-1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected error for invalid role, got success")
	}

	output := string(out)
	if !strings.Contains(output, "invalid") && !strings.Contains(output, "role") {
		t.Errorf("expected error about invalid role, got: %s", truncate(output, 200))
	}
}

// TestCLI_InvalidCLI verifies `liza agent coder --cli badcli` fails.
func TestCLI_InvalidCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary test in short mode")
	}

	bin := buildLiza(t)

	cmd := exec.Command(bin, "agent", "coder", "--agent-id", "coder-1", "--cli", "badcli")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected error for invalid CLI, got success")
	}

	output := string(out)
	if !strings.Contains(output, "invalid") || !strings.Contains(output, "CLI") {
		t.Errorf("expected error about invalid CLI, got: %s", truncate(output, 200))
	}
}

// TestCLI_AgentRequiresAgentID verifies agent command requires --agent-id.
func TestCLI_AgentRequiresAgentID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary test in short mode")
	}

	bin := buildLiza(t)

	// Don't set LIZA_AGENT_ID env var
	cmd := exec.Command(bin, "agent", "coder")
	cmd.Env = filterEnv(os.Environ(), "LIZA_AGENT_ID")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected error when --agent-id not provided, got success")
	}

	output := string(out)
	if !strings.Contains(output, "agent") {
		t.Errorf("expected error about agent ID, got: %s", truncate(output, 200))
	}
}

// TestCLI_MaxLoopsFlag verifies the --max-loops flag is recognized.
func TestCLI_MaxLoopsFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary test in short mode")
	}

	bin := buildLiza(t)

	// Just check the flag is accepted (will fail because no project, but shouldn't
	// fail because of an unknown flag)
	cmd := exec.Command(bin, "agent", "coder", "--agent-id", "coder-1", "--max-loops", "5", "--help")
	out, err := cmd.CombinedOutput()
	output := string(out)

	// --help should succeed regardless
	if err != nil {
		// --help with cobra may return 0 or non-zero depending on version
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() > 1 {
			t.Errorf("--max-loops flag might not be recognized. Exit: %d, Output: %s",
				exitErr.ExitCode(), truncate(output, 200))
		}
	}

	// Help output should mention max-loops
	if !strings.Contains(output, "max-loops") {
		t.Errorf("help output should mention max-loops flag, got: %s", truncate(output, 500))
	}
}

// TestCLI_ValidateNoProject verifies `liza validate` fails gracefully
// when there's no .liza directory.
func TestCLI_ValidateNoProject(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary test in short mode")
	}

	bin := buildLiza(t)

	tmpDir := t.TempDir()
	cmd := exec.Command(bin, "validate")
	cmd.Dir = tmpDir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected error for validate without project, got success")
	}

	output := string(out)
	// Should mention something about missing state or project
	if len(output) == 0 {
		t.Error("expected error output, got empty")
	}
}

// TestCLI_StatusNoProject verifies `liza status` fails gracefully.
func TestCLI_StatusNoProject(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary test in short mode")
	}

	bin := buildLiza(t)

	tmpDir := t.TempDir()
	cmd := exec.Command(bin, "status")
	cmd.Dir = tmpDir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected error for status without project, got success")
	}

	output := string(out)
	if len(output) == 0 {
		t.Error("expected error output, got empty")
	}
}

// TestCLI_LogIncompatibleWithInteractive verifies --log + -i error.
func TestCLI_LogIncompatibleWithInteractive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary test in short mode")
	}

	bin := buildLiza(t)

	cmd := exec.Command(bin, "agent", "coder", "--agent-id", "coder-1", "--log", "-i")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected error for --log + -i, got success")
	}

	output := string(out)
	if !strings.Contains(output, "incompatible") {
		t.Errorf("expected incompatibility error, got: %s", truncate(output, 200))
	}
}

// TestCLI_InitAndStatus verifies init → status pipeline works.
func TestCLI_InitAndStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary test in short mode")
	}
	homeDir, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(homeDir, ".liza")); os.IsNotExist(err) {
		t.Skip("skipping: ~/.liza not found (run 'liza setup' first)")
	}

	bin := buildLiza(t)

	// Create a temp project with a git repo
	tmpDir := t.TempDir()
	setupGit(t, tmpDir)

	// Create specs directory with a vision file
	specsDir := filepath.Join(tmpDir, "specs")
	if err := os.MkdirAll(specsDir, 0755); err != nil {
		t.Fatalf("Failed to create specs dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(specsDir, "vision.md"), []byte(validVisionContent), 0644); err != nil {
		t.Fatalf("Failed to write vision.md: %v", err)
	}

	// Run liza init — init takes a positional description argument
	initCmd := exec.Command(bin, "init", "A test project",
		"--spec", filepath.Join(specsDir, "vision.md"),
	)
	initCmd.Dir = tmpDir
	initOut, err := initCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("liza init failed: %v\nOutput: %s", err, string(initOut))
	}

	// Verify state.yaml was created
	statePath := filepath.Join(tmpDir, ".liza", "state.yaml")
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		t.Fatal("state.yaml not created after init")
	}

	// Run liza status
	statusCmd := exec.Command(bin, "status")
	statusCmd.Dir = tmpDir
	statusOut, err := statusCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("liza status failed: %v\nOutput: %s", err, string(statusOut))
	}

	statusStr := string(statusOut)
	if !strings.Contains(statusStr, "test") && !strings.Contains(statusStr, "RUNNING") {
		t.Errorf("status output should show project info, got: %s", truncate(statusStr, 500))
	}
}

// TestCLI_ValidateAfterInit verifies validate works after init.
func TestCLI_ValidateAfterInit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary test in short mode")
	}
	homeDir, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(homeDir, ".liza")); os.IsNotExist(err) {
		t.Skip("skipping: ~/.liza not found (run 'liza setup' first)")
	}

	bin := buildLiza(t)

	tmpDir := t.TempDir()
	setupGit(t, tmpDir)

	specsDir := filepath.Join(tmpDir, "specs")
	os.MkdirAll(specsDir, 0755)
	os.WriteFile(filepath.Join(specsDir, "vision.md"), []byte(validVisionContent), 0644)

	// Init — positional description argument
	initCmd := exec.Command(bin, "init", "test",
		"--spec", filepath.Join(specsDir, "vision.md"),
	)
	initCmd.Dir = tmpDir
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("init failed: %v\n%s", err, string(out))
	}

	// Validate
	valCmd := exec.Command(bin, "validate")
	valCmd.Dir = tmpDir
	valOut, err := valCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("validate failed: %v\nOutput: %s", err, string(valOut))
	}
}

// Helper: setupGit initializes a git repo with main and integration branches
func setupGit(t *testing.T, dir string) {
	t.Helper()
	commands := [][]string{
		{"git", "init", "--initial-branch", "main"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}

	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git command %v failed: %v\n%s", args, err, string(out))
		}
	}

	// Create initial commit
	readmePath := filepath.Join(dir, "README.md")
	os.WriteFile(readmePath, []byte("# Test\n"), 0644)
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = dir
	cmd.CombinedOutput()
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = dir
	cmd.CombinedOutput()

	// Create integration branch
	cmd = exec.Command("git", "branch", "integration")
	cmd.Dir = dir
	cmd.CombinedOutput()
}

// Helper: filterEnv removes a specific environment variable
func filterEnv(env []string, key string) []string {
	var filtered []string
	prefix := key + "="
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// Helper: truncate a string for display
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
