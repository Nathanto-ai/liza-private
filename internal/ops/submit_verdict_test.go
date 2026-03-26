package ops

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/errors"
	"github.com/liza-mas/liza/internal/git"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

func TestSubmitVerdict_Validation(t *testing.T) {
	tests := []struct {
		name        string
		taskID      string
		verdict     string
		reason      string
		agentID     string
		errContains string
	}{
		{
			name: "empty task ID", verdict: "APPROVED", agentID: "r1",
			errContains: "task ID is required",
		},
		{
			name: "empty verdict", taskID: "t1", agentID: "r1",
			errContains: "verdict is required",
		},
		{
			name: "empty agent ID", taskID: "t1", verdict: "APPROVED",
			errContains: "LIZA_AGENT_ID is required",
		},
		{
			name: "invalid verdict", taskID: "t1", verdict: "MAYBE", agentID: "r1",
			errContains: "must be APPROVED or REJECTED",
		},
		{
			name: "rejection without reason", taskID: "t1", verdict: "REJECTED", agentID: "r1",
			errContains: "rejection reason is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SubmitVerdict("/nonexistent", tt.taskID, tt.verdict, tt.reason, tt.agentID, "")
			testhelpers.RequireErrorContains(t, err, tt.errContains)
		})
	}
}

func TestSubmitVerdict_VerdictNormalization(t *testing.T) {
	// Lowercase "approved" should be accepted and normalized
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{
		testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now),
	}
	state.Agents["code-reviewer-1"] = models.Agent{
		Role:   "code-reviewer",
		Status: models.AgentStatusWorking,
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	result, err := SubmitVerdict(tmpDir, "task-1", "approved", "", "code-reviewer-1", "")
	if err != nil {
		t.Fatalf("SubmitVerdict() error: %v", err)
	}
	if result.Verdict != "APPROVED" {
		t.Errorf("Verdict = %q, want %q", result.Verdict, "APPROVED")
	}
}

func TestSubmitVerdict_Approved(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{
		testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now),
	}
	state.Agents["code-reviewer-1"] = models.Agent{
		Role:   "code-reviewer",
		Status: models.AgentStatusWorking,
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	result, err := SubmitVerdict(tmpDir, "task-1", "APPROVED", "", "code-reviewer-1", "")
	if err != nil {
		t.Fatalf("SubmitVerdict() error: %v", err)
	}

	if result.TaskID != "task-1" {
		t.Errorf("TaskID = %q, want %q", result.TaskID, "task-1")
	}
	if result.Verdict != "APPROVED" {
		t.Errorf("Verdict = %q, want %q", result.Verdict, "APPROVED")
	}

	// Verify state
	bb := db.New(stateFile)
	readState, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}

	task := readState.FindTask("task-1")
	if task == nil {
		t.Fatal("Task not found")
	}
	if task.Status != models.TaskStatusApproved {
		t.Errorf("Status = %v, want APPROVED", task.Status)
	}
	if task.ApprovedBy == nil || *task.ApprovedBy != "code-reviewer-1" {
		t.Error("ApprovedBy should be code-reviewer-1")
	}
	if task.RejectionReason != nil {
		t.Error("RejectionReason should be nil after approval")
	}
	if task.ReviewingBy != nil {
		t.Error("ReviewingBy should be cleared")
	}
	if task.ReviewLeaseExpires != nil {
		t.Error("ReviewLeaseExpires should be cleared")
	}

	lastHistory := task.History[len(task.History)-1]
	if lastHistory.Event != models.TaskEventApproved {
		t.Errorf("History event = %q, want %q", lastHistory.Event, models.TaskEventApproved)
	}
}

func TestSubmitVerdict_Rejected(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{
		testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now),
	}
	state.Agents["code-reviewer-1"] = models.Agent{
		Role:   "code-reviewer",
		Status: models.AgentStatusWorking,
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	result, err := SubmitVerdict(tmpDir, "task-1", "REJECTED", "Missing error handling", "code-reviewer-1", "")
	if err != nil {
		t.Fatalf("SubmitVerdict() error: %v", err)
	}

	if result.Verdict != "REJECTED" {
		t.Errorf("Verdict = %q, want %q", result.Verdict, "REJECTED")
	}
	if result.Reason != "Missing error handling" {
		t.Errorf("Reason = %q, want %q", result.Reason, "Missing error handling")
	}
	if result.EscalatedToBlocked {
		t.Error("EscalatedToBlocked = true, want false for normal rejection")
	}

	bb := db.New(stateFile)
	readState, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}

	task := readState.FindTask("task-1")
	if task == nil {
		t.Fatal("Task not found")
	}
	if task.Status != models.TaskStatusRejected {
		t.Errorf("Status = %v, want REJECTED", task.Status)
	}
	if task.RejectionReason == nil || *task.RejectionReason != "Missing error handling" {
		t.Error("RejectionReason not set correctly")
	}
	if task.ReviewCyclesCurrent != 1 {
		t.Errorf("ReviewCyclesCurrent = %d, want 1", task.ReviewCyclesCurrent)
	}
	if task.ReviewCyclesTotal != 1 {
		t.Errorf("ReviewCyclesTotal = %d, want 1", task.ReviewCyclesTotal)
	}

	lastHistory := task.History[len(task.History)-1]
	if lastHistory.Event != models.TaskEventRejected {
		t.Errorf("History event = %q, want %q", lastHistory.Event, models.TaskEventRejected)
	}
}

func TestSubmitVerdict_TaskNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	state := testhelpers.CreateValidState()
	testhelpers.WriteInitialState(t, stateFile, state)

	_, err := SubmitVerdict(tmpDir, "nonexistent", "APPROVED", "", "code-reviewer-1", "")
	if err == nil {
		t.Fatal("Expected error for nonexistent task")
	}
	if !errors.IsNotFound(err) {
		t.Errorf("expected NotFoundError, got %T: %v", err, err)
	}
}

func TestSubmitVerdict_WrongStatus(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{
		testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now),
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	_, err := SubmitVerdict(tmpDir, "task-1", "APPROVED", "", "code-reviewer-1", "")
	testhelpers.RequireErrorContains(t, err, "not in a reviewing state")
}

