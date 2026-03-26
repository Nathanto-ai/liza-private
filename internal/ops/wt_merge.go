package ops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
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
	"github.com/liza-mas/liza/internal/verify"
)

// maxMergeRetries is the maximum number of CAS retry attempts for the merge loop.
const maxMergeRetries = 3

// DefaultIntegrationTestTimeout bounds how long integration tests may run
// before the merge pipeline kills them. A hanging test without this timeout
// would block the entire merge queue indefinitely.
//
// Exported var (not const) so tests can override. Not parallel-safe:
// tests that override this must not run concurrently with other tests
// that call MergeWorktree. Current wt_merge tests are inherently serial
// (each creates a temp git repo), so this is safe in practice.
var DefaultIntegrationTestTimeout = 10 * time.Minute

// mergeCASRetryTestHook is a test-only hook invoked after reading integration HEAD
// in each CAS attempt and before merge/ref-update logic runs.
// Production code leaves this nil.
var mergeCASRetryTestHook func(attempt int, integrationRef, preMergeHEAD string) error

// findGitShell locates sh.exe from the Git for Windows installation.
// It finds git.exe on PATH and resolves ../bin/sh.exe relative to it,
// avoiding the WSL bash.exe at C:\WINDOWS\system32 which uses incompatible paths.
func findGitShell() (string, error) {
	gitExe, err := exec.LookPath("git")
	if err != nil {
		return "", fmt.Errorf("git not found on PATH: %w", err)
	}
	// git.exe is typically at <Git>/cmd/git.exe; sh.exe is at <Git>/bin/sh.exe
	gitDir := filepath.Dir(filepath.Dir(gitExe))
	sh := filepath.Join(gitDir, "bin", "sh.exe")
	if _, err := os.Stat(sh); err != nil {
		// Also check <Git>/usr/bin/sh.exe (some Git distributions)
		sh = filepath.Join(gitDir, "usr", "bin", "sh.exe")
		if _, err := os.Stat(sh); err != nil {
			return "", fmt.Errorf("sh.exe not found in Git installation at %s", gitDir)
		}
	}
	return sh, nil
}

// makeShellCmd creates an exec.Cmd that can run a shell script on any platform.
// On Windows, it wraps the script with sh/bash from Git for Windows.
func makeShellCmd(scriptPath string) *exec.Cmd {
	return makeShellCmdContext(context.Background(), scriptPath)
}

// makeShellCmdContext creates a context-aware exec.Cmd for shell scripts.
func makeShellCmdContext(ctx context.Context, scriptPath string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		// Use Git for Windows' sh.exe — NOT WSL's bash.exe which uses /mnt/ paths.
		sh, err := findGitShell()
		if err == nil {
			// Convert Windows path to forward-slash for sh
			return exec.CommandContext(ctx, sh, strings.ReplaceAll(scriptPath, "\\", "/"))
		}
		log.Printf("wt-merge: WARNING — could not find Git for Windows sh.exe: %v; falling back to PATH lookup", err)
		// Fallback: try PATH-based lookup (may find WSL bash — not ideal)
		sh2, err2 := exec.LookPath("sh")
		if err2 != nil {
			sh2, err2 = exec.LookPath("bash")
		}
		if err2 == nil {
			return exec.CommandContext(ctx, sh2, strings.ReplaceAll(scriptPath, "\\", "/"))
		}
		// Last resort: try running directly (will likely fail but gives clear error)
	}
	return exec.CommandContext(ctx, scriptPath)
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

