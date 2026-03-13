package integration

// e2e_workflow_test.go contains end-to-end integration tests for complete task workflows.
//
// These tests verify that entire workflows function correctly when commands are
// used in sequence.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/liza-mas/liza/internal/commands"
	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/ops"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// Test setup helper that returns a project directory and cleanup function
func setupTestProject(t *testing.T) (projectDir string, cleanup func()) {
	t.Helper()

	testhelpers.SetupGlobalLiza(t)

	tmpDir := t.TempDir()

	// Initialize git repository
	testhelpers.SetupTestGitRepo(t, tmpDir)

	// Store original directory to restore later
	originalDir, err := os.Getwd()
	testhelpers.AssertNoError(t, err)

	// Change to tmpDir
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}

	cleanup = func() {
		os.Chdir(originalDir)
	}

	return tmpDir, cleanup
}

// setupIntegrationTest performs the common integration test setup:
// init, create spec, add tasks, and return the blackboard.
func setupIntegrationTest(t *testing.T, projectDir string, taskIDs []string) (*db.Blackboard, string, string) {
	t.Helper()

	testhelpers.CreateSpecFile(t, projectDir, "feature.md", testhelpers.ValidVisionSpec)

	if err := commands.InitCommand("Test goal", "specs/feature.md", nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	statePath := filepath.Join(projectDir, ".liza", "state.yaml")
	logPath := filepath.Join(projectDir, ".liza", "log.yaml")
	bb := db.New(statePath)

	for _, taskID := range taskIDs {
		taskInput := &commands.TaskInput{
			ID:          taskID,
			Description: "Test feature " + taskID,
			DoneWhen:    "Done",
			Scope:       "Feature",
			Priority:    1,
			SpecRef:     "specs/feature.md",
			DependsOn:   []string{},
		}
		if err := commands.AddTaskCommand(statePath, logPath, taskInput, "planner-1"); err != nil {
			t.Fatalf("AddTask %s failed: %v", taskID, err)
		}
	}

	return bb, statePath, logPath
}

// TestSimpleWorkflow tests: init -> add-task -> claim -> submit-for-review -> approve -> merge
func TestSimpleWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration tests in short mode")
	}

	projectDir, cleanup := setupTestProject(t)
	defer cleanup()

	// Steps 1-2: Initialize liza and add task
	t.Log("Steps 1-2: Initialize liza and add task")
	taskID := "task-1"
	bb, _, _ := setupIntegrationTest(t, projectDir, []string{taskID})

	// Verify initialization
	state, err := bb.Read()
	testhelpers.AssertNoError(t, err)
	if state.Goal.Description != "Test goal" {
		t.Errorf("Expected goal description 'Test goal', got %s", state.Goal.Description)
	}

	// Verify task was added
	if len(state.Tasks) != 1 {
		t.Fatalf("Expected 1 task, got %d", len(state.Tasks))
	}
	if state.Tasks[0].Status != models.TaskStatusReady {
		t.Errorf("Expected task status READY, got %s", state.Tasks[0].Status)
	}

	// Step 3: Register an agent
	t.Log("Step 3: Register agent")
	agentID := "coder-1"
	testhelpers.RegisterTestAgent(t, bb, agentID, "coder")

	// Step 4: Claim the task
	t.Log("Step 4: Claim task")
	if err := commands.ClaimTaskCommand(projectDir, taskID, agentID); err != nil {
		t.Fatalf("ClaimTask failed: %v", err)
	}

	// Verify task was claimed
	state, err = bb.Read()
	testhelpers.AssertNoError(t, err)
	task := findTask(state.Tasks, taskID)
	if task == nil {
		t.Fatal("Task not found after claim")
	}
	if task.Status != models.TaskStatusImplementing {
		t.Errorf("Expected task status IMPLEMENTING, got %s", task.Status)
	}
	if task.AssignedTo == nil || *task.AssignedTo != agentID {
		t.Errorf("Expected task assigned to %s", agentID)
	}
	if task.Worktree == nil {
		t.Error("Expected worktree to be set after claim")
	}

	// Step 5: Make a commit and submit for review
	t.Log("Step 5: Make commit and submit for review")
	worktreePath := filepath.Join(projectDir, *task.Worktree)
	testFile := filepath.Join(worktreePath, "feature.go")
	if err := os.WriteFile(testFile, []byte("package main\n\nfunc Feature() {}\n"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	testTestFile := filepath.Join(worktreePath, "feature_test.go")
	if err := os.WriteFile(testTestFile, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	if err := exec.Command("git", "-C", worktreePath, "add", "feature.go", "feature_test.go").Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}
	if err := exec.Command("git", "-C", worktreePath, "commit", "-m", "Implement feature with tests").Run(); err != nil {
		t.Fatalf("Failed to git commit: %v", err)
	}

	// Write pre-execution checkpoint (required for submission)
	if err := ops.WriteCheckpoint(projectDir, &ops.WriteCheckpointInput{
		TaskID: taskID, AgentID: agentID,
		Intent: "Implement feature", ValidationPlan: "go test ./...",
		FilesToModify: []string{"feature.go"},
	}); err != nil {
		t.Fatalf("WriteCheckpoint failed: %v", err)
	}

	// Get commit hash
	output, err := exec.Command("git", "-C", worktreePath, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("Failed to get commit hash: %v", err)
	}
	reviewCommit := string(output[:40])

	if err := commands.SubmitForReviewCommand(projectDir, taskID, reviewCommit, agentID); err != nil {
		t.Fatalf("SubmitForReview failed: %v", err)
	}

	// Verify task is ready for review
	state, err = bb.Read()
	testhelpers.AssertNoError(t, err)
	task = findTask(state.Tasks, taskID)
	if task.Status != models.TaskStatusReadyForReview {
		t.Errorf("Expected task status READY_FOR_REVIEW, got %s", task.Status)
	}

	// Step 6: Register reviewer and approve
	t.Log("Step 6: Register reviewer and approve")
	reviewerID := "code-reviewer-1"
	testhelpers.RegisterTestAgent(t, bb, reviewerID, "code-reviewer")

	// Transition to REVIEWING (simulates supervisor reviewer claim)
	testhelpers.TransitionToReviewing(t, bb, taskID, reviewerID)

	if err := commands.SubmitVerdictCommand(projectDir, taskID, "APPROVED", "", reviewerID); err != nil {
		t.Fatalf("SubmitVerdict failed: %v", err)
	}

	// Verify task is approved
	state, err = bb.Read()
	testhelpers.AssertNoError(t, err)
	task = findTask(state.Tasks, taskID)
	if task.Status != models.TaskStatusApproved {
		t.Errorf("Expected task status APPROVED, got %s", task.Status)
	}

	// Step 7: Merge the task
	t.Log("Step 7: Merge task")
	// Set LIZA_AGENT_ID for merge command
	os.Setenv("LIZA_AGENT_ID", reviewerID)
	defer os.Unsetenv("LIZA_AGENT_ID")

	if err := commands.WtMergeCommand(projectDir, taskID, "code-reviewer-1"); err != nil {
		t.Fatalf("WtMerge failed: %v", err)
	}

	// Verify task is merged
	state, err = bb.Read()
	testhelpers.AssertNoError(t, err)
	task = findTask(state.Tasks, taskID)
	if task.Status != models.TaskStatusMerged {
		t.Errorf("Expected task status MERGED, got %s", task.Status)
	}
	if task.MergeCommit == nil {
		t.Error("Expected merge commit to be set")
	}

	t.Log("✓ Simple workflow test passed")
}

// TestTaskDependencyWorkflow tests that task dependencies are enforced across the workflow
func TestTaskDependencyWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration tests in short mode")
	}

	projectDir, cleanup := setupTestProject(t)
	defer cleanup()

	// Setup: init + add task-1 (no deps)
	bb, statePath, logPath := setupIntegrationTest(t, projectDir, []string{"task-1"})

	// Add task-2 (depends on task-1) — custom deps, can't use helper
	task2Input := &commands.TaskInput{
		ID:          "task-2",
		Description: "Dependent feature",
		DoneWhen:    "Done",
		Scope:       "Dependent",
		Priority:    1,
		SpecRef:     "specs/feature.md",
		DependsOn:   []string{"task-1"},
	}
	if err := commands.AddTaskCommand(statePath, logPath, task2Input, "planner-1"); err != nil {
		t.Fatalf("AddTask task-2 failed: %v", err)
	}

	// Register agent
	agentID := "coder-1"
	testhelpers.RegisterTestAgent(t, bb, agentID, "coder")

	// Try to claim task-2 (should fail because task-1 not merged)
	t.Log("Attempting to claim task-2 before task-1 is done")
	err := commands.ClaimTaskCommand(projectDir, "task-2", agentID)
	if err == nil {
		t.Fatal("Expected error when claiming task with unmet dependencies")
	}
	testhelpers.AssertErrorContains(t, err, "unmet dependencies")

	// Claim task-1
	t.Log("Claiming task-1")
	if err := commands.ClaimTaskCommand(projectDir, "task-1", agentID); err != nil {
		t.Fatalf("ClaimTask task-1 failed: %v", err)
	}

	// Release agent from task-1 and mark it as merged (simplified for test)
	err = bb.Modify(func(state *models.State) error {
		// Release agent from task
		if agent, ok := state.Agents[agentID]; ok {
			agent.CurrentTask = nil
			agent.Status = models.AgentStatusWaiting
			state.Agents[agentID] = agent
		}
		// Mark task as merged
		for i := range state.Tasks {
			if state.Tasks[i].ID == "task-1" {
				state.Tasks[i].Status = models.TaskStatusMerged
				mergeCommit := "merge123"
				state.Tasks[i].MergeCommit = &mergeCommit
				state.Tasks[i].Worktree = nil
				break
			}
		}
		return nil
	})
	testhelpers.AssertNoError(t, err)

	// Now try to claim task-2 (should succeed)
	t.Log("Claiming task-2 after task-1 is merged")
	if err := commands.ClaimTaskCommand(projectDir, "task-2", agentID); err != nil {
		t.Fatalf("ClaimTask task-2 failed after dependency was met: %v", err)
	}

	// Verify task-2 is claimed
	state, err := bb.Read()
	testhelpers.AssertNoError(t, err)
	task := findTask(state.Tasks, "task-2")
	if task.Status != models.TaskStatusImplementing {
		t.Errorf("Expected task-2 status IMPLEMENTING, got %s", task.Status)
	}

	t.Log("✓ Task dependency workflow test passed")
}

// findTask is a helper function to find a task by ID in a slice of tasks
func findTask(tasks []models.Task, taskID string) *models.Task {
	for i := range tasks {
		if tasks[i].ID == taskID {
			return &tasks[i]
		}
	}
	return nil
}