func TestSubmitVerdict_AgentReleased(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{
		testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now),
	}
	taskRef := "task-1"
	state.Agents["code-reviewer-1"] = models.Agent{
		Role:        "code-reviewer",
		Status:      models.AgentStatusWorking,
		CurrentTask: &taskRef,
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	_, err := SubmitVerdict(tmpDir, "task-1", "APPROVED", "", "code-reviewer-1", "")
	if err != nil {
		t.Fatalf("SubmitVerdict() error: %v", err)
	}

	bb := db.New(stateFile)
	readState, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}

	agent := readState.Agents["code-reviewer-1"]
	if agent.Status != models.AgentStatusIdle {
		t.Errorf("Agent status = %v, want idle", agent.Status)
	}
	if agent.CurrentTask != nil {
		t.Error("Agent CurrentTask should be nil after verdict")
	}
}

func TestSubmitVerdict_RejectedLimitEscalationTransitionsToBlocked(t *testing.T) {
	tests := []struct {
		name               string
		rejectionReason    string
		configureStateTask func(*models.State, *models.Task)
		wantReasonContains string
		wantQuestionHint   string
		wantReviewCurrent  int
		wantReviewTotal    int
	}{
		{
			name:            "review cycle limit",
			rejectionReason: "Still failing",
			configureStateTask: func(state *models.State, task *models.Task) {
				state.Config.MaxReviewCycles = 2
				task.ReviewCyclesCurrent = 1
				task.ReviewCyclesTotal = 1
				task.Attempt = 2
			},
			wantReasonContains: "review budget exhausted",
			wantQuestionHint:   "review cycle",
			wantReviewCurrent:  2,
			wantReviewTotal:    2,
		},
		{
			name:            "task iteration limit",
			rejectionReason: "Needs redesign",
			configureStateTask: func(state *models.State, task *models.Task) {
				state.Config.MaxReviewCycles = 5
				state.Config.MaxCoderIterations = 10
				task.Iteration = 2
				task.MaxIterations = 2
				task.Attempt = 2
			},
			wantReasonContains: "max iterations",
			wantQuestionHint:   "max iterations were exhausted",
			wantReviewCurrent:  1,
			wantReviewTotal:    1,
		},
		{
			name:            "combined limits",
			rejectionReason: "Needs rescope",
			configureStateTask: func(state *models.State, task *models.Task) {
				state.Config.MaxReviewCycles = 2
				state.Config.MaxCoderIterations = 10
				task.ReviewCyclesCurrent = 1
				task.ReviewCyclesTotal = 4
				task.Iteration = 2
				task.MaxIterations = 2
				task.Attempt = 2
			},
			wantReasonContains: "review budget and iteration limits exhausted",
			wantQuestionHint:   "both review cycles and iterations",
			wantReviewCurrent:  2,
			wantReviewTotal:    5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

			now := time.Now().UTC()
			state := testhelpers.CreateValidState()
			task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now)
			tt.configureStateTask(state, &task)
			state.Tasks = []models.Task{task}

			taskRef := "task-1"
			state.Agents["coder-1"] = models.Agent{
				Role:        "coder",
				Status:      models.AgentStatusWaiting,
				CurrentTask: &taskRef,
			}
			state.Agents["code-reviewer-1"] = models.Agent{
				Role:        "code-reviewer",
				Status:      models.AgentStatusReviewing,
				CurrentTask: &taskRef,
			}

			testhelpers.WriteInitialState(t, stateFile, state)

			result, err := SubmitVerdict(tmpDir, "task-1", "REJECTED", tt.rejectionReason, "code-reviewer-1", "")
			if err != nil {
				t.Fatalf("SubmitVerdict() error: %v", err)
			}
			if !result.EscalatedToBlocked {
				t.Error("EscalatedToBlocked = false, want true")
			}
			if !strings.Contains(result.BlockedReason, tt.wantReasonContains) {
				t.Errorf("BlockedReason = %q, want to contain %q", result.BlockedReason, tt.wantReasonContains)
			}

			bb := db.New(stateFile)
			readState, err := bb.Read()
			if err != nil {
				t.Fatalf("Failed to read state: %v", err)
			}

			blockedTask := readState.FindTask("task-1")
			if blockedTask == nil {
				t.Fatal("Task not found")
			}
			if blockedTask.Status != models.TaskStatusBlocked {
				t.Errorf("Status = %v, want BLOCKED", blockedTask.Status)
			}
			if blockedTask.BlockedReason == nil || !strings.Contains(*blockedTask.BlockedReason, tt.wantReasonContains) {
				t.Errorf("BlockedReason = %v, want to contain %q", blockedTask.BlockedReason, tt.wantReasonContains)
			}
			if len(blockedTask.BlockedQuestions) == 0 || !strings.Contains(blockedTask.BlockedQuestions[0], tt.wantQuestionHint) {
				t.Errorf("BlockedQuestions = %v, want first question to contain %q", blockedTask.BlockedQuestions, tt.wantQuestionHint)
			}
			if blockedTask.ReviewCyclesCurrent != tt.wantReviewCurrent {
				t.Errorf("ReviewCyclesCurrent = %d, want %d", blockedTask.ReviewCyclesCurrent, tt.wantReviewCurrent)
			}
			if blockedTask.ReviewCyclesTotal != tt.wantReviewTotal {
				t.Errorf("ReviewCyclesTotal = %d, want %d", blockedTask.ReviewCyclesTotal, tt.wantReviewTotal)
			}
			if blockedTask.AssignedTo != nil {
				t.Error("AssignedTo should be cleared after escalation")
			}
			if blockedTask.ReviewingBy != nil || blockedTask.ReviewLeaseExpires != nil {
				t.Error("Review lease fields should be cleared")
			}

			assertReleasedAgent(t, readState, "coder-1")
			assertReleasedAgent(t, readState, "code-reviewer-1")
		})
	}
}

