package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
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
		plan, err := planSprintAdvance(s, time.Now().UTC())
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
func planSprintAdvance(s *models.State, now time.Time) (*sprintAdvancePlan, error) {
	if s.Sprint.Status != models.SprintStatusCheckpoint {
		return nil, fmt.Errorf("cannot advance sprint: status is %s, expected CHECKPOINT", s.Sprint.Status)
	}
	if !s.AllPlannedTasksTerminal() {
		return nil, fmt.Errorf("cannot advance sprint: not all planned tasks are terminal")
	}

	// Snapshot current sprint for archive. Normalize legacy Number=0 to 1
	// so the archive and history entry pass validation (Number >= 1).
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

	return &sprintAdvancePlan{
		archivedSprint: archivedSprint,
		newSprintID:    newSprintID,
		newNumber:      newNumber,
		carriedTasks:   carriedTasks,
	}, nil
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
