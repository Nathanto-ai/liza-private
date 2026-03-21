package agent

import (
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

// TestClaimCoderTask_TieBreakingByCreationTime verifies that when multiple tasks
// have the same priority, the oldest task (by creation time) is selected.
func TestClaimCoderTask_TieBreakingByCreationTime(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

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

	// Should claim the oldest task
	if taskID != "task-oldest" {
		t.Errorf("Expected to claim 'task-oldest', but got '%s'", taskID)
	}
}

// TestClaimCoderTask_RespectsClaimability verifies that a higher-priority blocked task
// is skipped in favor of a lower-priority claimable task.
func TestClaimCoderTask_RespectsClaimability(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

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

	// Should claim the dependency task first (oldest claimable with priority 3)
	if taskID != "task-dep" {
		t.Errorf("Expected to claim 'task-dep', but got '%s'", taskID)
	}
}

// TestClaimCoderTask_RejectedTasksPriority verifies that REJECTED tasks
// participate in priority-based selection.
func TestClaimCoderTask_RejectedTasksPriority(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

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

// TestClaimCoderTask_AllSamePriority verifies FIFO behavior when all tasks have the same priority.
func TestClaimCoderTask_AllSamePriority(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

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

	// Should claim the oldest task (FIFO)
	if taskID != "task-1" {
		t.Errorf("Expected to claim 'task-1' (oldest), but got '%s'", taskID)
	}
}

// TestClaimCoderTask_MixedPriorities verifies correct selection across all priority levels (1-5).
func TestClaimCoderTask_MixedPriorities(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

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

// TestClaimReviewerTask_TieBreakingByCreationTime verifies that when multiple reviewable tasks
// have the same priority, the oldest task is selected.
func TestClaimReviewerTask_TieBreakingByCreationTime(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

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

	// Should claim the oldest task
	if taskID != "task-oldest" {
		t.Errorf("Expected to claim 'task-oldest', but got '%s'", taskID)
	}
}

// TestClaimReviewerTask_SkipsClaimedReviewTasks verifies that tasks already being reviewed
// by another reviewer are skipped, and the next highest-priority available task is selected.
func TestClaimReviewerTask_SkipsClaimedReviewTasks(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

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
	if err.Error() != "modification function failed: no reviewable tasks found" {
		t.Errorf("Expected 'modification function failed: no reviewable tasks found' error, got: %v", err)
	}
}
