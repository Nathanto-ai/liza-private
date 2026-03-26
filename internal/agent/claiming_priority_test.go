package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// TestClaimCoderTask_BasicPrioritySelection verifies that a high-priority task (priority 1)
// is selected over a low-priority task (priority 3) when both are claimable.
func TestClaimCoderTask_BasicPrioritySelection(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.SetupPipelineConfig(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// Create two claimable tasks with different priorities
	taskLow := testhelpers.BuildTaskByStatus("task-low", models.TaskStatusReady, now)
	taskLow.Priority = 3
	taskLow.Created = now.Add(-2 * time.Minute) // Older

	taskHigh := testhelpers.BuildTaskByStatus("task-high", models.TaskStatusReady, now)
	taskHigh.Priority = 1
	taskHigh.Created = now.Add(-1 * time.Minute) // Newer

	state.Tasks = []models.Task{taskLow, taskHigh}
	bb := testhelpers.WriteInitialState(t, statePath, state)

	// Claim a task
	taskID, _, err := claimCoderTask(tmpDir, "coder-1", bb)
	if err != nil {
		t.Fatalf("Expected successful claim, got error: %v", err)
	}

	// Should claim the high-priority task
	if taskID != "task-high" {
		t.Errorf("Expected to claim 'task-high' (priority 1), but got '%s'", taskID)
	}
}

// TestClaimCoderTask_SamePriorityRandomSelection verifies that when multiple tasks
// have the same priority, any task from the top tier is selected (randomized).
func TestClaimCoderTask_SamePriorityRandomSelection(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.SetupPipelineConfig(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// Create three tasks with same priority but different creation times
	taskOldest := testhelpers.BuildTaskByStatus("task-oldest", models.TaskStatusReady, now)
	taskOldest.Priority = 2
	taskOldest.Created = now.Add(-3 * time.Minute)

	taskMiddle := testhelpers.BuildTaskByStatus("task-middle", models.TaskStatusReady, now)
	taskMiddle.Priority = 2
	taskMiddle.Created = now.Add(-2 * time.Minute)

	taskNewest := testhelpers.BuildTaskByStatus("task-newest", models.TaskStatusReady, now)
	taskNewest.Priority = 2
	taskNewest.Created = now.Add(-1 * time.Minute)

	state.Tasks = []models.Task{taskNewest, taskMiddle, taskOldest} // Reverse order
	bb := testhelpers.WriteInitialState(t, statePath, state)

	// Claim a task
	taskID, _, err := claimCoderTask(tmpDir, "coder-1", bb)
	if err != nil {
		t.Fatalf("Expected successful claim, got error: %v", err)
	}

	// Should claim any task from the same-priority tier (randomized selection)
	validIDs := map[string]bool{"task-oldest": true, "task-middle": true, "task-newest": true}
	if !validIDs[taskID] {
		t.Errorf("Expected one of %v, but got '%s'", validIDs, taskID)
	}
}

// TestClaimCoderTask_RespectsClaimability verifies that a higher-priority blocked task
// is skipped in favor of a lower-priority claimable task.
func TestClaimCoderTask_RespectsClaimability(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.SetupPipelineConfig(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// Create a dependency task that blocks the high-priority task
	taskDep := testhelpers.BuildTaskByStatus("task-dep", models.TaskStatusReady, now)
	taskDep.Priority = 3
	taskDep.Created = now.Add(-3 * time.Minute)

	// Create high-priority task that is blocked by dependency
	taskHighBlocked := testhelpers.BuildTaskByStatus("task-high-blocked", models.TaskStatusReady, now)
	taskHighBlocked.Priority = 1
	taskHighBlocked.Created = now.Add(-2 * time.Minute)
	taskHighBlocked.DependsOn = []string{"task-dep"}

	// Create low-priority task that is claimable
	taskLowClaimable := testhelpers.BuildTaskByStatus("task-low-claimable", models.TaskStatusReady, now)
	taskLowClaimable.Priority = 3
	taskLowClaimable.Created = now.Add(-1 * time.Minute)

	state.Tasks = []models.Task{taskHighBlocked, taskLowClaimable, taskDep}
	bb := testhelpers.WriteInitialState(t, statePath, state)

	// Claim a task
	taskID, _, err := claimCoderTask(tmpDir, "coder-1", bb)
	if err != nil {
		t.Fatalf("Expected successful claim, got error: %v", err)
	}

	// Both task-dep and task-low-claimable are priority 3 and claimable (randomized)
	validIDs := map[string]bool{"task-dep": true, "task-low-claimable": true}
	if !validIDs[taskID] {
		t.Errorf("Expected one of %v, but got '%s'", validIDs, taskID)
	}
}

// TestClaimCoderTask_RejectedTasksPriority verifies that REJECTED tasks
// participate in priority-based selection.
func TestClaimCoderTask_RejectedTasksPriority(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.SetupPipelineConfig(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// Create a high-priority rejected task
	taskRejectedHigh := testhelpers.BuildTaskByStatus("task-rejected-high", models.TaskStatusRejected, now)
	taskRejectedHigh.Priority = 1
	taskRejectedHigh.Created = now.Add(-2 * time.Minute)
	taskRejectedHigh.AssignedTo = nil // Clear assignment so it's claimable

	// Create a low-priority unclaimed task
	taskUnclaimedLow := testhelpers.BuildTaskByStatus("task-unclaimed-low", models.TaskStatusReady, now)
	taskUnclaimedLow.Priority = 3
	taskUnclaimedLow.Created = now.Add(-1 * time.Minute)

	state.Tasks = []models.Task{taskUnclaimedLow, taskRejectedHigh}
	bb := testhelpers.WriteInitialState(t, statePath, state)

	// Claim a task
	taskID, _, err := claimCoderTask(tmpDir, "coder-1", bb)
	if err != nil {
		t.Fatalf("Expected successful claim, got error: %v", err)
	}

	// Should claim the high-priority rejected task
	if taskID != "task-rejected-high" {
		t.Errorf("Expected to claim 'task-rejected-high' (priority 1), but got '%s'", taskID)
	}
}

// TestClaimCoderTask_IntegrationFailedPriority verifies that INTEGRATION_FAILED tasks
// participate in priority-based selection.
func TestClaimCoderTask_IntegrationFailedPriority(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.SetupPipelineConfig(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// Create a high-priority integration failed task
	taskFailedHigh := testhelpers.BuildTaskByStatus("task-failed-high", models.TaskStatusIntegrationFailed, now)
	taskFailedHigh.Priority = 1
	taskFailedHigh.Created = now.Add(-2 * time.Minute)
	taskFailedHigh.AssignedTo = nil // Clear assignment so it's claimable

	// Create worktree for integration failed task (required)
	testhelpers.CreateTestWorktree(t, tmpDir, "task-failed-high")

	// Create a low-priority unclaimed task
	taskUnclaimedLow := testhelpers.BuildTaskByStatus("task-unclaimed-low", models.TaskStatusReady, now)
	taskUnclaimedLow.Priority = 4
	taskUnclaimedLow.Created = now.Add(-1 * time.Minute)

	state.Tasks = []models.Task{taskUnclaimedLow, taskFailedHigh}
	bb := testhelpers.WriteInitialState(t, statePath, state)

	// Claim a task
	taskID, _, err := claimCoderTask(tmpDir, "coder-1", bb)
	if err != nil {
		t.Fatalf("Expected successful claim, got error: %v", err)
	}

	// Should claim the high-priority integration failed task
	if taskID != "task-failed-high" {
		t.Errorf("Expected to claim 'task-failed-high' (priority 1), but got '%s'", taskID)
	}
}

// TestClaimCoderTask_AllSamePriority verifies that any task in the same-priority
// tier can be selected (randomized, not deterministic FIFO).
func TestClaimCoderTask_AllSamePriority(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.SetupPipelineConfig(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// Create three tasks with same priority
	task1 := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReady, now)
	task1.Priority = 3
	task1.Created = now.Add(-3 * time.Minute)

	task2 := testhelpers.BuildTaskByStatus("task-2", models.TaskStatusReady, now)
	task2.Priority = 3
	task2.Created = now.Add(-2 * time.Minute)

	task3 := testhelpers.BuildTaskByStatus("task-3", models.TaskStatusReady, now)
	task3.Priority = 3
	task3.Created = now.Add(-1 * time.Minute)

	state.Tasks = []models.Task{task3, task2, task1} // Out of order
	bb := testhelpers.WriteInitialState(t, statePath, state)

	// Claim a task
	taskID, _, err := claimCoderTask(tmpDir, "coder-1", bb)
	if err != nil {
		t.Fatalf("Expected successful claim, got error: %v", err)
	}

	// Should claim any task from the same-priority tier (randomized)
	validIDs := map[string]bool{"task-1": true, "task-2": true, "task-3": true}
	if !validIDs[taskID] {
		t.Errorf("Expected one of %v, but got '%s'", validIDs, taskID)
	}
}

// TestClaimCoderTask_MixedPriorities verifies correct selection across all priority levels (1-5).
func TestClaimCoderTask_MixedPriorities(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.SetupPipelineConfig(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// Create tasks with all priority levels (1-5)
	priorities := []struct {
		id       string
		priority int
		offset   time.Duration
	}{
		{"task-p5", 5, -5 * time.Minute},
		{"task-p3", 3, -4 * time.Minute},
		{"task-p1", 1, -3 * time.Minute},
		{"task-p4", 4, -2 * time.Minute},
		{"task-p2", 2, -1 * time.Minute},
	}

	for _, p := range priorities {
		task := testhelpers.BuildTaskByStatus(p.id, models.TaskStatusReady, now)
		task.Priority = p.priority
		task.Created = now.Add(p.offset)
		state.Tasks = append(state.Tasks, task)
	}

	bb := testhelpers.WriteInitialState(t, statePath, state)

	// Claim a task
	taskID, _, err := claimCoderTask(tmpDir, "coder-1", bb)
	if err != nil {
		t.Fatalf("Expected successful claim, got error: %v", err)
	}

	// Should claim priority 1 task
	if taskID != "task-p1" {
		t.Errorf("Expected to claim 'task-p1' (priority 1), but got '%s'", taskID)
	}
}

// TestClaimCoderTask_NoClaimableTasks verifies proper error handling when no tasks are claimable.
func TestClaimCoderTask_NoClaimableTasks(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.SetupPipelineConfig(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// Create only non-claimable tasks (assigned to a different agent)
	taskClaimed := testhelpers.BuildTaskByStatus("task-claimed", models.TaskStatusImplementing, now)
	taskClaimed.AssignedTo = testhelpers.StringPtr("coder-2")
	taskClaimed.Priority = 1

	taskMerged := testhelpers.BuildTaskByStatus("task-merged", models.TaskStatusMerged, now)
	taskMerged.Priority = 1

	state.Tasks = []models.Task{taskClaimed, taskMerged}
	bb := testhelpers.WriteInitialState(t, statePath, state)

	// Attempt to claim a task — should fail because there are no READY tasks
	// and the IMPLEMENTING task belongs to a different agent
	_, _, err := claimCoderTask(tmpDir, "coder-1", bb)
	if err == nil {
		t.Fatal("Expected error when no claimable tasks, got nil")
	}

	if err.Error() != "no claimable tasks found" {
		t.Errorf("Expected 'no claimable tasks found' error, got: %v", err)
	}
}

// TestClaimCoderTask_EmptyTaskList verifies proper error handling with an empty task list.
func TestClaimCoderTask_EmptyTaskList(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.SetupPipelineConfig(t, tmpDir)

	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{} // Empty
	bb := testhelpers.WriteInitialState(t, statePath, state)

	// Attempt to claim a task
	_, _, err := claimCoderTask(tmpDir, "coder-1", bb)
	if err == nil {
		t.Fatal("Expected error with empty task list, got nil")
	}

	if err.Error() != "no claimable tasks found" {
		t.Errorf("Expected 'no claimable tasks found' error, got: %v", err)
	}
}

// TestClaimReviewerTask_BasicPrioritySelection verifies that a high-priority reviewable task
// is selected over a low-priority reviewable task.
func TestClaimReviewerTask_BasicPrioritySelection(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.SetupPipelineConfig(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// Create two reviewable tasks with different priorities
	taskLow := testhelpers.BuildTaskByStatus("task-low", models.TaskStatusReadyForReview, now)
	taskLow.Priority = 4
	taskLow.Created = now.Add(-2 * time.Minute)

	taskHigh := testhelpers.BuildTaskByStatus("task-high", models.TaskStatusReadyForReview, now)
	taskHigh.Priority = 2
	taskHigh.Created = now.Add(-1 * time.Minute)

	state.Tasks = []models.Task{taskLow, taskHigh}
	bb := testhelpers.WriteInitialState(t, statePath, state)

	// Claim a reviewer task
	taskID, _, _, err := claimReviewerTask(tmpDir, "code-reviewer-1", 1800, bb)
	if err != nil {
		t.Fatalf("Expected successful claim, got error: %v", err)
	}

	// Should claim the high-priority task
	if taskID != "task-high" {
		t.Errorf("Expected to claim 'task-high' (priority 2), but got '%s'", taskID)
	}
}

// TestClaimReviewerTask_SamePriorityRandomSelection verifies that when multiple reviewable tasks
// have the same priority, any task from the tier is selected (randomized).
func TestClaimReviewerTask_SamePriorityRandomSelection(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.SetupPipelineConfig(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// Create three reviewable tasks with same priority
	taskOldest := testhelpers.BuildTaskByStatus("task-oldest", models.TaskStatusReadyForReview, now)
	taskOldest.Priority = 3
	taskOldest.Created = now.Add(-3 * time.Minute)

	taskMiddle := testhelpers.BuildTaskByStatus("task-middle", models.TaskStatusReadyForReview, now)
	taskMiddle.Priority = 3
	taskMiddle.Created = now.Add(-2 * time.Minute)

	taskNewest := testhelpers.BuildTaskByStatus("task-newest", models.TaskStatusReadyForReview, now)
	taskNewest.Priority = 3
	taskNewest.Created = now.Add(-1 * time.Minute)

	state.Tasks = []models.Task{taskNewest, taskMiddle, taskOldest}
	bb := testhelpers.WriteInitialState(t, statePath, state)

	// Claim a reviewer task
	taskID, _, _, err := claimReviewerTask(tmpDir, "code-reviewer-1", 1800, bb)
	if err != nil {
		t.Fatalf("Expected successful claim, got error: %v", err)
	}

	// Should claim any task from the same-priority tier (randomized)
	validIDs := map[string]bool{"task-oldest": true, "task-middle": true, "task-newest": true}
	if !validIDs[taskID] {
		t.Errorf("Expected one of %v, but got '%s'", validIDs, taskID)
	}
}

// TestClaimReviewerTask_SkipsClaimedReviewTasks verifies that tasks already being reviewed
// by another reviewer are skipped, and the next highest-priority available task is selected.
func TestClaimReviewerTask_SkipsClaimedReviewTasks(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.SetupPipelineConfig(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// High-priority task already being reviewed by a DIFFERENT reviewer
	// (REVIEWING state with ReviewingBy set to another agent — not a candidate)
	taskHighClaimed := testhelpers.BuildTaskByStatus("task-high-claimed", models.TaskStatusReviewing, now)
	taskHighClaimed.Priority = 1
	taskHighClaimed.Created = now.Add(-3 * time.Minute)
	otherReviewer := "code-reviewer-2"
	taskHighClaimed.ReviewingBy = &otherReviewer

	// Lower-priority task available for review
	taskLowAvailable := testhelpers.BuildTaskByStatus("task-low-available", models.TaskStatusReadyForReview, now)
	taskLowAvailable.Priority = 3
	taskLowAvailable.Created = now.Add(-2 * time.Minute)

	state.Tasks = []models.Task{taskHighClaimed, taskLowAvailable}
	bb := testhelpers.WriteInitialState(t, statePath, state)

	// Claim a reviewer task
	taskID, _, _, err := claimReviewerTask(tmpDir, "code-reviewer-1", 1800, bb)
	if err != nil {
		t.Fatalf("Expected successful claim, got error: %v", err)
	}

	// Should claim the available lower-priority task
	if taskID != "task-low-available" {
		t.Errorf("Expected to claim 'task-low-available', but got '%s'", taskID)
	}
}

// TestClaimReviewerTask_ReclaimsExpiredLease verifies that a high-priority task with an
// expired review lease is reclaimed over a lower-priority task.
func TestClaimReviewerTask_ReclaimsExpiredLease(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.SetupPipelineConfig(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// High-priority task with expired lease
	taskHighExpired := testhelpers.BuildTaskByStatus("task-high-expired", models.TaskStatusReadyForReview, now)
	taskHighExpired.Priority = 1
	taskHighExpired.Created = now.Add(-3 * time.Minute)
	reviewer := "code-reviewer-99"
	taskHighExpired.ReviewingBy = &reviewer
	expiredLease := now.Add(-5 * time.Minute) // Expired
	taskHighExpired.ReviewLeaseExpires = &expiredLease

	// Lower-priority task available
	taskLowAvailable := testhelpers.BuildTaskByStatus("task-low-available", models.TaskStatusReadyForReview, now)
	taskLowAvailable.Priority = 4
	taskLowAvailable.Created = now.Add(-2 * time.Minute)

	state.Tasks = []models.Task{taskLowAvailable, taskHighExpired}
	bb := testhelpers.WriteInitialState(t, statePath, state)

	// Claim a reviewer task
	taskID, _, _, err := claimReviewerTask(tmpDir, "code-reviewer-1", 1800, bb)
	if err != nil {
		t.Fatalf("Expected successful claim, got error: %v", err)
	}

	// Should reclaim the high-priority expired lease task
	if taskID != "task-high-expired" {
		t.Errorf("Expected to claim 'task-high-expired' (priority 1, expired lease), but got '%s'", taskID)
	}
}

// TestClaimReviewerTask_NoReviewableTasks verifies proper error handling when no tasks
// are available for review.
func TestClaimReviewerTask_NoReviewableTasks(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.SetupPipelineConfig(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// Only create tasks in non-reviewable states
	taskUnclaimed := testhelpers.BuildTaskByStatus("task-unclaimed", models.TaskStatusReady, now)
	taskUnclaimed.Priority = 1

	taskClaimed := testhelpers.BuildTaskByStatus("task-claimed", models.TaskStatusImplementing, now)
	taskClaimed.Priority = 1

	state.Tasks = []models.Task{taskUnclaimed, taskClaimed}
	bb := testhelpers.WriteInitialState(t, statePath, state)

	// Attempt to claim a reviewer task
	_, _, _, err := claimReviewerTask(tmpDir, "code-reviewer-1", 1800, bb)
	if err == nil {
		t.Fatal("Expected error when no reviewable tasks, got nil")
	}

	// claimReviewerTask wraps the error in a Modify() call, so check for substring
	if !strings.Contains(err.Error(), "no reviewable tasks found") {
		t.Errorf("Expected error containing 'no reviewable tasks found', got: %v", err)
	}
}

// TestShuffledByPriorityTier verifies that shuffledByPriorityTier filters to
// the highest-priority tier and includes all members.
func TestShuffledByPriorityTier(t *testing.T) {
	now := time.Now().UTC()

	t.Run("empty candidates", func(t *testing.T) {
		result := shuffledByPriorityTier(nil)
		if result != nil {
			t.Errorf("Expected nil for empty input, got %v", result)
		}
	})

	t.Run("single candidate", func(t *testing.T) {
		task := &models.Task{ID: "only", Priority: 1, Created: now}
		result := shuffledByPriorityTier([]*models.Task{task})
		if len(result) != 1 || result[0].ID != "only" {
			t.Errorf("Expected [only], got %v", result)
		}
	})

	t.Run("filters to top tier", func(t *testing.T) {
		p1a := &models.Task{ID: "p1a", Priority: 1, Created: now}
		p1b := &models.Task{ID: "p1b", Priority: 1, Created: now.Add(-time.Minute)}
		p2 := &models.Task{ID: "p2", Priority: 2, Created: now}
		p3 := &models.Task{ID: "p3", Priority: 3, Created: now}

		result := shuffledByPriorityTier([]*models.Task{p3, p1a, p2, p1b})
		if len(result) != 2 {
			t.Fatalf("Expected 2 tasks in top tier, got %d", len(result))
		}

		ids := map[string]bool{result[0].ID: true, result[1].ID: true}
		if !ids["p1a"] || !ids["p1b"] {
			t.Errorf("Expected {p1a, p1b}, got %v", ids)
		}
	})
}

// TestClaimDoerTask_RetriesOnFailure verifies that the retry loop skips a
// broken task and claims the next one in the shuffled tier.
func TestClaimDoerTask_RetriesOnFailure(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.SetupPipelineConfig(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// Create two same-priority tasks: one REJECTED (needs worktree but has none)
	// and one READY (will get a fresh worktree via ClaimTask).
	taskBroken := testhelpers.BuildTaskByStatus("task-broken", models.TaskStatusRejected, now)
	taskBroken.Priority = 1
	taskBroken.Created = now.Add(-2 * time.Minute)
	taskBroken.AssignedTo = nil
	// No worktree created — ClaimTask will fail for this one

	taskGood := testhelpers.BuildTaskByStatus("task-good", models.TaskStatusReady, now)
	taskGood.Priority = 1
	taskGood.Created = now.Add(-1 * time.Minute)

	state.Tasks = []models.Task{taskBroken, taskGood}
	bb := testhelpers.WriteInitialState(t, statePath, state)

	// Run enough times to verify that we always succeed (retry logic kicks in
	// when the broken task is tried first).
	for i := 0; i < 5; i++ {
		// Re-create state for each iteration to reset claim state
		state.Tasks[0].AssignedTo = nil
		state.Tasks[0].Status = models.TaskStatusRejected
		state.Tasks[1].AssignedTo = nil
		state.Tasks[1].Status = models.TaskStatusReady
		testhelpers.WriteInitialState(t, statePath, state)

		taskID, _, err := claimCoderTask(tmpDir, "coder-1", bb)
		if err != nil {
			t.Fatalf("Iteration %d: Expected successful claim, got error: %v", i, err)
		}
		if taskID != "task-good" {
			// task-broken may also succeed if ClaimTask handles it — either is fine
			if taskID != "task-broken" {
				t.Errorf("Iteration %d: unexpected task ID: %s", i, taskID)
			}
		}
	}
}
