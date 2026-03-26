package agent

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
)

// Regression tests for V&V Run 8 fixes.

// --- Fix 42: Reviewer re-claim after session exits without verdict ---

// TestFix42_ReviewerReClaimsOwnReviewingTask verifies that when a reviewer's
// CLI session exits without calling liza_submit_verdict, the next supervisor
// iteration re-claims the REVIEWING task instead of failing with "no reviewable
// tasks found".
// Bug: ISSUE-R8-01 — reviewer copilot exits without verdict, task stuck in
// REVIEWING, supervisor in retry loop.
func TestFix42_ReviewerReClaimsOwnReviewingTask(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.yaml")
	now := time.Now().UTC()
	lease := now.Add(30 * time.Minute)
	reviewer := "code-reviewer-1"
	wt := ".worktrees/task-review"
	rc := "abc1234"

	state := &models.State{
		Version: 1,
		Goal: models.Goal{
			ID:          "goal-1",
			Description: "Test goal",
			SpecRef:     "spec.md",
			Created:     now,
			Status:      models.GoalStatusInProgress,
		},
		Tasks: []models.Task{
			{
				ID:                 "task-stuck",
				Description:        "Task stuck in REVIEWING",
				Status:             models.TaskStatusReviewing,
				ReviewingBy:        &reviewer,
				ReviewLeaseExpires: &lease,
				Worktree:           &wt,
				ReviewCommit:       &rc,
				Priority:           1,
				SpecRef:            "spec.md",
				DoneWhen:           "done",
				Created:            now,
			},
		},
		Agents: map[string]models.Agent{
			reviewer: {
				Role:      "code-reviewer",
				Status:    models.AgentStatusReviewing,
				Heartbeat: now,
			},
		},
		Config: models.Config{
			IntegrationBranch: "main",
		},
	}

	bb := db.New(statePath)
	if err := bb.Write(state); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// claimReviewerTask should find the own REVIEWING task and return it
	// instead of calling ops.ClaimReviewerTask (which would fail).
	taskID, worktree, reviewCommit, err := claimReviewerTask(dir, reviewer, 1800, bb)
	if err != nil {
		t.Fatalf("claimReviewerTask() error: %v", err)
	}
	if taskID != "task-stuck" {
		t.Errorf("expected task-stuck, got %s", taskID)
	}
	if worktree != wt {
		t.Errorf("expected worktree %s, got %s", wt, worktree)
	}
	if reviewCommit != rc {
		t.Errorf("expected review_commit %s, got %s", rc, reviewCommit)
	}
}

// TestFix42_ReviewerDoesNotReClaimOtherTask verifies that a reviewer does NOT
// re-claim a REVIEWING task assigned to a different reviewer.
func TestFix42_ReviewerDoesNotReClaimOtherTask(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.yaml")
	now := time.Now().UTC()
	lease := now.Add(30 * time.Minute)
	otherReviewer := "code-reviewer-2"

	state := &models.State{
		Version: 1,
		Goal: models.Goal{
			ID:          "goal-1",
			Description: "Test goal",
			SpecRef:     "spec.md",
			Created:     now,
			Status:      models.GoalStatusInProgress,
		},
		Tasks: []models.Task{
			{
				ID:                 "task-other",
				Description:        "Task owned by another reviewer",
				Status:             models.TaskStatusReviewing,
				ReviewingBy:        &otherReviewer,
				ReviewLeaseExpires: &lease,
				Priority:           1,
				SpecRef:            "spec.md",
				DoneWhen:           "done",
				Created:            now,
			},
		},
		Agents: map[string]models.Agent{
			"code-reviewer-1": {
				Role:      "code-reviewer",
				Status:    models.AgentStatusIdle,
				Heartbeat: now,
			},
			otherReviewer: {
				Role:      "code-reviewer",
				Status:    models.AgentStatusReviewing,
				Heartbeat: now,
			},
		},
		Config: models.Config{
			IntegrationBranch: "main",
		},
	}

	bb := db.New(statePath)
	if err := bb.Write(state); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// claimReviewerTask for code-reviewer-1 should NOT find the task owned by
	// code-reviewer-2 and should fall through to ops.ClaimReviewerTask which
	// will error because there are no READY_FOR_REVIEW tasks.
	_, _, _, err := claimReviewerTask(dir, "code-reviewer-1", 1800, bb)
	if err == nil {
		t.Fatal("expected error for reviewer with no claimable tasks, got nil")
	}
}

// TestFix42_ReviewerReClaimNilFields verifies re-claim handles nil Worktree
// and nil ReviewCommit gracefully (returns empty strings).
func TestFix42_ReviewerReClaimNilFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.yaml")
	now := time.Now().UTC()
	lease := now.Add(30 * time.Minute)
	reviewer := "code-reviewer-1"

	state := &models.State{
		Version: 1,
		Goal: models.Goal{
			ID:          "goal-1",
			Description: "Test goal",
			SpecRef:     "spec.md",
			Created:     now,
			Status:      models.GoalStatusInProgress,
		},
		Tasks: []models.Task{
			{
				ID:                 "task-minimal",
				Description:        "Task with nil optional fields",
				Status:             models.TaskStatusReviewing,
				ReviewingBy:        &reviewer,
				ReviewLeaseExpires: &lease,
				Priority:           1,
				SpecRef:            "spec.md",
				DoneWhen:           "done",
				Created:            now,
			},
		},
		Agents: map[string]models.Agent{
			reviewer: {
				Role:      "code-reviewer",
				Status:    models.AgentStatusReviewing,
				Heartbeat: now,
			},
		},
		Config: models.Config{
			IntegrationBranch: "main",
		},
	}

	bb := db.New(statePath)
	if err := bb.Write(state); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	taskID, worktree, reviewCommit, err := claimReviewerTask(dir, reviewer, 1800, bb)
	if err != nil {
		t.Fatalf("claimReviewerTask() error: %v", err)
	}
	if taskID != "task-minimal" {
		t.Errorf("expected task-minimal, got %s", taskID)
	}
	if worktree != "" {
		t.Errorf("expected empty worktree for nil Worktree, got %q", worktree)
	}
	if reviewCommit != "" {
		t.Errorf("expected empty review_commit for nil ReviewCommit, got %q", reviewCommit)
	}
}
