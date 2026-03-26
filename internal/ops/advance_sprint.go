package ops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
	"github.com/liza-mas/liza/internal/pipeline"
	"gopkg.in/yaml.v3"
)

// AdvanceSprintResult contains the outcome of advancing to a new sprint.
type AdvanceSprintResult struct {
	ArchivedSprintID string
	NewSprintID      string
	NewSprintNumber  int
	CarriedTasks     []string
	ArchivePath      string
}

// sprintAdvancePlan holds validated, pre-computed data for a sprint advance.
// Created by planSprintAdvance (read-only), applied by applySprintAdvance (mutates state).
type sprintAdvancePlan struct {
	archivedSprint models.Sprint
	newSprintID    string
	newNumber      int
	carriedTasks   []string
}

// AdvanceSprint archives the current sprint and creates a new one.
// Non-terminal tasks are carried forward into the new sprint's planned scope.
//
// The archive file is written before state is mutated, so an archive write
// failure aborts the operation with no state change. This prevents data loss
// of full sprint detail.
//
// Precondition: sprint is at CHECKPOINT and all planned tasks are terminal.
// All precondition checks, archive write, and state mutation happen inside a
// single Modify to prevent TOCTOU races.
func AdvanceSprint(projectRoot string) (*AdvanceSprintResult, error) {
	lizaPaths := paths.New(projectRoot)
	statePath := lizaPaths.StatePath()
	blackboard := db.For(statePath)

	var result AdvanceSprintResult

	err := blackboard.Modify(func(s *models.State) error {
		plan, err := planSprintAdvance(s, time.Now().UTC(), projectRoot)
		if err != nil {
			return err
		}

		archivePath := lizaPaths.SprintArchivePath(plan.archivedSprint.Number)

		// Write archive BEFORE mutating state. If this fails, Modify aborts
		// and state is unchanged — no data loss.
		if err := writeSprintArchive(archivePath, &plan.archivedSprint); err != nil {
			return fmt.Errorf("archive write failed (state unchanged): %w", err)
		}

		applySprintAdvance(s, plan)

		result = AdvanceSprintResult{
			ArchivedSprintID: plan.archivedSprint.ID,
			NewSprintID:      plan.newSprintID,
			NewSprintNumber:  plan.newNumber,
			CarriedTasks:     plan.carriedTasks,
			ArchivePath:      archivePath,
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to advance sprint: %w", err)
	}

	return &result, nil
}

// planSprintAdvance validates preconditions and computes all derived values
// for a sprint advance without mutating state. Returns a plan that can be
// applied via applySprintAdvance.
func planSprintAdvance(s *models.State, now time.Time, projectRoot string) (*sprintAdvancePlan, error) {
	if s.Sprint.Status != models.SprintStatusCheckpoint {
		return nil, fmt.Errorf("cannot advance sprint: status is %s, expected CHECKPOINT", s.Sprint.Status)
	}
	allTerminal, termErr := allPlannedTasksTerminalForProject(s, projectRoot)
	if termErr != nil {
		return nil, fmt.Errorf("cannot advance sprint: %w", termErr)
	}
	if !allTerminal {
		return nil, fmt.Errorf("cannot advance sprint: not all planned tasks are terminal")
	}

	planningPairs, ppErr := loadPlanningPairsForAdvance(projectRoot)
	if ppErr != nil {
		return nil, fmt.Errorf("cannot advance sprint: %w", ppErr)
	}
	return buildSprintAdvancePlan(s, now, planningPairs)
}

// applySprintAdvance mutates state to record the completed sprint in history
// and create a new sprint. Must be called only after the archive file has been
// successfully written.
func applySprintAdvance(s *models.State, plan *sprintAdvancePlan) {
	s.SprintHistory = append(s.SprintHistory, models.SprintSummary{
		ID:        plan.archivedSprint.ID,
		Number:    plan.archivedSprint.Number,
		Status:    models.SprintStatusCompleted,
		Started:   plan.archivedSprint.Timeline.Started,
		Ended:     *plan.archivedSprint.Timeline.Ended,
		TasksDone: plan.archivedSprint.Metrics.TasksDone,
	})

	s.Sprint = models.Sprint{
		ID:      plan.newSprintID,
		Number:  plan.newNumber,
		GoalRef: s.Goal.ID,
		Scope: models.SprintScope{
			Planned: plan.carriedTasks,
			Stretch: []string{},
		},
		Timeline: models.SprintTimeline{
			Started: *plan.archivedSprint.Timeline.Ended,
		},
		Status:  models.SprintStatusInProgress,
		Metrics: models.SprintMetrics{},
	}
}

// writeSprintArchive writes the full sprint struct to a YAML archive file.
func writeSprintArchive(archivePath string, sprint *models.Sprint) error {
	data, err := yaml.Marshal(sprint)
	if err != nil {
		return fmt.Errorf("failed to marshal sprint for archive: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(archivePath), 0755); err != nil {
		return fmt.Errorf("failed to create archive directory: %w", err)
	}

	if err := os.WriteFile(archivePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write archive file: %w", err)
	}

	return nil
}

// planSprintAdvanceFromCompleted validates preconditions and computes all
// derived values for advancing from a COMPLETED sprint. Unlike planSprintAdvance
// (which requires CHECKPOINT), this handles the sub-pipeline flow where the
// sprint was marked COMPLETED after all tasks reached sprint-terminal state.
func planSprintAdvanceFromCompleted(s *models.State, now time.Time, projectRoot string) (*sprintAdvancePlan, error) {
	if s.Sprint.Status != models.SprintStatusCompleted {
		return nil, fmt.Errorf("cannot advance sprint: status is %s, expected COMPLETED", s.Sprint.Status)
	}

	planningPairs, err := loadPlanningPairsForAdvance(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("cannot advance sprint: %w", err)
	}
	return buildSprintAdvancePlan(s, now, planningPairs)
}

// buildSprintAdvancePlan is the shared implementation for sprint advance planning.
// It snapshots the current sprint for archive, normalizes legacy numbering, and
// computes carried tasks. Callers validate preconditions before calling this.
//
// planningPairs identifies which role-pairs are transition sources (planning pairs).
// Merged planning tasks with unconsumed output are carried forward alongside
// non-terminal tasks so the orchestrator can fire PLANNING_COMPLETE in the new sprint.
func buildSprintAdvancePlan(s *models.State, now time.Time, planningPairs map[string]bool) (*sprintAdvancePlan, error) {
	archivedSprint := s.Sprint
	if archivedSprint.Number == 0 {
		archivedSprint.Number = 1
	}
	archivedSprint.Status = models.SprintStatusCompleted
	ended := now
	archivedSprint.Timeline.Ended = &ended

	newNumber := archivedSprint.Number + 1
	newSprintID := fmt.Sprintf("sprint-%d", newNumber)
	carriedTasks := collectNonTerminalTaskIDs(s)
	carriedTasks = append(carriedTasks, collectMergedPlanningWithUnconsumedOutput(s, planningPairs)...)

	return &sprintAdvancePlan{
		archivedSprint: archivedSprint,
		newSprintID:    newSprintID,
		newNumber:      newNumber,
		carriedTasks:   carriedTasks,
	}, nil
}

// collectNonTerminalTaskIDs returns IDs of tasks not in a complete state.
func collectNonTerminalTaskIDs(state *models.State) []string {
	var carried []string
	for _, task := range state.Tasks {
		if !task.Status.IsComplete() {
			carried = append(carried, task.ID)
		}
	}
	return carried
}

// IsUnconsumedPlanningOutput reports whether a task is a merged planning task
// with output that has not yet been expanded into child tasks. Used by both
// sprint advance (carry-forward) and orchestrator wake detection (PLANNING_COMPLETE).
func IsUnconsumedPlanningOutput(task *models.Task, planningPairs map[string]bool) bool {
	if task == nil || task.Status != models.TaskStatusMerged || len(task.Output) == 0 {
		return false
	}
	if len(task.TransitionsExecuted) > 0 {
		return false // transitions already fired — children exist
	}
	return IsPlanningPair(task.RolePair, planningPairs)
}

// IsTransitionCycleBlocked checks if a task has a transition_cycle_blocked history event.
func IsTransitionCycleBlocked(task *models.Task) bool {
	for _, h := range task.History {
		if h.Event == models.TaskEventTransitionCycleBlocked {
			return true
		}
	}
	return false
}

// HasCycleBlockedDependency checks whether any upstream dependency is
// cycle-blocked, transitively, from the current task graph.
func HasCycleBlockedDependency(task *models.Task, state *models.State) bool {
	if task == nil || state == nil {
		return false
	}

	memo := make(map[string]bool)
	visiting := make(map[string]bool)

	var blocked func(taskID string) bool
	blocked = func(taskID string) bool {
		if result, ok := memo[taskID]; ok {
			return result
		}
		if visiting[taskID] {
			return false
		}

		dep := state.FindTask(taskID)
		if dep == nil {
			memo[taskID] = false
			return false
		}
		if IsTransitionCycleBlocked(dep) {
			memo[taskID] = true
			return true
		}

		visiting[taskID] = true
		defer delete(visiting, taskID)

		for _, upstreamID := range dep.DependsOn {
			if blocked(upstreamID) {
				memo[taskID] = true
				return true
			}
		}

		memo[taskID] = false
		return false
	}

	for _, depID := range task.DependsOn {
		if blocked(depID) {
			return true
		}
	}
	return false
}

// IsPlanningCompleteEligible returns true if a task has unconsumed planning output
// AND is not cycle-blocked directly or transitively via an upstream dependency.
// Used by wake detection and prompt rendering to exclude cycle-blocked tasks from
// PLANNING_COMPLETE triggering.
// IsUnconsumedPlanningOutput remains unchanged for carry-forward, replan, and checkpoint.
func IsPlanningCompleteEligible(task *models.Task, planningPairs map[string]bool, state *models.State) bool {
	return IsUnconsumedPlanningOutput(task, planningPairs) &&
		!IsTransitionCycleBlocked(task) &&
		!HasCycleBlockedDependency(task, state)
}

// collectMergedPlanningWithUnconsumedOutput returns IDs of planned tasks with
// unconsumed planning output. These need to be carried into the new sprint so
// the orchestrator can fire PLANNING_COMPLETE.
//
// Iterates state.Sprint.Scope.Planned (not all tasks) to avoid reintroducing
// tasks from prior sprints.
func collectMergedPlanningWithUnconsumedOutput(state *models.State, planningPairs map[string]bool) []string {
	var carried []string
	for _, taskID := range state.Sprint.Scope.Planned {
		task := state.FindTask(taskID)
		if IsUnconsumedPlanningOutput(task, planningPairs) {
			carried = append(carried, taskID)
		}
	}
	return carried
}

// loadPlanningPairsForAdvance loads planning pairs from pipeline config.
// Returns (nil, nil) when the pipeline config is absent (legacy project) —
// IsPlanningPair falls back to recognizing "code-planning-pair" as the only
// planning pair.
// Returns a non-nil error when the config exists but cannot be loaded (parse or
// validation failure), preventing silent fallback that would drop non-legacy
// planning pairs.
func loadPlanningPairsForAdvance(projectRoot string) (map[string]bool, error) {
	detCtx, err := LoadDetectionContext(projectRoot)
	if err != nil {
		if errors.Is(err, pipeline.ErrConfigNotFound) {
			return nil, nil // legacy project — no pipeline config
		}
		return nil, fmt.Errorf("pipeline config failed to load: %w", err)
	}
	return detCtx.PlanningPairs, nil
}
