package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// TestWaitForCoderWork tests coder work detection
func TestWaitForCoderWork(t *testing.T) {
	tests := []struct {
		name     string
		tasks    []models.Task
		wantWork bool
	}{
		{
			name: "claimable task available",
			tasks: []models.Task{
				testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReady, time.Now().UTC()),
			},
			wantWork: true,
		},
		{
			name: "rejected task available",
			tasks: []models.Task{
				testhelpers.BuildTaskByStatus("task-1", models.TaskStatusRejected, time.Now().UTC()),
			},
			wantWork: true,
		},
		{
			name: "integration failed task available",
			tasks: []models.Task{
				testhelpers.BuildTaskByStatus("task-1", models.TaskStatusIntegrationFailed, time.Now().UTC()),
			},
			wantWork: true,
		},
		{
			name: "no claimable tasks",
			tasks: []models.Task{
				testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, time.Now().UTC()),
			},
			wantWork: false,
		},
		{
			name: "task waiting on dependency",
			tasks: []models.Task{
				func() models.Task {
					task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReady, time.Now().UTC())
					task.DependsOn = []string{"task-2"}
					return task
				}(),
				testhelpers.BuildTaskByStatus("task-2", models.TaskStatusImplementing, time.Now().UTC()),
			},
			wantWork: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

			state := testhelpers.CreateValidState()
			state.Tasks = tt.tasks

			testhelpers.WriteInitialState(t, statePath, state)

			config := SupervisorConfig{
				StatePath:   statePath,
				ProjectRoot: tmpDir,
			}

			bb := db.New(statePath)
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			strategy, _ := NewRoleStrategy("coder", testResolver(t))
			hasWork, err := strategy.WaitForWork(ctx, bb, config, 10*time.Millisecond, 100*time.Millisecond)

			if err != nil {
				t.Fatalf("WaitForWork() error = %v", err)
			}

			if hasWork != tt.wantWork {
				t.Errorf("WaitForWork() = %v, want %v", hasWork, tt.wantWork)
			}
		})
	}
}

// TestWaitForReviewerWork tests reviewer work detection
func TestWaitForReviewerWork(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name        string
		tasks       []models.Task
		wantWork    bool
		wantCleared bool
	}{
		{
			name: "reviewable task available",
			tasks: []models.Task{
				testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReadyForReview, now),
			},
			wantWork: true,
		},
		{
			name: "task with expired review lease",
			tasks: func() []models.Task {
				task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now)
				task.ReviewingBy = testhelpers.StringPtr("code-reviewer-1")
				task.ReviewLeaseExpires = testhelpers.TimePtr(now.Add(-10 * time.Minute))
				return []models.Task{task}
			}(),
			wantWork:    true,
			wantCleared: true,
		},
		{
			name: "no reviewable tasks",
			tasks: []models.Task{
				testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now),
			},
			wantWork: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
			testhelpers.SetupPipelineConfig(t, tmpDir)
			projectRoot := tmpDir

			state := testhelpers.CreateValidState()
			state.Tasks = tt.tasks

			testhelpers.WriteInitialState(t, statePath, state)

			config := SupervisorConfig{
				StatePath:   statePath,
				ProjectRoot: projectRoot,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			strategy, _ := NewRoleStrategy("code-reviewer", testResolver(t))
			hasWork, err := strategy.WaitForWork(ctx, db.New(statePath), config, 10*time.Millisecond, 100*time.Millisecond)

			if err != nil {
				t.Fatalf("WaitForWork() error = %v", err)
			}

			if hasWork != tt.wantWork {
				t.Errorf("WaitForWork() = %v, want %v", hasWork, tt.wantWork)
			}

			if tt.wantCleared {
				updatedState, err := db.New(statePath).Read()
				if err != nil {
					t.Fatalf("failed to read updated state: %v", err)
				}

				task := updatedState.FindTask("task-1")
				if task == nil {
					t.Fatal("expected task-1 to exist")
				}
				if task.Status != models.TaskStatusReadyForReview {
					t.Errorf("task status = %s, want %s", task.Status, models.TaskStatusReadyForReview)
				}
				if task.ReviewingBy != nil {
					t.Errorf("reviewing_by = %v, want nil", *task.ReviewingBy)
				}
				if task.ReviewLeaseExpires != nil {
					t.Errorf("review_lease_expires = %v, want nil", *task.ReviewLeaseExpires)
				}
			}
		})
	}
}