func TestSubmitVerdict_MissingReviewCommit(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now)
	task.ReviewCommit = nil // Corrupt: REVIEWING without review_commit
	state.Tasks = []models.Task{task}
	state.Agents["code-reviewer-1"] = models.Agent{
		Role:   "code-reviewer",
		Status: models.AgentStatusWorking,
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	_, err := SubmitVerdict(tmpDir, "task-1", "APPROVED", "", "code-reviewer-1", "")
	if err == nil {
		t.Fatal("Expected error for missing review_commit, got nil")
	}
	if !strings.Contains(err.Error(), "no review_commit") {
		t.Errorf("Error = %q, want to contain 'no review_commit'", err.Error())
	}
}

func TestSubmitVerdict_ReviewCommitMismatch(t *testing.T) {
	tmpDir := t.TempDir()

	// Setup git repo + liza dir
	testhelpers.SetupTestGitRepo(t, tmpDir)
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	// Create worktree
	g := git.New(tmpDir)
	_, err := g.CreateWorktree("task-1", "integration")
	if err != nil {
		t.Fatalf("Failed to create worktree: %v", err)
	}
	wtPath := g.GetWorktreePath("task-1")

	// Make a commit in the worktree so HEAD diverges from integration
	implFile := filepath.Join(wtPath, "feature.go")
	if err := os.WriteFile(implFile, []byte("package feature\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, wtPath, "add", "feature.go")
	testhelpers.MustGit(t, wtPath, "commit", "-m", "Add feature")

	// Record a stale ReviewCommit (integration HEAD, not worktree HEAD)
	staleCommit := testhelpers.MustGit(t, tmpDir, "rev-parse", "integration")

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now)
	task.ReviewCommit = &staleCommit
	worktreeRel := g.GetWorktreeRelPath("task-1")
	task.Worktree = &worktreeRel
	state.Tasks = []models.Task{task}
	state.Agents["code-reviewer-1"] = models.Agent{
		Role:   "code-reviewer",
		Status: models.AgentStatusWorking,
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	_, err = SubmitVerdict(tmpDir, "task-1", "APPROVED", "", "code-reviewer-1", "")
	if err == nil {
		t.Fatal("Expected error for ReviewCommit vs worktree HEAD mismatch")
	}
	if !strings.Contains(err.Error(), "does not match worktree HEAD") {
		t.Fatalf("Expected mismatch error, got: %v", err)
	}

	// Verify task state unchanged
	bb := db.New(stateFile)
	readState, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}
	readTask := readState.FindTask("task-1")
	if readTask.Status != models.TaskStatusReviewing {
		t.Errorf("Status = %v, want REVIEWING (unchanged)", readTask.Status)
	}
}

func TestSubmitVerdict_StatErrorNotSilenced(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows returns ErrNotExist (not ENOTDIR) when a path component is a regular file")
	}
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	reviewCommit := "abc123def456"
	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now)
	task.ReviewCommit = &reviewCommit
	state.Tasks = []models.Task{task}
	state.Agents["code-reviewer-1"] = models.Agent{
		Role:   "code-reviewer",
		Status: models.AgentStatusWorking,
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	// Create a regular file at .worktrees so os.Stat(.worktrees/task-1)
	// returns ENOTDIR instead of ENOENT.
	wtParent := filepath.Join(tmpDir, ".worktrees")
	if err := os.WriteFile(wtParent, []byte("not-a-directory"), 0644); err != nil {
		t.Fatalf("Failed to create fixture: %v", err)
	}

	_, err := SubmitVerdict(tmpDir, "task-1", "APPROVED", "", "code-reviewer-1", "")
	if err == nil {
		t.Fatal("Expected stat error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to stat worktree") {
		t.Fatalf("Expected 'failed to stat worktree' error, got: %v", err)
	}

	// Verify task state unchanged
	bb := db.New(stateFile)
	readState, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}
	readTask := readState.FindTask("task-1")
	if readTask.Status != models.TaskStatusReviewing {
		t.Errorf("Status = %v, want REVIEWING (unchanged)", readTask.Status)
	}
}

