package mcp

import (
	"fmt"

	"github.com/liza-mas/liza/internal/commands"
	"github.com/liza-mas/liza/internal/identity"
	"github.com/liza-mas/liza/internal/ops"
	"github.com/liza-mas/liza/internal/paths"
	"github.com/liza-mas/liza/internal/roles"
)

// Version and BuildCommit are set from the embedded package's build-time
// variables when the MCP server binary starts. Defaults for dev/test use.
var (
	Version     = "dev"
	BuildCommit = "unknown"
)

// textResult builds a standard MCP text content response.
func textResult(msg string) (any, error) {
	return map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": msg,
			},
		},
	}, nil
}

// resourceContent builds a standard MCP resource content response.
func resourceContent(uri, mimeType, text string) any {
	return map[string]any{
		"contents": []any{
			map[string]any{
				"uri":      uri,
				"mimeType": mimeType,
				"text":     text,
			},
		},
	}
}

// inspectResource reads a Liza resource via the inspect command.
func (s *Server) inspectResource(uri string, args ...string) (any, error) {
	opts := commands.InspectOptions{
		Format:      "json",
		ProjectRoot: s.projectRoot,
		Internal:    false,
	}
	result, err := commands.InspectCommand(args, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", uri, err)
	}
	return resourceContent(uri, "application/json", result), nil
}

// requireString extracts a required non-empty string parameter.
func requireString(params map[string]any, key string) (string, error) {
	v, ok := params[key].(string)
	if !ok || v == "" {
		return "", fmt.Errorf("%s parameter required", key)
	}
	return v, nil
}

// extractStringSlice extracts an optional []string from a JSON params map.
// JSON arrays arrive as []any; non-string elements are silently skipped.
func extractStringSlice(params map[string]any, key string) []string {
	raw, ok := params[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// appendWarnings appends warning lines to a message string.
func appendWarnings(msg string, warnings []string) string {
	for _, w := range warnings {
		msg += "\nWarning: " + w
	}
	return msg
}

// requireTaskAndAgent extracts the common task_id + agent_id pair.
func requireTaskAndAgent(params map[string]any) (taskID, agentID string, err error) {
	taskID, err = requireString(params, "task_id")
	if err != nil {
		return "", "", err
	}
	agentID, err = requireString(params, "agent_id")
	if err != nil {
		return "", "", err
	}
	return taskID, agentID, nil
}

// requireRole validates agent ID format and that it matches the expected runtime role.
func requireRole(agentID, expectedRole string) error {
	if err := identity.ValidateFormat(agentID); err != nil {
		return fmt.Errorf("invalid agent ID %q: %w", agentID, err)
	}
	role, _ := identity.ExtractRole(agentID) // cannot fail after ValidateFormat
	if role != expectedRole {
		return fmt.Errorf("requires %s role (got %s from %s)", expectedRole, role, agentID)
	}
	return nil
}

// handleGet implements the liza_get tool
// Maps to: liza get <query>
func (s *Server) handleGet(params map[string]any) (any, error) {
	query, err := requireString(params, "query")
	if err != nil {
		return nil, err
	}

	format := "json"
	if f, ok := params["format"].(string); ok && f != "" {
		format = f
	}

	opts := commands.InspectOptions{
		Format:      format,
		ProjectRoot: s.projectRoot,
		Internal:    false, // Get formatted output
	}

	result, err := commands.InspectCommand([]string{query}, opts)
	if err != nil {
		return nil, fmt.Errorf("inspect command failed: %w", err)
	}

	return textResult(result)
}

// handleStatus implements the liza_status tool
// Maps to: liza status
func (s *Server) handleStatus(params map[string]any) (any, error) {
	opts := commands.StatusOptions{
		ProjectRoot: s.projectRoot,
	}

	result, err := commands.StatusCommand(opts)
	if err != nil {
		return nil, fmt.Errorf("status command failed: %w", err)
	}

	return textResult(result)
}

// handleValidate implements the liza_validate tool
// Maps to: liza validate
func (s *Server) handleValidate(params map[string]any) (any, error) {
	statePath := paths.New(s.projectRoot).StatePath()

	skipSpecFileCheck := false
	if skip, ok := params["skip_spec_check"].(bool); ok {
		skipSpecFileCheck = skip
	}

	err := commands.ValidateCommand(statePath, skipSpecFileCheck)

	var resultText string
	if err != nil {
		resultText = fmt.Sprintf("Validation failed: %v", err)
	} else {
		resultText = "Validation passed: workspace state is consistent"
	}

	return map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": resultText,
			},
		},
		"isError": err != nil,
	}, nil
}

