package ops

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	lizaerrors "github.com/liza-mas/liza/internal/errors"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

func TestAppendUniqueAgentID(t *testing.T) {
	tests := []struct {
		name     string
		failedBy []string
		agentID  string
		want     []string
	}{
		{
			name:     "first append adds ID",
			failedBy: []string{},
			agentID:  "agent-1",
			want:     []string{"agent-1"},
		},
		{
			name:     "duplicate append is no-op",
			failedBy: []string{"agent-1"},
			agentID:  "agent-1",
			want:     []string{"agent-1"},
		},
		{
			name:     "different ID appends correctly",
			failedBy: []string{"agent-1"},
			agentID:  "agent-2",
			want:     []string{"agent-1", "agent-2"},
		},
		{
			name:     "empty slice behavior",
			failedBy: nil,
			agentID:  "agent-1",
			want:     []string{"agent-1"},
		},
		{
			name:     "multiple existing IDs, no duplicate",
			failedBy: []string{"agent-1", "agent-2", "agent-3"},
			agentID:  "agent-4",
			want:     []string{"agent-1", "agent-2", "agent-3", "agent-4"},
		},
		{
			name:     "multiple existing IDs, duplicate in middle",
			failedBy: []string{"agent-1", "agent-2", "agent-3"},
			agentID:  "agent-2",
			want:     []string{"agent-1", "agent-2", "agent-3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendUniqueAgentID(tt.failedBy, tt.agentID)

			if len(got) != len(tt.want) {
				t.Errorf("appendUniqueAgentID() length = %v, want %v", len(got), len(tt.want))
				return
			}

			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("appendUniqueAgentID()[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestIntegrationFailedError(t *testing.T) {
	tests := []struct {
		name         string
		err          IntegrationFailedError
		wantContains []string
		wantMissing  []string
	}{
		{
			name: "merge conflict without rollback error",
			err: IntegrationFailedError{
				Reason: IntegrationReasonMergeConflict,
			},
			wantContains: []string{"integration failed", "merge conflict"},
			wantMissing:  []string{"rollback"},
		},
		{
			name: "HEAD mismatch without rollback error",
			err: IntegrationFailedError{
				Reason: IntegrationReasonHEADMismatch,
			},
			wantContains: []string{"integration failed", "worktree HEAD mismatch"},
			wantMissing:  []string{"rollback"},
		},
		{
			name: "test failure without rollback error",
			err: IntegrationFailedError{
				Reason:     IntegrationReasonTestsFailed,
				TestOutput: "FAIL: TestSomething",
			},
			wantContains: []string{"integration failed", "integration tests failed"},
			wantMissing:  []string{"rollback"},
		},
		{
			name: "test failure with rollback error",
			err: IntegrationFailedError{
				Reason:        IntegrationReasonTestsFailed,
				TestOutput:    "FAIL: TestSomething",
				RollbackError: fmt.Errorf("permission denied"),
			},
			wantContains: []string{"integration failed", "integration tests failed", "rollback also failed", "permission denied"},
		},
		{
			name: "rollback error includes wrapped error detail",
			err: IntegrationFailedError{
				Reason:        IntegrationReasonTestsFailed,
				RollbackError: fmt.Errorf("reset --hard failed: %w", fmt.Errorf("index.lock exists")),
			},
			wantContains: []string{"rollback also failed", "index.lock exists"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.err.Error()
			for _, want := range tt.wantContains {
				if !strings.Contains(msg, want) {
					t.Errorf("Error() = %q, want to contain %q", msg, want)
				}
			}
			for _, absent := range tt.wantMissing {
				if strings.Contains(msg, absent) {
					t.Errorf("Error() = %q, should NOT contain %q", msg, absent)
				}
			}
		})
	}
}

func TestIntegrationFailedError_ImplementsError(t *testing.T) {
	// Verify IntegrationFailedError satisfies error interface and errors.As works
	var err error = &IntegrationFailedError{Reason: IntegrationReasonMergeConflict}

	var intErr *IntegrationFailedError
	if !errors.As(err, &intErr) {
		t.Fatal("errors.As should match *IntegrationFailedError")
	}
	if intErr.Reason != IntegrationReasonMergeConflict {
		t.Errorf("Reason = %q, want %q", intErr.Reason, IntegrationReasonMergeConflict)
	}
}

// setupMergeTestRepo creates a git repo with integration branch, worktree, and state
// suitable for testing MergeWorktree. Returns projectRoot and statePath.
func setupMergeTestRepo(t *testing.T, taskID, agentID string) (string, string) {
	t.Helper()
	tmpDir := t.TempDir()

	testhelpers.SetupTestGitRepo(t, tmpDir)
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	// Create integration branch
	cmd := exec.Command("git", "-C", tmpDir, "checkout", "-b", "integration")
	output, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(output), "already exists") {
		t.Fatalf("Failed to create integration branch: %v\nOutput: %s", err, output)
	}
	if strings.Contains(string(output), "already exists") {
		cmd2 := exec.Command("git", "-C", tmpDir, "checkout", "integration")
		if err := cmd2.Run(); err != nil {
			t.Fatalf("Failed to checkout integration branch: %v", err)
		}
	}

	// Create initial state
	now := time.Now().UTC()
	initialState := testhelpers.CreateValidState()
	initialState.Config.IntegrationBranch = "integration"

	// Create worktree
	wtDir := filepath.Join(tmpDir, ".worktrees", taskID)
	if err := os.MkdirAll(filepath.Dir(wtDir), 0755); err != nil {
		t.Fatalf("Failed to create worktrees directory: %v", err)
	}

	cmd = exec.Command("git", "-C", tmpDir, "worktree", "add", wtDir, "integration", "-b", "task/"+taskID)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create worktree: %v", err)
	}

	// Make a commit in the worktree
	testFile := filepath.Join(wtDir, "test-"+taskID+".txt")
	if err := os.WriteFile(testFile, []byte("test content for "+taskID), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	cmd = exec.Command("git", "-C", wtDir, "add", ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}

	cmd = exec.Command("git", "-C", wtDir, "commit", "-m", "Test commit for "+taskID)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git commit: %v", err)
	}

	// Get worktree HEAD SHA
	cmd = exec.Command("git", "-C", wtDir, "rev-parse", "HEAD")
	shaOutput, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get commit SHA: %v", err)
	}
	wtCommit := strings.TrimSpace(string(shaOutput))

	// Create task in state
	worktreePath := filepath.Join(".worktrees", taskID)
	baseCommit := "base123"
	approvedBy := "code-reviewer-1"
	task := models.Task{
		ID:           taskID,
		Description:  "Test task",
		Status:       models.TaskStatusApproved,
		Priority:     1,
		Created:      now,
		SpecRef:      "README.md",
		DoneWhen:     "Done",
		Scope:        "Test",
		RolePair:     "coding-pair",
		Worktree:     &worktreePath,
		AssignedTo:   &agentID,
		BaseCommit:   &baseCommit,
		ReviewCommit: &wtCommit,
		ApprovedBy:   &approvedBy,
		History:      []models.TaskHistoryEntry{},
	}

	initialState.Tasks = append(initialState.Tasks, task)
	testhelpers.WriteInitialState(t, stateFile, initialState)

	return tmpDir, stateFile
}

func TestMergeWorktree_Validation(t *testing.T) {
	tests := []struct {
		name        string
		taskID      string
		agentID     string
		errContains string
	}{
		{
			name:        "empty task ID",
			taskID:      "",
			agentID:     "coder-1",
			errContains: "task ID is required",
		},
		{
			name:        "empty agent ID",
			taskID:      "task-1",
			agentID:     "",
			errContains: "agent ID is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := MergeWorktree("/nonexistent", tt.taskID, tt.agentID)
			testhelpers.RequireErrorContains(t, err, tt.errContains)
		})
	}
}

func TestMergeWorktree_TaskNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	state := testhelpers.CreateValidState()
	testhelpers.WriteInitialState(t, stateFile, state)

	_, err := MergeWorktree(tmpDir, "nonexistent", "coder-1")
	if err == nil {
		t.Fatal("Expected error for nonexistent task")
	}
	if !lizaerrors.IsNotFound(err) {
		t.Errorf("expected NotFoundError, got %T: %v", err, err)
	}
}

