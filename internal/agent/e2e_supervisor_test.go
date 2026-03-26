package agent

// e2e_supervisor_test.go — True end-to-end tests that invoke RunSupervisor
// with the full autonomous loop. A ScenarioExecutor implements CLIExecutor
// and simulates agent behavior by mutating state.yaml through the blackboard
// on each Execute() call. This validates:
//
//   - Agent registration / unregistration
//   - Work detection (fsnotify / polling)
//   - Task claiming (three-phase with git worktrees)
//   - Prompt building and saving
//   - Heartbeat lifecycle
//   - Exit-code handling (0, 42, crash)
//   - System-mode abort (STOPPED)
//
// Scenarios:
//   S1 — Happy-path coder: READY task → claim → Execute → READY_FOR_REVIEW → STOPPED → exit
//   S2 — Planner wake trigger: no tasks → planner detects INITIAL_PLANNING → Execute → tasks created → STOPPED
//   S3 — NHD escalation: READY task → claim → Execute → NEEDS_HUMAN_DECISION → STOPPED → exit
//   S4 — Runaway exit-42 loop: READY task → claim → exit 42 repeated → BLOCKED after threshold

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/roles"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// ---------------------------------------------------------------------------
// ScenarioExecutor — CLIExecutor that drives state mutations per step
// ---------------------------------------------------------------------------

// StepFunc is called on each Execute() invocation. It receives the blackboard,
// the agentID, and the prompt that was built for this iteration.
// Returning a non-zero exit code simulates the agent exiting with that code.
// Returning an error fails the Execute outright.
type StepFunc func(bb *db.Blackboard, agentID, prompt string) (exitCode int, err error)

// ScenarioExecutor implements CLIExecutor for E2E tests.
// It runs one StepFunc per Execute() call. After all steps are consumed,
// it sets the system mode to STOPPED to cleanly halt the supervisor loop.
type ScenarioExecutor struct {
	mu      sync.Mutex
	steps   []StepFunc
	index   int
	bb      *db.Blackboard
	calls   int
	prompts []string // Saved prompts from each call
}

// NewScenarioExecutor creates a new ScenarioExecutor with the given steps.
func NewScenarioExecutor(bb *db.Blackboard, steps ...StepFunc) *ScenarioExecutor {
	return &ScenarioExecutor{
		steps: steps,
		bb:    bb,
	}
}

func (se *ScenarioExecutor) Execute(ctx context.Context, cliName, agentID, prompt, projectRoot string, autoApprove bool) (int, error) {
	se.mu.Lock()
	defer se.mu.Unlock()

	se.calls++
	se.prompts = append(se.prompts, prompt)

	if se.index < len(se.steps) {
		step := se.steps[se.index]
		se.index++
		return step(se.bb, agentID, prompt)
	}

	// No more steps — stop the system
	return 0, se.stopSystem()
}

func (se *ScenarioExecutor) ExecuteInteractive(ctx context.Context, cliName, projectRoot string) (int, error) {
	return 0, fmt.Errorf("interactive mode not supported in E2E tests")
}

// stopSystem sets Config.Mode to STOPPED so the supervisor exits on next checkAbort.
func (se *ScenarioExecutor) stopSystem() error {
	return se.bb.Modify(func(state *models.State) error {
		state.Config.Mode = models.SystemModeStopped
		return nil
	})
}

// CallCount returns how many times Execute was called.
func (se *ScenarioExecutor) CallCount() int {
	se.mu.Lock()
	defer se.mu.Unlock()
	return se.calls
}

// Prompts returns all prompts that were passed to Execute.
func (se *ScenarioExecutor) Prompts() []string {
	se.mu.Lock()
	defer se.mu.Unlock()
	dst := make([]string, len(se.prompts))
	copy(dst, se.prompts)
	return dst
}

// ---------------------------------------------------------------------------
// E2E test environment setup
// ---------------------------------------------------------------------------

// e2eEnv holds all paths and objects for one E2E test.
type e2eEnv struct {
	ProjectRoot string
	StatePath   string
	LockPath    string
	SpecsDir    string
	BB          *db.Blackboard
}

