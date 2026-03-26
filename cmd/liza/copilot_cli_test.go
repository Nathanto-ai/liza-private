package main

import (
	"strings"
	"testing"

	"github.com/liza-mas/liza/internal/testhelpers"
)

// TestAgentCommandAcceptsCopilotCLI verifies that --cli copilot is accepted.
func TestAgentCommandAcceptsCopilotCLI(t *testing.T) {
	projectRoot := t.TempDir()
	testhelpers.SetupTestGitRepo(t, projectRoot)
	testhelpers.SetupLizaDir(t, projectRoot)

	err := executeRootCommand(t, projectRoot,
		"agent", "coder",
		"--agent-id", "coder-1",
		"--cli", "copilot",
	)

	if err == nil {
		// Supervisor tried to start — CLI accepted
		return
	}

	errMsg := err.Error()
	if strings.Contains(errMsg, "invalid CLI") {
		t.Fatalf("copilot should be accepted as CLI: %v", err)
	}
	// Other errors (e.g., gh not found, no tasks) are expected
	t.Logf("copilot CLI accepted (subsequent error expected): %v", err)
}

// TestAgentCommandRejectsInvalidCLI ensures unknown CLIs are rejected.
func TestAgentCommandRejectsInvalidCLI(t *testing.T) {
	projectRoot := t.TempDir()
	testhelpers.SetupTestGitRepo(t, projectRoot)
	testhelpers.SetupLizaDir(t, projectRoot)

	err := executeRootCommand(t, projectRoot,
		"agent", "coder",
		"--agent-id", "coder-1",
		"--cli", "chatgpt",
	)

	if err == nil {
		t.Fatal("expected error for invalid CLI 'chatgpt', got nil")
	}

	if !strings.Contains(err.Error(), "invalid CLI") {
		t.Fatalf("expected 'invalid CLI' error, got: %v", err)
	}
}

// TestAgentCommandModelFlagOnlyForCopilot verifies --model is rejected for non-copilot CLIs.
func TestAgentCommandModelFlagOnlyForCopilot(t *testing.T) {
	projectRoot := t.TempDir()
	testhelpers.SetupTestGitRepo(t, projectRoot)
	testhelpers.SetupLizaDir(t, projectRoot)

	err := executeRootCommand(t, projectRoot,
		"agent", "coder",
		"--agent-id", "coder-1",
		"--cli", "claude",
		"--model", "gpt-5-mini",
	)

	if err == nil {
		t.Fatal("expected error for --model with non-copilot CLI")
	}

	if !strings.Contains(err.Error(), "--model is only supported with --cli copilot") {
		t.Fatalf("expected model-copilot-only error, got: %v", err)
	}
}

// TestAgentCommandCopilotWithModelOverride verifies --model works with --cli copilot.
func TestAgentCommandCopilotWithModelOverride(t *testing.T) {
	projectRoot := t.TempDir()
	testhelpers.SetupTestGitRepo(t, projectRoot)
	testhelpers.SetupLizaDir(t, projectRoot)

	err := executeRootCommand(t, projectRoot,
		"agent", "coder",
		"--agent-id", "coder-1",
		"--cli", "copilot",
		"--model", "claude-opus-4.6",
	)

	if err == nil {
		return // supervisor started, that's fine
	}

	errMsg := err.Error()
	// Should NOT fail with "invalid CLI" or "--model is only supported"
	if strings.Contains(errMsg, "invalid CLI") || strings.Contains(errMsg, "--model is only supported") {
		t.Fatalf("copilot with --model should be accepted: %v", err)
	}
	t.Logf("copilot with model accepted (subsequent error expected): %v", err)
}

// TestAgentCommandCopilotAllRoles verifies copilot works with all runtime roles.
func TestAgentCommandCopilotAllRoles(t *testing.T) {
	roles := []string{"coder", "code-reviewer", "planner", "auditor"}
	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			projectRoot := t.TempDir()
			testhelpers.SetupTestGitRepo(t, projectRoot)
			testhelpers.SetupLizaDir(t, projectRoot)

			agentID := role + "-1"
			err := executeRootCommand(t, projectRoot,
				"agent", role,
				"--agent-id", agentID,
				"--cli", "copilot",
			)

			if err == nil {
				return
			}

			errMsg := err.Error()
			if strings.Contains(errMsg, "invalid CLI") || strings.Contains(errMsg, "invalid role") {
				t.Fatalf("copilot should work with role %s: %v", role, err)
			}
			t.Logf("role %s with copilot accepted (subsequent error expected): %v", role, err)
		})
	}
}