// handleVersion implements the liza_version tool
// Maps to: liza version
func (s *Server) handleVersion(params map[string]any) (any, error) {
	return textResult(fmt.Sprintf("liza-mcp version %s (commit: %s)", Version, BuildCommit))
}

// handleResourceReadInternal reads a resource by URI
func (s *Server) handleResourceReadInternal(uri string) (any, error) {
	switch uri {
	case "liza://state":
		return s.readStateResource()
	case "liza://tasks":
		return s.inspectResource(uri, "tasks")
	case "liza://agents":
		return s.inspectResource(uri, "agents")
	default:
		const taskPrefix = "liza://tasks/"
		if len(uri) > len(taskPrefix) && uri[:len(taskPrefix)] == taskPrefix {
			taskID := uri[len(taskPrefix):]
			return s.inspectResource(uri, "tasks", taskID)
		}
		return nil, fmt.Errorf("unknown resource URI: %s", uri)
	}
}

// readStateResource returns the raw state.yaml content under flock protection.
func (s *Server) readStateResource() (any, error) {
	data, err := s.bb.ReadRaw()
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	return resourceContent("liza://state", "application/x-yaml", string(data)), nil
}

// handleAddTask implements the liza_add_task tool
// Maps to: liza add-task
func (s *Server) handleAddTask(params map[string]any) (any, error) {
	id, err := requireString(params, "id")
	if err != nil {
		return nil, err
	}

	description, err := requireString(params, "desc")
	if err != nil {
		return nil, err
	}

	specRef, err := requireString(params, "spec")
	if err != nil {
		return nil, err
	}

	doneWhen, err := requireString(params, "done")
	if err != nil {
		return nil, err
	}

	scope, err := requireString(params, "scope")
	if err != nil {
		return nil, err
	}

	agentID, _ := params["agent_id"].(string)
	if agentID == "" {
		agentID = "planner-1"
	}

	if err := requireRole(agentID, roles.RuntimePlanner); err != nil {
		return nil, err
	}

	priority := 1
	if p, ok := params["priority"].(float64); ok {
		priority = int(p)
	} else if p, ok := params["priority"].(int); ok {
		priority = p
	}

	dependsOn := extractStringSlice(params, "depends")
	requirementRefs := extractStringSlice(params, "requirement_refs")
	acceptanceCriteria := extractStringSlice(params, "acceptance_criteria")
	verifyCommands := extractStringSlice(params, "verify_commands")
	errorBehavior, _ := params["error_behavior"].(string)

	taskType, _ := params["type"].(string)
	originFindingID, _ := params["origin_finding_id"].(string)

	input := &ops.AddTaskInput{
		ID:                 id,
		Type:               taskType,
		Description:        description,
		SpecRef:            specRef,
		DoneWhen:           doneWhen,
		Scope:              scope,
		Priority:           priority,
		DependsOn:          dependsOn,
		RequirementRefs:    requirementRefs,
		AcceptanceCriteria: acceptanceCriteria,
		VerifyCommands:     verifyCommands,
		ErrorBehavior:      errorBehavior,
		OriginFindingID:    originFindingID,
	}

	statePath := paths.New(s.projectRoot).StatePath()
	result, err := ops.AddTask(statePath, s.logPath, input, agentID)
	if err != nil {
		return nil, fmt.Errorf("add task failed: %w", err)
	}

	msg := fmt.Sprintf("Task %s added successfully", id)
	for _, w := range result.Warnings {
		msg += fmt.Sprintf("\nwarning: %s", w)
	}
	return textResult(msg)
}

// handleClaimTask implements the liza_claim_task tool
// Maps to: liza claim-task
func (s *Server) handleClaimTask(params map[string]any) (any, error) {
	taskID, agentID, err := requireTaskAndAgent(params)
	if err != nil {
		return nil, err
	}
	if err := requireRole(agentID, roles.RuntimeCoder); err != nil {
		return nil, err
	}

	result, err := ops.ClaimTask(s.projectRoot, taskID, agentID)
	if err != nil {
		return nil, fmt.Errorf("claim task failed: %w", err)
	}

	return textResult(fmt.Sprintf("Task %s claimed by %s (from %s), worktree: %s",
		result.TaskID, result.AgentID, result.SourceStatus, result.WorktreeRel))
}

