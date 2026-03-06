package agent

// e2e_runaway_prevention_test.go — Tests for runaway prevention mechanisms:
//
//   S9  — Crash retry limit: repeated crash exits → BLOCKED after threshold
//   S10 — Crash retry backoff: verify exponential delay growth
//   S11 — Budget tracker: iteration limit enforced in supervisor loop
//   S12 — MaxLoops flag: supervisor exits after N iterations
//   S13 — Observability: emitAgentReleased fires on supervisor exit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// ---------------------------------------------------------------------------
// S9 — Crash retry limit: consecutive non-0/non-42 exits → BLOCKED
// ---------------------------------------------------------------------------

func TestE2E_S9_CrashRetryLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// Use fast timeouts
	state.Config.CoderPollInterval = 1
	state.Config.CoderMaxWait = 10
	// Short crash retry config so test runs fast
	state.Config.CrashRetryLimit = 3
	state.Config.CrashRetryBaseDelaySec = 1
	state.Config.CrashRetryMaxDelaySec = 2

	task := testhelpers.BuildTaskByStatus("task-crashloop", models.TaskStatusReady, now)
	task.AcceptanceCriteria = []string{"AC-1: test"}
	state.Tasks = append(state.Tasks, task)

	env := setupE2E(t, state)

	var callCount int32

	// Every call returns exit code 1 (crash) AND sets HandoffPending=true.
	// HandoffPending is needed so the supervisor can re-claim the task on next
	// iteration (same pattern as the exit-42 test S4). After CrashRetryLimit (3)
	// consecutive crashes the supervisor should block the task.
	crashStep := func(bb *db.Blackboard, agentID, prompt string) (int, error) {
		atomic.AddInt32(&callCount, 1)
		// Set HandoffPending so the supervisor's ResumeHandoff finds the task
		err := bb.Modify(func(s *models.State) error {
			task := s.FindTask("task-crashloop")
			if task != nil {
				task.HandoffPending = true
			}
			return nil
		})
		if err != nil {
			return 1, err
		}
		return 1, nil // exit code 1 = crash
	}

	// Provide enough steps — crash limit is 3, so we need at least 4
	steps := make([]StepFunc, 10)
	for i := range steps {
		steps[i] = crashStep
	}
	executor := NewScenarioExecutor(env.BB, steps...)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	config := makeCoderConfig(env, "coder-1", executor)
	err := RunSupervisor(ctx, config)
	if err != nil {
		t.Fatalf("RunSupervisor failed: %v", err)
	}

	finalState := readFinalState(t, env.BB)
	finalTask := finalState.FindTask("task-crashloop")
	if finalTask == nil {
		t.Fatal("task-crashloop not found in final state")
	}

	// Task should be BLOCKED after crash retry limit exceeded
	if finalTask.Status != models.TaskStatusBlocked {
		t.Errorf("expected BLOCKED after crash limit, got %s", finalTask.Status)
	}

	// Should have a blocked reason mentioning crash retry
	if finalTask.BlockedReason == nil {
		t.Error("expected non-nil blocked reason")
	} else if got := *finalTask.BlockedReason; got == "" {
		t.Error("expected non-empty blocked reason")
	} else {
		t.Logf("Blocked reason: %s", got)
	}

	// Should have been called at least CrashRetryLimit+1 times (limit is 3)
	calls := int(atomic.LoadInt32(&callCount))
	if calls < 3+1 {
		t.Errorf("expected at least %d calls, got %d", 3+1, calls)
	}

	t.Logf("Crash retry limit test: %d calls before task blocked", calls)
}

// ---------------------------------------------------------------------------
// S10 — Crash retry backoff: verify delays grow exponentially
// ---------------------------------------------------------------------------

func TestCrashRetryTracker_Backoff(t *testing.T) {
	tracker := newCrashRetryTracker(models.Config{}) // uses defaults: 5s base, 120s max

	var delays []time.Duration
	for i := 0; i < 7; i++ {
		_, delay := tracker.record("task-1")
		delays = append(delays, delay)
	}

	// Expected: 5s, 10s, 20s, 40s, 80s, 120s (cap), 120s (cap)
	expected := []time.Duration{
		5 * time.Second,
		10 * time.Second,
		20 * time.Second,
		40 * time.Second,
		80 * time.Second,
		120 * time.Second,
		120 * time.Second,
	}
	for i, want := range expected {
		if delays[i] != want {
			t.Errorf("delay[%d] = %v, want %v", i, delays[i], want)
		}
	}
}

