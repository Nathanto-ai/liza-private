package statevalidate

import (
	"fmt"

	"github.com/liza-mas/liza/internal/models"
)

// validateTaskQuality enforces the spec quality gate: tasks cannot be in
// IMPLEMENTING status without acceptance criteria and verification commands.
// This prevents work from starting on poorly-defined tasks.
func validateTaskQuality(state *models.State, _ string, _ bool) error {
	for _, task := range state.Tasks {
		if task.Status != models.TaskStatusImplementing {
			continue
		}

		if len(task.AcceptanceCriteria) == 0 {
			return fmt.Errorf(
				"task %s is IMPLEMENTING but has no acceptance_criteria (spec quality gate)",
				task.ID,
			)
		}

		if len(task.VerifyCommands) == 0 {
			return fmt.Errorf(
				"task %s is IMPLEMENTING but has no verify_commands (spec quality gate)",
				task.ID,
			)
		}
	}

	return nil
}

// validateAuditFindings checks that audit findings have required fields.
func validateAuditFindings(state *models.State, _ string, _ bool) error {
	for i, f := range state.AuditFindings {
		if f.ID == "" {
			return fmt.Errorf("audit_findings[%d]: missing required field 'id'", i)
		}
		if f.TaskID == "" {
			return fmt.Errorf("audit_findings[%d] (%s): missing required field 'task_id'", i, f.ID)
		}
		if !f.IsValidSeverity() {
			return fmt.Errorf("audit_findings[%d] (%s): invalid severity %q (must be HIGH, MEDIUM, or LOW)", i, f.ID, f.Severity)
		}
		if !f.IsValidType() {
			return fmt.Errorf("audit_findings[%d] (%s): invalid type %q", i, f.ID, f.Type)
		}
		if f.Evidence == "" {
			return fmt.Errorf("audit_findings[%d] (%s): missing required field 'evidence'", i, f.ID)
		}
	}
	return nil
}