func TestMergeWorktree_Success(t *testing.T) {
	taskID := "merge-ok"
	agentID := "coder-1"
	tmpDir, stateFile := setupMergeTestRepo(t, taskID, agentID)

	result, err := MergeWorktree(tmpDir, taskID, agentID)
	if err != nil {
		t.Fatalf("MergeWorktree() unexpected error: %v", err)
	}

	// Verify result fields
	if result.TaskID != taskID {
		t.Errorf("TaskID = %q, want %q", result.TaskID, taskID)
	}
	if result.MergeCommit == "" {
		t.Error("MergeCommit should not be empty")
	}
	if result.TestsRan {
		t.Error("TestsRan should be false (no integration-test.sh)")
	}
	if !result.NoTestScriptFound {
		t.Error("NoTestScriptFound should be true (no integration-test.sh)")
	}

	// Verify main working tree was synced: the file committed in the worktree
	// must appear in the main working tree after merge (not just in git objects).
	mergedFile := filepath.Join(tmpDir, "test-"+taskID+".txt")
	content, readErr := os.ReadFile(mergedFile)
	if readErr != nil {
		t.Fatalf("Merged file should exist in main working tree after merge (working tree not synced): %v", readErr)
	}
	if string(content) != "test content for "+taskID {
		t.Errorf("Merged file content = %q, want %q", string(content), "test content for "+taskID)
	}

	// Verify state updated to MERGED
	state := readStateForTest(t, stateFile)
	task := state.FindTask(taskID)
	if task == nil {
		t.Fatal("Task not found in state")
	}
	if task.Status != models.TaskStatusMerged {
		t.Errorf("Task status = %v, want MERGED", task.Status)
	}
	if task.MergeCommit == nil {
		t.Error("MergeCommit should be set in state")
	}
	if task.Worktree != nil {
		t.Errorf("Worktree should be nil after merge, got %v", *task.Worktree)
	}

	// Verify history entry has tests_ran = false
	if len(task.History) == 0 {
		t.Fatal("Expected history entry for merge")
	}
	lastHistory := task.History[len(task.History)-1]
	if lastHistory.Event != models.TaskEventMerged {
		t.Errorf("Last history event = %q, want 'merged'", lastHistory.Event)
	}
	if lastHistory.Extra == nil {
		t.Fatal("Expected Extra map in history entry")
	}
	testsRanVal, ok := lastHistory.Extra["tests_ran"]
	if !ok {
		t.Fatal("Expected 'tests_ran' field in history Extra")
	}
	if testsRanVal != false {
		t.Errorf("tests_ran = %v, want false", testsRanVal)
	}
}

func TestMergeWorktree_SyncsRenamedFiles(t *testing.T) {
	taskID := "merge-rename"
	agentID := "coder-1"
	tmpDir, stateFile := setupMergeTestRepo(t, taskID, agentID)

	// The setupMergeTestRepo already created "test-merge-rename.txt" in the worktree.
	// Now rename it in the worktree and commit.
	wtDir := filepath.Join(tmpDir, ".worktrees", taskID)
	testhelpers.MustGit(t, wtDir, "mv", "test-"+taskID+".txt", "renamed-"+taskID+".txt")
	testhelpers.MustGit(t, wtDir, "commit", "-m", "Rename test file")

	// Update review_commit in state to the new HEAD.
	newSHA := testhelpers.MustGit(t, wtDir, "rev-parse", "HEAD")
	bb := db.New(stateFile)
	if err := bb.Modify(func(s *models.State) error {
		task := s.FindTask(taskID)
		task.ReviewCommit = &newSHA
		return nil
	}); err != nil {
		t.Fatalf("Failed to update review_commit: %v", err)
	}

	result, err := MergeWorktree(tmpDir, taskID, agentID)
	if err != nil {
		t.Fatalf("MergeWorktree() unexpected error: %v", err)
	}
	if result.MergeCommit == "" {
		t.Error("MergeCommit should not be empty")
	}

	// Old path must be gone from working tree.
	oldPath := filepath.Join(tmpDir, "test-"+taskID+".txt")
	if _, statErr := os.Stat(oldPath); !os.IsNotExist(statErr) {
		t.Errorf("Old renamed file should not exist in working tree, but Stat returned: %v", statErr)
	}

	// New path must exist with correct content.
	newPath := filepath.Join(tmpDir, "renamed-"+taskID+".txt")
	content, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("Renamed file should exist in working tree: %v", err)
	}
	if string(content) != "test content for "+taskID {
		t.Errorf("Renamed file content = %q, want %q", string(content), "test content for "+taskID)
	}
}

func TestMergeWorktree_SyncsDeletedFiles(t *testing.T) {
	taskID := "merge-delete"
	agentID := "coder-1"
	tmpDir, stateFile := setupMergeTestRepo(t, taskID, agentID)

	// Delete the file in the worktree and commit.
	wtDir := filepath.Join(tmpDir, ".worktrees", taskID)
	testhelpers.MustGit(t, wtDir, "rm", "test-"+taskID+".txt")
	testhelpers.MustGit(t, wtDir, "commit", "-m", "Delete test file")

	// Update review_commit in state.
	newSHA := testhelpers.MustGit(t, wtDir, "rev-parse", "HEAD")
	bb := db.New(stateFile)
	if err := bb.Modify(func(s *models.State) error {
		task := s.FindTask(taskID)
		task.ReviewCommit = &newSHA
		return nil
	}); err != nil {
		t.Fatalf("Failed to update review_commit: %v", err)
	}

	result, err := MergeWorktree(tmpDir, taskID, agentID)
	if err != nil {
		t.Fatalf("MergeWorktree() unexpected error: %v", err)
	}
	if result.MergeCommit == "" {
		t.Error("MergeCommit should not be empty")
	}

	// File must be gone from working tree.
	deletedPath := filepath.Join(tmpDir, "test-"+taskID+".txt")
	if _, statErr := os.Stat(deletedPath); !os.IsNotExist(statErr) {
		t.Errorf("Deleted file should not exist in working tree, but Stat returned: %v", statErr)
	}
}