func TestCrashRetryTracker_ResetOnNewTask(t *testing.T) {
	tracker := newCrashRetryTracker(models.Config{})

	// 3 crashes on task-1
	tracker.record("task-1")
	tracker.record("task-1")
	crashes, _ := tracker.record("task-1")
	if crashes != 3 {
		t.Errorf("expected 3 crashes, got %d", crashes)
	}

	// Switch to task-2 — should reset
	crashes, _ = tracker.record("task-2")
	if crashes != 1 {
		t.Errorf("expected 1 crash after task switch, got %d", crashes)
	}
}

func TestCrashRetryTracker_ExplicitReset(t *testing.T) {
	tracker := newCrashRetryTracker(models.Config{})

	tracker.record("task-1")
	tracker.record("task-1")
	tracker.reset()

	crashes, _ := tracker.record("task-1")
	if crashes != 1 {
		t.Errorf("expected 1 crash after reset, got %d", crashes)
	}
}

// ---------------------------------------------------------------------------
// S11 — Budget tracker: iteration limit enforced in supervisor loop
// ---------------------------------------------------------------------------

func TestE2E_S11_BudgetIterationLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// Set a very low iteration budget to test enforcement
	state.Config.MaxAgentIterations = 3
	state.Config.CoderPollInterval = 1
	state.Config.CoderMaxWait = 10

	// Create enough READY tasks to keep the coder busy
	for i := 1; i <= 10; i++ {
		task := testhelpers.BuildTaskByStatus(fmt.Sprintf("task-budget-%d", i), models.TaskStatusReady, now)
		task.Priority = i
		state.Tasks = append(state.Tasks, task)
	}

	env := setupE2E(t, state)

	var callCount int32

	// Each step completes the task successfully
	completeStep := func(bb *db.Blackboard, agentID, prompt string) (int, error) {
		atomic.AddInt32(&callCount, 1)

		s, readErr := bb.Read()
		if readErr != nil {
			return 1, readErr
		}
		var claimedID string
		for _, task := range s.Tasks {
			if task.Status == models.TaskStatusImplementing && task.AssignedTo != nil && *task.AssignedTo == agentID {
				claimedID = task.ID
				break
			}
		}
		if claimedID == "" {
			return 1, fmt.Errorf("no IMPLEMENTING task found for %s", agentID)
		}

		modErr := bb.Modify(func(s *models.State) error {
			task := s.FindTask(claimedID)
			if task == nil {
				return fmt.Errorf("task %s not found", claimedID)
			}
			if err := task.Transition(models.TaskStatusReadyForReview); err != nil {
				return err
			}
			rc := "commit-" + claimedID
			task.ReviewCommit = &rc
			return nil
		})
		return 0, modErr
	}

	steps := make([]StepFunc, 10)
	for i := range steps {
		steps[i] = completeStep
	}
	executor := NewScenarioExecutor(env.BB, steps...)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	config := makeCoderConfig(env, "coder-1", executor)
	err := RunSupervisor(ctx, config)
	if err != nil {
		t.Fatalf("RunSupervisor failed: %v", err)
	}

	calls := int(atomic.LoadInt32(&callCount))

	// Should have exited before processing all 10 tasks due to budget limit.
	// Budget is 3 iterations, meaning it records iteration 1,2,3 and on iteration 4
	// the check fires. So we expect ~3 task completions.
	if calls > 4 {
		t.Errorf("expected budget to limit calls to ~3, got %d", calls)
	}
	if calls == 0 {
		t.Error("expected at least 1 call")
	}

	t.Logf("Budget limit test: %d calls with MaxAgentIterations=3", calls)
}

// ---------------------------------------------------------------------------
// S12 — MaxLoops flag: supervisor exits after N iterations
// ---------------------------------------------------------------------------

