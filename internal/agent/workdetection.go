package agent

import (
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/ops"
)

// OrchestratorWakeTrigger represents what triggered the orchestrator to wake
type OrchestratorWakeTrigger string

const (
	WakeTriggerInitialPlanning     OrchestratorWakeTrigger = "INITIAL_PLANNING"
	WakeTriggerBlocked             OrchestratorWakeTrigger = "BLOCKED_TASKS"
	WakeTriggerIntegrationFailed   OrchestratorWakeTrigger = "INTEGRATION_FAILED"
	WakeTriggerHypothesisExhausted OrchestratorWakeTrigger = "HYPOTHESIS_EXHAUSTED"
	WakeTriggerImmediateDiscovery  OrchestratorWakeTrigger = "IMMEDIATE_DISCOVERY"
	WakeTriggerPlanningComplete    OrchestratorWakeTrigger = "PLANNING_COMPLETE"
	WakeTriggerSprintComplete      OrchestratorWakeTrigger = "SPRINT_COMPLETE"
	WakeTriggerReplanRequired      OrchestratorWakeTrigger = "AUDIT_REPLAN_REQUIRED"
	WakeTriggerRemediationNeeded   OrchestratorWakeTrigger = "AUDIT_REMEDIATION_NEEDED"
	WakeTriggerNone                OrchestratorWakeTrigger = "NONE"
)

// OrchestratorWakeResult contains the wake trigger and count
type OrchestratorWakeResult struct {
	Trigger OrchestratorWakeTrigger
	Count   int
}

// PlannerWakeTrigger is an alias for backward compatibility with private-main code.
type PlannerWakeTrigger = OrchestratorWakeTrigger

// PlannerWakeResult is an alias for backward compatibility with private-main code.
type PlannerWakeResult = OrchestratorWakeResult

// DetectPlannerWakeTriggers is a convenience wrapper around DetectOrchestratorWakeTriggers
// without pipeline-specific parameters.
func DetectPlannerWakeTriggers(state *models.State) PlannerWakeResult {
	return DetectOrchestratorWakeTriggers(state, nil, nil)
}

type orchestratorWakeTriggerSpec struct {
	Trigger     OrchestratorWakeTrigger
	Description string
	Count       func(state *models.State) int
}

var orchestratorWakeTriggerSpecs = []orchestratorWakeTriggerSpec{
	{
		Trigger:     WakeTriggerInitialPlanning,
		Description: "No tasks exist yet, so initial planning is required.",
		Count: func(state *models.State) int {
			if len(state.Tasks) == 0 {
				return 1
			}
			return 0
		},
	},
	{
		Trigger:     WakeTriggerBlocked,
		Description: "Blocked tasks need orchestrator intervention.",
		Count: func(state *models.State) int {
			return countActionableBlockedTasks(state)
		},
	},
	{
		Trigger:     WakeTriggerHypothesisExhausted,
		Description: "Tasks with repeated coder failures need orchestrator intervention.",
		Count:       countHypothesisExhaustedTasks,
	},
	{
		Trigger:     WakeTriggerImmediateDiscovery,
		Description: "Immediate discoveries need orchestrator triage.",
		Count:       countImmediateDiscoveries,
	},
	{
		Trigger:     WakeTriggerReplanRequired,
		Description: "Audit findings with REPLAN_REQUIRED classification need planner intervention.",
		Count:       countUnresolvedReplanFindings,
	},
	{
		Trigger:     WakeTriggerRemediationNeeded,
		Description: "Audit findings with REMEDIATE_WITH_TASK classification need remediation tasks.",
		Count:       countUnresolvedRemediationFindings,
	},
	// WakeTriggerSprintComplete is handled separately in DetectOrchestratorWakeTriggers
	// because it requires pipeline-aware terminal state checking.
}

// DetectOrchestratorWakeTriggers detects conditions that should wake the orchestrator.
// pipelineTerminals provides pipeline-defined sprint-terminal states (from ops.SprintTerminalStates).
// planningPairs provides role-pairs that are transition sources (from ops.TransitionSourcePairs).
// Pass nil for either to use fallback behavior.
//
// Returns the highest-priority trigger and count of items for that trigger.
// Priority order:
// 1. No tasks (initial planning)
// 2. Blocked tasks
// 3. Integration failed
// 4. Hypothesis exhausted (2+ failed_by)
// 5. Immediate discoveries (not yet converted to tasks)
// 6. Audit REPLAN_REQUIRED (unresolved findings requiring plan changes)
// 7. Audit REMEDIATE_WITH_TASK (findings needing new remediation tasks)
// 8. Planning complete (all planned tasks terminal, merged tasks have output[])
// 9. Sprint complete (all planned tasks terminal)
func DetectOrchestratorWakeTriggers(state *models.State, pipelineTerminals []models.TaskStatus, planningPairs map[string]bool) OrchestratorWakeResult {
	for _, triggerSpec := range orchestratorWakeTriggerSpecs {
		if count := triggerSpec.Count(state); count > 0 {
			return OrchestratorWakeResult{
				Trigger: triggerSpec.Trigger,
				Count:   count,
			}
		}
	}

	// Sprint-complete check: pipeline-aware when terminals are provided,
	// falls back to universal terminals when nil.
	// Guard: suppress when sprint is already CHECKPOINT or COMPLETED to prevent
	// the re-wake loop (supervisor sets COMPLETED → state change fires detection
	// → orchestrator wakes → calls sprint_checkpoint → rejected).
	if state.AllPlannedTasksTerminalWith(pipelineTerminals) {
		if state.Sprint.Status == models.SprintStatusCheckpoint ||
			state.Sprint.Status == models.SprintStatusCompleted {
			return OrchestratorWakeResult{Trigger: WakeTriggerNone}
		}
		// Distinguish planning completion (merged tasks with output[]) from sprint completion.
		if n := countMergedPlanningTasksWithOutput(state, planningPairs); n > 0 {
			return OrchestratorWakeResult{
				Trigger: WakeTriggerPlanningComplete,
				Count:   n,
			}
		}
		return OrchestratorWakeResult{
			Trigger: WakeTriggerSprintComplete,
			Count:   len(state.Sprint.Scope.Planned),
		}
	}

	return OrchestratorWakeResult{Trigger: WakeTriggerNone}
}