func TestWaitForCodePlannerWork(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name     string
		tasks    []models.Task
		wantWork bool
	}{
		{
			name: "draft coding plan task available",
			tasks: []models.Task{
				testhelpers.BuildTaskByStatus("task-1", models.TaskStatusDraftCodingPlan, now),
			},
			wantWork: true,
		},
		{
			name: "no code-planner claimable tasks",
			tasks: []models.Task{
				testhelpers.BuildTaskByStatus("task-1", models.TaskStatusCodePlanning, now),
			},
			wantWork: false,
		},
		{
			name: "resumable handoff wakes code-planner",
			tasks: func() []models.Task {
				agentID := "code-planner-1"
				task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusCodePlanning, now)
				task.HandoffPending = true
				task.AssignedTo = &agentID
				return []models.Task{task}
			}(),
			wantWork: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

			state := testhelpers.CreateValidState()
			state.Tasks = tt.tasks

			testhelpers.WriteInitialState(t, statePath, state)

			config := SupervisorConfig{StatePath: statePath, AgentID: "code-planner-1", ProjectRoot: tmpDir}
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			strategy, _ := NewRoleStrategy("code-planner", testResolver(t))
			hasWork, err := strategy.WaitForWork(ctx, db.New(statePath), config, 10*time.Millisecond, 100*time.Millisecond)
			if err != nil {
				t.Fatalf("WaitForWork() error = %v", err)
			}
			if hasWork != tt.wantWork {
				t.Errorf("WaitForWork() = %v, want %v", hasWork, tt.wantWork)
			}
		})
	}
}

func TestWaitForCodePlanReviewerWork(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name     string
		tasks    []models.Task
		wantWork bool
	}{
		{
			name: "coding plan to review task available",
			tasks: func() []models.Task {
				t := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusCodingPlanToReview, now)
				rc := "review123"
				t.ReviewCommit = &rc
				return []models.Task{t}
			}(),
			wantWork: true,
		},
		{
			name: "no code-plan-reviewer claimable tasks",
			tasks: []models.Task{
				testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewingCodingPlan, now),
			},
			wantWork: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
			testhelpers.SetupPipelineConfig(t, tmpDir)
			projectRoot := tmpDir

			state := testhelpers.CreateValidState()
			state.Tasks = tt.tasks

			testhelpers.WriteInitialState(t, statePath, state)

			config := SupervisorConfig{StatePath: statePath, ProjectRoot: projectRoot}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			strategy, _ := NewRoleStrategy("code-plan-reviewer", testResolver(t))
			hasWork, err := strategy.WaitForWork(ctx, db.New(statePath), config, 10*time.Millisecond, 100*time.Millisecond)
			if err != nil {
				t.Fatalf("WaitForWork() error = %v", err)
			}
			if hasWork != tt.wantWork {
				t.Errorf("WaitForWork() = %v, want %v", hasWork, tt.wantWork)
			}
		})
	}
}

// TestWaitForOrchestratorWork tests orchestrator work detection
func TestWaitForOrchestratorWork(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name     string
		tasks    []models.Task
		wantWork bool
	}{
		{
			name:     "initial planning - no tasks",
			tasks:    []models.Task{},
			wantWork: true,
		},
		{
			name: "blocked tasks",
			tasks: []models.Task{
				testhelpers.BuildTaskByStatus("task-1", models.TaskStatusBlocked, now),
			},
			wantWork: true,
		},
		{
			name: "no orchestrator work needed",
			tasks: []models.Task{
				testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now),
			},
			wantWork: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

			state := testhelpers.CreateValidState()
			state.Tasks = tt.tasks

			testhelpers.WriteInitialState(t, statePath, state)

			config := SupervisorConfig{
				StatePath:   statePath,
				ProjectRoot: tmpDir,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			// Orchestrator now respects maxWait parameter (uses 100ms timeout for test)
			strategy, _ := NewRoleStrategy("orchestrator", testResolver(t))
			hasWork, err := strategy.WaitForWork(ctx, db.New(statePath), config, 10*time.Millisecond, 100*time.Millisecond)

			if err != nil {
				t.Fatalf("WaitForWork() error = %v", err)
			}

			if hasWork != tt.wantWork {
				t.Errorf("WaitForWork() = %v, want %v", hasWork, tt.wantWork)
			}
		})
	}
}

