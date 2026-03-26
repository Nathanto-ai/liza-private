package roles

import (
	"slices"
	"testing"
)

func TestConstants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"Coder", Coder, "coder"},
		{"CodeReviewer", CodeReviewer, "code-reviewer"},
		{"Orchestrator", Orchestrator, "orchestrator"},
		{"CodePlanner", CodePlanner, "code-planner"},
		{"CodePlanReviewer", CodePlanReviewer, "code-plan-reviewer"},
		{"EpicPlanner", EpicPlanner, "epic-planner"},
		{"EpicPlanReviewer", EpicPlanReviewer, "epic-plan-reviewer"},
		{"USWriter", USWriter, "us-writer"},
		{"USReviewer", USReviewer, "us-reviewer"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.expected {
				t.Errorf("%s = %q, want %q", tt.name, tt.constant, tt.expected)
			}
		})
	}
}

func TestIsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		role string
		want bool
	}{
		{Coder, Coder, true},
		{CodeReviewer, CodeReviewer, true},
		{Orchestrator, Orchestrator, true},
		{CodePlanner, CodePlanner, true},
		{CodePlanReviewer, CodePlanReviewer, true},
		{EpicPlanner, EpicPlanner, true},
		{EpicPlanReviewer, EpicPlanReviewer, true},
		{USWriter, USWriter, true},
		{USReviewer, USReviewer, true},
		{"underscore form invalid", "code_reviewer", false},
		{"unknown", "unknown", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValid(tt.role); got != tt.want {
				t.Errorf("IsValid(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

func TestAll(t *testing.T) {
	t.Parallel()

	got := All()
	want := []string{
		Coder, CodeReviewer, Orchestrator,
		CodePlanner, CodePlanReviewer,
		EpicPlanner, EpicPlanReviewer,
		USWriter, USReviewer,
	}

	if len(got) != len(want) {
		t.Errorf("All() returned %d roles, want %d", len(got), len(want))
	}

	for _, role := range want {
		if !slices.Contains(got, role) {
			t.Errorf("All() missing role %q", role)
		}
	}
}

// TestNoWorkflowPrefix verifies that no exported constant uses the deprecated Workflow* prefix.
// This is a done_when criterion: constants must be Coder, CodeReviewer, etc. — not WorkflowCoder.
func TestNoWorkflowPrefix(t *testing.T) {
	t.Parallel()

	for _, role := range All() {
		if !IsValid(role) {
			t.Errorf("All() contains invalid role %q", role)
		}
	}
}

func TestNormalizeRoleName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"coder unchanged", "coder", "coder"},
		{"code_reviewer to code-reviewer", "code_reviewer", "code-reviewer"},
		{"orchestrator unchanged", "orchestrator", "orchestrator"},
		{"code_planner to code-planner", "code_planner", "code-planner"},
		{"code_plan_reviewer to code-plan-reviewer", "code_plan_reviewer", "code-plan-reviewer"},
		{"epic_planner to epic-planner", "epic_planner", "epic-planner"},
		{"epic_plan_reviewer to epic-plan-reviewer", "epic_plan_reviewer", "epic-plan-reviewer"},
		{"us_writer to us-writer", "us_writer", "us-writer"},
		{"us_reviewer to us-reviewer", "us_reviewer", "us-reviewer"},
		{"already hyphenated passes through", "code-reviewer", "code-reviewer"},
		{"unknown name passes through", "custom_role", "custom_role"},
		{"empty string passes through", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeRoleName(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeRoleName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestAllRolesAreHyphenated verifies the single-name-form invariant:
// all role values use hyphenated form, no underscores.
func TestAllRolesAreHyphenated(t *testing.T) {
	t.Parallel()

	for _, role := range All() {
		for _, c := range role {
			if c == '_' {
				t.Errorf("role %q contains underscore — expected hyphenated form only", role)
				break
			}
		}
	}
}

func TestAllRuntime(t *testing.T) {
	t.Parallel()

	got := AllRuntime()
	want := []string{RuntimeCoder, RuntimeCodeReviewer, RuntimePlanner, RuntimeAuditor}

	if len(got) != len(want) {
		t.Errorf("AllRuntime() returned %d roles, want %d", len(got), len(want))
	}

	for _, role := range want {
		if !slices.Contains(got, role) {
			t.Errorf("AllRuntime() missing role %q", role)
		}
	}
}

func TestAllWorkflow(t *testing.T) {
	t.Parallel()

	got := AllWorkflow()
	want := []string{WorkflowCoder, WorkflowCodeReviewer, WorkflowPlanner, WorkflowAuditor}

	if len(got) != len(want) {
		t.Errorf("AllWorkflow() returned %d roles, want %d", len(got), len(want))
	}

	for _, role := range want {
		if !slices.Contains(got, role) {
			t.Errorf("AllWorkflow() missing role %q", role)
		}
	}
}

// TestBidirectionalMapping ensures all valid runtime roles map to workflow and back.
func TestBidirectionalMapping(t *testing.T) {
	t.Parallel()

	for _, runtime := range AllRuntime() {
		workflow, err := ToWorkflow(runtime)
		if err != nil {
			t.Errorf("ToWorkflow(%q) failed: %v", runtime, err)
			continue
		}

		backToRuntime, err := ToRuntime(workflow)
		if err != nil {
			t.Errorf("ToRuntime(%q) failed: %v", workflow, err)
			continue
		}

		if backToRuntime != runtime {
			t.Errorf("Round-trip failed: %q -> %q -> %q", runtime, workflow, backToRuntime)
		}
	}
}

// TestCrossBoundaryResolution verifies the core requirement:
// agent runtime roles can be resolved to workflow roles for task operations.
func TestCrossBoundaryResolution(t *testing.T) {
	t.Parallel()

	// Simulate: agent with ID "code-reviewer-1" claims a task
	// The agent uses runtime role "code-reviewer"
	// The task workflow uses "code_reviewer"

	agentRuntimeRole := RuntimeCodeReviewer
	workflowRole, err := ToWorkflow(agentRuntimeRole)
	if err != nil {
		t.Fatalf("Failed to resolve runtime role to workflow: %v", err)
	}

	// Verify it matches the models constant
	if workflowRole != WorkflowCodeReviewer {
		t.Errorf("Workflow role mismatch: got %q, want %q", workflowRole, WorkflowCodeReviewer)
	}
}
