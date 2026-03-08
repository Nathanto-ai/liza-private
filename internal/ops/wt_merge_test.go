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
	if lastHistory.Event != "merged" {
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
	if lastHistory.Event != "merged" {
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
	if lastHistory.Event != "merged" {
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
