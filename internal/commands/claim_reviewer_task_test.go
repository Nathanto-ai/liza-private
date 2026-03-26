package commands

import (
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

func TestClaimReviewerTaskCommand(t *testing.T) {
	tests := []struct {
		name        string
		agentID     string
		taskStatus  models.TaskStatus
		hasReview   bool
		wantErr     bool
		errContains string
	}{
		{
			name:       "claim READY_FOR_REVIEW task",
			agentID:    "reviewer-1",
			taskStatus: models.TaskStatusReadyForReview,
			hasReview:  true,
			wantErr:    false,
		},
		{
			name:        "no reviewable tasks",
			agentID:     "reviewer-1",
			taskStatus:  models.TaskStatusReady,
			hasReview:   false,
			wantErr:     true,
			errContains: "no reviewable tasks",
		},
		{
			name:        "empty agent ID",
			agentID:     "",
			taskStatus:  models.TaskStatusReadyForReview,
			hasReview:   true,
			wantErr:     true,
			errContains: "agent ID is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			testhelpers.SetupTestGitRepo(t, tmpDir)
			statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

			now := time.Now().UTC()
			initialState := testhelpers.CreateValidState()

			// Set up a task with the specified status
			rc := "abc1234"
			wt := ".worktrees/task-1"
			bc := "base123"
			agent := "coder-1"
			task := models.Task{
				ID:          "task-1",
				Description: "Test task for review",
				Status:      tt.taskStatus,
				Priority:    1,
				Created:     now,
				SpecRef:     "README.md",
				DoneWhen:    "Done",
				Scope:       "Test",
				RolePair:    "coding-pair",
				AssignedTo:  &agent,
				BaseCommit:  &bc,
				History:     []models.TaskHistoryEntry{},
			}
			if tt.hasReview {
				task.ReviewCommit = &rc
				task.Worktree = &wt
			}
			initialState.Tasks = append(initialState.Tasks, task)

			bb := testhelpers.WriteInitialState(t, statePath, initialState)

			err := ClaimReviewerTaskCommand(tmpDir, tt.agentID)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got none")
					return
				}
				testhelpers.AssertErrorContains(t, err, tt.errContains)
				return
			}

			testhelpers.AssertNoError(t, err)

			// Verify task transitioned to REVIEWING
			state, err := bb.Read()
			if err != nil {
				t.Fatalf("Failed to read state: %v", err)
			}

			var found *models.Task
			for i := range state.Tasks {
				if state.Tasks[i].ID == "task-1" {
					found = &state.Tasks[i]
					break
				}
			}

			if found == nil {
				t.Fatal("Task not found in state")
			}

			if found.Status != models.TaskStatusReviewing {
				t.Errorf("Task status = %v, want REVIEWING", found.Status)
			}

			if found.ReviewingBy == nil || *found.ReviewingBy != tt.agentID {
				t.Errorf("ReviewingBy = %v, want %s", found.ReviewingBy, tt.agentID)
			}
		})
	}
}
