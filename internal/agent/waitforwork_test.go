package agent

import (
	"context"
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
				{
					ID:        "task-1",
					Status:    models.TaskStatusReady,
					DependsOn: []string{"task-2"},
				},
				testhelpers.BuildTaskByStatus("task-2", models.TaskStatusImplementing, time.Now().UTC()),
			},
			wantWork: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
			lizaDir := filepath.Dir(statePath)

			state := testhelpers.CreateValidState()
			state.Tasks = tt.tasks

			testhelpers.WriteInitialState(t, statePath, state)

			config := SupervisorConfig{
				StatePath: statePath,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			hasWork, err := waitForWork(ctx, db.New(statePath), lizaDir, "coder", config, 10*time.Millisecond, 100*time.Millisecond)

			if err != nil {
				t.Fatalf("waitForWork() error = %v", err)
			}

			if hasWork != tt.wantWork {
				t.Errorf("waitForWork() = %v, want %v", hasWork, tt.wantWork)
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
			projectRoot := tmpDir

			state := testhelpers.CreateValidState()
			state.Tasks = tt.tasks

			testhelpers.WriteInitialState(t, statePath, state)

			config := SupervisorConfig{
				StatePath: statePath,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			hasWork, err := waitForWork(ctx, db.New(statePath), projectRoot, "code-reviewer", config, 10*time.Millisecond, 100*time.Millisecond)

			if err != nil {
				t.Fatalf("waitForWork() error = %v", err)
			}

			if hasWork != tt.wantWork {
				t.Errorf("waitForWork() = %v, want %v", hasWork, tt.wantWork)
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

// TestWaitForPlannerWork tests planner work detection
func TestWaitForPlannerWork(t *testing.T) {
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
			name: "integration failed tasks",
			tasks: []models.Task{
				testhelpers.BuildTaskByStatus("task-1", models.TaskStatusIntegrationFailed, now),
			},
			wantWork: true,
		},
		{
			name: "no planner work needed",
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
			lizaDir := filepath.Dir(statePath)

			state := testhelpers.CreateValidState()
			state.Tasks = tt.tasks

			testhelpers.WriteInitialState(t, statePath, state)

			config := SupervisorConfig{
				StatePath: statePath,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			// Planner now respects maxWait parameter (uses 100ms timeout for test)
			hasWork, err := waitForWork(ctx, db.New(statePath), lizaDir, "planner", config, 10*time.Millisecond, 100*time.Millisecond)

			if err != nil {
				t.Fatalf("waitForWork() error = %v", err)
			}

			if hasWork != tt.wantWork {
				t.Errorf("waitForWork() = %v, want %v", hasWork, tt.wantWork)
			}
		})
	}
}

// TestPlannerRespectsMaxWaitConfig verifies that planner wait loop respects
// the configured max_wait value (does not override with hardcoded value)
func TestPlannerRespectsMaxWaitConfig(t *testing.T) {
	tmpDir := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	lizaDir := filepath.Dir(statePath)

	// Create state with no work available
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{
		testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, time.Now().UTC()),
	}
	testhelpers.WriteInitialState(t, statePath, state)

	config := SupervisorConfig{
		StatePath: statePath,
	}

	// Test with a short maxWait - planner should timeout when maxWait is reached
	ctx := context.Background()
	startTime := time.Now()
	hasWork, err := waitForWork(ctx, db.New(statePath), lizaDir, "planner", config, 10*time.Millisecond, 150*time.Millisecond)
	elapsed := time.Since(startTime)

	if err != nil {
		t.Fatalf("waitForWork() error = %v", err)
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
			name: "planner wakes on wake trigger",
			role: "planner",
			setupState: func() *models.State {
				state := testhelpers.CreateValidState()
				state.Tasks = []models.Task{}
				return state
			},
			modifyState: func(s *models.State) {
				// Add 5 unclaimed tasks to trigger planner wake
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
			lizaDir := filepath.Dir(statePath)

			// Write initial state with no work
			state := tt.setupState()
			testhelpers.WriteInitialState(t, statePath, state)

			config := SupervisorConfig{
				StatePath: statePath,
			}

			bb := db.New(statePath)

			// Start waiting in goroutine
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			startTime := time.Now()
			resultCh := make(chan bool, 1)
			errCh := make(chan error, 1)

			go func() {
				hasWork, err := waitForWork(ctx, bb, lizaDir, tt.role, config, 10*time.Millisecond, 5*time.Second)
				if err != nil {
					errCh <- err
					return
				}
				resultCh <- hasWork
			}()

			// Wait a bit for watcher to start
			time.Sleep(50 * time.Millisecond)

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
				t.Fatalf("waitForWork() error = %v", err)
			case hasWork := <-resultCh:
				elapsed := time.Since(startTime)

				if hasWork != tt.wantWork {
					t.Errorf("waitForWork() = %v, want %v", hasWork, tt.wantWork)
				}

				// Verify wake time is quick (under 500ms including setup)
				if hasWork && elapsed > 500*time.Millisecond {
					t.Errorf("Agent took %v to wake, expected < 500ms", elapsed)
				}
			case <-time.After(6 * time.Second):
				t.Fatal("Timeout waiting for waitForWork result")
			}
		})
	}
}

