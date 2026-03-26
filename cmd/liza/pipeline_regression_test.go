package main

import (
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// Regression tests for production bugs found during liza-calculator pipeline testing.

// TestWriteCheckpointCommandWiring verifies the write-checkpoint CLI command
// exists and correctly wires flags to the ops layer.
//
// Bug: write-checkpoint only existed as an MCP tool, not a CLI command.
// Coders using the CLI could not write pre-execution checkpoints, blocking
// the submit-for-review flow which requires a checkpoint.
//
// Fixed in: commit e34614e
func TestWriteCheckpointCommandWiring(t *testing.T) {
	t.Run("write-checkpoint writes checkpoint to task history", func(t *testing.T) {
		projectRoot, statePath := setupMutationTestProject(t, func(state *models.State) {
			now := time.Now().UTC()
			state.Tasks = []models.Task{
				testhelpers.BuildTaskByStatus("task-wc", models.TaskStatusImplementing, now),
			}
		})

		err := executeRootCommand(t, projectRoot,
			"write-checkpoint", "task-wc",
			"--agent-id", "coder-1",
			"--intent", "Implement feature X",
			"--validation-plan", "Run go test ./...",
			"--files", "main.go,main_test.go",
		)
		if err != nil {
			t.Fatalf("write-checkpoint failed: %v", err)
		}

		state := readState(t, statePath)
		task := mustFindTask(t, state, "task-wc")

		// Find the checkpoint history entry
		found := false
		for _, entry := range task.History {
			if entry.Event == "pre_execution_checkpoint" {
				found = true
				if intent, ok := entry.Extra["intent"]; !ok || intent != "Implement feature X" {
					t.Errorf("intent = %v, want 'Implement feature X'", intent)
				}
				if vp, ok := entry.Extra["validation_plan"]; !ok || vp != "Run go test ./..." {
					t.Errorf("validation_plan = %v, want 'Run go test ./...'", vp)
				}
				files, ok := entry.Extra["files_to_modify"]
				if !ok {
					t.Error("files_to_modify not found in checkpoint")
				} else if fileList, ok := files.([]interface{}); !ok || len(fileList) != 2 {
					t.Errorf("files_to_modify = %v, want 2-element list", files)
				}
				break
			}
		}
		if !found {
			t.Fatal("pre_execution_checkpoint entry not found in task history")
		}
	})

	t.Run("write-checkpoint rejects wrong agent", func(t *testing.T) {
		projectRoot, _ := setupMutationTestProject(t, func(state *models.State) {
			now := time.Now().UTC()
			state.Tasks = []models.Task{
				testhelpers.BuildTaskByStatus("task-wrong-agent", models.TaskStatusImplementing, now),
			}
		})

		// Task is assigned to coder-1 but we claim to be coder-99
		err := executeRootCommand(t, projectRoot,
			"write-checkpoint", "task-wrong-agent",
			"--agent-id", "coder-99",
			"--intent", "test intent",
			"--validation-plan", "test plan",
			"--files", "file.go",
		)
		if err == nil {
			t.Fatal("expected error for wrong agent, got nil")
		}
	})

	t.Run("write-checkpoint rejects wrong task status", func(t *testing.T) {
		projectRoot, _ := setupMutationTestProject(t, func(state *models.State) {
			now := time.Now().UTC()
			state.Tasks = []models.Task{
				testhelpers.BuildTaskByStatus("task-ready", models.TaskStatusReady, now),
			}
		})

		err := executeRootCommand(t, projectRoot,
			"write-checkpoint", "task-ready",
			"--agent-id", "coder-1",
			"--intent", "test intent",
			"--validation-plan", "test plan",
			"--files", "file.go",
		)
		if err == nil {
			t.Fatal("expected error for wrong task status, got nil")
		}
	})
}

// TestAddTaskExtendedFieldsWiring verifies the CLI add-task command passes
// acceptance-criteria, verify-commands, requirement-refs, error-behavior,
// and origin-finding-id through to the persisted task.
//
// Bug: TaskInput had these fields but AddTaskCommand never passed them to
// ops.AddTaskInput, so all extended fields were silently dropped.
//
// Fixed in: commit e34614e
func TestAddTaskExtendedFieldsWiring(t *testing.T) {
	t.Run("add-task passes acceptance-criteria and verify-commands", func(t *testing.T) {
		projectRoot, statePath := setupMutationTestProject(t, nil)
		testhelpers.CreateSpecFile(t, projectRoot, "vision.md", "# Vision\n")

		err := executeRootCommand(t, projectRoot,
			"add-task",
			"--id", "task-ext",
			"--desc", "Extended task",
			"--spec", "specs/vision.md",
			"--done", "Done",
			"--scope", "scope",
			"--priority", "1",
			"--acceptance-criteria", "Criterion A,Criterion B",
			"--verify-commands", "go test ./...,go vet ./...",
			"--agent-id", "planner-1",
		)
		if err != nil {
			t.Fatalf("add-task failed: %v", err)
		}

		state := readState(t, statePath)
		task := mustFindTask(t, state, "task-ext")

		if len(task.AcceptanceCriteria) != 2 {
			t.Fatalf("AcceptanceCriteria len = %d, want 2", len(task.AcceptanceCriteria))
		}
		if task.AcceptanceCriteria[0] != "Criterion A" {
			t.Errorf("AcceptanceCriteria[0] = %q, want %q", task.AcceptanceCriteria[0], "Criterion A")
		}
		if len(task.VerifyCommands) != 2 {
			t.Fatalf("VerifyCommands len = %d, want 2", len(task.VerifyCommands))
		}
		if task.VerifyCommands[0] != "go test ./..." {
			t.Errorf("VerifyCommands[0] = %q, want %q", task.VerifyCommands[0], "go test ./...")
		}
	})

	t.Run("add-task passes origin-finding-id for audit remediation", func(t *testing.T) {
		projectRoot, statePath := setupMutationTestProject(t, nil)
		testhelpers.CreateSpecFile(t, projectRoot, "vision.md", "# Vision\n")

		err := executeRootCommand(t, projectRoot,
			"add-task",
			"--id", "task-audit-fix",
			"--desc", "Fix audit finding",
			"--spec", "specs/vision.md",
			"--done", "Fixed",
			"--scope", "scope",
			"--priority", "5",
			"--origin-finding-id", "audit-001",
			"--error-behavior", "Return descriptive error",
			"--requirement-refs", "R2,R6",
			"--agent-id", "planner-1",
		)
		if err != nil {
			t.Fatalf("add-task failed: %v", err)
		}

		state := readState(t, statePath)
		task := mustFindTask(t, state, "task-audit-fix")

		if task.OriginFindingID != "audit-001" {
			t.Errorf("OriginFindingID = %q, want %q", task.OriginFindingID, "audit-001")
		}
		if task.ErrorBehavior != "Return descriptive error" {
			t.Errorf("ErrorBehavior = %q, want %q", task.ErrorBehavior, "Return descriptive error")
		}
		if len(task.RequirementRefs) != 2 {
			t.Fatalf("RequirementRefs len = %d, want 2", len(task.RequirementRefs))
		}
		if task.RequirementRefs[0] != "R2" {
			t.Errorf("RequirementRefs[0] = %q, want %q", task.RequirementRefs[0], "R2")
		}
	})
}
