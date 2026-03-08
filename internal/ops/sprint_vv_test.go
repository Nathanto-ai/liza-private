package ops

import (
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

func TestValidateSprintVV(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	t.Run("MERGED task with no VerificationResult → missing verification issue", func(t *testing.T) {
		state := testhelpers.CreateValidState()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now),
		}
		// No VerificationResult on task, and no audit findings
		state.AuditFindings = nil

		issues := validateSprintVV(state)
		if len(issues) == 0 {
			t.Fatal("expected issues, got none")
		}
		found := false
		for _, issue := range issues {
			if strings.Contains(issue, "task-1") && strings.Contains(issue, "missing verification") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected issue about missing verification for task-1, got: %v", issues)
		}
	})

	t.Run("MERGED task with no post_merge finding → missing audit issue", func(t *testing.T) {
		state := testhelpers.CreateValidState()
		task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now)
		task.VerificationResult = &models.VerificationResult{
			Passed:    true,
			Output:    "ok",
			Timestamp: now,
		}
		state.Tasks = []models.Task{task}
		state.AuditFindings = nil // no findings

		issues := validateSprintVV(state)
		if len(issues) == 0 {
			t.Fatal("expected issues, got none")
		}
		found := false
		for _, issue := range issues {
			if strings.Contains(issue, "task-1") && strings.Contains(issue, "missing post-merge audit") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected issue about missing post-merge audit for task-1, got: %v", issues)
		}
	})

	t.Run("all MERGED tasks have both VerificationResult and post_merge finding → empty", func(t *testing.T) {
		state := testhelpers.CreateValidState()
		task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now)
		task.VerificationResult = &models.VerificationResult{
			Passed:    true,
			Output:    "ok",
			Timestamp: now,
		}
		state.Tasks = []models.Task{task}
		state.AuditFindings = []models.AuditFinding{
			{
				ID:             "f-1",
				TaskID:         "task-1",
				Severity:       "LOW",
				Type:           "QUALITY_ISSUE",
				Phase:          "post_merge",
				Classification: "LOG_ONLY",
				Evidence:       "all good",
				Created:        now,
			},
		}

		issues := validateSprintVV(state)
		if len(issues) != 0 {
			t.Errorf("expected no issues, got: %v", issues)
		}
	})

	t.Run("unresolved REPLAN_REQUIRED finding → returns issue", func(t *testing.T) {
		state := testhelpers.CreateValidState()
		task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now)
		task.VerificationResult = &models.VerificationResult{Passed: true, Output: "ok", Timestamp: now}
		state.Tasks = []models.Task{task}
		state.AuditFindings = []models.AuditFinding{
			{
				ID:             "f-1",
				TaskID:         "task-1",
				Severity:       "LOW",
				Type:           "QUALITY_ISSUE",
				Phase:          "post_merge",
				Classification: "LOG_ONLY",
				Evidence:       "all good",
				Created:        now,
			},
			{
				ID:             "f-replan",
				TaskID:         "task-1",
				Severity:       "HIGH",
				Type:           "SPEC_MISMATCH",
				Phase:          "post_merge",
				Classification: "REPLAN_REQUIRED",
				Evidence:       "architecture drift",
				Created:        now,
				Resolved:       false,
			},
		}

		issues := validateSprintVV(state)
		found := false
		for _, issue := range issues {
			if strings.Contains(issue, "REPLAN_REQUIRED") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected REPLAN_REQUIRED issue, got: %v", issues)
		}
	})

	t.Run("unresolved REMEDIATE_WITH_TASK with no linked task → returns issue", func(t *testing.T) {
		state := testhelpers.CreateValidState()
		task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now)
		task.VerificationResult = &models.VerificationResult{Passed: true, Output: "ok", Timestamp: now}
		state.Tasks = []models.Task{task}
		state.AuditFindings = []models.AuditFinding{
			{
				ID:             "f-1",
				TaskID:         "task-1",
				Severity:       "LOW",
				Type:           "QUALITY_ISSUE",
				Phase:          "post_merge",
				Classification: "LOG_ONLY",
				Evidence:       "all good",
				Created:        now,
			},
			{
				ID:             "f-remed",
				TaskID:         "task-1",
				Severity:       "MEDIUM",
				Type:           "MISSING_TEST",
				Phase:          "post_merge",
				Classification: "REMEDIATE_WITH_TASK",
				Evidence:       "missing edge case",
				Created:        now,
				Resolved:       false,
				LinkedTaskID:   "",
			},
		}

		issues := validateSprintVV(state)
		found := false
		for _, issue := range issues {
			if strings.Contains(issue, "REMEDIATE_WITH_TASK") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected REMEDIATE_WITH_TASK issue, got: %v", issues)
		}
	})

	t.Run("resolved findings → returns empty", func(t *testing.T) {
		state := testhelpers.CreateValidState()
		task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now)
		task.VerificationResult = &models.VerificationResult{Passed: true, Output: "ok", Timestamp: now}
		state.Tasks = []models.Task{task}
		state.AuditFindings = []models.AuditFinding{
			{
				ID:             "f-1",
				TaskID:         "task-1",
				Severity:       "LOW",
				Type:           "QUALITY_ISSUE",
				Phase:          "post_merge",
				Classification: "LOG_ONLY",
				Evidence:       "all good",
				Created:        now,
			},
			{
				ID:             "f-replan",
				TaskID:         "task-1",
				Severity:       "HIGH",
				Type:           "SPEC_MISMATCH",
				Phase:          "post_merge",
				Classification: "REPLAN_REQUIRED",
				Evidence:       "was replanned",
				Created:        now,
				Resolved:       true,
			},
			{
				ID:             "f-remed",
				TaskID:         "task-1",
				Severity:       "MEDIUM",
				Type:           "MISSING_TEST",
				Phase:          "post_merge",
				Classification: "REMEDIATE_WITH_TASK",
				Evidence:       "fixed",
				Created:        now,
				Resolved:       true,
			},
		}

		issues := validateSprintVV(state)
		if len(issues) != 0 {
			t.Errorf("expected no issues for resolved findings, got: %v", issues)
		}
	})
}

