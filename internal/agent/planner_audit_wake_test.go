package agent

import (
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

func TestCountUnresolvedReplanFindings(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	t.Run("unresolved REPLAN_REQUIRED returns 1", func(t *testing.T) {
		state := testhelpers.CreateValidState()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now),
		}
		state.AuditFindings = []models.AuditFinding{
			{
				ID:             "f-1",
				TaskID:         "task-1",
				Severity:       "HIGH",
				Type:           "SPEC_MISMATCH",
				Phase:          "post_merge",
				Classification: "REPLAN_REQUIRED",
				Evidence:       "needs replan",
				Created:        now,
				Resolved:       false,
			},
		}

		got := countUnresolvedReplanFindings(state)
		if got != 1 {
			t.Errorf("countUnresolvedReplanFindings() = %d, want 1", got)
		}
	})

	t.Run("resolved REPLAN_REQUIRED returns 0", func(t *testing.T) {
		state := testhelpers.CreateValidState()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now),
		}
		state.AuditFindings = []models.AuditFinding{
			{
				ID:             "f-1",
				TaskID:         "task-1",
				Severity:       "HIGH",
				Type:           "SPEC_MISMATCH",
				Phase:          "post_merge",
				Classification: "REPLAN_REQUIRED",
				Evidence:       "was replanned",
				Created:        now,
				Resolved:       true,
			},
		}

		got := countUnresolvedReplanFindings(state)
		if got != 0 {
			t.Errorf("countUnresolvedReplanFindings() = %d, want 0", got)
		}
	})
}

func TestCountUnresolvedRemediationFindings(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	t.Run("unresolved REMEDIATE_WITH_TASK no LinkedTaskID returns 1", func(t *testing.T) {
		state := testhelpers.CreateValidState()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now),
		}
		state.AuditFindings = []models.AuditFinding{
			{
				ID:             "f-1",
				TaskID:         "task-1",
				Severity:       "MEDIUM",
				Type:           "MISSING_TEST",
				Phase:          "post_merge",
				Classification: "REMEDIATE_WITH_TASK",
				Evidence:       "missing edge-case test",
				Created:        now,
				Resolved:       false,
				LinkedTaskID:   "",
			},
		}

		got := countUnresolvedRemediationFindings(state)
		if got != 1 {
			t.Errorf("countUnresolvedRemediationFindings() = %d, want 1", got)
		}
	})

	t.Run("REMEDIATE_WITH_TASK with LinkedTaskID returns 0", func(t *testing.T) {
		state := testhelpers.CreateValidState()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now),
		}
		state.AuditFindings = []models.AuditFinding{
			{
				ID:             "f-1",
				TaskID:         "task-1",
				Severity:       "MEDIUM",
				Type:           "MISSING_TEST",
				Phase:          "post_merge",
				Classification: "REMEDIATE_WITH_TASK",
				Evidence:       "missing edge-case test",
				Created:        now,
				Resolved:       false,
				LinkedTaskID:   "task-fix-1",
			},
		}

		got := countUnresolvedRemediationFindings(state)
		if got != 0 {
			t.Errorf("countUnresolvedRemediationFindings() = %d, want 0", got)
		}
	})
}

