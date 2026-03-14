package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/ops"
	"github.com/liza-mas/liza/internal/roles"
)

// nonZeroOr returns val if positive, otherwise fallback.
func nonZeroOr(val, fallback int) int {
	if val > 0 {
		return val
	}
	return fallback
}

// getRoleWaitConfig returns poll interval and max wait based on role-specific config.
// Falls back to shell-script parity defaults when config values are unset.
func getRoleWaitConfig(state *models.State, role string) (pollInterval, maxWait time.Duration) {
	var pollSeconds, maxWaitSeconds int

	switch role {
	case roles.RuntimePlanner:
		pollSeconds = nonZeroOr(state.Config.PlannerPollInterval, models.DefaultPlannerPollInterval)
		maxWaitSeconds = nonZeroOr(state.Config.PlannerMaxWait, models.DefaultPlannerMaxWait)
	case roles.RuntimeCodeReviewer:
		pollSeconds = nonZeroOr(state.Config.ReviewerPollInterval, models.DefaultReviewerPollInterval)
		maxWaitSeconds = nonZeroOr(state.Config.ReviewerMaxWait, models.DefaultReviewerMaxWait)
	case roles.RuntimeAuditor:
		pollSeconds = nonZeroOr(state.Config.AuditorPollInterval, models.DefaultAuditorPollInterval)
		maxWaitSeconds = nonZeroOr(state.Config.AuditorMaxWait, models.DefaultAuditorMaxWait)
	default:
		pollSeconds = nonZeroOr(state.Config.CoderPollInterval, models.DefaultCoderPollInterval)
		maxWaitSeconds = nonZeroOr(state.Config.CoderMaxWait, models.DefaultCoderMaxWait)
	}

	return time.Duration(pollSeconds) * time.Second, time.Duration(maxWaitSeconds) * time.Second
}

// waitForWork is a dispatcher to role-specific wait functions
func waitForWork(ctx context.Context, bb *db.Blackboard, projectRoot string, role string, config SupervisorConfig, pollInterval, maxWait time.Duration) (bool, error) {
	logger := GetLogger()

	// Planners use the configured maxWait from getRoleWaitConfig, which provides
	// a default if PlannerMaxWait is not set. The planner wait loop will exit
	// on ABORT/state change or when maxWait is reached.
	logger.Debug("agent waiting for work", "maxWait", maxWait, "role", role)

	switch role {
	case roles.RuntimeCoder:
		return waitForCoderWork(ctx, bb, projectRoot, config.AgentID, pollInterval, maxWait)
	case roles.RuntimeCodeReviewer:
		return waitForReviewerWork(ctx, bb, projectRoot, pollInterval, maxWait)
	case roles.RuntimePlanner:
		return waitForPlannerWork(ctx, bb, projectRoot, pollInterval, maxWait)
	case roles.RuntimeAuditor:
		return waitForAuditorWork(ctx, bb, projectRoot, pollInterval, maxWait)
	default:
		return false, fmt.Errorf("unknown role: %s", role)
	}
}

// workCheckFunc checks if work is available for an agent role.
// Returns (hasWork, logMessage). If logMessage is non-empty, it will be printed when work is found.
type workCheckFunc func(*models.State) (hasWork bool, logMessage string)