func TestSubmitVerdictApprovals(t *testing.T) {
	t.Run("approved builds approval and sets derived approved_by", func(t *testing.T) {
		tmpDir := t.TempDir()
		stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

		now := time.Now().UTC()
		state := testhelpers.CreateValidState()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now),
		}
		state.Agents["code-reviewer-1"] = models.Agent{
			Role:     "code-reviewer",
			Status:   models.AgentStatusWorking,
			Provider: "claude",
		}
		testhelpers.WriteInitialState(t, stateFile, state)

		result, err := SubmitVerdict(tmpDir, "task-1", "APPROVED", "", "code-reviewer-1", "")
		if err != nil {
			t.Fatalf("SubmitVerdict() error: %v", err)
		}
		if result.Verdict != "APPROVED" {
			t.Errorf("Verdict = %q, want %q", result.Verdict, "APPROVED")
		}

		bb := db.New(stateFile)
		readState, err := bb.Read()
		if err != nil {
			t.Fatalf("Failed to read state: %v", err)
		}

		task := readState.FindTask("task-1")
		if task == nil {
			t.Fatal("Task not found")
		}

		// Verify approvals list
		if task.ApprovalCount() != 1 {
			t.Fatalf("ApprovalCount() = %d, want 1", task.ApprovalCount())
		}
		approval := task.Approvals[0]
		if approval.Agent != "code-reviewer-1" {
			t.Errorf("Approval.Agent = %q, want %q", approval.Agent, "code-reviewer-1")
		}
		if approval.Provider != "claude" {
			t.Errorf("Approval.Provider = %q, want %q", approval.Provider, "claude")
		}
		if approval.Timestamp.IsZero() {
			t.Error("Approval.Timestamp is zero")
		}

		// Verify derived ApprovedBy for backward compat
		if task.ApprovedBy == nil || *task.ApprovedBy != "code-reviewer-1" {
			t.Error("ApprovedBy (derived) should be code-reviewer-1")
		}

		// Verify LastApprover matches
		if task.LastApprover() != "code-reviewer-1" {
			t.Errorf("LastApprover() = %q, want %q", task.LastApprover(), "code-reviewer-1")
		}
	})

	t.Run("rejected clears approvals", func(t *testing.T) {
		tmpDir := t.TempDir()
		stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

		now := time.Now().UTC()
		state := testhelpers.CreateValidState()
		task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now)
		// Pre-populate approvals and derived ApprovedBy (simulating a partially-approved task re-entering review)
		task.Approvals = []models.Approval{
			{Agent: "code-reviewer-2", Provider: "codex", Timestamp: now.Add(-10 * time.Minute)},
		}
		priorApprover := "code-reviewer-2"
		task.ApprovedBy = &priorApprover
		state.Tasks = []models.Task{task}
		state.Agents["code-reviewer-1"] = models.Agent{
			Role:     "code-reviewer",
			Status:   models.AgentStatusWorking,
			Provider: "claude",
		}
		testhelpers.WriteInitialState(t, stateFile, state)

		result, err := SubmitVerdict(tmpDir, "task-1", "REJECTED", "Needs rework", "code-reviewer-1", "")
		if err != nil {
			t.Fatalf("SubmitVerdict() error: %v", err)
		}
		if result.Verdict != "REJECTED" {
			t.Errorf("Verdict = %q, want %q", result.Verdict, "REJECTED")
		}

		bb := db.New(stateFile)
		readState, err := bb.Read()
		if err != nil {
			t.Fatalf("Failed to read state: %v", err)
		}

		rejTask := readState.FindTask("task-1")
		if rejTask == nil {
			t.Fatal("Task not found")
		}
		if rejTask.Approvals != nil {
			t.Errorf("Approvals = %v, want nil after rejection", rejTask.Approvals)
		}
		if rejTask.ApprovedBy != nil {
			t.Errorf("ApprovedBy = %v, want nil after rejection (derived field must be cleared with approvals)", *rejTask.ApprovedBy)
		}
	})

	t.Run("approved with empty provider falls back gracefully", func(t *testing.T) {
		tmpDir := t.TempDir()
		stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

		now := time.Now().UTC()
		state := testhelpers.CreateValidState()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now),
		}
		// Agent without provider set (backward compat scenario)
		state.Agents["code-reviewer-1"] = models.Agent{
			Role:   "code-reviewer",
			Status: models.AgentStatusWorking,
		}
		testhelpers.WriteInitialState(t, stateFile, state)

		result, err := SubmitVerdict(tmpDir, "task-1", "APPROVED", "", "code-reviewer-1", "")
		if err != nil {
			t.Fatalf("SubmitVerdict() error: %v", err)
		}
		if result.Verdict != "APPROVED" {
			t.Errorf("Verdict = %q, want %q", result.Verdict, "APPROVED")
		}

		bb := db.New(stateFile)
		readState, err := bb.Read()
		if err != nil {
			t.Fatalf("Failed to read state: %v", err)
		}

		task := readState.FindTask("task-1")
		if task.ApprovalCount() != 1 {
			t.Fatalf("ApprovalCount() = %d, want 1", task.ApprovalCount())
		}
		// Provider should be empty string, not cause a crash
		if task.Approvals[0].Provider != "" {
			t.Errorf("Approval.Provider = %q, want empty string", task.Approvals[0].Provider)
		}
	})
}

func TestSubmitVerdict_ApprovedFromReviewing2(t *testing.T) {
	// Verifies that a verdict can be submitted from REVIEWING_CODE_2 state
	// (second review in quorum flow). The task should transition to APPROVED.
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	reviewCommit := "review123"
	worktree := ".worktrees/task-1"
	reviewingBy := "code-reviewer-2"
	reviewLease := now.Add(30 * time.Minute)
	state.Tasks = []models.Task{
		{
			ID:                 "task-1",
			Status:             models.TaskStatusReviewingCode2,
			RolePair:           "coding-pair",
			Priority:           1,
			ReviewCommit:       &reviewCommit,
			Worktree:           &worktree,
			ReviewingBy:        &reviewingBy,
			ReviewLeaseExpires: &reviewLease,
			History:            []models.TaskHistoryEntry{},
			Created:            now,
			Approvals: []models.Approval{
				{Agent: "code-reviewer-1", Provider: "anthropic", Timestamp: now},
			},
		},
	}
	state.Agents["code-reviewer-2"] = models.Agent{
		Role:   "code-reviewer",
		Status: models.AgentStatusReviewing,
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	result, err := SubmitVerdict(tmpDir, "task-1", "APPROVED", "", "code-reviewer-2", "")
	if err != nil {
		t.Fatalf("SubmitVerdict() error: %v", err)
	}
	if result.Verdict != "APPROVED" {
		t.Errorf("Verdict = %q, want %q", result.Verdict, "APPROVED")
	}

	bb := db.New(stateFile)
	readState, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}

	task := readState.FindTask("task-1")
	if task == nil {
		t.Fatal("Task not found")
	}
	if task.Status != models.TaskStatusApproved {
		t.Errorf("Status = %v, want CODE_APPROVED", task.Status)
	}
	if task.ApprovedBy == nil || *task.ApprovedBy != "code-reviewer-2" {
		t.Error("ApprovedBy should be code-reviewer-2")
	}
	if task.ReviewingBy != nil {
		t.Error("ReviewingBy should be cleared")
	}
}

func TestSubmitVerdict_RejectedFromReviewing2(t *testing.T) {
	// Verifies that a rejection can be submitted from REVIEWING_CODE_2 state.
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	reviewCommit := "review123"
	worktree := ".worktrees/task-1"
	reviewingBy := "code-reviewer-2"
	reviewLease := now.Add(30 * time.Minute)
	state.Tasks = []models.Task{
		{
			ID:                 "task-1",
			Status:             models.TaskStatusReviewingCode2,
			RolePair:           "coding-pair",
			Priority:           1,
			ReviewCommit:       &reviewCommit,
			Worktree:           &worktree,
			ReviewingBy:        &reviewingBy,
			ReviewLeaseExpires: &reviewLease,
			History:            []models.TaskHistoryEntry{},
			Created:            now,
			Approvals: []models.Approval{
				{Agent: "code-reviewer-1", Provider: "anthropic", Timestamp: now},
			},
		},
	}
	state.Agents["code-reviewer-2"] = models.Agent{
		Role:   "code-reviewer",
		Status: models.AgentStatusReviewing,
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	result, err := SubmitVerdict(tmpDir, "task-1", "REJECTED", "Needs improvement", "code-reviewer-2", "")
	if err != nil {
		t.Fatalf("SubmitVerdict() error: %v", err)
	}
	if result.Verdict != "REJECTED" {
		t.Errorf("Verdict = %q, want %q", result.Verdict, "REJECTED")
	}

	bb := db.New(stateFile)
	readState, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}

	task := readState.FindTask("task-1")
	if task == nil {
		t.Fatal("Task not found")
	}
	if task.Status != models.TaskStatusRejected {
		t.Errorf("Status = %v, want CODE_REJECTED", task.Status)
	}
	if task.RejectionReason == nil || *task.RejectionReason != "Needs improvement" {
		t.Error("RejectionReason not set correctly")
	}
}

