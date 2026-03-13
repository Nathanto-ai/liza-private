package commands

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// setupGlobalLiza delegates to testhelpers.SetupGlobalLiza.
func setupGlobalLiza(t *testing.T) string {
	return testhelpers.SetupGlobalLiza(t)
}

// skipIfSymlinkUnsupported skips the test if symlinks cannot be created
// (e.g. on Windows without Developer Mode or elevated privileges).
func skipIfSymlinkUnsupported(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return
	}
	tmp := t.TempDir()
	target := filepath.Join(tmp, "target")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("skipping: symlinks not supported on this Windows environment: %v", err)
	}
}

func TestInitCommand(t *testing.T) {
	tests := []struct {
		name        string
		description string
		specRef     string
		setup       func(t *testing.T, tmpDir string)
		skipGlobal  bool // if true, don't set up global liza
		wantErr     bool
		errContains string
	}{
		{
			name:        "successful initialization",
			description: "Test goal",
			specRef:     "specs/vision.md",
			setup: func(t *testing.T, tmpDir string) {
				testhelpers.CreateSpecFile(t, tmpDir, "vision.md", testhelpers.ValidVisionSpec)
			},
			wantErr: false,
		},
		{
			name:        ".liza already exists",
			description: "Test goal",
			specRef:     "specs/vision.md",
			setup: func(t *testing.T, tmpDir string) {
				testhelpers.CreateSpecFile(t, tmpDir, "vision.md", testhelpers.ValidVisionSpec)
				// Create .liza directory
				lizaDir := paths.New(tmpDir).LizaDir()
				if err := os.Mkdir(lizaDir, 0755); err != nil {
					t.Fatal(err)
				}
			},
			wantErr:     true,
			errContains: "already exists",
		},
		{
			name:        "spec file does not exist",
			description: "Test goal",
			specRef:     "specs/vision.md",
			setup:       func(t *testing.T, tmpDir string) {}, // No spec file
			wantErr:     true,
			errContains: "spec file does not exist",
		},
		{
			name:        "global config not found",
			description: "Test goal",
			specRef:     "specs/vision.md",
			skipGlobal:  true,
			setup: func(t *testing.T, tmpDir string) {
				testhelpers.CreateSpecFile(t, tmpDir, "vision.md", testhelpers.ValidVisionSpec)
			},
			wantErr:     true,
			errContains: "Run 'liza setup' first",
		},
		{
			name:        "spec validation fails for incomplete vision",
			description: "Test goal",
			specRef:     "specs/vision.md",
			setup: func(t *testing.T, tmpDir string) {
				testhelpers.CreateSpecFile(t, tmpDir, "vision.md", "# Vision\nJust a title.\n")
			},
			wantErr:     true,
			errContains: "spec validation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary git repo
			tmpDir := setupGitRepo(t)
			defer os.RemoveAll(tmpDir)

			// Set up global liza unless test skips it
			if !tt.skipGlobal {
				setupGlobalLiza(t)
			} else {
				// Point HOME to an empty dir so global check fails
				emptyHome := t.TempDir()
				t.Setenv("HOME", emptyHome)
				t.Setenv("USERPROFILE", emptyHome) // Windows uses USERPROFILE
			}

			// Change to temp directory
			originalDir, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			defer os.Chdir(originalDir)
			if err := os.Chdir(tmpDir); err != nil {
				t.Fatal(err)
			}

			// Run setup
			tt.setup(t, tmpDir)

			// Run init command
			err = InitCommand(tt.description, tt.specRef, nil)

			// Check error
			if (err != nil) != tt.wantErr {
				t.Errorf("InitCommand() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" {
				testhelpers.AssertErrorContains(t, err, tt.errContains)
				return
			}

			// If no error expected, verify the initialization
			if !tt.wantErr {
				verifyInitialization(t, tmpDir, tt.description, tt.specRef)
			}
		})
	}
}

func TestInitCommandDirectoryStructure(t *testing.T) {
	tmpDir := setupGitRepo(t)
	defer os.RemoveAll(tmpDir)

	setupGlobalLiza(t)

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(originalDir)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	testhelpers.CreateSpecFile(t, tmpDir, "vision.md", testhelpers.ValidVisionSpec)

	// Run init
	if err := InitCommand("Test goal", "specs/vision.md", nil); err != nil {
		t.Fatalf("InitCommand() error = %v", err)
	}

	// Verify directory structure
	lizaDir := paths.New(tmpDir).LizaDir()
	expectedDirs := []string{
		lizaDir,
		filepath.Join(lizaDir, "archive"),
	}

	for _, dir := range expectedDirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Errorf("directory %s was not created", dir)
		}
	}

	// Verify files
	expectedFiles := []string{
		filepath.Join(lizaDir, "state.yaml"),
		filepath.Join(lizaDir, "log.yaml"),
		filepath.Join(lizaDir, "alerts.log"),
		filepath.Join(lizaDir, "state.yaml.lock"),
	}

	for _, file := range expectedFiles {
		if _, err := os.Stat(file); os.IsNotExist(err) {
			t.Errorf("file %s was not created", file)
		}
	}

	// Verify GUARDRAILS.md template was created at project root
	guardrailsPath := filepath.Join(tmpDir, "GUARDRAILS.md")
	if _, err := os.Stat(guardrailsPath); os.IsNotExist(err) {
		t.Error("GUARDRAILS.md was not created at project root")
	}
}

