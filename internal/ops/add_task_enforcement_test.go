package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// TestAddTask_RequirementRefs tests that requirement_refs are persisted correctly.
func TestAddTask_RequirementRefs(t *testing.T) {
	t.Parallel()

	state := testhelpers.CreateValidState()
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.CreateSpecFile(t, tmpDir, "vision.md", "# Vision\n")
	bb := testhelpers.WriteInitialState(t, statePath, state)
	logPath := filepath.Join(tmpDir, ".liza", "liza.log")

	input := &AddTaskInput{
		ID:                 "task-with-refs",
		Description:        "Implement feature X",
		SpecRef:            "specs/vision.md",
		DoneWhen:           "tests pass",
		Scope:              "module X",
		Priority:           1,
		RequirementRefs:    []string{"R1", "R2"},
		AcceptanceCriteria: []string{"AC-1: feature works"},
		VerifyCommands:     []string{"pytest tests/test_x.py"},
		ErrorBehavior:      "return typed error on invalid input",
	}

	result, err := AddTask(statePath, logPath, input, "planner-1")
	if err != nil {
		t.Fatalf("AddTask failed: %v", err)
	}
	if result.TaskID != "task-with-refs" {
		t.Errorf("expected task ID 'task-with-refs', got %s", result.TaskID)
	}

	// Read back and verify fields
	finalState, err := bb.Read()
	if err != nil {
		t.Fatalf("failed to read state: %v", err)
	}
	task := finalState.FindTask("task-with-refs")
	if task == nil {
		t.Fatal("task not found after add")
	}

	if len(task.RequirementRefs) != 2 || task.RequirementRefs[0] != "R1" || task.RequirementRefs[1] != "R2" {
		t.Errorf("RequirementRefs = %v, want [R1 R2]", task.RequirementRefs)
	}
	if len(task.AcceptanceCriteria) != 1 || task.AcceptanceCriteria[0] != "AC-1: feature works" {
		t.Errorf("AcceptanceCriteria = %v, want [AC-1: feature works]", task.AcceptanceCriteria)
	}
	if len(task.VerifyCommands) != 1 || task.VerifyCommands[0] != "pytest tests/test_x.py" {
		t.Errorf("VerifyCommands = %v, want [pytest tests/test_x.py]", task.VerifyCommands)
	}
	if task.ErrorBehavior != "return typed error on invalid input" {
		t.Errorf("ErrorBehavior = %q, want 'return typed error on invalid input'", task.ErrorBehavior)
	}
}

// TestAddTask_EnforceRequirementRefs tests that missing refs are rejected when enforcement is on.
func TestAddTask_EnforceRequirementRefs(t *testing.T) {
	t.Parallel()

	state := testhelpers.CreateValidState()
	state.Config.EnforceRequirementRefs = true

	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.CreateSpecFile(t, tmpDir, "vision.md", "# Vision\n")
	testhelpers.WriteInitialState(t, statePath, state)
	logPath := filepath.Join(tmpDir, ".liza", "liza.log")

	input := &AddTaskInput{
		ID:          "task-no-refs",
		Description: "Implement feature Y",
		SpecRef:     "specs/vision.md",
		DoneWhen:    "tests pass",
		Scope:       "module Y",
		Priority:    1,
		// No RequirementRefs — should fail!
	}

	_, err := AddTask(statePath, logPath, input, "planner-1")
	if err == nil {
		t.Fatal("expected error for missing requirement_refs with enforcement on")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "requirement_refs required") {
		t.Errorf("error = %q, want containing 'requirement_refs required'", err.Error())
	}
}

// TestAddTask_EnforceDeduplication tests that duplicate tasks are rejected.
func TestAddTask_EnforceDeduplication(t *testing.T) {
	t.Parallel()

	state := testhelpers.CreateValidState()
	state.Config.EnforceDeduplication = true
	state.Tasks = append(state.Tasks, models.Task{
		ID:          "existing-task",
		Description: "Implement feature Z",
		Status:      models.TaskStatusReady,
		Priority:    1,
		Scope:       "module Z",
		SpecRef:     "specs/vision.md",
		DoneWhen:    "tests pass",
		Created:     state.Goal.Created,
		History:     []models.TaskHistoryEntry{},
	})

	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.CreateSpecFile(t, tmpDir, "vision.md", "# Vision\n")
	testhelpers.WriteInitialState(t, statePath, state)
	logPath := filepath.Join(tmpDir, ".liza", "liza.log")

	input := &AddTaskInput{
		ID:          "duplicate-task",
		Description: "Implement feature Z", // same description
		SpecRef:     "specs/vision.md",
		DoneWhen:    "tests pass",
		Scope:       "module Z", // same scope
		Priority:    2,
	}

	_, err := AddTask(statePath, logPath, input, "planner-1")
	if err == nil {
		t.Fatal("expected error for duplicate task")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
		t.Errorf("error = %q, want containing 'duplicate'", err.Error())
	}
}

