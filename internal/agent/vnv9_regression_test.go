package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
)

// Regression tests for V&V Run 9 fixes.

// --- Fix 43: blockTaskOnNoSubmit → NEEDS_HUMAN_DECISION (not BLOCKED) ---

// TestFix43_NoSubmitUsesNeedsHumanDecision verifies that blockTaskOnNoSubmit
// transitions to NEEDS_HUMAN_DECISION instead of BLOCKED, preventing the
// planner from waking on BLOCKED_TASKS trigger and creating useless meta-tasks.
func TestFix43_NoSubmitUsesNeedsHumanDecision(t *testing.T) {
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
				Description: "Task stuck in task_complete loop",
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
		t.Fatal("task-loop not found")
	}

	// Must be NEEDS_HUMAN_DECISION (Fix 43), NOT BLOCKED
	if task.Status != models.TaskStatusNeedsHumanDecision {
		t.Errorf("expected NEEDS_HUMAN_DECISION, got %s", task.Status)
	}

	// Must NOT be BLOCKED — this is the key regression assertion
	if task.Status == models.TaskStatusBlocked {
		t.Error("task is BLOCKED — Fix 43 regression: should be NEEDS_HUMAN_DECISION to avoid planner wake cascade")
	}

	// Reason should still be set
	if task.BlockedReason == nil || *task.BlockedReason == "" {
		t.Error("expected BlockedReason to be set with task_complete loop explanation")
	}

	// No blocked questions (Fix 43 removed them)
	if len(task.BlockedQuestions) > 0 {
		t.Errorf("expected no BlockedQuestions (Fix 43), got %d", len(task.BlockedQuestions))
	}

	// AssignedTo should be cleared
	if task.AssignedTo != nil {
		t.Errorf("expected AssignedTo nil, got %v", *task.AssignedTo)
	}

	// History should record "needs_human_decision" event
	found := false
	for _, h := range task.History {
		if h.Event == "needs_human_decision" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected history entry with event 'needs_human_decision'")
	}
}

// TestFix43_NeedsHumanDecisionDoesNotTriggerPlannerWake verifies that
// NEEDS_HUMAN_DECISION status is NOT counted by the BLOCKED_TASKS wake trigger.
func TestFix43_NeedsHumanDecisionDoesNotTriggerPlannerWake(t *testing.T) {
	t.Parallel()

	state := &models.State{
		Tasks: []models.Task{
			{
				ID:     "task-nhd",
				Status: models.TaskStatusNeedsHumanDecision,
			},
		},
	}

	triggers := DetectPlannerWakeTriggers(state)
	if triggers.Trigger == WakeTriggerBlocked {
		t.Error("NEEDS_HUMAN_DECISION should NOT trigger BLOCKED_TASKS wake — Fix 43 regression")
	}
}

// TestFix43_BlockedStillTriggersWake is a sanity check that real BLOCKED tasks
// still trigger the planner (we only changed the no-submit path, not all blocking).
func TestFix43_BlockedStillTriggersWake(t *testing.T) {
	t.Parallel()

	state := &models.State{
		Tasks: []models.Task{
			{
				ID:     "task-blocked",
				Status: models.TaskStatusBlocked,
			},
		},
	}

	triggers := DetectPlannerWakeTriggers(state)
	if triggers.Trigger != WakeTriggerBlocked {
		t.Errorf("expected BLOCKED_TASKS trigger for genuinely BLOCKED task, got %s", triggers.Trigger)
	}
}

// TestFix43_ImplementingTaskTransitionsToNHD verifies that an IMPLEMENTING task
// (not just READY) also transitions to NEEDS_HUMAN_DECISION.
func TestFix43_ImplementingTaskTransitionsToNHD(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.yaml")
	now := time.Now().UTC()
	agent := "coder-1"

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
				ID:          "task-impl",
				Description: "Task currently implementing",
				Status:      models.TaskStatusImplementing,
				AssignedTo:  &agent,
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

	blockTaskOnNoSubmit(bb, "task-impl", "coder-1", 3, 3)

	readState, _ := bb.Read()
	task := readState.FindTask("task-impl")
	if task.Status != models.TaskStatusNeedsHumanDecision {
		t.Errorf("expected NEEDS_HUMAN_DECISION for IMPLEMENTING task, got %s", task.Status)
	}
}

// --- Fix 47: MCP monitor fallback deadline ---

// TestFix47_MonitorFallbackWhenFileNeverAppears verifies that the MCP inactivity
// monitor cancels the session even when the activity file never appears.
// Bug: If os.WriteFile for the initial activity file fails (path issue, permissions,
// OneDrive sync lock), the monitor's "file missing → skip" logic loops forever.
// Fix: Added wall-clock fallback deadline of 2× timeout.
func TestFix47_MonitorFallbackWhenFileNeverAppears(t *testing.T) {
	t.Parallel()

	// Use a non-existent path so the file never appears
	activityPath := filepath.Join(t.TempDir(), "nonexistent", "mcp-activity")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use a tiny timeout so the test completes quickly
	// Fallback = 2 * 100ms = 200ms
	timeout := 100 * time.Millisecond

	done := make(chan struct{})
	go func() {
		monitorMCPActivity(ctx, cancel, activityPath, timeout, 50*time.Millisecond, "test-agent")
		close(done)
	}()

	select {
	case <-done:
		// Monitor exited — verify context was cancelled
		if ctx.Err() == nil {
			t.Error("Monitor exited but context was not cancelled")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Monitor did not trigger fallback deadline within 5s")
	}
}

// TestFix47_MonitorStillDetectsStaleFile verifies normal inactivity detection
// still works alongside the fallback mechanism.
func TestFix47_MonitorStillDetectsStaleFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	activityPath := filepath.Join(dir, "mcp-activity")

	// Write a stale timestamp (2 minutes ago)
	staleTime := time.Now().UTC().Add(-2 * time.Minute)
	os.WriteFile(activityPath, []byte(staleTime.Format(time.RFC3339)), 0644)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Timeout = 100ms — the file was written 2 min ago, so it's stale
	timeout := 100 * time.Millisecond

	done := make(chan struct{})
	go func() {
		monitorMCPActivity(ctx, cancel, activityPath, timeout, 50*time.Millisecond, "test-agent")
		close(done)
	}()

	select {
	case <-done:
		if ctx.Err() == nil {
			t.Error("Monitor detected stale file but didn't cancel context")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Monitor did not detect stale activity file")
	}
}

// TestFix47_MonitorDoesNotCancelWhileActive verifies the monitor does NOT
// cancel when the activity file is being regularly updated.
func TestFix47_MonitorDoesNotCancelWhileActive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	activityPath := filepath.Join(dir, "mcp-activity")

	// Write a fresh timestamp
	os.WriteFile(activityPath, []byte(time.Now().UTC().Format(time.RFC3339)), 0644)

	ctx, cancel := context.WithCancel(context.Background())

	// Timeout = 10 seconds — file is fresh, should not cancel
	timeout := 10 * time.Second

	go monitorMCPActivity(ctx, cancel, activityPath, timeout, 50*time.Millisecond, "test-agent")

	// Wait briefly, then verify context is still active
	time.Sleep(200 * time.Millisecond)
	if ctx.Err() != nil {
		t.Error("Monitor cancelled context despite fresh activity file")
	}

	// Clean up
	cancel()
}
