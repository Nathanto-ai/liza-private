package ops

import (
	"testing"

	"github.com/liza-mas/liza/internal/models"
)

func TestClassifyFinding(t *testing.T) {
	t.Parallel()

	t.Run("HIGH SPEC_MISMATCH on MERGED task → REOPEN_TASK", func(t *testing.T) {
		f := models.AuditFinding{Severity: "HIGH", Type: "SPEC_MISMATCH"}
		got := ClassifyFinding(f, models.TaskStatusMerged, 0)
		if got != "REOPEN_TASK" {
			t.Errorf("got %q, want REOPEN_TASK", got)
		}
	})

	t.Run("HIGH SPEC_MISMATCH on READY task → REPLAN_REQUIRED", func(t *testing.T) {
		f := models.AuditFinding{Severity: "HIGH", Type: "SPEC_MISMATCH"}
		got := ClassifyFinding(f, models.TaskStatusReady, 0)
		if got != "REPLAN_REQUIRED" {
			t.Errorf("got %q, want REPLAN_REQUIRED", got)
		}
	})

	t.Run("HIGH SYSTEMIC_SPEC_DRIFT → REPLAN_REQUIRED", func(t *testing.T) {
		f := models.AuditFinding{Severity: "HIGH", Type: "SYSTEMIC_SPEC_DRIFT"}
		got := ClassifyFinding(f, models.TaskStatusImplementing, 0)
		if got != "REPLAN_REQUIRED" {
			t.Errorf("got %q, want REPLAN_REQUIRED", got)
		}
	})

	t.Run("HIGH MISSING_TEST → REMEDIATE_WITH_TASK", func(t *testing.T) {
		f := models.AuditFinding{Severity: "HIGH", Type: "MISSING_TEST"}
		got := ClassifyFinding(f, models.TaskStatusMerged, 0)
		if got != "REMEDIATE_WITH_TASK" {
			t.Errorf("got %q, want REMEDIATE_WITH_TASK", got)
		}
	})

	t.Run("MEDIUM MISSING_TEST → REMEDIATE_WITH_TASK", func(t *testing.T) {
		f := models.AuditFinding{Severity: "MEDIUM", Type: "MISSING_TEST"}
		got := ClassifyFinding(f, models.TaskStatusMerged, 0)
		if got != "REMEDIATE_WITH_TASK" {
			t.Errorf("got %q, want REMEDIATE_WITH_TASK", got)
		}
	})

	t.Run("MEDIUM VERIFICATION_GAP → REMEDIATE_WITH_TASK", func(t *testing.T) {
		f := models.AuditFinding{Severity: "MEDIUM", Type: "VERIFICATION_GAP"}
		got := ClassifyFinding(f, models.TaskStatusMerged, 0)
		if got != "REMEDIATE_WITH_TASK" {
			t.Errorf("got %q, want REMEDIATE_WITH_TASK", got)
		}
	})

	t.Run("MEDIUM ARCHITECTURE_DEBT → REPLAN_REQUIRED", func(t *testing.T) {
		f := models.AuditFinding{Severity: "MEDIUM", Type: "ARCHITECTURE_DEBT"}
		got := ClassifyFinding(f, models.TaskStatusMerged, 0)
		if got != "REPLAN_REQUIRED" {
			t.Errorf("got %q, want REPLAN_REQUIRED", got)
		}
	})

	t.Run("MEDIUM QUALITY_ISSUE → LOG_ONLY", func(t *testing.T) {
		f := models.AuditFinding{Severity: "MEDIUM", Type: "QUALITY_ISSUE"}
		got := ClassifyFinding(f, models.TaskStatusMerged, 0)
		if got != "LOG_ONLY" {
			t.Errorf("got %q, want LOG_ONLY", got)
		}
	})

	t.Run("LOW anything → LOG_ONLY", func(t *testing.T) {
		f := models.AuditFinding{Severity: "LOW", Type: "MISSING_TEST"}
		got := ClassifyFinding(f, models.TaskStatusMerged, 0)
		if got != "LOG_ONLY" {
			t.Errorf("got %q, want LOG_ONLY", got)
		}
	})

	t.Run("repeated findings (3+) escalate to REPLAN_REQUIRED", func(t *testing.T) {
		f := models.AuditFinding{Severity: "LOW", Type: "QUALITY_ISSUE"}
		got := ClassifyFinding(f, models.TaskStatusMerged, 3)
		if got != "REPLAN_REQUIRED" {
			t.Errorf("got %q, want REPLAN_REQUIRED (escalation)", got)
		}
	})

	t.Run("unknown severity falls back to auditor suggestion", func(t *testing.T) {
		f := models.AuditFinding{Severity: "UNKNOWN", Classification: "REMEDIATE_WITH_TASK"}
		got := ClassifyFinding(f, models.TaskStatusMerged, 0)
		if got != "REMEDIATE_WITH_TASK" {
			t.Errorf("got %q, want REMEDIATE_WITH_TASK (fallback)", got)
		}
	})

	t.Run("unknown severity no suggestion → LOG_ONLY", func(t *testing.T) {
		f := models.AuditFinding{Severity: "UNKNOWN"}
		got := ClassifyFinding(f, models.TaskStatusMerged, 0)
		if got != "LOG_ONLY" {
			t.Errorf("got %q, want LOG_ONLY (default fallback)", got)
		}
	})
}

func TestCountUnresolvedFindingsForTask(t *testing.T) {
	t.Parallel()

	findings := []models.AuditFinding{
		{ID: "f1", TaskID: "task-1", Resolved: false},
		{ID: "f2", TaskID: "task-1", Resolved: true},
		{ID: "f3", TaskID: "task-1", Resolved: false},
		{ID: "f4", TaskID: "task-2", Resolved: false},
	}

	got := countUnresolvedFindingsForTask(findings, "task-1")
	if got != 2 {
		t.Errorf("got %d, want 2", got)
	}

	got = countUnresolvedFindingsForTask(findings, "task-2")
	if got != 1 {
		t.Errorf("got %d, want 1", got)
	}

	got = countUnresolvedFindingsForTask(findings, "task-3")
	if got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}