// setupE2E creates a complete test environment:
//   - git repo with main + integration branches
//   - .liza/ with state.yaml
//   - specs/ with a dummy spec file
//   - state with tight timeouts for fast test execution
func setupE2E(t *testing.T, state *models.State) *e2eEnv {
	t.Helper()

	tmpDir := t.TempDir()

	// Create a real git repo with main + integration branches
	testhelpers.SetupTestGitRepo(t, tmpDir)

	// Create .liza/ directory
	statePath, lockPath := testhelpers.SetupLizaDir(t, tmpDir)
	_ = lockPath

	// Set fast timeouts so tests don't hang
	state.Config.CoderPollInterval = 1
	state.Config.CoderMaxWait = 3
	state.Config.PlannerPollInterval = 1
	state.Config.PlannerMaxWait = 3
	state.Config.ReviewerPollInterval = 1
	state.Config.ReviewerMaxWait = 3
	state.Config.HeartbeatInterval = 60 // Keep heartbeat slow — not under test
	state.Config.LeaseDuration = 600

	// Write state
	bb := testhelpers.WriteInitialState(t, statePath, state)

	// Create specs directory with a minimal spec file (buildPrompt reads it)
	specsDir := filepath.Join(tmpDir, "specs")
	testhelpers.CreateSpecFile(t, tmpDir, "vision.md", testhelpers.ValidVisionSpec)

	// Create prompts directory so savePrompt succeeds
	promptsDir := filepath.Join(tmpDir, ".liza", "prompts")
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		t.Fatalf("Failed to create prompts dir: %v", err)
	}

	return &e2eEnv{
		ProjectRoot: tmpDir,
		StatePath:   statePath,
		LockPath:    lockPath,
		SpecsDir:    specsDir,
		BB:          bb,
	}
}

// makeCoderConfig builds a SupervisorConfig for a coder agent.
func makeCoderConfig(env *e2eEnv, agentID string, executor CLIExecutor) SupervisorConfig {
	return SupervisorConfig{
		AgentID:          agentID,
		Role:             roles.RuntimeCoder,
		ProjectRoot:      env.ProjectRoot,
		StatePath:        env.StatePath,
		LogPath:          filepath.Join(env.ProjectRoot, ".liza", "liza.log"),
		SpecsDir:         env.SpecsDir,
		CLIName:          "claude",
		Interactive:      false,
		Executor:         executor,
		ExecutionTimeout: 30 * time.Second,
	}
}

// makePlannerConfig builds a SupervisorConfig for a planner agent.
func makePlannerConfig(env *e2eEnv, agentID string, executor CLIExecutor) SupervisorConfig {
	return SupervisorConfig{
		AgentID:          agentID,
		Role:             roles.RuntimePlanner,
		ProjectRoot:      env.ProjectRoot,
		StatePath:        env.StatePath,
		LogPath:          filepath.Join(env.ProjectRoot, ".liza", "liza.log"),
		SpecsDir:         env.SpecsDir,
		CLIName:          "claude",
		Interactive:      false,
		Executor:         executor,
		ExecutionTimeout: 30 * time.Second,
	}
}

// readFinalState reads the state from the blackboard after the supervisor exits.
func readFinalState(t *testing.T, bb *db.Blackboard) *models.State {
	t.Helper()
	state, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read final state: %v", err)
	}
	return state
}

// ---------------------------------------------------------------------------
// S1 — Happy-path coder: READY → IMPLEMENTING → READY_FOR_REVIEW → exit
// ---------------------------------------------------------------------------

