package agent

import (
	"errors"
	"fmt"
	"math/rand/v2"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/ops"
	"github.com/liza-mas/liza/internal/pipeline"
	"github.com/liza-mas/liza/internal/roles"
)

func claimDoerTask(projectRoot, agentID, role string, bb *db.Blackboard) (taskID, worktree string, err error) {
	logger := GetLogger()

	if role == models.RoleCoder || role == models.RoleCodePlanner {
		handoffResult, err := ops.ResumeHandoff(ops.ResumeHandoffInput{
			ProjectRoot: projectRoot,
			AgentID:     agentID,
		})
		if err != nil {
			return "", "", err
		}
		if handoffResult.Found {
			logger.Info("Resuming claimed task from handoff", "task_id", handoffResult.TaskID, "agent_id", agentID)
			return handoffResult.TaskID, handoffResult.Worktree, nil
		}
	}

	state, err := bb.Read()
	if err != nil {
		return "", "", fmt.Errorf("failed to read state: %w", err)
	}

	pr := loadResolver(projectRoot)

	// Check for tasks already IMPLEMENTING and assigned to this agent.
	// This handles re-invocation when the CLI exited without completing.
	for i := range state.Tasks {
		task := &state.Tasks[i]
		if task.Status == models.TaskStatusImplementing &&
			task.AssignedTo != nil &&
			*task.AssignedTo == agentID {
			wt := ""
			if task.Worktree != nil {
				wt = *task.Worktree
			}
			logger.Info("Re-claiming own in-progress task", "task_id", task.ID, "agent_id", agentID)
			return task.ID, wt, nil
		}
	}

	var candidates []*models.Task
	for i := range state.Tasks {
		if state.Tasks[i].IsClaimable(role, state.Tasks, pr) {
			candidates = append(candidates, &state.Tasks[i])
		}
	}
	tier := shuffledByPriorityTier(candidates)

	if len(tier) == 0 {
		return "", "", fmt.Errorf("no claimable tasks found")
	}

	// Try each candidate in the shuffled tier until one succeeds.
	var lastErr error
	for _, task := range tier {
		result, claimErr := ops.ClaimTask(projectRoot, task.ID, agentID)
		if claimErr != nil {
			logger.Warn("Claim attempt failed, trying next candidate",
				"task_id", task.ID, "error", claimErr)
			lastErr = claimErr
			continue
		}
		return result.TaskID, result.WorktreeRel, nil
	}

	return "", "", fmt.Errorf("all %d candidates in top priority tier failed to claim: %w", len(tier), lastErr)
}

// claimCoderTask wraps claimDoerTask for backward compatibility.
func claimCoderTask(projectRoot, agentID string, bb *db.Blackboard) (taskID, worktree string, err error) {
	return claimDoerTask(projectRoot, agentID, models.RoleCoder, bb)
}

// shuffledByPriorityTier returns candidates in the highest-priority tier,
// shuffled randomly. This prevents multiple agents from deterministically
// converging on the same task.
func shuffledByPriorityTier(candidates []*models.Task) []*models.Task {
	tier := models.TopPriorityTier(candidates)
	rand.Shuffle(len(tier), func(i, j int) {
		tier[i], tier[j] = tier[j], tier[i]
	})
	return tier
}

func claimReviewerTaskForRole(projectRoot, agentID, role string, leaseDuration int, bb *db.Blackboard) (taskID, worktree, reviewCommit string, err error) {
	logger := GetLogger()

	// Fix 42: Check for tasks already REVIEWING and assigned to this reviewer.
	// This handles re-invocation when the CLI session exited without submitting a verdict.
	state, err := bb.Read()
	if err != nil {
		return "", "", "", fmt.Errorf("failed to read state: %w", err)
	}
	for i := range state.Tasks {
		task := &state.Tasks[i]
		if task.Status == models.TaskStatusReviewing &&
			task.ReviewingBy != nil &&
			*task.ReviewingBy == agentID {
			wt := ""
			if task.Worktree != nil {
				wt = *task.Worktree
			}
			rc := ""
			if task.ReviewCommit != nil {
				rc = *task.ReviewCommit
			}
			logger.Info("Re-claiming own in-review task (session ended without verdict)",
				"task_id", task.ID, "agent_id", agentID)
			return task.ID, wt, rc, nil
		}
	}

	result, err := ops.ClaimReviewerTask(ops.ClaimReviewerTaskInput{
		ProjectRoot:   projectRoot,
		AgentID:       agentID,
		Role:          role,
		LeaseDuration: leaseDuration,
	})
	if err != nil {
		logger.Error("Review claim error", "error", err)
		return "", "", "", err
	}

	return result.TaskID, result.Worktree, result.ReviewCommit, nil
}

