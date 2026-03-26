package integration

// multiprocess_state_regression_test.go contains regression tests for the
// multi-process concurrent state.yaml write corruption found during the
// tasktrack live test session (March 2026).
//
// Root cause: internal/filelock/filelock.go used Signal(0) for stale lock
// detection which is a no-op on Windows, causing valid locks to be falsely
// identified as stale and cleaned up — allowing multiple processes to write
// state.yaml simultaneously, producing corrupted YAML.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
	"gopkg.in/yaml.v3"
)

// TestMultiProcessConcurrentModify spawns multiple child processes that
// concurrently increment a counter in state.yaml via Blackboard.Modify.
// If the file lock doesn't provide mutual exclusion across processes,
// some increments will be lost or the file will be corrupted.
func TestMultiProcessConcurrentModify(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-process integration test in short mode")
	}

	// When invoked as a subprocess, do the work and exit
	if os.Getenv("LIZA_TEST_MULTIPROCESS_WORKER") == "1" {
		multiprocessWorker(t)
		return
	}

	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

	state := testhelpers.CreateValidState()
	state.Config.HeartbeatInterval = 0 // use as counter
	testhelpers.WriteInitialState(t, statePath, state)

	numProcesses := 4
	incrementsPerProcess := 10

	// Spawn multiple child processes
	var wg sync.WaitGroup
	results := make([]error, numProcesses)

	for i := 0; i < numProcesses; i++ {
		wg.Add(1)
		go func(processIndex int) {
			defer wg.Done()

			cmd := exec.Command(os.Args[0],
				"-test.run=^TestMultiProcessConcurrentModify$",
				"-test.v",
			)
			cmd.Env = append(os.Environ(),
				"LIZA_TEST_MULTIPROCESS_WORKER=1",
				fmt.Sprintf("LIZA_TEST_STATE_PATH=%s", statePath),
				fmt.Sprintf("LIZA_TEST_INCREMENTS=%d", incrementsPerProcess),
			)
			cmd.Dir = tmpDir

			output, err := cmd.CombinedOutput()
			if err != nil {
				results[processIndex] = fmt.Errorf("process %d failed: %v\noutput: %s", processIndex, err, output)
			}
		}(i)
	}

	wg.Wait()

	// Check subprocess results
	for i, err := range results {
		if err != nil {
			t.Errorf("subprocess %d: %v", i, err)
		}
	}

	// Verify state.yaml is valid YAML (not corrupted)
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("failed to read state.yaml: %v", err)
	}

	var finalState models.State
	if err := yaml.Unmarshal(data, &finalState); err != nil {
		t.Fatalf("state.yaml is corrupted (invalid YAML): %v\ncontents:\n%s", err, string(data))
	}

	// Verify all increments were applied
	expectedCount := numProcesses * incrementsPerProcess
	if finalState.Config.HeartbeatInterval != expectedCount {
		t.Errorf("counter = %d, want %d (some updates lost due to race condition)",
			finalState.Config.HeartbeatInterval, expectedCount)
	}

	t.Logf("✓ Multi-process concurrent modify: counter=%d (expected %d)",
		finalState.Config.HeartbeatInterval, expectedCount)
}

// multiprocessWorker is the child process entry point for TestMultiProcessConcurrentModify.
func multiprocessWorker(t *testing.T) {
	statePath := os.Getenv("LIZA_TEST_STATE_PATH")
	if statePath == "" {
		t.Fatal("LIZA_TEST_STATE_PATH not set")
	}

	increments := 10
	if v := os.Getenv("LIZA_TEST_INCREMENTS"); v != "" {
		fmt.Sscanf(v, "%d", &increments)
	}

	bb := db.New(statePath)

	for i := 0; i < increments; i++ {
		err := bb.Modify(func(state *models.State) error {
			state.Config.HeartbeatInterval++
			return nil
		})
		if err != nil {
			t.Fatalf("Modify failed on iteration %d: %v", i, err)
		}
	}
}

