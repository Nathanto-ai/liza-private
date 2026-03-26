package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
)

// Regression tests for V&V Run 7 fixes.

// --- Fix 37: noSubmitTracker ---

// TestFix37_NoSubmitTrackerThreshold verifies that noSubmitTracker fires
// after the configured number of consecutive no-submit exits.
// Bug: Coder exits code 0 without calling liza_submit_for_review, task reverts
// to READY, gets re-claimed infinitely (ISSUE-R7-07).
func TestFix37_NoSubmitTrackerThreshold(t *testing.T) {
	t.Parallel()

	tracker := newNoSubmitTracker(models.Config{MaxNoSubmitIterations: 3})

	// First two records should not trigger
	if tracker.record("task-1") {
		t.Error("expected record #1 to return false")
	}
	if tracker.record("task-1") {
		t.Error("expected record #2 to return false")
	}
	// Third should trigger
	if !tracker.record("task-1") {
		t.Error("expected record #3 to return true (threshold reached)")
	}
}

// TestFix37_NoSubmitTrackerResetOnNewTask verifies that switching task IDs
// resets the counter.
func TestFix37_NoSubmitTrackerResetOnNewTask(t *testing.T) {
	t.Parallel()

	tracker := newNoSubmitTracker(models.Config{MaxNoSubmitIterations: 2})

	tracker.record("task-1")
	// Switch to a different task — counter should reset
	if tracker.record("task-2") {
		t.Error("expected counter to reset on new task ID")
	}
	if !tracker.record("task-2") {
		t.Error("expected second record on task-2 to trigger threshold")
	}
}

// TestFix37_NoSubmitTrackerReset verifies that explicit reset clears state.
func TestFix37_NoSubmitTrackerReset(t *testing.T) {
	t.Parallel()

	tracker := newNoSubmitTracker(models.Config{MaxNoSubmitIterations: 2})
	tracker.record("task-1")
	tracker.reset()

	if tracker.record("task-1") {
		t.Error("expected first record after reset to return false")
	}
}

// TestFix37_NoSubmitTrackerDefaultLimit verifies the default limit when
// MaxNoSubmitIterations is zero.
func TestFix37_NoSubmitTrackerDefaultLimit(t *testing.T) {
	t.Parallel()

	tracker := newNoSubmitTracker(models.Config{}) // zero = use default
	for i := 1; i < DefaultNoSubmitLimit; i++ {
		if tracker.record("task-1") {
			t.Errorf("expected record #%d to not trigger (default limit=%d)", i, DefaultNoSubmitLimit)
		}
	}
	if !tracker.record("task-1") {
		t.Errorf("expected record #%d to trigger at default limit", DefaultNoSubmitLimit)
	}
}

// TestFix37_BlockTaskOnNoSubmit verifies that blockTaskOnNoSubmit transitions
// a READY task to NEEDS_HUMAN_DECISION (Fix 43 changed from BLOCKED) with the correct reason.
func TestFix37_BlockTaskOnNoSubmit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.yaml")
	now := time.Now().UTC()

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
				ID:          "task-loop",
				Description: "Task stuck in loop",
				Status:      models.TaskStatusReady,
				Priority:    1,
				SpecRef:     "spec.md",
				DoneWhen:    "done",
				Created:     now,
			},
		},
		Agents: make(map[string]models.Agent),
		Config: models.Config{IntegrationBranch: "main"},
	}

	bb := db.New(statePath)
	if err := bb.Write(state); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	blockTaskOnNoSubmit(bb, "task-loop", "coder-1", 3, 3)

	readState, err := bb.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	task := readState.FindTask("task-loop")
	if task == nil {
		t.Fatal("task-loop not found in state")
	}
	// Fix 43: now NEEDS_HUMAN_DECISION instead of BLOCKED
	if task.Status != models.TaskStatusNeedsHumanDecision {
		t.Errorf("expected task status NEEDS_HUMAN_DECISION, got %s", task.Status)
	}
	if task.BlockedReason == nil {
		t.Fatal("expected BlockedReason to be set")
	}
	if task.AssignedTo != nil {
		t.Errorf("expected AssignedTo to be nil after escalation, got %v", *task.AssignedTo)
	}
}

// TestFix37_BlockTaskOnNoSubmitSkipsEmpty verifies no panic on empty taskID.
func TestFix37_BlockTaskOnNoSubmitSkipsEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.yaml")

	state := &models.State{
		Version: 1,
		Goal:    models.Goal{ID: "g", Description: "g", SpecRef: "s", Created: time.Now().UTC(), Status: models.GoalStatusInProgress},
		Agents:  make(map[string]models.Agent),
		Config:  models.Config{IntegrationBranch: "main"},
	}

	bb := db.New(statePath)
	if err := bb.Write(state); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Should not panic
	blockTaskOnNoSubmit(bb, "", "coder-1", 3, 3)
}

// --- Fix 41: detectAndFixStaleClaim ---