// claimReviewerTask wraps claimReviewerTaskForRole for backward compatibility.
func claimReviewerTask(projectRoot, agentID string, leaseDuration int, bb *db.Blackboard) (taskID, worktree, reviewCommit string, err error) {
	return claimReviewerTaskForRole(projectRoot, agentID, models.RoleCodeReviewer, leaseDuration, bb)
}

// releaseReviewerClaimQuietly releases a reviewer claim, logging but not
// propagating errors. Used in supervisor recovery paths for transient failures
// where blockReviewerTask was NOT called.
func releaseReviewerClaimQuietly(projectRoot, taskID, agentID string) {
	_, err := ops.ReleaseClaim(projectRoot, taskID, roles.ClaimReviewer, true, "supervisor: worktree check transient failure", agentID)
	if err != nil {
		GetLogger().Warn("Failed to release reviewer claim during recovery",
			"task_id", taskID, "error", err)
	}
}

// mergeGateInput holds the inputs for the merge gate evaluation.
type mergeGateInput struct {
	task              *models.Task
	agents            map[string]models.Agent
	effectiveQuorum   int
	providerDiversity string // "preferred" or ""
	reviewerRole      string // workflow role name for reviewers in this role-pair
}

// mergeGateResult holds the outcome of the merge gate evaluation.
type mergeGateResult struct {
	proceed    bool
	extra      map[string]any // diversity fields for merge history
	skipReason string         // non-empty when proceed is false
}

// evaluateMergeGate checks quorum defense-in-depth and evaluates provider diversity.
// Pure function — all inputs are passed explicitly for testability.
func evaluateMergeGate(input mergeGateInput) *mergeGateResult {
	result := &mergeGateResult{proceed: true}

	// Defense-in-depth: quorum check
	if input.task.ApprovalCount() < input.effectiveQuorum {
		result.proceed = false
		result.skipReason = fmt.Sprintf("approval count %d < effective quorum %d",
			input.task.ApprovalCount(), input.effectiveQuorum)
		return result
	}

	// No diversity evaluation when not configured
	if input.providerDiversity != "preferred" {
		return result
	}

	// Diversity achieved — approvals come from different providers
	if input.task.HasProviderDiversity() {
		result.extra = map[string]any{"diversity_achieved": true}
		return result
	}

	// Diversity not achieved — check if it's achievable in the reviewer pool
	providers := make(map[string]bool)
	for _, agent := range input.agents {
		if agent.Role == input.reviewerRole {
			providers[agent.Provider] = true
		}
	}

	if len(providers) <= 1 {
		// All reviewers share one provider (or no reviewers registered)
		reason := "no reviewer agents registered"
		for p := range providers {
			reason = fmt.Sprintf("all reviewers use provider %s", p)
		}
		result.extra = map[string]any{
			"diversity_not_achievable": true,
			"reason":                   reason,
		}
	} else {
		// Different providers exist but diversity wasn't achieved in approvals
		result.extra = map[string]any{"diversity_not_met": true}
	}

	return result
}

