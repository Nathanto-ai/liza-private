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

// V&V Run 3 regression tests — circuit breaker and depth.

func TestClassifyFinding_RemediationCircuitBreaker(t *testing.T) {
	t.Parallel()

	t.Run("HIGH on merged remediation task → LOG_ONLY", func(t *testing.T) {
		f := models.AuditFinding{Severity: "HIGH", Type: "MISSING_TEST"}
		got := ClassifyFinding(f, models.TaskStatusMerged, 0, WithRemediationTask(true))
		if got != "LOG_ONLY" {
			t.Errorf("got %q, want LOG_ONLY (circuit breaker)", got)
		}
	})

	t.Run("HIGH on non-remediation merged task → normal", func(t *testing.T) {
		f := models.AuditFinding{Severity: "HIGH", Type: "MISSING_TEST"}
		got := ClassifyFinding(f, models.TaskStatusMerged, 0, WithRemediationTask(false))
		if got != "REMEDIATE_WITH_TASK" {
			t.Errorf("got %q, want REMEDIATE_WITH_TASK", got)
		}
	})

	t.Run("HIGH with depth >= 1 → LOG_ONLY", func(t *testing.T) {
		f := models.AuditFinding{Severity: "HIGH", Type: "MISSING_TEST"}
		got := ClassifyFinding(f, models.TaskStatusReady, 0, WithRemediationDepth(1))
		if got != "LOG_ONLY" {
			t.Errorf("got %q, want LOG_ONLY (depth limit)", got)
		}
	})

	t.Run("MEDIUM on remediation task not merged → normal", func(t *testing.T) {
		f := models.AuditFinding{Severity: "MEDIUM", Type: "MISSING_TEST"}
		got := ClassifyFinding(f, models.TaskStatusReady, 0, WithRemediationTask(true))
		if got != "REMEDIATE_WITH_TASK" {
			t.Errorf("got %q, want REMEDIATE_WITH_TASK", got)
		}
	})

	t.Run("backward compatible — no options", func(t *testing.T) {
		f := models.AuditFinding{Severity: "HIGH", Type: "MISSING_TEST"}
		got := ClassifyFinding(f, models.TaskStatusMerged, 0)
		if got != "REMEDIATE_WITH_TASK" {
			t.Errorf("got %q, want REMEDIATE_WITH_TASK (backward compat)", got)
		}
	})
}

func TestComputeRemediationDepth(t *testing.T) {
	t.Parallel()

	t.Run("original task has depth 0", func(t *testing.T) {
		task := &models.Task{ID: "original"}
		if d := computeRemediationDepth(task, nil, nil); d != 0 {
			t.Errorf("got depth %d, want 0", d)
		}
	})

	t.Run("first remediation has depth 1", func(t *testing.T) {
		tasks := []models.Task{
			{ID: "original"},
			{ID: "fix-f1", OriginFindingID: "f1"},
		}
		findings := []models.AuditFinding{
			{ID: "f1", TaskID: "original"},
		}
		if d := computeRemediationDepth(&tasks[1], tasks, findings); d != 1 {
			t.Errorf("got depth %d, want 1", d)
		}
	})

	t.Run("second remediation has depth 2", func(t *testing.T) {
		tasks := []models.Task{
			{ID: "original"},
			{ID: "fix-f1", OriginFindingID: "f1"},
			{ID: "fix-f2", OriginFindingID: "f2"},
		}
		findings := []models.AuditFinding{
			{ID: "f1", TaskID: "original"},
			{ID: "f2", TaskID: "fix-f1"},
		}
		if d := computeRemediationDepth(&tasks[2], tasks, findings); d != 2 {
			t.Errorf("got depth %d, want 2", d)
		}
	})

	t.Run("cycle protection", func(t *testing.T) {
		tasks := []models.Task{
			{ID: "task-a", OriginFindingID: "f-b"},
			{ID: "task-b", OriginFindingID: "f-a"},
		}
		findings := []models.AuditFinding{
			{ID: "f-a", TaskID: "task-a"},
			{ID: "f-b", TaskID: "task-b"},
		}
		d := computeRemediationDepth(&tasks[0], tasks, findings)
		if d > 10 {
			t.Errorf("got depth %d, expected cycle protection to limit depth", d)
		}
	})
}
