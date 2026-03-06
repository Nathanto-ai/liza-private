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
		{"RuntimeCoder", RuntimeCoder, "coder"},
		{"RuntimeCodeReviewer", RuntimeCodeReviewer, "code-reviewer"},
		{"RuntimePlanner", RuntimePlanner, "planner"},
		{"WorkflowCoder", WorkflowCoder, "coder"},
		{"WorkflowCodeReviewer", WorkflowCodeReviewer, "code_reviewer"},
		{"WorkflowPlanner", WorkflowPlanner, "planner"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.expected {
				t.Errorf("%s = %q, want %q", tt.name, tt.constant, tt.expected)
			}
		})
	}
}

func TestToWorkflow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		runtimeRole string
		want        string
		wantErr     bool
	}{
		{
			name:        "coder maps to coder",
			runtimeRole: RuntimeCoder,
			want:        WorkflowCoder,
			wantErr:     false,
		},
		{
			name:        "code-reviewer maps to code_reviewer",
			runtimeRole: RuntimeCodeReviewer,
			want:        WorkflowCodeReviewer,
			wantErr:     false,
		},
		{
			name:        "planner maps to planner",
			runtimeRole: RuntimePlanner,
			want:        WorkflowPlanner,
			wantErr:     false,
		},
		{
			name:        "unknown role returns error",
			runtimeRole: "unknown-role",
			want:        "",
			wantErr:     true,
		},
		{
			name:        "underscore form not valid runtime",
			runtimeRole: "code_reviewer",
			want:        "",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToWorkflow(tt.runtimeRole)
			if (err != nil) != tt.wantErr {
				t.Errorf("ToWorkflow(%q) error = %v, wantErr %v", tt.runtimeRole, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ToWorkflow(%q) = %q, want %q", tt.runtimeRole, got, tt.want)
			}
		})
	}
}

func TestToRuntime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		workflowRole string
		want         string
		wantErr      bool
	}{
		{
			name:         "coder maps to coder",
			workflowRole: WorkflowCoder,
			want:         RuntimeCoder,
			wantErr:      false,
		},
		{
			name:         "code_reviewer maps to code-reviewer",
			workflowRole: WorkflowCodeReviewer,
			want:         RuntimeCodeReviewer,
			wantErr:      false,
		},
		{
			name:         "planner maps to planner",
			workflowRole: WorkflowPlanner,
			want:         RuntimePlanner,
			wantErr:      false,
		},
		{
			name:         "unknown role returns error",
			workflowRole: "unknown_role",
			want:         "",
			wantErr:      true,
		},
		{
			name:         "hyphen form not valid workflow",
			workflowRole: "code-reviewer",
			want:         "",
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToRuntime(tt.workflowRole)
			if (err != nil) != tt.wantErr {
				t.Errorf("ToRuntime(%q) error = %v, wantErr %v", tt.workflowRole, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ToRuntime(%q) = %q, want %q", tt.workflowRole, got, tt.want)
			}
		})
	}
}

func TestIsValidRuntime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		role string
		want bool
	}{
		{RuntimeCoder, RuntimeCoder, true},
		{RuntimeCodeReviewer, RuntimeCodeReviewer, true},
		{RuntimePlanner, RuntimePlanner, true},
		{"code_reviewer", "code_reviewer", false},
		{"unknown", "unknown", false},
		{"", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidRuntime(tt.role); got != tt.want {
				t.Errorf("IsValidRuntime(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

func TestIsValidWorkflow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		role string
		want bool
	}{
		{WorkflowCoder, WorkflowCoder, true},
		{WorkflowCodeReviewer, WorkflowCodeReviewer, true},
		{WorkflowPlanner, WorkflowPlanner, true},
		{"code-reviewer", "code-reviewer", false},
		{"unknown", "unknown", false},
		{"", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidWorkflow(tt.role); got != tt.want {
				t.Errorf("IsValidWorkflow(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
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