// waitForWorkEventDriven is a generic event-driven wait implementation for all agent roles.
// It uses fsnotify to detect state changes and wake immediately when work becomes available.
func waitForWorkEventDriven(
	ctx context.Context,
	bb *db.Blackboard,
	projectRoot string,
	pollInterval, maxWait time.Duration,
	checkWork workCheckFunc,
) (bool, error) {
	logger := GetLogger()

	// Check context cancellation before doing any work.
	// If we skip this check, and work is available, we'd return true immediately
	// even if the context was already cancelled, causing the supervisor to
	// continue running when it should stop.
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}

	deadline := time.Now().Add(maxWait)

	// Check immediately first
	state, err := bb.ReadCached()
	if err != nil {
		return false, fmt.Errorf("failed to read state: %w", err)
	}

	// Check for ABORT before checking for work
	if stopped, reason := isSystemStopped(state); stopped {
		logger.Info("ABORT detected", "reason", reason)
		return false, nil
	}

	if hasWork, logMsg := checkWork(state); hasWork {
		if logMsg != "" {
			logger.Info(logMsg)
		}
		return true, nil
	} else if state.Config.DiagnosticLogging && logMsg != "" {
		// Only show "no work" diagnostics if enabled
		logger.Info(logMsg)
	}

	// Try to set up event-driven watching
	watcher, err := bb.WatchForChanges()
	if err != nil {
		// Fallback to polling if watcher fails
		return waitForWorkPolling(ctx, bb, projectRoot, pollInterval, maxWait, checkWork)
	}
	defer watcher.Close()

	// Add ticker for periodic ABORT checks (file-based fallback)
	abortTicker := time.NewTicker(5 * time.Second)
	defer abortTicker.Stop()

	// Deadline timer — created once to avoid timer leak in the select loop
	deadlineTimer := time.NewTimer(time.Until(deadline))
	defer deadlineTimer.Stop()

	// Event-driven wait loop
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()

		case <-abortTicker.C:
			state, err := bb.ReadCached()
			if err != nil {
				return false, fmt.Errorf("failed to read state: %w", err)
			}
			if stopped, reason := isSystemStopped(state); stopped {
				logger.Info("ABORT detected", "reason", reason)
				return false, nil
			}
			if time.Now().After(deadline) {
				return false, nil
			}

		case <-watcher.Events():
			// State changed, check for work
			state, err := bb.ReadCached()
			if err != nil {
				return false, fmt.Errorf("failed to read state: %w", err)
			}

			// Check for ABORT before checking for work
			if stopped, reason := isSystemStopped(state); stopped {
				logger.Info("ABORT detected", "reason", reason)
				return false, nil
			}

			if hasWork, logMsg := checkWork(state); hasWork {
				if logMsg != "" {
					logger.Info(logMsg)
				}
				return true, nil
			} else if state.Config.DiagnosticLogging && logMsg != "" {
				// Only show "no work" diagnostics if enabled
				logger.Info(logMsg)
			}

			if time.Now().After(deadline) {
				return false, nil
			}

		case err := <-watcher.Errors():
			// Watcher error, fallback to polling
			logger.Warn("Watcher error, falling back to polling", "error", err)
			watcher.Close()
			return waitForWorkPolling(ctx, bb, projectRoot, pollInterval, maxWait, checkWork)

		case <-deadlineTimer.C:
			return false, nil
		}
	}
}

// waitForWorkPolling is a generic polling wait implementation for all agent roles.
// This is used as a fallback when fsnotify is unavailable or encounters errors.
func waitForWorkPolling(
	ctx context.Context,
	bb *db.Blackboard,
	projectRoot string,
	pollInterval, maxWait time.Duration,
	checkWork workCheckFunc,
) (bool, error) {
	logger := GetLogger()
	deadline := time.Now().Add(maxWait)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-ticker.C:
			state, err := bb.Read()
			if err != nil {
				return false, fmt.Errorf("failed to read state: %w", err)
			}

			// Check for ABORT before checking for work
			if stopped, reason := isSystemStopped(state); stopped {
				logger.Info("ABORT detected", "reason", reason)
				return false, nil
			}

			if hasWork, logMsg := checkWork(state); hasWork {
				if logMsg != "" {
					logger.Info(logMsg)
				}
				return true, nil
			} else if state.Config.DiagnosticLogging && logMsg != "" {
				// Only show "no work" diagnostics if enabled
				logger.Info(logMsg)
			}

			if time.Now().After(deadline) {
				return false, nil
			}
		}
	}
}

// waitForCoderWork waits for claimable tasks, resumable handoff tasks,
// or tasks already claimed by this agent that are still IMPLEMENTING
// (i.e. the previous CLI invocation exited without completing the task).
func waitForCoderWork(ctx context.Context, bb *db.Blackboard, projectRoot, agentID string, pollInterval, maxWait time.Duration) (bool, error) {
	if cleared, err := ops.ClearStaleCodingClaims(projectRoot); err != nil {
		GetLogger().Warn("Failed to clear stale coding claims before coder wait", "error", err)
	} else if cleared > 0 {
		GetLogger().Info("Cleared stale coding claims before coder wait", "count", cleared)
	}

	return waitForWorkEventDriven(ctx, bb, projectRoot, pollInterval, maxWait,
		func(s *models.State) (bool, string) {
			claimable := models.CountClaimableTasks(s, models.RoleCoder)
			resumableHandoffs := countResumableHandoffTasks(s, agentID)
			ownInProgress := countOwnInProgressTasks(s, agentID)
			logMsg := models.GetCoderWorkDiagnostics(s)

			if resumableHandoffs > 0 {
				handoffMsg := fmt.Sprintf("Found %d resumable handoff task(s) for %s", resumableHandoffs, agentID)
				if logMsg != "" {
					logMsg = handoffMsg + "; " + logMsg
				} else {
					logMsg = handoffMsg
				}
			}

			if ownInProgress > 0 {
				ownMsg := fmt.Sprintf("Found %d in-progress task(s) already claimed by %s", ownInProgress, agentID)
				if logMsg != "" {
					logMsg = ownMsg + "; " + logMsg
				} else {
					logMsg = ownMsg
				}
			}

			return claimable > 0 || resumableHandoffs > 0 || ownInProgress > 0, logMsg
		})
}

