package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
)

// checkAbort returns true if system mode is STOPPED
func checkAbort(projectRoot string) bool {
	statePath := paths.New(projectRoot).StatePath()
	if bb := db.For(statePath); bb != nil {
		state, err := bb.Read()
		if err == nil {
			stopped, _ := isSystemStopped(state)
			return stopped
		}
	}
	return false
}

// isSystemStopped checks if system is in STOPPED mode from already-read state
// Returns (stopped bool, reason string)
func isSystemStopped(state *models.State) (bool, string) {
	if state.Config.Mode == models.SystemModeStopped {
		return true, "System mode is STOPPED"
	}
	return false, ""
}

// waitWhilePaused blocks while system is PAUSED or sprint is in CHECKPOINT status
func waitWhilePaused(ctx context.Context, projectRoot string) error {
	logger := GetLogger()
	statePath := paths.New(projectRoot).StatePath()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		isPaused := false
		pauseReason := ""

		if bb := db.For(statePath); bb != nil {
			state, err := bb.Read()
			if err == nil {
				if state.Config.Mode == models.SystemModePaused {
					isPaused = true
					pauseReason = "[PAUSED] System mode is PAUSED"
				} else if state.Config.Mode == models.SystemModeCircuitBreakerTripped {
					isPaused = true
					pauseReason = "[CIRCUIT BREAKER] Circuit breaker triggered - system halted"
				} else if state.Sprint.Status == models.SprintStatusCheckpoint {
					isPaused = true
					pauseReason = "[CHECKPOINT] Sprint is at checkpoint"
				}
			}
		}

		if !isPaused {
			// Not paused, continue
			return nil
		}

		// Wait and check again
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			logger.Info("System paused, waiting for resume", "pause_reason", pauseReason)
		}
	}
}

// executeAgent executes the CLI with timeout.
// Heartbeat is handled at the supervisor level to keep it active during idle periods.
func executeAgent(ctx context.Context, config SupervisorConfig, prompt string) (int, error) {
	logger := GetLogger()
	// Interactive mode: launch CLI without -p so user can paste the prompt
	if config.Interactive {
		fmt.Println("=== INTERACTIVE MODE ===")
		fmt.Println("Paste the prompt from the file above into the CLI session.")
		fmt.Printf("Launching: %s\n", config.CLIName)
		return config.Executor.ExecuteInteractive(ctx, config.CLIName, config.ProjectRoot)
	}

	// Create timeout context for CLI execution
	execCtx, cancelExec := context.WithTimeout(ctx, config.ExecutionTimeout)
	defer cancelExec()

	// Execute CLI with timeout
	exitCode, err := config.Executor.Execute(execCtx, config.CLIName, config.AgentID, prompt, config.ProjectRoot, config.AutoApprove)

	// Check if execution timed out
	if err != nil && errors.Is(err, context.DeadlineExceeded) {
		logger.Error("Agent execution timeout",
			"agent_id", config.AgentID,
			"timeout", config.ExecutionTimeout,
			"hint", "CLI may be hung, will retry")
		return 1, nil // Return failure code to trigger retry
	}

	// Check if parent context was cancelled (e.g., MCP inactivity timeout).
	// This is NOT a fatal error — the supervisor should restart the session.
	if err != nil && errors.Is(err, context.Canceled) {
		logger.Warn("Agent execution cancelled (MCP inactivity or signal)",
			"agent_id", config.AgentID,
			"hint", "session will restart")
		return 1, nil // Return failure code to trigger retry
	}

	// Check if timeout context was cancelled (even if Execute returned successfully)
	if execCtx.Err() == context.DeadlineExceeded {
		logger.Error("Agent execution timeout (context deadline exceeded)",
			"agent_id", config.AgentID,
			"timeout", config.ExecutionTimeout,
			"hint", "CLI may be hung, will retry")
		return 1, nil // Return failure code to trigger retry
	}

	return exitCode, err
}