func TestResolveEffectiveImpact(t *testing.T) {
	tests := []struct {
		name    string
		history []models.TaskHistoryEntry
		want    string
	}{
		{
			name:    "no impact declared returns standard",
			history: nil,
			want:    "standard",
		},
		{
			name: "checkpoint-only impact",
			history: []models.TaskHistoryEntry{
				{Event: models.TaskEventPreExecutionCheckpoint, Extra: map[string]any{"impact": "significant"}},
			},
			want: "significant",
		},
		{
			name: "verdict upgrades checkpoint impact",
			history: []models.TaskHistoryEntry{
				{Event: models.TaskEventPreExecutionCheckpoint, Extra: map[string]any{"impact": "significant"}},
				{Event: models.TaskEventApproved, Extra: map[string]any{"impact": "architecture"}},
			},
			want: "architecture",
		},
		{
			name: "rejection resets cycle — post-rejection checkpoint starts fresh",
			history: []models.TaskHistoryEntry{
				{Event: models.TaskEventPreExecutionCheckpoint, Extra: map[string]any{"impact": "architecture"}},
				{Event: models.TaskEventRejected},
				{Event: models.TaskEventPreExecutionCheckpoint, Extra: map[string]any{"impact": "standard"}},
			},
			want: "standard",
		},
		{
			name: "entries without impact are ignored",
			history: []models.TaskHistoryEntry{
				{Event: models.TaskEventPreExecutionCheckpoint, Extra: map[string]any{"impact": "significant"}},
				{Event: models.TaskEventSubmittedForReview},
			},
			want: "significant",
		},
		{
			name: "only checkpoint and verdict events contribute impact",
			history: []models.TaskHistoryEntry{
				{Event: models.TaskEventPreExecutionCheckpoint, Extra: map[string]any{"impact": "standard"}},
				{Event: models.TaskEventApproved, Extra: map[string]any{"impact": "significant"}},
				{Event: models.TaskEventBlocked},
			},
			want: "significant",
		},
		{
			name: "empty extra on checkpoint defaults to standard",
			history: []models.TaskHistoryEntry{
				{Event: models.TaskEventPreExecutionCheckpoint},
			},
			want: "standard",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveEffectiveImpact(tt.history)
			if got != tt.want {
				t.Errorf("ResolveEffectiveImpact() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestQuorumEvaluation(t *testing.T) {
	setupQuorumEnv := func(t *testing.T, task models.Task, agents map[string]models.Agent, pipelineYAML string) (string, string) {
		t.Helper()
		tmpDir := t.TempDir()
		stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

		// Write custom pipeline config
		pipelinePath := filepath.Join(tmpDir, ".liza", "pipeline.yaml")
		if err := os.WriteFile(pipelinePath, []byte(pipelineYAML), 0644); err != nil {
			t.Fatalf("Failed to write pipeline config: %v", err)
		}

		state := testhelpers.CreateValidState()
		state.Tasks = []models.Task{task}
		for id, agent := range agents {
			state.Agents[id] = agent
		}
		testhelpers.WriteInitialState(t, stateFile, state)
		return tmpDir, stateFile
	}

	// Pipeline with quorum 1 (standard) but quorum 2 for architecture
	quorum2Pipeline := `pipeline:
  roles:
    coder:
      type: doer
      display-name: Coder
      timeouts: {execution: 2h, poll-interval: 30s, max-wait: 30m}
      context-sections: [assigned-task]
      allowed-operations: [write-checkpoint, submit-for-review]
    code-reviewer:
      type: reviewer
      display-name: Code Reviewer
      timeouts: {execution: 30m, poll-interval: 30s, max-wait: 30m}
      context-sections: [review-task]
      allowed-operations: [submit-verdict]
    orchestrator:
      type: orchestrator
      display-name: Orchestrator
      max-instances: 1
      timeouts: {execution: 4h, poll-interval: 60s, max-wait: 30m}
      context-sections: [orchestrator-dashboard]
      allowed-operations: [add-tasks]
  role-pairs:
    coding-pair:
      doer: coder
      reviewer: code-reviewer
      review-policy:
        quorum: 1
        significant-change:
          quorum: 2
          provider-diversity: preferred
        architecture-impact:
          quorum: 2
          provider-diversity: preferred
      states:
        initial: DRAFT_CODE
        executing: IMPLEMENTING_CODE
        submitted: CODE_READY_FOR_REVIEW
        reviewing: REVIEWING_CODE
        approved: CODE_APPROVED
        rejected: CODE_REJECTED
        partially-approved: CODE_PARTIALLY_APPROVED
        reviewing-2: REVIEWING_CODE_2
  sub-pipelines:
    coding:
      steps: [coding-pair]
`

	t.Run("quorum-1 standard path — single approval transitions to approved", func(t *testing.T) {
		now := time.Now().UTC()
		task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now)
		// Checkpoint with standard impact
		task.History = append(task.History, models.TaskHistoryEntry{
			Time:  now.Add(-5 * time.Minute),
			Event: models.TaskEventPreExecutionCheckpoint,
			Extra: map[string]any{"impact": "standard"},
		})

		tmpDir, stateFile := setupQuorumEnv(t, task, map[string]models.Agent{
			"code-reviewer-1": {Role: "code-reviewer", Status: models.AgentStatusWorking, Provider: "claude"},
		}, quorum2Pipeline)

		result, err := SubmitVerdict(tmpDir, "task-1", "APPROVED", "", "code-reviewer-1", "")
		if err != nil {
			t.Fatalf("SubmitVerdict() error: %v", err)
		}
		if result.Verdict != "APPROVED" {
			t.Errorf("Verdict = %q, want %q", result.Verdict, "APPROVED")
		}

		bb := db.New(stateFile)
		readState, _ := bb.Read()
		taskResult := readState.FindTask("task-1")
		if taskResult.Status != models.TaskStatusApproved {
			t.Errorf("Status = %v, want CODE_APPROVED", taskResult.Status)
		}
	})

	t.Run("quorum-2 both reviewers approve — second approval transitions to approved", func(t *testing.T) {
		now := time.Now().UTC()

		// Task already partially approved by reviewer 1, now in reviewing_2
		task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now)
		task.Status = models.TaskStatus("REVIEWING_CODE_2")
		task.Approvals = []models.Approval{
			{Agent: "code-reviewer-1", Provider: "claude", Timestamp: now.Add(-5 * time.Minute)},
		}
		reviewingBy := "code-reviewer-2"
		task.ReviewingBy = &reviewingBy
		// History with architecture impact
		task.History = append(task.History, models.TaskHistoryEntry{
			Time:  now.Add(-10 * time.Minute),
			Event: models.TaskEventPreExecutionCheckpoint,
			Extra: map[string]any{"impact": "architecture"},
		})

		tmpDir, stateFile := setupQuorumEnv(t, task, map[string]models.Agent{
			"code-reviewer-1": {Role: "code-reviewer", Status: models.AgentStatusIdle, Provider: "claude"},
			"code-reviewer-2": {Role: "code-reviewer", Status: models.AgentStatusWorking, Provider: "codex"},
		}, quorum2Pipeline)

		result, err := SubmitVerdict(tmpDir, "task-1", "APPROVED", "", "code-reviewer-2", "")
		if err != nil {
			t.Fatalf("SubmitVerdict() error: %v", err)
		}
		if result.Verdict != "APPROVED" {
			t.Errorf("Verdict = %q, want %q", result.Verdict, "APPROVED")
		}

		bb := db.New(stateFile)
		readState, _ := bb.Read()
		taskResult := readState.FindTask("task-1")
		if taskResult.Status != models.TaskStatusApproved {
			t.Errorf("Status = %v, want CODE_APPROVED", taskResult.Status)
		}
		if taskResult.ApprovalCount() != 2 {
			t.Errorf("ApprovalCount() = %d, want 2", taskResult.ApprovalCount())
		}
	})

	t.Run("impact upgrade triggers partial approval", func(t *testing.T) {
		now := time.Now().UTC()
		task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now)
		// Checkpoint with standard impact
		task.History = append(task.History, models.TaskHistoryEntry{
			Time:  now.Add(-5 * time.Minute),
			Event: models.TaskEventPreExecutionCheckpoint,
			Extra: map[string]any{"impact": "standard"},
		})

		tmpDir, stateFile := setupQuorumEnv(t, task, map[string]models.Agent{
			"code-reviewer-1": {Role: "code-reviewer", Status: models.AgentStatusWorking, Provider: "claude"},
		}, quorum2Pipeline)

		// Reviewer approves with architecture impact — upgrades quorum to 2
		result, err := SubmitVerdict(tmpDir, "task-1", "APPROVED", "", "code-reviewer-1", "architecture")
		if err != nil {
			t.Fatalf("SubmitVerdict() error: %v", err)
		}
		if result.Verdict != "APPROVED" {
			t.Errorf("Verdict = %q, want %q", result.Verdict, "APPROVED")
		}

		bb := db.New(stateFile)
		readState, _ := bb.Read()
		taskResult := readState.FindTask("task-1")
		if taskResult.Status != models.TaskStatus("CODE_PARTIALLY_APPROVED") {
			t.Errorf("Status = %v, want CODE_PARTIALLY_APPROVED", taskResult.Status)
		}
		if taskResult.ApprovalCount() != 1 {
			t.Errorf("ApprovalCount() = %d, want 1", taskResult.ApprovalCount())
		}

		// Verify impact stored in history extra
		found := false
		for i := len(taskResult.History) - 1; i >= 0; i-- {
			if taskResult.History[i].Event == models.TaskEventApproved {
				if v, ok := taskResult.History[i].Extra["impact"].(string); ok && v == "architecture" {
					found = true
				}
				break
			}
		}
		if !found {
			t.Error("Expected impact=architecture in approved history entry Extra")
		}
	})

	t.Run("rejection clears and restarts", func(t *testing.T) {
		now := time.Now().UTC()

		// Task in reviewing_2 with 1 prior approval
		task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now)
		task.Status = models.TaskStatus("REVIEWING_CODE_2")
		task.Approvals = []models.Approval{
			{Agent: "code-reviewer-1", Provider: "claude", Timestamp: now.Add(-5 * time.Minute)},
		}
		priorApprover := "code-reviewer-1"
		task.ApprovedBy = &priorApprover
		reviewingBy := "code-reviewer-2"
		task.ReviewingBy = &reviewingBy
		task.History = append(task.History, models.TaskHistoryEntry{
			Time:  now.Add(-10 * time.Minute),
			Event: models.TaskEventPreExecutionCheckpoint,
			Extra: map[string]any{"impact": "architecture"},
		})

		tmpDir, stateFile := setupQuorumEnv(t, task, map[string]models.Agent{
			"code-reviewer-2": {Role: "code-reviewer", Status: models.AgentStatusWorking, Provider: "codex"},
		}, quorum2Pipeline)

		result, err := SubmitVerdict(tmpDir, "task-1", "REJECTED", "Architectural concerns", "code-reviewer-2", "")
		if err != nil {
			t.Fatalf("SubmitVerdict() error: %v", err)
		}
		if result.Verdict != "REJECTED" {
			t.Errorf("Verdict = %q, want %q", result.Verdict, "REJECTED")
		}

		bb := db.New(stateFile)
		readState, _ := bb.Read()
		taskResult := readState.FindTask("task-1")
		if taskResult.Status != models.TaskStatusRejected {
			t.Errorf("Status = %v, want CODE_REJECTED", taskResult.Status)
		}
		if taskResult.Approvals != nil {
			t.Errorf("Approvals = %v, want nil after rejection", taskResult.Approvals)
		}
		if taskResult.ApprovedBy != nil {
			t.Errorf("ApprovedBy = %v, want nil after rejection", taskResult.ApprovedBy)
		}
	})

	t.Run("impact downgrade rejected", func(t *testing.T) {
		now := time.Now().UTC()
		task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now)
		// Checkpoint declares architecture impact
		task.History = append(task.History, models.TaskHistoryEntry{
			Time:  now.Add(-5 * time.Minute),
			Event: models.TaskEventPreExecutionCheckpoint,
			Extra: map[string]any{"impact": "architecture"},
		})

		tmpDir, _ := setupQuorumEnv(t, task, map[string]models.Agent{
			"code-reviewer-1": {Role: "code-reviewer", Status: models.AgentStatusWorking, Provider: "claude"},
		}, quorum2Pipeline)

		// Reviewer attempts to downgrade to standard — should be rejected
		_, err := SubmitVerdict(tmpDir, "task-1", "APPROVED", "", "code-reviewer-1", "standard")
		if err == nil {
			t.Fatal("Expected error for impact downgrade")
		}
		if !strings.Contains(err.Error(), "cannot downgrade") {
			t.Errorf("Error = %q, want to contain 'cannot downgrade'", err.Error())
		}
	})
}