// mergeAlreadyIntegrated handles the case where a task's worktree is missing but its
// review_commit is already reachable from the integration branch. This allows automatic
// recovery when an approved task's worktree was lost after the commit was integrated
// (e.g. crash, manual cleanup, or file-sync interference).
// Returns an error if the review_commit is NOT on integration — in that case the task
// truly cannot proceed without its worktree.
func mergeAlreadyIntegrated(
	bb *db.Blackboard,
	gitWrapper *git.Git,
	state *models.State,
	task *models.Task,
	taskID, agentID, projectRoot string,
) (*MergeResult, error) {
	reviewCommit := *task.ReviewCommit
	expectedCommit, err := gitWrapper.GetCommitSHA(reviewCommit)
	if err != nil {
		return nil, fmt.Errorf("task has no worktree and review_commit (%s) not found: %w", reviewCommit, err)
	}

	integrationBranch := state.Config.IntegrationBranch
	if integrationBranch == "" {
		integrationBranch = "main"
	}
	integrationRef := "refs/heads/" + integrationBranch

	integrationHEAD, err := gitWrapper.GetCommitSHA(integrationRef)
	if err != nil {
		return nil, fmt.Errorf("task has no worktree and cannot read integration HEAD: %w", err)
	}

	isAncestor, err := gitWrapper.IsAncestor(expectedCommit, integrationHEAD)
	if err != nil {
		return nil, fmt.Errorf("task has no worktree and ancestry check failed: %w", err)
	}

	if !isAncestor {
		return nil, fmt.Errorf("task has no worktree and review_commit (%s) is not on integration branch", expectedCommit[:7])
	}

	// The approved commit is already on integration — transition directly to MERGED.
	log.Printf("wt-merge %s: worktree missing but review_commit %s is already on integration — auto-recovering to MERGED", taskID, expectedCommit[:7])

	mergeCommit := integrationHEAD
	err = bb.Modify(func(s *models.State) error {
		t := s.FindTask(taskID)
		if t == nil {
			return &lizaerrors.NotFoundError{Entity: "task", ID: taskID}
		}
		if t.Status != models.TaskStatusApproved {
			return fmt.Errorf("task %s status changed concurrently (now %s)", taskID, t.Status)
		}
		if err := t.Transition(models.TaskStatusMerged); err != nil {
			return err
		}
		t.Worktree = nil
		t.MergeCommit = &mergeCommit

		if t.AssignedTo != nil {
			s.ReleaseAgent(*t.AssignedTo)
		}

		reason := "worktree missing — review_commit already on integration"
		t.History = append(t.History, models.TaskHistoryEntry{
			Time:   time.Now(),
			Event:  "merged",
			Agent:  &agentID,
			Commit: &mergeCommit,
			Reason: &reason,
			Extra:  map[string]any{"auto_recovered": true},
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to auto-recover task to MERGED: %w", err)
	}

	var warnings []string
	if _, err := UpdateSprintMetrics(projectRoot); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to update sprint metrics: %v", err))
	}

	return &MergeResult{
		TaskID:      taskID,
		MergeCommit: mergeCommit,
		FastForward: true,
		Warnings:    warnings,
	}, nil
}

// appendUniqueAgentID adds an agent ID to failed_by if not already present
func appendUniqueAgentID(failedBy []string, agentID string) []string {
	if slices.Contains(failedBy, agentID) {
		return failedBy
	}
	return append(failedBy, agentID)
}

// pipelineTransition applies a status transition using the pipeline transition map.
func pipelineTransition(t *models.Task, to models.TaskStatus, pb *pipelineBundle) error {
	return t.TransitionWith(to, pb.transitions)
}

// toVerificationResult converts a verify.Result (from the shared verification
// executor) into a models.VerificationResult with structured per-command data.
func toVerificationResult(vr *verify.Result, phase string) *models.VerificationResult {
	result := &models.VerificationResult{
		Passed:    vr.Passed,
		Timestamp: time.Now().UTC(),
		Phase:     phase,
	}

	var outputBuf strings.Builder
	for _, cr := range vr.Results {
		result.Commands = append(result.Commands, models.VerificationCmdResult{
			Command:  cr.Command,
			ExitCode: cr.ExitCode,
			Output:   cr.Output,
			Duration: cr.Duration,
			Error:    cr.Error,
		})
		if cr.ExitCode == 0 {
			fmt.Fprintf(&outputBuf, "\n[PASS] %s\n", cr.Command)
		} else {
			fmt.Fprintf(&outputBuf, "\n[FAIL] %s: exit code %d\n%s\n", cr.Command, cr.ExitCode, cr.Output)
		}
	}
	result.Output = outputBuf.String()

	return result
}

// markIntegrationFailed transitions a task to INTEGRATION_FAILED under lock.
// Re-validates the task is still in an approved state to prevent concurrent transitions.
// If mergeCommit is non-empty, it's recorded on both the task and the history entry.
func markIntegrationFailed(bb *db.Blackboard, taskID, agentID, reason, mergeCommit string, pb *pipelineBundle) error {
	var pr models.PipelineResolver
	if pb != nil {
		pr = pb.pr
	}
	return bb.Modify(func(s *models.State) error {
		t := s.FindTask(taskID)
		if t == nil {
			return &lizaerrors.NotFoundError{Entity: "task", ID: taskID}
		}
		if !models.IsApprovedForMerge(t, pr) {
			return fmt.Errorf("task %s status changed concurrently (now %s)", taskID, t.Status)
		}
		if err := pipelineTransition(t, models.TaskStatusIntegrationFailed, pb); err != nil {
			return err
		}
		t.FailedBy = appendUniqueAgentID(t.FailedBy, agentID)

		// Refresh lease — task stays assigned to original coder for conflict resolution.
		renewLease(s, t)
		if t.AssignedTo != nil {
			if agent, ok := s.Agents[*t.AssignedTo]; ok {
				agent.LeaseExpires = t.LeaseExpires
				s.Agents[*t.AssignedTo] = agent
			}
		}

		entry := models.TaskHistoryEntry{
			Time:   time.Now(),
			Event:  models.TaskEventIntegrationFailed,
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

// shortSHA truncates a SHA to 7 characters for log messages.
func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// casMergeOutcome holds the result of a compare-and-swap merge into an integration ref.
type casMergeOutcome struct {
	mergeCommit  string // SHA of the resulting commit (merge commit or fast-forwarded task commit)
	preMergeHEAD string // integration HEAD before the merge (needed for working tree sync and rollback)
	fastForward  bool   // true when the merge was a fast-forward or the commit was already merged
	conflict     bool   // true when merge-tree found conflicts; caller handles state transition
}

// performCASMerge merges expectedCommit into integrationRef using a compare-and-swap
// retry loop to handle concurrent merges. Returns conflict=true when merge-tree
// detects conflicts (caller is responsible for the INTEGRATION_FAILED transition).
func performCASMerge(gw *git.Git, integrationRef, expectedCommit, taskID string) (*casMergeOutcome, error) {
	var mergeCommit, preMergeHEAD string
	var fastForward bool

	var attempt int
	for attempt = 0; attempt < maxMergeRetries; attempt++ {
		if attempt > 0 {
			log.Printf("wt-merge %s: CAS retry attempt %d/%d", taskID, attempt+1, maxMergeRetries)
		}

		var err error
		preMergeHEAD, err = gw.GetCommitSHA(integrationRef)
		if err != nil {
			return nil, fmt.Errorf("failed to get integration HEAD: %w", err)
		}
		if mergeCASRetryTestHook != nil {
			if hookErr := mergeCASRetryTestHook(attempt, integrationRef, preMergeHEAD); hookErr != nil {
				return nil, fmt.Errorf("merge CAS retry hook failed: %w", hookErr)
			}
		}

		// Already merged — expectedCommit is ancestor of integration HEAD.
		isAncestor, err := gw.IsAncestor(expectedCommit, preMergeHEAD)
		if err != nil {
			return nil, fmt.Errorf("failed to check ancestry: %w", err)
		}
		if isAncestor {
			return &casMergeOutcome{
				mergeCommit:  preMergeHEAD,
				preMergeHEAD: preMergeHEAD,
				fastForward:  true,
			}, nil
		}

		// Fast-forward: integration HEAD is ancestor of expected commit.
		isFF, err := gw.IsAncestor(preMergeHEAD, expectedCommit)
		if err != nil {
			return nil, fmt.Errorf("failed to check fast-forward: %w", err)
		}
		if isFF {
			if err := gw.UpdateRef(integrationRef, expectedCommit, preMergeHEAD); err != nil {
				var casErr *git.RefConflictError
				if errors.As(err, &casErr) {
					continue
				}
				return nil, fmt.Errorf("failed to fast-forward integration branch: %w", err)
			}
			return &casMergeOutcome{
				mergeCommit:  expectedCommit,
				preMergeHEAD: preMergeHEAD,
				fastForward:  true,
			}, nil
		}

		// True merge — use merge-tree (no working tree modification).
		treeSHA, clean, err := gw.MergeTree(preMergeHEAD, expectedCommit)
		if err != nil {
			return nil, fmt.Errorf("merge-tree computation failed: %w", err)
		}
		if !clean {
			return &casMergeOutcome{
				preMergeHEAD: preMergeHEAD,
				conflict:     true,
			}, nil
		}

		mergeMsg := "Merge " + taskID + " (task/" + taskID + ")"
		mergeCommit, err = gw.CreateCommitFromTree(treeSHA, []string{preMergeHEAD, expectedCommit}, mergeMsg)
		if err != nil {
			return nil, fmt.Errorf("failed to create merge commit: %w", err)
		}
		fastForward = false

		if err := gw.UpdateRef(integrationRef, mergeCommit, preMergeHEAD); err != nil {
			var casErr *git.RefConflictError
			if errors.As(err, &casErr) {
				continue
			}
			return nil, fmt.Errorf("failed to update integration branch: %w", err)
		}
		break
	}
	if attempt == maxMergeRetries {
		return nil, fmt.Errorf("merge CAS failed after %d attempts — high contention on %s", maxMergeRetries, integrationRef)
	}

	return &casMergeOutcome{
		mergeCommit:  mergeCommit,
		preMergeHEAD: preMergeHEAD,
		fastForward:  fastForward,
	}, nil
}

// bootstrapScripts are the conventional project bootstrap scripts checked in order.
// The first one that exists is executed to install dependencies before verify_commands.
var bootstrapScripts = []string{
	"scripts/bootstrap.ps1",
	"scripts/bootstrap.sh",
	"scripts/bootstrap.bat",
}

// runBootstrapInDir runs the first available project bootstrap script in the given
// directory. This ensures verify worktrees have installed dependencies before
// verify_commands execute. Errors are logged but non-fatal — verification will
// still run and report its own failures.
func runBootstrapInDir(dir string) {
	for _, script := range bootstrapScripts {
		scriptPath := filepath.Join(dir, script)
		if _, err := os.Stat(scriptPath); err != nil {
			continue
		}

		log.Printf("wt-merge: running bootstrap script %s in %s", script, dir)
		var cmd *exec.Cmd
		if strings.HasSuffix(script, ".ps1") {
			cmd = exec.Command("powershell", "-ExecutionPolicy", "Bypass", "-File", scriptPath)
		} else if strings.HasSuffix(script, ".bat") {
			cmd = exec.Command("cmd", "/c", scriptPath)
		} else {
			cmd = makeShellCmd(scriptPath)
		}
		cmd.Dir = dir
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		if err := cmd.Run(); err != nil {
			log.Printf("wt-merge: bootstrap script %s failed (non-fatal): %v\n%s", script, err, buf.String())
		} else {
			log.Printf("wt-merge: bootstrap script %s completed successfully", script)
		}
		return // Only run the first matching script
	}
}

// MergeWorktree merges an approved task into the integration branch.
// This is the final step in the task lifecycle, integrating completed work.
// Returns IntegrationFailedError if merge conflicts or integration tests fail.
//
// No terminal I/O — integration test output is captured and returned in the result or error.
func MergeWorktree(projectRoot, taskID, agentID string, mergeExtra ...map[string]any) (*MergeResult, error) {
	if taskID == "" {
		return nil, &PreconditionError{Reason: "task ID is required"}
	}
	if agentID == "" {
		return nil, &PreconditionError{Reason: "agent ID is required"}
	}

	// Setup paths
	statePath := paths.New(projectRoot).StatePath()

	// Read state
	bb := db.For(statePath)
	state, task, err := readTaskState(bb, taskID)
	if err != nil {
		return nil, err
	}

	// Load pipeline bundle once for all pipeline-aware checks
	pb, pbErr := loadPipelineBundle(projectRoot)
	if pbErr != nil {
		return nil, fmt.Errorf("failed to load pipeline config: %w", pbErr)
	}
	pr := pb.pr

	if !models.IsApprovedForMerge(task, pr) {
		return nil, &PreconditionError{Reason: fmt.Sprintf("task must be in an approved state to merge (current status: %s)", task.Status)}
	}


	if task.ReviewCommit == nil {
		return nil, &PreconditionError{Reason: "task has no review_commit"}
	}

	// Initialize git wrapper
	gitWrapper := git.New(projectRoot)

	// Fix 52: If the worktree is missing but the approved review_commit is already
	// reachable from the integration branch, skip directly to the MERGED transition.
	// This recovers from scenarios where the worktree was lost (crash, manual cleanup,
	// OneDrive interference) after the commit was already effectively integrated.
	// If the commit is NOT yet on integration but the physical worktree still exists
	// (state cleared by task recovery), fall through to normal CAS merge path.
	if task.Worktree == nil {
		result, err := mergeAlreadyIntegrated(bb, gitWrapper, state, task, taskID, agentID, projectRoot)
		if err == nil {
			return result, nil
		}
		// If the error is NOT about missing from integration, surface it.
		if !strings.Contains(err.Error(), "is not on integration branch") {
			return nil, err
		}
		// Check if the physical worktree still exists (recovery scenario where
		// state.Worktree was cleared but the git worktree dir remains).
		if _, statErr := os.Stat(gitWrapper.GetWorktreePath(taskID)); statErr != nil {
			return nil, err // No physical worktree either — cannot recover
		}
		log.Printf("wt-merge %s: worktree missing from state but physical worktree exists — proceeding with merge", taskID)
	}

	// Normalize review_commit to full SHA.
	reviewCommit := *task.ReviewCommit
	expectedCommit, err := gitWrapper.GetCommitSHA(reviewCommit)
	if err != nil {
		return nil, fmt.Errorf("review_commit (%s) not found in repository: %w", reviewCommit, err)
	}

	// Worktree-present path: verify HEAD matches review_commit to detect tampering.
	// Worktree-absent path (e.g. cleared by task recovery after Ctrl-C): skip HEAD
	// verification — the approved commit is still in git and safe to merge.
	if task.Worktree != nil {
		wtHEAD, err := gitWrapper.GetWorktreeHEAD(taskID)
		if err != nil {
			return nil, fmt.Errorf("failed to get worktree HEAD: %w", err)
		}

		if wtHEAD != expectedCommit {
			// HEAD mismatch indicates state corruption — stops retry loops, preserves worktree
			reason := fmt.Sprintf("worktree HEAD (%s) does not match approved commit (%s)", shortSHA(wtHEAD), shortSHA(expectedCommit))
			if err := markIntegrationFailed(bb, taskID, agentID, reason, "", pb); err != nil {
				return nil, fmt.Errorf("failed to update state to INTEGRATION_FAILED: %w", err)
			}

			return nil, &IntegrationFailedError{Reason: IntegrationReasonHEADMismatch}
		}
	} else {
		log.Printf("wt-merge %s: WARNING — worktree missing (cleared by recovery?), proceeding with review_commit %s", taskID, shortSHA(expectedCommit))
	}

	// Get integration branch
	integrationBranch := state.Config.IntegrationBranch
	if integrationBranch == "" {
		integrationBranch = "main"
	}

	integrationRef := "refs/heads/" + integrationBranch

	outcome, err := performCASMerge(gitWrapper, integrationRef, expectedCommit, taskID)
	if err != nil {
		return nil, err
	}
	if outcome.conflict {
		if updateErr := markIntegrationFailed(bb, taskID, agentID, "merge conflict", "", pb); updateErr != nil {
			return nil, fmt.Errorf("failed to update state to INTEGRATION_FAILED: %w", updateErr)
		}
		return nil, &IntegrationFailedError{Reason: IntegrationReasonMergeConflict}
	}

	mergeCommit := outcome.mergeCommit
	preMergeHEAD := outcome.preMergeHEAD
	fastForward := outcome.fastForward

	// Sync files changed by the merge into the main working tree.
	// update-ref only moves the ref pointer; without this, files added/modified
	// by the merged commit are absent from the working directory. This is required
	// both for integration test correctness (tests run in projectRoot) and so the
	// working tree reflects what's committed after merge.
	// Only touches merge-affected files — safe for working trees with unrelated
	// pending changes (e.g. .liza/state.yaml).
	if err := gitWrapper.SyncMergedFiles(preMergeHEAD, mergeCommit); err != nil {
		return nil, fmt.Errorf("failed to sync working tree after merge: %w", err)
	}

	// Detect current branch early — needed for working tree restore on both
	// success and rollback paths.
	var warnings []string
	currentBranch, branchErr := gitWrapper.GetCurrentBranch()
	if branchErr != nil {
		warnings = append(warnings, fmt.Sprintf("skipped working tree restore (branch detection failed: %v)", branchErr))
	}

	// Run integration tests if they exist
	var testsRan bool
	var noTestScriptFound bool
	var testOutput string
	integrationTestScript := filepath.Join(projectRoot, "scripts", "integration-test.sh")
	if _, statErr := os.Stat(integrationTestScript); statErr == nil {
		testsRan = true
		var combinedOutput bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), DefaultIntegrationTestTimeout)
		defer cancel()
		cmd := makeShellCmdContext(ctx, integrationTestScript)
		cmd.Dir = projectRoot
		cmd.Stdout = &combinedOutput
		cmd.Stderr = &combinedOutput
		// Kill the entire process tree on timeout (Unix: process group kill;
		// Windows: default CommandContext kill). WaitDelay ensures cmd.Wait
		// returns even if child processes hold pipes open after kill.
		configProcessGroupKill(cmd)
		cmd.WaitDelay = 5 * time.Second

		if runErr := cmd.Run(); runErr != nil {
			testOutput = combinedOutput.String()
			if ctx.Err() == context.DeadlineExceeded {
				testOutput += fmt.Sprintf("\n[liza] integration test killed after %s timeout", DefaultIntegrationTestTimeout)
			}

			// CAS rollback: only rewind if ref still points to our merge commit.
			// If someone else merged on top, rewinding would drop their work.
			var rollbackErr error
			if err := gitWrapper.UpdateRef(integrationRef, preMergeHEAD, mergeCommit); err != nil {
				var casErr *git.RefConflictError
				if errors.As(err, &casErr) {
					log.Printf("wt-merge %s: skipping rollback — another merge landed on top of %s", taskID, shortSHA(mergeCommit))
				} else {
					rollbackErr = err
				}
			} else {
				// Ref rolled back — sync working tree to match pre-merge state.
				// This reverse sync undoes the forward SyncMergedFiles, returning
				// the tree to its pre-MergeWorktree state. No additional restore
				// is needed even when checked out on a non-integration branch.
				if syncErr := gitWrapper.SyncMergedFiles(mergeCommit, preMergeHEAD); syncErr != nil {
					log.Printf("wt-merge %s: WARNING — failed to sync working tree after rollback: %v", taskID, syncErr)
				}
			}

			if updateErr := markIntegrationFailed(bb, taskID, agentID, "integration tests failed", mergeCommit, pb); updateErr != nil {
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

	// Restore working tree when checked-out branch differs from integration.
	if branchErr == nil && currentBranch != integrationBranch {
		if syncErr := gitWrapper.RestoreSyncedFiles(preMergeHEAD, mergeCommit, "HEAD"); syncErr != nil {
			warnings = append(warnings, fmt.Sprintf("failed to restore working tree: %v", syncErr))
		}
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
		verifyWorktreeCreated := true
		if addErr := addCmd.Run(); addErr != nil {
			log.Printf("wt-merge %s: failed to create verify worktree: %v — trying fallbacks", taskID, addErr)
			verifyWorktreeCreated = false
			// Fallback chain: task worktree → project root
			if task.Worktree != nil {
				if _, statErr := os.Stat(*task.Worktree); statErr == nil {
					verifyDir = *task.Worktree
				} else {
					log.Printf("wt-merge %s: task worktree %s not found, falling back to project root", taskID, *task.Worktree)
					verifyDir = projectRoot
				}
			} else {
				verifyDir = projectRoot
			}
		}
		defer func() {
			// Clean up the temporary verify worktree (only if we created it)
			if verifyWorktreeCreated {
				rmCmd := exec.Command("git", "worktree", "remove", "--force", verifyDir)
				rmCmd.Dir = projectRoot
				_ = rmCmd.Run()
			}
		}()

		// Fix 53: Run project bootstrap script in the verify worktree before verify_commands.
		if verifyWorktreeCreated {
			runBootstrapInDir(verifyDir)
		}

		// Use the shared verification executor (same path as post-submission verify)
		cfg := verify.DefaultConfig()
		vResult := verify.RunVerification(context.Background(), task.VerifyCommands, verifyDir, cfg)
		verifyResult = toVerificationResult(vResult, "merge")

		// Fix 38: If verification fails and we weren't running from project root,
		// retry from project root before giving up.
		if !verifyResult.Passed && verifyDir != projectRoot {
			log.Printf("wt-merge %s: verify_commands failed in %s, retrying from project root", taskID, verifyDir)
			retryResult := verify.RunVerification(context.Background(), task.VerifyCommands, projectRoot, cfg)
			retryVerify := toVerificationResult(retryResult, "merge-retry-root")
			if retryVerify.Passed {
				log.Printf("wt-merge %s: verify_commands passed from project root (worktree path was stale)", taskID)
				verifyResult = retryVerify
			}
		}

		if !verifyResult.Passed {
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
			if updateErr := markIntegrationFailed(bb, taskID, agentID, "verify_commands failed", mergeCommit, pb); updateErr != nil {
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
		if !models.IsApprovedForMerge(t, pr) {
			return fmt.Errorf("task %s status changed concurrently (now %s)", taskID, t.Status)
		}
		if err := pipelineTransition(t, models.TaskStatusMerged, pb); err != nil {
			return err
		}
		t.Worktree = nil
		t.MergeCommit = &mergeCommit
		t.VerificationResult = verifyResult // nil if no verify_commands defined

		// Record completion handoff event — audit trail for sprint analysis.
		t.HandoffEvents = append(t.HandoffEvents, models.HandoffEvent{
			Timestamp: time.Now(),
			Agent:     agentID,
			Trigger:   models.HandoffTriggerCompletion,
		})

		// Release the assigned agent — only if still working on this task.
		// After submission the coder's CurrentTask is cleared; if the coder
		// has since claimed another task we must not blow them to IDLE.
		if t.AssignedTo != nil {
			if a, ok := s.Agents[*t.AssignedTo]; ok {
				if a.CurrentTask != nil && *a.CurrentTask == taskID {
					s.ReleaseAgent(*t.AssignedTo)
				}
			}
		}

		// Add history entry with merge-gate extra fields
		extra := map[string]any{"tests_ran": testsRan}
		if len(mergeExtra) > 0 && mergeExtra[0] != nil {
			maps.Copy(extra, mergeExtra[0])
		}
		historyEntry := models.TaskHistoryEntry{
			Time:   time.Now(),
			Event:  models.TaskEventMerged,
			Agent:  &agentID,
			Commit: &mergeCommit,
			Extra:  extra,
		}
		t.History = append(t.History, historyEntry)

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to update state to MERGED: %w", err)
	}

	// Cleanup: Remove worktree directory and branch (after state commit — safe now).
	// Errors are non-fatal — state is already committed, collect as warnings.
	if err := gitWrapper.RemoveWorktreeDir(taskID); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to remove worktree directory: %v", err))
	}
	taskBranch := paths.TaskBranchPrefix + taskID
	if exists, err := gitWrapper.BranchExists(taskBranch); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to check branch %s: %v", taskBranch, err))
	} else if exists {
		if err := gitWrapper.DeleteBranch(taskBranch); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to delete branch %s: %v", taskBranch, err))
		}
	}

	// Check if this merged task was the last active successor of a
	// superseded predecessor — if so, clean up the predecessor's branch.
	warnings = append(warnings, cleanupPredecessorBranches(bb, gitWrapper, taskID)...)

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