func TestDetectPlannerWakeTriggers_AuditFindings(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	t.Run("REPLAN_REQUIRED finding triggers WakeTriggerReplanRequired", func(t *testing.T) {
		state := testhelpers.CreateValidState()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now),
		}
		state.Sprint.Scope.Planned = []string{"task-1"}
		state.AuditFindings = []models.AuditFinding{
			{
				ID:             "f-1",
				TaskID:         "task-1",
				Severity:       "HIGH",
				Type:           "SPEC_MISMATCH",
				Phase:          "post_merge",
				Classification: "REPLAN_REQUIRED",
				Evidence:       "architecture mismatch",
				Created:        now,
				Resolved:       false,
			},
		}

		result := DetectPlannerWakeTriggers(state)
		// Audit triggers now fire BEFORE SprintComplete so that remediation
		// work is addressed before the sprint is checkpointed.
		// Priority: InitialPlanning > Blocked > IntegrationFailed >
		//   HypothesisExhausted > ImmediateDiscovery > ReplanRequired >
		//   RemediationNeeded > SprintComplete
		if result.Trigger != WakeTriggerReplanRequired {
			t.Errorf("Trigger = %q, want %q (ReplanRequired fires before SprintComplete)",
				result.Trigger, WakeTriggerReplanRequired)
		}
	})

	t.Run("REPLAN_REQUIRED finding with non-terminal tasks triggers ReplanRequired", func(t *testing.T) {
		state := testhelpers.CreateValidState()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now),
			testhelpers.BuildTaskByStatus("task-2", models.TaskStatusImplementing, now),
		}
		state.Sprint.Scope.Planned = []string{"task-1", "task-2"}
		state.AuditFindings = []models.AuditFinding{
			{
				ID:             "f-1",
				TaskID:         "task-1",
				Severity:       "HIGH",
				Type:           "SPEC_MISMATCH",
				Phase:          "post_merge",
				Classification: "REPLAN_REQUIRED",
				Evidence:       "architecture mismatch",
				Created:        now,
				Resolved:       false,
			},
		}

		result := DetectPlannerWakeTriggers(state)
		if result.Trigger != WakeTriggerReplanRequired {
			t.Errorf("Trigger = %q, want %q", result.Trigger, WakeTriggerReplanRequired)
		}
		if result.Count != 1 {
			t.Errorf("Count = %d, want 1", result.Count)
		}
	})

	t.Run("REMEDIATE_WITH_TASK finding triggers WakeTriggerRemediationNeeded", func(t *testing.T) {
		state := testhelpers.CreateValidState()
		state.Tasks = []models.Task{
			testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now),
			testhelpers.BuildTaskByStatus("task-2", models.TaskStatusImplementing, now),
		}
		state.Sprint.Scope.Planned = []string{"task-1", "task-2"}
		state.AuditFindings = []models.AuditFinding{
			{
				ID:             "f-1",
				TaskID:         "task-1",
				Severity:       "MEDIUM",
				Type:           "MISSING_TEST",
				Phase:          "post_merge",
				Classification: "REMEDIATE_WITH_TASK",
				Evidence:       "missing edge-case test",
				Created:        now,
				Resolved:       false,
				LinkedTaskID:   "",
			},
		}

		result := DetectPlannerWakeTriggers(state)
		if result.Trigger != WakeTriggerRemediationNeeded {
			t.Errorf("Trigger = %q, want %q", result.Trigger, WakeTriggerRemediationNeeded)
		}
		if result.Count != 1 {
			t.Errorf("Count = %d, want 1", result.Count)
		}
	})
}

// TestAuditRemediationPriorityOverSprintComplete is a regression test for the
// audit→planner pipeline fix. REMEDIATE_WITH_TASK findings must fire before
// SPRINT_COMPLETE, otherwise the planner checkpoints the sprint without
// creating remediation tasks.
func TestAuditRemediationPriorityOverSprintComplete(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()

	// Setup: all planned tasks are MERGED (sprint would be complete)
	// AND there's an unresolved REMEDIATE_WITH_TASK finding
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{
		testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now),
		testhelpers.BuildTaskByStatus("task-2", models.TaskStatusMerged, now),
	}
	state.Sprint.Scope.Planned = []string{"task-1", "task-2"}
	state.AuditFindings = []models.AuditFinding{
		{
			ID:             "f-remediation",
			TaskID:         "task-2",
			Severity:       "MEDIUM",
			Type:           "QUALITY_ISSUE",
			Phase:          "post_merge",
			Classification: "REMEDIATE_WITH_TASK",
			Evidence:       "uses err == instead of errors.Is()",
			Created:        now,
			Resolved:       false,
			LinkedTaskID:   "",
		},
	}

	result := DetectPlannerWakeTriggers(state)

	// REMEDIATE_WITH_TASK must fire BEFORE SPRINT_COMPLETE
	if result.Trigger != WakeTriggerRemediationNeeded {
		t.Errorf("Trigger = %q, want %q — audit remediation must take priority over sprint complete",
			result.Trigger, WakeTriggerRemediationNeeded)
	}
	if result.Count != 1 {
		t.Errorf("Count = %d, want 1", result.Count)
	}
}

// TestAuditRemediationResolvedFallsThruToSprintComplete verifies that once
// a REMEDIATE_WITH_TASK finding is resolved (or has a linked task), the
// detector falls through to SPRINT_COMPLETE as normal.
func TestAuditRemediationResolvedFallsThruToSprintComplete(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()

	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{
		testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now),
	}
	state.Sprint.Scope.Planned = []string{"task-1"}
	state.AuditFindings = []models.AuditFinding{
		{
			ID:             "f-resolved",
			TaskID:         "task-1",
			Severity:       "LOW",
			Type:           "QUALITY_ISSUE",
			Phase:          "post_merge",
			Classification: "REMEDIATE_WITH_TASK",
			Evidence:       "fixed issue",
			Created:        now,
			Resolved:       true, // resolved — should not trigger
		},
	}

	result := DetectPlannerWakeTriggers(state)
	if result.Trigger != WakeTriggerSprintComplete {
		t.Errorf("Trigger = %q, want %q — resolved findings should fall through to sprint complete",
			result.Trigger, WakeTriggerSprintComplete)
	}
}
