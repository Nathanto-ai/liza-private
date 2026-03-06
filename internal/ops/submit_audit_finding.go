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
	Evidence          string
	RecommendedAction string
	SpecReference     string
}

// AuditFindingResult contains the outcome of submitting an audit finding.
type AuditFindingResult struct {
	FindingID string
	TaskID    string
	Severity  string
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
		Evidence:          input.Evidence,
		RecommendedAction: input.RecommendedAction,
		SpecReference:     input.SpecReference,
		Created:           time.Now().UTC(),
	}

	if !finding.IsValidSeverity() {
		return nil, fmt.Errorf("invalid severity %q (must be HIGH, MEDIUM, or LOW)", input.Severity)
	}
	if !finding.IsValidType() {
		return nil, fmt.Errorf("invalid type %q (must be SPEC_MISMATCH, MISSING_TEST, MISSING_EDGE_CASE, or QUALITY_ISSUE)", input.Type)
	}

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

		state.AuditFindings = append(state.AuditFindings, finding)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to submit audit finding: %w", err)
	}

	return &AuditFindingResult{
		FindingID: input.FindingID,
		TaskID:    input.TaskID,
		Severity:  input.Severity,
	}, nil
}
