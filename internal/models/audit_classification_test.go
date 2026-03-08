package models

import (
	"testing"
)

func TestIsValidPhase(t *testing.T) {
	t.Parallel()

	validPhases := []string{"pre_execution", "post_execution", "post_merge"}
	for _, phase := range validPhases {
		t.Run("valid_"+phase, func(t *testing.T) {
			f := &AuditFinding{Phase: phase}
			if !f.IsValidPhase() {
				t.Errorf("IsValidPhase() = false for %q, want true", phase)
			}
		})
	}

	t.Run("empty string is valid (backward compat)", func(t *testing.T) {
		f := &AuditFinding{Phase: ""}
		if !f.IsValidPhase() {
			t.Error("IsValidPhase() = false for empty string, want true")
		}
	})

	t.Run("invalid_phase returns false", func(t *testing.T) {
		f := &AuditFinding{Phase: "invalid_phase"}
		if f.IsValidPhase() {
			t.Error("IsValidPhase() = true for invalid_phase, want false")
		}
	})
}

func TestIsValidClassification(t *testing.T) {
	t.Parallel()

	validClassifications := []string{"LOG_ONLY", "REMEDIATE_WITH_TASK", "REOPEN_TASK", "REPLAN_REQUIRED"}
	for _, c := range validClassifications {
		t.Run("valid_"+c, func(t *testing.T) {
			f := &AuditFinding{Classification: c}
			if !f.IsValidClassification() {
				t.Errorf("IsValidClassification() = false for %q, want true", c)
			}
		})
	}

	t.Run("empty string is valid (backward compat)", func(t *testing.T) {
		f := &AuditFinding{Classification: ""}
		if !f.IsValidClassification() {
			t.Error("IsValidClassification() = false for empty string, want true")
		}
	})

	t.Run("INVALID returns false", func(t *testing.T) {
		f := &AuditFinding{Classification: "INVALID"}
		if f.IsValidClassification() {
			t.Error("IsValidClassification() = true for INVALID, want false")
		}
	})
}

func TestMergedToReadyTransitionValid(t *testing.T) {
	t.Parallel()

	if !TaskStatusMerged.CanTransition(TaskStatusReady) {
		t.Error("MERGED→READY transition should be valid")
	}
}
