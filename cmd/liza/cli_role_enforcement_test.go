package main

import (
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// TestCLIRoleEnforcement_ClaimTaskRejectsNonCoder verifies that a non-coder
// agent is rejected when calling claim-task via the CLI.
func TestCLIRoleEnforcement_ClaimTaskRejectsNonCoder(t *testing.T) {
	projectRoot, _ := setupMutationTestProject(t, func(state *models.State) {
		now := time.Now().UTC()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-role-1", models.TaskStatusReady, now),
		}
	})

	err := executeRootCommand(t, projectRoot, "claim-task", "task-role-1", "auditor-1")
	if err == nil {
		t.Fatal("expected error for auditor calling claim-task, got nil")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected 'not allowed' error, got: %v", err)
	}
}

// TestCLIRoleEnforcement_ClaimTaskAllowsCoder verifies coders can claim tasks.
func TestCLIRoleEnforcement_ClaimTaskAllowsCoder(t *testing.T) {
	projectRoot, statePath := setupMutationTestProject(t, func(state *models.State) {
		now := time.Now().UTC()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-role-2", models.TaskStatusReady, now),
		}
	})

	err := executeRootCommand(t, projectRoot, "claim-task", "task-role-2", "coder-1")
	if err != nil {
		t.Fatalf("coder should be able to claim-task: %v", err)
	}

	state := readState(t, statePath)
	task := mustFindTask(t, state, "task-role-2")
	if task.Status != models.TaskStatusImplementing {
		t.Fatalf("task status = %s, want IMPLEMENTING", task.Status)
	}
}

// TestCLIRoleEnforcement_SubmitVerdictRejectsNonReviewer verifies that
// non-reviewer agents are rejected when calling submit-verdict.
func TestCLIRoleEnforcement_SubmitVerdictRejectsNonReviewer(t *testing.T) {
	projectRoot, _ := setupMutationTestProject(t, func(state *models.State) {
		now := time.Now().UTC()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-role-3", models.TaskStatusReviewing, now),
		}
	})

	err := executeRootCommand(t, projectRoot, "submit-verdict", "task-role-3", "APPROVED", "--agent-id", "coder-1")
	if err == nil {
		t.Fatal("expected error for coder calling submit-verdict, got nil")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected 'not allowed' error, got: %v", err)
	}
}

// TestCLIRoleEnforcement_SubmitVerdictAllowsReviewer verifies reviewers can submit verdicts.
func TestCLIRoleEnforcement_SubmitVerdictAllowsReviewer(t *testing.T) {
	projectRoot, statePath := setupMutationTestProject(t, func(state *models.State) {
		now := time.Now().UTC()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-role-4", models.TaskStatusReviewing, now),
		}
	})

	err := executeRootCommand(t, projectRoot, "submit-verdict", "task-role-4", "APPROVED", "--agent-id", "code-reviewer-1")
	if err != nil {
		t.Fatalf("reviewer should be able to submit-verdict: %v", err)
	}

	state := readState(t, statePath)
	task := mustFindTask(t, state, "task-role-4")
	if task.Status != models.TaskStatusApproved {
		t.Fatalf("task status = %s, want APPROVED", task.Status)
	}
}

// TestCLIRoleEnforcement_WriteCheckpointRejectsNonCoder verifies that
// non-coder agents are rejected when calling write-checkpoint.
func TestCLIRoleEnforcement_WriteCheckpointRejectsNonCoder(t *testing.T) {
	projectRoot, _ := setupMutationTestProject(t, func(state *models.State) {
		now := time.Now().UTC()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-role-5", models.TaskStatusImplementing, now),
		}
	})

	err := executeRootCommand(t, projectRoot,
		"write-checkpoint", "task-role-5",
		"--agent-id", "planner-1",
		"--intent", "test",
	)
	if err == nil {
		t.Fatal("expected error for planner calling write-checkpoint, got nil")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected 'not allowed' error, got: %v", err)
	}
}

