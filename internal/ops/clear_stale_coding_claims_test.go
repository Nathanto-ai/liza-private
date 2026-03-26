package ops

import (
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

func TestClearStaleCodingClaims_NoStale(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)
	setupLogFile(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// IMPLEMENTING task with future lease — not stale
	futureLease := now.Add(30 * time.Minute)
	coder := "coder-1"
	baseCommit := "abc1234"
	worktree := ".worktrees/t1"
	state.Tasks = []models.Task{
		{
			ID: "t1", Description: "Active coding", Status: models.TaskStatusImplementing,
			Priority: 1, Created: now, SpecRef: "README.md", DoneWhen: "Done", Scope: "Test",
			AssignedTo: &coder, LeaseExpires: &futureLease,
			BaseCommit: &baseCommit, Worktree: &worktree,
			History: []models.TaskHistoryEntry{},
		},
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	cleared, err := ClearStaleCodingClaims(tmpDir)
	if err != nil {
		t.Fatalf("ClearStaleCodingClaims() error: %v", err)
	}
	if cleared != 0 {
		t.Errorf("cleared = %d, want 0", cleared)
	}
}

func TestClearStaleCodingClaims_ExpiredLease(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)
	setupLogFile(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// IMPLEMENTING task with expired lease
	expiredLease := now.Add(-5 * time.Minute)
	coder := "coder-1"
	baseCommit := "abc1234"
	worktree := ".worktrees/t1"
	state.Tasks = []models.Task{
		{
			ID: "t1", Description: "Stale coding", Status: models.TaskStatusImplementing,
			Priority: 1, Created: now, SpecRef: "README.md", DoneWhen: "Done", Scope: "Test",
			AssignedTo: &coder, LeaseExpires: &expiredLease,
			BaseCommit: &baseCommit, Worktree: &worktree,
			History: []models.TaskHistoryEntry{},
		},
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	cleared, err := ClearStaleCodingClaims(tmpDir)
	if err != nil {
		t.Fatalf("ClearStaleCodingClaims() error: %v", err)
	}
	if cleared != 1 {
		t.Errorf("cleared = %d, want 1", cleared)
	}

	// Verify state: should be READY, assignment cleared
	readState := readStateForTest(t, stateFile)
	task := readState.FindTask("t1")
	if task == nil {
		t.Fatal("Task not found")
	}
	if task.Status != models.TaskStatusReady {
		t.Errorf("Status = %v, want READY", task.Status)
	}
	if task.AssignedTo != nil {
		t.Errorf("AssignedTo should be nil, got %v", *task.AssignedTo)
	}
	if task.LeaseExpires != nil {
		t.Error("LeaseExpires should be nil")
	}
}

func TestClearStaleCodingClaims_MissingLease(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)
	setupLogFile(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// IMPLEMENTING task with coder but no lease (malformed state)
	coder := "coder-1"
	state.Tasks = []models.Task{
		{
			ID: "t1", Description: "Malformed coding", Status: models.TaskStatusImplementing,
			Priority: 1, Created: now, SpecRef: "README.md", DoneWhen: "Done", Scope: "Test",
			AssignedTo: &coder, // no LeaseExpires
			History:    []models.TaskHistoryEntry{},
		},
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	cleared, err := ClearStaleCodingClaims(tmpDir)
	if err != nil {
		t.Fatalf("ClearStaleCodingClaims() error: %v", err)
	}
	if cleared != 1 {
		t.Errorf("cleared = %d, want 1 (malformed lease treated as expired)", cleared)
	}
}

func TestClearStaleCodingClaims_SkipsNonImplementing(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)
	setupLogFile(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	// REVIEWING and READY tasks should be skipped entirely
	state.Tasks = []models.Task{
		testhelpers.BuildTaskByStatus("t1", models.TaskStatusReviewing, now),
		testhelpers.BuildTaskByStatus("t2", models.TaskStatusReady, now),
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	cleared, err := ClearStaleCodingClaims(tmpDir)
	if err != nil {
		t.Fatalf("ClearStaleCodingClaims() error: %v", err)
	}
	if cleared != 0 {
		t.Errorf("cleared = %d, want 0", cleared)
	}
}

func TestClearStaleCodingClaims_MultipleStale(t *testing.T) {
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)
	setupLogFile(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()

	expiredLease := now.Add(-10 * time.Minute)
	coder1 := "coder-1"
	coder2 := "coder-2"
	baseCommit := "abc1234"
	wt1 := ".worktrees/t1"
	wt2 := ".worktrees/t2"
	state.Tasks = []models.Task{
		{
			ID: "t1", Description: "Stale 1", Status: models.TaskStatusImplementing,
			Priority: 1, Created: now, SpecRef: "README.md", DoneWhen: "Done", Scope: "Test",
			AssignedTo: &coder1, LeaseExpires: &expiredLease,
			BaseCommit: &baseCommit, Worktree: &wt1,
			History: []models.TaskHistoryEntry{},
		},
		{
			ID: "t2", Description: "Stale 2", Status: models.TaskStatusImplementing,
			Priority: 1, Created: now, SpecRef: "README.md", DoneWhen: "Done", Scope: "Test",
			AssignedTo: &coder2, LeaseExpires: &expiredLease,
			BaseCommit: &baseCommit, Worktree: &wt2,
			History: []models.TaskHistoryEntry{},
		},
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	cleared, err := ClearStaleCodingClaims(tmpDir)
	if err != nil {
		t.Fatalf("ClearStaleCodingClaims() error: %v", err)
	}
	if cleared != 2 {
		t.Errorf("cleared = %d, want 2", cleared)
	}
}