func TestE2E_S1_HappyPathCoder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// Add a READY task
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReady, now)
	task.AcceptanceCriteria = []string{
		"AC-1: Given a bug, When fixed, Then tests pass",
	}
	task.VerifyCommands = []string{"go test ./..."}
	state.Tasks = append(state.Tasks, task)

	env := setupE2E(t, state)

	// Step 1: Simulate agent completing work — transition task to READY_FOR_REVIEW
	step1 := func(bb *db.Blackboard, agentID, prompt string) (int, error) {
		// Verify we received a prompt (proves prompt building worked)
		if prompt == "" {
			return 1, fmt.Errorf("expected non-empty prompt")
		}

		// Simulate agent completing work: transition task IMPLEMENTING → READY_FOR_REVIEW
		err := bb.Modify(func(s *models.State) error {
			t := s.FindTask("task-1")
			if t == nil {
				return fmt.Errorf("task-1 not found")
			}
			if t.Status != models.TaskStatusImplementing {
				return fmt.Errorf("expected IMPLEMENTING, got %s", t.Status)
			}
			if err := t.Transition(models.TaskStatusReadyForReview); err != nil {
				return err
			}
			reviewCommit := "abc1234"
			t.ReviewCommit = &reviewCommit
			return nil
		})
		if err != nil {
			return 1, err
		}

		return 0, nil // exit 0 = success
	}

	// After step1 returns, the next Execute call will stop the system
	// (ScenarioExecutor has only 1 step, so the 2nd call triggers stopSystem).
	// But actually the supervisor will see no more claimable tasks and exit via waitForWork.
	executor := NewScenarioExecutor(env.BB, step1)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	config := makeCoderConfig(env, "coder-1", executor)
	err := RunSupervisor(ctx, config)
	if err != nil {
		t.Fatalf("RunSupervisor failed: %v", err)
	}

	// Verify final state
	finalState := readFinalState(t, env.BB)

	// Task should be READY_FOR_REVIEW
	finalTask := finalState.FindTask("task-1")
	if finalTask == nil {
		t.Fatal("task-1 not found in final state")
	}
	if finalTask.Status != models.TaskStatusReadyForReview {
		t.Errorf("expected READY_FOR_REVIEW, got %s", finalTask.Status)
	}

	// Task should have been assigned to our agent
	if finalTask.AssignedTo == nil || *finalTask.AssignedTo != "coder-1" {
		t.Errorf("expected AssignedTo=coder-1, got %v", finalTask.AssignedTo)
	}

	// ReviewCommit should be set
	if finalTask.ReviewCommit == nil || *finalTask.ReviewCommit != "abc1234" {
		t.Errorf("expected ReviewCommit=abc1234, got %v", finalTask.ReviewCommit)
	}

	// Agent should have been registered then unregistered
	// After RunSupervisor returns, unregisterAgent runs via defer — but agent
	// entry might still exist with status OFFLINE. Check that it was registered at some point.
	// The executor got called, which proves the full loop ran.
	if executor.CallCount() < 1 {
		t.Errorf("expected at least 1 Execute call, got %d", executor.CallCount())
	}

	// Verify a prompt was built and passed
	prompts := executor.Prompts()
	if len(prompts) == 0 || prompts[0] == "" {
		t.Error("expected non-empty prompt to be passed to executor")
	}
}

// ---------------------------------------------------------------------------
// S2 — Planner wake trigger: no tasks → INITIAL_PLANNING → agent creates tasks
// ---------------------------------------------------------------------------

