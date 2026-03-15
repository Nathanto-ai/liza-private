package prompts

import (
	"fmt"

	"github.com/liza-mas/liza/internal/models"
)

// BasePromptConfig contains configuration for building the base prompt
type BasePromptConfig struct {
	Role        string
	AgentID     string
	SpecsDir    string
	ProjectRoot string
	StatePath   string
	GoalDesc    string
	GoalSpecRef string
}

// PlannerContextConfig contains configuration for building planner context
type PlannerContextConfig struct {
}

// CoderContextConfig contains configuration for building coder context
type CoderContextConfig struct {
	ProjectRoot       string
	AgentID           string
	IntegrationBranch string
	HandoffNote       *models.HandoffNote
}

// ReviewerContextConfig contains configuration for building reviewer context
type ReviewerContextConfig struct {
	ProjectRoot string
	AgentID     string
}

// AuditorContextConfig contains configuration for building auditor context
type AuditorContextConfig struct {
	ProjectRoot       string
	AgentID           string
	IntegrationBranch string
}

// BuildBasePrompt creates the base bootstrap prompt for all agents
func BuildBasePrompt(config BasePromptConfig) (string, error) {
	return executeTemplate("base_prompt", config)
}

// plannerContextData is the template data for planner_context.tmpl
type plannerContextData struct {
	WakeTrigger           string
	SprintNumber          int
	SprintHistory         []models.SprintSummary
	TotalTasks            int
	Merged                int
	InProgress            int
	Unclaimed             int
	Blocked               int
	IntegrationFailed     int
	HypothesisExhausted   int
	ImmediateDiscoveries  int
	WakeInstructions      string
	AuditFindings         []models.AuditFinding
	UnresolvedRemediation int
	UnresolvedReplan      int
}

// BuildPlannerContext creates planner-specific context with sprint state
func BuildPlannerContext(state *models.State, config PlannerContextConfig) (string, error) {
	totalTasks := len(state.Tasks)
	merged := countTasksByStatus(state.Tasks, models.TaskStatusMerged)
	blocked := countTasksByStatus(state.Tasks, models.TaskStatusBlocked)
	integrationFailed := countTasksByStatus(state.Tasks, models.TaskStatusIntegrationFailed)
	unclaimed := countTasksByStatus(state.Tasks, models.TaskStatusReady)

	inProgress := countTasksByStatus(state.Tasks, models.TaskStatusImplementing) +
		countTasksByStatus(state.Tasks, models.TaskStatusReadyForReview) +
		countTasksByStatus(state.Tasks, models.TaskStatusApproved)

	hypothesisExhausted := 0
	for _, task := range state.Tasks {
		if len(task.FailedBy) >= 2 && !task.Status.IsComplete() {
			hypothesisExhausted++
		}
	}

	immediateDiscoveries := 0
	for _, disc := range state.Discovered {
		if disc.Urgency == "immediate" && disc.ConvertedToTask == nil {
			immediateDiscoveries++
		}
	}

	// Count unresolved audit findings
	// Also check task OriginFindingID as fallback for finding linkage
	taskOrigins := make(map[string]bool)
	for _, t := range state.Tasks {
		if t.OriginFindingID != "" {
			taskOrigins[t.OriginFindingID] = true
		}
	}

	unresolvedRemediation := 0
	unresolvedReplan := 0
	for _, f := range state.AuditFindings {
		if f.Resolved {
			continue
		}
		switch f.Classification {
		case "REMEDIATE_WITH_TASK":
			if f.LinkedTaskID == "" && !taskOrigins[f.ID] {
				unresolvedRemediation++
			}
		case "REPLAN_REQUIRED":
			unresolvedReplan++
		}
	}

	wakeTrigger := determineWakeTrigger(totalTasks, blocked, integrationFailed, hypothesisExhausted, immediateDiscoveries, unresolvedReplan, unresolvedRemediation, state.AllPlannedTasksTerminal())

	wakeInstructions, err := buildInstructionsForWakeTrigger(wakeTrigger, state.Goal.SpecRef, state)
	if err != nil {
		return "", fmt.Errorf("building wake instructions: %w", err)
	}

	data := plannerContextData{
		WakeTrigger:           wakeTrigger,
		SprintNumber:          state.Sprint.Number,
		SprintHistory:         state.SprintHistory,
		TotalTasks:            totalTasks,
		Merged:                merged,
		InProgress:            inProgress,
		Unclaimed:             unclaimed,
		Blocked:               blocked,
		IntegrationFailed:     integrationFailed,
		HypothesisExhausted:   hypothesisExhausted,
		ImmediateDiscoveries:  immediateDiscoveries,
		WakeInstructions:      wakeInstructions,
		AuditFindings:         state.AuditFindings,
		UnresolvedRemediation: unresolvedRemediation,
		UnresolvedReplan:      unresolvedReplan,
	}
	return executeTemplate("planner_context", data)
}

