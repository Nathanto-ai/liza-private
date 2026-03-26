package main

import (
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/commands"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/ops"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// Regression tests for V&V Run 3 issues (Fixes 14–21).
// Tests requiring internal function access are in their respective packages:
//   internal/mcp/vnv3_regression_test.go  — Fix 14 (auditor exec), Fix 15 (reviewer tools)
//   internal/ops/classify_finding_test.go — Fix 16 (circuit breaker, depth computation)
//   internal/agent/vnv3_regression_test.go — Fix 18 (reviewer reclaim)

// TestFix15_InvalidSubpathReturnsError verifies that liza_get with invalid
// subpaths returns an error instead of silently returning the parent resource.
func TestFix15_InvalidSubpathReturnsError(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

	state := &models.State{
		Version: 1,
		Config:  models.Config{Mode: models.SystemModeRunning},
		Tasks: []models.Task{
			{ID: "test-task", Description: "Test", Status: models.TaskStatusReady, Created: time.Now()},
		},
	}
	testhelpers.WriteInitialState(t, statePath, state)

	invalidSubpaths := []string{
		"tasks/test-task/verify_head",
		"tasks/test-task/files",
		"tasks/test-task/diff",
		"tasks/test-task/commits",
		"tasks/test-task/notes",
		"tasks/test-task/history",
	}

	for _, query := range invalidSubpaths {
		t.Run(query, func(t *testing.T) {
			args := strings.Split(query, "/")
			opts := commands.InspectOptions{Format: "json", ProjectRoot: tmpDir}
			_, err := commands.InspectCommand(args, opts)
			if err == nil {
				t.Errorf("InspectCommand(%q) should return error for invalid subpath, got nil", query)
			}
			if err != nil && !strings.Contains(err.Error(), "subpaths are not supported") {
				t.Errorf("error should mention subpaths, got: %v", err)
			}
		})
	}
}

// TestFix15_ValidSubpathStillWorks verifies that valid liza_get queries still work.
func TestFix15_ValidSubpathStillWorks(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now()
	state := &models.State{
		Version: 1,
		Config:  models.Config{Mode: models.SystemModeRunning},
		Tasks: []models.Task{
			{ID: "test-task", Description: "Test", Status: models.TaskStatusReady, Created: now},
		},
		Agents: map[string]models.Agent{
			"coder-1": {Role: "coder", Status: models.AgentStatusIdle, Heartbeat: now},
		},
	}
	testhelpers.WriteInitialState(t, statePath, state)

	validQueries := [][]string{
		{"tasks"},
		{"tasks", "test-task"},
		{"agents"},
		{"agents", "coder-1"},
		{"config"},
	}

	for _, args := range validQueries {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			opts := commands.InspectOptions{Format: "json", ProjectRoot: tmpDir}
			_, err := commands.InspectCommand(args, opts)
			if err != nil {
				t.Errorf("InspectCommand(%v) returned unexpected error: %v", args, err)
			}
		})
	}
}

// TestFix16_RemediationCircuitBreaker verifies circuit breaker via exported API.
func TestFix16_RemediationCircuitBreaker(t *testing.T) {
	t.Parallel()

	t.Run("HIGH on merged remediation → LOG_ONLY", func(t *testing.T) {
		f := models.AuditFinding{Severity: "HIGH", Type: "MISSING_TEST"}
		got := ops.ClassifyFinding(f, models.TaskStatusMerged, 0, ops.WithRemediationTask(true))
		if got != "LOG_ONLY" {
			t.Errorf("got %q, want LOG_ONLY", got)
		}
	})

	t.Run("backward compatible no options", func(t *testing.T) {
		f := models.AuditFinding{Severity: "HIGH", Type: "MISSING_TEST"}
		got := ops.ClassifyFinding(f, models.TaskStatusMerged, 0)
		if got != "REMEDIATE_WITH_TASK" {
			t.Errorf("got %q, want REMEDIATE_WITH_TASK", got)
		}
	})
}

// TestFix19_FindingDeduplication verifies duplicate findings are downgraded.
func TestFix19_FindingDeduplication(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := &models.State{
		Version: 1,
		Config:  models.Config{Mode: models.SystemModeRunning},
		Tasks: []models.Task{
			{ID: "original-task", Status: models.TaskStatusMerged, Created: now, Description: "Orig"},
			{ID: "fix-f1", Status: models.TaskStatusMerged, OriginFindingID: "f1", Created: now, Description: "Fix"},
		},
		AuditFindings: []models.AuditFinding{
			{
				ID:           "f1",
				TaskID:       "original-task",
				Severity:     "HIGH",
				Type:         "MISSING_TEST",
				Phase:        "post_merge",
				Evidence:     "compilation error detected",
				LinkedTaskID: "fix-f1",
				Created:      now,
			},
		},
	}
	testhelpers.WriteInitialState(t, statePath, state)

	result, err := ops.SubmitAuditFinding(tmpDir, ops.AuditFindingInput{
		FindingID:         "f2",
		TaskID:            "fix-f1",
		Severity:          "HIGH",
		Type:              "MISSING_TEST",
		Phase:             "post_merge",
		Evidence:          "same compilation error detected again",
		RecommendedAction: "fix the compilation error",
		SpecReference:     "R1",
	})
	if err != nil {
		t.Fatalf("SubmitAuditFinding failed: %v", err)
	}

	if result.Classification != "LOG_ONLY" {
		t.Errorf("duplicate finding classification = %q, want LOG_ONLY", result.Classification)
	}
}
