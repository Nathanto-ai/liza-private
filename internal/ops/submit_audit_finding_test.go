package ops

import (
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

func TestSubmitAuditFinding_Validation(t *testing.T) {
	tests := []struct {
		name        string
		input       AuditFindingInput
		errContains string
	}{
		{
			name:        "empty finding_id",
			input:       AuditFindingInput{TaskID: "t1", Severity: "HIGH", Type: "SPEC_MISMATCH", Evidence: "e"},
			errContains: "finding_id is required",
		},
		{
			name:        "empty task_id",
			input:       AuditFindingInput{FindingID: "f1", Severity: "HIGH", Type: "SPEC_MISMATCH", Evidence: "e"},
			errContains: "task_id is required",
		},
		{
			name:        "empty severity",
			input:       AuditFindingInput{FindingID: "f1", TaskID: "t1", Type: "SPEC_MISMATCH", Evidence: "e"},
			errContains: "severity is required",
		},
		{
			name:        "empty evidence",
			input:       AuditFindingInput{FindingID: "f1", TaskID: "t1", Severity: "HIGH", Type: "SPEC_MISMATCH"},
			errContains: "evidence is required",
		},
		{
			name:        "empty type",
			input:       AuditFindingInput{FindingID: "f1", TaskID: "t1", Severity: "HIGH", Evidence: "e"},
			errContains: "type is required",
		},
		{
			name:        "invalid severity",
			input:       AuditFindingInput{FindingID: "f1", TaskID: "t1", Severity: "CRITICAL", Type: "SPEC_MISMATCH", Evidence: "e"},
			errContains: "invalid severity",
		},
		{
			name:        "invalid type",
			input:       AuditFindingInput{FindingID: "f1", TaskID: "t1", Severity: "HIGH", Type: "INVALID_TYPE", Evidence: "e"},
			errContains: "invalid type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SubmitAuditFinding("/nonexistent", tt.input)
			testhelpers.RequireErrorContains(t, err, tt.errContains)
		})
	}
}

func TestSubmitAuditFinding_Success(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := &models.State{
		Tasks: []models.Task{
			{
				ID:          "task-1",
				Description: "Test task",
				DoneWhen:    "done",
				Priority:    1,
				Status:      models.TaskStatusReadyForReview,
				Created:     now,
			},
		},
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	result, err := SubmitAuditFinding(tmpDir, AuditFindingInput{
		FindingID:         "finding-1",
		TaskID:            "task-1",
		Severity:          "HIGH",
		Type:              "SPEC_MISMATCH",
		Evidence:          "Missing validation for empty input",
		RecommendedAction: "Add edge case test",
		SpecReference:     "AC-2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FindingID != "finding-1" {
		t.Errorf("FindingID = %q, want finding-1", result.FindingID)
	}
	if result.TaskID != "task-1" {
		t.Errorf("TaskID = %q, want task-1", result.TaskID)
	}

	// Verify finding was persisted
	bb := db.For(stateFile)
	updated, err := bb.Read()
	if err != nil {
		t.Fatalf("failed to read state: %v", err)
	}
	if len(updated.AuditFindings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(updated.AuditFindings))
	}
	f := updated.AuditFindings[0]
	if f.ID != "finding-1" || f.TaskID != "task-1" || f.Severity != "HIGH" {
		t.Errorf("finding = %+v", f)
	}
}

func TestSubmitAuditFinding_TaskNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	state := &models.State{Tasks: []models.Task{}}
	testhelpers.WriteInitialState(t, stateFile, state)

	_, err := SubmitAuditFinding(tmpDir, AuditFindingInput{
		FindingID: "f1",
		TaskID:    "nonexistent",
		Severity:  "LOW",
		Type:      "QUALITY_ISSUE",
		Evidence:  "something",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestSubmitAuditFinding_DuplicateID(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := &models.State{
		Tasks: []models.Task{
			{
				ID:          "task-1",
				Description: "Test task",
				DoneWhen:    "done",
				Priority:    1,
				Status:      models.TaskStatusReady,
				Created:     now,
			},
		},
		AuditFindings: []models.AuditFinding{
			{
				ID:       "f1",
				TaskID:   "task-1",
				Severity: "LOW",
				Type:     "QUALITY_ISSUE",
				Evidence: "existing",
				Created:  now,
			},
		},
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	_, err := SubmitAuditFinding(tmpDir, AuditFindingInput{
		FindingID: "f1",
		TaskID:    "task-1",
		Severity:  "HIGH",
		Type:      "SPEC_MISMATCH",
		Evidence:  "duplicate",
	})
	testhelpers.RequireErrorContains(t, err, "already exists")
}