func TestMergeWorktree_RollbackSyncsRenamedFiles(t *testing.T) {
	taskID := "merge-rename-rollback"
	agentID := "coder-1"
	tmpDir, stateFile := setupMergeTestRepo(t, taskID, agentID)

	// Rename the file in the worktree and commit.
	wtDir := filepath.Join(tmpDir, ".worktrees", taskID)
	testhelpers.MustGit(t, wtDir, "mv", "test-"+taskID+".txt", "renamed-"+taskID+".txt")
	testhelpers.MustGit(t, wtDir, "commit", "-m", "Rename test file")

	// Update review_commit in state.
	newSHA := testhelpers.MustGit(t, wtDir, "rev-parse", "HEAD")
	bb := db.New(stateFile)
	if err := bb.Modify(func(s *models.State) error {
		task := s.FindTask(taskID)
		task.ReviewCommit = &newSHA
		return nil
	}); err != nil {
		t.Fatalf("Failed to update review_commit: %v", err)
	}

	// Create a failing integration test script to trigger rollback.
	scriptsDir := filepath.Join(tmpDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatalf("Failed to create scripts dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(scriptsDir, "integration-test.sh"),
		[]byte("#!/bin/sh\nexit 1\n"),
		0755,
	); err != nil {
		t.Fatalf("Failed to write test script: %v", err)
	}

	_, err := MergeWorktree(tmpDir, taskID, agentID)
	var intErr *IntegrationFailedError
	if !errors.As(err, &intErr) {
		t.Fatalf("Expected *IntegrationFailedError, got %T: %v", err, err)
	}

	// After rollback: old path must be restored (absent in toCommit=preMerge means
	// the file existed before the rename), new path must be gone.
	// But wait — the original file (test-<taskID>.txt) was created in the worktree,
	// not on integration. Before merge, it didn't exist in the main working tree.
	// So after rollback, NEITHER file should exist.
	oldPath := filepath.Join(tmpDir, "test-"+taskID+".txt")
	if _, statErr := os.Stat(oldPath); !os.IsNotExist(statErr) {
		t.Errorf("Original file should not exist in working tree after rollback (it was worktree-only), Stat: %v", statErr)
	}
	newPath := filepath.Join(tmpDir, "renamed-"+taskID+".txt")
	if _, statErr := os.Stat(newPath); !os.IsNotExist(statErr) {
		t.Errorf("Renamed file should not exist in working tree after rollback, Stat: %v", statErr)
	}

	// State should be INTEGRATION_FAILED.
	state := readStateForTest(t, stateFile)
	task := state.FindTask(taskID)
	if task.Status != models.TaskStatusIntegrationFailed {
		t.Errorf("Task status = %v, want INTEGRATION_FAILED", task.Status)
	}
}

func TestMergeWorktree_CodingPlanApproved(t *testing.T) {
	taskID := "merge-plan-ok"
	agentID := "code-plan-reviewer-1"
	tmpDir := t.TempDir()

	testhelpers.SetupTestGitRepo(t, tmpDir)
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	// Create integration branch
	cmd := exec.Command("git", "-C", tmpDir, "checkout", "-b", "integration")
	output, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(output), "already exists") {
		t.Fatalf("Failed to create integration branch: %v\nOutput: %s", err, output)
	}

	now := time.Now().UTC()
	initialState := testhelpers.CreateValidState()
	initialState.Config.IntegrationBranch = "integration"

	// Create worktree
	wtDir := filepath.Join(tmpDir, ".worktrees", taskID)
	if err := os.MkdirAll(filepath.Dir(wtDir), 0755); err != nil {
		t.Fatalf("Failed to create worktrees directory: %v", err)
	}
	cmd = exec.Command("git", "-C", tmpDir, "worktree", "add", wtDir, "integration", "-b", "task/"+taskID)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create worktree: %v", err)
	}

	// Make a commit in the worktree
	testFile := filepath.Join(wtDir, "plan-"+taskID+".md")
	if err := os.WriteFile(testFile, []byte("# Plan\n"), 0644); err != nil {
		t.Fatalf("Failed to write plan file: %v", err)
	}
	cmd = exec.Command("git", "-C", wtDir, "add", ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}
	cmd = exec.Command("git", "-C", wtDir, "commit", "-m", "Plan for "+taskID)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git commit: %v", err)
	}

	// Get worktree HEAD SHA
	cmd = exec.Command("git", "-C", wtDir, "rev-parse", "HEAD")
	shaOutput, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get commit SHA: %v", err)
	}
	wtCommit := strings.TrimSpace(string(shaOutput))

	worktreePath := filepath.Join(".worktrees", taskID)
	baseCommit := "base123"
	approvedBy := agentID
	task := models.Task{
		ID:           taskID,
		Description:  "Plan task",
		Status:       models.TaskStatusCodingPlanApproved,
		Priority:     1,
		Created:      now,
		SpecRef:      "README.md",
		DoneWhen:     "Plan approved",
		Scope:        "auth",
		RolePair:     "code-planning-pair",
		Worktree:     &worktreePath,
		AssignedTo:   testhelpers.StringPtr("code-planner-1"),
		BaseCommit:   &baseCommit,
		ReviewCommit: &wtCommit,
		ApprovedBy:   &approvedBy,
		History:      []models.TaskHistoryEntry{},
	}

	initialState.Tasks = append(initialState.Tasks, task)
	testhelpers.WriteInitialState(t, stateFile, initialState)

	result, err := MergeWorktree(tmpDir, taskID, agentID)
	if err != nil {
		t.Fatalf("MergeWorktree() unexpected error: %v", err)
	}

	if result.TaskID != taskID {
		t.Errorf("TaskID = %q, want %q", result.TaskID, taskID)
	}
	if result.MergeCommit == "" {
		t.Error("MergeCommit should not be empty")
	}

	// Verify state updated to MERGED
	state := readStateForTest(t, stateFile)
	mergedTask := state.FindTask(taskID)
	if mergedTask == nil {
		t.Fatal("Task not found in state")
	}
	if mergedTask.Status != models.TaskStatusMerged {
		t.Errorf("Task status = %v, want MERGED", mergedTask.Status)
	}
	if mergedTask.Worktree != nil {
		t.Errorf("Worktree should be nil after merge, got %v", *mergedTask.Worktree)
	}
}

func TestMergeWorktree_PipelineCodingPairApproved(t *testing.T) {
	taskID := "merge-pipeline-ok"
	agentID := "code-reviewer-1"

	// Use setupPipelineTest to get a project with frozen pipeline config
	tmpDir, stateFile := setupPipelineTest(t)

	// Create integration branch
	cmd := exec.Command("git", "-C", tmpDir, "checkout", "-b", "integration")
	output, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(output), "already exists") {
		t.Fatalf("Failed to create integration branch: %v\nOutput: %s", err, output)
	}

	now := time.Now().UTC()
	initialState := testhelpers.CreateValidState()
	initialState.Config.IntegrationBranch = "integration"

	// Create worktree
	wtDir := filepath.Join(tmpDir, ".worktrees", taskID)
	if err := os.MkdirAll(filepath.Dir(wtDir), 0755); err != nil {
		t.Fatalf("Failed to create worktrees directory: %v", err)
	}
	cmd = exec.Command("git", "-C", tmpDir, "worktree", "add", wtDir, "integration", "-b", "task/"+taskID)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create worktree: %v", err)
	}

	// Make a commit in the worktree
	testFile := filepath.Join(wtDir, "code-"+taskID+".go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("Failed to write code file: %v", err)
	}
	cmd = exec.Command("git", "-C", wtDir, "add", ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}
	cmd = exec.Command("git", "-C", wtDir, "commit", "-m", "Code for "+taskID)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git commit: %v", err)
	}

	// Get worktree HEAD SHA
	cmd = exec.Command("git", "-C", wtDir, "rev-parse", "HEAD")
	shaOutput, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get commit SHA: %v", err)
	}
	wtCommit := strings.TrimSpace(string(shaOutput))

	worktreePath := filepath.Join(".worktrees", taskID)
	baseCommit := "base123"
	approvedBy := agentID
	task := models.Task{
		ID:           taskID,
		Description:  "Pipeline coding task",
		Status:       "CODE_APPROVED", // Pipeline-resolved approved status
		Priority:     1,
		Created:      now,
		SpecRef:      "README.md",
		DoneWhen:     "Code approved",
		Scope:        "auth",
		RolePair:     "coding-pair",
		Worktree:     &worktreePath,
		AssignedTo:   testhelpers.StringPtr("coder-1"),
		BaseCommit:   &baseCommit,
		ReviewCommit: &wtCommit,
		ApprovedBy:   &approvedBy,
		History:      []models.TaskHistoryEntry{},
	}

	initialState.Tasks = append(initialState.Tasks, task)
	testhelpers.WriteInitialState(t, stateFile, initialState)

	result, err := MergeWorktree(tmpDir, taskID, agentID)
	if err != nil {
		t.Fatalf("MergeWorktree() unexpected error: %v", err)
	}

	if result.TaskID != taskID {
		t.Errorf("TaskID = %q, want %q", result.TaskID, taskID)
	}
	if result.MergeCommit == "" {
		t.Error("MergeCommit should not be empty")
	}

	// Verify state updated to MERGED
	state := readStateForTest(t, stateFile)
	mergedTask := state.FindTask(taskID)
	if mergedTask == nil {
		t.Fatal("Task not found in state")
	}
	if mergedTask.Status != models.TaskStatusMerged {
		t.Errorf("Task status = %v, want MERGED", mergedTask.Status)
	}
	if mergedTask.Worktree != nil {
		t.Errorf("Worktree should be nil after merge, got %v", *mergedTask.Worktree)
	}
}

