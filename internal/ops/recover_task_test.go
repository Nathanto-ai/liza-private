package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// setupImplementingTask creates a state with coder-1 implementing task-1.
// The agent PID defaults to 999999 (dead). Returns (tmpDir, stateFile).
func setupImplementingTask(t *testing.T, agentPID int) (string, string) {
	t.Helper()
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	taskRef := "task-1"
	now := time.Now().UTC()
	leaseExpires := now.Add(-10 * time.Minute) // expired
	worktreeRef := ".worktrees/task-1"
	state := testhelpers.CreateValidState()
	state.Agents["coder-1"] = models.Agent{
		Role:         "coder",
		Status:       models.AgentStatusWorking,
		CurrentTask:  &taskRef,
		LeaseExpires: &leaseExpires,
		PID:          agentPID,
	}
	state.Tasks = append(state.Tasks, models.Task{
		ID:           "task-1",
		Description:  "test task",
		Status:       models.TaskStatusImplementing,
		Priority:     1,
		AssignedTo:   strPtr("coder-1"),
		Worktree:     &worktreeRef,
		LeaseExpires: &leaseExpires,
		SpecRef:      "spec.md",
		DoneWhen:     "tests pass",
		Scope:        "small",
	})
	testhelpers.WriteInitialState(t, stateFile, state)
	return tmpDir, stateFile
}

// setupEmptyState creates a valid state with no tasks or agents. Returns (tmpDir, stateFile).
func setupEmptyState(t *testing.T) (string, string) {
	t.Helper()
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.WriteInitialState(t, stateFile, testhelpers.CreateValidState())
	return tmpDir, stateFile
}

func TestRecoverTask_Validation(t *testing.T) {
	_, err := RecoverTask("/nonexistent", "", false, "reason")
	if err == nil {
		t.Fatal("Expected error for empty task ID")
	}
	if !strings.Contains(err.Error(), "task ID required") {
		t.Errorf("Error = %q, want to contain 'task ID required'", err.Error())
	}
}

func TestRecoverTask_InvalidTaskID(t *testing.T) {
	_, err := RecoverTask("/nonexistent", "../escape", false, "reason")
	if err == nil {
		t.Fatal("Expected error for invalid task ID")
	}
	if !strings.Contains(err.Error(), "invalid task ID") {
		t.Errorf("Error = %q, want to contain 'invalid task ID'", err.Error())
	}
}

func TestRecoverTask_NotInState_NoForce(t *testing.T) {
	tmpDir, _ := setupEmptyState(t)

	_, err := RecoverTask(tmpDir, "task-1", false, "reason")
	if err == nil {
		t.Fatal("Expected error for task not in state without --force")
	}
	if !strings.Contains(err.Error(), "not found in state") {
		t.Errorf("Error = %q, want to contain 'not found in state'", err.Error())
	}
}

func TestRecoverTask_NotInState_Force_CleansGitArtifacts(t *testing.T) {
	tmpDir, _ := setupEmptyState(t)
	testhelpers.SetupTestGitRepo(t, tmpDir)

	// Create a worktree directory (simulating orphaned artifact)
	wtDir := filepath.Join(tmpDir, ".worktrees", "task-1")
	if err := os.MkdirAll(wtDir, 0755); err != nil {
		t.Fatalf("Failed to create worktree dir: %v", err)
	}

	result, err := RecoverTask(tmpDir, "task-1", true, "orphan cleanup")
	if err != nil {
		t.Fatalf("RecoverTask() error: %v", err)
	}

	if result.InState {
		t.Error("Expected InState=false")
	}
	if result.AgentRecovered {
		t.Error("Expected AgentRecovered=false")
	}
	if _, err := os.Stat(wtDir); !os.IsNotExist(err) {
		t.Error("Expected worktree directory to be removed")
	}
}

func TestRecoverTask_NotInState_Force_NothingToClean(t *testing.T) {
	tmpDir, _ := setupEmptyState(t)
	testhelpers.SetupTestGitRepo(t, tmpDir)

	result, err := RecoverTask(tmpDir, "task-1", true, "cleanup")
	if err != nil {
		t.Fatalf("RecoverTask() error: %v", err)
	}

	if result.InState {
		t.Error("Expected InState=false")
	}
}