// TestOrchestratorRespectsMaxWaitConfig verifies that orchestrator wait loop respects
// the configured max_wait value (does not override with hardcoded value)
func TestOrchestratorRespectsMaxWaitConfig(t *testing.T) {
	tmpDir := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

	// Create state with no work available
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{
		testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, time.Now().UTC()),
	}
	testhelpers.WriteInitialState(t, statePath, state)

	config := SupervisorConfig{
		StatePath:   statePath,
		ProjectRoot: tmpDir,
	}

	// Test with a short maxWait - orchestrator should timeout when maxWait is reached
	ctx := context.Background()
	startTime := time.Now()
	strategy, _ := NewRoleStrategy("orchestrator", testResolver(t))
	hasWork, err := strategy.WaitForWork(ctx, db.New(statePath), config, 10*time.Millisecond, 150*time.Millisecond)
	elapsed := time.Since(startTime)

	if err != nil {
		t.Fatalf("WaitForWork() error = %v", err)
	}

	if hasWork {
		t.Error("Expected no work after timeout")
	}

	// Should wait approximately the maxWait duration (150ms), not a year
	// Allow some tolerance for test execution variability
	if elapsed < 150*time.Millisecond || elapsed > 300*time.Millisecond {
		t.Errorf("Expected timeout around 150ms, got %v", elapsed)
	}
}

// TestWaitForWorkEventDriven tests that agents wake quickly on state changes
func TestWaitForWorkEventDriven(t *testing.T) {
	tests := []struct {
		name        string
		role        string
		setupState  func() *models.State
		modifyState func(*models.State)
		wantWork    bool
	}{
		{
			name: "coder wakes on new claimable task",
			role: "coder",
			setupState: func() *models.State {
				state := testhelpers.CreateValidState()
				state.Tasks = []models.Task{
					testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, time.Now().UTC()),
				}
				return state
			},
			modifyState: func(s *models.State) {
				s.Tasks = append(s.Tasks, testhelpers.BuildTaskByStatus("task-2", models.TaskStatusReady, time.Now().UTC()))
			},
			wantWork: true,
		},
		{
			name: "reviewer wakes on reviewable task",
			role: "code-reviewer",
			setupState: func() *models.State {
				state := testhelpers.CreateValidState()
				state.Tasks = []models.Task{}
				return state
			},
			modifyState: func(s *models.State) {
				s.Tasks = append(s.Tasks, testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReadyForReview, time.Now().UTC()))
			},
			wantWork: true,
		},
		{
			name: "orchestrator wakes on wake trigger",
			role: "orchestrator",
			setupState: func() *models.State {
				state := testhelpers.CreateValidState()
				state.Tasks = []models.Task{}
				return state
			},
			modifyState: func(s *models.State) {
				// Add 5 unclaimed tasks to trigger orchestrator wake
				for i := 1; i <= 5; i++ {
					taskID := "task-" + string(rune('0'+i))
					s.Tasks = append(s.Tasks, testhelpers.BuildTaskByStatus(taskID, models.TaskStatusReady, time.Now().UTC()))
				}
			},
			wantWork: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

			// Write initial state with no work
			state := tt.setupState()
			testhelpers.WriteInitialState(t, statePath, state)

			config := SupervisorConfig{
				StatePath:   statePath,
				ProjectRoot: tmpDir,
			}

			bb := db.New(statePath)

			// Start waiting in goroutine
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			startTime := time.Now()
			resultCh := make(chan bool, 1)
			errCh := make(chan error, 1)

			strategy, _ := NewRoleStrategy(tt.role, testResolver(t))
			go func() {
				hasWork, err := strategy.WaitForWork(ctx, bb, config, 10*time.Millisecond, 5*time.Second)
				if err != nil {
					errCh <- err
					return
				}
				resultCh <- hasWork
			}()

			// Wait a bit for watcher to start
			time.Sleep(200 * time.Millisecond)

			// Modify state to create work
			if err := bb.Modify(func(s *models.State) error {
				tt.modifyState(s)
				return nil
			}); err != nil {
				t.Fatalf("Failed to modify state: %v", err)
			}

			// Wait for result
			select {
			case err := <-errCh:
				t.Fatalf("WaitForWork() error = %v", err)
			case hasWork := <-resultCh:
				elapsed := time.Since(startTime)

				if hasWork != tt.wantWork {
					t.Errorf("WaitForWork() = %v, want %v", hasWork, tt.wantWork)
				}

				// Verify wake time is quick (under 2s including setup)
				if hasWork && elapsed > 2*time.Second {
					t.Errorf("Agent took %v to wake, expected < 2s", elapsed)
				}
			case <-time.After(6 * time.Second):
				t.Fatal("Timeout waiting for WaitForWork result")
			}
		})
	}
}