func TestMergeWorktree_MergeConflict(t *testing.T) {
	taskID := "merge-conflict"
	agentID := "coder-1"
	tmpDir, stateFile := setupMergeTestRepo(t, taskID, agentID)

	// Create a conflicting commit on integration branch
	cmd := exec.Command("git", "-C", tmpDir, "checkout", "integration")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to checkout integration: %v", err)
	}

	conflictFile := filepath.Join(tmpDir, "test-"+taskID+".txt")
	if err := os.WriteFile(conflictFile, []byte("conflicting content"), 0644); err != nil {
		t.Fatalf("Failed to write conflict file: %v", err)
	}

	cmd = exec.Command("git", "-C", tmpDir, "add", ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}

	cmd = exec.Command("git", "-C", tmpDir, "commit", "-m", "Conflicting commit")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to commit: %v", err)
	}

	// Attempt merge — should fail with IntegrationFailedError
	_, err := MergeWorktree(tmpDir, taskID, agentID)
	if err == nil {
		t.Fatal("Expected error for merge conflict, got nil")
	}

	var intErr *IntegrationFailedError
	if !errors.As(err, &intErr) {
		t.Fatalf("Expected *IntegrationFailedError, got %T: %v", err, err)
	}

	if intErr.Reason != IntegrationReasonMergeConflict {
		t.Errorf("Reason = %q, want %q", intErr.Reason, IntegrationReasonMergeConflict)
	}
	if intErr.RollbackError != nil {
		t.Errorf("RollbackError should be nil for merge conflicts, got %v", intErr.RollbackError)
	}

	// Verify state updated to INTEGRATION_FAILED
	state := readStateForTest(t, stateFile)
	task := state.FindTask(taskID)
	if task == nil {
		t.Fatal("Task not found in state")
	}
	if task.Status != models.TaskStatusIntegrationFailed {
		t.Errorf("Task status = %v, want INTEGRATION_FAILED", task.Status)
	}
	if len(task.FailedBy) == 0 || task.FailedBy[0] != agentID {
		t.Errorf("FailedBy = %v, want [%s]", task.FailedBy, agentID)
	}
}

func TestMergeWorktree_IntegrationTestFailure(t *testing.T) {
	taskID := "merge-testfail"
	agentID := "coder-1"
	tmpDir, stateFile := setupMergeTestRepo(t, taskID, agentID)

	// Create a failing integration test script
	scriptsDir := filepath.Join(tmpDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatalf("Failed to create scripts dir: %v", err)
	}
	script := filepath.Join(scriptsDir, "integration-test.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'FAIL: TestSomething'\nexit 1\n"), 0755); err != nil {
		t.Fatalf("Failed to write test script: %v", err)
	}

	// Attempt merge — should fail with IntegrationFailedError after test failure
	_, err := MergeWorktree(tmpDir, taskID, agentID)
	if err == nil {
		t.Fatal("Expected error for test failure, got nil")
	}

	var intErr *IntegrationFailedError
	if !errors.As(err, &intErr) {
		t.Fatalf("Expected *IntegrationFailedError, got %T: %v", err, err)
	}

	// Verify reason constant
	if intErr.Reason != IntegrationReasonTestsFailed {
		t.Errorf("Reason = %q, want %q", intErr.Reason, IntegrationReasonTestsFailed)
	}

	// Verify test output captured
	if intErr.TestOutput == "" {
		t.Error("TestOutput should be non-empty when tests fail")
	}
	if !strings.Contains(intErr.TestOutput, "FAIL: TestSomething") {
		t.Errorf("TestOutput = %q, want to contain 'FAIL: TestSomething'", intErr.TestOutput)
	}

	// Verify rollback succeeded (RollbackError should be nil)
	if intErr.RollbackError != nil {
		t.Errorf("RollbackError should be nil when rollback succeeds, got %v", intErr.RollbackError)
	}

	// Verify state updated to INTEGRATION_FAILED
	state := readStateForTest(t, stateFile)
	task := state.FindTask(taskID)
	if task == nil {
		t.Fatal("Task not found in state")
	}
	if task.Status != models.TaskStatusIntegrationFailed {
		t.Errorf("Task status = %v, want INTEGRATION_FAILED", task.Status)
	}

	// Verify integration branch was rolled back (should not contain the merge)
	cmd := exec.Command("git", "-C", tmpDir, "checkout", "integration")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to checkout integration: %v", err)
	}
	cmd = exec.Command("git", "-C", tmpDir, "log", "--oneline")
	logOutput, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get git log: %v", err)
	}
	if strings.Contains(string(logOutput), "Test commit for "+taskID) {
		t.Error("Integration branch should have been rolled back, but still contains the merged commit")
	}
}

func TestMergeWorktree_IntegrationTestTimeout(t *testing.T) {
	taskID := "merge-timeout"
	agentID := "coder-1"
	tmpDir, stateFile := setupMergeTestRepo(t, taskID, agentID)

	// Create a script that hangs forever
	scriptsDir := filepath.Join(tmpDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatalf("Failed to create scripts dir: %v", err)
	}
	script := filepath.Join(scriptsDir, "integration-test.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'starting'\nsleep 3600\n"), 0755); err != nil {
		t.Fatalf("Failed to write test script: %v", err)
	}

	// Override timeout to something short for test
	origTimeout := DefaultIntegrationTestTimeout
	DefaultIntegrationTestTimeout = 500 * time.Millisecond
	t.Cleanup(func() { DefaultIntegrationTestTimeout = origTimeout })

	_, err := MergeWorktree(tmpDir, taskID, agentID)
	if err == nil {
		t.Fatal("Expected error for timed-out test, got nil")
	}

	var intErr *IntegrationFailedError
	if !errors.As(err, &intErr) {
		t.Fatalf("Expected *IntegrationFailedError, got %T: %v", err, err)
	}

	if intErr.Reason != IntegrationReasonTestsFailed {
		t.Errorf("Reason = %q, want %q", intErr.Reason, IntegrationReasonTestsFailed)
	}

	if !strings.Contains(intErr.TestOutput, "killed after") {
		t.Errorf("TestOutput should mention timeout, got %q", intErr.TestOutput)
	}

	// Verify state updated to INTEGRATION_FAILED
	state := readStateForTest(t, stateFile)
	task := state.FindTask(taskID)
	if task == nil {
		t.Fatal("Task not found in state")
	}
	if task.Status != models.TaskStatusIntegrationFailed {
		t.Errorf("Task status = %v, want INTEGRATION_FAILED", task.Status)
	}
}

func TestMergeWorktree_CASRetryDeterministic(t *testing.T) {
	taskID := "merge-cas-retry"
	agentID := "coder-1"
	tmpDir, stateFile := setupMergeTestRepo(t, taskID, agentID)

	// Capture the task commit SHA (the approved worktree commit).
	stateBefore := readStateForTest(t, stateFile)
	taskBefore := stateBefore.FindTask(taskID)
	if taskBefore == nil || taskBefore.ReviewCommit == nil {
		t.Fatal("task/review_commit missing in test setup")
	}
	taskCommit := *taskBefore.ReviewCommit

	// Create a competing integration commit (simulates another reviewer merge)
	// but do not advance integration yet.
	testhelpers.MustGit(t, tmpDir, "checkout", "integration")
	testhelpers.MustGit(t, tmpDir, "checkout", "-b", "competing-cas")
	competingFile := filepath.Join(tmpDir, "competing-cas.txt")
	if err := os.WriteFile(competingFile, []byte("competing change\n"), 0644); err != nil {
		t.Fatalf("Failed to write competing file: %v", err)
	}
	testhelpers.MustGit(t, tmpDir, "add", "competing-cas.txt")
	testhelpers.MustGit(t, tmpDir, "commit", "-m", "Competing integration commit")
	competingSHA := testhelpers.MustGit(t, tmpDir, "rev-parse", "HEAD")
	testhelpers.MustGit(t, tmpDir, "checkout", "integration")
	testhelpers.MustGit(t, tmpDir, "branch", "-D", "competing-cas")

	hookCalls := 0
	previousHook := mergeCASRetryTestHook
	mergeCASRetryTestHook = func(attempt int, integrationRef, preMergeHEAD string) error {
		hookCalls++
		if attempt != 0 {
			return nil
		}
		// Force first attempt to use stale preMergeHEAD by advancing integration
		// immediately after preMergeHEAD is read.
		cmd := exec.Command("git", "-C", tmpDir, "update-ref", integrationRef, competingSHA, preMergeHEAD)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to force CAS conflict: %w (output: %s)", err, output)
		}
		return nil
	}
	defer func() { mergeCASRetryTestHook = previousHook }()

	result, err := MergeWorktree(tmpDir, taskID, agentID)
	if err != nil {
		t.Fatalf("MergeWorktree() unexpected error: %v", err)
	}
	if hookCalls < 2 {
		t.Fatalf("expected at least 2 CAS attempts (initial + retry), got %d", hookCalls)
	}
	if result.MergeCommit == "" {
		t.Fatal("MergeCommit should not be empty")
	}

	// Verify state transitioned to MERGED.
	stateAfter := readStateForTest(t, stateFile)
	taskAfter := stateAfter.FindTask(taskID)
	if taskAfter == nil {
		t.Fatal("Task not found in state")
	}
	if taskAfter.Status != models.TaskStatusMerged {
		t.Errorf("Task status = %v, want MERGED", taskAfter.Status)
	}

	integrationHEAD := testhelpers.MustGit(t, tmpDir, "rev-parse", "integration")
	assertAncestor := func(ancestor, descendant, label string) {
		t.Helper()
		cmd := exec.Command("git", "-C", tmpDir, "merge-base", "--is-ancestor", ancestor, descendant)
		err := cmd.Run()
		if err == nil {
			return
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			t.Fatalf("%s (%s) is not an ancestor of %s", label, ancestor, descendant)
		}
		t.Fatalf("merge-base failed for %s: %v", label, err)
	}
	assertAncestor(taskCommit, integrationHEAD, "task commit")
	assertAncestor(competingSHA, integrationHEAD, "competing commit")
}