// handleSubmitForReview implements the liza_submit_for_review tool
// Maps to: liza submit-for-review
func (s *Server) handleSubmitForReview(params map[string]any) (any, error) {
	taskID, err := requireString(params, "task_id")
	if err != nil {
		return nil, err
	}

	commitSHA, err := requireString(params, "commit_sha")
	if err != nil {
		return nil, err
	}

	agentID, err := requireString(params, "agent_id")
	if err != nil {
		return nil, err
	}

	if err := requireRole(agentID, roles.RuntimeCoder); err != nil {
		return nil, err
	}

	result, err := ops.SubmitForReview(s.projectRoot, taskID, commitSHA, agentID)
	if err != nil {
		return nil, fmt.Errorf("submit for review failed: %w", err)
	}

	return textResult(fmt.Sprintf("Task %s submitted for review (commit: %s)", result.TaskID, result.ReviewCommit))
}

// handleHandoff implements the liza_handoff tool
// Maps to: liza handoff
func (s *Server) handleHandoff(params map[string]any) (any, error) {
	taskID, agentID, err := requireTaskAndAgent(params)
	if err != nil {
		return nil, err
	}
	if err := requireRole(agentID, roles.RuntimeCoder); err != nil {
		return nil, err
	}

	summary, err := requireString(params, "summary")
	if err != nil {
		return nil, err
	}

	nextAction, err := requireString(params, "next_action")
	if err != nil {
		return nil, err
	}

	result, err := ops.Handoff(s.projectRoot, taskID, summary, nextAction, agentID)
	if err != nil {
		return nil, fmt.Errorf("handoff failed: %w", err)
	}

	return textResult(fmt.Sprintf("Handoff initiated for task %s by %s", result.TaskID, result.AgentID))
}

// handleSubmitVerdict implements the liza_submit_verdict tool
// Maps to: liza submit-verdict
func (s *Server) handleSubmitVerdict(params map[string]any) (any, error) {
	taskID, err := requireString(params, "task_id")
	if err != nil {
		return nil, err
	}

	verdict, err := requireString(params, "verdict")
	if err != nil {
		return nil, err
	}

	agentID, err := requireString(params, "agent_id")
	if err != nil {
		return nil, err
	}

	if err := requireRole(agentID, roles.RuntimeCodeReviewer); err != nil {
		return nil, err
	}

	reason, _ := params["reason"].(string)

	result, err := ops.SubmitVerdict(s.projectRoot, taskID, verdict, reason, agentID)
	if err != nil {
		return nil, fmt.Errorf("submit verdict failed: %w", err)
	}

	return textResult(fmt.Sprintf("Verdict %s submitted for task %s", result.Verdict, result.TaskID))
}

// handleMarkBlocked implements the liza_mark_blocked tool
// Maps to: liza mark-blocked
func (s *Server) handleMarkBlocked(params map[string]any) (any, error) {
	taskID, agentID, err := requireTaskAndAgent(params)
	if err != nil {
		return nil, err
	}

	reason, err := requireString(params, "reason")
	if err != nil {
		return nil, err
	}

	questions := extractStringSlice(params, "questions")
	if len(questions) == 0 {
		return nil, fmt.Errorf("questions parameter required (1-3 questions)")
	}

	_, err = ops.MarkBlocked(s.projectRoot, taskID, reason, questions, agentID)
	if err != nil {
		return nil, fmt.Errorf("mark blocked failed: %w", err)
	}

	return textResult(fmt.Sprintf("Task %s marked as BLOCKED", taskID))
}

// handleReleaseClaim implements the liza_release_claim tool
// Maps to: liza release-claim
func (s *Server) handleReleaseClaim(params map[string]any) (any, error) {
	taskID, err := requireString(params, "task_id")
	if err != nil {
		return nil, err
	}

	role, err := requireString(params, "role")
	if err != nil {
		return nil, err
	}

	agentID, err := requireString(params, "agent_id")
	if err != nil {
		return nil, err
	}

	reason, _ := params["reason"].(string)
	force, _ := params["force"].(bool)

	result, err := ops.ReleaseClaim(s.projectRoot, taskID, role, force, reason, agentID)
	if err != nil {
		return nil, fmt.Errorf("release claim failed: %w", err)
	}

	msg := "Claim released for task " + result.TaskID
	if result.ReleasedReviewer && result.ReleasedCoder {
		msg = "Released reviewer and coder claims for task " + result.TaskID
	} else if result.ReleasedReviewer {
		msg = "Released reviewer claim for task " + result.TaskID
	} else if result.ReleasedCoder {
		msg = "Released coder claim for task " + result.TaskID
	}
	return textResult(msg)
}