func isResumableHandoff(task *models.Task, agentID string) bool {
	return task.Status == models.TaskStatusImplementing &&
		task.HandoffPending &&
		task.AssignedTo != nil &&
		*task.AssignedTo == agentID
}

func countResumableHandoffTasks(state *models.State, agentID string) int {
	count := 0
	for i := range state.Tasks {
		if isResumableHandoff(&state.Tasks[i], agentID) {
			count++
		}
	}
	return count
}

// countOwnInProgressTasks counts tasks that are IMPLEMENTING and already
// assigned to this agent (but not handoff-pending, which is handled separately).
// This enables re-invocation when the CLI exits without completing the task.
func countOwnInProgressTasks(state *models.State, agentID string) int {
	count := 0
	for i := range state.Tasks {
		task := &state.Tasks[i]
		if task.Status == models.TaskStatusImplementing &&
			!task.HandoffPending &&
			task.AssignedTo != nil &&
			*task.AssignedTo == agentID {
			count++
		}
	}
	return count
}

// waitForReviewerWork waits for reviewable tasks using event-driven detection
func waitForReviewerWork(ctx context.Context, bb *db.Blackboard, projectRoot string, pollInterval, maxWait time.Duration) (bool, error) {
	if cleared, err := ops.ClearStaleReviewClaims(projectRoot); err != nil {
		GetLogger().Warn("Failed to clear stale review claims before reviewer wait", "error", err)
	} else if cleared > 0 {
		GetLogger().Info("Cleared stale review claims before reviewer wait", "count", cleared)
	}

	return waitForWorkEventDriven(ctx, bb, projectRoot, pollInterval, maxWait,
		func(s *models.State) (bool, string) {
			count := models.CountReviewableTasks(s, models.RoleCodeReviewer)
			logMsg := models.GetReviewerWorkDiagnostics(s)
			return count > 0, logMsg
		})
}

// waitForPlannerWork waits for planner wake triggers using event-driven detection
func waitForPlannerWork(ctx context.Context, bb *db.Blackboard, projectRoot string, pollInterval, maxWait time.Duration) (bool, error) {
	return waitForWorkEventDriven(ctx, bb, projectRoot, pollInterval, maxWait,
		func(s *models.State) (bool, string) {
			result := DetectPlannerWakeTriggers(s)
			if result.Trigger != WakeTriggerNone {
				return true, fmt.Sprintf("Planner wake trigger: %s (count: %d)", result.Trigger, result.Count)
			}
			return false, ""
		})
}

// waitForAuditorWork waits for tasks that need auditing in any phase:
// MERGED tasks needing post-merge audit, READY_FOR_REVIEW needing post-execution audit,
// or READY tasks needing pre-execution audit.
func waitForAuditorWork(ctx context.Context, bb *db.Blackboard, projectRoot string, pollInterval, maxWait time.Duration) (bool, error) {
	return waitForWorkEventDriven(ctx, bb, projectRoot, pollInterval, maxWait,
		func(s *models.State) (bool, string) {
			unaudited := countUnauditedTasks(s)
			if unaudited > 0 {
				return true, fmt.Sprintf("Found %d task(s) awaiting audit", unaudited)
			}
			return false, ""
		})
}

// countUnauditedTasks counts tasks that need auditing in any phase:
// MERGED without post_merge audit, READY_FOR_REVIEW without post_execution audit,
// or READY without pre_execution audit.
func countUnauditedTasks(state *models.State) int {
	// Build per-phase audit set
	audited := make(map[string]map[string]bool, len(state.AuditFindings))
	for _, finding := range state.AuditFindings {
		if audited[finding.TaskID] == nil {
			audited[finding.TaskID] = make(map[string]bool)
		}
		if finding.Phase != "" {
			audited[finding.TaskID][finding.Phase] = true
		}
	}

	count := 0
	for _, task := range state.Tasks {
		switch task.Status {
		case models.TaskStatusMerged:
			phases := audited[task.ID]
			if phases == nil || !phases["post_merge"] {
				count++
			}
		case models.TaskStatusReadyForReview:
			phases := audited[task.ID]
			if phases == nil || !phases["post_execution"] {
				count++
			}
		// Note: READY tasks are intentionally excluded — they have no code
		// to audit yet. Pre-execution spec audits are handled by planner
		// wake triggers, not the auditor busy-loop.
		}
	}
	return count
}