func TestMergeWorktree_SuccessWithPassingTests(t *testing.T) {
	taskID := "merge-testpass"
	agentID := "coder-1"
	tmpDir, _ := setupMergeTestRepo(t, taskID, agentID)

	// Create a passing integration test script
	scriptsDir := filepath.Join(tmpDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatalf("Failed to create scripts dir: %v", err)
	}
	script := filepath.Join(scriptsDir, "integration-test.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'ok: all tests passed'\nexit 0\n"), 0755); err != nil {
		t.Fatalf("Failed to write test script: %v", err)
	}

	result, err := MergeWorktree(tmpDir, taskID, agentID)
	if err != nil {
		t.Fatalf("MergeWorktree() unexpected error: %v", err)
	}

	if !result.TestsRan {
		t.Error("TestsRan should be true when integration-test.sh exists")
	}
	if result.NoTestScriptFound {
		t.Error("NoTestScriptFound should be false when integration-test.sh exists")
	}
	if !strings.Contains(result.TestOutput, "all tests passed") {
		t.Errorf("TestOutput = %q, want to contain 'all tests passed'", result.TestOutput)
	}
}

func TestMergeWorktree_HEADMismatch(t *testing.T) {
	taskID := "merge-mismatch"
	agentID := "coder-1"
	tmpDir, stateFile := setupMergeTestRepo(t, taskID, agentID)

	// Tamper with review_commit to create mismatch — use integration HEAD (valid but different)
	cmd := exec.Command("git", "-C", tmpDir, "rev-parse", "integration")
	integrationSHA, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get integration SHA: %v", err)
	}
	mismatchCommit := strings.TrimSpace(string(integrationSHA))

	bb := db.New(stateFile)
	if err := bb.Modify(func(s *models.State) error {
		task := s.FindTask(taskID)
		if task != nil {
			task.ReviewCommit = &mismatchCommit
		}
		return nil
	}); err != nil {
		t.Fatalf("Failed to update state: %v", err)
	}

	_, err = MergeWorktree(tmpDir, taskID, agentID)
	if err == nil {
		t.Fatal("Expected error for HEAD mismatch, got nil")
	}

	var intErr *IntegrationFailedError
	if !errors.As(err, &intErr) {
		t.Fatalf("Expected *IntegrationFailedError, got %T: %v", err, err)
	}
	if intErr.Reason != IntegrationReasonHEADMismatch {
		t.Errorf("Reason = %q, want %q", intErr.Reason, IntegrationReasonHEADMismatch)
	}
}

func TestMergeWorktree_NoTestScriptWarning(t *testing.T) {
	taskID := "merge-notestscript"
	agentID := "coder-1"
	tmpDir, stateFile := setupMergeTestRepo(t, taskID, agentID)

	var result *MergeResult
	var err error
	captureLogOutput(t, func() {
		result, err = MergeWorktree(tmpDir, taskID, agentID)
	})
	if err != nil {
		t.Fatalf("MergeWorktree() unexpected error: %v", err)
	}

	// Verify result distinguishes "no test script found" from "tests ran"
	if result.TestsRan {
		t.Error("TestsRan should be false when integration-test.sh is missing")
	}
	if !result.NoTestScriptFound {
		t.Error("NoTestScriptFound should be true when integration-test.sh is missing")
	}

	// No warning log when script simply doesn't exist — this is a normal case

	// Verify state updated to MERGED
	state := readStateForTest(t, stateFile)
	task := state.FindTask(taskID)
	if task == nil {
		t.Fatal("Task not found in state")
	}
	if task.Status != models.TaskStatusMerged {
		t.Errorf("Task status = %v, want MERGED", task.Status)
	}

	// Verify history entry has tests_ran = false
	if len(task.History) == 0 {
		t.Fatal("Expected history entry for merge")
	}
	lastHistory := task.History[len(task.History)-1]
	if lastHistory.Event != models.TaskEventMerged {
		t.Errorf("Last history event = %q, want 'merged'", lastHistory.Event)
	}
	if lastHistory.Extra == nil {
		t.Fatal("Expected Extra map in history entry")
	}
	testsRanVal, ok := lastHistory.Extra["tests_ran"]
	if !ok {
		t.Fatal("Expected 'tests_ran' field in history Extra")
	}
	if testsRanVal != false {
		t.Errorf("tests_ran = %v, want false", testsRanVal)
	}
}

func TestMergeWorktree_TestsRanInHistory(t *testing.T) {
	taskID := "merge-testshistory"
	agentID := "coder-1"
	tmpDir, stateFile := setupMergeTestRepo(t, taskID, agentID)

	// Create a passing integration test script
	scriptsDir := filepath.Join(tmpDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatalf("Failed to create scripts dir: %v", err)
	}
	script := filepath.Join(scriptsDir, "integration-test.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'ok: all tests passed'\nexit 0\n"), 0755); err != nil {
		t.Fatalf("Failed to write test script: %v", err)
	}

	result, err := MergeWorktree(tmpDir, taskID, agentID)
	if err != nil {
		t.Fatalf("MergeWorktree() unexpected error: %v", err)
	}

	// Verify result
	if !result.TestsRan {
		t.Error("TestsRan should be true when integration-test.sh exists and passes")
	}
	if result.NoTestScriptFound {
		t.Error("NoTestScriptFound should be false when integration-test.sh exists")
	}

	// Verify state
	state := readStateForTest(t, stateFile)
	task := state.FindTask(taskID)
	if task == nil {
		t.Fatal("Task not found in state")
	}

	// Verify history entry has tests_ran = true
	if len(task.History) == 0 {
		t.Fatal("Expected history entry for merge")
	}
	lastHistory := task.History[len(task.History)-1]
	if lastHistory.Event != models.TaskEventMerged {
		t.Errorf("Last history event = %q, want 'merged'", lastHistory.Event)
	}
	if lastHistory.Extra == nil {
		t.Fatal("Expected Extra map in history entry")
	}
	testsRanVal, ok := lastHistory.Extra["tests_ran"]
	if !ok {
		t.Fatal("Expected 'tests_ran' field in history Extra")
	}
	if testsRanVal != true {
		t.Errorf("tests_ran = %v, want true", testsRanVal)
	}
}