// TestWaitForWorkCancellation tests context cancellation during wait
func TestWaitForWorkCancellation(t *testing.T) {
	tmpDir := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

	// Create state with no work
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{}
	testhelpers.WriteInitialState(t, statePath, state)

	config := SupervisorConfig{
		StatePath:   statePath,
		ProjectRoot: tmpDir,
	}

	bb := db.New(statePath)
	ctx, cancel := context.WithCancel(context.Background())

	// Start waiting in goroutine
	errCh := make(chan error, 1)
	started := make(chan struct{})
	strategy, _ := NewRoleStrategy("coder", testResolver(t))
	go func() {
		close(started)
		_, err := strategy.WaitForWork(ctx, bb, config, 10*time.Millisecond, 10*time.Second)
		errCh <- err
	}()

	<-started
	cancel()

	// Should return context.Canceled error quickly
	select {
	case err := <-errCh:
		if err != context.Canceled {
			t.Errorf("Expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for cancellation")
	}
}

// TestWaitForWorkTimeout tests deadline expiration
func TestWaitForWorkTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

	// Create state with no work
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{}
	testhelpers.WriteInitialState(t, statePath, state)

	config := SupervisorConfig{
		StatePath:   statePath,
		ProjectRoot: tmpDir,
	}

	bb := db.New(statePath)
	ctx := context.Background()

	startTime := time.Now()
	strategy, _ := NewRoleStrategy("coder", testResolver(t))
	hasWork, err := strategy.WaitForWork(ctx, bb, config, 10*time.Millisecond, 200*time.Millisecond)
	elapsed := time.Since(startTime)

	if err != nil {
		t.Fatalf("WaitForWork() error = %v", err)
	}

	if hasWork {
		t.Error("Expected no work after timeout")
	}

	// Should wait approximately the maxWait duration
	if elapsed < 200*time.Millisecond || elapsed > 300*time.Millisecond {
		t.Errorf("Expected timeout around 200ms, got %v", elapsed)
	}
}

