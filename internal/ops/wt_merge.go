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
	"slices"
	"strings"
	"time"

	"github.com/liza-mas/liza/internal/db"
	lizaerrors "github.com/liza-mas/liza/internal/errors"
	"github.com/liza-mas/liza/internal/git"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
)

// maxMergeRetries is the maximum number of CAS retry attempts for the merge loop.
const maxMergeRetries = 3

// mergeCASRetryTestHook is a test-only hook invoked after reading integration HEAD
// in each CAS attempt and before merge/ref-update logic runs.
// Production code leaves this nil.
var mergeCASRetryTestHook func(attempt int, integrationRef, preMergeHEAD string) error

// makeShellCmd creates an exec.Cmd that can run a shell script on any platform.
// On Windows, it wraps the script with sh/bash from Git for Windows.
func makeShellCmd(scriptPath string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		// Try to find sh.exe (bundled with Git for Windows)
		sh, err := exec.LookPath("sh")
		if err != nil {
			sh, err = exec.LookPath("bash")
		}
		if err == nil {
			// Convert Windows path to forward-slash for sh
			return exec.Command(sh, strings.ReplaceAll(scriptPath, "\\", "/"))
		}
		// Fallback: try running directly (will likely fail but gives clear error)
	}
	return exec.Command(scriptPath)
}

// Integration failure reason constants.
const (
	IntegrationReasonHEADMismatch  = "worktree HEAD mismatch"
	IntegrationReasonMergeConflict = "merge conflict"
	IntegrationReasonTestsFailed   = "integration tests failed"
)

// IntegrationFailedError indicates the merge or integration tests failed.
// State has been updated to INTEGRATION_FAILED appropriately.
type IntegrationFailedError struct {
	Reason        string
	TestOutput    string // non-empty when failure is from integration tests
	RollbackError error  // non-nil if rollback (ResetHard) also failed — integration branch may contain failing code
}

func (e *IntegrationFailedError) Error() string {
	if e.RollbackError != nil {
		return fmt.Sprintf("integration failed: %s (rollback also failed: %v)", e.Reason, e.RollbackError)
	}
	return fmt.Sprintf("integration failed: %s", e.Reason)
}

// MergeResult contains the outcome of a successful worktree merge.
type MergeResult struct {
	TaskID            string
	MergeCommit       string
	FastForward       bool
	TestsRan          bool
	NoTestScriptFound bool     // true when integration test script was missing (distinguishes from "tests ran")
	TestOutput        string   // captured stdout+stderr from integration tests (if any)
	Warnings          []string // non-fatal warnings from cleanup/metrics
}

// appendUniqueAgentID adds an agent ID to failed_by if not already present
func appendUniqueAgentID(failedBy []string, agentID string) []string {
	if slices.Contains(failedBy, agentID) {
		return failedBy
	}
	return append(failedBy, agentID)
}

// markIntegrationFailed transitions a task to INTEGRATION_FAILED under lock.
// Re-validates the task is still APPROVED to prevent concurrent transitions.
// If mergeCommit is non-empty, it's recorded on both the task and the history entry.
func markIntegrationFailed(bb *db.Blackboard, taskID, agentID, reason, mergeCommit string) error {
	return bb.Modify(func(s *models.State) error {
		t := s.FindTask(taskID)
		if t == nil {
			return &lizaerrors.NotFoundError{Entity: "task", ID: taskID}
		}
		if t.Status != models.TaskStatusApproved {
			return fmt.Errorf("task %s status changed concurrently (now %s)", taskID, t.Status)
		}
		if err := t.Transition(models.TaskStatusIntegrationFailed); err != nil {
			return err
		}
		t.FailedBy = appendUniqueAgentID(t.FailedBy, agentID)

		entry := models.TaskHistoryEntry{
			Time:   time.Now(),
			Event:  "integration_failed",
			Agent:  &agentID,
			Reason: &reason,
		}
		if mergeCommit != "" {
			t.MergeCommit = &mergeCommit
			entry.Commit = &mergeCommit
		}
		t.History = append(t.History, entry)

		return nil
	})
}

