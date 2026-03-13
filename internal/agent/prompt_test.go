package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/errors"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// TestBuildPrompt tests the buildPrompt function
func TestBuildPrompt(t *testing.T) {
	now := time.Now().UTC()
	state := &models.State{
		Version: 1,
		Goal: models.Goal{
			ID:          "goal-1",
			Description: "Test goal",
			SpecRef:     "spec.md",
			Status:      models.GoalStatusInProgress,
			Created:     now,
		},
		Tasks: []models.Task{
			{
				ID:          "task-1",
				Description: "Test task",
				Status:      models.TaskStatusImplementing,
				Priority:    1,
				SpecRef:     "spec.md",
				DoneWhen:    "Task is complete",
				Created:     now,
			},
		},
		Agents: make(map[string]models.Agent),
		Config: models.Config{
			IntegrationBranch: "main",
		},
	}

	tests := []struct {
		name        string
		role        string
		taskID      string
		initialTask string
		wantErr     bool
		contains    []string
	}{
		{
			name:     "coder prompt",
			role:     "coder",
			taskID:   "task-1",
			wantErr:  false,
			contains: []string{"coder", "Test goal", "task-1"},
		},
		{
			name:     "code-reviewer prompt",
			role:     "code-reviewer",
			taskID:   "task-1",
			wantErr:  false,
			contains: []string{"reviewer", "Test goal", "task-1"},
		},
		{
			name:     "planner prompt",
			role:     "planner",
			taskID:   "",
			wantErr:  false,
			contains: []string{"planner", "Test goal"},
		},
		{
			name:     "coder with non-existent task",
			role:     "coder",
			taskID:   "task-999",
			wantErr:  true,
			contains: nil,
		},
		{
			name:        "coder with initial task",
			role:        "coder",
			taskID:      "task-1",
			initialTask: "task-1",
			wantErr:     false,
			contains:    []string{"coder", "RESUME CONTEXT", "task-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			// Create the spec file on disk so planner validation can read it
			if err := os.WriteFile(filepath.Join(tmpDir, "spec.md"), []byte(testhelpers.ValidVisionSpec), 0644); err != nil {
				t.Fatal(err)
			}

			config := SupervisorConfig{
				Role:        tt.role,
				AgentID:     tt.role + "-1",
				ProjectRoot: tmpDir,
				SpecsDir:    filepath.Join(tmpDir, "specs"),
				StatePath:   filepath.Join(tmpDir, "state.yaml"),
				InitialTask: tt.initialTask,
			}

			prompt, err := buildPrompt(state, config, tt.taskID)

			if (err != nil) != tt.wantErr {
				t.Errorf("buildPrompt() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if !errors.IsNotFound(err) {
					t.Errorf("expected NotFoundError, got %T: %v", err, err)
				}
				return
			}

			// Check that prompt contains expected strings
			for _, expected := range tt.contains {
				if !strings.Contains(prompt, expected) {
					t.Errorf("buildPrompt() prompt should contain %q", expected)
				}
			}

			// Verify prompt is not empty
			if prompt == "" {
				t.Error("buildPrompt() returned empty prompt")
			}
		})
	}
}

// TestPromptSaving tests prompt file creation
func TestPromptSaving(t *testing.T) {
	tmpDir := t.TempDir()
	promptDir := filepath.Join(tmpDir, "agent-prompts")

	prompt := "Test prompt content"
	agentID := "coder-1"

	filePath, err := savePrompt(promptDir, agentID, prompt)
	if err != nil {
		t.Fatalf("savePrompt() error = %v", err)
	}

	// Verify directory was created
	if _, err := os.Stat(promptDir); os.IsNotExist(err) {
		t.Error("Prompt directory should be created")
	}

	// Verify file exists and has correct content
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read prompt file: %v", err)
	}

	if string(content) != prompt {
		t.Errorf("Prompt content = %q, want %q", string(content), prompt)
	}

	// Verify filename format
	filename := filepath.Base(filePath)
	if !strings.HasPrefix(filename, agentID+"-") {
		t.Errorf("Filename should start with agent ID, got %s", filename)
	}
	if !strings.HasSuffix(filename, ".txt") {
		t.Errorf("Filename should end with .txt, got %s", filename)
	}
}

// TestSavePromptMultipleCalls tests that savePrompt creates unique filenames
func TestSavePromptMultipleCalls(t *testing.T) {
	tmpDir := t.TempDir()
	promptDir := filepath.Join(tmpDir, "prompts")

	// Save multiple prompts
	path1, err := savePrompt(promptDir, "coder-1", "prompt 1")
	if err != nil {
		t.Fatalf("savePrompt() error = %v", err)
	}

	// savePrompt uses second-resolution timestamps (20060102-150405).
	// Wait until the wall-clock second advances so the next call produces
	// a distinct filename, instead of polling savePrompt in a tight loop
	// which would create dozens of duplicate files as a side effect.
	startSec := time.Now().UTC().Truncate(time.Second)
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for time.Now().UTC().Truncate(time.Second).Equal(startSec) {
		select {
		case <-deadline:
			t.Fatal("wall-clock second did not advance within timeout")
		case <-ticker.C:
		}
	}

	path2, err := savePrompt(promptDir, "coder-1", "prompt 2")
	if err != nil {
		t.Fatalf("savePrompt() error = %v", err)
	}

	// Verify paths are different
	if path1 == path2 {
		t.Error("savePrompt() should create unique filenames")
	}

	// Verify both files exist
	if _, err := os.Stat(path1); os.IsNotExist(err) {
		t.Error("First prompt file should exist")
	}
	if _, err := os.Stat(path2); os.IsNotExist(err) {
		t.Error("Second prompt file should exist")
	}
}

// TestOutputSaving tests agent output file creation
func TestOutputSaving(t *testing.T) {
	tmpDir := t.TempDir()
	outputsDir := filepath.Join(tmpDir, "agent-outputs")

	output := "Test agent output content"
	agentID := "claude-1"

	filePath, err := saveOutput(outputsDir, agentID, "txt", output)
	if err != nil {
		t.Fatalf("saveOutput() error = %v", err)
	}

	// Verify directory was created
	if _, err := os.Stat(outputsDir); os.IsNotExist(err) {
		t.Error("Outputs directory should be created")
	}

	// Verify file exists and has correct content
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read output file: %v", err)
	}

	if string(content) != output {
		t.Errorf("Output content = %q, want %q", string(content), output)
	}

	// Verify filename format
	filename := filepath.Base(filePath)
	if !strings.HasPrefix(filename, agentID+"-") {
		t.Errorf("Filename should start with agent ID, got %s", filename)
	}
	if !strings.HasSuffix(filename, ".txt") {
		t.Errorf("Filename should end with .txt, got %s", filename)
	}
}