// TestWaitForWorkEventDrivenAbortStateMode tests ABORT detection via state mode in event-driven wait
func TestWaitForWorkEventDrivenAbortStateMode(t *testing.T) {
	tmpDir := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{} // No work available
	testhelpers.WriteInitialState(t, statePath, state)

	bb := db.New(statePath)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	startTime := time.Now()
	resultCh := make(chan bool, 1)
	errCh := make(chan error, 1)

	// Start waiting in background
	started := make(chan struct{})
	strategy, _ := NewRoleStrategy("coder", testResolver(t))
	config := SupervisorConfig{StatePath: statePath, ProjectRoot: tmpDir, AgentID: "coder-1"}
	go func() {
		close(started)
		hasWork, err := strategy.WaitForWork(ctx, bb, config, 10*time.Millisecond, 5*time.Second)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- hasWork
	}()

	<-started

	// Brief pause to let WaitForWork goroutine complete its initial state read
	// before we modify the file. On Windows, concurrent file access causes
	// "Access is denied" or "being used by another process" errors.
	time.Sleep(50 * time.Millisecond)

	// Set state to STOPPED
	if err := bb.Modify(func(s *models.State) error {
		s.Config.Mode = models.SystemModeStopped
		return nil
	}); err != nil {
		t.Fatalf("Failed to set STOPPED mode: %v", err)
	}

	// Should return quickly
	select {
	case err := <-errCh:
		t.Fatalf("WaitForWork() error = %v", err)
	case hasWork := <-resultCh:
		elapsed := time.Since(startTime)

		if hasWork {
			t.Error("WaitForWork() should return false when ABORT detected")
		}

		// Should respond well before the 5s context deadline.
		// fsnotify detects instantly; ticker fallback takes up to 1s.
		if elapsed > 1500*time.Millisecond {
			t.Errorf("ABORT detection took %v, expected < 1.5s", elapsed)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("Timeout waiting for ABORT response")
	}
}

// TestWaitForWorkPollingAbortStateMode tests ABORT detection via state mode in polling wait
func TestWaitForWorkPollingAbortStateMode(t *testing.T) {
	tmpDir := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{} // No work available
	testhelpers.WriteInitialState(t, statePath, state)

	bb := db.New(statePath)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	startTime := time.Now()
	resultCh := make(chan bool, 1)
	errCh := make(chan error, 1)

	// Use polling wait with 50ms poll interval
	checkWork := func(s *models.State) (bool, string) {
		return models.CountClaimableTasks(s, models.RoleCoder, nil) > 0, ""
	}

	// Start waiting in background
	started := make(chan struct{})
	go func() {
		close(started)
		hasWork, err := waitForWorkPolling(ctx, bb, tmpDir, 50*time.Millisecond, 5*time.Second, checkWork)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- hasWork
	}()

	<-started

	if err := bb.Modify(func(s *models.State) error {
		s.Config.Mode = models.SystemModeStopped
		return nil
	}); err != nil {
		t.Fatalf("Failed to set STOPPED mode: %v", err)
	}

	// Should detect on next poll (within 100ms)
	select {
	case err := <-errCh:
		t.Fatalf("waitForWorkPolling() error = %v", err)
	case hasWork := <-resultCh:
		elapsed := time.Since(startTime)

		if hasWork {
			t.Error("waitForWorkPolling() should return false when ABORT detected")
		}

		// Should respond within 500ms (wide margin for slow CI + race detector)
		if elapsed > 500*time.Millisecond {
			t.Errorf("ABORT detection took %v, expected < 500ms", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for ABORT response")
	}
}

// TestAbortPrecedenceOverWork tests that ABORT takes precedence over work
func TestAbortPrecedenceOverWork(t *testing.T) {
	tmpDir := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	state.Config.Mode = models.SystemModeStopped // STOPPED mode
	state.Tasks = []models.Task{
		testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReady, now), // Work available
	}
	testhelpers.WriteInitialState(t, statePath, state)

	bb := db.New(statePath)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Should return false (ABORT), not true (work available)
	strategy, _ := NewRoleStrategy("coder", testResolver(t))
	config := SupervisorConfig{StatePath: statePath, ProjectRoot: tmpDir, AgentID: "coder-1"}
	hasWork, err := strategy.WaitForWork(ctx, bb, config, 10*time.Millisecond, 100*time.Millisecond)

	if err != nil {
		t.Fatalf("WaitForWork() error = %v", err)
	}

	if hasWork {
		t.Error("WaitForWork() should return false when ABORT present, even with work available")
	}
}

// writePhase2PipelineConfig writes the full Phase 2 pipeline.yaml into tmpDir/.liza/
// so that LoadResolverForModels can find it.
func writePhase2PipelineConfig(t *testing.T, tmpDir string) {
	t.Helper()
	pipelineYAML := `pipeline:
  roles:
    epic-planner:
      type: doer
      display-name: "Epic Planner"
    epic-plan-reviewer:
      type: reviewer
      display-name: "Epic Plan Reviewer"
    us-writer:
      type: doer
      display-name: "US Writer"
    us-reviewer:
      type: reviewer
      display-name: "US Reviewer"
    code-planner:
      type: doer
      display-name: "Code Planner"
    code-plan-reviewer:
      type: reviewer
      display-name: "Code Plan Reviewer"
    coder:
      type: doer
      display-name: "Coder"
    code-reviewer:
      type: reviewer
      display-name: "Code Reviewer"
  role-pairs:
    epic-planning-pair:
      doer: epic-planner
      reviewer: epic-plan-reviewer
      states:
        initial: DRAFT_EPIC_PLAN
        executing: EPIC_PLANNING
        submitted: EPIC_PLAN_TO_REVIEW
        reviewing: REVIEWING_EPIC_PLAN
        approved: EPIC_PLAN_APPROVED
        rejected: EPIC_PLAN_REJECTED
    us-writing-pair:
      doer: us-writer
      reviewer: us-reviewer
      states:
        initial: DRAFT_US
        executing: WRITING_US
        submitted: US_READY_FOR_REVIEW
        reviewing: REVIEWING_US
        approved: US_APPROVED
        rejected: US_REJECTED
    code-planning-pair:
      doer: code-planner
      reviewer: code-plan-reviewer
      states:
        initial: DRAFT_CODING_PLAN
        executing: CODE_PLANNING
        submitted: CODING_PLAN_TO_REVIEW
        reviewing: REVIEWING_CODING_PLAN
        approved: CODING_PLAN_APPROVED
        rejected: CODING_PLAN_REJECTED
    coding-pair:
      doer: coder
      reviewer: code-reviewer
      states:
        initial: DRAFT_CODE
        executing: IMPLEMENTING_CODE
        submitted: CODE_READY_FOR_REVIEW
        reviewing: REVIEWING_CODE
        approved: CODE_APPROVED
        rejected: CODE_REJECTED
  sub-pipelines:
    epic-spec-subpipeline:
      steps:
        - epic-planning-pair
        - us-writing-pair
      transitions:
        - name: epic-to-us
          from: epic-planning-pair.approved
          to: us-writing-pair.initial
          trigger: manual
          cardinality: per-subtask
    coding-subpipeline:
      steps:
        - code-planning-pair
        - coding-pair
      transitions:
        - name: code-plan-to-coding
          from: code-planning-pair.approved
          to: coding-pair.initial
          trigger: manual
          cardinality: per-subtask
  pipeline-transitions:
    - name: us-to-coding
      from: epic-spec-subpipeline.us-writing-pair.approved
      to: coding-subpipeline.code-planning-pair.initial
      trigger: manual
      cardinality: one-to-one
  entry-points:
    general-objective: epic-spec-subpipeline.epic-planning-pair
    detailed-spec: coding-subpipeline.code-planning-pair
`
	pipelinePath := filepath.Join(tmpDir, ".liza", "pipeline.yaml")
	if err := os.WriteFile(pipelinePath, []byte(pipelineYAML), 0644); err != nil {
		t.Fatalf("Failed to write pipeline.yaml: %v", err)
	}
}

func TestWaitForEpicPlannerWork(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name     string
		tasks    []models.Task
		wantWork bool
	}{
		{
			name: "draft epic plan task available",
			tasks: []models.Task{
				{
					ID:       "task-1",
					Status:   "DRAFT_EPIC_PLAN",
					RolePair: "epic-planning-pair",
					Priority: 1,
					Created:  now,
					SpecRef:  "README.md",
					DoneWhen: "Done",
					Scope:    "Test",
					History:  []models.TaskHistoryEntry{},
				},
			},
			wantWork: true,
		},
		{
			name: "rejected epic plan task available",
			tasks: []models.Task{
				{
					ID:       "task-1",
					Status:   "EPIC_PLAN_REJECTED",
					RolePair: "epic-planning-pair",
					Priority: 1,
					Created:  now,
					SpecRef:  "README.md",
					DoneWhen: "Done",
					Scope:    "Test",
					History:  []models.TaskHistoryEntry{},
				},
			},
			wantWork: true,
		},
		{
			name: "no epic-planner claimable tasks",
			tasks: []models.Task{
				{
					ID:       "task-1",
					Status:   "EPIC_PLANNING",
					RolePair: "epic-planning-pair",
					Priority: 1,
					Created:  now,
					SpecRef:  "README.md",
					DoneWhen: "Done",
					Scope:    "Test",
					History:  []models.TaskHistoryEntry{},
				},
			},
			wantWork: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
			testhelpers.SetupPipelineConfig(t, tmpDir)
			writePhase2PipelineConfig(t, tmpDir)
			projectRoot := tmpDir

			state := testhelpers.CreateValidState()
			state.Tasks = tt.tasks
			testhelpers.WriteInitialState(t, statePath, state)

			config := SupervisorConfig{StatePath: statePath, AgentID: "epic-planner-1", ProjectRoot: projectRoot}
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			strategy, _ := NewRoleStrategy("epic-planner", testResolver(t))
			hasWork, err := strategy.WaitForWork(ctx, db.New(statePath), config, 10*time.Millisecond, 100*time.Millisecond)
			if err != nil {
				t.Fatalf("WaitForWork() error = %v", err)
			}
			if hasWork != tt.wantWork {
				t.Errorf("WaitForWork() = %v, want %v", hasWork, tt.wantWork)
			}
		})
	}
}

