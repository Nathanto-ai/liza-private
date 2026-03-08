package commands

import (
	"os"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// Regression tests for add-task CLI field passthrough.
//
// Bug: AcceptanceCriteria, VerifyCommands, RequirementRefs, ErrorBehavior,
// and OriginFindingID were defined in commands.TaskInput but never passed
// through to ops.AddTaskInput. Tasks created via the CLI were missing these
// fields in state.yaml.
//
// Fixed in: commit e34614e (feat: add write-checkpoint CLI command + fix add-task field passthrough)

func setupAddTaskTest(t *testing.T) (stateFile, logFile string) {
	t.Helper()
	tmpDir := t.TempDir()
	stateFile, _ = testhelpers.SetupLizaDir(t, tmpDir)
	logFile = paths.New(tmpDir).LogPath()
	testhelpers.CreateSpecFile(t, tmpDir, "vision.md", "# Vision\n")

	now := time.Now().UTC()
	state := &models.State{
		Version: 1,
		Goal: models.Goal{
			ID:               "goal-1",
			Description:      "Test goal",
			SpecRef:          "specs/vision.md",
			Created:          now,
			Status:           models.GoalStatusInProgress,
			AlignmentHistory: []models.AlignmentHistory{},
		},
		Tasks:  []models.Task{},
		Agents: make(map[string]models.Agent),
		Sprint: models.Sprint{
			ID:      "sprint-1",
			GoalRef: "goal-1",
			Scope: models.SprintScope{
				Planned: []string{},
				Stretch: []string{},
			},
			Timeline: models.SprintTimeline{Started: now},
			Status:   models.SprintStatusInProgress,
			Metrics:  models.SprintMetrics{},
		},
		CircuitBreaker: models.CircuitBreaker{
			Status:  "OK",
			History: []models.CircuitBreakerHistory{},
		},
		Config: models.Config{
			MaxCoderIterations: 10,
			MaxReviewCycles:    5,
			IntegrationBranch:  "integration",
		},
	}
	testhelpers.WriteInitialState(t, stateFile, state)
	if err := os.WriteFile(logFile, []byte{}, 0644); err != nil {
		t.Fatalf("Failed to create log file: %v", err)
	}
	return stateFile, logFile
}

// TestAddTask_AcceptanceCriteriaPassthrough verifies AcceptanceCriteria
// flows from TaskInput through to the persisted task in state.yaml.
func TestAddTask_AcceptanceCriteriaPassthrough(t *testing.T) {
	stateFile, logFile := setupAddTaskTest(t)

	input := &TaskInput{
		ID:          "task-ac",
		Description: "Task with acceptance criteria",
		SpecRef:     "specs/vision.md",
		DoneWhen:    "Criteria met",
		Scope:       "scope",
		Priority:    1,
		AcceptanceCriteria: []string{
			"Criterion A is met",
			"Criterion B is met",
			"Criterion C is met",
		},
	}

	err := AddTaskCommand(stateFile, logFile, input, "planner-1")
	if err != nil {
		t.Fatalf("AddTaskCommand() error: %v", err)
	}

	bb := db.New(stateFile)
	state, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}

	task := state.FindTask("task-ac")
	if task == nil {
		t.Fatal("Task not found")
	}
	if len(task.AcceptanceCriteria) != 3 {
		t.Fatalf("AcceptanceCriteria len = %d, want 3", len(task.AcceptanceCriteria))
	}
	if task.AcceptanceCriteria[0] != "Criterion A is met" {
		t.Errorf("AcceptanceCriteria[0] = %q, want %q", task.AcceptanceCriteria[0], "Criterion A is met")
	}
}

// TestAddTask_VerifyCommandsPassthrough verifies VerifyCommands
// flows from TaskInput through to the persisted task.
func TestAddTask_VerifyCommandsPassthrough(t *testing.T) {
	stateFile, logFile := setupAddTaskTest(t)

	input := &TaskInput{
		ID:          "task-vc",
		Description: "Task with verify commands",
		SpecRef:     "specs/vision.md",
		DoneWhen:    "Tests pass",
		Scope:       "scope",
		Priority:    1,
		VerifyCommands: []string{
			"go test ./...",
			"go vet ./...",
		},
	}

	err := AddTaskCommand(stateFile, logFile, input, "planner-1")
	if err != nil {
		t.Fatalf("AddTaskCommand() error: %v", err)
	}

	bb := db.New(stateFile)
	state, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}

	task := state.FindTask("task-vc")
	if task == nil {
		t.Fatal("Task not found")
	}
	if len(task.VerifyCommands) != 2 {
		t.Fatalf("VerifyCommands len = %d, want 2", len(task.VerifyCommands))
	}
	if task.VerifyCommands[0] != "go test ./..." {
		t.Errorf("VerifyCommands[0] = %q, want %q", task.VerifyCommands[0], "go test ./...")
	}
}