// TestAddTask_DeduplicationBlocksSameDescription tests that tasks with the same
// description are always rejected, regardless of scope differences.
// This prevents planner LLMs from creating near-duplicate remediation tasks.
func TestAddTask_DeduplicationBlocksSameDescription(t *testing.T) {
	t.Parallel()

	state := testhelpers.CreateValidState()
	state.Tasks = append(state.Tasks, models.Task{
		ID:          "existing-task",
		Description: "Implement feature Z",
		Status:      models.TaskStatusReady,
		Priority:    1,
		Scope:       "module Z",
		SpecRef:     "specs/vision.md",
		DoneWhen:    "tests pass",
		Created:     state.Goal.Created,
		History:     []models.TaskHistoryEntry{},
	})

	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.CreateSpecFile(t, tmpDir, "vision.md", "# Vision\n")
	testhelpers.WriteInitialState(t, statePath, state)
	logPath := filepath.Join(tmpDir, ".liza", "liza.log")

	input := &AddTaskInput{
		ID:          "different-scope-task",
		Description: "Implement feature Z", // same description
		SpecRef:     "specs/vision.md",
		DoneWhen:    "tests pass",
		Scope:       "module A", // different scope — still blocked by description dedup
		Priority:    2,
	}

	_, err := AddTask(statePath, logPath, input, "planner-1")
	if err == nil {
		t.Fatal("expected error for duplicate description, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected duplicate error, got: %v", err)
	}
}

// TestAddTask_DeduplicationIgnoresTerminalTasks tests dedup skips abandoned/superseded.
func TestAddTask_DeduplicationIgnoresTerminalTasks(t *testing.T) {
	t.Parallel()

	state := testhelpers.CreateValidState()
	state.Config.EnforceDeduplication = true
	state.Tasks = append(state.Tasks, models.Task{
		ID:          "abandoned-task",
		Description: "Implement feature Z",
		Status:      models.TaskStatusAbandoned,
		Priority:    1,
		Scope:       "module Z",
		SpecRef:     "specs/vision.md",
		DoneWhen:    "tests pass",
		Created:     state.Goal.Created,
		History:     []models.TaskHistoryEntry{},
	})

	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.CreateSpecFile(t, tmpDir, "vision.md", "# Vision\n")
	testhelpers.WriteInitialState(t, statePath, state)
	logPath := filepath.Join(tmpDir, ".liza", "liza.log")

	input := &AddTaskInput{
		ID:          "new-task-z",
		Description: "Implement feature Z", // same description as abandoned
		SpecRef:     "specs/vision.md",
		DoneWhen:    "tests pass",
		Scope:       "module Z", // same scope
		Priority:    2,
	}

	result, err := AddTask(statePath, logPath, input, "planner-1")
	if err != nil {
		t.Fatalf("expected success, abandoned task should be ignored for dedup: %v", err)
	}
	if result.TaskID != "new-task-z" {
		t.Errorf("unexpected task ID: %s", result.TaskID)
	}
}

// setupForAddTask creates a temp dir with git repo, liza dir, spec file, and returns paths.
func setupForAddTask(t *testing.T, state *models.State) (statePath, logPath string) {
	t.Helper()
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ = testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.CreateSpecFile(t, tmpDir, "vision.md", "# Vision\n")
	testhelpers.WriteInitialState(t, statePath, state)
	logPath = filepath.Join(tmpDir, ".liza", "liza.log")
	// Ensure log directory exists
	os.MkdirAll(filepath.Dir(logPath), 0755)
	return statePath, logPath
}

// TestAddTask_OriginFindingAutoLink verifies that passing origin_finding_id
// automatically sets LinkedTaskID on the matching audit finding.
func TestAddTask_OriginFindingAutoLink(t *testing.T) {
	t.Parallel()

	state := testhelpers.CreateValidState()
	state.AuditFindings = []models.AuditFinding{
		{
			ID:             "cli-001",
			TaskID:         "some-task",
			Severity:       "MEDIUM",
			Type:           "QUALITY_ISSUE",
			Classification: "REMEDIATE_WITH_TASK",
			Evidence:       "bad error handling",
		},
		{
			ID:             "cli-002",
			TaskID:         "some-task",
			Severity:       "LOW",
			Type:           "MISSING_EDGE_CASE",
			Classification: "LOG_ONLY",
			Evidence:       "missing edge case tests",
		},
	}

	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.CreateSpecFile(t, tmpDir, "vision.md", "# Vision\n")
	bb := testhelpers.WriteInitialState(t, statePath, state)
	logPath := filepath.Join(tmpDir, ".liza", "liza.log")
	os.MkdirAll(filepath.Dir(logPath), 0755)

	input := &AddTaskInput{
		ID:              "fix-errors",
		Description:     "Fix error handling for cli-001",
		SpecRef:         "specs/vision.md",
		DoneWhen:        "errors.Is used",
		Scope:           "CLI error handling",
		Priority:        1,
		OriginFindingID: "cli-001",
		RequirementRefs: []string{"R1"},
	}

	result, err := AddTask(statePath, logPath, input, "planner-1")
	if err != nil {
		t.Fatalf("AddTask failed: %v", err)
	}
	if result.TaskID != "fix-errors" {
		t.Fatalf("wrong task ID: %s", result.TaskID)
	}

	// Verify the finding was auto-linked
	finalState, err := bb.Read()
	if err != nil {
		t.Fatalf("failed to read state: %v", err)
	}

	var found bool
	for _, f := range finalState.AuditFindings {
		if f.ID == "cli-001" {
			found = true
			if f.LinkedTaskID != "fix-errors" {
				t.Errorf("cli-001 LinkedTaskID = %q, want %q", f.LinkedTaskID, "fix-errors")
			}
		}
		if f.ID == "cli-002" && f.LinkedTaskID != "" {
			t.Errorf("cli-002 should NOT be linked, got LinkedTaskID = %q", f.LinkedTaskID)
		}
	}
	if !found {
		t.Fatal("cli-001 finding not found in state")
	}

	// Verify the task itself has OriginFindingID
	task := finalState.FindTask("fix-errors")
	if task == nil {
		t.Fatal("task not found")
	}
	if task.OriginFindingID != "cli-001" {
		t.Errorf("task OriginFindingID = %q, want %q", task.OriginFindingID, "cli-001")
	}
}
