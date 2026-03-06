package statevalidate

import (
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

func TestValidateTaskQuality(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	lease := now.Add(30 * time.Minute)
	worktree := ".worktrees/task-1"
	baseCommit := "abc123"
	agent := "coder-1"

	tests := []struct {
		name        string
		tasks       []models.Task
		wantErr     bool
		errContains string
	}{
		{
			name: "IMPLEMENTING task with acceptance criteria and verify commands passes",
			tasks: []models.Task{
				{
					ID:                 "task-1",
					Type:               models.TaskTypeCoding,
					Description:        "Implement feature",
					Status:             models.TaskStatusImplementing,
					Priority:           1,
					AssignedTo:         &agent,
					Worktree:           &worktree,
					BaseCommit:         &baseCommit,
					LeaseExpires:       &lease,
					SpecRef:            "specs/vision.md",
					DoneWhen:           "tests pass",
					Scope:              "feature module",
					AcceptanceCriteria: []string{"Given: input When: action Then: result"},
					VerifyCommands:     []string{"make test"},
					Created:            now,
					History:            []models.TaskHistoryEntry{},
				},
			},
			wantErr: false,
		},
		{
			name: "IMPLEMENTING task without acceptance criteria fails",
			tasks: []models.Task{
				{
					ID:             "task-1",
					Type:           models.TaskTypeCoding,
					Description:    "Implement feature",
					Status:         models.TaskStatusImplementing,
					Priority:       1,
					AssignedTo:     &agent,
					Worktree:       &worktree,
					BaseCommit:     &baseCommit,
					LeaseExpires:   &lease,
					SpecRef:        "specs/vision.md",
					DoneWhen:       "tests pass",
					Scope:          "feature module",
					VerifyCommands: []string{"make test"},
					Created:        now,
					History:        []models.TaskHistoryEntry{},
				},
			},
			wantErr:     true,
			errContains: "no acceptance_criteria",
		},
		{
			name: "IMPLEMENTING task without verify commands fails",
			tasks: []models.Task{
				{
					ID:                 "task-1",
					Type:               models.TaskTypeCoding,
					Description:        "Implement feature",
					Status:             models.TaskStatusImplementing,
					Priority:           1,
					AssignedTo:         &agent,
					Worktree:           &worktree,
					BaseCommit:         &baseCommit,
					LeaseExpires:       &lease,
					SpecRef:            "specs/vision.md",
					DoneWhen:           "tests pass",
					Scope:              "feature module",
					AcceptanceCriteria: []string{"Given: input When: action Then: result"},
					Created:            now,
					History:            []models.TaskHistoryEntry{},
				},
			},
			wantErr:     true,
			errContains: "no verify_commands",
		},
		{
			name: "DRAFT task without criteria passes (no gate)",
			tasks: []models.Task{
				{
					ID:          "task-1",
					Type:        models.TaskTypeCoding,
					Description: "Draft task",
					Status:      models.TaskStatusDraft,
					Priority:    1,
					Created:     now,
					History:     []models.TaskHistoryEntry{},
				},
			},
			wantErr: false,
		},
		{
			name: "READY task without criteria passes (gate is at IMPLEMENTING)",
			tasks: []models.Task{
				{
					ID:          "task-1",
					Type:        models.TaskTypeCoding,
					Description: "Ready task",
					Status:      models.TaskStatusReady,
					Priority:    1,
					SpecRef:     "specs/vision.md",
					DoneWhen:    "tests pass",
					Scope:       "feature",
					Created:     now,
					History:     []models.TaskHistoryEntry{},
				},
			},
			wantErr: false,
		},
		{
			name:    "no tasks passes",
			tasks:   []models.Task{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := testhelpers.CreateValidState()
			state.Tasks = tt.tasks

			err := validateTaskQuality(state, "", true)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errContains)
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("error = %q, want containing %q", err.Error(), tt.errContains)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestValidateAuditFindings(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	tests := []struct {
		name        string
		findings    []models.AuditFinding
		wantErr     bool
		errContains string
	}{
		{
			name:     "no findings passes",
			findings: nil,
			wantErr:  false,
		},
		{
			name: "valid finding passes",
			findings: []models.AuditFinding{
				{
					ID:       "finding-1",
					TaskID:   "task-1",
					Severity: "HIGH",
					Type:     "SPEC_MISMATCH",
					Evidence: "Expected behavior X but got Y",
					Created:  now,
				},
			},
			wantErr: false,
		},
		{
			name: "finding missing ID fails",
			findings: []models.AuditFinding{
				{
					TaskID:   "task-1",
					Severity: "HIGH",
					Type:     "SPEC_MISMATCH",
					Evidence: "evidence",
					Created:  now,
				},
			},
			wantErr:     true,
			errContains: "missing required field 'id'",
		},
		{
			name: "finding missing task_id fails",
			findings: []models.AuditFinding{
				{
					ID:       "finding-1",
					Severity: "HIGH",
					Type:     "SPEC_MISMATCH",
					Evidence: "evidence",
					Created:  now,
				},
			},
			wantErr:     true,
			errContains: "missing required field 'task_id'",
		},
		{
			name: "finding with invalid severity fails",
			findings: []models.AuditFinding{
				{
					ID:       "finding-1",
					TaskID:   "task-1",
					Severity: "CRITICAL",
					Type:     "SPEC_MISMATCH",
					Evidence: "evidence",
					Created:  now,
				},
			},
			wantErr:     true,
			errContains: "invalid severity",
		},
		{
			name: "finding with invalid type fails",
			findings: []models.AuditFinding{
				{
					ID:       "finding-1",
					TaskID:   "task-1",
					Severity: "HIGH",
					Type:     "UNKNOWN_TYPE",
					Evidence: "evidence",
					Created:  now,
				},
			},
			wantErr:     true,
			errContains: "invalid type",
		},
		{
			name: "finding missing evidence fails",
			findings: []models.AuditFinding{
				{
					ID:       "finding-1",
					TaskID:   "task-1",
					Severity: "HIGH",
					Type:     "SPEC_MISMATCH",
					Created:  now,
				},
			},
			wantErr:     true,
			errContains: "missing required field 'evidence'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := testhelpers.CreateValidState()
			state.AuditFindings = tt.findings

			err := validateAuditFindings(state, "", true)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errContains)
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("error = %q, want containing %q", err.Error(), tt.errContains)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

// contains is a simple substring check helper for test assertions.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsSubstring(s, substr)
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