// TestCLIRoleEnforcement_HandoffRejectsNonCoder verifies non-coders
// cannot call handoff.
func TestCLIRoleEnforcement_HandoffRejectsNonCoder(t *testing.T) {
	projectRoot, _ := setupMutationTestProject(t, func(state *models.State) {
		now := time.Now().UTC()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-role-6", models.TaskStatusImplementing, now),
		}
	})

	err := executeRootCommand(t, projectRoot,
		"handoff", "task-role-6", "handoff summary", "next action",
		"--agent-id", "code-reviewer-1",
	)
	if err == nil {
		t.Fatal("expected error for reviewer calling handoff, got nil")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected 'not allowed' error, got: %v", err)
	}
}

// TestCLIRoleEnforcement_AddTaskRejectsNonPlanner verifies that non-planner
// agents are rejected when calling add-task.
func TestCLIRoleEnforcement_AddTaskRejectsNonPlanner(t *testing.T) {
	projectRoot, _ := setupMutationTestProject(t, nil)

	err := executeRootCommand(t, projectRoot,
		"add-task",
		"--id", "task-new",
		"--desc", "Test task",
		"--spec", "specs/test.md",
		"--done", "It works",
		"--agent-id", "coder-1",
	)
	if err == nil {
		t.Fatal("expected error for coder calling add-task, got nil")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected 'not allowed' error, got: %v", err)
	}
}

// TestCLIRoleEnforcement_SupersedeTaskRejectsNonPlanner verifies non-planners
// cannot call supersede-task.
func TestCLIRoleEnforcement_SupersedeTaskRejectsNonPlanner(t *testing.T) {
	projectRoot, _ := setupMutationTestProject(t, func(state *models.State) {
		now := time.Now().UTC()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-role-7", models.TaskStatusBlocked, now),
		}
	})

	err := executeRootCommand(t, projectRoot,
		"supersede-task", "task-role-7", "task-new-1", "Split for complexity",
		"--agent-id", "coder-1",
	)
	if err == nil {
		t.Fatal("expected error for coder calling supersede-task, got nil")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected 'not allowed' error, got: %v", err)
	}
}

// TestCLIRoleEnforcement_MarkBlockedAllowsMultipleRoles verifies that
// mark-blocked accepts coder, code-reviewer, and auditor roles.
func TestCLIRoleEnforcement_MarkBlockedAllowsMultipleRoles(t *testing.T) {
	for _, agentID := range []string{"coder-1", "code-reviewer-1", "auditor-1"} {
		t.Run(agentID, func(t *testing.T) {
			projectRoot, _ := setupMutationTestProject(t, func(state *models.State) {
				now := time.Now().UTC()
				state.Tasks = []models.Task{
					testhelpers.BuildTaskByStatus("task-block", models.TaskStatusImplementing, now),
				}
			})

			err := executeRootCommand(t, projectRoot,
				"mark-blocked", "task-block",
				"--agent-id", agentID,
				"--reason", "test block",
			)
			// The command should not fail with "not allowed" — it may fail
			// for other reasons (e.g., wrong task state for non-coder) but
			// the role gate must pass.
			if err != nil && strings.Contains(err.Error(), "not allowed") {
				t.Fatalf("expected %s to be allowed for mark-blocked, got: %v", agentID, err)
			}
		})
	}
}

// TestCLIRoleEnforcement_MarkBlockedRejectsPlanner verifies that planners
// cannot call mark-blocked.
func TestCLIRoleEnforcement_MarkBlockedRejectsPlanner(t *testing.T) {
	projectRoot, _ := setupMutationTestProject(t, func(state *models.State) {
		now := time.Now().UTC()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-block-p", models.TaskStatusImplementing, now),
		}
	})

	err := executeRootCommand(t, projectRoot,
		"mark-blocked", "task-block-p",
		"--agent-id", "planner-1",
		"--reason", "test block",
		"--questions", "what is wrong?",
	)
	if err == nil {
		t.Fatal("expected error for planner calling mark-blocked, got nil")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected 'not allowed' error, got: %v", err)
	}
}