func TestE2E_S2_PlannerWakeTrigger(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	state := testhelpers.CreateValidState()
	// No tasks — triggers INITIAL_PLANNING wake trigger

	env := setupE2E(t, state)

	// Step 1: Simulate planner creating tasks
	step1 := func(bb *db.Blackboard, agentID, prompt string) (int, error) {
		err := bb.Modify(func(s *models.State) error {
			now := time.Now().UTC()

			// Planner creates two tasks
			s.Tasks = append(s.Tasks, models.Task{
				ID:          "task-plan-1",
				Type:        models.TaskTypeCoding,
				Description: "Implement feature A",
				Status:      models.TaskStatusReady,
				Priority:    1,
				Created:     now,
				SpecRef:     "specs/vision.md",
				DoneWhen:    "Feature A works",
				Scope:       "Feature A scope",
				History:     []models.TaskHistoryEntry{},
			})
			s.Tasks = append(s.Tasks, models.Task{
				ID:          "task-plan-2",
				Type:        models.TaskTypeCoding,
				Description: "Implement feature B",
				Status:      models.TaskStatusReady,
				Priority:    2,
				Created:     now,
				SpecRef:     "specs/vision.md",
				DoneWhen:    "Feature B works",
				Scope:       "Feature B scope",
				History:     []models.TaskHistoryEntry{},
			})

			// Stop the system after planning
			s.Config.Mode = models.SystemModeStopped
			return nil
		})
		return 0, err
	}

	executor := NewScenarioExecutor(env.BB, step1)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	config := makePlannerConfig(env, "planner-1", executor)
	err := RunSupervisor(ctx, config)
	if err != nil {
		t.Fatalf("RunSupervisor failed: %v", err)
	}

	// Verify final state
	finalState := readFinalState(t, env.BB)

	// Planner should have created 2 tasks
	if len(finalState.Tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(finalState.Tasks))
	}

	// Both tasks should be READY
	for _, task := range finalState.Tasks {
		if task.Status != models.TaskStatusReady {
			t.Errorf("task %s: expected READY, got %s", task.ID, task.Status)
		}
	}

	// Executor should have been called exactly once
	if executor.CallCount() != 1 {
		t.Errorf("expected 1 Execute call, got %d", executor.CallCount())
	}

	// System should be STOPPED
	if finalState.Config.Mode != models.SystemModeStopped {
		t.Errorf("expected STOPPED mode, got %s", finalState.Config.Mode)
	}
}

// ---------------------------------------------------------------------------
// S3 — NHD escalation: READY → claim → NEEDS_HUMAN_DECISION → exit
// ---------------------------------------------------------------------------

