package ops

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/log"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
	"github.com/liza-mas/liza/internal/statevalidate"
)

// AddTaskInput represents the input parameters for adding a task.
type AddTaskInput struct {
	ID                 string
	Type               string
	RolePair           string
	Description        string
	SpecRef            string
	PlanRef            string
	DoneWhen           string
	Scope              string
	Priority           int
	DependsOn          []string
	RequirementRefs    []string
	AcceptanceCriteria []string
	VerifyCommands     []string
	ErrorBehavior      string
	OriginTaskID       string
	OriginFindingID    string
}

// AddTaskResult contains the outcome of adding a task.
type AddTaskResult struct {
	TaskID   string
	Warnings []string
}

// PostWriteValidationError indicates the mutation succeeded but state
// validation failed immediately afterward.
type PostWriteValidationError struct {
	Err error
}

func (e *PostWriteValidationError) Error() string {
	return fmt.Sprintf("task added but state validation failed: %v", e.Err)
}

func (e *PostWriteValidationError) Unwrap() error {
	return e.Err
}

// AddTask atomically persists a new task after validating inputs and checking
// for duplicates. Also updates sprint.scope.planned, goal.alignment_history,
// links originating audit findings, and appends to the activity log.
func AddTask(statePath, logPath string, input *AddTaskInput, orchestratorID string) (*AddTaskResult, error) {
	if orchestratorID == "" {
		return nil, &PreconditionError{Reason: "orchestrator agent ID is required"}
	}
	if err := paths.ValidateTaskID(input.ID); err != nil {
		return nil, fmt.Errorf("invalid task ID: %w", err)
	}
	if input.Description == "" {
		return nil, &PreconditionError{Reason: "description is required"}
	}
	if input.SpecRef == "" {
		return nil, &PreconditionError{Reason: "spec_ref is required"}
	}
	if input.DoneWhen == "" {
		return nil, &PreconditionError{Reason: "done_when is required"}
	}
	if input.Scope == "" {
		return nil, &PreconditionError{Reason: "scope is required"}
	}
	if input.Priority < 1 {
		return nil, &PreconditionError{Reason: fmt.Sprintf("priority must be positive, got %d", input.Priority)}
	}

	if input.Type == "" {
		input.Type = string(models.TaskTypeCoding)
	}
	if input.PlanRef == "" {
		input.PlanRef = input.SpecRef
	}

	taskType := models.TaskType(input.Type)
	if !taskType.IsValid() {
		return nil, &PreconditionError{Reason: fmt.Sprintf("unknown task type %q; valid types: %s",
			input.Type, strings.Join(models.ValidTaskTypeNames(), ", "))}
	}

	projectRoot := filepath.Dir(filepath.Dir(statePath))
	resolver, _, err := loadResolver(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to load pipeline config: %w", err)
	}

	if input.RolePair == "" {
	outer:
		for _, candidate := range resolver.RolePairNames() {
			switch taskType {
			case models.TaskTypePlanning:
				if strings.Contains(candidate, "planning") {
					input.RolePair = candidate
					break outer
				}
			default:
				if strings.Contains(candidate, "coding") {
					input.RolePair = candidate
					break outer
				}
			}
		}
		if input.RolePair == "" {
			return nil, &PreconditionError{Reason: fmt.Sprintf("role_pair is required; available: %s", strings.Join(resolver.RolePairNames(), ", "))}
		}
	}
	if _, rpErr := resolver.RolePair(input.RolePair); rpErr != nil {
		return nil, &PreconditionError{Reason: fmt.Sprintf("unknown role_pair %q; available role_pairs: %s", input.RolePair, strings.Join(resolver.RolePairNames(), ", "))}
	}

	normalizedDeps := []string{}
	for _, dep := range input.DependsOn {
		trimmed := strings.TrimSpace(dep)
		if trimmed != "" {
			normalizedDeps = append(normalizedDeps, trimmed)
		}
	}

	now := time.Now().UTC()
	bb := db.For(statePath)

	state, err := bb.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read state for enforcement checks: %w", err)
	}

	if state.Config.EnforceRequirementRefs && len(input.RequirementRefs) == 0 {
		return nil, fmt.Errorf("task %s: requirement_refs required (enforce_requirement_refs is enabled in config)", input.ID)
	}

	for _, existing := range state.Tasks {
		if existing.Status.IsComplete() {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(existing.Description), strings.TrimSpace(input.Description)) {
			return nil, fmt.Errorf("task %s is a duplicate of existing task %s (same description)", input.ID, existing.ID)
		}
	}

	if input.OriginTaskID != "" {
		for _, existing := range state.Tasks {
			if existing.ID != input.OriginTaskID {
				continue
			}
			if existing.Status == models.TaskStatusImplementing ||
				existing.Status == models.TaskStatusReadyForReview ||
				existing.Status == models.TaskStatusReviewing ||
				existing.Status == models.TaskStatusRejected {
				return nil, fmt.Errorf(
					"task %s: origin task %s is still active (status: %s) — wait for it to complete or be blocked before creating remediation",
					input.ID, existing.ID, existing.Status,
				)
			}
		}
	}

	if err := rejectFrameworkMetaTask(input); err != nil {
		return nil, err
	}

	if state.Config.EnforceDeduplication {
		for _, existing := range state.Tasks {
			if existing.Status.IsComplete() {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(existing.Description), strings.TrimSpace(input.Description)) &&
				strings.EqualFold(strings.TrimSpace(existing.Scope), strings.TrimSpace(input.Scope)) {
				return nil, fmt.Errorf(
					"task %s is a duplicate of existing task %s (same description + scope; enforce_deduplication is enabled)",
					input.ID, existing.ID,
				)
			}
		}
	}

	specFile := input.SpecRef
	if idx := strings.Index(specFile, "#"); idx != -1 {
		specFile = specFile[:idx]
	}
	specPath := specFile
	if !filepath.IsAbs(specPath) {
		specPath = filepath.Join(projectRoot, specFile)
	}
	if _, statErr := os.Stat(specPath); os.IsNotExist(statErr) {
		return nil, fmt.Errorf("task %s: spec_ref file not found: %s", input.ID, specFile)
	}

	initialStatus, err := resolver.InitialStatus(input.RolePair)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve initial status for role-pair %q: %w", input.RolePair, err)
	}

	newTask := models.Task{
		ID:                 input.ID,
		Type:               taskType,
		RolePair:           input.RolePair,
		Description:        input.Description,
		Status:             initialStatus,
		Priority:           input.Priority,
		SpecRef:            paths.NormalizeSpecRef(input.SpecRef),
		PlanRef:            paths.NormalizeSpecRef(input.PlanRef),
		DoneWhen:           input.DoneWhen,
		Scope:              input.Scope,
		DependsOn:          normalizedDeps,
		RequirementRefs:    input.RequirementRefs,
		AcceptanceCriteria: input.AcceptanceCriteria,
		VerifyCommands:     input.VerifyCommands,
		ErrorBehavior:      input.ErrorBehavior,
		OriginTaskID:       input.OriginTaskID,
		OriginFindingID:    input.OriginFindingID,
		Created:            now,
		History:            []models.TaskHistoryEntry{},
	}

	err = bb.Modify(func(state *models.State) error {
		if state.FindTask(input.ID) != nil {
			return &PreconditionError{Reason: fmt.Sprintf("task '%s' already exists", input.ID)}
		}
		state.Tasks = append(state.Tasks, newTask)

		if !slices.Contains(state.Sprint.Scope.Planned, input.ID) {
			state.Sprint.Scope.Planned = append(state.Sprint.Scope.Planned, input.ID)
		}

		alignmentEntry := models.AlignmentHistory{
			Timestamp: now,
			Event:     models.TaskEventPlanning,
			Summary:   fmt.Sprintf("Added task %s: %s", input.ID, input.Description),
		}
		state.Goal.AlignmentHistory = append(state.Goal.AlignmentHistory, alignmentEntry)

		if input.OriginFindingID != "" {
			for i := range state.AuditFindings {
				if state.AuditFindings[i].ID == input.OriginFindingID {
					state.AuditFindings[i].LinkedTaskID = input.ID
					break
				}
			}
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to add task: %w", err)
	}

	result := &AddTaskResult{TaskID: input.ID}

	logger := log.New(logPath)
	logEntry := log.Entry{
		Timestamp: now,
		Agent:     orchestratorID,
		Action:    "task_added",
		Task:      &input.ID,
		Detail:    input.Description,
	}
	if err := logger.Append(logEntry); err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("activity log write failed: %v", err))
	}

	if err := statevalidate.ValidateStateFile(statePath, false, io.Discard); err != nil {
		return nil, &PostWriteValidationError{Err: err}
	}

	return result, nil
}

