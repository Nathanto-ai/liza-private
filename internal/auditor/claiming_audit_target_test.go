package auditor

import (
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

func TestFindAuditTarget_MergedTaskPriority(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	t.Run("MERGED task with no post_merge audit returns PostMerge target (Priority 1)", func(t *testing.T) {
		t.Parallel()

		state := testhelpers.CreateValidState()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now),
		}
		state.AuditFindings = nil

		target := FindAuditTarget(state)
		if target == nil {
			t.Fatal("expected non-nil target")
		}
		if target.TaskID != "task-1" {
			t.Errorf("TaskID = %q, want %q", target.TaskID, "task-1")
		}
		if target.Phase != AuditPhasePostMerge {
			t.Errorf("Phase = %q, want %q", target.Phase, AuditPhasePostMerge)
		}
	})

	t.Run("MERGED task already has post_merge finding → skips it, picks READY_FOR_REVIEW (Priority 2)", func(t *testing.T) {
		t.Parallel()

		state := testhelpers.CreateValidState()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now),
			testhelpers.BuildTaskByStatus("task-2", models.TaskStatusReadyForReview, now),
		}
		state.AuditFindings = []models.AuditFinding{
			{
				ID:       "finding-1",
				TaskID:   "task-1",
				Severity: "LOW",
				Type:     "QUALITY_ISSUE",
				Phase:    "post_merge",
				Evidence: "looks good",
				Created:  now,
			},
		}

		target := FindAuditTarget(state)
		if target == nil {
			t.Fatal("expected non-nil target")
		}
		if target.TaskID != "task-2" {
			t.Errorf("TaskID = %q, want %q", target.TaskID, "task-2")
		}
		if target.Phase != AuditPhasePostExecution {
			t.Errorf("Phase = %q, want %q", target.Phase, AuditPhasePostExecution)
		}
	})

	t.Run("all three priorities present → picks MERGED first", func(t *testing.T) {
		t.Parallel()

		state := testhelpers.CreateValidState()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-ready", models.TaskStatusReady, now),
			testhelpers.BuildTaskByStatus("task-review", models.TaskStatusReadyForReview, now),
			testhelpers.BuildTaskByStatus("task-merged", models.TaskStatusMerged, now),
		}
		state.AuditFindings = nil

		target := FindAuditTarget(state)
		if target == nil {
			t.Fatal("expected non-nil target")
		}
		if target.TaskID != "task-merged" {
			t.Errorf("TaskID = %q, want %q", target.TaskID, "task-merged")
		}
		if target.Phase != AuditPhasePostMerge {
			t.Errorf("Phase = %q, want %q", target.Phase, AuditPhasePostMerge)
		}
	})

	t.Run("no unaudited tasks → returns nil", func(t *testing.T) {
		t.Parallel()

		state := testhelpers.CreateValidState()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now),
		}
		state.AuditFindings = []models.AuditFinding{
			{
				ID:       "finding-1",
				TaskID:   "task-1",
				Severity: "LOW",
				Type:     "QUALITY_ISSUE",
				Phase:    "post_merge",
				Evidence: "audited",
				Created:  now,
			},
		}

		target := FindAuditTarget(state)
		if target != nil {
			t.Fatalf("expected nil, got %+v", target)
		}
	})

	t.Run("legacy finding (no phase) does NOT satisfy per-phase check", func(t *testing.T) {
		t.Parallel()

		state := testhelpers.CreateValidState()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now),
		}
		// Legacy finding with no phase — goes to _legacy bucket, should NOT block post_merge
		state.AuditFindings = []models.AuditFinding{
			{
				ID:       "finding-legacy",
				TaskID:   "task-1",
				Severity: "LOW",
				Type:     "QUALITY_ISSUE",
				Phase:    "", // legacy
				Evidence: "old finding",
				Created:  now,
			},
		}

		target := FindAuditTarget(state)
		if target == nil {
			t.Fatal("expected non-nil target — legacy finding should not satisfy post_merge phase")
		}
		if target.TaskID != "task-1" {
			t.Errorf("TaskID = %q, want %q", target.TaskID, "task-1")
		}
		if target.Phase != AuditPhasePostMerge {
			t.Errorf("Phase = %q, want %q", target.Phase, AuditPhasePostMerge)
		}
	})
}