func TestMergeWorktree_VerifyCommandFailure(t *testing.T) {
	taskID := "merge-verifyfail"
	agentID := "coder-1"
	tmpDir, stateFile := setupMergeTestRepo(t, taskID, agentID)

	// Add a failing verify_command to the task
	bb := db.New(stateFile)
	if err := bb.Modify(func(s *models.State) error {
		task := s.FindTask(taskID)
		if task != nil {
			if runtime.GOOS == "windows" {
				task.VerifyCommands = []string{"exit /b 1"}
			} else {
				task.VerifyCommands = []string{"exit 1"}
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Failed to add verify_commands: %v", err)
	}

	_, err := MergeWorktree(tmpDir, taskID, agentID)
	if err == nil {
		t.Fatal("Expected error for verify failure, got nil")
	}

	var intErr *IntegrationFailedError
	if !errors.As(err, &intErr) {
		t.Fatalf("Expected *IntegrationFailedError, got %T: %v", err, err)
	}

	if intErr.Reason != IntegrationReasonTestsFailed {
		t.Errorf("Reason = %q, want %q", intErr.Reason, IntegrationReasonTestsFailed)
	}

	// Verify state updated to INTEGRATION_FAILED
	state := readStateForTest(t, stateFile)
	task := state.FindTask(taskID)
	if task == nil {
		t.Fatal("Task not found in state")
	}
	if task.Status != models.TaskStatusIntegrationFailed {
		t.Errorf("Task status = %v, want INTEGRATION_FAILED", task.Status)
	}

	// Verify the VerificationResult was persisted
	if task.VerificationResult == nil {
		t.Fatal("Expected VerificationResult to be persisted on task")
	}
	if task.VerificationResult.Passed {
		t.Error("VerificationResult.Passed should be false")
	}

	// Verify integration branch was rolled back
	cmd := exec.Command("git", "-C", tmpDir, "checkout", "integration")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to checkout integration: %v", err)
	}
	cmd = exec.Command("git", "-C", tmpDir, "log", "--oneline")
	logOutput, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get git log: %v", err)
	}
	if strings.Contains(string(logOutput), "Test commit for "+taskID) {
		t.Error("Integration branch should have been rolled back, but still contains the merged commit")
	}
}

func TestMergeWorktree_NonNotExistStatErrorNotMisclassified(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows returns ErrNotExist (not ENOTDIR) when a path component is a regular file")
	}
	taskID := "merge-script-stat-error"
	agentID := "coder-1"
	tmpDir, stateFile := setupMergeTestRepo(t, taskID, agentID)

	// Create a regular file at <projectRoot>/scripts so os.Stat on
	// <projectRoot>/scripts/integration-test.sh returns ENOTDIR.
	scriptsPath := filepath.Join(tmpDir, "scripts")
	if err := os.WriteFile(scriptsPath, []byte("not-a-directory"), 0644); err != nil {
		t.Fatalf("Failed to create scripts path fixture: %v", err)
	}

	var result *MergeResult
	var err error
	logOutput := captureLogOutput(t, func() {
		result, err = MergeWorktree(tmpDir, taskID, agentID)
	})
	if err != nil {
		t.Fatalf("MergeWorktree() unexpected error: %v", err)
	}

	if result.TestsRan {
		t.Error("TestsRan should be false when integration-test.sh cannot be stat'ed")
	}
	if result.NoTestScriptFound {
		t.Error("NoTestScriptFound should be false for non-not-exist stat errors")
	}
	if !strings.Contains(logOutput, "unable to stat integration test script") {
		t.Fatalf("expected stat-error warning log, got logs: %q", logOutput)
	}

	state := readStateForTest(t, stateFile)
	task := state.FindTask(taskID)
	if task == nil {
		t.Fatal("Task not found in state")
	}
	if task.Status != models.TaskStatusMerged {
		t.Errorf("Task status = %v, want MERGED", task.Status)
	}
}

func captureLogOutput(t *testing.T, fn func()) string {
	t.Helper()

	var buf bytes.Buffer
	origWriter := log.Writer()
	origFlags := log.Flags()
	origPrefix := log.Prefix()

	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")

	defer func() {
		log.SetOutput(origWriter)
		log.SetFlags(origFlags)
		log.SetPrefix(origPrefix)
	}()

	fn()
	return buf.String()
}