// coderContextData is the template data for coder_context.tmpl
type coderContextData struct {
	Task              *models.Task
	Config            CoderContextConfig
	WorktreePath      string
	HasPriorRejection bool
}

// BuildCoderContext creates coder-specific context with task details
func BuildCoderContext(task *models.Task, config CoderContextConfig) (string, error) {
	worktreePath := ""
	if task.Worktree != nil {
		worktreePath = fmt.Sprintf("%s/%s", config.ProjectRoot, *task.Worktree)
	}

	data := coderContextData{
		Task:              task,
		Config:            config,
		WorktreePath:      worktreePath,
		HasPriorRejection: hasPriorRejection(task),
	}
	return executeTemplate("coder_context", data)
}

// reviewerContextData is the template data for reviewer_context.tmpl
type reviewerContextData struct {
	Task              *models.Task
	Config            ReviewerContextConfig
	WorktreePath      string
	BaseCommit        string
	ReviewCommit      string
	AssignedTo        string
	HasPriorRejection bool
}

// BuildReviewerContext creates reviewer-specific context with review details
func BuildReviewerContext(task *models.Task, config ReviewerContextConfig) (string, error) {
	worktreePath := ""
	if task.Worktree != nil {
		worktreePath = fmt.Sprintf("%s/%s", config.ProjectRoot, *task.Worktree)
	}

	data := reviewerContextData{
		Task:              task,
		Config:            config,
		WorktreePath:      worktreePath,
		BaseCommit:        derefString(task.BaseCommit),
		ReviewCommit:      derefString(task.ReviewCommit),
		AssignedTo:        derefString(task.AssignedTo),
		HasPriorRejection: hasPriorRejection(task),
	}
	return executeTemplate("reviewer_context", data)
}

// auditorContextData is the template data for auditor_context.tmpl
type auditorContextData struct {
	Config         AuditorContextConfig
	UnauditedTasks []models.Task
	AuditFindings  []models.AuditFinding
	TotalMerged    int
	TotalTasks     int
}

// BuildAuditorContext creates auditor-specific context with merged tasks to audit
func BuildAuditorContext(state *models.State, config AuditorContextConfig) (string, error) {
	// Build set of task IDs that already have audit findings
	audited := make(map[string]bool, len(state.AuditFindings))
	for _, finding := range state.AuditFindings {
		audited[finding.TaskID] = true
	}

	var unaudited []models.Task
	merged := 0
	for _, task := range state.Tasks {
		if task.Status == models.TaskStatusMerged {
			merged++
			if !audited[task.ID] {
				unaudited = append(unaudited, task)
			}
		}
	}

	data := auditorContextData{
		Config:         config,
		UnauditedTasks: unaudited,
		AuditFindings:  state.AuditFindings,
		TotalMerged:    merged,
		TotalTasks:     len(state.Tasks),
	}
	return executeTemplate("auditor_context", data)
}

// derefString returns the value pointed to by s, or "" if s is nil.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// hasPriorRejection reports whether the task has actionable rejection feedback from a prior iteration
func hasPriorRejection(task *models.Task) bool {
	return task.Iteration > 1 && task.RejectionReason != nil && *task.RejectionReason != "" && *task.RejectionReason != "null"
}