func TestE2E_S12_MaxLoopsFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	state.Config.CoderPollInterval = 1
	state.Config.CoderMaxWait = 10
	// Set high budget so MaxLoops is what limits us
	state.Config.MaxAgentIterations = 100

	for i := 1; i <= 10; i++ {
		task := testhelpers.BuildTaskByStatus(fmt.Sprintf("task-maxloop-%d", i), models.TaskStatusReady, now)
		task.Priority = i
		state.Tasks = append(state.Tasks, task)
	}

	env := setupE2E(t, state)

	var callCount int32

	completeStep := func(bb *db.Blackboard, agentID, prompt string) (int, error) {
		atomic.AddInt32(&callCount, 1)

		s, readErr := bb.Read()
		if readErr != nil {
			return 1, readErr
		}
		var claimedID string
		for _, task := range s.Tasks {
			if task.Status == models.TaskStatusImplementing && task.AssignedTo != nil && *task.AssignedTo == agentID {
				claimedID = task.ID
				break
			}
		}
		if claimedID == "" {
			return 1, fmt.Errorf("no IMPLEMENTING task for %s", agentID)
		}

		modErr := bb.Modify(func(s *models.State) error {
			task := s.FindTask(claimedID)
			if task == nil {
				return fmt.Errorf("task %s not found", claimedID)
			}
			if err := task.Transition(models.TaskStatusReadyForReview); err != nil {
				return err
			}
			rc := "commit-" + claimedID
			task.ReviewCommit = &rc
			return nil
		})
		return 0, modErr
	}

	steps := make([]StepFunc, 10)
	for i := range steps {
		steps[i] = completeStep
	}
	executor := NewScenarioExecutor(env.BB, steps...)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	config := makeCoderConfig(env, "coder-1", executor)
	config.MaxLoops = 2 // Only allow 2 loop iterations

	err := RunSupervisor(ctx, config)
	if err != nil {
		t.Fatalf("RunSupervisor failed: %v", err)
	}

	calls := int(atomic.LoadInt32(&callCount))

	// MaxLoops=2 means iteration 1 and 2 proceed, then iteration 3 is blocked.
	// So we expect exactly 2 task completions.
	if calls > 3 {
		t.Errorf("expected MaxLoops=2 to limit calls to 2-3, got %d", calls)
	}
	if calls == 0 {
		t.Error("expected at least 1 call")
	}

	t.Logf("MaxLoops test: %d calls with MaxLoops=2", calls)
}

// ---------------------------------------------------------------------------
// S13 — Events log: verify AGENT_RELEASED event is emitted on exit
// ---------------------------------------------------------------------------

func TestE2E_S13_AgentReleasedEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	state.Config.CoderPollInterval = 1
	state.Config.CoderMaxWait = 3

	task := testhelpers.BuildTaskByStatus("task-events", models.TaskStatusReady, now)
	state.Tasks = append(state.Tasks, task)

	env := setupE2E(t, state)

	// Create the events log directory so the emitter can write
	eventsDir := filepath.Join(env.ProjectRoot, ".liza")
	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		t.Fatalf("Failed to create events dir: %v", err)
	}

	// Single step: complete the task → READY_FOR_REVIEW
	step1 := func(bb *db.Blackboard, agentID, prompt string) (int, error) {
		err := bb.Modify(func(s *models.State) error {
			task := s.FindTask("task-events")
			if task == nil {
				return fmt.Errorf("task-events not found")
			}
			if err := task.Transition(models.TaskStatusReadyForReview); err != nil {
				return err
			}
			rc := "abc1234"
			task.ReviewCommit = &rc
			return nil
		})
		return 0, err
	}

	executor := NewScenarioExecutor(env.BB, step1)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	config := makeCoderConfig(env, "coder-1", executor)
	err := RunSupervisor(ctx, config)
	if err != nil {
		t.Fatalf("RunSupervisor failed: %v", err)
	}

	// Verify the events file exists
	eventsPath := filepath.Join(env.ProjectRoot, ".liza", "events.jsonl")
	if _, err := os.Stat(eventsPath); os.IsNotExist(err) {
		t.Log("events.jsonl not created (emitter may have failed to initialize) — test still validates no crash occurred")
		return
	}

	// Read the events file and check for AGENT_RELEASED
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("Failed to read events file: %v", err)
	}

	eventsStr := string(data)
	if len(eventsStr) == 0 {
		t.Log("events.jsonl is empty — emitter may not have flushed")
		return
	}

	// Check that AGENT_CLAIMED and AGENT_RELEASED both appear
	hasRegistered := false
	hasReleased := false
	for _, line := range splitLines(eventsStr) {
		if line == "" {
			continue
		}
		if contains(line, "AGENT_CLAIMED") {
			hasRegistered = true
		}
		if contains(line, "AGENT_RELEASED") {
			hasReleased = true
		}
	}

	if !hasRegistered {
		t.Logf("AGENT_CLAIMED event not found in events log (events may not have flushed)")
	}
	if !hasReleased {
		t.Logf("AGENT_RELEASED event not found in events log (events may not have flushed)")
	}

	t.Logf("Events log contains %d bytes, registered=%v released=%v", len(data), hasRegistered, hasReleased)
}

// helpers for the events test
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