// TestMergeWorktree_MissingWorktreeWithReviewCommit tests that MergeWorktree succeeds
// when the worktree was deleted (e.g. by task recovery after Ctrl-C) but review_commit
// is still valid in git. This simulates the real scenario where:
// 1. Writer claims task → worktree created
// 2. Writer submits for review → review_commit set
// 3. Task recovery releases coder claim → Worktree=nil
// 4. Reviewer approves → task is APPROVED with Worktree=nil but valid review_commit
// 5. MergeWorktree should succeed by skipping HEAD verification
func TestMergeWorktree_MissingWorktreeWithReviewCommit(t *testing.T) {
	taskID := "missing-wt"
	agentID := "reviewer-1"

	// Set up a repo with a worktree commit, then remove the worktree from state
	tmpDir, stateFile := setupMergeTestRepo(t, taskID, agentID)

	// Read state and clear Worktree to simulate recovery
	bb := db.New(stateFile)
	err := bb.Modify(func(s *models.State) error {
		task := s.FindTask(taskID)
		if task == nil {
			return fmt.Errorf("task not found")
		}
		task.Worktree = nil
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to clear worktree: %v", err)
	}

	// MergeWorktree should succeed despite missing worktree
	result, err := MergeWorktree(tmpDir, taskID, agentID)
	if err != nil {
		t.Fatalf("MergeWorktree() with missing worktree: unexpected error: %v", err)
	}

	if result.TaskID != taskID {
		t.Errorf("TaskID = %q, want %q", result.TaskID, taskID)
	}
	if result.MergeCommit == "" {
		t.Error("MergeCommit should be non-empty (commit was merged)")
	}

	// Verify state was updated to MERGED
	afterState := readStateForTest(t, stateFile)
	task := afterState.FindTask(taskID)
	if task == nil {
		t.Fatal("Task not found after merge")
	}
	if task.Status != models.TaskStatusMerged {
		t.Errorf("Task status = %s, want %s", task.Status, models.TaskStatusMerged)
	}
}

func TestMergeWorktree_MissingWorktreeNoReviewCommit(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	approvedBy := "reviewer-1"
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{
		{
			ID:           "task-1",
			Status:       models.TaskStatusApproved,
			RolePair:     "coding-pair",
			Worktree:     nil,
			ReviewCommit: nil,
			ApprovedBy:   &approvedBy,
		},
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	_, err := MergeWorktree(tmpDir, "task-1", "reviewer-1")
	testhelpers.RequireErrorContains(t, err, "no review_commit")
}

func TestMergeWorktree_CleansWorkingTreeWhenNotOnIntegration(t *testing.T) {
	taskID := "merge-clean-wt"
	agentID := "coder-1"
	tmpDir, stateFile := setupMergeTestRepo(t, taskID, agentID)

	// Switch main working tree to "main" — NOT integration.
	testhelpers.MustGit(t, tmpDir, "checkout", "main")

	result, err := MergeWorktree(tmpDir, taskID, agentID)
	if err != nil {
		t.Fatalf("MergeWorktree() unexpected error: %v", err)
	}
	if result.MergeCommit == "" {
		t.Fatal("MergeCommit should not be empty")
	}

	// Verify state updated to MERGED.
	state := readStateForTest(t, stateFile)
	task := state.FindTask(taskID)
	if task == nil {
		t.Fatal("Task not found in state")
	}
	if task.Status != models.TaskStatusMerged {
		t.Errorf("Task status = %v, want MERGED", task.Status)
	}

	// After merge, working tree should be clean (relative to checked-out branch HEAD).
	// .liza/ and .worktrees/ are infra dirs excluded from this assertion.
	status := testhelpers.MustGit(t, tmpDir, "status", "--short")
	for _, line := range strings.Split(status, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Parse git status --short format: "XY <path>" or "XY <old> -> <new>"
		// Fields splits on whitespace; field 0 is status, field 1+ is path.
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		path := fields[1]
		// Renames: "XY old -> new" — use the destination path.
		if len(fields) >= 4 && fields[2] == "->" {
			path = fields[3]
		}
		if strings.HasPrefix(path, ".liza/") || strings.HasPrefix(path, ".worktrees/") {
			continue
		}
		t.Errorf("working tree not clean after merge: %s", line)
	}
}

// TestMergeWorktree_DoesNotReleaseCoder_WhenCoderMovedOn verifies that merging
// task-1 does NOT reset a coder to IDLE if the coder has already claimed task-2.
// This is the race: submit task-1 → claim task-2 → merge task-1. The stale
// AssignedTo on task-1 still points to the coder, but CurrentTask has moved on.
func TestMergeWorktree_DoesNotReleaseCoder_WhenCoderMovedOn(t *testing.T) {
	taskID := "merge-guard"
	coderID := "coder-1"
	reviewerID := "code-reviewer-1"
	tmpDir, stateFile := setupMergeTestRepo(t, taskID, coderID)

	// Simulate: coder submitted task-1 and has since claimed task-2.
	// Agent's CurrentTask is now task-2, status WORKING.
	task2ID := "task-2"
	bb := db.New(stateFile)
	err := bb.Modify(func(state *models.State) error {
		state.Agents[coderID] = models.Agent{
			Role:        "coder",
			Status:      models.AgentStatusWorking,
			CurrentTask: &task2ID,
		}
		state.Agents[reviewerID] = models.Agent{
			Role:   "code-reviewer",
			Status: models.AgentStatusWorking,
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to set up agent state: %v", err)
	}

	result, err := MergeWorktree(tmpDir, taskID, reviewerID)
	if err != nil {
		t.Fatalf("MergeWorktree() unexpected error: %v", err)
	}
	if result.MergeCommit == "" {
		t.Fatal("MergeCommit should not be empty")
	}

	// Verify: coder is still WORKING on task-2, NOT reset to IDLE.
	state := readStateForTest(t, stateFile)
	coder, ok := state.Agents[coderID]
	if !ok {
		t.Fatal("Coder agent not found in state")
	}
	if coder.Status != models.AgentStatusWorking {
		t.Errorf("Coder status = %v, want WORKING (should not have been released)", coder.Status)
	}
	if coder.CurrentTask == nil || *coder.CurrentTask != task2ID {
		t.Errorf("Coder CurrentTask = %v, want %q", coder.CurrentTask, task2ID)
	}
}

func TestMergeWorktree_WritesHandoffEvent(t *testing.T) {
	taskID := "merge-handoff"
	agentID := "code-reviewer-1"
	tmpDir, stateFile := setupMergeTestRepo(t, taskID, agentID)

	beforeMerge := time.Now().UTC()
	result, err := MergeWorktree(tmpDir, taskID, agentID)
	if err != nil {
		t.Fatalf("MergeWorktree() unexpected error: %v", err)
	}
	if result.TaskID != taskID {
		t.Errorf("TaskID = %q, want %q", result.TaskID, taskID)
	}

	// Verify state updated to MERGED with HandoffEvent
	state := readStateForTest(t, stateFile)
	task := state.FindTask(taskID)
	if task == nil {
		t.Fatal("Task not found in state")
	}
	if task.Status != models.TaskStatusMerged {
		t.Fatalf("Task status = %v, want MERGED", task.Status)
	}

	// Must have exactly one handoff event (completion)
	if len(task.HandoffEvents) != 1 {
		t.Fatalf("HandoffEvents count = %d, want 1", len(task.HandoffEvents))
	}
	evt := task.HandoffEvents[0]
	if evt.Trigger != models.HandoffTriggerCompletion {
		t.Errorf("HandoffEvent.Trigger = %q, want %q", evt.Trigger, models.HandoffTriggerCompletion)
	}
	if evt.Agent != agentID {
		t.Errorf("HandoffEvent.Agent = %q, want %q", evt.Agent, agentID)
	}
	if evt.Timestamp.Before(beforeMerge) {
		t.Errorf("HandoffEvent.Timestamp = %v, want >= %v", evt.Timestamp, beforeMerge)
	}
}

// readStateForTest reads state from a state file for test verification.
func readStateForTest(t *testing.T, stateFile string) *models.State {
	t.Helper()
	bb := db.New(stateFile)
	state, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}
	return state
}

func TestMarkIntegrationFailed_RefreshesLease(t *testing.T) {
	tmpDir, stateFile := setupPipelineTest(t)

	coderID := "coder-1"
	reviewerID := "code-reviewer-1"
	taskID := "task-lease"
	now := time.Now().UTC()
	leaseDuration := 120 // seconds

	initialState := testhelpers.CreateValidState()
	initialState.Config.IntegrationBranch = "integration"
	initialState.Config.LeaseDuration = leaseDuration

	expiredLease := now.Add(-10 * time.Minute)
	worktree := ".worktrees/" + taskID
	initialState.Tasks = []models.Task{
		{
			ID:           taskID,
			Description:  "Test lease refresh",
			Status:       models.TaskStatusApproved,
			Priority:     1,
			Created:      now,
			SpecRef:      "README.md",
			DoneWhen:     "Done",
			Scope:        "Test",
			RolePair:     "coding-pair",
			Worktree:     &worktree,
			AssignedTo:   &coderID,
			LeaseExpires: &expiredLease,
			History:      []models.TaskHistoryEntry{},
		},
	}
	initialState.Agents[coderID] = models.Agent{
		Role:         "coder",
		Status:       models.AgentStatusWaiting,
		CurrentTask:  &taskID,
		LeaseExpires: &expiredLease,
	}
	initialState.Agents[reviewerID] = models.Agent{
		Role:   "code-reviewer",
		Status: models.AgentStatusWorking,
	}
	testhelpers.WriteInitialState(t, stateFile, initialState)

	bb := db.New(stateFile)
	pb, err := loadPipelineBundle(tmpDir)
	if err != nil {
		t.Fatalf("loadPipelineBundle() error: %v", err)
	}

	callStart := time.Now().UTC()
	// reviewerID is the agent calling MergeWorktree; coderID is AssignedTo
	err = markIntegrationFailed(bb, taskID, reviewerID, "merge conflict", "", pb)
	if err != nil {
		t.Fatalf("markIntegrationFailed() error: %v", err)
	}

	state := readStateForTest(t, stateFile)
	task := state.FindTask(taskID)
	if task == nil {
		t.Fatal("Task not found")
	}

	// Task lease should be refreshed
	expectedMin := callStart.Add(time.Duration(leaseDuration) * time.Second)
	if task.LeaseExpires == nil {
		t.Fatal("Task LeaseExpires is nil, want refreshed lease")
	}
	if task.LeaseExpires.Before(expectedMin) {
		t.Errorf("Task LeaseExpires = %v, want >= %v", task.LeaseExpires, expectedMin)
	}

	// Coder agent lease (AssignedTo) should match task lease
	coderAgent, ok := state.Agents[coderID]
	if !ok {
		t.Fatalf("Coder agent %q not found", coderID)
	}
	if coderAgent.LeaseExpires == nil {
		t.Fatal("Coder agent LeaseExpires is nil, want synced with task")
	}
	if !coderAgent.LeaseExpires.Equal(*task.LeaseExpires) {
		t.Errorf("Coder agent LeaseExpires = %v, want equal to task LeaseExpires %v", coderAgent.LeaseExpires, task.LeaseExpires)
	}

	// Reviewer agent lease should NOT have been refreshed
	reviewerAgent := state.Agents[reviewerID]
	if reviewerAgent.LeaseExpires != nil {
		t.Errorf("Reviewer agent LeaseExpires = %v, want nil (reviewer was not the target)", reviewerAgent.LeaseExpires)
	}
}

// Fix 52: Test auto-recovery when worktree is missing but review_commit is already on integration.
func TestMergeWorktree_MissingWorktreeAlreadyIntegrated(t *testing.T) {
	taskID := "merge-no-wt"
	agentID := "coder-1"
	tmpDir := t.TempDir()

	testhelpers.SetupTestGitRepo(t, tmpDir)
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	// Create integration branch
	cmd := exec.Command("git", "-C", tmpDir, "checkout", "-b", "integration")
	output, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(output), "already exists") {
		t.Fatalf("Failed to create integration branch: %v\nOutput: %s", err, output)
	}
	if strings.Contains(string(output), "already exists") {
		cmd2 := exec.Command("git", "-C", tmpDir, "checkout", "integration")
		if err := cmd2.Run(); err != nil {
			t.Fatalf("Failed to checkout integration branch: %v", err)
		}
	}

	// Make a commit directly on integration (simulating the reviewed commit already merged)
	testFile := filepath.Join(tmpDir, "test-"+taskID+".txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}
	cmd = exec.Command("git", "-C", tmpDir, "add", "test-"+taskID+".txt")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}
	cmd = exec.Command("git", "-C", tmpDir, "commit", "-m", "Reviewed commit for "+taskID)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git commit: %v", err)
	}

	// Get the commit SHA
	cmd = exec.Command("git", "-C", tmpDir, "rev-parse", "HEAD")
	shaOutput, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get commit SHA: %v", err)
	}
	reviewCommit := strings.TrimSpace(string(shaOutput))

	// Create state with APPROVED task but NO worktree
	now := time.Now().UTC()
	initialState := testhelpers.CreateValidState()
	initialState.Config.IntegrationBranch = "integration"

	baseCommit := "base123"
	approvedBy := "code-reviewer-1"
	task := models.Task{
		ID:           taskID,
		Description:  "Test task with missing worktree",
		Status:       models.TaskStatusApproved,
		Priority:     1,
		Created:      now,
		SpecRef:      "README.md",
		DoneWhen:     "Done",
		Scope:        "Test",
		RolePair:     "coding-pair",
		Worktree:     nil, // <-- missing worktree
		AssignedTo:   &agentID,
		BaseCommit:   &baseCommit,
		ReviewCommit: &reviewCommit,
		ApprovedBy:   &approvedBy,
		History:      []models.TaskHistoryEntry{},
	}

	initialState.Tasks = append(initialState.Tasks, task)
	testhelpers.WriteInitialState(t, stateFile, initialState)

	result, err := MergeWorktree(tmpDir, taskID, agentID)
	if err != nil {
		t.Fatalf("MergeWorktree() unexpected error: %v", err)
	}

	if result.TaskID != taskID {
		t.Errorf("TaskID = %q, want %q", result.TaskID, taskID)
	}
	if result.MergeCommit == "" {
		t.Error("MergeCommit should not be empty")
	}

	// Verify state updated to MERGED
	state := readStateForTest(t, stateFile)
	mergedTask := state.FindTask(taskID)
	if mergedTask == nil {
		t.Fatal("Task not found in state")
	}
	if mergedTask.Status != models.TaskStatusMerged {
		t.Errorf("Task status = %v, want MERGED", mergedTask.Status)
	}
	if mergedTask.MergeCommit == nil {
		t.Error("MergeCommit should be set")
	}

	// Verify history entry has auto_recovered flag
	if len(mergedTask.History) == 0 {
		t.Fatal("Expected history entry")
	}
	lastHistory := mergedTask.History[len(mergedTask.History)-1]
	if lastHistory.Event != "merged" {
		t.Errorf("Last history event = %q, want 'merged'", lastHistory.Event)
	}
	if lastHistory.Extra == nil || lastHistory.Extra["auto_recovered"] != true {
		t.Error("Expected auto_recovered=true in history Extra")
	}
}

