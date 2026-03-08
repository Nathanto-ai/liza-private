package commands

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
)

// StatusOptions contains options for the status command
type StatusOptions struct {
	Format      string // "dashboard", "json", "yaml"
	Detailed    bool   // Include anomalies and circuit breaker
	ProjectRoot string
}

// statusData contains all status information
type statusData struct {
	Goal           goalStatus            `json:"goal"`
	Sprint         sprintStatus          `json:"sprint"`
	Config         configStatus          `json:"config"`
	Tasks          taskStatus            `json:"tasks"`
	Agents         []agentStatus         `json:"agents"`
	PlannerState   plannerStatus         `json:"planner_state"`
	WorkQueues     workQueuesStatus      `json:"work_queues"`
	Anomalies      *[]string             `json:"anomalies,omitempty"`
	CircuitBreaker *circuitBreakerStatus `json:"circuit_breaker,omitempty"`
}

type goalStatus struct {
	Description string `json:"description"`
	Status      string `json:"status"`
	SpecRef     string `json:"spec_ref"`
}

type sprintStatus struct {
	ID         string `json:"id"`
	Number     int    `json:"number"`
	Status     string `json:"status"`
	StartTime  string `json:"start_time"`
	TasksDone  int    `json:"tasks_done"`
	TasksTotal int    `json:"tasks_total"`
}

type configStatus struct {
	Mode        string  `json:"mode"`
	PausedBy    *string `json:"paused_by,omitempty"`
	PauseReason *string `json:"pause_reason,omitempty"`
}

type taskStatus struct {
	Total         int            `json:"total"`
	Active        int            `json:"active"`
	Terminal      int            `json:"terminal"`
	ByStatus      map[string]int `json:"by_status"`
	Claimable     int            `json:"claimable"`
	Reviewable    int            `json:"reviewable"`
	BlockedByDeps int            `json:"blocked_by_deps"`
}

type agentStatus struct {
	ID                 string `json:"id"`
	Role               string `json:"role"`
	Status             string `json:"status"`
	PID                int    `json:"pid"`
	CurrentTask        string `json:"current_task"`
	TimeSinceHeartbeat string `json:"time_since_heartbeat"`
	ProcessStatus      string `json:"process_status"`
}

type plannerStatus struct {
	Trigger      string `json:"trigger"`
	TriggerCount int    `json:"trigger_count"`
	Reason       string `json:"reason"`
}

type workQueuesStatus struct {
	Coder    queueStatus `json:"coder"`
	Reviewer queueStatus `json:"reviewer"`
}

type queueStatus struct {
	Available int    `json:"available"`
	Reason    string `json:"reason"`
}

type circuitBreakerStatus struct {
	Status   string   `json:"status"`
	Triggers []string `json:"triggers,omitempty"`
}

// StatusCommand returns a comprehensive system status
func StatusCommand(opts StatusOptions) (string, error) {
	// Setup paths
	statePath := paths.New(opts.ProjectRoot).StatePath()

	// Read state
	bb := db.For(statePath)
	state, err := bb.Read()
	if err != nil {
		return "", fmt.Errorf("failed to read state: %w", err)
	}

	// Build status data
	status := buildStatusData(state, opts.Detailed)

	// Format output
	switch opts.Format {
	case "json":
		return formatJSON(status)
	case "yaml":
		return formatYAML(status)
	default: // "dashboard" or empty
		return formatStatusDashboard(status)
	}
}

// buildStatusData populates the statusData structure from state
func buildStatusData(state *models.State, detailed bool) statusData {
	data := statusData{}

	// Populate goal information
	data.Goal = goalStatus{
		Description: state.Goal.Description,
		Status:      string(state.Goal.Status),
		SpecRef:     state.Goal.SpecRef,
	}

	// Populate sprint information
	data.Sprint = sprintStatus{
		ID:         state.Sprint.ID,
		Number:     state.Sprint.Number,
		Status:     string(state.Sprint.Status),
		StartTime:  state.Sprint.Timeline.Started.Format(time.RFC3339),
		TasksDone:  state.Sprint.Metrics.TasksDone,
		TasksTotal: len(state.Tasks),
	}

	// Populate config/system mode
	data.Config = configStatus{
		Mode: string(state.Config.Mode),
	}
	if state.Config.Mode == models.SystemModePaused {
		data.Config.PausedBy = state.Config.ModeChangedBy
	}

	// Populate task statistics
	data.Tasks = buildTaskStatus(state)

	// Populate agent information
	data.Agents = buildAgentStatuses(state)

	// Populate planner state
	data.PlannerState = buildPlannerStatus(state)

	// Populate work queues
	data.WorkQueues = buildWorkQueuesStatus(state, data.Tasks.Claimable, data.Tasks.Reviewable)

	// Optionally include detailed information
	if detailed {
		if len(state.Anomalies) > 0 {
			anomalies := make([]string, len(state.Anomalies))
			for i, anomaly := range state.Anomalies {
				anomalies[i] = fmt.Sprintf("[%s] %s by %s: %s",
					anomaly.Timestamp.Format("2006-01-02 15:04"),
					anomaly.Type,
					anomaly.Reporter,
					anomaly.Task)
			}
			data.Anomalies = &anomalies
		}

		if state.CircuitBreaker.Status != "" && state.CircuitBreaker.Status != "OK" {
			cb := &circuitBreakerStatus{
				Status: state.CircuitBreaker.Status,
			}
			if state.CircuitBreaker.CurrentTrigger != nil {
				cb.Triggers = []string{
					fmt.Sprintf("%s (severity: %s)",
						state.CircuitBreaker.CurrentTrigger.Pattern,
						state.CircuitBreaker.CurrentTrigger.Severity),
				}
			}
			data.CircuitBreaker = cb
		}
	}

	return data
}

