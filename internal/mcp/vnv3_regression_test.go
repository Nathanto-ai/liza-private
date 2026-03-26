package mcp

import "testing"

// Regression tests for V&V Run 3 fixes — MCP layer.

// TestFix14_AuditorExecAllowed verifies auditor role now has liza_exec access.
func TestFix14_AuditorExecAllowed(t *testing.T) {
	t.Parallel()

	t.Run("auditor role includes liza_exec", func(t *testing.T) {
		allowed := roleAllowedTools("auditor")
		if !allowed["liza_exec"] {
			t.Error("auditor role should have liza_exec in allowed tools")
		}
	})

	t.Run("auditor role retains existing tools", func(t *testing.T) {
		allowed := roleAllowedTools("auditor")
		for _, tool := range []string{"liza_submit_audit_finding", "liza_analyze", "liza_mark_blocked"} {
			if !allowed[tool] {
				t.Errorf("auditor role missing expected tool %q", tool)
			}
		}
	})
}

// TestFix14_AuditorExecReadOnlyEnforcement verifies read-only command filter.
func TestFix14_AuditorExecReadOnlyEnforcement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		allowed bool
	}{
		{"go test", "go test ./...", true},
		{"go vet", "go vet ./...", true},
		{"go build", "go build ./...", true},
		{"git log", "git log --oneline -5", true},
		{"git show", "git show HEAD", true},
		{"git diff", "git diff HEAD~1..HEAD", true},
		{"ls", "ls -la", true},
		{"cat", "cat main.go", true},
		{"find", "find . -name '*.go'", true},
		{"pipe allowed", "go test ./... | head -20", true},
		{"rm blocked", "rm -rf .", false},
		{"redirect blocked", "echo test > file.go", false},
		{"touch blocked", "touch newfile.go", false},
		{"mv blocked", "mv old.go new.go", false},
		{"cp blocked", "cp file.go copy.go", false},
		{"python blocked", "python script.py", false},
		{"go run blocked", "go run main.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isReadOnlyCommand(tt.command)
			if got != tt.allowed {
				t.Errorf("isReadOnlyCommand(%q) = %v, want %v", tt.command, got, tt.allowed)
			}
		})
	}
}

// TestFix15_CodeReviewerExecAllowed verifies code-reviewer has liza_exec.
func TestFix15_CodeReviewerExecAllowed(t *testing.T) {
	t.Parallel()

	allowed := roleAllowedTools("code-reviewer")
	if !allowed["liza_exec"] {
		t.Error("code-reviewer role should have liza_exec in allowed tools")
	}
}