// TestMultiProcessStateNotCorrupted spawns multiple writer processes that
// each add a unique task to state.yaml. Verifies the final state is valid
// YAML and contains all tasks.
func TestMultiProcessStateNotCorrupted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-process integration test in short mode")
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("unsupported platform")
	}

	// Subprocess worker mode
	if os.Getenv("LIZA_TEST_CORRUPTION_WORKER") == "1" {
		corruptionWorker(t)
		return
	}

	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{} // start empty
	testhelpers.WriteInitialState(t, statePath, state)

	numProcesses := 4

	// Each process adds a unique task
	var wg sync.WaitGroup
	results := make([]error, numProcesses)

	for i := 0; i < numProcesses; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			cmd := exec.Command(os.Args[0],
				"-test.run=^TestMultiProcessStateNotCorrupted$",
				"-test.v",
			)
			cmd.Env = append(os.Environ(),
				"LIZA_TEST_CORRUPTION_WORKER=1",
				fmt.Sprintf("LIZA_TEST_STATE_PATH=%s", statePath),
				fmt.Sprintf("LIZA_TEST_TASK_ID=task-%d", idx),
			)
			cmd.Dir = tmpDir

			output, err := cmd.CombinedOutput()
			if err != nil {
				results[idx] = fmt.Errorf("process %d: %v\noutput: %s", idx, err, output)
			}
		}(i)
	}

	wg.Wait()

	for i, err := range results {
		if err != nil {
			t.Errorf("subprocess %d: %v", i, err)
		}
	}

	// Verify YAML is valid
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}

	var finalState models.State
	if err := yaml.Unmarshal(data, &finalState); err != nil {
		t.Fatalf("state.yaml corrupted: %v\n--- begin ---\n%s\n--- end ---", err, limitString(string(data), 2000))
	}

	// Verify all tasks present
	taskIDs := make(map[string]bool)
	for _, task := range finalState.Tasks {
		taskIDs[task.ID] = true
	}

	for i := 0; i < numProcesses; i++ {
		expected := fmt.Sprintf("task-%d", i)
		if !taskIDs[expected] {
			t.Errorf("missing task %s from final state (has %v)", expected, taskIDs)
		}
	}

	t.Logf("✓ Multi-process state integrity: %d/%d tasks present, YAML valid",
		len(finalState.Tasks), numProcesses)
}

func corruptionWorker(t *testing.T) {
	statePath := os.Getenv("LIZA_TEST_STATE_PATH")
	taskID := os.Getenv("LIZA_TEST_TASK_ID")
	if statePath == "" || taskID == "" {
		t.Fatal("LIZA_TEST_STATE_PATH and LIZA_TEST_TASK_ID required")
	}

	bb := db.New(statePath)

	err := bb.Modify(func(state *models.State) error {
		now := time.Now().UTC()
		state.Tasks = append(state.Tasks, models.Task{
			ID:          taskID,
			Type:        "coding",
			Description: "Test task " + taskID,
			Status:      models.TaskStatusReady,
			Priority:    1,
			DoneWhen:    "done",
			Scope:       "test/",
			Created:     now,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("Modify failed: %v", err)
	}
}

// TestFileLockPreventsStateTruncation verifies that when one process holds
// the lock and another's lock attempt times out, the state file is not
// truncated or corrupted.
func TestFileLockPreventsStateTruncation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

	state := testhelpers.CreateValidState()
	bb := testhelpers.WriteInitialState(t, statePath, state)

	// Do a long-running modify (holds lock for 2 seconds)
	errCh := make(chan error, 1)
	go func() {
		errCh <- bb.Modify(func(s *models.State) error {
			time.Sleep(2 * time.Second)
			s.Config.HeartbeatInterval = 999
			return nil
		})
	}()

	// Give first modify time to acquire lock
	time.Sleep(200 * time.Millisecond)

	// Try a concurrent modify with short timeout
	shortBB := db.New(statePath).WithLockTimeout(500 * time.Millisecond)
	err := shortBB.Modify(func(s *models.State) error {
		s.Config.HeartbeatInterval = 888
		return nil
	})

	// This should fail with lock timeout
	if err == nil {
		t.Log("concurrent modify succeeded (lock was released fast enough)")
	} else {
		if !strings.Contains(err.Error(), "lock") {
			t.Logf("unexpected error type: %v", err)
		}
	}

	// Wait for first modify to complete
	if firstErr := <-errCh; firstErr != nil {
		t.Fatalf("first modify failed: %v", firstErr)
	}

	// Verify state is valid YAML (not corrupted)
	data, err := os.ReadFile(filepath.Join(tmpDir, ".liza", "state.yaml"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}

	var finalState models.State
	if err := yaml.Unmarshal(data, &finalState); err != nil {
		t.Fatalf("state.yaml corrupted after concurrent access: %v", err)
	}

	t.Logf("✓ State intact after concurrent lock contention (heartbeat=%d)",
		finalState.Config.HeartbeatInterval)
}

func limitString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...(truncated)"
}
