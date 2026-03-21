package models

import (
	"testing"
	"time"
)

// Regression tests for V&V Run 9 — Fix 44: SUPERSEDED dep satisfaction.

// TestFix44_SupersededDepSatisfied verifies that IsClaimable treats a
// SUPERSEDED dependency as satisfied, allowing downstream tasks to proceed.
// Bug: In Run 8, implement-middleware-and-health-shutdown was stuck because
// its dependency implement-crud-handlers was SUPERSEDED (not MERGED).
func TestFix44_SupersededDepSatisfied(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	allTasks := []Task{
		{
			ID:      "dep-superseded",
			Status:  TaskStatusSuperseded,
			Created: now,
		},
		{
			ID:        "downstream",
			Status:    TaskStatusReady,
			DependsOn: []string{"dep-superseded"},
			SpecRef:   "spec.md",
			DoneWhen:  "done",
			Created:   now,
		},
	}

	downstream := &allTasks[1]
	if !downstream.IsClaimable(RoleCoder, allTasks) {
		t.Error("expected downstream task to be claimable when dep is SUPERSEDED — Fix 44 regression")
	}
}

// TestFix44_MergedDepStillSatisfied is a sanity check that MERGED deps
// still satisfy dependencies (existing behavior preserved).
func TestFix44_MergedDepStillSatisfied(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	allTasks := []Task{
		{
			ID:      "dep-merged",
			Status:  TaskStatusMerged,
			Created: now,
		},
		{
			ID:        "downstream",
			Status:    TaskStatusReady,
			DependsOn: []string{"dep-merged"},
			SpecRef:   "spec.md",
			DoneWhen:  "done",
			Created:   now,
		},
	}

	downstream := &allTasks[1]
	if !downstream.IsClaimable(RoleCoder, allTasks) {
		t.Error("expected downstream task to be claimable when dep is MERGED")
	}
}

// TestFix44_ImplementingDepStillBlocks verifies that IMPLEMENTING deps still
// block downstream tasks (we only added SUPERSEDED, not other statuses).
func TestFix44_ImplementingDepStillBlocks(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	allTasks := []Task{
		{
			ID:      "dep-impl",
			Status:  TaskStatusImplementing,
			Created: now,
		},
		{
			ID:        "downstream",
			Status:    TaskStatusReady,
			DependsOn: []string{"dep-impl"},
			SpecRef:   "spec.md",
			DoneWhen:  "done",
			Created:   now,
		},
	}

	downstream := &allTasks[1]
	if downstream.IsClaimable(RoleCoder, allTasks) {
		t.Error("expected downstream to NOT be claimable when dep is IMPLEMENTING")
	}
}

// TestFix44_MixedDepsPartialSuperseded verifies that when one dep is MERGED
// and another is SUPERSEDED, the task is claimable (both satisfy).
func TestFix44_MixedDepsPartialSuperseded(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	allTasks := []Task{
		{
			ID:      "dep-merged",
			Status:  TaskStatusMerged,
			Created: now,
		},
		{
			ID:      "dep-superseded",
			Status:  TaskStatusSuperseded,
			Created: now,
		},
		{
			ID:        "downstream",
			Status:    TaskStatusReady,
			DependsOn: []string{"dep-merged", "dep-superseded"},
			SpecRef:   "spec.md",
			DoneWhen:  "done",
			Created:   now,
		},
	}

	downstream := &allTasks[2]
	if !downstream.IsClaimable(RoleCoder, allTasks) {
		t.Error("expected claimable with mixed MERGED+SUPERSEDED deps — Fix 44 regression")
	}
}

// TestFix44_ReadyDepStillBlocks verifies READY deps still block.
func TestFix44_ReadyDepStillBlocks(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	allTasks := []Task{
		{
			ID:      "dep-ready",
			Status:  TaskStatusReady,
			Created: now,
		},
		{
			ID:        "downstream",
			Status:    TaskStatusReady,
			DependsOn: []string{"dep-ready"},
			SpecRef:   "spec.md",
			DoneWhen:  "done",
			Created:   now,
		},
	}

	downstream := &allTasks[1]
	if downstream.IsClaimable(RoleCoder, allTasks) {
		t.Error("expected downstream to NOT be claimable when dep is READY")
	}
}