func TestRecoverTask_ImplementingTask_WithAgent(t *testing.T) {
	tmpDir, stateFile := setupImplementingTask(t, 999999)

	result, err := RecoverTask(tmpDir, "task-1", false, "crashed")
	if err != nil {
		t.Fatalf("RecoverTask() error: %v", err)
	}

	if !result.InState {
		t.Error("Expected InState=true")
	}
	if result.AgentID != "coder-1" {
		t.Errorf("AgentID = %q, want %q", result.AgentID, "coder-1")
	}
	if !result.ClaimReleased {
		t.Error("Expected ClaimReleased=true")
	}
	if !result.AgentRecovered {
		t.Error("Expected AgentRecovered=true")
	}

	// Verify state
	readState, err := db.New(stateFile).Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}

	if _, exists := readState.Agents["coder-1"]; exists {
		t.Error("Agent should be removed from state")
	}

	task := readState.FindTask("task-1")
	if task == nil {
		t.Fatal("Task should still exist")
	}
	if task.Status != models.TaskStatusReady {
		t.Errorf("Task status = %s, want %s", task.Status, models.TaskStatusReady)
	}
	if task.AssignedTo != nil {
		t.Error("Task AssignedTo should be nil")
	}
	if task.LeaseExpires != nil {
		t.Error("Task LeaseExpires should be nil")
	}
	if task.Worktree != nil {
		t.Error("Task Worktree should be nil")
	}

	lastNote := readState.HumanNotes[len(readState.HumanNotes)-1]
	if !strings.Contains(lastNote.Message, "task-1") || !strings.Contains(lastNote.Message, "coder-1") {
		t.Errorf("Note message = %q, want to contain task and agent IDs", lastNote.Message)
	}
}

func TestRecoverTask_ReviewingTask(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	taskRef := "task-2"
	now := time.Now().UTC()
	leaseExpires := now.Add(-10 * time.Minute)
	reviewCommit := "abc123"
	baseCommit := "def456"
	worktreeRef := ".worktrees/task-2"
	state := testhelpers.CreateValidState()
	state.Agents["code-reviewer-1"] = models.Agent{
		Role:         "code-reviewer",
		Status:       models.AgentStatusReviewing,
		CurrentTask:  &taskRef,
		LeaseExpires: &leaseExpires,
	}
	state.Tasks = append(state.Tasks, models.Task{
		ID:                 "task-2",
		Description:        "test task",
		Status:             models.TaskStatusReviewing,
		Priority:           1,
		ReviewingBy:        strPtr("code-reviewer-1"),
		ReviewLeaseExpires: &leaseExpires,
		ReviewCommit:       &reviewCommit,
		BaseCommit:         &baseCommit,
		Worktree:           &worktreeRef,
		SpecRef:            "spec.md",
		DoneWhen:           "tests pass",
		Scope:              "small",
	})
	testhelpers.WriteInitialState(t, stateFile, state)

	result, err := RecoverTask(tmpDir, "task-2", false, "crashed")
	if err != nil {
		t.Fatalf("RecoverTask() error: %v", err)
	}

	if result.AgentID != "code-reviewer-1" {
		t.Errorf("AgentID = %q, want %q", result.AgentID, "code-reviewer-1")
	}
	if !result.ClaimReleased {
		t.Error("Expected ClaimReleased=true")
	}
	if !result.AgentRecovered {
		t.Error("Expected AgentRecovered=true")
	}

	readState, err := db.New(stateFile).Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}
	if _, exists := readState.Agents["code-reviewer-1"]; exists {
		t.Error("Agent should be removed from state")
	}
	task := readState.FindTask("task-2")
	if task == nil {
		t.Fatal("Task should still exist")
	}
	if task.Status != models.TaskStatusReadyForReview {
		t.Errorf("Task status = %s, want %s", task.Status, models.TaskStatusReadyForReview)
	}
	if task.ReviewingBy != nil {
		t.Error("Task ReviewingBy should be nil")
	}
}

