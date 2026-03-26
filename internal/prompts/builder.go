package prompts

import (
"bytes"
"fmt"
"strings"

"github.com/liza-mas/liza/internal/models"
"github.com/liza-mas/liza/internal/ops"
"github.com/liza-mas/liza/internal/verify"
)

// BasePromptConfig contains configuration for building the base prompt
type BasePromptConfig struct {
Role        string
AgentID     string
TaskID      string // empty for orchestrator
SpecsDir    string
ProjectRoot string
StatePath   string
GoalDesc    string
GoalSpecRef string
}

// SiblingTaskSummary provides minimal context about sibling tasks in the same sprint
type SiblingTaskSummary struct {
ID          string
Description string
Status      string
PlanRef     string
RolePair    string
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
// RenderOrchestratorDashboard pre-renders the orchestrator dashboard and wake instruction
// strings for use as RoleContextData.DashboardOutput and RoleContextData.WakeInstruction.
// This replaces the old BuildOrchestratorContext + orchestrator_context.tmpl approach.
func RenderOrchestratorDashboard(state *models.State, projectRoot, agentID string) (dashboard, wakeInstruction string, err error) {
totalTasks := len(state.Tasks)
merged := countTasksByStatus(state.Tasks, models.TaskStatusMerged)
blocked := countTasksByStatus(state.Tasks, models.TaskStatusBlocked)
integrationFailed := countTasksByStatus(state.Tasks, models.TaskStatusIntegrationFailed)
unclaimed := countTasksByStatus(state.Tasks, models.TaskStatusReady)

inProgress := countTasksByStatus(state.Tasks, models.TaskStatusImplementing) +
countTasksByStatus(state.Tasks, models.TaskStatusReadyForReview) +
countTasksByStatus(state.Tasks, models.TaskStatusApproved)

hypothesisExhausted := countHypothesisExhausted(state.Tasks)
immediateDiscoveries := countImmediateDiscoveries(state.Discovered)

detCtx, detErr := ops.LoadDetectionContext(projectRoot)
var sprintTerminals []models.TaskStatus
var planningPairs map[string]bool
if detErr == nil {
sprintTerminals = detCtx.SprintTerminals
planningPairs = detCtx.PlanningPairs
}

cycleBlocked := countCycleBlockedPlanning(state.Tasks, planningPairs)

sprintComplete := state.AllPlannedTasksTerminalWith(sprintTerminals)

var planningTasks []planningTaskData
if sprintComplete {
planningTasks = collectMergedPlanningTasks(state, planningPairs)
}

wakeTrigger := determineWakeTrigger(totalTasks, blocked, hypothesisExhausted, immediateDiscoveries, sprintComplete, planningTasks)

wakeData, wakeErr := buildWakeTemplateData(state.Goal.SpecRef, state.Goal.EntryPoint, projectRoot)
if wakeErr != nil {
return "", "", fmt.Errorf("building wake template data: %w", wakeErr)
}

wakeInstructions, instrErr := buildInstructionsForWakeTrigger(wakeTrigger, agentID, wakeData, planningTasks)
if instrErr != nil {
return "", "", fmt.Errorf("building wake instructions: %w", instrErr)
}

// Build the dashboard string (replaces orchestrator_context.tmpl rendering)
var b strings.Builder
b.WriteString("\n\n=== ORCHESTRATOR CONTEXT ===\n")
b.WriteString(fmt.Sprintf("WAKE TRIGGER: %s\n", wakeTrigger))
b.WriteString("\nSPRINT STATE:\n")
if state.Sprint.Number > 0 {
b.WriteString(fmt.Sprintf("- Sprint number: %d\n", state.Sprint.Number))
}
if len(state.SprintHistory) > 0 {
b.WriteString(fmt.Sprintf("- Previous sprints: %d\n", len(state.SprintHistory)))
for _, sh := range state.SprintHistory {
b.WriteString(fmt.Sprintf("  - %s: %s (%d tasks done)\n", sh.ID, sh.Status, sh.TasksDone))
}
}
b.WriteString(fmt.Sprintf("- Total tasks: %d\n", totalTasks))
b.WriteString(fmt.Sprintf("- Merged: %d\n", merged))
b.WriteString(fmt.Sprintf("- In progress: %d\n", inProgress))
b.WriteString(fmt.Sprintf("- Unclaimed: %d\n", unclaimed))
b.WriteString(fmt.Sprintf("- Blocked: %d\n", blocked))
b.WriteString(fmt.Sprintf("- Integration failed: %d\n", integrationFailed))
b.WriteString(fmt.Sprintf("- Hypothesis exhausted: %d\n", hypothesisExhausted))
b.WriteString(fmt.Sprintf("- Immediate discoveries: %d\n", immediateDiscoveries))
if cycleBlocked > 0 {
b.WriteString(fmt.Sprintf("- Cycle-blocked planning: %d\n", cycleBlocked))
}

b.WriteString(fmt.Sprintf(`
ORCHESTRATOR COMMANDS (resolve AFTER initialization: ToolSearch select:mcp__liza__liza_get,mcp__liza__liza_status,mcp__liza__liza_add_tasks,mcp__liza__liza_supersede_task,mcp__liza__liza_assess_blocked,mcp__liza__liza_wt_delete,mcp__liza__liza_sprint_checkpoint,mcp__liza__liza_update_sprint_metrics):
- liza_add_tasks — Add one or more tasks to blackboard (atomic per task, with validation)
  Tool parameters: {"tasks": [{"id": "...", "desc": "...", "spec": "...", "done": "...", "scope": "...", "priority": N, "depends": [...]}], "agent_id": "%s"}
- liza_supersede_task — Supersede task
  Tool parameters: {"task_id": "...", "replacement_ids": [...], "reason": "...", "agent_id": "%s"}
- liza_assess_blocked — Record orchestrator assessment of a BLOCKED task (prevents re-wake loops)
  Tool parameters: {"task_id": "...", "note": "...", "agent_id": "%s"}
- liza_wt_delete — Delete worktree for abandoned/superseded/blocked tasks
  Tool parameters: {"task_id": "...", "agent_id": "%s"}
- liza_sprint_checkpoint — Create sprint checkpoint for human review (pauses all agents)
  Tool parameters: {"agent_id": "%s"}
- liza_update_sprint_metrics — Recompute sprint metrics from current state
  Tool parameters: {"agent_id": "%s"}

ANOMALY LOGGING:
| Event | Type | Required Fields |
|-------|------|-----------------|
| Two coders failed same task | hypothesis_exhaustion | — |
| Spec gap discovered | spec_gap | — |
| Review stuck in cycles | review_deadlock | — |
| Multiple reviewers failed | review_exhaustion | reviewers_failed, common_blocker |
| Protocol ambiguity | system_ambiguity | protocol_section, question |
Format: anomalies: [{id, type, task, reporter, timestamp (ISO 8601), details: {<fields>}}]

SELF-VALIDATION GATES (verify before adding each task):
| Gate | Requirement |
|------|-------------|
| Spec reference | Each task must cite spec |
| Success criteria | Each task must have falsifiable done |
| Scope boundary | IN scope stated (functional area, not file names) |
| Dependency check | Dependencies stated if any |
| TDD inclusion | Code tasks include tests |

FIELD FORMAT GUIDELINES:
- done: observable behavior, specific, falsifiable. Bad: "works correctly". Good: "GET /users returns 200"
- spec: path to spec optionally with #anchor

TASK CREATION ORDER:
When adding multiple tasks with dependencies, create them in topological order — dependency-free tasks first, then tasks that depend on them. liza_add_tasks validates that all `+"`depends`"+` IDs already exist; creating a task that references a not-yet-created dependency will fail.

ERROR RECOVERY:
On MCP tool errors, diagnose the root cause before retrying. Read the error message, investigate the constraint that failed (e.g. missing dependency, invalid state), and fix the underlying issue. Do NOT retry the same call blindly.

MULTIPLE BLOCKED TASKS: Process sequentially by priority (lowest number first), then by timestamp.
Work unit = all planned state changes executed. Do NOT exit until all tools have been called.
`, agentID, agentID, agentID, agentID, agentID, agentID))

// Wake instruction is rendered separately by the wake-instructions block
wakeInstr := fmt.Sprintf("INSTRUCTIONS:\n%s", wakeInstructions)

return b.String(), wakeInstr, nil
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

wakeTrigger := determinePlannerWakeTrigger(totalTasks, blocked, integrationFailed, hypothesisExhausted, immediateDiscoveries, unresolvedReplan, unresolvedRemediation, state.AllPlannedTasksTerminal())

wakeInstructions, err := buildPlannerWakeInstructions(wakeTrigger, state.Goal.SpecRef, state)
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
task = sanitizeTaskForPrompt(task)
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
task = sanitizeTaskForPrompt(task)
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

// sanitizeTaskForPrompt returns a shallow copy of the task with verify_commands
// and done_when sanitized for the current environment.
func sanitizeTaskForPrompt(task *models.Task) *models.Task {
copy := *task
if len(copy.VerifyCommands) > 0 {
sanitized := make([]string, len(copy.VerifyCommands))
for i, cmd := range copy.VerifyCommands {
sanitized[i] = verify.StripRaceFlagIfNeeded(cmd)
}
copy.VerifyCommands = sanitized
}
copy.DoneWhen = verify.StripRaceFlagIfNeeded(copy.DoneWhen)
return &copy
}

// hasPriorRejection reports whether the task has actionable rejection feedback from a prior iteration
func hasPriorRejection(task *models.Task) bool {
return task.Iteration > 1 && task.RejectionReason != nil && *task.RejectionReason != "" && *task.RejectionReason != "null"
}
func countTasksByStatus(tasks []models.Task, status models.TaskStatus) int {
count := 0
for _, task := range tasks {
if task.Status == status {
count++
}
}
return count
}

// countHypothesisExhausted counts non-terminal tasks that have been failed by 2+ reviewers.
func countHypothesisExhausted(tasks []models.Task) int {
count := 0
for _, task := range tasks {
if len(task.FailedBy) >= 2 && !task.Status.IsTerminal() {
count++
}
}
return count
}

// countImmediateDiscoveries counts unresolved discoveries with "immediate" urgency.
func countImmediateDiscoveries(discovered []models.Discovery) int {
count := 0
for _, disc := range discovered {
if disc.Urgency == "immediate" && disc.ConvertedToTask == nil {
count++
}
}
return count
}

// countCycleBlockedPlanning counts MERGED planning tasks with a transition_cycle_blocked history event.
func countCycleBlockedPlanning(tasks []models.Task, planningPairs map[string]bool) int {
count := 0
for i := range tasks {
if tasks[i].Status == models.TaskStatusMerged &&
ops.IsPlanningPair(tasks[i].RolePair, planningPairs) &&
ops.IsTransitionCycleBlocked(&tasks[i]) {
count++
}
}
return count
}

// BuildRoleContext assembles role-specific context by rendering the named template
// blocks in order and concatenating their output. Each block is a modular .tmpl file
// in templates/blocks/ that receives a unified RoleContextData.
//
// Block boundaries are normalized here rather than relying on template whitespace
// control ({{- -}} trimming), which is fragile and linter-hostile. Each non-empty
// block is TrimSpace'd and joined with a blank-line separator.
func BuildRoleContext(role string, sectionNames []string, data *RoleContextData) (string, error) {
var blocks []string
for _, section := range sectionNames {
var sectionBuf bytes.Buffer
if err := blockTmpl.ExecuteTemplate(&sectionBuf, section, data); err != nil {
return "", fmt.Errorf("block template %q for role %q: %w", section, role, err)
}
rendered := strings.TrimSpace(sectionBuf.String())
if rendered == "" {
continue
}
blocks = append(blocks, rendered)
}
if len(blocks) == 0 {
return "", nil
}
// Leading \n\n separates from base prompt; \n\n between blocks = one blank line.
return "\n\n" + strings.Join(blocks, "\n\n") + "\n", nil
}

// determinePlannerWakeTrigger determines what triggered the planner to wake (private-main variant)
func determinePlannerWakeTrigger(totalTasks, blocked, integrationFailed, hypothesisExhausted, immediateDiscoveries, unresolvedReplan, unresolvedRemediation int, sprintComplete bool) string {
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
	AgentID string
	Tasks   []blockedTaskInfo
}

// buildPlannerWakeInstructions returns trigger-specific instructions for the planner (private-main variant)
func buildPlannerWakeInstructions(wakeTrigger, goalSpecRef string, state *models.State) (string, error) {
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