func handleApprovedMerges(projectRoot, agentID string, bb *db.Blackboard, pr models.PipelineResolver) error {
	logger := GetLogger()
	state, err := bb.Read()
	if err != nil {
		return err
	}

	// Load the concrete resolver once for quorum and diversity lookups.
	cfg, cfgErr := pipeline.LoadFrozen(projectRoot)
	if cfgErr != nil {
		return fmt.Errorf("failed to load pipeline config: %w", cfgErr)
	}
	resolver := pipeline.NewResolver(cfg)

	for i := range state.Tasks {
		task := &state.Tasks[i]
		if models.IsApprovedForMerge(task, pr) &&
			task.LastApprover() == agentID &&
			task.MergeCommit == nil {

			// Resolve effective impact and quorum for merge gate
			effectiveImpact := ops.ResolveEffectiveImpact(task.History)
			effectiveQuorum, qErr := resolver.EffectiveQuorum(task.RolePair, effectiveImpact)
			if qErr != nil {
				logger.Warn("Failed to resolve quorum, skipping merge",
					"task_id", task.ID, "error", qErr)
				continue
			}

			// Get provider diversity config for this impact level
			diversity, dErr := resolver.ProviderDiversity(task.RolePair, effectiveImpact)
			if dErr != nil {
				logger.Warn("Failed to resolve diversity policy, skipping merge",
					"task_id", task.ID, "error", dErr)
				continue
			}

			// Get reviewer role for this role-pair
			reviewerRole, rErr := pr.ReviewerRole(task.RolePair)
			if rErr != nil {
				logger.Warn("Failed to resolve reviewer role, skipping merge",
					"task_id", task.ID, "error", rErr)
				continue
			}

			// Evaluate merge gate: quorum defense-in-depth + provider diversity
			gate := evaluateMergeGate(mergeGateInput{
				task:              task,
				agents:            state.Agents,
				effectiveQuorum:   effectiveQuorum,
				providerDiversity: diversity,
				reviewerRole:      reviewerRole,
			})

			if !gate.proceed {
				logger.Warn("Merge gate: quorum defense-in-depth failed, skipping merge",
					"task_id", task.ID, "reason", gate.skipReason)
				continue
			}

			logger.Info("Merging approved task", "task_id", task.ID)

			result, err := ops.MergeWorktree(projectRoot, task.ID, agentID, gate.extra)
			if err != nil {
				var integrationErr *ops.IntegrationFailedError
				if errors.As(err, &integrationErr) {
					logArgs := []any{
						"task_id", task.ID,
						"reason", integrationErr.Reason,
					}
					if integrationErr.TestOutput != "" {
						logArgs = append(logArgs, "test_output", integrationErr.TestOutput)
					}
					if integrationErr.RollbackError != nil {
						logArgs = append(logArgs, "rollback_error", integrationErr.RollbackError)
					}
					logger.Warn("Integration failed", logArgs...)
					continue
				}
				logger.Warn("Failed to merge task, will retry",
					"task_id", task.ID,
					"error", err)
				continue
			}

			for _, w := range result.Warnings {
				logger.Warn("Merge cleanup warning", "task_id", task.ID, "warning", w)
			}

			logger.Info("Successfully merged task", "task_id", task.ID)
		}
	}

	return nil
}

func hasPendingMerges(bb *db.Blackboard, agentID string, pr models.PipelineResolver) bool {
	state, err := bb.ReadCached()
	if err != nil {
		return false // Safe default: proceed to normal wait
	}

	for i := range state.Tasks {
		task := &state.Tasks[i]
		if models.IsApprovedForMerge(task, pr) &&
			task.LastApprover() == agentID {
			return true
		}
	}
	return false
}

// handleAvailableTransitions creates child tasks from pipeline transitions
// and adds them to the current sprint's scope.
// Called from orchestrator PreWork after checkpoint acknowledgment.
func handleAvailableTransitions(projectRoot string) error {
	results, err := ops.ExecuteAvailableTransitions(projectRoot)
	if err != nil {
		return err
	}

	logger := GetLogger()
	for _, r := range results {
		logger.Info("Pipeline transition executed",
			"source_task", r.SourceTaskID,
			"transition", r.TransitionName,
			"children_created", len(r.ChildTaskIDs))
	}

	return nil
}

func logTaskSubmissionIfCompleted(bb *db.Blackboard, taskID, agentID string, pr models.PipelineResolver) error {
	state, err := bb.Read()
	if err != nil {
		return fmt.Errorf("failed to read state: %w", err)
	}

	if task := state.FindTask(taskID); task != nil {
		if models.IsSubmittedStatus(task, pr) {
			reviewCommit := "unknown"
			if task.ReviewCommit != nil {
				reviewCommit = *task.ReviewCommit
			}

			GetLogger().Info("Task submitted for review",
				"task_id", task.ID,
				"review_commit", reviewCommit,
				"agent_id", agentID,
				"integration_fix", task.IntegrationFix)

			return nil
		}

		if models.IsExecutingStatus(task, pr) {
			GetLogger().Warn("Agent exited with task still claimed",
				"task_id", task.ID,
				"agent_id", agentID)

			releaseErr := bb.Modify(func(s *models.State) error {
				t := s.FindTask(taskID)
				if t == nil || t.Status != models.TaskStatusImplementing {
					return nil // task gone or status changed, nothing to do
				}
				if err := t.Transition(models.TaskStatusReady); err != nil {
					return err
				}
				t.AssignedTo = nil
				t.LeaseExpires = nil
				return nil
			})
			if releaseErr != nil {
				GetLogger().Warn("Failed to release stale coding claim on exit",
					"task_id", task.ID, "error", releaseErr)
			}
			return nil
		}

		if task.Status == models.TaskStatusBlocked {
			GetLogger().Info("Agent blocked task due to dependency issue",
				"task_id", task.ID,
				"agent_id", agentID)
			return nil
		}

		return nil
	}

	return nil
}