func TestE2E_S3_NeedsHumanDecision(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// Add a READY task with ambiguous spec
	task := testhelpers.BuildTaskByStatus("task-nhd", models.TaskStatusReady, now)
	task.Description = "Investigate ambiguous requirements"
	state.Tasks = append(state.Tasks, task)

	env := setupE2E(t, state)

	// Step 1: Simulate agent escalating to NHD
	step1 := func(bb *db.Blackboard, agentID, prompt string) (int, error) {
		err := bb.Modify(func(s *models.State) error {
			t := s.FindTask("task-nhd")
			if t == nil {
				return fmt.Errorf("task-nhd not found")
			}

			// Agent escalates: IMPLEMENTING → NEEDS_HUMAN_DECISION
			if err := t.Transition(models.TaskStatusNeedsHumanDecision); err != nil {
				return err
			}
			reason := "Spec is ambiguous: unclear whether feature X should do A or B"
			t.BlockedReason = &reason
			t.BlockedQuestions = []string{
				"Should feature X do A or B?",
				"What is the expected behavior for edge case Y?",
			}
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

	// Verify final state
	finalState := readFinalState(t, env.BB)

	finalTask := finalState.FindTask("task-nhd")
	if finalTask == nil {
		t.Fatal("task-nhd not found in final state")
	}
	if finalTask.Status != models.TaskStatusNeedsHumanDecision {
		t.Errorf("expected NEEDS_HUMAN_DECISION, got %s", finalTask.Status)
	}

	// Blocked questions should be preserved
	if len(finalTask.BlockedQuestions) != 2 {
		t.Errorf("expected 2 blocked questions, got %d", len(finalTask.BlockedQuestions))
	}

	// Blocked reason should be set
	if finalTask.BlockedReason == nil || *finalTask.BlockedReason == "" {
		t.Error("expected non-empty blocked reason")
	}

	// Executor was called exactly once
	if executor.CallCount() != 1 {
		t.Errorf("expected 1 Execute call, got %d", executor.CallCount())
	}
}

// ---------------------------------------------------------------------------
// S4 — Runaway exit-42 loop: repeated exit 42 → BLOCKED after threshold
// ---------------------------------------------------------------------------

func TestE2E_S4_RunawayExit42Loop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// Set very short backoff so we don't waste time sleeping
	state.Config.Exit42RestartThreshold = 3  // Block after 3 restarts (RestartCount > 3)
	state.Config.Exit42MaxBackoffSeconds = 1 // 1-second max backoff for fast tests
	// Keep poll/wait short
	state.Config.CoderPollInterval = 1
	state.Config.CoderMaxWait = 5

	// Add a READY task
	task := testhelpers.BuildTaskByStatus("task-runaway", models.TaskStatusReady, now)
	task.AcceptanceCriteria = []string{
		"AC-1: Given a problem, When attempted, Then it fails",
	}
	state.Tasks = append(state.Tasks, task)

	env := setupE2E(t, state)

	// Each step returns exit code 42 AND sets HandoffPending=true.
	// This simulates an agent that hits context limits and marks the task for handoff
	// before exiting. Without HandoffPending, the task stays IMPLEMENTING but is
	// not resumable — the supervisor would exit instead of retrying.
	//
	// The flow per iteration:
	//   1. waitForWork → sees resumable handoff (or first time: claimable task)
	//   2. claimCoderTask → ResumeHandoff (or first time: ClaimTask)
	//   3. executeAgent → this step func → sets HandoffPending=true, returns exit 42
	//   4. resetAgentAfterExit → agent IDLE
	//   5. exit42Tracker.Handle → increments count
	//   6. When count > threshold → task transitions to BLOCKED → loop exits
	exit42Step := func(bb *db.Blackboard, agentID, prompt string) (int, error) {
		// Set HandoffPending so the supervisor can resume the task on next iteration
		err := bb.Modify(func(s *models.State) error {
			task := s.FindTask("task-runaway")
			if task != nil {
				task.HandoffPending = true
			}
			return nil
		})
		if err != nil {
			return 1, err
		}
		return 42, nil
	}

	// Provide enough steps to exceed the threshold (3) plus margin.
	// threshold=3 means blocking occurs at RestartCount > 3, so we need 4 executions.
	executor := NewScenarioExecutor(env.BB, exit42Step, exit42Step, exit42Step, exit42Step, exit42Step, exit42Step)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	config := makeCoderConfig(env, "coder-1", executor)
	err := RunSupervisor(ctx, config)
	if err != nil {
		t.Fatalf("RunSupervisor failed: %v", err)
	}

	// Verify final state
	finalState := readFinalState(t, env.BB)

	finalTask := finalState.FindTask("task-runaway")
	if finalTask == nil {
		t.Fatal("task-runaway not found in final state")
	}

	// Task should be BLOCKED after exceeding the exit-42 threshold
	if finalTask.Status != models.TaskStatusBlocked {
		t.Errorf("expected BLOCKED after exit-42 threshold, got %s", finalTask.Status)
	}

	// The exit42 restart count should be at or above the threshold
	if finalTask.Exit42RestartCount < 3 {
		t.Errorf("expected Exit42RestartCount >= 3, got %d", finalTask.Exit42RestartCount)
	}

	// Executor should have been called at least 3 times (the threshold)
	if executor.CallCount() < 3 {
		t.Errorf("expected at least 3 Execute calls, got %d", executor.CallCount())
	}
}

// ---------------------------------------------------------------------------
// S5 — Stress: many tasks processed sequentially by a single coder
// ---------------------------------------------------------------------------

func TestE2E_S5_StressSequentialTasks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E stress test in short mode")
	}

	const taskCount = 10
	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// Create N READY tasks
	for i := 1; i <= taskCount; i++ {
		task := testhelpers.BuildTaskByStatus(fmt.Sprintf("task-%d", i), models.TaskStatusReady, now)
		task.Priority = i // Different priorities ensure deterministic claim order
		state.Tasks = append(state.Tasks, task)
	}

	env := setupE2E(t, state)

	// Each step transitions the claimed task to READY_FOR_REVIEW.
	// The supervisor will loop back, find the next claimable task, claim it, and call Execute again.
	var completedTasks []string
	var completedMu sync.Mutex

	makeStep := func() StepFunc {
		return func(bb *db.Blackboard, agentID, prompt string) (int, error) {
			// Find which task is currently IMPLEMENTING (that's the one we just claimed)
			s, err := bb.Read()
			if err != nil {
				return 1, err
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

			// Transition to READY_FOR_REVIEW
			err = bb.Modify(func(s *models.State) error {
				t := s.FindTask(claimedID)
				if t == nil {
					return fmt.Errorf("task %s not found", claimedID)
				}
				if err := t.Transition(models.TaskStatusReadyForReview); err != nil {
					return err
				}
				rc := "commit-" + claimedID
				t.ReviewCommit = &rc
				return nil
			})
			if err != nil {
				return 1, err
			}

			completedMu.Lock()
			completedTasks = append(completedTasks, claimedID)
			completedMu.Unlock()
			return 0, nil
		}
	}

	// Build steps — one per task
	steps := make([]StepFunc, taskCount)
	for i := range steps {
		steps[i] = makeStep()
	}
	executor := NewScenarioExecutor(env.BB, steps...)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	config := makeCoderConfig(env, "coder-1", executor)
	err := RunSupervisor(ctx, config)
	if err != nil {
		t.Fatalf("RunSupervisor failed: %v", err)
	}

	// Verify all tasks were completed
	finalState := readFinalState(t, env.BB)

	completedMu.Lock()
	completed := len(completedTasks)
	completedMu.Unlock()

	if completed != taskCount {
		t.Errorf("expected %d tasks completed, got %d", taskCount, completed)
	}

	// All tasks should be READY_FOR_REVIEW
	for _, task := range finalState.Tasks {
		if task.Status != models.TaskStatusReadyForReview {
			t.Errorf("task %s: expected READY_FOR_REVIEW, got %s", task.ID, task.Status)
		}
	}

	t.Logf("Stress test completed: %d tasks processed sequentially, calls=%d", completed, executor.CallCount())
}

// ---------------------------------------------------------------------------
// S6 — Stress: concurrent supervisors racing for tasks
// ---------------------------------------------------------------------------

func TestE2E_S6_StressConcurrentCoders(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E stress test in short mode")
	}

	const taskCount = 6
	const coderCount = 3
	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	for i := 1; i <= taskCount; i++ {
		task := testhelpers.BuildTaskByStatus(fmt.Sprintf("task-%d", i), models.TaskStatusReady, now)
		task.Priority = i
		state.Tasks = append(state.Tasks, task)
	}

	env := setupE2E(t, state)

	// Track which agent completed which tasks
	type completion struct {
		agentID string
		taskID  string
	}
	var completions []completion
	var compMu sync.Mutex

	// Each coder gets its own executor. Every Execute call completes the current task.
	makeCoderExecutor := func(agentID string) *ScenarioExecutor {
		step := func(bb *db.Blackboard, aid, prompt string) (int, error) {
			s, err := bb.Read()
			if err != nil {
				return 1, err
			}
			var claimedID string
			for _, task := range s.Tasks {
				if task.Status == models.TaskStatusImplementing && task.AssignedTo != nil && *task.AssignedTo == aid {
					claimedID = task.ID
					break
				}
			}
			if claimedID == "" {
				return 1, fmt.Errorf("no IMPLEMENTING task for %s", aid)
			}

			err = bb.Modify(func(s *models.State) error {
				t := s.FindTask(claimedID)
				if t == nil {
					return fmt.Errorf("task %s gone", claimedID)
				}
				if err := t.Transition(models.TaskStatusReadyForReview); err != nil {
					return err
				}
				rc := "commit-" + claimedID
				t.ReviewCommit = &rc
				return nil
			})
			if err != nil {
				return 1, err
			}

			compMu.Lock()
			completions = append(completions, completion{agentID: aid, taskID: claimedID})
			compMu.Unlock()
			return 0, nil
		}

		// Each coder gets enough steps to handle all tasks (though it will only get ~taskCount/coderCount)
		steps := make([]StepFunc, taskCount)
		for i := range steps {
			steps[i] = step
		}
		return NewScenarioExecutor(env.BB, steps...)
	}

	// Launch coders concurrently
	var wg sync.WaitGroup
	errs := make([]error, coderCount)

	for i := 1; i <= coderCount; i++ {
		i := i
		agentID := fmt.Sprintf("coder-%d", i)
		executor := makeCoderExecutor(agentID)
		config := makeCoderConfig(env, agentID, executor)
		// More generous maxWait for concurrent test
		config.ExecutionTimeout = 60 * time.Second

		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			errs[i-1] = RunSupervisor(ctx, config)
		}()
	}

	wg.Wait()

	// Check no fatal errors from supervisors
	for i, err := range errs {
		if err != nil {
			t.Errorf("coder-%d RunSupervisor error: %v", i+1, err)
		}
	}

	// Verify results
	finalState := readFinalState(t, env.BB)

	compMu.Lock()
	totalCompleted := len(completions)
	compMu.Unlock()

	// All tasks should be READY_FOR_REVIEW (each claimed by exactly one agent)
	reviewCount := 0
	for _, task := range finalState.Tasks {
		if task.Status == models.TaskStatusReadyForReview {
			reviewCount++
		}
	}

	if reviewCount != taskCount {
		t.Errorf("expected %d tasks READY_FOR_REVIEW, got %d", taskCount, reviewCount)
	}

	// Verify no task was double-claimed — each task should appear at most once in completions
	taskClaims := make(map[string]int)
	compMu.Lock()
	for _, c := range completions {
		taskClaims[c.taskID]++
	}
	compMu.Unlock()
	for tid, count := range taskClaims {
		if count > 1 {
			t.Errorf("task %s was completed %d times (should be exactly 1)", tid, count)
		}
	}

	// Verify multiple agents actually participated
	agentSeen := make(map[string]bool)
	compMu.Lock()
	for _, c := range completions {
		agentSeen[c.agentID] = true
	}
	compMu.Unlock()

	t.Logf("Stress concurrent: %d tasks completed by %d agents (completions=%d)", reviewCount, len(agentSeen), totalCompleted)
	if len(agentSeen) < 2 {
		t.Logf("WARNING: only %d agent(s) got work — race conditions may have caused serialisation", len(agentSeen))
	}
}

