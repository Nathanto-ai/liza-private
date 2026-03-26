package commands

import (
	"fmt"

	"github.com/liza-mas/liza/internal/ops"
)

// SubmitAuditFindingCommand submits an audit finding and prints the result to stdout.
// Delegates business logic to ops.SubmitAuditFinding.
func SubmitAuditFindingCommand(projectRoot string, input ops.AuditFindingInput) error {
	result, err := ops.SubmitAuditFinding(projectRoot, input)
	if err != nil {
		return fmt.Errorf("submit audit finding: %w", err)
	}

	fmt.Printf("Finding submitted: %s\n", result.FindingID)
	fmt.Printf("  task_id:        %s\n", result.TaskID)
	fmt.Printf("  severity:       %s\n", result.Severity)
	fmt.Printf("  classification: %s\n", result.Classification)
	if result.TaskReopened {
		fmt.Printf("  task_reopened:   true (MERGED → READY)\n")
	}
	return nil
}