func TestRecoverTask_DualClaim_ReviewerPIDAlive_NoForce(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	coderTask := "task-1"
	reviewerTask := "task-1"
	now := time.Now().UTC()
	expiredLease := now.Add(-10 * time.Minute)
	activeLease := now.Add(10 * time.Minute)
	worktreeRef := ".worktrees/task-1"
	reviewCommit := "abc123"
	baseCommit := "def456"
	state := testhelpers.CreateValidState()
	// Coder: dead PID, expired lease
	state.Agents["coder-1"] = models.Agent{
		Role:         "coder",
		Status:       models.AgentStatusWaiting,
		CurrentTask:  &coderTask,
		LeaseExpires: &expiredLease,
		PID:          999999,
	}
	// Reviewer: alive PID, active lease
	state.Agents["code-reviewer-1"] = models.Agent{
		Role:         "code-reviewer",
		Status:       models.AgentStatusReviewing,
		CurrentTask:  &reviewerTask,
		LeaseExpires: &activeLease,
		PID:          os.Getpid(),
	}
	state.Tasks = append(state.Tasks, models.Task{
		ID:                 "task-1",
		Description:        "test task",
		Status:             models.TaskStatusReviewing,
		Priority:           1,
		AssignedTo:         strPtr("coder-1"),
		ReviewingBy:        strPtr("code-reviewer-1"),
		ReviewLeaseExpires: &activeLease,
		LeaseExpires:       &expiredLease,
		Worktree:           &worktreeRef,
		ReviewCommit:       &reviewCommit,
		BaseCommit:         &baseCommit,
		SpecRef:            "spec.md",
		DoneWhen:           "tests pass",
		Scope:              "small",
	})
	testhelpers.WriteInitialState(t, stateFile, state)

	// Should refuse — reviewer PID is alive
	_, err := RecoverTask(tmpDir, "task-1", false, "reason")
	if err == nil {
		t.Fatal("Expected error for alive reviewer PID without force")
	}
	if !strings.Contains(err.Error(), "reviewer") {
		t.Errorf("Error = %q, want to mention 'reviewer'", err.Error())
	}
	if !strings.Contains(err.Error(), "still running") {
		t.Errorf("Error = %q, want to contain 'still running'", err.Error())
	}

	// Verify nothing was modified
	readState, _ := db.New(stateFile).Read()
	if _, exists := readState.Agents["code-reviewer-1"]; !exists {
		t.Error("Reviewer agent should NOT have been deleted")
	}
	if _, exists := readState.Agents["coder-1"]; !exists {
		t.Error("Coder agent should NOT have been deleted")
	}
}

func TestRecoverTask_PIDAlive_NoForce(t *testing.T) {
	tmpDir, _ := setupImplementingTask(t, os.Getpid())

	_, err := RecoverTask(tmpDir, "task-1", false, "reason")
	if err == nil {
		t.Fatal("Expected error for alive PID without force")
	}
	if !strings.Contains(err.Error(), "still running") {
		t.Errorf("Error = %q, want to contain 'still running'", err.Error())
	}
}

func TestRecoverTask_PIDAlive_WithForce(t *testing.T) {
	tmpDir, _ := setupImplementingTask(t, os.Getpid())

	result, err := RecoverTask(tmpDir, "task-1", true, "forced recovery")
	if err != nil {
		t.Fatalf("RecoverTask() with force error: %v", err)
	}
	if !result.AgentRecovered {
		t.Error("Expected AgentRecovered=true with force")
	}
}

func TestRecoverTask_NoAgent_TaskOnly(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	worktreeRef := ".worktrees/task-1"
	state := testhelpers.CreateValidState()
	state.Tasks = append(state.Tasks, models.Task{
		ID:          "task-1",
		Description: "test task",
		Status:      models.TaskStatusReady,
		Priority:    1,
		Worktree:    &worktreeRef,
		SpecRef:     "spec.md",
		DoneWhen:    "tests pass",
		Scope:       "small",
	})
	testhelpers.WriteInitialState(t, stateFile, state)

	result, err := RecoverTask(tmpDir, "task-1", false, "cleanup")
	if err != nil {
		t.Fatalf("RecoverTask() error: %v", err)
	}

	if !result.InState {
		t.Error("Expected InState=true")
	}
	if result.AgentID != "" {
		t.Errorf("AgentID = %q, want empty", result.AgentID)
	}
	if result.AgentRecovered {
		t.Error("Expected AgentRecovered=false")
	}

	task, _ := db.New(stateFile).Read()
	if task.FindTask("task-1").Worktree != nil {
		t.Error("Task Worktree should be nil")
	}
}