// TestWaitForWorkCancellation tests context cancellation during wait
func TestWaitForWorkCancellation(t *testing.T) {
	tmpDir := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	lizaDir := filepath.Dir(statePath)

	// Create state with no work
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{}
	testhelpers.WriteInitialState(t, statePath, state)

	config := SupervisorConfig{
		StatePath: statePath,
	}

	bb := db.New(statePath)
	ctx, cancel := context.WithCancel(context.Background())

	// Start waiting in goroutine
	errCh := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		_, err := waitForWork(ctx, bb, lizaDir, "coder", config, 10*time.Millisecond, 10*time.Second)
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
	lizaDir := filepath.Dir(statePath)

	// Create state with no work
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{}
	testhelpers.WriteInitialState(t, statePath, state)

	config := SupervisorConfig{
		StatePath: statePath,
	}

	bb := db.New(statePath)
	ctx := context.Background()

	startTime := time.Now()
	hasWork, err := waitForWork(ctx, bb, lizaDir, "coder", config, 10*time.Millisecond, 200*time.Millisecond)
	elapsed := time.Since(startTime)

	if err != nil {
		t.Fatalf("waitForWork() error = %v", err)
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
	lizaDir := filepath.Dir(statePath)

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
	go func() {
		close(started)
		hasWork, err := waitForCoderWork(ctx, bb, lizaDir, "coder-1", 10*time.Millisecond, 5*time.Second)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- hasWork
	}()

	<-started

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
		t.Fatalf("waitForCoderWork() error = %v", err)
	case hasWork := <-resultCh:
		elapsed := time.Since(startTime)

		if hasWork {
			t.Error("waitForCoderWork() should return false when ABORT detected")
		}

		// Should respond within 200ms
		if elapsed > 200*time.Millisecond {
			t.Errorf("ABORT detection took %v, expected < 200ms", elapsed)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("Timeout waiting for ABORT response")
	}
}

// TestWaitForWorkPollingAbortStateMode tests ABORT detection via state mode in polling wait
func TestWaitForWorkPollingAbortStateMode(t *testing.T) {
	tmpDir := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	lizaDir := filepath.Dir(statePath)

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
		return models.CountClaimableTasks(s, models.RoleCoder) > 0, ""
	}

	// Start waiting in background
	started := make(chan struct{})
	go func() {
		close(started)
		hasWork, err := waitForWorkPolling(ctx, bb, lizaDir, 50*time.Millisecond, 5*time.Second, checkWork)
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

		// Should respond within 100ms
		if elapsed > 100*time.Millisecond {
			t.Errorf("ABORT detection took %v, expected < 100ms", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for ABORT response")
	}
}

// TestAbortPrecedenceOverWork tests that ABORT takes precedence over work
func TestAbortPrecedenceOverWork(t *testing.T) {
	tmpDir := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	lizaDir := filepath.Dir(statePath)

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
	hasWork, err := waitForCoderWork(ctx, bb, lizaDir, "coder-1", 10*time.Millisecond, 100*time.Millisecond)

	if err != nil {
		t.Fatalf("waitForCoderWork() error = %v", err)
	}

	if hasWork {
		t.Error("waitForCoderWork() should return false when ABORT present, even with work available")
	}
}

func TestWaitForCoderWorkDetectsResumableHandoff(t *testing.T) {
	tmpDir := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
	lizaDir := filepath.Dir(statePath)

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

	hasWork, err := waitForCoderWork(ctx, bb, lizaDir, "coder-1", 10*time.Millisecond, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("waitForCoderWork() error = %v", err)
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
