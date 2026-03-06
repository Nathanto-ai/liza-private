package statevalidate

import (
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// TestValidateTaskQuality_RequirementRefs tests the requirement_refs enforcement gate.
func TestValidateTaskQuality_RequirementRefs(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	lease := now.Add(30 * time.Minute)
	worktree := ".worktrees/task-1"
	baseCommit := "abc123"
	agent := "coder-1"

	tests := []struct {
		name              string
		enforceReqRefs    bool
		requirementRefs   []string
		wantErr           bool
		errContains       string
	}{
		{
			name:            "enforcement off, no refs, passes",
			enforceReqRefs:  false,
			requirementRefs: nil,
			wantErr:         false,
		},
		{
			name:            "enforcement on, has refs, passes",
			enforceReqRefs:  true,
			requirementRefs: []string{"R1", "R2"},
			wantErr:         false,
		},
		{
			name:            "enforcement on, no refs, fails",
			enforceReqRefs:  true,
			requirementRefs: nil,
			wantErr:         true,
			errContains:     "no requirement_refs",
		},
		{
			name:            "enforcement on, empty refs, fails",
			enforceReqRefs:  true,
			requirementRefs: []string{},
			wantErr:         true,
			errContains:     "no requirement_refs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := testhelpers.CreateValidState()
			state.Config.EnforceRequirementRefs = tt.enforceReqRefs
			state.Tasks = []models.Task{
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
					RequirementRefs:    tt.requirementRefs,
					AcceptanceCriteria: []string{"AC-1: feature works"},
					VerifyCommands:     []string{"make test"},
					Created:            now,
					History:            []models.TaskHistoryEntry{},
				},
			}

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