func TestInitCommandIntegrationBranch(t *testing.T) {
	tmpDir := setupGitRepo(t)
	defer os.RemoveAll(tmpDir)

	setupGlobalLiza(t)

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(originalDir)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	testhelpers.CreateSpecFile(t, tmpDir, "vision.md", testhelpers.ValidVisionSpec)

	// Verify integration branch doesn't exist
	cmd := exec.Command("git", "rev-parse", "--verify", "integration")
	if err := cmd.Run(); err == nil {
		t.Fatal("integration branch already exists before init")
	}

	// Run init
	if err := InitCommand("Test goal", "specs/vision.md", nil); err != nil {
		t.Fatalf("InitCommand() error = %v", err)
	}

	// Verify integration branch was created
	cmd = exec.Command("git", "rev-parse", "--verify", "integration")
	if err := cmd.Run(); err != nil {
		t.Error("integration branch was not created")
	}
}

// Helper functions

func setupGitRepo(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()

	// Resolve symlinks so paths match os.Getwd() on macOS
	// (macOS: /var -> /private/var, but t.TempDir() returns /var/...)
	tmpDir, err := filepath.EvalSymlinks(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	// Initialize git repo with "main" as default branch
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	// Configure git
	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	// Create initial commit
	readmeFile := filepath.Join(tmpDir, "README.md")
	if err := os.WriteFile(readmeFile, []byte("# Test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd = exec.Command("git", "add", "README.md")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	return tmpDir
}

func verifyInitialization(t *testing.T, tmpDir, description, specRef string) {
	t.Helper()

	lizaDir := paths.New(tmpDir).LizaDir()
	statePath := filepath.Join(lizaDir, "state.yaml")

	// Read state
	bb := db.New(statePath)
	state, err := bb.Read()
	if err != nil {
		t.Fatalf("failed to read state: %v", err)
	}

	// Verify version
	if state.Version != 1 {
		t.Errorf("state.Version = %d, want 1", state.Version)
	}

	// Verify goal
	if state.Goal.Description != description {
		t.Errorf("state.Goal.Description = %q, want %q", state.Goal.Description, description)
	}
	wantSpecRef := filepath.Join(tmpDir, specRef)
	if state.Goal.SpecRef != wantSpecRef {
		t.Errorf("state.Goal.SpecRef = %q, want %q", state.Goal.SpecRef, wantSpecRef)
	}
	if state.Goal.Status != models.GoalStatusInProgress {
		t.Errorf("state.Goal.Status = %v, want %v", state.Goal.Status, models.GoalStatusInProgress)
	}
	if state.Goal.ID == "" {
		t.Error("state.Goal.ID is empty")
	}
	if state.Goal.Created.IsZero() {
		t.Error("state.Goal.Created is zero")
	}
	if len(state.Goal.AlignmentHistory) == 0 {
		t.Error("state.Goal.AlignmentHistory is empty")
	}

	// Verify tasks is empty
	if len(state.Tasks) != 0 {
		t.Errorf("state.Tasks length = %d, want 0", len(state.Tasks))
	}

	// Verify agents is empty
	if len(state.Agents) != 0 {
		t.Errorf("state.Agents length = %d, want 0", len(state.Agents))
	}

	// Verify sprint
	if state.Sprint.ID != "sprint-1" {
		t.Errorf("state.Sprint.ID = %q, want %q", state.Sprint.ID, "sprint-1")
	}
	if state.Sprint.GoalRef != state.Goal.ID {
		t.Errorf("state.Sprint.GoalRef = %q, want %q", state.Sprint.GoalRef, state.Goal.ID)
	}
	if state.Sprint.Status != models.SprintStatusInProgress {
		t.Errorf("state.Sprint.Status = %v, want %v", state.Sprint.Status, models.SprintStatusInProgress)
	}

	// Verify config
	if state.Config.MaxCoderIterations != 10 {
		t.Errorf("state.Config.MaxCoderIterations = %d, want 10", state.Config.MaxCoderIterations)
	}
	if state.Config.MaxReviewCycles != 5 {
		t.Errorf("state.Config.MaxReviewCycles = %d, want 5", state.Config.MaxReviewCycles)
	}
	if state.Config.IntegrationBranch != "integration" {
		t.Errorf("state.Config.IntegrationBranch = %q, want %q", state.Config.IntegrationBranch, "integration")
	}
	if state.Config.Mode != models.SystemModeRunning {
		t.Errorf("state.Config.Mode = %q, want %q", state.Config.Mode, models.SystemModeRunning)
	}

	// Verify circuit breaker
	if state.CircuitBreaker.Status != "OK" {
		t.Errorf("state.CircuitBreaker.Status = %q, want %q", state.CircuitBreaker.Status, "OK")
	}

	// Verify timestamp is recent (within 5 seconds)
	now := time.Now().UTC()
	diff := now.Sub(state.Goal.Created)
	if diff < 0 || diff > 5*time.Second {
		t.Errorf("state.Goal.Created timestamp difference = %v, want < 5s", diff)
	}
}

func TestInitCommand_CreatesContractSymlinks(t *testing.T) {
	skipIfSymlinkUnsupported(t)
	// Create temporary git repo
	gitDir := setupGitRepo(t)
	defer os.RemoveAll(gitDir)

	fakeHome := setupGlobalLiza(t)

	// Change to temp directory
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(originalDir)
	if err := os.Chdir(gitDir); err != nil {
		t.Fatal(err)
	}

	// Setup
	testhelpers.CreateSpecFile(t, gitDir, "vision.md", testhelpers.ValidVisionSpec)

	// Run init
	err = InitCommand("Test goal", "specs/vision.md", nil)
	if err != nil {
		t.Fatalf("InitCommand failed: %v", err)
	}

	// Verify contract symlinks point to absolute global path
	globalDir := filepath.Join(fakeHome, ".liza")
	expectedTarget := filepath.Join(globalDir, "CORE.md")
	for _, name := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md"} {
		linkPath := filepath.Join(gitDir, name)
		target, err := os.Readlink(linkPath)
		if err != nil {
			t.Errorf("Symlink %s not created: %v", name, err)
			continue
		}
		if target != expectedTarget {
			t.Errorf("Symlink %s target = %q, want %q", name, target, expectedTarget)
		}
	}
}

func TestInitCommand_SkipsCorrectSymlinks(t *testing.T) {
	skipIfSymlinkUnsupported(t)
	gitDir := setupGitRepo(t)
	defer os.RemoveAll(gitDir)

	fakeHome := setupGlobalLiza(t)

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(originalDir)
	if err := os.Chdir(gitDir); err != nil {
		t.Fatal(err)
	}

	testhelpers.CreateSpecFile(t, gitDir, "vision.md", testhelpers.ValidVisionSpec)

	// Pre-create CLAUDE.md as the correct symlink (absolute to global)
	globalDir := filepath.Join(fakeHome, ".liza")
	correctTarget := filepath.Join(globalDir, "CORE.md")
	claudePath := filepath.Join(gitDir, "CLAUDE.md")
	if err := os.Symlink(correctTarget, claudePath); err != nil {
		t.Fatal(err)
	}

	// Run init
	if err := InitCommand("Test goal", "specs/vision.md", nil); err != nil {
		t.Fatalf("InitCommand failed: %v", err)
	}

	// CLAUDE.md should still point to the same target (untouched)
	target, err := os.Readlink(claudePath)
	if err != nil {
		t.Fatalf("CLAUDE.md is no longer a symlink: %v", err)
	}
	if target != correctTarget {
		t.Errorf("CLAUDE.md target changed; got %q, want %q", target, correctTarget)
	}
}

func TestInitCommand_DoesNotOverwriteWithoutConsent(t *testing.T) {
	skipIfSymlinkUnsupported(t)
	gitDir := setupGitRepo(t)
	defer os.RemoveAll(gitDir)

	setupGlobalLiza(t)

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(originalDir)
	if err := os.Chdir(gitDir); err != nil {
		t.Fatal(err)
	}

	testhelpers.CreateSpecFile(t, gitDir, "vision.md", testhelpers.ValidVisionSpec)

	// Pre-create CLAUDE.md as a regular file
	existingContent := "# Custom contract\n"
	claudePath := filepath.Join(gitDir, "CLAUDE.md")
	if err := os.WriteFile(claudePath, []byte(existingContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Run init — stdin is not interactive in tests, so the prompt
	// will fail to read and the file should be left untouched.
	if err := InitCommand("Test goal", "specs/vision.md", nil); err != nil {
		t.Fatalf("InitCommand failed: %v", err)
	}

	// CLAUDE.md should be untouched (no consent given)
	content, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("Failed to read CLAUDE.md: %v", err)
	}
	if string(content) != existingContent {
		t.Errorf("CLAUDE.md was modified without consent; got %q, want %q", string(content), existingContent)
	}

	// AGENTS.md and GEMINI.md should still be created as symlinks
	for _, name := range []string{"AGENTS.md", "GEMINI.md"} {
		linkPath := filepath.Join(gitDir, name)
		if _, err := os.Readlink(linkPath); err != nil {
			t.Errorf("Symlink %s not created: %v", name, err)
		}
	}
}

func TestInitCommand_WritesClaudeSettings(t *testing.T) {
	// Create temporary git repo
	gitDir := setupGitRepo(t)
	defer os.RemoveAll(gitDir)

	setupGlobalLiza(t)

	// Change to temp directory
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(originalDir)
	if err := os.Chdir(gitDir); err != nil {
		t.Fatal(err)
	}

	// Setup
	testhelpers.CreateSpecFile(t, gitDir, "vision.md", testhelpers.ValidVisionSpec)

	// Run init
	err = InitCommand("Test goal", "specs/vision.md", nil)
	if err != nil {
		t.Fatalf("InitCommand failed: %v", err)
	}

	// Verify .claude directory was created
	claudeDir := filepath.Join(gitDir, ".claude")
	if _, err := os.Stat(claudeDir); os.IsNotExist(err) {
		t.Errorf(".claude directory not created")
	}

	// Verify settings.json was created
	settingsPath := filepath.Join(claudeDir, "settings.json")
	info, err := os.Stat(settingsPath)
	if os.IsNotExist(err) {
		t.Fatalf("settings.json not created")
	}

	// Verify file permissions
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0644 {
		t.Errorf("settings.json has wrong permissions: got %o, want 0644", info.Mode().Perm())
	}

	// Read and parse JSON
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("Failed to read settings.json: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(content, &settings); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v", err)
	}

	// Verify permissions structure exists
	perms, ok := settings["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions field missing or invalid type")
	}

	// Verify defaultMode
	if _, ok := perms["defaultMode"]; !ok {
		t.Errorf("permissions.defaultMode field missing")
	}

	// Verify allow array exists
	allow, ok := perms["allow"].([]any)
	if !ok {
		t.Fatalf("permissions.allow field missing or not an array")
	}

	// Verify allow array has some permissions
	if len(allow) == 0 {
		t.Errorf("permissions.allow array is empty")
	}

	// Verify key liza MCP tools are in allow array (explicit tool format)
	foundLizaMCP := false
	for _, perm := range allow {
		permStr := perm.(string)
		// Check for explicit tool format: mcp__liza__liza_add_task
		if strings.HasPrefix(permStr, "mcp__liza__") {
			foundLizaMCP = true
			break
		}
	}
	if !foundLizaMCP {
		t.Errorf("Expected liza MCP tools in allow array (e.g., mcp__liza__liza_add_task)")
	}
}