// buildTaskStatus calculates task statistics
func buildTaskStatus(state *models.State) taskStatus {
	ts := taskStatus{
		Total:    len(state.Tasks),
		ByStatus: make(map[string]int),
	}

	// Build merged task IDs for dependency checking
	mergedIDs := make(map[string]bool)
	for _, task := range state.Tasks {
		if task.Status == models.TaskStatusMerged {
			mergedIDs[task.ID] = true
		}
	}

	// Count tasks by status and categories
	for _, task := range state.Tasks {
		// Count by status
		ts.ByStatus[string(task.Status)]++

		// Count active vs complete
		if task.Status.IsComplete() {
			ts.Terminal++
		} else {
			ts.Active++
		}

		// Check if blocked by dependencies
		if task.Status == models.TaskStatusReady ||
			task.Status == models.TaskStatusRejected ||
			task.Status == models.TaskStatusIntegrationFailed {
			hasUnsatisfiedDeps := false
			for _, depID := range task.DependsOn {
				if !mergedIDs[depID] {
					hasUnsatisfiedDeps = true
					break
				}
			}
			if hasUnsatisfiedDeps {
				ts.BlockedByDeps++
			}
		}
	}

	// Count work availability
	ts.Claimable = models.CountClaimableTasks(state, models.RoleCoder)
	ts.Reviewable = models.CountReviewableTasks(state, models.RoleCodeReviewer)

	return ts
}

// buildAgentStatuses converts agent map to agent status list
func buildAgentStatuses(state *models.State) []agentStatus {
	agents := make([]agentStatus, 0, len(state.Agents))
	now := time.Now().UTC()

	for id, agent := range state.Agents {
		as := agentStatus{
			ID:          id,
			Role:        agent.Role,
			Status:      string(agent.Status),
			CurrentTask: "",
		}

		if agent.CurrentTask != nil {
			as.CurrentTask = *agent.CurrentTask
		}

		// Calculate time since heartbeat
		timeSince := now.Sub(agent.Heartbeat)
		as.TimeSinceHeartbeat = formatDuration(timeSince)

		// Check process status
		as.ProcessStatus = getProcessStatus(agent.PID)
		as.PID = agent.PID

		agents = append(agents, as)
	}

	return agents
}

// buildPlannerStatus determines planner state
func buildPlannerStatus(state *models.State) plannerStatus {
	trigger, count := detectPlannerWakeTriggers(state)

	ps := plannerStatus{
		Trigger:      trigger,
		TriggerCount: count,
	}

	// Build human-readable reason
	switch trigger {
	case "INITIAL_PLANNING":
		ps.Reason = "No tasks exist; initial planning needed"
	case "BLOCKED_TASKS":
		ps.Reason = fmt.Sprintf("%d task(s) are blocked and need attention", count)
		if state.SprintStalled() {
			ps.Reason += " (\u26a0\ufe0f sprint stalled \u2014 all non-terminal tasks blocked)"
		}
	case "INTEGRATION_FAILED":
		ps.Reason = fmt.Sprintf("%d task(s) failed integration", count)
	case "HYPOTHESIS_EXHAUSTED":
		ps.Reason = fmt.Sprintf("%d task(s) exhausted hypotheses (2+ failures)", count)
	case "IMMEDIATE_DISCOVERY":
		ps.Reason = fmt.Sprintf("%d immediate discovery(ies) need to be converted to tasks", count)
	case "SPRINT_COMPLETE":
		ps.Reason = fmt.Sprintf("All %d planned task(s) reached terminal state; sprint complete", count)
	case "NONE":
		ps.Reason = "No triggers; planner is idle"
	default:
		ps.Reason = "Unknown trigger"
	}

	return ps
}