// handleSupersede implements the liza_supersede_task tool
// Maps to: liza supersede-task
func (s *Server) handleSupersede(params map[string]any) (any, error) {
	taskID, agentID, err := requireTaskAndAgent(params)
	if err != nil {
		return nil, err
	}
	if err := requireRole(agentID, roles.RuntimePlanner); err != nil {
		return nil, err
	}

	reason, err := requireString(params, "reason")
	if err != nil {
		return nil, err
	}

	replacementIDs := extractStringSlice(params, "replacement_ids")

	result, err := ops.SupersedeTask(s.projectRoot, taskID, replacementIDs, reason, agentID)
	if err != nil {
		return nil, fmt.Errorf("supersede task failed: %w", err)
	}

	return textResult(fmt.Sprintf("Task %s superseded (was %s)", result.TaskID, result.OriginalStatus))
}

// handleWtCreate implements the liza_wt_create tool
// Maps to: liza wt-create
func (s *Server) handleWtCreate(params map[string]any) (any, error) {
	taskID, err := requireString(params, "task_id")
	if err != nil {
		return nil, err
	}

	fresh, _ := params["fresh"].(bool)

	result, err := ops.CreateWorktree(s.projectRoot, taskID, fresh)
	if err != nil {
		return nil, fmt.Errorf("wt-create failed: %w", err)
	}

	msg := fmt.Sprintf("Worktree created for task %s", taskID)
	if result.AlreadyExisted {
		msg = fmt.Sprintf("Worktree already exists for task %s", taskID)
	}
	return textResult(appendWarnings(msg, result.Warnings))
}

// handleWtDelete implements the liza_wt_delete tool
// Maps to: liza wt-delete
func (s *Server) handleWtDelete(params map[string]any) (any, error) {
	taskID, err := requireString(params, "task_id")
	if err != nil {
		return nil, err
	}

	result, err := ops.DeleteWorktree(s.projectRoot, taskID)
	if err != nil {
		return nil, fmt.Errorf("wt-delete failed: %w", err)
	}

	if !result.Existed {
		return textResult(fmt.Sprintf("No worktree for task %s", taskID))
	}

	return textResult(appendWarnings(
		fmt.Sprintf("Worktree deleted for task %s", taskID),
		result.Warnings,
	))
}

// handleWtMerge implements the liza_wt_merge tool
// Maps to: liza wt-merge
func (s *Server) handleWtMerge(params map[string]any) (any, error) {
	taskID, agentID, err := requireTaskAndAgent(params)
	if err != nil {
		return nil, err
	}
	if err := requireRole(agentID, roles.RuntimeCodeReviewer); err != nil {
		return nil, err
	}

	result, err := ops.MergeWorktree(s.projectRoot, taskID, agentID)
	if err != nil {
		return nil, fmt.Errorf("wt-merge failed: %w", err)
	}

	return textResult(appendWarnings(
		fmt.Sprintf("Task %s merged to integration branch (commit: %s)", result.TaskID, result.MergeCommit),
		result.Warnings,
	))
}

// handleAnalyze implements the liza_analyze tool
// Maps to: liza analyze
func (s *Server) handleAnalyze(params map[string]any) (any, error) {
	result, err := ops.Analyze(s.projectRoot)
	if err != nil {
		return nil, fmt.Errorf("analyze failed: %w", err)
	}

	if !result.Triggered {
		return textResult("Circuit breaker: OK — no patterns detected")
	}

	return textResult(fmt.Sprintf("Circuit breaker TRIGGERED: pattern=%s, severity=%s, report=%s",
		result.Pattern, result.Severity, result.ReportPath))
}

// handleSprintCheckpoint implements the liza_sprint_checkpoint tool
// Maps to: liza checkpoint
func (s *Server) handleSprintCheckpoint(params map[string]any) (any, error) {
	result, err := ops.SprintCheckpoint(s.projectRoot)
	if err != nil {
		return nil, fmt.Errorf("checkpoint failed: %w", err)
	}

	return textResult(fmt.Sprintf("Sprint checkpoint created at %s. Report: %s. Agents will pause at their next check.",
		result.CheckpointAt.Format("2006-01-02T15:04:05Z07:00"), result.ReportPath))
}

// handleUpdateSprintMetrics implements the liza_update_sprint_metrics tool
// Maps to: liza update-sprint-metrics
func (s *Server) handleUpdateSprintMetrics(params map[string]any) (any, error) {
	metrics, err := ops.UpdateSprintMetrics(s.projectRoot)
	if err != nil {
		return nil, fmt.Errorf("update sprint metrics failed: %w", err)
	}

	msg := fmt.Sprintf("Sprint metrics updated: %d done, %d in progress, %d blocked",
		metrics.TasksDone, metrics.TasksInProgress, metrics.TasksBlocked)

	warnings := ops.CheckSuspiciousRates(metrics)
	for _, w := range warnings {
		msg += "\n" + w
	}

	return textResult(msg)
}

