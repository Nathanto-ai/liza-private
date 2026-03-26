package prompts

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// Regression tests for V&V Run 10 fixes — prompt layer.

// TestFix49_CoderPromptStripsRaceFromVerifyCommands verifies that on Windows,
// verify_commands containing -race are sanitized before being rendered into
// the coder prompt. This prevents coders from seeing unrunnable commands.
func TestFix49_CoderPromptStripsRaceFromVerifyCommands(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now)
	task.Description = "Implement storage"
	task.DoneWhen = "go test -race ./... passes"
	task.VerifyCommands = []string{"go test -race -v ./...", "go vet ./..."}

	config := CoderContextConfig{
		ProjectRoot: "/project",
		AgentID:     "coder-1",
	}

	prompt, err := BuildCoderContext(&task, config)
	if err != nil {
		t.Fatalf("BuildCoderContext() error: %v", err)
	}

	if runtime.GOOS == "windows" {
		// The rendered verify command list items should not contain -race.
		// Static template guidance text may reference -race as a warning — that's OK.
		for _, line := range strings.Split(prompt, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "- go test") && strings.Contains(trimmed, "-race") {
				t.Errorf("Coder prompt verify command should NOT contain -race on Windows (Fix 49): %s", trimmed)
			}
		}
		// DONE WHEN line should show the sanitized value
		for _, line := range strings.Split(prompt, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(trimmed, "go test") && strings.Contains(trimmed, "passes") {
				if strings.Contains(trimmed, "-race") {
					t.Error("Coder prompt DONE WHEN value should NOT contain -race on Windows (Fix 49)")
				}
			}
		}
	}
	// On non-Windows, -race should remain in verify_commands
	if runtime.GOOS != "windows" {
		found := false
		for _, line := range strings.Split(prompt, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "- go test") && strings.Contains(line, "-race") {
				found = true
				break
			}
		}
		if !found {
			t.Error("Coder prompt should preserve -race in verify commands on non-Windows")
		}
	}
}

// TestFix49_ReviewerPromptStripsRaceFromVerifyCommands verifies that on Windows,
// verify_commands containing -race are sanitized in the reviewer prompt.
// Bug: Reviewer saw raw verify_commands with -race, tried to run them,
// got CGO errors, and rejected the task — creating a rejection cycle.
func TestFix49_ReviewerPromptStripsRaceFromVerifyCommands(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now)
	task.Description = "Implement storage"
	task.DoneWhen = "go test -race ./... passes"
	task.VerifyCommands = []string{"go test -race -count=1 ./..."}
	reviewCommit := "abc123"
	task.ReviewCommit = &reviewCommit

	config := ReviewerContextConfig{
		ProjectRoot: "/project",
		AgentID:     "reviewer-1",
	}

	prompt, err := BuildReviewerContext(&task, config)
	if err != nil {
		t.Fatalf("BuildReviewerContext() error: %v", err)
	}

	if runtime.GOOS == "windows" {
		// The verify commands section should not contain "go test -race"
		// (the template guidance text may reference -race as a warning)
		if strings.Contains(prompt, "go test -race") {
			t.Error("Reviewer prompt should NOT contain 'go test -race' on Windows (Fix 49)")
		}
	}
}

// TestFix49_SanitizeDoesNotMutateOriginalTask verifies that sanitizeTaskForPrompt
// returns a copy and does not modify the original task's verify_commands.
func TestFix49_SanitizeDoesNotMutateOriginalTask(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now)
	task.VerifyCommands = []string{"go test -race ./..."}
	original := task.VerifyCommands[0]

	_ = sanitizeTaskForPrompt(&task)

	if task.VerifyCommands[0] != original {
		t.Errorf("sanitizeTaskForPrompt mutated original task: got %q, want %q",
			task.VerifyCommands[0], original)
	}
}

// TestFix51_PlannerPromptHasEnvironmentConstraints verifies the planner prompt
// includes ENVIRONMENT CONSTRAINTS preventing -race in verify_commands on Windows.
func TestFix51_PlannerPromptHasEnvironmentConstraints(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	state := &models.State{
		Version: 1,
		Goal: models.Goal{
			ID:          "goal-1",
			Description: "Test goal",
			SpecRef:     "spec.md",
			Created:     now,
			Status:      models.GoalStatusInProgress,
		},
		Tasks:  []models.Task{},
		Agents: map[string]models.Agent{},
		Config: models.Config{
			HeartbeatInterval: 60,
			LeaseDuration:     1800,
			MaxReviewCycles:   5,
		},
	}

	config := PlannerContextConfig{}
	prompt, err := BuildPlannerContext(state, config)
	if err != nil {
		t.Fatalf("BuildPlannerContext() error: %v", err)
	}

	if !strings.Contains(prompt, "ENVIRONMENT CONSTRAINTS") {
		t.Error("Planner prompt must include ENVIRONMENT CONSTRAINTS section (Fix 51)")
	}
	if !strings.Contains(prompt, "Do NOT include `-race`") {
		t.Error("Planner prompt must warn against -race in verify_commands (Fix 51)")
	}
}
