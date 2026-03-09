package mcp

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// TestConcurrentClaimSameTask_MCPvsMCP verifies only one MCP handler succeeds when claiming same task
func TestConcurrentClaimSameTask_MCPvsMCP(t *testing.T) {
	projectRoot, cleanup := setupTestWorkspaceWithGit(t)
	defer cleanup()

	server := NewServer(projectRoot, filepath.Join(projectRoot, ".liza", "log.yaml"), "")

	var wg sync.WaitGroup
	wg.Add(2)

	var err1, err2 error
	var result1, result2 any

	// Two goroutines try to claim the same task simultaneously
	go func() {
		defer wg.Done()
		result1, err1 = server.handleClaimTask(map[string]any{
			"task_id":  "task-1",
			"agent_id": "coder-1",
		})
	}()

	go func() {
		defer wg.Done()
		result2, err2 = server.handleClaimTask(map[string]any{
			"task_id":  "task-1",
			"agent_id": "coder-2",
		})
	}()

	wg.Wait()

	// Exactly one should succeed
	successCount := 0
	if err1 == nil {
		successCount++
		t.Logf("Goroutine 1 succeeded: %v", result1)
	} else {
		t.Logf("Goroutine 1 failed: %v", err1)
	}

	if err2 == nil {
		successCount++
		t.Logf("Goroutine 2 succeeded: %v", result2)
	} else {
		t.Logf("Goroutine 2 failed: %v", err2)
	}

	if successCount != 1 {
		t.Errorf("Expected exactly 1 success, got %d (both should not succeed due to atomicity)", successCount)
	}

	// Verify state: task should be claimed by exactly one agent
	statePath := filepath.Join(projectRoot, ".liza", "state.yaml")
	bb := db.New(statePath)
	state, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}

	task := state.Tasks[0]
	if task.Status != models.TaskStatusImplementing {
		t.Errorf("Expected task status IMPLEMENTING, got %s", task.Status)
	}

	if task.AssignedTo == nil {
		t.Error("Expected task to be assigned to an agent")
	} else if *task.AssignedTo != "coder-1" && *task.AssignedTo != "coder-2" {
		t.Errorf("Expected task assigned to coder-1 or coder-2, got %s", *task.AssignedTo)
	}
}

// TestConcurrentAddTask verifies concurrent task additions with different IDs
func TestConcurrentAddTask(t *testing.T) {
	projectRoot, cleanup := setupTestWorkspaceWithGit(t)
	defer cleanup()

	server := NewServer(projectRoot, filepath.Join(projectRoot, ".liza", "log.yaml"), "")

	var wg sync.WaitGroup
	numTasks := 5
	wg.Add(numTasks)

	errors := make([]error, numTasks)

	// Multiple goroutines add different tasks simultaneously
	for i := 0; i < numTasks; i++ {
		i := i // capture loop variable
		go func() {
			defer wg.Done()
			_, errors[i] = server.handleAddTask(map[string]any{
				"id":       fmt.Sprintf("task-concurrent-%d", i),
				"desc":     fmt.Sprintf("Concurrent task %d", i),
				"spec":     "specs/test-spec.md",
				"done":     "Task is complete",
				"scope":    "Test concurrent adds",
				"priority": 1,
				"agent_id": "planner-1",
			})
		}()
	}

	wg.Wait()

	// All should succeed (different task IDs)
	failCount := 0
	for i, err := range errors {
		if err != nil {
			failCount++
			t.Logf("Task %d failed: %v", i, err)
		}
	}

	if failCount > 0 {
		t.Errorf("Expected all tasks to be added successfully, but %d failed", failCount)
	}

	// Verify all tasks were added
	statePath := filepath.Join(projectRoot, ".liza", "state.yaml")
	bb := db.New(statePath)
	state, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}

	// Should have original task-1 plus 5 new tasks
	if len(state.Tasks) != 6 {
		t.Errorf("Expected 6 tasks (1 original + 5 new), got %d", len(state.Tasks))
	}
}