// handleClearStaleReviews implements the liza_clear_stale_review_claims tool
// Maps to: liza clear-stale-review-claims
func (s *Server) handleClearStaleReviews(params map[string]any) (any, error) {
	count, err := ops.ClearStaleReviewClaims(s.projectRoot)
	if err != nil {
		return nil, fmt.Errorf("clear stale review claims failed: %w", err)
	}

	return textResult(fmt.Sprintf("Cleared %d stale review claims", count))
}

// handleWriteCheckpoint implements the liza_write_checkpoint tool
func (s *Server) handleWriteCheckpoint(params map[string]any) (any, error) {
	taskID, agentID, err := requireTaskAndAgent(params)
	if err != nil {
		return nil, err
	}
	if err := requireRole(agentID, roles.RuntimeCoder); err != nil {
		return nil, err
	}

	intent, err := requireString(params, "intent")
	if err != nil {
		return nil, err
	}

	validationPlan, err := requireString(params, "validation_plan")
	if err != nil {
		return nil, err
	}

	filesToModify := extractStringSlice(params, "files_to_modify")
	if len(filesToModify) == 0 {
		return nil, fmt.Errorf("files_to_modify parameter required (at least one file)")
	}

	assumptions := extractStringSlice(params, "assumptions")

	risks, _ := params["risks"].(string)
	tddNotRequired, _ := params["tdd_not_required"].(string)

	input := &ops.WriteCheckpointInput{
		TaskID:         taskID,
		AgentID:        agentID,
		Intent:         intent,
		ValidationPlan: validationPlan,
		FilesToModify:  filesToModify,
		Assumptions:    assumptions,
		Risks:          risks,
		TDDNotRequired: tddNotRequired,
	}

	if err := ops.WriteCheckpoint(s.projectRoot, input); err != nil {
		return nil, fmt.Errorf("write checkpoint failed: %w", err)
	}

	return textResult(fmt.Sprintf("Pre-execution checkpoint written for task %s", taskID))
}

// handleDeleteAgent implements the liza_delete_agent tool
// Maps to: liza delete agent
func (s *Server) handleDeleteAgent(params map[string]any) (any, error) {
	agentID, err := requireString(params, "agent_id")
	if err != nil {
		return nil, err
	}

	reason, err := requireString(params, "reason")
	if err != nil {
		return nil, err
	}

	force, _ := params["force"].(bool)

	result, err := ops.DeleteAgent(s.projectRoot, agentID, force, false, reason)
	if err != nil {
		return nil, fmt.Errorf("delete agent failed: %w", err)
	}

	return textResult(fmt.Sprintf("Agent %s deleted", result.AgentID))
}

// handleSubmitAuditFinding implements the liza_submit_audit_finding tool
// Maps to: ops.SubmitAuditFinding
func (s *Server) handleSubmitAuditFinding(params map[string]any) (any, error) {
	findingID, err := requireString(params, "finding_id")
	if err != nil {
		return nil, err
	}

	taskID, err := requireString(params, "task_id")
	if err != nil {
		return nil, err
	}

	agentID, err := requireString(params, "agent_id")
	if err != nil {
		return nil, err
	}

	if err := requireRole(agentID, roles.RuntimeAuditor); err != nil {
		return nil, err
	}

	severity, err := requireString(params, "severity")
	if err != nil {
		return nil, err
	}

	findingType, err := requireString(params, "type")
	if err != nil {
		return nil, err
	}

	evidence, err := requireString(params, "evidence")
	if err != nil {
		return nil, err
	}

	recommendedAction, _ := params["recommended_action"].(string)
	specReference, _ := params["spec_reference"].(string)

	phase, err := requireString(params, "phase")
	if err != nil {
		return nil, err
	}

	classification, err := requireString(params, "classification")
	if err != nil {
		return nil, err
	}

	result, err := ops.SubmitAuditFinding(s.projectRoot, ops.AuditFindingInput{
		FindingID:         findingID,
		TaskID:            taskID,
		Severity:          severity,
		Type:              findingType,
		Phase:             phase,
		Classification:    classification,
		Evidence:          evidence,
		RecommendedAction: recommendedAction,
		SpecReference:     specReference,
	})
	if err != nil {
		return nil, fmt.Errorf("submit audit finding failed: %w", err)
	}

	msg := fmt.Sprintf("Audit finding %s submitted for task %s (severity: %s, classification: %s, phase: %s)", result.FindingID, result.TaskID, result.Severity, result.Classification, phase)
	if result.TaskReopened {
		msg += fmt.Sprintf(" — task %s reopened (MERGED → READY)", result.TaskID)
	}
	return textResult(msg)
}