func TestJoinIssues(t *testing.T) {
	t.Parallel()

	issues := []string{"issue A", "issue B", "issue C"}
	got := joinIssues(issues)
	want := "issue A\n  - issue B\n  - issue C"
	if got != want {
		t.Errorf("joinIssues() = %q, want %q", got, want)
	}
}

func TestSprintCheckpoint_VVGuard_Blocks(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	state.Sprint.Status = models.SprintStatusInProgress
	state.Sprint.Timeline.Started = now.Add(-2 * time.Hour)
	state.Sprint.Timeline.Deadline = now.Add(6 * time.Hour)
	state.Config.RequireAuditForSprintClose = true

	// MERGED task without VerificationResult or audit finding → V&V should fail
	state.Tasks = []models.Task{
		testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now),
	}
	state.AuditFindings = nil

	testhelpers.WriteInitialState(t, stateFile, state)

	_, err := SprintCheckpoint(tmpDir)
	if err == nil {
		t.Fatal("expected error when V&V requirements not met")
	}
	if !strings.Contains(err.Error(), "V&V requirements not met") {
		t.Errorf("error = %q, want to contain 'V&V requirements not met'", err.Error())
	}
}

func TestSprintCheckpoint_VVGuard_Disabled(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	state.Sprint.Status = models.SprintStatusInProgress
	state.Sprint.Timeline.Started = now.Add(-2 * time.Hour)
	state.Sprint.Timeline.Deadline = now.Add(6 * time.Hour)
	state.Config.RequireAuditForSprintClose = false

	// MERGED task without VerificationResult — should still succeed because guard is disabled
	state.Tasks = []models.Task{
		testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now),
	}
	state.AuditFindings = nil

	testhelpers.WriteInitialState(t, stateFile, state)

	result, err := SprintCheckpoint(tmpDir)
	if err != nil {
		t.Fatalf("SprintCheckpoint() error: %v (V&V guard disabled should allow this)", err)
	}
	if result.CheckpointAt.IsZero() {
		t.Error("CheckpointAt should not be zero")
	}
}
