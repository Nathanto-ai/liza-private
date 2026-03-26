package agent

import (
	"testing"

	"github.com/liza-mas/liza/internal/models"
)

// Regression tests for V&V Run 3 fixes — agent layer.

// TestFix18_ReviewerReclaimDetection verifies the reviewer wait function
// detects tasks in REVIEWING state assigned to this reviewer agent.
func TestFix18_ReviewerReclaimDetection(t *testing.T) {
	t.Parallel()

	reviewerID := "code-reviewer-1"
	state := &models.State{
		Tasks: []models.Task{
			{
				ID:          "task-reviewing",
				Status:      models.TaskStatusReviewing,
				ReviewingBy: strPtr(reviewerID),
			},
			{
				ID:     "task-ready",
				Status: models.TaskStatusReadyForReview,
			},
			{
				ID:          "task-other-reviewer",
				Status:      models.TaskStatusReviewing,
				ReviewingBy: strPtr("code-reviewer-2"),
			},
		},
	}

	count := countOwnReviewingTasks(state, reviewerID)
	if count != 1 {
		t.Errorf("countOwnReviewingTasks() = %d, want 1", count)
	}
}

// TestFix18_ReviewerReclaimZero verifies zero when no REVIEWING tasks match.
func TestFix18_ReviewerReclaimZero(t *testing.T) {
	t.Parallel()

	state := &models.State{
		Tasks: []models.Task{
			{ID: "task-ready", Status: models.TaskStatusReadyForReview},
		},
	}

	count := countOwnReviewingTasks(state, "code-reviewer-1")
	if count != 0 {
		t.Errorf("countOwnReviewingTasks() = %d, want 0", count)
	}
}

func strPtr(s string) *string {
	return &s
}