func TestWaitForEpicPlanReviewerWork(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name     string
		tasks    []models.Task
		wantWork bool
	}{
		{
			name: "epic plan to review task available",
			tasks: func() []models.Task {
				rc := "review123"
				return []models.Task{
					{
						ID:           "task-1",
						Status:       "EPIC_PLAN_TO_REVIEW",
						RolePair:     "epic-planning-pair",
						Priority:     1,
						Created:      now,
						SpecRef:      "README.md",
						DoneWhen:     "Done",
						Scope:        "Test",
						History:      []models.TaskHistoryEntry{},
						ReviewCommit: &rc,
					},
				}
			}(),
			wantWork: true,
		},
		{
			name: "no epic-plan-reviewer claimable tasks",
			tasks: []models.Task{
				{
					ID:       "task-1",
					Status:   "REVIEWING_EPIC_PLAN",
					RolePair: "epic-planning-pair",
					Priority: 1,
					Created:  now,
					SpecRef:  "README.md",
					DoneWhen: "Done",
					Scope:    "Test",
					History:  []models.TaskHistoryEntry{},
				},
			},
			wantWork: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
			testhelpers.SetupPipelineConfig(t, tmpDir)
			writePhase2PipelineConfig(t, tmpDir)
			projectRoot := tmpDir

			state := testhelpers.CreateValidState()
			state.Tasks = tt.tasks
			testhelpers.WriteInitialState(t, statePath, state)

			config := SupervisorConfig{StatePath: statePath, ProjectRoot: projectRoot}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			strategy, _ := NewRoleStrategy("epic-plan-reviewer", testResolver(t))
			hasWork, err := strategy.WaitForWork(ctx, db.New(statePath), config, 10*time.Millisecond, 100*time.Millisecond)
			if err != nil {
				t.Fatalf("WaitForWork() error = %v", err)
			}
			if hasWork != tt.wantWork {
				t.Errorf("WaitForWork() = %v, want %v", hasWork, tt.wantWork)
			}
		})
	}
}

