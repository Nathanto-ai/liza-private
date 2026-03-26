package prompts

import (
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// Regression tests for V&V Run 6 fixes — prompt layer.

// TestFix33_ReviewerPromptNoEditInstructions verifies the reviewer prompt
// contains explicit "DO NOT edit files" guidance.
// Bug: Reviewer used copilot built-in edit tool to modify source code (ISSUE-R6-04).
// Fix: Added ROLE BOUNDARY section to reviewer prompt.
func TestFix33_ReviewerPromptNoEditInstructions(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReadyForReview, now)
	task.Description = "Implement server"
	task.DoneWhen = "go test ./... passes"
	assignedTo := "coder-1"
	task.AssignedTo = &assignedTo
	baseCommit := "abc123"
	task.BaseCommit = &baseCommit
	reviewCommit := "def456"
	task.ReviewCommit = &reviewCommit
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
		"ROLE BOUNDARY",
		"MUST NOT create, edit, modify, or delete",
		"REJECT the task",
	}
	for _, phrase := range requiredPhrases {
		if !strings.Contains(prompt, phrase) {
			t.Errorf("Reviewer prompt missing no-edit phrase: %q", phrase)
		}
	}
}

// TestFix33_ReviewerPromptBuildArtifactChecklist verifies the reviewer prompt
// includes a build artifact check in the review instructions.
// Bug: Reviewer approved commit with coverage files and tmp backups (ISSUE-R6-07).
// Fix: Added build artifact checklist item to reviewer instructions.
func TestFix33_ReviewerPromptBuildArtifactChecklist(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReadyForReview, now)
	assignedTo := "coder-1"
	task.AssignedTo = &assignedTo
	baseCommit := "abc123"
	task.BaseCommit = &baseCommit
	reviewCommit := "def456"
	task.ReviewCommit = &reviewCommit
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

	if !strings.Contains(prompt, "Build artifact check") {
		t.Error("Reviewer prompt missing build artifact checklist item")
	}
	if !strings.Contains(prompt, ".gitignore") {
		t.Error("Reviewer prompt build artifact check should mention .gitignore")
	}
}

// TestFix34_CoderPromptTaskCompleteWarning verifies the coder prompt
// warns against calling task_complete instead of liza_submit_for_review,
// AND instructs to use task_complete AFTER submission to exit cleanly.
// Bug: Coder called task_complete (copilot built-in) instead of liza_submit_for_review,
// losing 5 commits of work (ISSUE-R6-05).
// Fix 34: Added explicit task_complete warning.
// Fix 46: Changed from "Do NOT call task_complete" to allow task_complete AFTER submission,
// preventing infinite task_complete loop in copilot autopilot mode.
func TestFix34_CoderPromptTaskCompleteWarning(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now)
	task.Description = "Implement tests"
	task.DoneWhen = "go test ./... passes"

	config := CoderContextConfig{
		ProjectRoot: "/project",
		AgentID:     "coder-1",
	}

	prompt, err := BuildCoderContext(&task, config)
	if err != nil {
		t.Fatalf("BuildCoderContext() error: %v", err)
	}

	requiredPhrases := []string{
		"task_complete",
		"LOSE your work",
		"liza_submit_for_review",
		"call task_complete to end the session",
	}
	for _, phrase := range requiredPhrases {
		if !strings.Contains(prompt, phrase) {
			t.Errorf("Coder prompt missing task_complete warning phrase: %q", phrase)
		}
	}

	// Verify prompt does NOT say "Do NOT call task_complete" (Fix 46 removed this)
	if strings.Contains(prompt, "Do NOT call \"task_complete\"") {
		t.Error("Coder prompt should NOT contain 'Do NOT call task_complete' (Fix 46: allow task_complete after submission)")
	}
}

// TestFix35_CoderPromptIncrementalCommits verifies the coder prompt
// includes guidance for incremental commits.
// Bug: Coder spent 19 min analyzing before writing any code (ISSUE-R6-06).
// Fix: Added incremental commit guidance.
func TestFix35_CoderPromptIncrementalCommits(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now)

	config := CoderContextConfig{
		ProjectRoot: "/project",
		AgentID:     "coder-1",
	}

	prompt, err := BuildCoderContext(&task, config)
	if err != nil {
		t.Fatalf("BuildCoderContext() error: %v", err)
	}

	if !strings.Contains(prompt, "Commit incrementally") {
		t.Error("Coder prompt missing incremental commit guidance")
	}
	if !strings.Contains(prompt, "Do NOT batch all changes") {
		t.Error("Coder prompt missing anti-batch commit guidance")
	}
}

// TestFix36_PlannerPromptGitignoreGuidance verifies the planner prompt
// includes .gitignore creation in the build hygiene gate.
// Bug: No .gitignore created, build artifacts committed (ISSUE-R6-07).
// Fix: Added build hygiene gate to planner self-validation.
func TestFix36_PlannerPromptGitignoreGuidance(t *testing.T) {
	t.Parallel()

	state := testhelpers.CreateValidState()

	config := PlannerContextConfig{}

	prompt, err := BuildPlannerContext(state, config)
	if err != nil {
		t.Fatalf("BuildPlannerContext() error: %v", err)
	}

	if !strings.Contains(prompt, ".gitignore") {
		t.Error("Planner prompt missing .gitignore guidance")
	}
	if !strings.Contains(prompt, "Build hygiene") {
		t.Error("Planner prompt missing build hygiene gate")
	}
}
