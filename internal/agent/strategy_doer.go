package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/ops"
	"github.com/liza-mas/liza/internal/pipeline"
)

// doerStrategy handles task-implementing roles: coder, code-planner, epic-planner, us-writer.
type doerStrategy struct {
	role             string             // role name in hyphenated form (e.g. "coder")
	resolver         *pipeline.Resolver // pipeline resolver for context sections
	executionTimeout time.Duration      // from YAML; 0 = use type default
	yamlPollSec      int                // from YAML; 0 = use type default
	yamlMaxWaitSec   int                // from YAML; 0 = use type default
}

const defaultDoerTimeout = 2 * time.Hour

func (s *doerStrategy) DefaultTimeout() time.Duration {
	if s.executionTimeout > 0 {
		return s.executionTimeout
	}
	return defaultDoerTimeout
}

func (s *doerStrategy) WaitConfig(state *models.State) (pollInterval, maxWait time.Duration) {
	poll := nonZeroOr(state.Config.CoderPollInterval, nonZeroOr(s.yamlPollSec, models.DefaultCoderPollInterval))
	max := nonZeroOr(state.Config.CoderMaxWait, nonZeroOr(s.yamlMaxWaitSec, models.DefaultCoderMaxWait))
	return time.Duration(poll) * time.Second, time.Duration(max) * time.Second
}

// ApplyYAMLTimeouts sets YAML-sourced timeout overrides on a strategy.
// Used by RunSupervisor to inject role-specific timeouts from pipeline config.
func ApplyYAMLTimeouts(s RoleStrategy, execution, pollInterval, maxWait time.Duration) {
	execDur := execution
	pollSec := int(pollInterval.Seconds())
	maxWaitSec := int(maxWait.Seconds())

	switch v := s.(type) {
	case *doerStrategy:
		v.executionTimeout = execDur
		v.yamlPollSec = pollSec
		v.yamlMaxWaitSec = maxWaitSec
	case *reviewerStrategy:
		v.executionTimeout = execDur
		v.yamlPollSec = pollSec
		v.yamlMaxWaitSec = maxWaitSec
	case *orchestratorStrategy:
		v.executionTimeout = execDur
		v.yamlPollSec = pollSec
		v.yamlMaxWaitSec = maxWaitSec
	}
}

func (s *doerStrategy) PreWork(_ context.Context, _ *db.Blackboard, _ SupervisorConfig) (bool, error) {
	return false, nil
}

func (s *doerStrategy) WaitForWork(ctx context.Context, bb *db.Blackboard, config SupervisorConfig, pollInterval, maxWait time.Duration) (bool, error) {
	// Clear stale doer claims before checking for work, similar to how
	// waitForCoderWork clears stale coding claims. This prevents tasks from
	// being permanently stuck when an agent process dies.
	if cleared, err := ops.ClearStaleDoerClaims(config.ProjectRoot); err != nil {
		GetLogger().Warn("Failed to clear stale doer claims before wait", "error", err)
	} else if cleared > 0 {
		GetLogger().Info("Cleared stale doer claims before wait", "count", cleared)
	}

	pr := loadResolver(config.ProjectRoot)
	return waitForWorkEventDriven(ctx, bb, config.ProjectRoot, pollInterval, maxWait,
		func(state *models.State) (bool, string) {
			claimable := models.CountClaimableTasks(state, s.role, pr)
			resumableHandoffs := countResumableHandoffTasks(state, config.AgentID, pr)

			logMsg := fmt.Sprintf("%s: %d claimable, %d resumable handoffs", s.role, claimable, resumableHandoffs)

			// Use richer diagnostics for coder role
			if s.role == "coder" {
				logMsg = models.GetCoderWorkDiagnostics(state, pr)
				if resumableHandoffs > 0 {
					handoffMsg := fmt.Sprintf("Found %d resumable handoff task(s) for %s", resumableHandoffs, config.AgentID)
					if logMsg != "" {
						logMsg = handoffMsg + "; " + logMsg
					} else {
						logMsg = handoffMsg
					}
				}
			}

			return claimable > 0 || resumableHandoffs > 0, logMsg
		})
}

func (s *doerStrategy) ClaimTask(config SupervisorConfig, bb *db.Blackboard) (string, string, error) {
	taskID, _, err := claimDoerTask(config.ProjectRoot, config.AgentID, s.role, bb)
	if err != nil {
		return "", "", err
	}
	return taskID, taskID, nil
}

func (s *doerStrategy) PreExecution(_ *db.Blackboard, _ SupervisorConfig) error {
	return nil
}

func (s *doerStrategy) BuildPrompt(state *models.State, config SupervisorConfig, taskID string) (string, error) {
	return buildPromptWithContext(state, config, taskID, s.resolver)
}

func (s *doerStrategy) PostExecution(bb *db.Blackboard, config SupervisorConfig, _ string, claimedTaskID string, _ *models.State) error {
	if claimedTaskID == "" {
		return nil
	}

	pr, err := ops.LoadResolverForModels(config.ProjectRoot)
	if err != nil {
		GetLogger().Warn("Failed to load pipeline resolver — skipping submission log", "error", err)
		return nil
	}

	if err := logTaskSubmissionIfCompleted(bb, claimedTaskID, config.AgentID, pr); err != nil {
		GetLogger().Warn("Failed to log task submission", "error", err, "task_id", claimedTaskID)
	}
	return nil
}
