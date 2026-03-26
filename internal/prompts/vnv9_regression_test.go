package prompts

import (
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// Regression tests for V&V Run 9 fixes — prompt layer.

// TestFix46_CoderPromptAllowsTaskCompleteAfterSubmission verifies the coder
// prompt instructs agents to call task_complete AFTER liza_submit_for_review,
// rather than forbidding task_complete entirely.
// Bug: Copilot in autopilot mode has no exit mechanism except task_complete.
// Forbidding task_complete caused infinite loops (20+ iterations) after submission.
// Fix: Changed prompt to allow task_complete AFTER submission to cleanly exit.
func TestFix46_CoderPromptAllowsTaskCompleteAfterSubmission(t *testing.T) {
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

	// Must instruct calling task_complete to end the session
	if !strings.Contains(prompt, "call task_complete to end the session") {
		t.Error("Coder prompt must instruct to call task_complete after submission")
	}

	// Must NOT forbid task_complete entirely (old behavior caused infinite loop)
	if strings.Contains(prompt, "Do NOT call \"task_complete\"") {
		t.Error("Coder prompt must NOT forbid task_complete (causes infinite loop in autopilot)")
	}

	// Must still require liza_submit_for_review as the mandatory submission step
	if !strings.Contains(prompt, "MUST call liza_submit_for_review BEFORE") {
		t.Error("Coder prompt must require liza_submit_for_review before task_complete")
	}

	// Must warn that task_complete alone loses work
	if !strings.Contains(prompt, "LOSE your work") {
		t.Error("Coder prompt must warn that task_complete alone loses work")
	}
}

// TestFix46_ReviewerPromptExitsViaTaskComplete verifies the reviewer prompt
// also instructs agents to call task_complete after submitting verdict.
func TestFix46_ReviewerPromptExitsViaTaskComplete(t *testing.T) {
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

	config := ReviewerContextConfig{
		ProjectRoot: "/project",
		AgentID:     "reviewer-1",
	}

	prompt, err := BuildReviewerContext(&task, config)
	if err != nil {
		t.Fatalf("BuildReviewerContext() error: %v", err)
	}

	if !strings.Contains(prompt, "call task_complete to end the session") {
		t.Error("Reviewer prompt must instruct to call task_complete after verdict")
	}
}

// TestFix46_BasePromptMentionsTaskComplete verifies the base prompt
// includes the task_complete exit instruction for all roles.
func TestFix46_BasePromptMentionsTaskComplete(t *testing.T) {
	t.Parallel()

	config := BasePromptConfig{
		Role:    "coder",
		AgentID: "coder-1",
	}

	prompt, err := BuildBasePrompt(config)
	if err != nil {
		t.Fatalf("BuildBasePrompt() error: %v", err)
	}

	if !strings.Contains(prompt, "call task_complete to end the session") {
		t.Error("Base prompt must mention task_complete as session exit mechanism")
	}
}
