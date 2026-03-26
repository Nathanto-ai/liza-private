package prompts

import (
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// Regression tests for V&V Run 5 fixes — prompt layer.

// TestFix28_AuditorContextIncludesIntegrationBranch verifies that the auditor
// prompt template includes the integration branch when configured.
// Bug: Auditor verified on master but merged code was on integration branch,
// causing false "no packages" findings that triggered unnecessary remediation.
func TestFix28_AuditorContextIncludesIntegrationBranch(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	state.Config.IntegrationBranch = "integration"
	mergeCommit := "abc123"
	state.Tasks = []models.Task{
		{
			ID:          "task-merged",
			Description: "Implement feature",
			Status:      models.TaskStatusMerged,
			MergeCommit: &mergeCommit,
			Priority:    1,
			DoneWhen:    "tests pass",
			Created:     now,
			History:     []models.TaskHistoryEntry{},
		},
	}

	config := AuditorContextConfig{
		ProjectRoot:       "/project",
		AgentID:           "auditor-1",
		IntegrationBranch: "integration",
	}

	prompt, err := BuildAuditorContext(state, config)
	if err != nil {
		t.Fatalf("BuildAuditorContext() error: %v", err)
	}

	if !strings.Contains(prompt, "INTEGRATION BRANCH: integration") {
		t.Error("Auditor prompt should display integration branch")
	}
	if !strings.Contains(prompt, "git checkout integration") {
		t.Error("Auditor prompt should instruct checkout of integration branch for verification")
	}
}

// TestFix28_AuditorContextLanguageAgnostic verifies that the auditor prompt
// does not hardcode Go-specific commands.
// Bug: Auditor template hardcoded "go vet ./..." and "go test ./..." instead of
// being language-agnostic.
func TestFix28_AuditorContextLanguageAgnostic(t *testing.T) {
	t.Parallel()

	state := testhelpers.CreateValidState()

	config := AuditorContextConfig{
		ProjectRoot: "/project",
		AgentID:     "auditor-1",
	}

	prompt, err := BuildAuditorContext(state, config)
	if err != nil {
		t.Fatalf("BuildAuditorContext() error: %v", err)
	}

	// The template should no longer hardcode Go commands
	if strings.Contains(prompt, `"go vet ./..."`) {
		t.Error("Auditor prompt should not hardcode 'go vet' — should be language-agnostic")
	}
	if strings.Contains(prompt, `"go test ./... -count=1"`) {
		t.Error("Auditor prompt should not hardcode 'go test' — should be language-agnostic")
	}

	// Should reference project type detection instead
	if !strings.Contains(prompt, "detect the project type") {
		t.Error("Auditor prompt should include project type detection guidance")
	}
}

// TestFix29_ReviewerPromptEnvironmentConstraints verifies the reviewer template
// includes environment constraints that prevent adding flags not in the spec.
// Bug: Reviewer independently tried -race flag (not in spec), which failed because
// CGO/C compiler was unavailable. This caused 3 invalid rejections.
func TestFix29_ReviewerPromptEnvironmentConstraints(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReadyForReview, now)
	task.Description = "Implement storage"
	task.DoneWhen = "go test ./... passes"
	assignedTo := "coder-1"
	task.AssignedTo = &assignedTo
	baseCommit := "abc123"
	task.BaseCommit = &baseCommit
	reviewCommit := "def456"
	task.ReviewCommit = &reviewCommit
	task.Iteration = 1
	worktree := ".worktrees/task-1"
	task.Worktree = &worktree

	config := ReviewerContextConfig{
		ProjectRoot: "/project",
		AgentID:     "code-reviewer-1",
	}

	prompt, err := BuildReviewerContext(&task, config)
	if err != nil {
		t.Fatalf("BuildReviewerContext() error: %v", err)
	}

	requiredPhrases := []string{
		"ENVIRONMENT CONSTRAINTS",
		"Do NOT require -race",
		"done_when criteria",
	}

	for _, phrase := range requiredPhrases {
		if !strings.Contains(prompt, phrase) {
			t.Errorf("Reviewer prompt missing environment constraint phrase: %s", phrase)
		}
	}
}