// Fix 52: Test that missing worktree with review_commit NOT on integration still fails.
func TestMergeWorktree_MissingWorktreeNotIntegrated(t *testing.T) {
	taskID := "merge-no-wt-fail"
	agentID := "coder-1"
	tmpDir := t.TempDir()

	testhelpers.SetupTestGitRepo(t, tmpDir)
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	// Create integration branch
	cmd := exec.Command("git", "-C", tmpDir, "checkout", "-b", "integration")
	output, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(output), "already exists") {
		t.Fatalf("Failed to create integration branch: %v\nOutput: %s", err, output)
	}
	if strings.Contains(string(output), "already exists") {
		cmd2 := exec.Command("git", "-C", tmpDir, "checkout", "integration")
		if err := cmd2.Run(); err != nil {
			t.Fatalf("Failed to checkout integration branch: %v", err)
		}
	}

	// Make a commit on a separate branch (NOT on integration)
	cmd = exec.Command("git", "-C", tmpDir, "checkout", "-b", "task/"+taskID)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create task branch: %v", err)
	}
	testFile := filepath.Join(tmpDir, "test-"+taskID+".txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}
	cmd = exec.Command("git", "-C", tmpDir, "add", "test-"+taskID+".txt")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}
	cmd = exec.Command("git", "-C", tmpDir, "commit", "-m", "Reviewed commit on separate branch")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git commit: %v", err)
	}

	cmd = exec.Command("git", "-C", tmpDir, "rev-parse", "HEAD")
	shaOutput, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get commit SHA: %v", err)
	}
	reviewCommit := strings.TrimSpace(string(shaOutput))

	// Switch back to integration
	cmd = exec.Command("git", "-C", tmpDir, "checkout", "integration")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to checkout integration: %v", err)
	}

	// Create state with APPROVED task, NO worktree, commit NOT on integration
	now := time.Now().UTC()
	initialState := testhelpers.CreateValidState()
	initialState.Config.IntegrationBranch = "integration"

	baseCommit := "base123"
	approvedBy := "code-reviewer-1"
	task := models.Task{
		ID:           taskID,
		Description:  "Test task with missing worktree",
		Status:       models.TaskStatusApproved,
		Priority:     1,
		Created:      now,
		SpecRef:      "README.md",
		DoneWhen:     "Done",
		Scope:        "Test",
		RolePair:     "coding-pair",
		Worktree:     nil,
		AssignedTo:   &agentID,
		BaseCommit:   &baseCommit,
		ReviewCommit: &reviewCommit,
		ApprovedBy:   &approvedBy,
		History:      []models.TaskHistoryEntry{},
	}

	initialState.Tasks = append(initialState.Tasks, task)
	testhelpers.WriteInitialState(t, stateFile, initialState)

	_, err = MergeWorktree(tmpDir, taskID, agentID)
	if err == nil {
		t.Fatal("Expected error for missing worktree with commit not on integration")
	}
	if !strings.Contains(err.Error(), "not on integration branch") {
		t.Errorf("Error = %q, want it to contain 'not on integration branch'", err.Error())
	}

	// Verify task is still APPROVED (no state change)
	state := readStateForTest(t, stateFile)
	unchanged := state.FindTask(taskID)
	if unchanged == nil {
		t.Fatal("Task not found")
	}
	if unchanged.Status != models.TaskStatusApproved {
		t.Errorf("Task status = %v, want APPROVED (unchanged)", unchanged.Status)
	}
}

// Fix 53: Test that runBootstrapInDir runs a bootstrap script when present.
func TestRunBootstrapInDir(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a bootstrap script that creates a marker file
	scriptsDir := filepath.Join(tmpDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatalf("Failed to create scripts dir: %v", err)
	}

	markerFile := filepath.Join(tmpDir, "bootstrapped.marker")
	if runtime.GOOS == "windows" {
		// Create a .bat script on Windows
		script := fmt.Sprintf("@echo off\necho bootstrapped > \"%s\"\n", markerFile)
		if err := os.WriteFile(filepath.Join(scriptsDir, "bootstrap.bat"), []byte(script), 0755); err != nil {
			t.Fatalf("Failed to write script: %v", err)
		}
	} else {
		script := fmt.Sprintf("#!/bin/sh\necho bootstrapped > '%s'\n", markerFile)
		if err := os.WriteFile(filepath.Join(scriptsDir, "bootstrap.sh"), []byte(script), 0755); err != nil {
			t.Fatalf("Failed to write script: %v", err)
		}
	}

	runBootstrapInDir(tmpDir)

	if _, err := os.Stat(markerFile); err != nil {
		t.Errorf("Bootstrap script did not run — marker file missing: %v", err)
	}
}

// Fix 53: Test that runBootstrapInDir is a no-op when no script exists.
func TestRunBootstrapInDir_NoScript(t *testing.T) {
	tmpDir := t.TempDir()

	// Should not panic or error — just a no-op
	logOutput := captureLogOutput(t, func() {
		runBootstrapInDir(tmpDir)
	})

	if strings.Contains(logOutput, "running bootstrap script") {
		t.Error("Should not have attempted to run any bootstrap script")
	}
}
