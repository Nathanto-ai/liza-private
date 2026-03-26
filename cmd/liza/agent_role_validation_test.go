package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/liza-mas/liza/internal/roles"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// TestAgentCommandAcceptsAllRuntimeRoles ensures that the agent command
// accepts every role returned by roles.AllRuntime(). This prevents the
// scenario where a new role is added to roles.go but the CLI validation
// or help text is not updated.
//
// Root cause: The auditor role was added to roles.AllRuntime() but the
// error message on the agent command was hardcoded to "coder, code-reviewer,
// or planner". Future roles must also be wired here.
func TestAgentCommandAcceptsAllRuntimeRoles(t *testing.T) {
	for _, role := range roles.AllRuntime() {
		t.Run(role, func(t *testing.T) {
			projectRoot := t.TempDir()
			testhelpers.SetupTestGitRepo(t, projectRoot)
			testhelpers.SetupLizaDir(t, projectRoot)

			agentID := fmt.Sprintf("%s-1", role)

			// The agent command will fail eventually (no supervisor, no CLI binary),
			// but it must NOT fail with "invalid role". We only test the role
			// validation gate here — not the full supervisor loop.
			err := executeRootCommand(t, projectRoot,
				"agent", role,
				"--agent-id", agentID,
				"--cli", "claude",
			)

			if err == nil {
				// Unexpected success means the supervisor tried to start.
				// That's fine for validation purposes — role was accepted.
				return
			}

			errMsg := err.Error()

			// The role validation must pass. If we see "invalid role", the
			// CLI's allowed-role list is out of sync with roles.AllRuntime().
			if strings.Contains(errMsg, "invalid role") {
				t.Fatalf("role %q rejected by agent command: %v\n"+
					"roles.AllRuntime() = %v\n"+
					"FIX: Update the role validation in cmd/liza/main.go to use roles.AllRuntime()",
					role, err, roles.AllRuntime())
			}

			// Any other error is expected (e.g., supervisor can't start,
			// CLI not found, no tasks). The point is: the role was accepted.
			t.Logf("role %q accepted (subsequent error expected): %v", role, err)
		})
	}
}

// TestAgentCommandRejectsInvalidRole ensures unknown roles are rejected.
func TestAgentCommandRejectsInvalidRole(t *testing.T) {
	projectRoot := t.TempDir()
	testhelpers.SetupTestGitRepo(t, projectRoot)
	testhelpers.SetupLizaDir(t, projectRoot)

	err := executeRootCommand(t, projectRoot,
		"agent", "wizard",
		"--agent-id", "wizard-1",
		"--cli", "claude",
	)

	if err == nil {
		t.Fatal("expected error for invalid role 'wizard', got nil")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "invalid role") {
		// Could also fail at identity.ValidateRole — still a rejection.
		// Accept either "invalid role" or "agent ID role mismatch".
		if !strings.Contains(errMsg, "invalid") {
			t.Fatalf("expected 'invalid role' error for 'wizard', got: %v", err)
		}
	}
}

// TestAllRuntimeRolesHaveHelpTextMention verifies the agent command's help
// text mentions every valid role. This catches stale documentation when a
// new role is added.
func TestAllRuntimeRolesHaveHelpTextMention(t *testing.T) {
	helpText := agentCmd.Long

	for _, role := range roles.AllRuntime() {
		if !strings.Contains(helpText, role) {
			t.Errorf("agent command help text does not mention role %q\n"+
				"Help text:\n%s\n"+
				"FIX: Update the Long description in agentCmd (cmd/liza/main.go)",
				role, helpText)
		}
	}
}

// TestRolesAllRuntimeMatchesConstants ensures AllRuntime() stays in sync
// with the role constants. If a new RuntimeXxx constant is added but
// AllRuntime() is not updated, this test will catch it.
func TestRolesAllRuntimeMatchesConstants(t *testing.T) {
	allRoles := roles.AllRuntime()

	// Verify known roles are present
	expected := []string{
		roles.RuntimeCoder,
		roles.RuntimeCodeReviewer,
		roles.RuntimePlanner,
		roles.RuntimeAuditor,
	}

	for _, want := range expected {
		found := false
		for _, got := range allRoles {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("roles.AllRuntime() missing %q\n"+
				"AllRuntime() = %v\n"+
				"FIX: Add the role to AllRuntime() in internal/roles/roles.go",
				want, allRoles)
		}
	}

	// Verify AllRuntime() doesn't have extras not in constants
	knownSet := map[string]bool{
		roles.RuntimeCoder:        true,
		roles.RuntimeCodeReviewer: true,
		roles.RuntimePlanner:      true,
		roles.RuntimeAuditor:      true,
	}
	for _, role := range allRoles {
		if !knownSet[role] {
			t.Errorf("roles.AllRuntime() contains unexpected role %q — update this test's knownSet", role)
		}
	}
}
