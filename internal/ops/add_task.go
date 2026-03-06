package ops

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/log"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/statevalidate"
)

// AddTaskInput represents the input parameters for adding a task.
type AddTaskInput struct {
	ID                 string
	Type               string
	Description        string
	SpecRef            string
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
// and appends to the activity log. No terminal I/O.
func AddTask(statePath, logPath string, input *AddTaskInput, plannerID string) (*AddTaskResult, error) {
	if plannerID == "" {
		plannerID = "planner-1"
	}
	if input.ID == "" {
		return nil, fmt.Errorf("task ID is required")
	}
	if input.Description == "" {
		return nil, fmt.Errorf("description is required")
	}
	if input.SpecRef == "" {
		return nil, fmt.Errorf("spec_ref is required")
	}
	if input.DoneWhen == "" {
		return nil, fmt.Errorf("done_when is required")
	}
	if input.Scope == "" {
		return nil, fmt.Errorf("scope is required")
	}
	if input.Priority < 1 {
		return nil, fmt.Errorf("priority must be positive, got %d", input.Priority)
	}

	if input.Type == "" {
		input.Type = string(models.TaskTypeCoding)
	}

	taskType := models.TaskType(input.Type)
	if !taskType.IsValid() {
		return nil, fmt.Errorf("unknown task type %q", input.Type)
	}

	normalizedDeps := []string{}
	for _, dep := range input.DependsOn {
		trimmed := strings.TrimSpace(dep)
		if trimmed != "" {
			normalizedDeps = append(normalizedDeps, trimmed)
		}
	}

	now := time.Now().UTC()
	agentID := plannerID

	bb := db.For(statePath)

	existingTask, err := bb.GetTask(input.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to check for existing task: %w", err)
	}
	if existingTask != nil {
		return nil, fmt.Errorf("task '%s' already exists in %s", input.ID, statePath)
	}

	// Read state for config-driven enforcement checks
	state, err := bb.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read state for enforcement checks: %w", err)
	}

	// Enforce requirement_refs when config flag is enabled
	if state.Config.EnforceRequirementRefs && len(input.RequirementRefs) == 0 {
		return nil, fmt.Errorf("task %s: requirement_refs required (enforce_requirement_refs is enabled in config)", input.ID)
	}

	// Enforce deduplication: reject tasks with identical description + scope
	if state.Config.EnforceDeduplication {
		for _, existing := range state.Tasks {
			if existing.Status.IsTerminal() {
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

	newTask := models.Task{
		ID:                 input.ID,
		Type:               taskType,
		Description:        input.Description,
		Status:             models.TaskStatusReady,
		Priority:           input.Priority,
		SpecRef:            input.SpecRef,
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
		state.Tasks = append(state.Tasks, newTask)

		if !slices.Contains(state.Sprint.Scope.Planned, input.ID) {
			state.Sprint.Scope.Planned = append(state.Sprint.Scope.Planned, input.ID)
		}

		alignmentEntry := models.AlignmentHistory{
			Timestamp: now,
			Event:     "planning",
			Summary:   fmt.Sprintf("Added task %s: %s", input.ID, input.Description),
		}
		state.Goal.AlignmentHistory = append(state.Goal.AlignmentHistory, alignmentEntry)

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to add task: %w", err)
	}

	result := &AddTaskResult{TaskID: input.ID}

	logger := log.New(logPath)
	logEntry := log.Entry{
		Timestamp: now,
		Agent:     agentID,
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