func TestRecoverTask_Idempotent(t *testing.T) {
	tmpDir, _ := setupImplementingTask(t, 999999)

	result1, err := RecoverTask(tmpDir, "task-1", false, "first")
	if err != nil {
		t.Fatalf("First RecoverTask() error: %v", err)
	}
	if !result1.AgentRecovered {
		t.Error("First recovery should recover agent")
	}

	// Second recovery — task still in state but already clean
	result2, err := RecoverTask(tmpDir, "task-1", false, "second")
	if err != nil {
		t.Fatalf("Second RecoverTask() error: %v", err)
	}
	if result2.AgentRecovered {
		t.Error("Second recovery should not recover agent (already gone)")
	}
	if result2.ClaimReleased {
		t.Error("Second recovery should not release claim (already released)")
	}
}

func TestRecoverTask_DefaultReason(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	state := testhelpers.CreateValidState()
	state.Tasks = append(state.Tasks, models.Task{
		ID:          "task-1",
		Description: "test task",
		Status:      models.TaskStatusReady,
		Priority:    1,
		SpecRef:     "spec.md",
		DoneWhen:    "tests pass",
		Scope:       "small",
	})
	testhelpers.WriteInitialState(t, stateFile, state)

	result, err := RecoverTask(tmpDir, "task-1", false, "")
	if err != nil {
		t.Fatalf("RecoverTask() error: %v", err)
	}
	if !result.InState {
		t.Error("Expected InState=true")
	}

	readState, err := db.New(stateFile).Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}
	lastNote := readState.HumanNotes[len(readState.HumanNotes)-1]
	if !strings.Contains(lastNote.Message, "task recovery") {
		t.Errorf("Note message = %q, want to contain default reason 'task recovery'", lastNote.Message)
	}
}

// setupTaskInStatus creates a state with the given task in the specified status.
// For mid-pipeline statuses, sets up appropriate agent claims and fields.
func setupTaskInStatus(t *testing.T, taskID string, status models.TaskStatus) (string, string) {
	t.Helper()
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	leaseExpires := now.Add(-10 * time.Minute)
	worktreeRef := ".worktrees/" + taskID
	reviewCommit := "abc123"
	baseCommit := "def456"
	mergeCommit := "merge789"
	blockedReason := "some reason"

	state := testhelpers.CreateValidState()

	task := models.Task{
		ID:          taskID,
		Description: "test task",
		Status:      status,
		Priority:    1,
		SpecRef:     "spec.md",
		DoneWhen:    "tests pass",
		Scope:       "small",
		Worktree:    &worktreeRef,
		BaseCommit:  &baseCommit,
	}

	switch status {
	case models.TaskStatusIntegrationFailed:
		task.AssignedTo = strPtr("coder-1")
		task.LeaseExpires = &leaseExpires
		task.ReviewCommit = &reviewCommit
		task.ApprovedBy = strPtr("code-reviewer-1")
		task.MergeCommit = &mergeCommit
		task.FailedBy = append(task.FailedBy, "coder-1")
		state.Agents["coder-1"] = models.Agent{
			Role:   "coder",
			Status: models.AgentStatusIdle,
			PID:    999999,
		}
	case models.TaskStatusApproved:
		task.ReviewCommit = &reviewCommit
		task.ApprovedBy = strPtr("code-reviewer-1")
	case models.TaskStatusBlocked:
		task.BlockedReason = &blockedReason
		task.BlockedQuestions = []string{"What went wrong?"}
	case models.TaskStatusRejected:
		task.ReviewCommit = &reviewCommit
		task.FailedBy = append(task.FailedBy, "code-reviewer-1")
	}

	state.Tasks = append(state.Tasks, task)
	testhelpers.WriteInitialState(t, stateFile, state)
	return tmpDir, stateFile
}