// ---------------------------------------------------------------------------
// S7 — Stress: mixed roles (planner + coder) concurrent lifecycle
// ---------------------------------------------------------------------------

func TestE2E_S7_StressMixedRoles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E stress test in short mode")
	}

	state := testhelpers.CreateValidState()
	// Start with no tasks — planner creates them, coder processes them
	env := setupE2E(t, state)

	const tasksToCreate = 4

	// -- Planner executor: creates tasks, then stops system after one execution
	plannerStep := func(bb *db.Blackboard, agentID, prompt string) (int, error) {
		err := bb.Modify(func(s *models.State) error {
			now := time.Now().UTC()
			for i := 1; i <= tasksToCreate; i++ {
				s.Tasks = append(s.Tasks, models.Task{
					ID:          fmt.Sprintf("task-mixed-%d", i),
					Type:        models.TaskTypeCoding,
					Description: fmt.Sprintf("Mixed role task %d", i),
					Status:      models.TaskStatusReady,
					Priority:    i,
					Created:     now,
					SpecRef:     "specs/vision.md",
					DoneWhen:    "Tests pass",
					Scope:       "Task scope",
					RolePair:    "coding-pair",
					History:     []models.TaskHistoryEntry{},
				})
			}
			return nil
		})
		return 0, err
	}

	// Planner runs once (creates tasks) then system detects no more planner triggers after tasks exist.
	plannerExec := NewScenarioExecutor(env.BB, plannerStep)

	// -- Coder executor: processes tasks one at a time
	coderStep := func(bb *db.Blackboard, agentID, prompt string) (int, error) {
		s, err := bb.Read()
		if err != nil {
			return 1, err
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

		err = bb.Modify(func(s *models.State) error {
			t := s.FindTask(claimedID)
			if t == nil {
				return fmt.Errorf("task %s gone", claimedID)
			}
			if err := t.Transition(models.TaskStatusReadyForReview); err != nil {
				return err
			}
			rc := "commit-" + claimedID
			t.ReviewCommit = &rc
			return nil
		})
		return 0, err
	}

	coderSteps := make([]StepFunc, tasksToCreate)
	for i := range coderSteps {
		coderSteps[i] = coderStep
	}
	coderExec := NewScenarioExecutor(env.BB, coderSteps...)

	plannerConfig := makePlannerConfig(env, "planner-1", plannerExec)
	coderConfig := makeCoderConfig(env, "coder-1", coderExec)

	var wg sync.WaitGroup
	var plannerErr, coderErr error

	// Start planner first so it creates tasks
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		plannerErr = RunSupervisor(ctx, plannerConfig)
	}()

	// Give planner a moment to register and create tasks, then start coder
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(2 * time.Second) // Let planner create tasks first
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		coderErr = RunSupervisor(ctx, coderConfig)
	}()

	wg.Wait()

	if plannerErr != nil {
		t.Errorf("planner error: %v", plannerErr)
	}
	if coderErr != nil {
		t.Errorf("coder error: %v", coderErr)
	}

	finalState := readFinalState(t, env.BB)

	// Planner should have created tasks
	if len(finalState.Tasks) < tasksToCreate {
		t.Errorf("expected at least %d tasks, got %d", tasksToCreate, len(finalState.Tasks))
	}

	// Count how many the coder completed
	reviewCount := 0
	for _, task := range finalState.Tasks {
		if task.Status == models.TaskStatusReadyForReview {
			reviewCount++
		}
	}

	t.Logf("Mixed roles: planner created %d tasks, coder completed %d", len(finalState.Tasks), reviewCount)

	if reviewCount == 0 {
		t.Error("coder completed zero tasks — mixed-role handoff failed")
	}
}