// countActionableBlockedTasks counts BLOCKED tasks that the orchestrator should
// wake up for. A blocked task is actionable if it has never been assessed, or if
// new activity has occurred since the last assessment (on the task itself, its
// dependencies, or via human notes).
func countActionableBlockedTasks(state *models.State) int {
	count := 0
	for i := range state.Tasks {
		if state.Tasks[i].Status == models.TaskStatusBlocked && isTaskActionableSinceAssessment(&state.Tasks[i], state) {
			count++
		}
	}
	return count
}

// isTaskActionableSinceAssessment determines whether a task should trigger an
// orchestrator wake. Returns true if:
//   - No orchestrator_assessment history entry exists (never triaged)
//   - The task itself has a non-assessment history entry after the last assessment
//   - Any dependency has a non-assessment history entry after the last assessment
//   - A human note targets this task (by ID or "all") after the last assessment
//
// Used by both BLOCKED and HYPOTHESIS_EXHAUSTED wake triggers.
func isTaskActionableSinceAssessment(task *models.Task, state *models.State) bool {
	// Find the last orchestrator_assessment (reverse scan).
	var lastAssessment *models.TaskHistoryEntry
	for i := len(task.History) - 1; i >= 0; i-- {
		if task.History[i].Event == models.TaskEventOrchestratorAssessment {
			lastAssessment = &task.History[i]
			break
		}
	}

	// Never assessed → actionable.
	if lastAssessment == nil {
		return true
	}

	// Check own history for non-assessment activity after last assessment.
	for i := range task.History {
		if task.History[i].Event != models.TaskEventOrchestratorAssessment &&
			task.History[i].Time.After(lastAssessment.Time) {
			return true
		}
	}

	// Check dependency history for non-assessment activity after last assessment.
	for _, depID := range task.DependsOn {
		dep := state.FindTask(depID)
		if dep == nil {
			continue // orphan reference, skip gracefully
		}
		for i := range dep.History {
			if dep.History[i].Event != models.TaskEventOrchestratorAssessment &&
				dep.History[i].Time.After(lastAssessment.Time) {
				return true
			}
		}
	}

	// Check human notes targeting this task (by ID or "all") after last assessment.
	// Design choice: notes on dependency tasks do NOT re-activate this blocked task.
	// Rationale: human targets specific tasks by ID; if they want to wake all
	// dependents, they add notes to each or use for:"all". This avoids surprising
	// cascade wakes when a human annotates a dependency for unrelated reasons.
	for i := range state.HumanNotes {
		note := &state.HumanNotes[i]
		if (note.For == task.ID || note.For == "all") && note.Timestamp.After(lastAssessment.Time) {
			return true
		}
	}

	return false
}

func countHypothesisExhaustedTasks(state *models.State) int {
	count := 0
	for i := range state.Tasks {
		if len(state.Tasks[i].FailedBy) >= 2 && !state.Tasks[i].Status.IsComplete() &&
			isTaskActionableSinceAssessment(&state.Tasks[i], state) {
			count++
		}
	}
	return count
}

func countImmediateDiscoveries(state *models.State) int {
	count := 0
	for _, disc := range state.Discovered {
		if disc.Urgency == "immediate" && disc.ConvertedToTask == nil {
			count++
		}
	}
	return count
}

// countMergedPlanningTasksWithOutput counts planned tasks with unconsumed
// planning output, indicating tasks ready to be expanded into coding tasks.
// Uses the shared predicate ops.IsPlanningCompleteEligible.
func countMergedPlanningTasksWithOutput(state *models.State, planningPairs map[string]bool) int {
	count := 0
	for _, taskID := range state.Sprint.Scope.Planned {
		task := state.FindTask(taskID)
		if ops.IsPlanningCompleteEligible(task, planningPairs, state) {
			count++
		}
	}
	return count
}

// countUnresolvedReplanFindings counts audit findings classified as REPLAN_REQUIRED
// that have not been resolved and have no linked remediation task.
func countUnresolvedReplanFindings(state *models.State) int {
	taskOrigins := make(map[string]bool)
	for _, t := range state.Tasks {
		if t.OriginFindingID != "" {
			taskOrigins[t.OriginFindingID] = true
		}
	}

	count := 0
	for _, f := range state.AuditFindings {
		if f.Classification == "REPLAN_REQUIRED" && !f.Resolved && f.LinkedTaskID == "" && !taskOrigins[f.ID] {
			count++
		}
	}
	return count
}

// countUnresolvedRemediationFindings counts audit findings classified as
// REMEDIATE_WITH_TASK that have no linked remediation task yet.
func countUnresolvedRemediationFindings(state *models.State) int {
	taskOrigins := make(map[string]bool)
	for _, t := range state.Tasks {
		if t.OriginFindingID != "" {
			taskOrigins[t.OriginFindingID] = true
		}
	}

	count := 0
	for _, f := range state.AuditFindings {
		if f.Classification == "REMEDIATE_WITH_TASK" && !f.Resolved && f.LinkedTaskID == "" && !taskOrigins[f.ID] {
			count++
		}
	}
	return count
}
