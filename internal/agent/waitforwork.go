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

// loadResolver loads the pipeline resolver for work detection, logging a warning
// on failure. Returns nil on error (callers treat nil as "no work visible").
func loadResolver(projectRoot string) models.PipelineResolver {
	pr, err := ops.LoadResolverForModels(projectRoot)
	if err != nil {
		GetLogger().Warn("Failed to load pipeline resolver for work detection", "error", err)
	}
	return pr
}

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

	logger.Debug("agent waiting for work", "maxWait", maxWait, "role", role)

	switch role {
	case roles.RuntimeCoder:
		return waitForCoderWork(ctx, bb, projectRoot, config.AgentID, pollInterval, maxWait)
	case roles.RuntimeCodeReviewer:
		return waitForReviewerWork(ctx, bb, projectRoot, config.AgentID, pollInterval, maxWait)
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

	// Add ticker for periodic ABORT checks (file-based fallback).
	// Keep this well below typical maxWait to avoid racing with context deadlines.
	abortTicker := time.NewTicker(1 * time.Second)
	defer abortTicker.Stop()

	// Deadline timer — created once to avoid timer leak in the select loop
	deadlineTimer := time.NewTimer(time.Until(deadline))
	defer deadlineTimer.Stop()

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

func isResumableHandoff(task *models.Task, agentID string, pr models.PipelineResolver) bool {
	return models.IsExecutingStatus(task, pr) &&
		task.HandoffPending &&
		task.AssignedTo != nil &&
		*task.AssignedTo == agentID
}

func countResumableHandoffTasks(state *models.State, agentID string, pr models.PipelineResolver) int {
	count := 0
	for i := range state.Tasks {
		if isResumableHandoff(&state.Tasks[i], agentID, pr) {
			count++
		}
	}
	return count
}

// waitForCoderWork waits for claimable tasks, resumable handoff tasks,
// or tasks already claimed by this agent that are still IMPLEMENTING.
func waitForCoderWork(ctx context.Context, bb *db.Blackboard, projectRoot, agentID string, pollInterval, maxWait time.Duration) (bool, error) {
	if cleared, err := ops.ClearStaleCodingClaims(projectRoot); err != nil {
		GetLogger().Warn("Failed to clear stale coding claims before coder wait", "error", err)
	} else if cleared > 0 {
		GetLogger().Info("Cleared stale coding claims before coder wait", "count", cleared)
	}

	pr := loadResolver(projectRoot)

	return waitForWorkEventDriven(ctx, bb, projectRoot, pollInterval, maxWait,
		func(s *models.State) (bool, string) {
			claimable := models.CountClaimableTasks(s, models.RoleCoder, pr)
			resumableHandoffs := countResumableHandoffTasks(s, agentID, pr)
			ownInProgress := countOwnInProgressTasks(s, agentID)
			logMsg := models.GetCoderWorkDiagnostics(s, pr)

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

// countOwnReviewingTasks counts tasks that are REVIEWING and assigned to this
// reviewer agent. This enables re-invocation when the CLI session exits without
// submitting a verdict, preventing tasks from being stuck in REVIEWING state.
func countOwnReviewingTasks(state *models.State, agentID string) int {
	count := 0
	for i := range state.Tasks {
		task := &state.Tasks[i]
		if task.Status == models.TaskStatusReviewing &&
			task.ReviewingBy != nil &&
			*task.ReviewingBy == agentID {
			count++
		}
	}
	return count
}

// waitForReviewerWork waits for reviewable tasks using event-driven detection.
// Also detects tasks already in REVIEWING state assigned to this reviewer
// (i.e., the previous CLI session ended without submitting a verdict).
func waitForReviewerWork(ctx context.Context, bb *db.Blackboard, projectRoot, agentID string, pollInterval, maxWait time.Duration) (bool, error) {
	if cleared, err := ops.ClearStaleReviewClaims(projectRoot); err != nil {
		GetLogger().Warn("Failed to clear stale review claims before reviewer wait", "error", err)
	} else if cleared > 0 {
		GetLogger().Info("Cleared stale review claims before reviewer wait", "count", cleared)
	}

	return waitForWorkEventDriven(ctx, bb, projectRoot, pollInterval, maxWait,
		func(s *models.State) (bool, string) {
			pr := loadResolver(projectRoot)
			count := models.CountReviewableTasks(s, models.RoleCodeReviewer, pr)
			ownReviewing := countOwnReviewingTasks(s, agentID)
			logMsg := models.GetReviewerWorkDiagnostics(s, pr)

			if ownReviewing > 0 {
				ownMsg := fmt.Sprintf("Found %d task(s) still in REVIEWING assigned to %s (session ended without verdict)", ownReviewing, agentID)
				if logMsg != "" {
					logMsg = ownMsg + "; " + logMsg
				} else {
					logMsg = ownMsg
				}
			}

			return count > 0 || ownReviewing > 0, logMsg
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

// countUnauditedTasks counts MERGED tasks that have not yet received a
// post_merge audit finding. Only MERGED tasks are counted — tasks still in
// READY_FOR_REVIEW have not been reviewed/merged yet and should not trigger
// auditor wakes.
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
		if task.Status == models.TaskStatusMerged {
			phases := audited[task.ID]
			if phases == nil || !phases["post_merge"] {
				count++
			}
		}
	}
	return count
}
