package ops

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// Regression tests for V&V Run 6 fixes — ops layer.

// TestFix32_AddTaskRejectsNonexistentSpecRef verifies that AddTask rejects
// tasks with a spec_ref pointing to a file that does not exist, BEFORE writing
// to state. This prevents partial/corrupt state entries.
// Bug: liza_add_task wrote task to state.yaml even when spec_ref file was missing (ISSUE-R6-03).
// Fix: Pre-write spec_ref file existence check before bb.Modify.
func TestFix32_AddTaskRejectsNonexistentSpecRef(t *testing.T) {
	t.Parallel()

	state := testhelpers.CreateValidState()
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	// Do NOT create the spec file — this should cause rejection
	testhelpers.WriteInitialState(t, statePath, state)
	logPath := filepath.Join(tmpDir, ".liza", "liza.log")

	input := &AddTaskInput{
		ID:          "task-bad-spec",
		Description: "Implement feature with bad spec",
		SpecRef:     "specs/nonexistent.md", // file does not exist
		DoneWhen:    "tests pass",
		Scope:       "module X",
		Priority:    1,
	}

	_, err := AddTask(statePath, logPath, input, "planner-1")
	if err == nil {
		t.Fatal("expected error for nonexistent spec_ref, but AddTask succeeded")
	}
	if !strings.Contains(err.Error(), "spec_ref file not found") {
		t.Errorf("error = %q, want containing 'spec_ref file not found'", err.Error())
	}

	// Verify the task was NOT written to state
	bb := testhelpers.WriteInitialState(t, statePath, state) // re-read original state
	finalState, err := bb.Read()
	if err != nil {
		t.Fatalf("failed to read state: %v", err)
	}
	if finalState.FindTask("task-bad-spec") != nil {
		t.Error("task with bad spec_ref should NOT have been written to state")
	}
}

// TestFix32_AddTaskAcceptsExistingSpecRef verifies that AddTask still works
// when the spec_ref file exists.
func TestFix32_AddTaskAcceptsExistingSpecRef(t *testing.T) {
	t.Parallel()

	state := testhelpers.CreateValidState()
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.CreateSpecFile(t, tmpDir, "vision.md", "# Vision\n")
	testhelpers.WriteInitialState(t, statePath, state)
	logPath := filepath.Join(tmpDir, ".liza", "liza.log")

	input := &AddTaskInput{
		ID:          "task-good-spec",
		Description: "Implement feature with good spec",
		SpecRef:     "specs/vision.md", // file exists
		DoneWhen:    "tests pass",
		Scope:       "module Y",
		Priority:    1,
	}

	result, err := AddTask(statePath, logPath, input, "planner-1")
	if err != nil {
		t.Fatalf("AddTask failed: %v", err)
	}
	if result.TaskID != "task-good-spec" {
		t.Errorf("expected task ID 'task-good-spec', got %s", result.TaskID)
	}
}

// TestFix32_AddTaskSpecRefWithAnchor verifies that spec_ref with a #anchor
// is resolved to the base file for existence check.
func TestFix32_AddTaskSpecRefWithAnchor(t *testing.T) {
	t.Parallel()

	state := testhelpers.CreateValidState()
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.CreateSpecFile(t, tmpDir, "vision.md", "# Vision\n## Section\n")
	testhelpers.WriteInitialState(t, statePath, state)
	logPath := filepath.Join(tmpDir, ".liza", "liza.log")

	input := &AddTaskInput{
		ID:          "task-anchored",
		Description: "Feature from spec section",
		SpecRef:     "specs/vision.md#section", // anchor should be stripped
		DoneWhen:    "tests pass",
		Scope:       "module Z",
		Priority:    1,
	}

	result, err := AddTask(statePath, logPath, input, "planner-1")
	if err != nil {
		t.Fatalf("AddTask with anchor failed: %v", err)
	}
	if result.TaskID != "task-anchored" {
		t.Errorf("expected task ID 'task-anchored', got %s", result.TaskID)
	}
}

// TestFix30_ActiveOriginTaskRejection verifies that remediation tasks are
// rejected when the origin task is still active (IMPLEMENTING, REVIEWING, etc.)
// Bug from Run 5: Planner created duplicate remediation while origin was active.
func TestFix30_ActiveOriginTaskRejection(t *testing.T) {
	t.Parallel()

	for _, activeStatus := range []models.TaskStatus{
		models.TaskStatusImplementing,
		models.TaskStatusReadyForReview,
		models.TaskStatusReviewing,
		models.TaskStatusRejected,
	} {
		t.Run(string(activeStatus), func(t *testing.T) {
			t.Parallel()
			state := testhelpers.CreateValidState()

			agent := "coder-1"
			originTask := models.Task{
				ID:          "origin-task",
				Description: "Original feature",
				Status:      activeStatus,
				Priority:    1,
				Scope:       "module X",
				SpecRef:     "specs/vision.md",
				DoneWhen:    "tests pass",
				Created:     state.Goal.Created,
				History:     []models.TaskHistoryEntry{},
				AssignedTo:  &agent,
			}
			state.Tasks = append(state.Tasks, originTask)

			tmpDir := t.TempDir()
			testhelpers.SetupTestGitRepo(t, tmpDir)
			statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
			testhelpers.CreateSpecFile(t, tmpDir, "vision.md", "# Vision\n")
			testhelpers.WriteInitialState(t, statePath, state)
			logPath := filepath.Join(tmpDir, ".liza", "liza.log")

			input := &AddTaskInput{
				ID:           "remediation-task",
				Description:  "Fix origin task issues",
				SpecRef:      "specs/vision.md",
				DoneWhen:     "fix applied",
				Scope:        "module X remediation",
				Priority:     1,
				OriginTaskID: "origin-task",
			}

			_, err := AddTask(statePath, logPath, input, "planner-1")
			if err == nil {
				t.Fatalf("expected error when origin task is %s", activeStatus)
			}
			if !strings.Contains(err.Error(), "still active") {
				t.Errorf("error = %q, want containing 'still active'", err.Error())
			}
		})
	}
}
