// Package planner provides task creation policies and controls for converting
// auditor findings into new tasks while preventing runaway task generation.
package planner

import (
	"fmt"
	"strings"

	"github.com/liza-mas/liza/internal/models"
)

// DefaultMaxTasksPerRun is the default maximum tasks that can be generated from
// findings in a single run.
const DefaultMaxTasksPerRun = 10

// DefaultMaxIterationsPerTask is the default maximum iterations per task.
const DefaultMaxIterationsPerTask = 5

// TaskProposal represents a potential task to be created from an audit finding.
type TaskProposal struct {
	FindingID    string
	TaskID       string
	Description  string
	SpecRef      string
	DoneWhen     string
	Scope        string
	Priority     int
	OriginTaskID string
}

// TaskCreationPolicy controls how findings are converted into tasks.
type TaskCreationPolicy struct {
	MaxTasksPerRun       int
	MaxIterationsPerTask int
}

// DefaultPolicy returns a policy with sensible defaults.
func DefaultPolicy() TaskCreationPolicy {
	return TaskCreationPolicy{
		MaxTasksPerRun:       DefaultMaxTasksPerRun,
		MaxIterationsPerTask: DefaultMaxIterationsPerTask,
	}
}

// PolicyFromConfig creates a policy from the state config, falling back to defaults.
func PolicyFromConfig(config models.Config) TaskCreationPolicy {
	p := DefaultPolicy()
	if config.MaxTasksPerRun > 0 {
		p.MaxTasksPerRun = config.MaxTasksPerRun
	}
	return p
}

// EvaluateFinding determines if an audit finding warrants a new task.
// Returns nil if the finding doesn't meet the task creation criteria:
// - Finding must reference a spec clause
// - Finding must have evidence
// - Finding must have a recommended action (bounded scope)
func EvaluateFinding(finding models.AuditFinding) *TaskProposal {
	if finding.SpecReference == "" {
		return nil // no spec reference
	}
	if finding.Evidence == "" {
		return nil // no evidence
	}
	if finding.RecommendedAction == "" {
		return nil // no bounded scope/action
	}
	if finding.Resolved {
		return nil // already resolved
	}

	return &TaskProposal{
		FindingID:    finding.ID,
		TaskID:       fmt.Sprintf("fix-%s", finding.ID),
		Description:  fmt.Sprintf("Fix %s: %s", finding.Type, finding.RecommendedAction),
		SpecRef:      finding.SpecReference,
		DoneWhen:     fmt.Sprintf("Finding %s resolved: %s", finding.ID, finding.Evidence),
		Scope:        finding.RecommendedAction,
		Priority:     severityToPriority(finding.Severity),
		OriginTaskID: finding.TaskID,
	}
}

// DeduplicateTasks removes proposals that duplicate existing non-terminal tasks
// with matching description or spec_ref and origin_finding_id.
func DeduplicateTasks(proposals []TaskProposal, existingTasks []models.Task) []TaskProposal {
	// Build lookup of existing non-terminal tasks by origin_finding_id
	existingByFinding := make(map[string]bool)
	existingByDesc := make(map[string]bool)
	for _, task := range existingTasks {
		if task.Status.IsTerminal() {
			continue
		}
		if task.OriginFindingID != "" {
			existingByFinding[task.OriginFindingID] = true
		}
		existingByDesc[strings.ToLower(task.Description)] = true
	}

	var unique []TaskProposal
	seen := make(map[string]bool)

	for _, p := range proposals {
		// Skip if a task for this finding already exists
		if existingByFinding[p.FindingID] {
			continue
		}
		// Skip if a task with the same description exists
		if existingByDesc[strings.ToLower(p.Description)] {
			continue
		}
		// Skip duplicates within the batch
		if seen[p.FindingID] {
			continue
		}
		seen[p.FindingID] = true
		unique = append(unique, p)
	}

	return unique
}

// CountFindingOriginatedTasks counts how many non-terminal tasks in the given
// list were originated from audit findings (have OriginFindingID set).
func CountFindingOriginatedTasks(tasks []models.Task) int {
	count := 0
	for _, t := range tasks {
		if t.OriginFindingID != "" && !t.Status.IsTerminal() {
			count++
		}
	}
	return count
}

// CheckBudget returns an error if creating another finding-originated task
// would exceed the per-run limit.
func CheckBudget(tasks []models.Task, maxPerRun int) error {
	if maxPerRun <= 0 {
		maxPerRun = DefaultMaxTasksPerRun
	}
	current := CountFindingOriginatedTasks(tasks)
	if current >= maxPerRun {
		return fmt.Errorf("task creation budget exceeded: %d/%d finding-originated tasks already exist", current, maxPerRun)
	}
	return nil
}

func severityToPriority(severity string) int {
	switch severity {
	case "HIGH":
		return 1
	case "MEDIUM":
		return 2
	case "LOW":
		return 3
	default:
		return 2
	}
}
