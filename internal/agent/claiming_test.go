package agent

import (
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// TestClaimCoderTaskReturnsOwnInProgressTask tests that claimCoderTask returns an
// already-claimed IMPLEMENTING task assigned to this agent, enabling re-invocation.
func TestClaimCoderTaskReturnsOwnInProgressTask(t *testing.T) {
	tmpDir := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now)
	task.AssignedTo = testhelpers.StringPtr("coder-1")
	task.Worktree = testhelpers.StringPtr(".liza/worktrees/task-1")
	state.Tasks = []models.Task{task}
	testhelpers.WriteInitialState(t, statePath, state)

	bb := db.New(statePath)
	taskID, worktree, err := claimCoderTask(tmpDir, "coder-1", bb)
	if err != nil {
		t.Fatalf("claimCoderTask() error = %v", err)
	}
	if taskID != "task-1" {
		t.Errorf("claimCoderTask() taskID = %q, want %q", taskID, "task-1")
	}
	if worktree != ".liza/worktrees/task-1" {
		t.Errorf("claimCoderTask() worktree = %q, want %q", worktree, ".liza/worktrees/task-1")
	}
}

// TestClaimCoderTaskDoesNotReturnOtherAgentTask ensures claimCoderTask does not
// return a task assigned to a different agent.
func TestClaimCoderTaskDoesNotReturnOtherAgentTask(t *testing.T) {
	tmpDir := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now)
	task.AssignedTo = testhelpers.StringPtr("coder-2")
	state.Tasks = []models.Task{task}
	testhelpers.WriteInitialState(t, statePath, state)

	bb := db.New(statePath)
	_, _, err := claimCoderTask(tmpDir, "coder-1", bb)
	if err == nil {
		t.Fatal("claimCoderTask() should error when only other agent's task exists")
	}
}

// TestHasPendingMerges tests the hasPendingMerges function
func TestHasPendingMerges(t *testing.T) {
	tests := []struct {
		name     string
		tasks    []models.Task
		agentID  string
		expected bool
	}{
		{
			name:     "no tasks returns false",
			tasks:    []models.Task{},
			agentID:  "code-reviewer-1",
			expected: false,
		},
		{
			name: "approved task with merge_commit set returns false",
			tasks: []models.Task{
				{
					ID:          "task-1",
					Status:      models.TaskStatusApproved,
					ApprovedBy:  testhelpers.StringPtr("code-reviewer-1"),
					MergeCommit: testhelpers.StringPtr("abc123"),
				},
			},
			agentID:  "code-reviewer-1",
			expected: false,
		},
		{
			name: "approved task by different agent returns false",
			tasks: []models.Task{
				{
					ID:          "task-1",
					Status:      models.TaskStatusApproved,
					ApprovedBy:  testhelpers.StringPtr("code-reviewer-2"),
					MergeCommit: nil,
				},
			},
			agentID:  "code-reviewer-1",
			expected: false,
		},
		{
			name: "approved task by this agent without merge_commit returns true",
			tasks: []models.Task{
				{
					ID:          "task-1",
					Status:      models.TaskStatusApproved,
					ApprovedBy:  testhelpers.StringPtr("code-reviewer-1"),
					MergeCommit: nil,
				},
			},
			agentID:  "code-reviewer-1",
			expected: true,
		},
		{
			name: "multiple tasks, one pending returns true",
			tasks: []models.Task{
				{
					ID:          "task-1",
					Status:      models.TaskStatusApproved,
					ApprovedBy:  testhelpers.StringPtr("code-reviewer-1"),
					MergeCommit: testhelpers.StringPtr("abc123"),
				},
				{
					ID:          "task-2",
					Status:      models.TaskStatusApproved,
					ApprovedBy:  testhelpers.StringPtr("code-reviewer-1"),
					MergeCommit: nil,
				},
			},
			agentID:  "code-reviewer-1",
			expected: true,
		},
		{
			name: "integration_failed task returns false",
			tasks: []models.Task{
				{
					ID:          "task-1",
					Status:      models.TaskStatusIntegrationFailed,
					ApprovedBy:  testhelpers.StringPtr("code-reviewer-1"),
					MergeCommit: nil,
				},
			},
			agentID:  "code-reviewer-1",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test blackboard with tasks
			tmpDir := t.TempDir()
			statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

			state := testhelpers.CreateValidState()
			state.Tasks = tt.tasks

			testhelpers.WriteInitialState(t, statePath, state)

			bb := db.New(statePath)

			// Test hasPendingMerges
			result := hasPendingMerges(bb, tt.agentID)
			if result != tt.expected {
				t.Errorf("hasPendingMerges() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