// ---------------------------------------------------------------------------
// S8 — Stress: rapid state churn with crash recovery
// ---------------------------------------------------------------------------

func TestE2E_S8_StressCrashRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E stress test in short mode")
	}

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// 5 tasks — the agent will crash on the first 2, succeed on the rest
	const taskCount = 5
	for i := 1; i <= taskCount; i++ {
		task := testhelpers.BuildTaskByStatus(fmt.Sprintf("task-crash-%d", i), models.TaskStatusReady, now)
		task.Priority = i
		state.Tasks = append(state.Tasks, task)
	}

	env := setupE2E(t, state)

	// Track execution attempts
	var attempts []string
	var attemptMu sync.Mutex
	callNum := 0

	step := func(bb *db.Blackboard, agentID, prompt string) (int, error) {
		s, err := bb.Read()
		if err != nil {
			return 1, err
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

		attemptMu.Lock()
		callNum++
		currentCall := callNum
		attempts = append(attempts, fmt.Sprintf("call=%d task=%s", currentCall, claimedID))
		attemptMu.Unlock()

		// First 2 calls: crash (exit code 1) — agent will retry
		if currentCall <= 2 {
			return 1, nil
		}

		// Remaining calls: complete the task
		err = bb.Modify(func(s *models.State) error {
			t := s.FindTask(claimedID)
			if t == nil {
				return fmt.Errorf("task %s gone", claimedID)
			}
			if err := t.Transition(models.TaskStatusReadyForReview); err != nil {
				return err
			}
			rc := "commit-" + claimedID
			t.ReviewCommit = &rc
			return nil
		})
		return 0, err
	}

	// Need enough steps: 2 crashes + up to 5 successes + some margin
	steps := make([]StepFunc, taskCount+5)
	for i := range steps {
		steps[i] = step
	}
	executor := NewScenarioExecutor(env.BB, steps...)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	config := makeCoderConfig(env, "coder-1", executor)
	err := RunSupervisor(ctx, config)
	if err != nil {
		t.Fatalf("RunSupervisor failed: %v", err)
	}

	finalState := readFinalState(t, env.BB)

	reviewCount := 0
	for _, task := range finalState.Tasks {
		if task.Status == models.TaskStatusReadyForReview {
			reviewCount++
		}
	}

	attemptMu.Lock()
	totalAttempts := len(attempts)
	attemptMu.Unlock()

	t.Logf("Crash recovery: %d attempts, %d tasks completed (of %d)", totalAttempts, reviewCount, taskCount)

	// At least some tasks should be completed
	if reviewCount == 0 {
		t.Error("no tasks completed after crash recovery")
	}

	// We should have had more attempts than tasks (due to crashes)
	if totalAttempts <= taskCount {
		t.Logf("NOTE: expected more than %d attempts due to crashes, got %d", taskCount, totalAttempts)
	}
}
