// Package roles provides unified role name constants used throughout the system.
// All roles use the hyphenated form (e.g. "code-reviewer") as the single canonical name.
package roles

import "fmt"

// Claim-type selectors used by ReleaseClaim to indicate which claim slot to release.
// These are NOT role names — a code-planner releases its claim with ClaimDoer,
// a code-plan-reviewer with ClaimReviewer.
const (
	ClaimDoer     = "doer"
	ClaimReviewer = "reviewer"
	ClaimBoth     = "both"
)

// Runtime role names used by agents at runtime.
const (
	RuntimeCoder        = "coder"
	RuntimeCodeReviewer = "code-reviewer"
	RuntimePlanner      = "planner"
	RuntimeAuditor      = "auditor"
)

// Unified role name constants. Single hyphenated form used everywhere.
const (
	Coder            = "coder"
	CodeReviewer     = "code-reviewer"
	Orchestrator     = "orchestrator"
	CodePlanner      = "code-planner"
	CodePlanReviewer = "code-plan-reviewer"
	EpicPlanner      = "epic-planner"
	EpicPlanReviewer = "epic-plan-reviewer"
	USWriter         = "us-writer"
	USReviewer       = "us-reviewer"
)

// Workflow role names used in task workflow definitions.
const (
	WorkflowCoder        = "coder"
	WorkflowCodeReviewer = "code_reviewer"
	WorkflowPlanner      = "planner"
	WorkflowAuditor      = "auditor"
)

// runtimeToWorkflow maps runtime role names to workflow role names.
var runtimeToWorkflow = map[string]string{
	RuntimeCoder:        WorkflowCoder,
	RuntimeCodeReviewer: WorkflowCodeReviewer,
	RuntimePlanner:      WorkflowPlanner,
	RuntimeAuditor:      WorkflowAuditor,
}

// workflowToRuntime maps workflow role names to runtime role names.
var workflowToRuntime = map[string]string{
	WorkflowCoder:        RuntimeCoder,
	WorkflowCodeReviewer: RuntimeCodeReviewer,
	WorkflowPlanner:      RuntimePlanner,
	WorkflowAuditor:      RuntimeAuditor,
}

// validRoles is the set of all valid role names.
var validRoles = map[string]bool{
	Coder:            true,
	CodeReviewer:     true,
	Orchestrator:     true,
	CodePlanner:      true,
	CodePlanReviewer: true,
	EpicPlanner:      true,
	EpicPlanReviewer: true,
	USWriter:         true,
	USReviewer:       true,
}

// IsValid checks if the given role is a valid role name.
func IsValid(role string) bool {
	return validRoles[role]
}

// All returns all valid role names.
func All() []string {
	return []string{
		Coder, CodeReviewer, Orchestrator,
		CodePlanner, CodePlanReviewer,
		EpicPlanner, EpicPlanReviewer,
		USWriter, USReviewer,
	}
}

// underscoreToHyphenated maps deprecated underscore-form role names to their
// canonical hyphenated form. Used only for migration/normalization.
var underscoreToHyphenated = map[string]string{
	"coder":              Coder,
	"code_reviewer":      CodeReviewer,
	"orchestrator":       Orchestrator,
	"code_planner":       CodePlanner,
	"code_plan_reviewer": CodePlanReviewer,
	"epic_planner":       EpicPlanner,
	"epic_plan_reviewer": EpicPlanReviewer,
	"us_writer":          USWriter,
	"us_reviewer":        USReviewer,
}

// NormalizeRoleName converts a known underscore-form role name to its
// canonical hyphenated form. Unknown names are returned unchanged.
func NormalizeRoleName(name string) string {
	if normalized, ok := underscoreToHyphenated[name]; ok {
		return normalized
	}
	return name
}

// IsValidRuntime checks if the given role is a valid runtime role.
func IsValidRuntime(role string) bool {
	_, ok := runtimeToWorkflow[role]
	return ok
}

// IsValidWorkflow checks if the given role is a valid workflow role.
func IsValidWorkflow(role string) bool {
	_, ok := workflowToRuntime[role]
	return ok
}

// AllRuntime returns all valid runtime role names.
func AllRuntime() []string {
	return []string{RuntimeCoder, RuntimeCodeReviewer, RuntimePlanner, RuntimeAuditor}
}

// AllWorkflow returns all valid workflow role names.
func AllWorkflow() []string {
	return []string{WorkflowCoder, WorkflowCodeReviewer, WorkflowPlanner, WorkflowAuditor}
}

// ToWorkflow converts a runtime role to its workflow role.
func ToWorkflow(role string) (string, error) {
	workflow, ok := runtimeToWorkflow[role]
	if !ok {
		return "", fmt.Errorf("unknown runtime role: %s", role)
	}
	return workflow, nil
}

// ToRuntime converts a workflow role to its runtime role.
func ToRuntime(role string) (string, error) {
	runtime, ok := workflowToRuntime[role]
	if !ok {
		return "", fmt.Errorf("unknown workflow role: %s", role)
	}
	return runtime, nil
}