func assertReleasedAgent(t *testing.T, state *models.State, agentID string) {
	t.Helper()

	agent := state.Agents[agentID]
	if agent.Status != models.AgentStatusIdle || agent.CurrentTask != nil {
		t.Errorf("%s should be released to IDLE, got status=%v current_task=%v", agentID, agent.Status, agent.CurrentTask)
	}
}

func TestSubmitVerdict_RejectedRefreshesLease(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	leaseDuration := 120
	expiredLease := now.Add(-10 * time.Minute)

	state := testhelpers.CreateValidState()
	state.Config.LeaseDuration = leaseDuration
	coderID := "coder-1"
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now)
	task.AssignedTo = &coderID
	task.LeaseExpires = &expiredLease
	state.Tasks = []models.Task{task}
	state.Agents["code-reviewer-1"] = models.Agent{
		Role:   "code-reviewer",
		Status: models.AgentStatusWorking,
	}
	state.Agents[coderID] = models.Agent{
		Role:   "coder",
		Status: models.AgentStatusWaiting,
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	callStart := time.Now().UTC()
	result, err := SubmitVerdict(tmpDir, "task-1", "REJECTED", "Needs work", "code-reviewer-1", "")
	if err != nil {
		t.Fatalf("SubmitVerdict() error: %v", err)
	}
	if result.EscalatedToBlocked {
		t.Fatal("Unexpected escalation — test expects non-escalating rejection")
	}

	bb := db.New(stateFile)
	readState, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}

	rejTask := readState.FindTask("task-1")
	if rejTask == nil {
		t.Fatal("Task not found")
	}

	// Lease should be refreshed on non-escalating rejection
	expectedMin := callStart.Add(time.Duration(leaseDuration) * time.Second)
	if rejTask.LeaseExpires == nil {
		t.Fatal("LeaseExpires is nil, want refreshed lease")
	}
	if rejTask.LeaseExpires.Before(expectedMin) {
		t.Errorf("LeaseExpires = %v, want >= %v", rejTask.LeaseExpires, expectedMin)
	}
}

