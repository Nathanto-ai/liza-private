package ops

import "github.com/liza-mas/liza/internal/models"

// ClassifyFinding applies deterministic supervisor policy to decide the
// control-plane classification for an audit finding.  The auditor may
// suggest a classification, but this function is the source of truth.
//
// Policy rules (evaluated top-to-bottom, first match wins):
//
//  0. Remediation loop circuit breaker: if the finding targets a task that
//     is itself a remediation (has OriginFindingID) AND merge verification
//     passed, downgrade HIGH→LOG_ONLY to break infinite loops.
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
// isRemediationTask indicates whether the target task was itself created from
// an audit finding (has OriginFindingID set). When true and the task passed
// merge verification, HIGH findings are downgraded to LOG_ONLY to break
// remediation loops.
// remediationDepth tracks how many chained remediation tasks led to this point.
func ClassifyFinding(finding models.AuditFinding, taskStatus models.TaskStatus, repeatedFindings int, opts ...ClassifyOption) string {
	cfg := classifyConfig{}
	for _, o := range opts {
		o(&cfg)
	}

	// Circuit breaker: if this is a remediation task that passed merge verification
	// and the finding claims compilation/syntax errors, downgrade to LOG_ONLY.
	if cfg.isRemediationTask && taskStatus == models.TaskStatusMerged {
		return "LOG_ONLY"
	}

	// Remediation depth limit: if we are already at depth ≥ 1, cap at LOG_ONLY
	// to prevent unbounded task generation chains.
	if cfg.remediationDepth >= 1 && finding.Severity == "HIGH" {
		return "LOG_ONLY"
	}

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

// ClassifyOption configures optional parameters for ClassifyFinding.
type ClassifyOption func(*classifyConfig)

type classifyConfig struct {
	isRemediationTask bool
	remediationDepth  int
}

// WithRemediationTask indicates the target task originated from an audit finding.
func WithRemediationTask(isRemediation bool) ClassifyOption {
	return func(c *classifyConfig) {
		c.isRemediationTask = isRemediation
	}
}

// WithRemediationDepth sets the chain depth of remediation tasks.
func WithRemediationDepth(depth int) ClassifyOption {
	return func(c *classifyConfig) {
		c.remediationDepth = depth
	}
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

// computeRemediationDepth follows the chain of OriginFindingID → TaskID links
// to determine how deep in the remediation chain a task is.
// depth 0 = original task, depth 1 = first remediation, depth 2 = remediation of remediation, etc.
func computeRemediationDepth(task *models.Task, allTasks []models.Task, findings []models.AuditFinding) int {
	depth := 0
	current := task
	seen := make(map[string]bool) // cycle protection

	for current != nil && current.OriginFindingID != "" {
		if seen[current.ID] {
			break
		}
		seen[current.ID] = true
		depth++

		// Find the finding that originated this task
		var originFinding *models.AuditFinding
		for i := range findings {
			if findings[i].ID == current.OriginFindingID {
				originFinding = &findings[i]
				break
			}
		}
		if originFinding == nil {
			break
		}

		// Find the task that the finding was filed against
		var parentTask *models.Task
		for i := range allTasks {
			if allTasks[i].ID == originFinding.TaskID {
				parentTask = &allTasks[i]
				break
			}
		}
		current = parentTask
	}

	return depth
}
