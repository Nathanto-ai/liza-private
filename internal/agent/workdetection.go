package agent

import (
	"github.com/liza-mas/liza/internal/models"
)

// PlannerWakeTrigger represents what triggered the planner to wake
type PlannerWakeTrigger string

const (
	WakeTriggerInitialPlanning     PlannerWakeTrigger = "INITIAL_PLANNING"
	WakeTriggerBlocked             PlannerWakeTrigger = "BLOCKED_TASKS"
	WakeTriggerIntegrationFailed   PlannerWakeTrigger = "INTEGRATION_FAILED"
	WakeTriggerHypothesisExhausted PlannerWakeTrigger = "HYPOTHESIS_EXHAUSTED"
	WakeTriggerImmediateDiscovery  PlannerWakeTrigger = "IMMEDIATE_DISCOVERY"
	WakeTriggerSprintComplete      PlannerWakeTrigger = "SPRINT_COMPLETE"
	WakeTriggerReplanRequired      PlannerWakeTrigger = "AUDIT_REPLAN_REQUIRED"
	WakeTriggerRemediationNeeded   PlannerWakeTrigger = "AUDIT_REMEDIATION_NEEDED"
	WakeTriggerNone                PlannerWakeTrigger = "NONE"
)

// PlannerWakeResult contains the wake trigger and count
type PlannerWakeResult struct {
	Trigger PlannerWakeTrigger
	Count   int
}

type plannerWakeTriggerSpec struct {
	Trigger     PlannerWakeTrigger
	Description string
	Count       func(state *models.State) int
}

var plannerWakeTriggerSpecs = []plannerWakeTriggerSpec{
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
		Description: "Blocked tasks need planner intervention.",
		Count: func(state *models.State) int {
			return countTasksByStatus(state, models.TaskStatusBlocked)
		},
	},
	{
		Trigger:     WakeTriggerIntegrationFailed,
		Description: "Integration failures need planner intervention.",
		Count: func(state *models.State) int {
			return countTasksByStatus(state, models.TaskStatusIntegrationFailed)
		},
	},
	{
		Trigger:     WakeTriggerHypothesisExhausted,
		Description: "Tasks with repeated coder failures need planner intervention.",
		Count:       countHypothesisExhaustedTasks,
	},
	{
		Trigger:     WakeTriggerImmediateDiscovery,
		Description: "Immediate discoveries need planner triage.",
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
	{
		Trigger:     WakeTriggerSprintComplete,
		Description: "All planned tasks are terminal and the sprint can be closed out.",
		Count: func(state *models.State) int {
			if state.AllPlannedTasksTerminal() {
				return len(state.Sprint.Scope.Planned)
			}
			return 0
		},
	},
}

// DetectPlannerWakeTriggers detects conditions that should wake the planner
// Returns the highest-priority trigger and count of items for that trigger
// Priority order:
// 1. No tasks (initial planning)
// 2. Blocked tasks
// 3. Integration failed
// 4. Hypothesis exhausted (2+ failed_by)
// 5. Immediate discoveries (not yet converted to tasks)
// 6. Audit REPLAN_REQUIRED (unresolved findings requiring plan changes)
// 7. Audit REMEDIATE_WITH_TASK (findings needing new remediation tasks)
// 8. Sprint complete (all planned tasks terminal)
func DetectPlannerWakeTriggers(state *models.State) PlannerWakeResult {
	for _, triggerSpec := range plannerWakeTriggerSpecs {
		if count := triggerSpec.Count(state); count > 0 {
			return PlannerWakeResult{
				Trigger: triggerSpec.Trigger,
				Count:   count,
			}
		}
	}

	return PlannerWakeResult{
		Trigger: WakeTriggerNone,
		Count:   0,
	}
}

func countTasksByStatus(state *models.State, status models.TaskStatus) int {
	count := 0
	for _, task := range state.Tasks {
		if task.Status == status {
			count++
		}
	}
	return count
}

func countHypothesisExhaustedTasks(state *models.State) int {
	count := 0
	for _, task := range state.Tasks {
		if len(task.FailedBy) >= 2 && !task.Status.IsComplete() {
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

// countUnresolvedReplanFindings counts audit findings classified as REPLAN_REQUIRED
// that have not been resolved. These indicate the planner must re-evaluate the plan.
func countUnresolvedReplanFindings(state *models.State) int {
	count := 0
	for _, f := range state.AuditFindings {
		if f.Classification == "REPLAN_REQUIRED" && !f.Resolved {
			count++
		}
	}
	return count
}

// countUnresolvedRemediationFindings counts audit findings classified as
// REMEDIATE_WITH_TASK that have no linked remediation task yet.
// Checks both LinkedTaskID on the finding and OriginFindingID on tasks.
func countUnresolvedRemediationFindings(state *models.State) int {
	// Build set of finding IDs that have a task with matching OriginFindingID
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
