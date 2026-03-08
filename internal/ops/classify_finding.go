package ops

import "github.com/liza-mas/liza/internal/models"

// ClassifyFinding applies deterministic supervisor policy to decide the
// control-plane classification for an audit finding.  The auditor may
// suggest a classification, but this function is the source of truth.
//
// Policy rules (evaluated top-to-bottom, first match wins):
//
//  1. HIGH severity + SPEC_MISMATCH on a MERGED task → REOPEN_TASK
//  2. HIGH severity + SPEC_MISMATCH (not merged)      → REPLAN_REQUIRED
//  3. HIGH severity (any other type)                   → REMEDIATE_WITH_TASK
//  4. MEDIUM severity + type ∈ {MISSING_TEST, MISSING_EDGE_CASE, SPEC_MISMATCH,
//     VERIFICATION_GAP, CAPABILITY_MISSING}             → REMEDIATE_WITH_TASK
//  5. MEDIUM severity + type ∈ {ARCHITECTURE_DEBT, SYSTEMIC_SPEC_DRIFT} → REPLAN_REQUIRED
//  6. LOW severity                                     → LOG_ONLY
//  7. Fallback: use auditor suggestion if provided, else LOG_ONLY.
//
// The function also accepts the auditor's suggested classification and the
// current task status so that REOPEN_TASK is only applied when the task is
// actually in MERGED state. repeatedFindings indicates how many unresolved
// findings already reference the same task — systemic repetitions escalate.
func ClassifyFinding(finding models.AuditFinding, taskStatus models.TaskStatus, repeatedFindings int) string {
	// Repeated findings on the same task escalate to REPLAN
	if repeatedFindings >= 3 {
		return "REPLAN_REQUIRED"
	}

	switch finding.Severity {
	case "HIGH":
		if finding.Type == "SPEC_MISMATCH" || finding.Type == "SYSTEMIC_SPEC_DRIFT" {
			if taskStatus == models.TaskStatusMerged {
				return "REOPEN_TASK"
			}
			return "REPLAN_REQUIRED"
		}
		return "REMEDIATE_WITH_TASK"

	case "MEDIUM":
		switch finding.Type {
		case "MISSING_TEST", "MISSING_EDGE_CASE", "SPEC_MISMATCH",
			"VERIFICATION_GAP", "CAPABILITY_MISSING":
			return "REMEDIATE_WITH_TASK"
		case "ARCHITECTURE_DEBT", "SYSTEMIC_SPEC_DRIFT":
			return "REPLAN_REQUIRED"
		case "QUALITY_ISSUE":
			return "LOG_ONLY"
		default:
			return "REMEDIATE_WITH_TASK"
		}

	case "LOW":
		return "LOG_ONLY"
	}

	// Fallback: trust auditor suggestion if provided
	if finding.Classification != "" {
		return finding.Classification
	}
	return "LOG_ONLY"
}

// countUnresolvedFindingsForTask returns the number of unresolved audit
// findings that reference the given task ID.
func countUnresolvedFindingsForTask(findings []models.AuditFinding, taskID string) int {
	count := 0
	for _, f := range findings {
		if f.TaskID == taskID && !f.Resolved {
			count++
		}
	}
	return count
}