// TestAddTask_OriginFindingIDPassthrough verifies OriginFindingID
// flows through for audit-originated remediation tasks.
func TestAddTask_OriginFindingIDPassthrough(t *testing.T) {
	stateFile, logFile := setupAddTaskTest(t)

	input := &TaskInput{
		ID:              "task-remediation",
		Description:     "Fix audit finding",
		SpecRef:         "specs/vision.md",
		DoneWhen:        "Finding resolved",
		Scope:           "scope",
		Priority:        5,
		OriginFindingID: "audit-001",
	}

	err := AddTaskCommand(stateFile, logFile, input, "planner-1")
	if err != nil {
		t.Fatalf("AddTaskCommand() error: %v", err)
	}

	bb := db.New(stateFile)
	state, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}

	task := state.FindTask("task-remediation")
	if task == nil {
		t.Fatal("Task not found")
	}
	if task.OriginFindingID != "audit-001" {
		t.Errorf("OriginFindingID = %q, want %q", task.OriginFindingID, "audit-001")
	}
}

// TestAddTask_ErrorBehaviorPassthrough verifies ErrorBehavior field.
func TestAddTask_ErrorBehaviorPassthrough(t *testing.T) {
	stateFile, logFile := setupAddTaskTest(t)

	input := &TaskInput{
		ID:            "task-eb",
		Description:   "Task with error behavior",
		SpecRef:       "specs/vision.md",
		DoneWhen:      "Done",
		Scope:         "scope",
		Priority:      1,
		ErrorBehavior: "Return CalcError with descriptive message",
	}

	err := AddTaskCommand(stateFile, logFile, input, "planner-1")
	if err != nil {
		t.Fatalf("AddTaskCommand() error: %v", err)
	}

	bb := db.New(stateFile)
	state, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}

	task := state.FindTask("task-eb")
	if task == nil {
		t.Fatal("Task not found")
	}
	if task.ErrorBehavior != "Return CalcError with descriptive message" {
		t.Errorf("ErrorBehavior = %q, want %q", task.ErrorBehavior, "Return CalcError with descriptive message")
	}
}

// TestAddTask_RequirementRefsPassthrough verifies RequirementRefs field.
func TestAddTask_RequirementRefsPassthrough(t *testing.T) {
	stateFile, logFile := setupAddTaskTest(t)

	input := &TaskInput{
		ID:              "task-rr",
		Description:     "Task with requirement refs",
		SpecRef:         "specs/vision.md",
		DoneWhen:        "Done",
		Scope:           "scope",
		Priority:        1,
		RequirementRefs: []string{"R1", "R2", "R3"},
	}

	err := AddTaskCommand(stateFile, logFile, input, "planner-1")
	if err != nil {
		t.Fatalf("AddTaskCommand() error: %v", err)
	}

	bb := db.New(stateFile)
	state, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}

	task := state.FindTask("task-rr")
	if task == nil {
		t.Fatal("Task not found")
	}
	if len(task.RequirementRefs) != 3 {
		t.Fatalf("RequirementRefs len = %d, want 3", len(task.RequirementRefs))
	}
	if task.RequirementRefs[2] != "R3" {
		t.Errorf("RequirementRefs[2] = %q, want %q", task.RequirementRefs[2], "R3")
	}
}

// TestAddTask_AllExtendedFieldsPassthrough verifies all extended fields in
// a single task creation — the exact scenario that was broken before the fix.
func TestAddTask_AllExtendedFieldsPassthrough(t *testing.T) {
	stateFile, logFile := setupAddTaskTest(t)

	input := &TaskInput{
		ID:          "task-full",
		Description: "Full remediation task from audit",
		SpecRef:     "specs/vision.md",
		DoneWhen:    "All criteria met",
		Scope:       "tokenizer.go parser.go",
		Priority:    5,
		AcceptanceCriteria: []string{
			"Tokenizer recognizes percent as operator",
			"Parser handles modulo with correct precedence",
		},
		VerifyCommands:  []string{"go test ./...", "go vet ./..."},
		RequirementRefs: []string{"R2", "R6"},
		ErrorBehavior:   "Return division by zero error for modulo by zero",
		OriginFindingID: "audit-001",
	}

	err := AddTaskCommand(stateFile, logFile, input, "planner-1")
	if err != nil {
		t.Fatalf("AddTaskCommand() error: %v", err)
	}

	bb := db.New(stateFile)
	state, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}

	task := state.FindTask("task-full")
	if task == nil {
		t.Fatal("Task not found")
	}

	// Verify ALL extended fields persisted
	if len(task.AcceptanceCriteria) != 2 {
		t.Errorf("AcceptanceCriteria len = %d, want 2", len(task.AcceptanceCriteria))
	}
	if len(task.VerifyCommands) != 2 {
		t.Errorf("VerifyCommands len = %d, want 2", len(task.VerifyCommands))
	}
	if len(task.RequirementRefs) != 2 {
		t.Errorf("RequirementRefs len = %d, want 2", len(task.RequirementRefs))
	}
	if task.ErrorBehavior != "Return division by zero error for modulo by zero" {
		t.Errorf("ErrorBehavior = %q", task.ErrorBehavior)
	}
	if task.OriginFindingID != "audit-001" {
		t.Errorf("OriginFindingID = %q, want %q", task.OriginFindingID, "audit-001")
	}
}
