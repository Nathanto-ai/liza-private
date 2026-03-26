package commands

import (
	"testing"

	"github.com/liza-mas/liza/internal/roles"
)

func TestRequireRole_AllowedRole(t *testing.T) {
	t.Parallel()
	err := RequireRole("coder-1", roles.RuntimeCoder)
	if err != nil {
		t.Fatalf("expected nil error for allowed role, got: %v", err)
	}
}

func TestRequireRole_DisallowedRole(t *testing.T) {
	t.Parallel()
	err := RequireRole("auditor-1", roles.RuntimeCoder)
	if err == nil {
		t.Fatal("expected error for disallowed role, got nil")
	}
}

func TestRequireRole_EmptyAgentID_SkipsCheck(t *testing.T) {
	t.Parallel()
	err := RequireRole("", roles.RuntimeCoder)
	if err != nil {
		t.Fatalf("expected nil error for empty agent ID (manual mode), got: %v", err)
	}
}

func TestRequireRole_MultipleAllowed(t *testing.T) {
	t.Parallel()
	err := RequireRole("code-reviewer-1", roles.RuntimeCoder, roles.RuntimeCodeReviewer)
	if err != nil {
		t.Fatalf("expected nil for code-reviewer in multi-role allowlist, got: %v", err)
	}
}

func TestRequireRole_MultipleAllowed_Rejected(t *testing.T) {
	t.Parallel()
	err := RequireRole("planner-1", roles.RuntimeCoder, roles.RuntimeCodeReviewer)
	if err == nil {
		t.Fatal("expected error for planner in coder+reviewer allowlist, got nil")
	}
}

func TestRequireRole_InvalidAgentIDFormat(t *testing.T) {
	t.Parallel()
	err := RequireRole("invalid", roles.RuntimeCoder)
	if err == nil {
		t.Fatal("expected error for invalid agent ID format, got nil")
	}
}

func TestMutationRoles_CoderOnlyCommands(t *testing.T) {
	t.Parallel()
	coderOnly := []string{"claim-task", "submit-for-review", "handoff", "write-checkpoint"}
	for _, cmd := range coderOnly {
		allowed, ok := MutationRoles[cmd]
		if !ok {
			t.Errorf("command %q not in MutationRoles", cmd)
			continue
		}
		if len(allowed) != 1 || allowed[0] != roles.RuntimeCoder {
			t.Errorf("command %q should be coder-only, got %v", cmd, allowed)
		}
	}
}

func TestMutationRoles_ReviewerOnlyCommands(t *testing.T) {
	t.Parallel()
	reviewerOnly := []string{"submit-verdict", "wt-merge", "claim-review", "clear-stale-review-claims"}
	for _, cmd := range reviewerOnly {
		allowed, ok := MutationRoles[cmd]
		if !ok {
			t.Errorf("command %q not in MutationRoles", cmd)
			continue
		}
		if len(allowed) != 1 || allowed[0] != roles.RuntimeCodeReviewer {
			t.Errorf("command %q should be code-reviewer-only, got %v", cmd, allowed)
		}
	}
}

func TestMutationRoles_PlannerOnlyCommands(t *testing.T) {
	t.Parallel()
	plannerOnly := []string{"add-task", "supersede-task", "sprint-checkpoint", "delete-agent", "update-sprint-metrics"}
	for _, cmd := range plannerOnly {
		allowed, ok := MutationRoles[cmd]
		if !ok {
			t.Errorf("command %q not in MutationRoles", cmd)
			continue
		}
		if len(allowed) != 1 || allowed[0] != roles.RuntimePlanner {
			t.Errorf("command %q should be planner-only, got %v", cmd, allowed)
		}
	}
}

func TestMutationRoles_NonCoderCannotClaimTask(t *testing.T) {
	t.Parallel()
	for _, role := range []string{"auditor-1", "planner-1", "code-reviewer-1"} {
		err := RequireRole(role, MutationRoles["claim-task"]...)
		if err == nil {
			t.Errorf("expected error for %s calling claim-task, got nil", role)
		}
	}
}

func TestMutationRoles_NonCoderCannotSubmitForReview(t *testing.T) {
	t.Parallel()
	for _, role := range []string{"auditor-1", "planner-1", "code-reviewer-1"} {
		err := RequireRole(role, MutationRoles["submit-for-review"]...)
		if err == nil {
			t.Errorf("expected error for %s calling submit-for-review, got nil", role)
		}
	}
}

func TestMutationRoles_NonReviewerCannotSubmitVerdict(t *testing.T) {
	t.Parallel()
	for _, role := range []string{"auditor-1", "planner-1", "coder-1"} {
		err := RequireRole(role, MutationRoles["submit-verdict"]...)
		if err == nil {
			t.Errorf("expected error for %s calling submit-verdict, got nil", role)
		}
	}
}

func TestMutationRoles_NonPlannerCannotAddTask(t *testing.T) {
	t.Parallel()
	for _, role := range []string{"auditor-1", "coder-1", "code-reviewer-1"} {
		err := RequireRole(role, MutationRoles["add-task"]...)
		if err == nil {
			t.Errorf("expected error for %s calling add-task, got nil", role)
		}
	}
}

func TestMutationRoles_CoderRetainsAllCapabilities(t *testing.T) {
	t.Parallel()
	coderCommands := []string{"claim-task", "submit-for-review", "handoff", "write-checkpoint",
		"wt-create", "wt-delete", "mark-blocked", "release-claim"}
	for _, cmd := range coderCommands {
		allowed, ok := MutationRoles[cmd]
		if !ok {
			t.Errorf("command %q not in MutationRoles", cmd)
			continue
		}
		err := RequireRole("coder-1", allowed...)
		if err != nil {
			t.Errorf("coder should be allowed for %q, got: %v", cmd, err)
		}
	}
}
