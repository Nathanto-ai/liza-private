package auditor

import (
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

func TestFindAuditTarget(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	reviewCommit := "abc123"

	tests := []struct {
		name       string
		tasks      []models.Task
		findings   []models.AuditFinding
		wantTaskID string
		wantPhase  AuditPhase
		wantNil    bool
	}{
		{
			name:    "no tasks returns nil",
			tasks:   []models.Task{},
			wantNil: true,
		},
		{
			name: "READY_FOR_REVIEW task selected for post-execution audit",
			tasks: []models.Task{
				{
					ID:           "task-1",
					Status:       models.TaskStatusReadyForReview,
					Description:  "Test task",
					Priority:     1,
					SpecRef:      "specs/vision.md",
					DoneWhen:     "done",
					Scope:        "test",
					ReviewCommit: &reviewCommit,
					Created:      now,
					History:      []models.TaskHistoryEntry{},
				},
			},
			wantTaskID: "task-1",
			wantPhase:  AuditPhasePostExecution,
		},
		{
			name: "READY task selected for pre-execution audit",
			tasks: []models.Task{
				{
					ID:          "task-1",
					Status:      models.TaskStatusReady,
					Description: "Test task",
					Priority:    1,
					SpecRef:     "specs/vision.md",
					DoneWhen:    "done",
					Scope:       "test",
					Created:     now,
					History:     []models.TaskHistoryEntry{},
				},
			},
			wantTaskID: "task-1",
			wantPhase:  AuditPhasePreExecution,
		},
		{
			name: "already audited task in same phase is skipped",
			tasks: []models.Task{
				{
					ID:           "task-1",
					Status:       models.TaskStatusReadyForReview,
					Description:  "Test task",
					Priority:     1,
					SpecRef:      "specs/vision.md",
					DoneWhen:     "done",
					Scope:        "test",
					ReviewCommit: &reviewCommit,
					Created:      now,
					History:      []models.TaskHistoryEntry{},
				},
			},
			findings: []models.AuditFinding{
				{
					ID:       "finding-1",
					TaskID:   "task-1",
					Severity: "LOW",
					Type:     "QUALITY_ISSUE",
					Phase:    "post_execution",
					Evidence: "minor issue",
					Created:  now,
				},
			},
			wantNil: true,
		},
		{
			name: "READY_FOR_REVIEW prioritized over READY",
			tasks: []models.Task{
				{
					ID:          "task-1",
					Status:      models.TaskStatusReady,
					Description: "Ready task",
					Priority:    1,
					SpecRef:     "specs/vision.md",
					DoneWhen:    "done",
					Scope:       "test",
					Created:     now,
					History:     []models.TaskHistoryEntry{},
				},
				{
					ID:           "task-2",
					Status:       models.TaskStatusReadyForReview,
					Description:  "Review task",
					Priority:     1,
					SpecRef:      "specs/vision.md",
					DoneWhen:     "done",
					Scope:        "test",
					ReviewCommit: &reviewCommit,
					Created:      now,
					History:      []models.TaskHistoryEntry{},
				},
			},
			wantTaskID: "task-2",
			wantPhase:  AuditPhasePostExecution,
		},
		{
			name: "IMPLEMENTING task not selected",
			tasks: []models.Task{
				{
					ID:          "task-1",
					Status:      models.TaskStatusImplementing,
					Description: "Implementing task",
					Priority:    1,
					SpecRef:     "specs/vision.md",
					DoneWhen:    "done",
					Scope:       "test",
					Created:     now,
					History:     []models.TaskHistoryEntry{},
				},
			},
			wantNil: true,
		},
		{
			name: "MERGED task selected for post-merge audit",
			tasks: []models.Task{
				{
					ID:          "task-1",
					Status:      models.TaskStatusMerged,
					Description: "Merged task",
					Priority:    1,
					SpecRef:     "specs/vision.md",
					DoneWhen:    "done",
					Scope:       "test",
					Created:     now,
					History:     []models.TaskHistoryEntry{},
				},
			},
			wantTaskID: "task-1",
			wantPhase:  AuditPhasePostMerge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := testhelpers.CreateValidState()
			state.Tasks = tt.tasks
			state.AuditFindings = tt.findings

			target := FindAuditTarget(state)

			if tt.wantNil {
				if target != nil {
					t.Fatalf("expected nil, got %+v", target)
				}
				return
			}

			if target == nil {
				t.Fatal("expected non-nil target")
			}

			if target.TaskID != tt.wantTaskID {
				t.Errorf("TaskID = %q, want %q", target.TaskID, tt.wantTaskID)
			}

			if target.Phase != tt.wantPhase {
				t.Errorf("Phase = %q, want %q", target.Phase, tt.wantPhase)
			}
		})
	}
}

func TestNewFinding(t *testing.T) {
	t.Parallel()

	finding := NewFinding("f-1", "task-42", "HIGH", "SPEC_MISMATCH",
		"Expected X got Y", "Update implementation", "AC-3", "post_execution", "LOG_ONLY")

	if finding.ID != "f-1" {
		t.Errorf("ID = %q, want %q", finding.ID, "f-1")
	}
	if finding.TaskID != "task-42" {
		t.Errorf("TaskID = %q, want %q", finding.TaskID, "task-42")
	}
	if finding.Severity != "HIGH" {
		t.Errorf("Severity = %q, want %q", finding.Severity, "HIGH")
	}
	if finding.Type != "SPEC_MISMATCH" {
		t.Errorf("Type = %q, want %q", finding.Type, "SPEC_MISMATCH")
	}
	if finding.Evidence != "Expected X got Y" {
		t.Errorf("Evidence = %q, want %q", finding.Evidence, "Expected X got Y")
	}
	if finding.SpecReference != "AC-3" {
		t.Errorf("SpecReference = %q, want %q", finding.SpecReference, "AC-3")
	}
	if finding.Created.IsZero() {
		t.Error("Created should not be zero")
	}
	if finding.Resolved {
		t.Error("new finding should not be resolved")
	}
}