// MergeWorktree merges an approved task into the integration branch.
// This is the final step in the task lifecycle, integrating completed work.
// Returns IntegrationFailedError if merge conflicts or integration tests fail.
//
// No terminal I/O — integration test output is captured and returned in the result or error.
func MergeWorktree(projectRoot, taskID, agentID string) (*MergeResult, error) {
	if taskID == "" {
		return nil, fmt.Errorf("task ID is required")
	}
	if agentID == "" {
		return nil, fmt.Errorf("agent ID is required")
	}

	// Setup paths
	statePath := paths.New(projectRoot).StatePath()

	// Read state
	bb := db.For(statePath)
	state, task, err := readTaskState(bb, taskID)
	if err != nil {
		return nil, err
	}

	if task.Status != models.TaskStatusApproved {
		return nil, fmt.Errorf("task must be APPROVED to merge (current status: %s)", task.Status)
	}

	if task.Worktree == nil {
		return nil, fmt.Errorf("task has no worktree")
	}

	if task.ReviewCommit == nil {
		return nil, fmt.Errorf("task has no review_commit")
	}

	// Initialize git wrapper
	gitWrapper := git.New(projectRoot)

	// Get worktree HEAD and verify it matches review_commit
	wtHEAD, err := gitWrapper.GetWorktreeHEAD(taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree HEAD: %w", err)
	}

	// Normalize review_commit to full SHA and compare exact match.
	// This mirrors shell parity: resolve refs/short SHAs first, then compare canonical SHAs.
	reviewCommit := *task.ReviewCommit
	expectedCommit, err := gitWrapper.GetCommitSHA(reviewCommit)
	if err != nil {
		return nil, fmt.Errorf("review_commit (%s) not found in repository: %w", reviewCommit, err)
	}
	if wtHEAD != expectedCommit {
		// HEAD mismatch indicates state corruption — stops retry loops, preserves worktree
		shortWtHEAD := wtHEAD
		if len(wtHEAD) > 7 {
			shortWtHEAD = wtHEAD[:7]
		}
		shortReviewCommit := expectedCommit
		if len(expectedCommit) > 7 {
			shortReviewCommit = expectedCommit[:7]
		}
		reason := fmt.Sprintf("worktree HEAD (%s) does not match approved commit (%s)", shortWtHEAD, shortReviewCommit)
		if err := markIntegrationFailed(bb, taskID, agentID, reason, ""); err != nil {
			return nil, fmt.Errorf("failed to update state to INTEGRATION_FAILED: %w", err)
		}

		return nil, &IntegrationFailedError{Reason: IntegrationReasonHEADMismatch}
	}

	// Get integration branch
	integrationBranch := state.Config.IntegrationBranch
	if integrationBranch == "" {
		integrationBranch = "main"
	}

	// Merge with CAS retry loop: read HEAD → merge-tree → commit-tree → update-ref (CAS).
	// On RefConflictError (another merge landed), re-read HEAD and retry.
	integrationRef := "refs/heads/" + integrationBranch

	var mergeCommit string
	var preMergeHEAD string
	var fastForward bool

	var attempt int
	for attempt = 0; attempt < maxMergeRetries; attempt++ {
		if attempt > 0 {
			log.Printf("wt-merge %s: CAS retry attempt %d/%d", taskID, attempt+1, maxMergeRetries)
		}

		// (Re-)read integration HEAD
		preMergeHEAD, err = gitWrapper.GetCommitSHA(integrationRef)
		if err != nil {
			return nil, fmt.Errorf("failed to get integration HEAD: %w", err)
		}
		if mergeCASRetryTestHook != nil {
			if hookErr := mergeCASRetryTestHook(attempt, integrationRef, preMergeHEAD); hookErr != nil {
				return nil, fmt.Errorf("merge CAS retry hook failed: %w", hookErr)
			}
		}

		// Check if worktree HEAD is already ancestor of integration (already merged)
		isAncestor, err := gitWrapper.IsAncestor(expectedCommit, preMergeHEAD)
		if err != nil {
			return nil, fmt.Errorf("failed to check ancestry: %w", err)
		}

		if isAncestor {
			// Already merged - nothing to do, no CAS needed
			mergeCommit = preMergeHEAD
			fastForward = true
			break
		}

		isFF, err := gitWrapper.IsAncestor(preMergeHEAD, expectedCommit)
		if err != nil {
			return nil, fmt.Errorf("failed to check fast-forward: %w", err)
		}

		if isFF {
			// Fast-forward: CAS update ref to task commit
			if err := gitWrapper.UpdateRef(integrationRef, expectedCommit, preMergeHEAD); err != nil {
				var casErr *git.RefConflictError
				if errors.As(err, &casErr) {
					continue // CAS failed — retry from new HEAD
				}
				return nil, fmt.Errorf("failed to fast-forward integration branch: %w", err)
			}
			mergeCommit = expectedCommit
			fastForward = true
			break
		}

		// True merge required - use merge-tree (no working tree modification)
		treeSHA, clean, err := gitWrapper.MergeTree(preMergeHEAD, expectedCommit)
		if err != nil {
			return nil, fmt.Errorf("merge-tree computation failed: %w", err)
		}
		if !clean {
			// Merge conflicts - mark as integration failed (no retry)
			if updateErr := markIntegrationFailed(bb, taskID, agentID, "merge conflict", ""); updateErr != nil {
				return nil, fmt.Errorf("failed to update state to INTEGRATION_FAILED: %w", updateErr)
			}
			return nil, &IntegrationFailedError{Reason: IntegrationReasonMergeConflict}
		}

		// Create merge commit using commit-tree (no working tree needed)
		mergeMsg := "Merge " + taskID + " (task/" + taskID + ")"
		mergeCommit, err = gitWrapper.CreateCommitFromTree(treeSHA, []string{preMergeHEAD, expectedCommit}, mergeMsg)
		if err != nil {
			return nil, fmt.Errorf("failed to create merge commit: %w", err)
		}
		fastForward = false

		// CAS update integration branch to point to new merge commit
		if err := gitWrapper.UpdateRef(integrationRef, mergeCommit, preMergeHEAD); err != nil {
			var casErr *git.RefConflictError
			if errors.As(err, &casErr) {
				continue // CAS failed — another merge landed; retry from new HEAD
			}
			return nil, fmt.Errorf("failed to update integration branch: %w", err)
		}
		break // CAS succeeded
	}
	if attempt == maxMergeRetries {
		return nil, fmt.Errorf("merge CAS failed after %d attempts — high contention on %s", maxMergeRetries, integrationRef)
	}

	// Run integration tests if they exist
	var testsRan bool
	var noTestScriptFound bool
	var testOutput string
	integrationTestScript := filepath.Join(projectRoot, "scripts", "integration-test.sh")
	if _, statErr := os.Stat(integrationTestScript); statErr == nil {
		testsRan = true
		var combinedOutput bytes.Buffer
		cmd := makeShellCmd(integrationTestScript)
		cmd.Dir = projectRoot
		cmd.Stdout = &combinedOutput
		cmd.Stderr = &combinedOutput

		if runErr := cmd.Run(); runErr != nil {
			testOutput = combinedOutput.String()

			// CAS rollback: only rewind if ref still points to our merge commit.
			// If someone else merged on top, rewinding would drop their work.
			var rollbackErr error
			if err := gitWrapper.UpdateRef(integrationRef, preMergeHEAD, mergeCommit); err != nil {
				var casErr *git.RefConflictError
				if errors.As(err, &casErr) {
					log.Printf("wt-merge %s: skipping rollback — another merge landed on top of %s", taskID, mergeCommit[:7])
				} else {
					rollbackErr = err
				}
			}

			if updateErr := markIntegrationFailed(bb, taskID, agentID, "integration tests failed", mergeCommit); updateErr != nil {
				return nil, fmt.Errorf("failed to update state to INTEGRATION_FAILED: %w", updateErr)
			}

			return nil, &IntegrationFailedError{
				Reason:        IntegrationReasonTestsFailed,
				TestOutput:    testOutput,
				RollbackError: rollbackErr,
			}
		}

		testOutput = combinedOutput.String()
	} else if errors.Is(statErr, os.ErrNotExist) {
		// Integration test script not found — expected for projects without one.
		noTestScriptFound = true
	} else {
		// Distinguish actual stat failures from true missing-script cases.
		log.Printf("wt-merge %s: WARNING — unable to stat integration test script at %s: %v; proceeding without tests", taskID, integrationTestScript, statErr)
	}

	// Run task-specific verify_commands if defined (hard gate — failure → INTEGRATION_FAILED)
	// Verify commands run in a temporary worktree of the integration branch (post-merge)
	// so they test the merged result, not the pre-existing project root.
	var verifyResult *models.VerificationResult
	if len(task.VerifyCommands) > 0 {
		// Create a temporary worktree from the merge commit for verification
		verifyDir := filepath.Join(projectRoot, ".worktrees", taskID+"-verify")
		addCmd := exec.Command("git", "worktree", "add", "--detach", verifyDir, mergeCommit)
		addCmd.Dir = projectRoot
		if addErr := addCmd.Run(); addErr != nil {
			log.Printf("wt-merge %s: failed to create verify worktree: %v — falling back to task worktree", taskID, addErr)
			verifyDir = *task.Worktree
		}
		defer func() {
			// Clean up the temporary verify worktree
			rmCmd := exec.Command("git", "worktree", "remove", "--force", verifyDir)
			rmCmd.Dir = projectRoot
			_ = rmCmd.Run()
		}()

		var verifyBuf bytes.Buffer
		verifyPassed := true
		for _, vcmd := range task.VerifyCommands {
			log.Printf("wt-merge %s: running verify command: %s", taskID, vcmd)
			cmd := exec.Command("sh", "-c", vcmd)
			cmd.Dir = verifyDir
			cmd.Stdout = &verifyBuf
			cmd.Stderr = &verifyBuf
			if runErr := cmd.Run(); runErr != nil {
				verifyBuf.WriteString(fmt.Sprintf("\n[FAIL] %s: %v\n", vcmd, runErr))
				verifyPassed = false
				break
			}
			verifyBuf.WriteString(fmt.Sprintf("\n[PASS] %s\n", vcmd))
		}
		verifyResult = &models.VerificationResult{
			Passed:    verifyPassed,
			Output:    verifyBuf.String(),
			Timestamp: time.Now().UTC(),
		}
		if !verifyPassed {
			// Rollback merge if verification fails
			if err := gitWrapper.UpdateRef(integrationRef, preMergeHEAD, mergeCommit); err != nil {
				var casErr *git.RefConflictError
				if !errors.As(err, &casErr) {
					log.Printf("wt-merge %s: rollback after verify failure: %v", taskID, err)
				}
			}
			// Persist verification result before marking failed
			_ = bb.Modify(func(s *models.State) error {
				t := s.FindTask(taskID)
				if t != nil {
					t.VerificationResult = verifyResult
				}
				return nil
			})
			if updateErr := markIntegrationFailed(bb, taskID, agentID, "verify_commands failed", mergeCommit); updateErr != nil {
				return nil, fmt.Errorf("failed to update state to INTEGRATION_FAILED: %w", updateErr)
			}
			return nil, &IntegrationFailedError{
				Reason:     IntegrationReasonTestsFailed,
				TestOutput: verifyResult.Output,
			}
		}
	}

	// Update state to MERGED (before worktree cleanup — if write fails,
	// worktree still exists for investigation; reverse order would lose the worktree
	// while state still says APPROVED)
	err = bb.Modify(func(s *models.State) error {
		t := s.FindTask(taskID)
		if t == nil {
			return &lizaerrors.NotFoundError{Entity: "task", ID: taskID}
		}
		// Re-validate status under lock to prevent concurrent transition
		if t.Status != models.TaskStatusApproved {
			return fmt.Errorf("task %s status changed concurrently (now %s)", taskID, t.Status)
		}
		if err := t.Transition(models.TaskStatusMerged); err != nil {
			return err
		}
		t.Worktree = nil
		t.MergeCommit = &mergeCommit
		t.VerificationResult = verifyResult // nil if no verify_commands defined

		// Release the assigned agent
		if t.AssignedTo != nil {
			s.ReleaseAgent(*t.AssignedTo)
		}

		// Add history entry
		historyEntry := models.TaskHistoryEntry{
			Time:   time.Now(),
			Event:  "merged",
			Agent:  &agentID,
			Commit: &mergeCommit,
			Extra:  map[string]any{"tests_ran": testsRan},
		}
		t.History = append(t.History, historyEntry)

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to update state to MERGED: %w", err)
	}

	// Cleanup: Remove worktree (after state commit — safe to lose worktree now)
	// Errors are non-fatal — state is already committed, collect as warnings
	var warnings []string
	if err := gitWrapper.RemoveWorktree(taskID); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to remove worktree: %v", err))
	}
	// Delete the task branch (idempotent — may already be gone via RemoveWorktree or manual cleanup)
	taskBranch := paths.TaskBranchPrefix + taskID
	if exists, err := gitWrapper.BranchExists(taskBranch); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to check branch %s: %v", taskBranch, err))
	} else if exists {
		if err := gitWrapper.DeleteBranch(taskBranch); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to delete branch %s: %v", taskBranch, err))
		}
	}

	// Update sprint metrics — non-fatal
	if _, err := UpdateSprintMetrics(projectRoot); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to update sprint metrics: %v", err))
	}

	return &MergeResult{
		TaskID:            taskID,
		MergeCommit:       mergeCommit,
		FastForward:       fastForward,
		TestsRan:          testsRan,
		NoTestScriptFound: noTestScriptFound,
		TestOutput:        testOutput,
		Warnings:          warnings,
	}, nil
}