// detectPlannerWakeTriggers detects conditions that should wake the planner
func detectPlannerWakeTriggers(state *models.State) (trigger string, count int) {
	if len(state.Tasks) == 0 {
		return "INITIAL_PLANNING", 1
	}

	var blocked, integrationFailed, hypothesisExhausted int
	for _, task := range state.Tasks {
		switch task.Status {
		case models.TaskStatusBlocked:
			blocked++
		case models.TaskStatusIntegrationFailed:
			integrationFailed++
		}
		if len(task.FailedBy) >= 2 && !task.Status.IsComplete() {
			hypothesisExhausted++
		}
	}

	// Return in priority order
	switch {
	case blocked > 0:
		return "BLOCKED_TASKS", blocked
	case integrationFailed > 0:
		return "INTEGRATION_FAILED", integrationFailed
	case hypothesisExhausted > 0:
		return "HYPOTHESIS_EXHAUSTED", hypothesisExhausted
	}

	immediateDiscoveries := 0
	for _, disc := range state.Discovered {
		if disc.Urgency == "immediate" && disc.ConvertedToTask == nil {
			immediateDiscoveries++
		}
	}
	if immediateDiscoveries > 0 {
		return "IMMEDIATE_DISCOVERY", immediateDiscoveries
	}

	if state.AllPlannedTasksTerminal() {
		return "SPRINT_COMPLETE", len(state.Sprint.Scope.Planned)
	}

	return "NONE", 0
}

// buildWorkQueuesStatus calculates work queue availability
func buildWorkQueuesStatus(state *models.State, claimable, reviewable int) workQueuesStatus {
	return workQueuesStatus{
		Coder: queueStatus{
			Available: claimable,
			Reason:    models.GetCoderWorkDiagnostics(state),
		},
		Reviewer: queueStatus{
			Available: reviewable,
			Reason:    models.GetReviewerWorkDiagnostics(state),
		},
	}
}

// getProcessStatus checks if a process is running
func getProcessStatus(pid int) string {
	if pid == 0 {
		return "unknown"
	}

	// Try to find the process
	process, err := os.FindProcess(pid)
	if err != nil {
		return "not found"
	}

	// Send signal 0 to check if process exists
	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return "running"
	}

	return "stopped"
}

func writeTasksSection(b *strings.Builder, tasks taskStatus) {
	b.WriteString("=== TASKS ===\n")
	fmt.Fprintf(b, "Total: %d (%d active, %d terminal)\n",
		tasks.Total, tasks.Active, tasks.Terminal)

	if len(tasks.ByStatus) > 0 {
		b.WriteString("\nBy Status:\n")
		statuses := make([]string, 0, len(tasks.ByStatus))
		for status := range tasks.ByStatus {
			statuses = append(statuses, status)
		}
		slices.Sort(statuses)
		for _, status := range statuses {
			fmt.Fprintf(b, "  %s: %d\n", status, tasks.ByStatus[status])
		}
	}

	fmt.Fprintf(b, "\nClaimable: %d tasks\n", tasks.Claimable)
	fmt.Fprintf(b, "Reviewable: %d tasks\n", tasks.Reviewable)
	if tasks.BlockedByDeps > 0 {
		fmt.Fprintf(b, "Blocked by dependencies: %d tasks\n", tasks.BlockedByDeps)
	}
	b.WriteString("\n")
}

func writeAgentsSection(b *strings.Builder, agents []agentStatus) {
	b.WriteString("=== AGENTS ===\n")
	if len(agents) == 0 {
		b.WriteString("No active agents\n\n")
		return
	}
	headers := []string{"ID", "Role", "Status", "PID", "Task", "Heartbeat", "Process"}
	rows := make([][]string, len(agents))
	for i, agent := range agents {
		pidStr := "-"
		if agent.PID != 0 {
			pidStr = fmt.Sprintf("%d", agent.PID)
		}
		rows[i] = []string{
			agent.ID,
			agent.Role,
			agent.Status,
			pidStr,
			agent.CurrentTask,
			agent.TimeSinceHeartbeat,
			agent.ProcessStatus,
		}
	}
	b.WriteString(formatTable(headers, rows))
	b.WriteString("\n\n")
}

// statusDashboardData is the template data for status_dashboard.tmpl
type statusDashboardData struct {
	statusData
	TasksSection  string
	AgentsSection string
	AnomalyList   []string
}

// formatStatusDashboard renders the status as a dashboard
func formatStatusDashboard(data statusData) (string, error) {
	// Pre-render imperative sections (table formatters stay as-is)
	var tasksBuf, agentsBuf strings.Builder
	writeTasksSection(&tasksBuf, data.Tasks)
	writeAgentsSection(&agentsBuf, data.Agents)

	var anomalyList []string
	if data.Anomalies != nil {
		anomalyList = *data.Anomalies
	}

	tmplData := statusDashboardData{
		statusData:    data,
		TasksSection:  tasksBuf.String(),
		AgentsSection: agentsBuf.String(),
		AnomalyList:   anomalyList,
	}
	return executeCommandTemplate("status_dashboard", tmplData)
}