func TestWaitForUSWriterWork(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name     string
		tasks    []models.Task
		wantWork bool
	}{
		{
			name: "draft US task available",
			tasks: []models.Task{
				{
					ID:       "task-1",
					Status:   "DRAFT_US",
					RolePair: "us-writing-pair",
					Priority: 1,
					Created:  now,
					SpecRef:  "README.md",
					DoneWhen: "Done",
					Scope:    "Test",
					History:  []models.TaskHistoryEntry{},
				},
			},
			wantWork: true,
		},
		{
			name: "no us-writer claimable tasks",
			tasks: []models.Task{
				{
					ID:       "task-1",
					Status:   "WRITING_US",
					RolePair: "us-writing-pair",
					Priority: 1,
					Created:  now,
					SpecRef:  "README.md",
					DoneWhen: "Done",
					Scope:    "Test",
					History:  []models.TaskHistoryEntry{},
				},
			},
			wantWork: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
			testhelpers.SetupPipelineConfig(t, tmpDir)
			writePhase2PipelineConfig(t, tmpDir)
			projectRoot := tmpDir

			state := testhelpers.CreateValidState()
			state.Tasks = tt.tasks
			testhelpers.WriteInitialState(t, statePath, state)

			config := SupervisorConfig{StatePath: statePath, AgentID: "us-writer-1", ProjectRoot: projectRoot}
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			strategy, _ := NewRoleStrategy("us-writer", testResolver(t))
			hasWork, err := strategy.WaitForWork(ctx, db.New(statePath), config, 10*time.Millisecond, 100*time.Millisecond)
			if err != nil {
				t.Fatalf("WaitForWork() error = %v", err)
			}
			if hasWork != tt.wantWork {
				t.Errorf("WaitForWork() = %v, want %v", hasWork, tt.wantWork)
			}
		})
	}
}

