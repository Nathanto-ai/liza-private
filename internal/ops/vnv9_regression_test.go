package ops

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/liza-mas/liza/internal/testhelpers"
)

// Regression tests for V&V Run 9 — Fix 45: Planner meta-task rejection.

// TestFix45_RejectsFrameworkMetaTaskDescription verifies that AddTask rejects
// tasks whose description references liza-internal framework terms.
// Bug: In Run 8, the planner created 8 meta-tasks (repair-coder-submission-workflow,
// investigate-coder-submission-loop, test-coder-submission-workflow, etc.) that
// the coder cannot implement because they target orchestrator behavior.
func TestFix45_RejectsFrameworkMetaTaskDescription(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		desc string
		term string
	}{
		{"submit_for_review", "Repair coder so it calls liza_submit_for_review correctly", "liza_submit_for_review"},
		{"task_complete_loop", "Investigate why coder enters task_complete loop", "task_complete loop"},
		{"task_complete_tool", "Fix coder to stop calling task_complete tool", "task_complete tool"},
		{"mcp_tool", "Ensure coder uses MCP tool for submission", "mcp tool"},
		{"coder_agent", "Debug coder agent submission workflow", "coder agent"},
		{"coder_submission", "Repair coder submission process", "coder submission"},
		{"submission_workflow", "Fix broken submission workflow in pipeline", "submission workflow"},
		{"supervisor", "Modify supervisor to handle edge case", "supervisor"},
		{"copilot_native", "Stop coder from using copilot-native task_complete", "copilot-native"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := &AddTaskInput{
				ID:          "meta-task-" + tc.name,
				Description: tc.desc,
				SpecRef:     "specs/vision.md",
				DoneWhen:    "tests pass",
				Scope:       "pipeline",
				Priority:    1,
			}
			err := rejectFrameworkMetaTask(input)
			if err == nil {
				t.Errorf("expected rejection for description containing %q, got nil", tc.term)
			}
			if err != nil && !strings.Contains(err.Error(), tc.term) {
				t.Errorf("expected error to mention %q, got: %v", tc.term, err)
			}
		})
	}
}

// TestFix45_RejectsFrameworkMetaTaskDoneWhen verifies rejection also works
// when the framework term appears in done_when instead of description.
func TestFix45_RejectsFrameworkMetaTaskDoneWhen(t *testing.T) {
	t.Parallel()

	input := &AddTaskInput{
		ID:          "meta-donewhen",
		Description: "Fix submission issues",
		SpecRef:     "specs/vision.md",
		DoneWhen:    "coder agent calls liza_submit_for_review successfully",
		Scope:       "pipeline",
		Priority:    1,
	}
	err := rejectFrameworkMetaTask(input)
	if err == nil {
		t.Error("expected rejection for done_when containing framework term")
	}
}

// TestFix45_AcceptsNormalTask verifies that normal application tasks are NOT
// rejected by the framework meta-task filter.
func TestFix45_AcceptsNormalTask(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		desc string
	}{
		{"crud", "Implement CRUD handlers for recipe API"},
		{"middleware", "Add request logging middleware and graceful shutdown"},
		{"tests", "Write unit and integration tests for all endpoints"},
		{"validation", "Add input validation for recipe fields"},
		{"gitignore", "Create .gitignore with Go build artifacts"},
		{"data_model", "Implement data model and in-memory store"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := &AddTaskInput{
				ID:          "normal-" + tc.name,
				Description: tc.desc,
				SpecRef:     "specs/vision.md",
				DoneWhen:    "go test ./... passes",
				Scope:       "module",
				Priority:    1,
			}
			err := rejectFrameworkMetaTask(input)
			if err != nil {
				t.Errorf("normal task %q rejected: %v", tc.desc, err)
			}
		})
	}
}

// TestFix45_CaseInsensitive verifies the check is case-insensitive.
func TestFix45_CaseInsensitive(t *testing.T) {
	t.Parallel()

	input := &AddTaskInput{
		ID:          "meta-upper",
		Description: "Fix CODER AGENT to call LIZA_SUBMIT_FOR_REVIEW",
		SpecRef:     "specs/vision.md",
		DoneWhen:    "done",
		Scope:       "pipeline",
		Priority:    1,
	}
	err := rejectFrameworkMetaTask(input)
	if err == nil {
		t.Error("expected rejection for uppercase framework terms")
	}
}

// TestFix45_AddTaskIntegration verifies the rejection works through the full
// AddTask pipeline (not just the helper function).
func TestFix45_AddTaskIntegration(t *testing.T) {
	t.Parallel()

	state := testhelpers.CreateValidState()
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.CreateSpecFile(t, tmpDir, "vision.md", "# Vision\n")
	testhelpers.WriteInitialState(t, statePath, state)
	logPath := filepath.Join(tmpDir, ".liza", "liza.log")

	input := &AddTaskInput{
		ID:          "repair-coder-submission-workflow",
		Description: "Repair coder submission workflow so it uses liza_submit_for_review",
		SpecRef:     "specs/vision.md",
		DoneWhen:    "coder agent submits via MCP tool",
		Scope:       "pipeline",
		Priority:    1,
	}

	_, err := AddTask(statePath, logPath, input, "planner-1")
	if err == nil {
		t.Fatal("expected AddTask to reject framework meta-task, but it succeeded")
	}
	if !strings.Contains(err.Error(), "framework-internal") {
		t.Errorf("expected error about framework-internal term, got: %v", err)
	}
}