// TestFix41_DetectAndFixStaleClaim verifies that tasks IMPLEMENTING with
// a stale-heartbeat agent are released to READY.
func TestFix41_DetectAndFixStaleClaim(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.yaml")
	now := time.Now().UTC()
	staleTime := now.Add(-10 * time.Minute)
	deadAgent := "coder-dead"

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
				ID:          "task-stale",
				Description: "Stale task",
				Status:      models.TaskStatusImplementing,
				AssignedTo:  &deadAgent,
				Priority:    1,
				SpecRef:     "spec.md",
				DoneWhen:    "done",
				Created:     now,
			},
		},
		Agents: map[string]models.Agent{
			"coder-dead": {
				Role:      "coder",
				Status:    models.AgentStatusWorking,
				Heartbeat: staleTime, // 10 minutes old
			},
			"coder-alive": {
				Role:      "coder",
				Status:    models.AgentStatusIdle,
				Heartbeat: now,
			},
		},
		Config: models.Config{
			IntegrationBranch: "main",
			HeartbeatInterval: 60, // 1 minute
		},
	}

	bb := db.New(statePath)
	if err := bb.Write(state); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	fixed := detectAndFixStaleClaim(bb, "coder-alive")
	if !fixed {
		t.Fatal("expected detectAndFixStaleClaim to return true")
	}

	readState, err := bb.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	task := readState.FindTask("task-stale")
	if task == nil {
		t.Fatal("task-stale not found")
	}
	if task.Status != models.TaskStatusReady {
		t.Errorf("expected READY, got %s", task.Status)
	}
	if task.AssignedTo != nil {
		t.Errorf("expected AssignedTo nil, got %v", *task.AssignedTo)
	}
}

// TestFix41_NoStaleClaimWhenFresh verifies that fresh heartbeats are not
// treated as stale.
func TestFix41_NoStaleClaimWhenFresh(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.yaml")
	now := time.Now().UTC()
	otherAgent := "coder-2"

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
				ID:          "task-active",
				Description: "Active task",
				Status:      models.TaskStatusImplementing,
				AssignedTo:  &otherAgent,
				Priority:    1,
				SpecRef:     "spec.md",
				DoneWhen:    "done",
				Created:     now,
			},
		},
		Agents: map[string]models.Agent{
			"coder-2": {
				Role:      "coder",
				Status:    models.AgentStatusWorking,
				Heartbeat: now, // fresh
			},
		},
		Config: models.Config{
			IntegrationBranch: "main",
			HeartbeatInterval: 60,
		},
	}

	bb := db.New(statePath)
	if err := bb.Write(state); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	fixed := detectAndFixStaleClaim(bb, "coder-1")
	if fixed {
		t.Error("expected detectAndFixStaleClaim to return false (heartbeat is fresh)")
	}
}

// TestFix41_StaleClaimSkipsOwnTasks verifies that the caller's own tasks
// are not treated as stale.
func TestFix41_StaleClaimSkipsOwnTasks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.yaml")
	now := time.Now().UTC()
	staleTime := now.Add(-10 * time.Minute)
	myAgent := "coder-1"

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
				ID:          "my-task",
				Description: "My task",
				Status:      models.TaskStatusImplementing,
				AssignedTo:  &myAgent,
				Priority:    1,
				SpecRef:     "spec.md",
				DoneWhen:    "done",
				Created:     now,
			},
		},
		Agents: map[string]models.Agent{
			"coder-1": {
				Role:      "coder",
				Status:    models.AgentStatusWorking,
				Heartbeat: staleTime, // stale, but it's our own task
			},
		},
		Config: models.Config{
			IntegrationBranch: "main",
			HeartbeatInterval: 60,
		},
	}

	bb := db.New(statePath)
	if err := bb.Write(state); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	fixed := detectAndFixStaleClaim(bb, "coder-1")
	if fixed {
		t.Error("expected detectAndFixStaleClaim to return false (own task)")
	}

	// Task should remain IMPLEMENTING
	readState, _ := bb.Read()
	task := readState.FindTask("my-task")
	if task.Status != models.TaskStatusImplementing {
		t.Errorf("expected task to remain IMPLEMENTING, got %s", task.Status)
	}
}

// TestFix41_StaleClaimUnknownAgent verifies that tasks assigned to an
// agent not in the agents map are treated as stale.
func TestFix41_StaleClaimUnknownAgent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.yaml")
	now := time.Now().UTC()
	ghostAgent := "coder-ghost"

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
				ID:          "orphan-task",
				Description: "Orphan task",
				Status:      models.TaskStatusImplementing,
				AssignedTo:  &ghostAgent,
				Priority:    1,
				SpecRef:     "spec.md",
				DoneWhen:    "done",
				Created:     now,
			},
		},
		Agents: map[string]models.Agent{}, // ghost agent not registered
		Config: models.Config{
			IntegrationBranch: "main",
			HeartbeatInterval: 60,
		},
	}

	bb := db.New(statePath)
	if err := bb.Write(state); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	fixed := detectAndFixStaleClaim(bb, "coder-alive")
	if !fixed {
		t.Fatal("expected stale claim fix for unknown agent")
	}

	readState, _ := bb.Read()
	task := readState.FindTask("orphan-task")
	if task.Status != models.TaskStatusReady {
		t.Errorf("expected READY, got %s", task.Status)
	}

	// Verify the file can be cleaned up (ensure no leftover lock files)
	if _, err := os.Stat(statePath); err != nil {
		t.Errorf("state file missing after test: %v", err)
	}
}