// verifyPlannerStateChanges checks if planner made expected state changes after completion
func verifyPlannerStateChanges(bb *db.Blackboard, stateBefore *models.State) error {
	logger := GetLogger()
	// Read state after agent execution
	stateAfter, err := bb.ReadCached()
	if err != nil {
		return fmt.Errorf("failed to read state after agent execution: %w", err)
	}

	// Detect the wake trigger that caused this planner run
	result := DetectPlannerWakeTriggers(stateBefore)

	// Verify expected changes based on trigger
	switch result.Trigger {
	case WakeTriggerInitialPlanning:
		// INITIAL_PLANNING: expect tasks to be created
		if len(stateAfter.Tasks) == 0 {
			return fmt.Errorf("planner completed with INITIAL_PLANNING trigger but no tasks were created")
		}
		logger.Info("Planner created tasks", "task_count", len(stateAfter.Tasks))

	case WakeTriggerBlocked:
		// BLOCKED_TASKS: expect blocked tasks to be unblocked or superseded
		blockedBefore := 0
		blockedAfter := 0
		for _, task := range stateBefore.Tasks {
			if task.Status == models.TaskStatusBlocked {
				blockedBefore++
			}
		}
		for _, task := range stateAfter.Tasks {
			if task.Status == models.TaskStatusBlocked {
				blockedAfter++
			}
		}
		if blockedAfter >= blockedBefore {
			return fmt.Errorf("planner completed with BLOCKED_TASKS trigger but blocked count didn't decrease (before: %d, after: %d)", blockedBefore, blockedAfter)
		}
		logger.Info("Planner resolved blocked tasks", "before", blockedBefore, "after", blockedAfter)

	case WakeTriggerIntegrationFailed:
		// INTEGRATION_FAILED: expect failed tasks to be claimed by coders or handled by planner
		// Count tasks that were INTEGRATION_FAILED before planner ran
		failedBefore := 0
		failedTaskIDs := make([]string, 0)
		for _, task := range stateBefore.Tasks {
			if task.Status == models.TaskStatusIntegrationFailed {
				failedBefore++
				failedTaskIDs = append(failedTaskIDs, task.ID)
			}
		}

		// Check what happened to those tasks after planner ran
		stillFailed := 0
		claimed := 0
		superseded := 0
		for _, taskID := range failedTaskIDs {
			afterTask := stateAfter.FindTask(taskID)
			if afterTask != nil {
				switch afterTask.Status {
				case models.TaskStatusIntegrationFailed:
					stillFailed++
				case models.TaskStatusImplementing:
					claimed++
				case models.TaskStatusSuperseded:
					superseded++
				}
			}
		}

		// Success conditions:
		// 1. Tasks were claimed by coders (expected case)
		// 2. Tasks were superseded by planner (structural issue)
		// 3. Combination of both
		handled := claimed + superseded
		if handled == 0 && stillFailed == failedBefore {
			return fmt.Errorf("planner completed with INTEGRATION_FAILED trigger but no tasks were handled (still %d INTEGRATION_FAILED)", stillFailed)
		}

		logger.Info("Planner checked integration failures", "claimed", claimed, "superseded", superseded, "still_failed", stillFailed)

	case WakeTriggerHypothesisExhausted:
		// HYPOTHESIS_EXHAUSTED: expect exhausted tasks to be updated or superseded
		exhaustedBefore := 0
		exhaustedAfter := 0
		for _, task := range stateBefore.Tasks {
			if len(task.FailedBy) >= 2 {
				exhaustedBefore++
			}
		}
		for _, task := range stateAfter.Tasks {
			if len(task.FailedBy) >= 2 {
				exhaustedAfter++
			}
		}
		if exhaustedAfter >= exhaustedBefore {
			return fmt.Errorf("planner completed with HYPOTHESIS_EXHAUSTED trigger but exhausted count didn't decrease (before: %d, after: %d)", exhaustedBefore, exhaustedAfter)
		}
		logger.Info("Planner handled exhausted hypotheses", "before", exhaustedBefore, "after", exhaustedAfter)

	case WakeTriggerImmediateDiscovery:
		// IMMEDIATE_DISCOVERY: expect discoveries to be converted to tasks
		immediateBefore := 0
		immediateAfter := 0
		for _, disc := range stateBefore.Discovered {
			if disc.Urgency == "immediate" && disc.ConvertedToTask == nil {
				immediateBefore++
			}
		}
		for _, disc := range stateAfter.Discovered {
			if disc.Urgency == "immediate" && disc.ConvertedToTask == nil {
				immediateAfter++
			}
		}
		if immediateAfter >= immediateBefore {
			return fmt.Errorf("planner completed with IMMEDIATE_DISCOVERY trigger but unconverted count didn't decrease (before: %d, after: %d)", immediateBefore, immediateAfter)
		}
		logger.Info("Planner handled immediate discoveries", "before", immediateBefore, "after", immediateAfter)

	case WakeTriggerReplanRequired:
		// AUDIT_REPLAN_REQUIRED: expect unresolved replan findings to decrease
		// (planner should create tasks with origin_finding_id, which auto-links findings)
		unresolvedBefore := countUnresolvedReplanFindings(stateBefore)
		unresolvedAfter := countUnresolvedReplanFindings(stateAfter)
		if unresolvedAfter >= unresolvedBefore && unresolvedBefore > 0 {
			return fmt.Errorf("planner completed with AUDIT_REPLAN_REQUIRED trigger but unresolved replan findings didn't decrease (before: %d, after: %d) — ensure tasks are created with origin_finding_id", unresolvedBefore, unresolvedAfter)
		}
		logger.Info("Planner addressed replan findings", "before", unresolvedBefore, "after", unresolvedAfter)

	case WakeTriggerRemediationNeeded:
		// AUDIT_REMEDIATION_NEEDED: expect unresolved remediation findings to decrease
		unresolvedBefore := countUnresolvedRemediationFindings(stateBefore)
		unresolvedAfter := countUnresolvedRemediationFindings(stateAfter)
		if unresolvedAfter >= unresolvedBefore && unresolvedBefore > 0 {
			return fmt.Errorf("planner completed with AUDIT_REMEDIATION_NEEDED trigger but unresolved remediation findings didn't decrease (before: %d, after: %d) — ensure tasks are created with origin_finding_id", unresolvedBefore, unresolvedAfter)
		}
		logger.Info("Planner addressed remediation findings", "before", unresolvedBefore, "after", unresolvedAfter)

	case WakeTriggerSprintComplete:
		// SPRINT_COMPLETE: expect sprint status to be CHECKPOINT (or COMPLETED)
		if stateAfter.Sprint.Status != models.SprintStatusCheckpoint && stateAfter.Sprint.Status != models.SprintStatusCompleted {
			return fmt.Errorf("planner completed with SPRINT_COMPLETE trigger but sprint status is %s (expected CHECKPOINT or COMPLETED)", stateAfter.Sprint.Status)
		}
		if stateAfter.Sprint.Timeline.CheckpointAt == nil {
			return fmt.Errorf("planner completed with SPRINT_COMPLETE trigger but checkpoint_at is not set")
		}
		logger.Info("Planner completed sprint", "status", stateAfter.Sprint.Status)
	}

	return nil
}
