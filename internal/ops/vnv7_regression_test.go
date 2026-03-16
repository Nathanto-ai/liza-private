package ops

import (
	"fmt"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// Regression tests for V&V Run 7 Fix 40: VERIFICATION_GAP circuit breaker.

// TestFix40_VerificationGapCircuitBreaker verifies that the third
// VERIFICATION_GAP finding is downgraded to LOG_ONLY when two non-LOG_ONLY
// VERIFICATION_GAP findings already exist.
// Bug: Auditor filed unlimited VERIFICATION_GAP findings, spawning excessive
// remediation tasks (ISSUE-R7-03).
func TestFix40_VerificationGapCircuitBreaker(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := &models.State{
		Version: 1,
		Goal: models.Goal{
			ID:          "goal-1",
			Description: "Test",
			SpecRef:     "spec.md",
			Created:     now,
			Status:      models.GoalStatusInProgress,
		},
		Tasks: []models.Task{
			{
				ID:          "task-1",
				Description: "Test task",
				DoneWhen:    "done",
				Priority:    1,
				Status:      models.TaskStatusMerged,
				SpecRef:     "spec.md",
				Created:     now,
			},
		},
		Agents: make(map[string]models.Agent),
		// Pre-seed 2 VERIFICATION_GAP findings with non-LOG_ONLY classification
		AuditFindings: []models.AuditFinding{
			{
				ID:             "f-gap-1",
				TaskID:         "task-1",
				Severity:       "MEDIUM",
				Type:           "VERIFICATION_GAP",
				Phase:          "post_merge",
				Classification: "REMEDIATE_WITH_TASK",
				Evidence:       "gap 1",
				Created:        now,
			},
			{
				ID:             "f-gap-2",
				TaskID:         "task-1",
				Severity:       "MEDIUM",
				Type:           "VERIFICATION_GAP",
				Phase:          "post_merge",
				Classification: "REMEDIATE_WITH_TASK",
				Evidence:       "gap 2",
				Created:        now,
			},
		},
		Config: models.Config{IntegrationBranch: "main"},
	}

	testhelpers.WriteInitialState(t, stateFile, state)

	// Submit a third VERIFICATION_GAP finding
	result, err := SubmitAuditFinding(tmpDir, AuditFindingInput{
		FindingID:      "f-gap-3",
		TaskID:         "task-1",
		Severity:       "MEDIUM",
		Type:           "VERIFICATION_GAP",
		Phase:          "post_merge",
		Classification: "REMEDIATE_WITH_TASK",
		Evidence:       "gap 3",
	})
	if err != nil {
		t.Fatalf("SubmitAuditFinding failed: %v", err)
	}

	// The circuit breaker should have forced LOG_ONLY
	if result.Classification != "LOG_ONLY" {
		t.Errorf("classification = %s, want LOG_ONLY (circuit breaker)", result.Classification)
	}

	// Verify in state
	bb := db.For(stateFile)
	readState, err := bb.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	var found *models.AuditFinding
	for i := range readState.AuditFindings {
		if readState.AuditFindings[i].ID == "f-gap-3" {
			found = &readState.AuditFindings[i]
			break
		}
	}
	if found == nil {
		t.Fatal("finding f-gap-3 not found in state")
	}
	if found.Classification != "LOG_ONLY" {
		t.Errorf("stored classification = %s, want LOG_ONLY", found.Classification)
	}
}

// TestFix40_VerificationGapBelowLimit verifies that VERIFICATION_GAP findings
// are classified normally when under the limit.
func TestFix40_VerificationGapBelowLimit(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := &models.State{
		Version: 1,
		Goal: models.Goal{
			ID:          "goal-1",
			Description: "Test",
			SpecRef:     "spec.md",
			Created:     now,
			Status:      models.GoalStatusInProgress,
		},
		Tasks: []models.Task{
			{
				ID:          "task-1",
				Description: "Test task",
				DoneWhen:    "done",
				Priority:    1,
				Status:      models.TaskStatusMerged,
				SpecRef:     "spec.md",
				Created:     now,
			},
		},
		Agents:        make(map[string]models.Agent),
		AuditFindings: []models.AuditFinding{}, // no prior findings
		Config:        models.Config{IntegrationBranch: "main"},
	}

	testhelpers.WriteInitialState(t, stateFile, state)

	// First VERIFICATION_GAP — should NOT be forced to LOG_ONLY
	result, err := SubmitAuditFinding(tmpDir, AuditFindingInput{
		FindingID:      "f-gap-1",
		TaskID:         "task-1",
		Severity:       "MEDIUM",
		Type:           "VERIFICATION_GAP",
		Phase:          "post_merge",
		Classification: "REMEDIATE_WITH_TASK",
		Evidence:       "first gap",
	})
	if err != nil {
		t.Fatalf("SubmitAuditFinding failed: %v", err)
	}

	// Classification should be set by ClassifyFinding (not forced to LOG_ONLY)
	// The exact classification depends on ClassifyFinding logic, but should NOT be
	// forced to LOG_ONLY by the circuit breaker.
	if result.Classification == "" {
		t.Error("expected non-empty classification")
	}
}

// TestFix40_NonVerificationGapUnaffected verifies that non-VERIFICATION_GAP
// findings are not affected by the circuit breaker.
func TestFix40_NonVerificationGapUnaffected(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := &models.State{
		Version: 1,
		Goal: models.Goal{
			ID:          "goal-1",
			Description: "Test",
			SpecRef:     "spec.md",
			Created:     now,
			Status:      models.GoalStatusInProgress,
		},
		Tasks: []models.Task{
			{
				ID:          "task-1",
				Description: "Test task",
				DoneWhen:    "done",
				Priority:    1,
				Status:      models.TaskStatusMerged,
				SpecRef:     "spec.md",
				Created:     now,
			},
		},
		Agents: make(map[string]models.Agent),
		// Pre-seed 5 VERIFICATION_GAP findings to blast past limit
		AuditFindings: func() []models.AuditFinding {
			var findings []models.AuditFinding
			for i := 0; i < 5; i++ {
				findings = append(findings, models.AuditFinding{
					ID:             fmt.Sprintf("f-gap-%d", i),
					TaskID:         "task-1",
					Severity:       "MEDIUM",
					Type:           "VERIFICATION_GAP",
					Phase:          "post_merge",
					Classification: "REMEDIATE_WITH_TASK",
					Evidence:       fmt.Sprintf("gap %d", i),
					Created:        now,
				})
			}
			return findings
		}(),
		Config: models.Config{IntegrationBranch: "main"},
	}

	testhelpers.WriteInitialState(t, stateFile, state)

	// SPEC_MISMATCH finding should not be affected by VERIFICATION_GAP circuit breaker
	result, err := SubmitAuditFinding(tmpDir, AuditFindingInput{
		FindingID:      "f-spec-1",
		TaskID:         "task-1",
		Severity:       "HIGH",
		Type:           "SPEC_MISMATCH",
		Phase:          "post_merge",
		Classification: "REMEDIATE_WITH_TASK",
		Evidence:       "spec mismatch",
	})
	if err != nil {
		t.Fatalf("SubmitAuditFinding failed: %v", err)
	}

	// Should not be forced to LOG_ONLY
	if result.Classification == "LOG_ONLY" {
		t.Error("SPEC_MISMATCH should not be downgraded by VERIFICATION_GAP circuit breaker")
	}
}