func TestRecoverTask_IntegrationFailed_ResetsToReady(t *testing.T) {
	tmpDir, stateFile := setupTaskInStatus(t, "task-1", models.TaskStatusIntegrationFailed)

	result, err := RecoverTask(tmpDir, "task-1", true, "integration recovery")
	if err != nil {
		t.Fatalf("RecoverTask() error: %v", err)
	}
	if !result.InState {
		t.Error("Expected InState=true")
	}

	readState, _ := db.New(stateFile).Read()
	task := readState.FindTask("task-1")
	if task == nil {
		t.Fatal("Task should still exist")
	}
	if task.Status != models.TaskStatusReady {
		t.Errorf("Task status = %s, want READY", task.Status)
	}
	if task.ReviewCommit != nil {
		t.Error("ReviewCommit should be nil after reset")
	}
	if task.ApprovedBy != nil {
		t.Error("ApprovedBy should be nil after reset")
	}
	if task.MergeCommit != nil {
		t.Error("MergeCommit should be nil after reset")
	}
	if len(task.FailedBy) != 0 {
		t.Errorf("FailedBy should be empty, got %v", task.FailedBy)
	}

	// Should have status_reset history entry
	found := false
	for _, h := range task.History {
		if h.Event == "status_reset" {
			found = true
			if h.Reason == nil || !strings.Contains(*h.Reason, "INTEGRATION_FAILED") {
				t.Errorf("status_reset reason should mention INTEGRATION_FAILED, got %v", h.Reason)
			}
		}
	}
	if !found {
		t.Error("Expected status_reset history entry")
	}
}

func TestRecoverTask_Approved_ResetsToReady(t *testing.T) {
	tmpDir, stateFile := setupTaskInStatus(t, "task-1", models.TaskStatusApproved)

	result, err := RecoverTask(tmpDir, "task-1", true, "stale approval")
	if err != nil {
		t.Fatalf("RecoverTask() error: %v", err)
	}
	if !result.InState {
		t.Error("Expected InState=true")
	}

	readState, _ := db.New(stateFile).Read()
	task := readState.FindTask("task-1")
	if task.Status != models.TaskStatusReady {
		t.Errorf("Task status = %s, want READY", task.Status)
	}
	if task.ApprovedBy != nil {
		t.Error("ApprovedBy should be nil after reset")
	}
}

func TestRecoverTask_Blocked_ResetsToReady(t *testing.T) {
	tmpDir, stateFile := setupTaskInStatus(t, "task-1", models.TaskStatusBlocked)

	_, err := RecoverTask(tmpDir, "task-1", true, "unblocked")
	if err != nil {
		t.Fatalf("RecoverTask() error: %v", err)
	}

	readState, _ := db.New(stateFile).Read()
	task := readState.FindTask("task-1")
	if task.Status != models.TaskStatusReady {
		t.Errorf("Task status = %s, want READY", task.Status)
	}
	if task.BlockedReason != nil {
		t.Error("BlockedReason should be nil after reset")
	}
	if len(task.BlockedQuestions) != 0 {
		t.Error("BlockedQuestions should be empty after reset")
	}
}

func TestRecoverTask_Rejected_ResetsToReady(t *testing.T) {
	tmpDir, stateFile := setupTaskInStatus(t, "task-1", models.TaskStatusRejected)

	_, err := RecoverTask(tmpDir, "task-1", true, "retry")
	if err != nil {
		t.Fatalf("RecoverTask() error: %v", err)
	}

	readState, _ := db.New(stateFile).Read()
	task := readState.FindTask("task-1")
	if task.Status != models.TaskStatusReady {
		t.Errorf("Task status = %s, want READY", task.Status)
	}
}

func TestRecoverTask_Ready_StaysReady(t *testing.T) {
	// READY is not a stuck status — should not be modified
	tmpDir, stateFile := setupTaskInStatus(t, "task-1", models.TaskStatusReady)

	_, err := RecoverTask(tmpDir, "task-1", true, "cleanup")
	if err != nil {
		t.Fatalf("RecoverTask() error: %v", err)
	}

	readState, _ := db.New(stateFile).Read()
	task := readState.FindTask("task-1")
	if task.Status != models.TaskStatusReady {
		t.Errorf("Task status = %s, want READY", task.Status)
	}
}
