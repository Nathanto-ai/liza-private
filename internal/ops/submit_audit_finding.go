package ops

import (
	"fmt"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/errors"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
)

// AuditFindingInput holds the parameters for submitting an audit finding.
type AuditFindingInput struct {
	FindingID         string
	TaskID            string
	Severity          string // HIGH, MEDIUM, LOW
	Type              string // SPEC_MISMATCH, MISSING_TEST, MISSING_EDGE_CASE, QUALITY_ISSUE
	Phase             string // pre_execution, post_execution, post_merge
	Classification    string // LOG_ONLY, REMEDIATE_WITH_TASK, REOPEN_TASK, REPLAN_REQUIRED
	Evidence          string
	RecommendedAction string
	SpecReference     string
}

// AuditFindingResult contains the outcome of submitting an audit finding.
type AuditFindingResult struct {
	FindingID      string
	TaskID         string
	Severity       string
	Classification string
	TaskReopened   bool   // true if REOPEN_TASK caused MERGED → READY
	LinkedTaskID   string // set if REMEDIATE_WITH_TASK (placeholder for future use)
}

// SubmitAuditFinding atomically adds an audit finding to the project state.
// The finding references a task and contains advisory information for the
// supervisor to evaluate. No terminal I/O.
func SubmitAuditFinding(projectRoot string, input AuditFindingInput) (*AuditFindingResult, error) {
	if input.FindingID == "" {
		return nil, fmt.Errorf("finding_id is required")
	}
	if input.TaskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}
	if input.Severity == "" {
		return nil, fmt.Errorf("severity is required")
	}
	if input.Evidence == "" {
		return nil, fmt.Errorf("evidence is required")
	}
	if input.Type == "" {
		return nil, fmt.Errorf("type is required")
	}

	lp := paths.New(projectRoot)
	bb := db.For(lp.StatePath())

	// Validate the finding fields before writing
	finding := models.AuditFinding{
		ID:                input.FindingID,
		TaskID:            input.TaskID,
		Severity:          input.Severity,
		Type:              input.Type,
		Phase:             input.Phase,
		Classification:    input.Classification,
		Evidence:          input.Evidence,
		RecommendedAction: input.RecommendedAction,
		SpecReference:     input.SpecReference,
		Created:           time.Now().UTC(),
	}

	if !finding.IsValidSeverity() {
		return nil, fmt.Errorf("invalid severity %q (must be HIGH, MEDIUM, or LOW)", input.Severity)
	}
	if !finding.IsValidType() {
		return nil, fmt.Errorf("invalid type %q", input.Type)
	}
	if !finding.IsValidPhase() {
		return nil, fmt.Errorf("invalid phase %q (must be pre_execution, post_execution, or post_merge)", input.Phase)
	}
	if !finding.IsValidClassification() {
		return nil, fmt.Errorf("invalid classification %q (must be LOG_ONLY, REMEDIATE_WITH_TASK, REOPEN_TASK, or REPLAN_REQUIRED)", input.Classification)
	}

	var taskReopened bool

	err := bb.Modify(func(state *models.State) error {
		// Verify the referenced task exists
		task := state.FindTask(input.TaskID)
		if task == nil {
			return &errors.NotFoundError{Entity: "task", ID: input.TaskID}
		}

		// Check for duplicate finding ID
		for _, existing := range state.AuditFindings {
			if existing.ID == input.FindingID {
				return fmt.Errorf("audit finding %q already exists", input.FindingID)
			}
		}

		// Apply deterministic supervisor classification policy.
		// The auditor's classification is treated as a suggestion; the
		// control-plane makes the final decision based on severity, type, and context.
		repeats := countUnresolvedFindingsForTask(state.AuditFindings, input.TaskID)
		finding.Classification = ClassifyFinding(finding, task.Status, repeats)

		// Handle REOPEN_TASK classification: transition MERGED → READY
		if finding.Classification == "REOPEN_TASK" && task.Status == models.TaskStatusMerged {
			if !task.Status.CanTransition(models.TaskStatusReady) {
				return fmt.Errorf("cannot reopen task %q: MERGED → READY transition not allowed", input.TaskID)
			}
			task.Status = models.TaskStatusReady
			taskReopened = true
		}

		state.AuditFindings = append(state.AuditFindings, finding)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to submit audit finding: %w", err)
	}

	return &AuditFindingResult{
		FindingID:      input.FindingID,
		TaskID:         input.TaskID,
		Severity:       input.Severity,
		Classification: finding.Classification, // deterministic, may differ from input
		TaskReopened:   taskReopened,
	}, nil
}
