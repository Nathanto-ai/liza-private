// Package roles provides role name constants and mapping between runtime
// role names (used in agent config/CLI) and workflow role names (used in task definitions).
package roles

import "fmt"

// Runtime role names used in agent configuration, CLI, and supervisor.
// These are the hyphenated forms that appear in agent IDs like "code-reviewer-1".
const (
	RuntimeCoder        = "coder"
	RuntimeCodeReviewer = "code-reviewer"
	RuntimePlanner      = "planner"
	RuntimeAuditor      = "auditor"
)

// Workflow role names used in task workflow definitions.
// These are the underscore forms stored in models.
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

// ToWorkflow converts a runtime role name to its workflow equivalent.
// Returns error if the role is not recognized.
func ToWorkflow(runtimeRole string) (string, error) {
	if workflow, ok := runtimeToWorkflow[runtimeRole]; ok {
		return workflow, nil
	}
	return "", fmt.Errorf("unknown runtime role: %s", runtimeRole)
}

// ToRuntime converts a workflow role name to its runtime equivalent.
// Returns error if the role is not recognized.
func ToRuntime(workflowRole string) (string, error) {
	if runtime, ok := workflowToRuntime[workflowRole]; ok {
		return runtime, nil
	}
	return "", fmt.Errorf("unknown workflow role: %s", workflowRole)
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
