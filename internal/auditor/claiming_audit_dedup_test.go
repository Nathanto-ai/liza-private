package auditor

import (
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
)

func TestBuildAuditedSet(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	t.Run("finding with phase=post_merge sets audited[taskID][post_merge]", func(t *testing.T) {
		findings := []models.AuditFinding{
			{
				ID:       "f-1",
				TaskID:   "task-1",
				Severity: "LOW",
				Type:     "QUALITY_ISSUE",
				Phase:    "post_merge",
				Evidence: "ok",
				Created:  now,
			},
		}

		audited := buildAuditedSet(findings)
		if audited["task-1"] == nil {
			t.Fatal("expected task-1 in audited set")
		}
		if !audited["task-1"]["post_merge"] {
			t.Error("expected audited[task-1][post_merge] = true")
		}
	})

	t.Run("finding with empty phase goes to _legacy bucket", func(t *testing.T) {
		findings := []models.AuditFinding{
			{
				ID:       "f-legacy",
				TaskID:   "task-1",
				Severity: "LOW",
				Type:     "QUALITY_ISSUE",
				Phase:    "",
				Evidence: "old finding",
				Created:  now,
			},
		}

		audited := buildAuditedSet(findings)
		if audited["task-1"] == nil {
			t.Fatal("expected task-1 in audited set")
		}
		if !audited["task-1"]["_legacy"] {
			t.Error("expected audited[task-1][_legacy] = true")
		}
		// Should NOT set post_merge
		if audited["task-1"]["post_merge"] {
			t.Error("expected audited[task-1][post_merge] = false for legacy finding")
		}
	})
}

func TestIsPhaseAudited(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	t.Run("task with only legacy finding is NOT audited for post_merge", func(t *testing.T) {
		findings := []models.AuditFinding{
			{
				ID:       "f-legacy",
				TaskID:   "task-1",
				Severity: "LOW",
				Type:     "QUALITY_ISSUE",
				Phase:    "",
				Evidence: "old",
				Created:  now,
			},
		}
		audited := buildAuditedSet(findings)
		if isPhaseAudited(audited, "task-1", "post_merge") {
			t.Error("isPhaseAudited(task-1, post_merge) = true, want false (only legacy finding)")
		}
	})

	t.Run("task with post_merge finding IS audited for post_merge", func(t *testing.T) {
		findings := []models.AuditFinding{
			{
				ID:       "f-1",
				TaskID:   "task-1",
				Severity: "LOW",
				Type:     "QUALITY_ISSUE",
				Phase:    "post_merge",
				Evidence: "checked",
				Created:  now,
			},
		}
		audited := buildAuditedSet(findings)
		if !isPhaseAudited(audited, "task-1", "post_merge") {
			t.Error("isPhaseAudited(task-1, post_merge) = false, want true")
		}
	})

	t.Run("unknown task returns false", func(t *testing.T) {
		audited := buildAuditedSet(nil)
		if isPhaseAudited(audited, "task-unknown", "post_merge") {
			t.Error("isPhaseAudited(unknown task) = true, want false")
		}
	})

	t.Run("per-phase independence: post_execution does not satisfy post_merge", func(t *testing.T) {
		findings := []models.AuditFinding{
			{
				ID:       "f-1",
				TaskID:   "task-1",
				Severity: "LOW",
				Type:     "QUALITY_ISSUE",
				Phase:    "post_execution",
				Evidence: "checked execution",
				Created:  now,
			},
		}
		audited := buildAuditedSet(findings)
		if isPhaseAudited(audited, "task-1", "post_merge") {
			t.Error("isPhaseAudited(task-1, post_merge) = true, want false (only post_execution audited)")
		}
		if !isPhaseAudited(audited, "task-1", "post_execution") {
			t.Error("isPhaseAudited(task-1, post_execution) = false, want true")
		}
	})
}