// AddTasksInput represents the input for batch task creation.
type AddTasksInput struct {
	Tasks          []AddTaskInput
	OrchestratorID string
}

// AddTasksResult contains the outcome of batch task creation.
type AddTasksResult struct {
	Results []AddTaskItemResult
}

// AddTaskItemResult contains the outcome of adding a single task in a batch.
type AddTaskItemResult struct {
	TaskID   string
	Success  bool
	Error    string
	Warnings []string
}

// AddTasks adds multiple tasks in a single call. Each task is added independently.
func AddTasks(statePath, logPath string, input *AddTasksInput) (*AddTasksResult, error) {
	if len(input.Tasks) == 0 {
		return nil, &PreconditionError{Reason: "at least one task is required"}
	}
	orchestratorID := input.OrchestratorID
	if orchestratorID == "" {
		return nil, &PreconditionError{Reason: "orchestrator agent ID is required"}
	}
	result := &AddTasksResult{Results: make([]AddTaskItemResult, 0, len(input.Tasks))}
	for i := range input.Tasks {
		r, err := AddTask(statePath, logPath, &input.Tasks[i], orchestratorID)
		item := AddTaskItemResult{TaskID: input.Tasks[i].ID}
		if err != nil {
			var postWriteErr *PostWriteValidationError
			if errors.As(err, &postWriteErr) {
				item.Error = err.Error()
				result.Results = append(result.Results, item)
				return result, err
			}
			item.Error = err.Error()
		} else {
			item.Success = true
			item.TaskID = r.TaskID
			item.Warnings = r.Warnings
		}
		result.Results = append(result.Results, item)
	}
	return result, nil
}

var frameworkTerms = []string{
	"liza_submit_for_review",
	"liza_submit_work",
	"liza_submit_verdict",
	"task_complete loop",
	"task_complete tool",
	"mcp tool",
	"mcp validation",
	"mcp server",
	"coder agent",
	"coder submission",
	"coder workflow",
	"supervisor",
	"submission workflow",
	"copilot-native",
}

func rejectFrameworkMetaTask(input *AddTaskInput) error {
	descLower := strings.ToLower(input.Description)
	doneLower := strings.ToLower(input.DoneWhen)
	for _, term := range frameworkTerms {
		if strings.Contains(descLower, term) || strings.Contains(doneLower, term) {
			return fmt.Errorf(
				"task %s rejected: description or done_when references framework-internal term %q — coders cannot implement tasks that target liza orchestrator behavior",
				input.ID, term,
			)
		}
	}
	return nil
}
