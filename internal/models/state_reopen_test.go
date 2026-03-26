package models

import (
	"testing"
)

func TestMergedToReadyTransition(t *testing.T) {
	t.Parallel()

	t.Run("MERGED→READY is valid", func(t *testing.T) {
		if !TaskStatusMerged.CanTransition(TaskStatusReady) {
			t.Error("CanTransition(MERGED, READY) = false, want true")
		}
	})

	t.Run("MERGED→IMPLEMENTING is not valid", func(t *testing.T) {
		if TaskStatusMerged.CanTransition(TaskStatusImplementing) {
			t.Error("CanTransition(MERGED, IMPLEMENTING) = true, want false")
		}
	})

	t.Run("MERGED is not terminal (can transition to READY for audit reopen)", func(t *testing.T) {
		if TaskStatusMerged.IsTerminal() {
			t.Error("IsTerminal(MERGED) = true, want false (MERGED can be reopened by audit)")
		}
	})

	t.Run("Task.Transition from MERGED to READY succeeds", func(t *testing.T) {
		task := Task{ID: "task-1", Status: TaskStatusMerged}
		err := task.Transition(TaskStatusReady)
		if err != nil {
			t.Fatalf("Transition(MERGED→READY) error: %v", err)
		}
		if task.Status != TaskStatusReady {
			t.Errorf("Status = %s, want READY", task.Status)
		}
	})

	t.Run("Task.Transition from MERGED to IMPLEMENTING fails", func(t *testing.T) {
		task := Task{ID: "task-1", Status: TaskStatusMerged}
		err := task.Transition(TaskStatusImplementing)
		if err == nil {
			t.Fatal("expected error for Transition(MERGED→IMPLEMENTING)")
		}
		if task.Status != TaskStatusMerged {
			t.Errorf("Status changed to %s on failed transition, should stay MERGED", task.Status)
		}
	})
}