// TestConcurrentAddTaskSameID verifies only one succeeds when adding same task ID
func TestConcurrentAddTaskSameID(t *testing.T) {
	projectRoot, cleanup := setupTestWorkspaceWithGit(t)
	defer cleanup()

	server := NewServer(projectRoot, filepath.Join(projectRoot, ".liza", "log.yaml"), "")

	var wg sync.WaitGroup
	wg.Add(2)

	var err1, err2 error

	// Two goroutines try to add the same task ID simultaneously
	go func() {
		defer wg.Done()
		_, err1 = server.handleAddTask(map[string]any{
			"id":       "task-duplicate",
			"desc":     "Duplicate task from goroutine 1",
			"spec":     "specs/test-spec.md",
			"done":     "Task is complete",
			"scope":    "Test duplicate",
			"priority": 1,
			"agent_id": "planner-1",
		})
	}()

	go func() {
		defer wg.Done()
		_, err2 = server.handleAddTask(map[string]any{
			"id":       "task-duplicate",
			"desc":     "Duplicate task from goroutine 2",
			"spec":     "specs/test-spec.md",
			"done":     "Task is complete",
			"scope":    "Test duplicate",
			"priority": 1,
			"agent_id": "planner-1",
		})
	}()

	wg.Wait()

	// Exactly one should succeed
	successCount := 0
	if err1 == nil {
		successCount++
		t.Log("Goroutine 1 succeeded")
	} else {
		t.Logf("Goroutine 1 failed: %v", err1)
	}

	if err2 == nil {
		successCount++
		t.Log("Goroutine 2 succeeded")
	} else {
		t.Logf("Goroutine 2 failed: %v", err2)
	}

	if successCount != 1 {
		t.Errorf("Expected exactly 1 success for duplicate task ID, got %d", successCount)
	}

	// Verify only one task exists with that ID
	statePath := filepath.Join(projectRoot, ".liza", "state.yaml")
	bb := db.New(statePath)
	state, err := bb.Read()
	if err != nil {
		t.Fatalf("Failed to read state: %v", err)
	}

	count := 0
	for _, task := range state.Tasks {
		if task.ID == "task-duplicate" {
			count++
		}
	}

	if count != 1 {
		t.Errorf("Expected exactly 1 task with ID 'task-duplicate', got %d", count)
	}
}

// TestLockAcquisitionTime verifies lock acquisition is reasonably fast
func TestLockAcquisitionTime(t *testing.T) {
	projectRoot, cleanup := setupTestWorkspaceWithGit(t)
	defer cleanup()

	server := NewServer(projectRoot, filepath.Join(projectRoot, ".liza", "log.yaml"), "")

	// Measure time for a simple read operation
	start := time.Now()
	_, err := server.handleGet(map[string]any{
		"query": "tasks",
	})
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("handleGet failed: %v", err)
	}

	// Lock acquisition should be fast (< 1 second for uncontended case)
	if duration > 1*time.Second {
		t.Errorf("Lock acquisition took %v, expected < 1s", duration)
	}

	t.Logf("Lock acquisition time: %v", duration)
}

// TestNoDeadlocks verifies no deadlocks under concurrent load
func TestNoDeadlocks(t *testing.T) {
	projectRoot, cleanup := setupTestWorkspaceWithGit(t)
	defer cleanup()

	server := NewServer(projectRoot, filepath.Join(projectRoot, ".liza", "log.yaml"), "")

	// Add more tasks for this test
	statePath := filepath.Join(projectRoot, ".liza", "state.yaml")
	bb := db.New(statePath)
	err := bb.Modify(func(state *models.State) error {
		for i := 2; i <= 5; i++ {
			task := testhelpers.BuildTaskByStatus(fmt.Sprintf("task-%d", i), models.TaskStatusReady, time.Now().UTC())
			state.Tasks = append(state.Tasks, task)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to add tasks: %v", err)
	}

	var wg sync.WaitGroup
	numOperations := 20
	wg.Add(numOperations)

	// Mix of reads and writes
	for i := 0; i < numOperations; i++ {
		i := i // capture
		go func() {
			defer wg.Done()

			if i%2 == 0 {
				// Read operation
				_, _ = server.handleGet(map[string]any{
					"query": "tasks",
				})
			} else {
				// Write operation (claim a task)
				taskNum := (i % 4) + 2 // task-2 through task-5
				_, _ = server.handleClaimTask(map[string]any{
					"task_id":  fmt.Sprintf("task-%d", taskNum),
					"agent_id": fmt.Sprintf("coder-%d", i),
				})
			}
		}()
	}

	// Wait with timeout to detect deadlocks
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("All operations completed without deadlock")
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout: possible deadlock detected")
	}
}