// countTasksByStatus counts tasks with a specific status
func countTasksByStatus(tasks []models.Task, status models.TaskStatus) int {
	count := 0
	for _, task := range tasks {
		if task.Status == status {
			count++
		}
	}
	return count
}

// determineWakeTrigger determines what triggered the planner to wake
func determineWakeTrigger(totalTasks, blocked, integrationFailed, hypothesisExhausted, immediateDiscoveries, unresolvedReplan, unresolvedRemediation int, sprintComplete bool) string {
	if totalTasks == 0 {
		return "INITIAL_PLANNING"
	}
	if blocked > 0 {
		return "BLOCKED_TASKS"
	}
	if integrationFailed > 0 {
		return "INTEGRATION_FAILED"
	}
	if hypothesisExhausted > 0 {
		return "HYPOTHESIS_EXHAUSTED"
	}
	if immediateDiscoveries > 0 {
		return "IMMEDIATE_DISCOVERY"
	}
	if unresolvedReplan > 0 {
		return "AUDIT_REPLAN_REQUIRED"
	}
	if unresolvedRemediation > 0 {
		return "AUDIT_REMEDIATION_NEEDED"
	}
	if sprintComplete {
		return "SPRINT_COMPLETE"
	}
	return "UNKNOWN"
}

// wakeTemplateData is used by wake trigger templates that need GoalSpecRef
type wakeTemplateData struct {
	GoalSpecRef string
}

// blockedTaskInfo holds summary data for a blocked task, used in the wake_blocked_tasks template.
type blockedTaskInfo struct {
	ID            string
	Description   string
	BlockedReason string
	Worktree      string
	AssignedTo    string
}

// wakeBlockedData is the template data for wake_blocked_tasks.tmpl
type wakeBlockedData struct {
	Tasks []blockedTaskInfo
}

// buildInstructionsForWakeTrigger returns trigger-specific instructions
func buildInstructionsForWakeTrigger(wakeTrigger, goalSpecRef string, state *models.State) (string, error) {
	switch wakeTrigger {
	case "INITIAL_PLANNING":
		return executeTemplate("wake_initial_planning", wakeTemplateData{GoalSpecRef: goalSpecRef})
	case "BLOCKED_TASKS":
		data := buildBlockedTaskData(state)
		return executeTemplate("wake_blocked_tasks", data)
	case "INTEGRATION_FAILED":
		return executeTemplate("wake_integration_failed", nil)
	case "HYPOTHESIS_EXHAUSTED":
		return executeTemplate("wake_hypothesis_exhausted", nil)
	case "IMMEDIATE_DISCOVERY":
		return executeTemplate("wake_immediate_discovery", nil)
	case "AUDIT_REPLAN_REQUIRED":
		return executeTemplate("wake_audit_replan", nil)
	case "AUDIT_REMEDIATION_NEEDED":
		return executeTemplate("wake_audit_remediation", nil)
	case "SPRINT_COMPLETE":
		return executeTemplate("wake_sprint_complete", nil)
	default:
		return "", nil
	}
}

// buildBlockedTaskData extracts blocked task info from state for the planner wake template.
func buildBlockedTaskData(state *models.State) wakeBlockedData {
	var tasks []blockedTaskInfo
	if state != nil {
		for _, t := range state.Tasks {
			if t.Status != models.TaskStatusBlocked {
				continue
			}
			info := blockedTaskInfo{
				ID:          t.ID,
				Description: t.Description,
			}
			if t.BlockedReason != nil {
				info.BlockedReason = *t.BlockedReason
			}
			if t.Worktree != nil {
				info.Worktree = *t.Worktree
			}
			if t.AssignedTo != nil {
				info.AssignedTo = *t.AssignedTo
			}
			tasks = append(tasks, info)
		}
	}
	return wakeBlockedData{Tasks: tasks}
}