func TestSubmitVerdict_EscalationClearsLease(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	coderID := "coder-1"
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now)
	task.AssignedTo = &coderID
	task.ReviewCyclesCurrent = 1
	task.ReviewCyclesTotal = 1
	task.Attempt = 2
	state := testhelpers.CreateValidState()
	state.Config.MaxReviewCycles = 2
	state.Tasks = []models.Task{task}

	taskRef := "task-1"
	state.Agents[coderID] = models.Agent{
		Role:        "coder",
		Status:      models.AgentStatusWaiting,
		CurrentTask: &taskRef,
	}
	state.Agents["code-reviewer-1"] = models.Agent{
		Role:   "code-reviewer",
		Status: models.AgentStatusReviewing,
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	result, err := SubmitVerdict(tmpDir, "task-1", "REJECTED", "Still broken", "code-reviewer-1", "")
	if err != nil {
		t.Fatalf("SubmitVerdict() error: %v", err)
	}
	if !result.EscalatedToBlocked {
		t.Fatal("Expected escalation to BLOCKED")
	}

	bb := db.New(stateFile)
	readState, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}

	blockedTask := readState.FindTask("task-1")
	if blockedTask == nil {
		t.Fatal("Task not found")
	}

	// Escalation should clear lease and assignment
	if blockedTask.LeaseExpires != nil {
		t.Errorf("LeaseExpires = %v, want nil after escalation", blockedTask.LeaseExpires)
	}
	if blockedTask.AssignedTo != nil {
		t.Errorf("AssignedTo = %v, want nil after escalation", blockedTask.AssignedTo)
	}
}

func TestSubmitVerdict_RejectionAtReviewCap_Attempt1_TriggersNewAttempt(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	coderID := "coder-1"
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now)
	task.AssignedTo = &coderID
	task.Attempt = 1
	task.Iteration = 3
	task.ReviewCyclesCurrent = 1
	task.ReviewCyclesTotal = 1

	state := testhelpers.CreateValidState()
	state.Config.MaxReviewCycles = 2
	state.Tasks = []models.Task{task}

	taskRef := "task-1"
	state.Agents[coderID] = models.Agent{
		Role:        "coder",
		Status:      models.AgentStatusWaiting,
		CurrentTask: &taskRef,
	}
	state.Agents["code-reviewer-1"] = models.Agent{
		Role:   "code-reviewer",
		Status: models.AgentStatusReviewing,
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	result, err := SubmitVerdict(tmpDir, "task-1", "REJECTED", "Approach is wrong", "code-reviewer-1", "")
	if err != nil {
		t.Fatalf("SubmitVerdict() error: %v", err)
	}
	if !result.NewAttemptTriggered {
		t.Error("NewAttemptTriggered = false, want true")
	}
	if result.EscalatedToBlocked {
		t.Error("EscalatedToBlocked = true, want false for new attempt")
	}

	// Verify task transitioned to initial status with attempt 2
	bb := db.New(stateFile)
	readState, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}

	transitioned := readState.FindTask("task-1")
	if transitioned == nil {
		t.Fatal("Task not found")
	}
	if transitioned.Attempt != 2 {
		t.Errorf("Attempt = %d, want 2", transitioned.Attempt)
	}
	if transitioned.Status != models.TaskStatusReady {
		t.Errorf("Status = %v, want %v (initial status)", transitioned.Status, models.TaskStatusReady)
	}
	if transitioned.Iteration != 0 {
		t.Errorf("Iteration = %d, want 0", transitioned.Iteration)
	}
	if transitioned.ReviewCyclesCurrent != 0 {
		t.Errorf("ReviewCyclesCurrent = %d, want 0", transitioned.ReviewCyclesCurrent)
	}
	if transitioned.AssignedTo != nil {
		t.Errorf("AssignedTo = %v, want nil", transitioned.AssignedTo)
	}
	if transitioned.RejectionReason != nil {
		t.Errorf("RejectionReason = %v, want nil after attempt transition", transitioned.RejectionReason)
	}

	// Coder agent should be released by TransitionToNewAttempt
	assertReleasedAgent(t, readState, coderID)
	// Reviewer agent released by SubmitVerdict
	assertReleasedAgent(t, readState, "code-reviewer-1")
}