func TestWaitForUSReviewerWork(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name     string
		tasks    []models.Task
		wantWork bool
	}{
		{
			name: "US ready for review task available",
			tasks: func() []models.Task {
				rc := "review123"
				return []models.Task{
					{
						ID:           "task-1",
						Status:       "US_READY_FOR_REVIEW",
						RolePair:     "us-writing-pair",
						Priority:     1,
						Created:      now,
						SpecRef:      "README.md",
						DoneWhen:     "Done",
						Scope:        "Test",
						History:      []models.TaskHistoryEntry{},
						ReviewCommit: &rc,
					},
				}
			}(),
			wantWork: true,
		},
		{
			name: "no us-reviewer claimable tasks",
			tasks: []models.Task{
				{
					ID:       "task-1",
					Status:   "REVIEWING_US",
					RolePair: "us-writing-pair",
					Priority: 1,
					Created:  now,
					SpecRef:  "README.md",
					DoneWhen: "Done",
					Scope:    "Test",
					History:  []models.TaskHistoryEntry{},
				},
			},
			wantWork: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
			testhelpers.SetupPipelineConfig(t, tmpDir)
			writePhase2PipelineConfig(t, tmpDir)
			projectRoot := tmpDir

			state := testhelpers.CreateValidState()
			state.Tasks = tt.tasks
			testhelpers.WriteInitialState(t, statePath, state)

			config := SupervisorConfig{StatePath: statePath, ProjectRoot: projectRoot}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			strategy, _ := NewRoleStrategy("us-reviewer", testResolver(t))
			hasWork, err := strategy.WaitForWork(ctx, db.New(statePath), config, 10*time.Millisecond, 100*time.Millisecond)
			if err != nil {
				t.Fatalf("WaitForWork() error = %v", err)
			}
			if hasWork != tt.wantWork {
				t.Errorf("WaitForWork() = %v, want %v", hasWork, tt.wantWork)
			}
		})
	}
}

func TestWaitForCoderWorkDetectsResumableHandoff(t *testing.T) {
	tmpDir := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now)
	task.HandoffPending = true
	assigned := "coder-1"
	task.AssignedTo = &assigned
	state.Tasks = []models.Task{task}
	state.Config.Mode = models.SystemModeRunning
	testhelpers.WriteInitialState(t, statePath, state)

	bb := db.New(statePath)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	strategy, _ := NewRoleStrategy("coder", testResolver(t))
	config := SupervisorConfig{StatePath: statePath, ProjectRoot: tmpDir, AgentID: "coder-1"}
	hasWork, err := strategy.WaitForWork(ctx, bb, config, 10*time.Millisecond, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForWork() error = %v", err)
	}
	if !hasWork {
		t.Fatal("expected resumable handoff to be detected as available work")
	}
}

// TestWaitForCoderWorkDetectsOwnInProgressTask tests that the coder detects
// its own IMPLEMENTING task (not handoff-pending) as available work, enabling
// re-invocation when the CLI exits without completing the task.
func TestWaitForCoderWorkDetectsOwnInProgressTask(t *testing.T) {
	tmpDir := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	lizaDir := filepath.Dir(statePath)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now)
	task.HandoffPending = false
	task.AssignedTo = testhelpers.StringPtr("coder-1")
	state.Tasks = []models.Task{task}
	state.Config.Mode = models.SystemModeRunning
	testhelpers.WriteInitialState(t, statePath, state)

	bb := db.New(statePath)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	hasWork, err := waitForCoderWork(ctx, bb, lizaDir, "coder-1", 10*time.Millisecond, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("waitForCoderWork() error = %v", err)
	}
	if !hasWork {
		t.Fatal("expected own in-progress task to be detected as available work")
	}
}

// TestWaitForCoderWorkIgnoresOtherAgentInProgressTask ensures a coder does NOT
// see another agent's IMPLEMENTING task as its own work.
func TestWaitForCoderWorkIgnoresOtherAgentInProgressTask(t *testing.T) {
	tmpDir := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	lizaDir := filepath.Dir(statePath)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now)
	task.HandoffPending = false
	task.AssignedTo = testhelpers.StringPtr("coder-2") // different agent
	state.Tasks = []models.Task{task}
	state.Config.Mode = models.SystemModeRunning
	testhelpers.WriteInitialState(t, statePath, state)

	bb := db.New(statePath)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	hasWork, err := waitForCoderWork(ctx, bb, lizaDir, "coder-1", 10*time.Millisecond, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("waitForCoderWork() error = %v", err)
	}
	if hasWork {
		t.Fatal("expected other agent's in-progress task NOT to be detected as work")
	}
}