func TestSubmitVerdict_RejectionAtReviewCap_Attempt1_TransitionFailure_PropagatesError(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	coderID := "coder-1"
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now)
	task.AssignedTo = &coderID
	task.Attempt = 1
	task.Iteration = 3
	task.ReviewCyclesCurrent = 1
	task.ReviewCyclesTotal = 1

	state := testhelpers.CreateValidState()
	state.Config.MaxReviewCycles = 2
	state.Tasks = []models.Task{task}

	taskRef := "task-1"
	state.Agents[coderID] = models.Agent{
		Role:        "coder",
		Status:      models.AgentStatusWaiting,
		CurrentTask: &taskRef,
	}
	state.Agents["code-reviewer-1"] = models.Agent{
		Role:   "code-reviewer",
		Status: models.AgentStatusReviewing,
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	// Use testTransitionHooks to replace the sentinel between Phase 1 and Phase 3,
	// causing Phase 3 to fail with a "sentinel replaced" error.
	bb := db.New(stateFile)
	testTransitionHooks = &transitionTestHooks{
		afterPhase1: func() {
			// Replace sentinel with a different value to simulate concurrent modification.
			_ = bb.Modify(func(s *models.State) error {
				t := s.FindTask("task-1")
				if t != nil {
					interloper := "coder-interloper"
					t.AssignedTo = &interloper
				}
				return nil
			})
		},
	}
	t.Cleanup(func() { testTransitionHooks = nil })

	result, err := SubmitVerdict(tmpDir, "task-1", "REJECTED", "Approach is wrong", "code-reviewer-1", "")

	// SubmitVerdict must return error (not a result) when TransitionToNewAttempt fails.
	if err == nil {
		t.Fatal("SubmitVerdict() returned nil error, want error propagated from TransitionToNewAttempt failure")
	}
	if result != nil {
		t.Errorf("SubmitVerdict() returned non-nil result %+v, want nil on transition failure", result)
	}

	// Error should contain both the SubmitVerdict context and the phase 3 failure.
	if !strings.Contains(err.Error(), "attempt transition failed") {
		t.Errorf("error %q should contain 'attempt transition failed'", err.Error())
	}
	if !strings.Contains(err.Error(), "sentinel replaced") {
		t.Errorf("error %q should contain 'sentinel replaced'", err.Error())
	}

	// TransitionToNewAttempt Phase 1 committed (Attempt=2, counters reset)
	// but Phase 3 failed — task is stuck with the interloper AssignedTo.
	readState, readErr := bb.Read()
	if readErr != nil {
		t.Fatalf("Failed to read state: %v", readErr)
	}
	failedTask := readState.FindTask("task-1")
	if failedTask == nil {
		t.Fatal("Task not found")
	}
	if failedTask.Attempt != 2 {
		t.Errorf("Attempt = %d, want 2 (Phase 1 committed)", failedTask.Attempt)
	}
	if failedTask.AssignedTo == nil || *failedTask.AssignedTo != "coder-interloper" {
		t.Errorf("AssignedTo = %v, want 'coder-interloper' (sentinel was replaced, Phase 3 aborted)", failedTask.AssignedTo)
	}
}

func TestSubmitVerdict_RejectionAtReviewCap_Attempt2_TriggersBlocked(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	coderID := "coder-1"
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReviewing, now)
	task.AssignedTo = &coderID
	task.Attempt = 2
	task.Iteration = 3
	task.ReviewCyclesCurrent = 1
	task.ReviewCyclesTotal = 6

	state := testhelpers.CreateValidState()
	state.Config.MaxReviewCycles = 2
	state.Tasks = []models.Task{task}

	taskRef := "task-1"
	state.Agents[coderID] = models.Agent{
		Role:        "coder",
		Status:      models.AgentStatusWaiting,
		CurrentTask: &taskRef,
	}
	state.Agents["code-reviewer-1"] = models.Agent{
		Role:   "code-reviewer",
		Status: models.AgentStatusReviewing,
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	result, err := SubmitVerdict(tmpDir, "task-1", "REJECTED", "Still wrong", "code-reviewer-1", "")
	if err != nil {
		t.Fatalf("SubmitVerdict() error: %v", err)
	}
	if !result.EscalatedToBlocked {
		t.Error("EscalatedToBlocked = false, want true")
	}
	if result.NewAttemptTriggered {
		t.Error("NewAttemptTriggered = true, want false for attempt 2")
	}

	bb := db.New(stateFile)
	readState, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}

	blockedTask := readState.FindTask("task-1")
	if blockedTask == nil {
		t.Fatal("Task not found")
	}
	if blockedTask.Status != models.TaskStatusBlocked {
		t.Errorf("Status = %v, want BLOCKED", blockedTask.Status)
	}
	if blockedTask.BlockedReason == nil {
		t.Fatal("BlockedReason is nil, want set")
	}

	assertReleasedAgent(t, readState, coderID)
	assertReleasedAgent(t, readState, "code-reviewer-1")
}
