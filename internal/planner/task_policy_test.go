package planner

import (
	"fmt"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
)

func TestEvaluateFinding(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	tests := []struct {
		name    string
		finding models.AuditFinding
		wantNil bool
	}{
		{
			name: "finding with all criteria creates proposal",
			finding: models.AuditFinding{
				ID:                "finding-1",
				TaskID:            "task-1",
				Severity:          "HIGH",
				Type:              "SPEC_MISMATCH",
				SpecReference:     "AC-3",
				Evidence:          "Expected X got Y",
				RecommendedAction: "Update handler to return correct format",
				Created:           now,
			},
			wantNil: false,
		},
		{
			name: "finding without spec_ref returns nil",
			finding: models.AuditFinding{
				ID:                "finding-2",
				TaskID:            "task-1",
				Severity:          "HIGH",
				Type:              "QUALITY_ISSUE",
				Evidence:          "code smell",
				RecommendedAction: "refactor",
				Created:           now,
			},
			wantNil: true,
		},
		{
			name: "finding without evidence returns nil",
			finding: models.AuditFinding{
				ID:                "finding-3",
				TaskID:            "task-1",
				Severity:          "MEDIUM",
				Type:              "MISSING_TEST",
				SpecReference:     "AC-1",
				RecommendedAction: "add test",
				Created:           now,
			},
			wantNil: true,
		},
		{
			name: "finding without recommended action returns nil",
			finding: models.AuditFinding{
				ID:            "finding-4",
				TaskID:        "task-1",
				Severity:      "LOW",
				Type:          "MISSING_EDGE_CASE",
				SpecReference: "AC-2",
				Evidence:      "edge case not handled",
				Created:       now,
			},
			wantNil: true,
		},
		{
			name: "resolved finding returns nil",
			finding: models.AuditFinding{
				ID:                "finding-5",
				TaskID:            "task-1",
				Severity:          "HIGH",
				Type:              "SPEC_MISMATCH",
				SpecReference:     "AC-1",
				Evidence:          "was broken",
				RecommendedAction: "fix it",
				Created:           now,
				Resolved:          true,
			},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			proposal := EvaluateFinding(tt.finding)

			if tt.wantNil {
				if proposal != nil {
					t.Fatalf("expected nil, got %+v", proposal)
				}
				return
			}

			if proposal == nil {
				t.Fatal("expected non-nil proposal")
			}

			if proposal.FindingID != tt.finding.ID {
				t.Errorf("FindingID = %q, want %q", proposal.FindingID, tt.finding.ID)
			}
			if proposal.OriginTaskID != tt.finding.TaskID {
				t.Errorf("OriginTaskID = %q, want %q", proposal.OriginTaskID, tt.finding.TaskID)
			}
		})
	}
}

func TestDeduplicateTasks(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	tests := []struct {
		name           string
		proposals      []TaskProposal
		existingTasks  []models.Task
		wantCount      int
		wantFindingIDs []string
	}{
		{
			name:      "no proposals returns empty",
			proposals: []TaskProposal{},
			wantCount: 0,
		},
		{
			name: "unique proposals all kept",
			proposals: []TaskProposal{
				{FindingID: "f-1", TaskID: "fix-f-1", Description: "Fix A"},
				{FindingID: "f-2", TaskID: "fix-f-2", Description: "Fix B"},
			},
			wantCount:      2,
			wantFindingIDs: []string{"f-1", "f-2"},
		},
		{
			name: "existing task with same finding ID removed",
			proposals: []TaskProposal{
				{FindingID: "f-1", TaskID: "fix-f-1", Description: "Fix A"},
				{FindingID: "f-2", TaskID: "fix-f-2", Description: "Fix B"},
			},
			existingTasks: []models.Task{
				{
					ID:              "existing-1",
					Status:          models.TaskStatusReady,
					OriginFindingID: "f-1",
					Description:     "different desc",
					Created:         now,
				},
			},
			wantCount:      1,
			wantFindingIDs: []string{"f-2"},
		},
		{
			name: "terminal task with same finding ID does not block",
			proposals: []TaskProposal{
				{FindingID: "f-1", TaskID: "fix-f-1", Description: "Fix A"},
			},
			existingTasks: []models.Task{
				{
					ID:              "existing-1",
					Status:          models.TaskStatusAbandoned,
					OriginFindingID: "f-1",
					Description:     "old fix",
					Created:         now,
				},
			},
			wantCount:      1,
			wantFindingIDs: []string{"f-1"},
		},
		{
			name: "duplicate finding IDs within batch collapsed",
			proposals: []TaskProposal{
				{FindingID: "f-1", TaskID: "fix-f-1", Description: "Fix A"},
				{FindingID: "f-1", TaskID: "fix-f-1-dup", Description: "Fix A dup"},
			},
			wantCount:      1,
			wantFindingIDs: []string{"f-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := DeduplicateTasks(tt.proposals, tt.existingTasks)

			if len(result) != tt.wantCount {
				t.Fatalf("result count = %d, want %d", len(result), tt.wantCount)
			}

			for i, wantID := range tt.wantFindingIDs {
				if i >= len(result) {
					break
				}
				if result[i].FindingID != wantID {
					t.Errorf("result[%d].FindingID = %q, want %q", i, result[i].FindingID, wantID)
				}
			}
		})
	}
}

func TestCheckBudget(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	tests := []struct {
		name      string
		tasks     []models.Task
		maxPerRun int
		wantErr   bool
	}{
		{
			name:      "under budget passes",
			tasks:     []models.Task{},
			maxPerRun: 10,
			wantErr:   false,
		},
		{
			name: "at budget fails",
			tasks: func() []models.Task {
				var tasks []models.Task
				for i := 0; i < 10; i++ {
					tasks = append(tasks, models.Task{
						ID:              fmt.Sprintf("task-%d", i),
						Status:          models.TaskStatusReady,
						OriginFindingID: fmt.Sprintf("f-%d", i),
						Created:         now,
					})
				}
				return tasks
			}(),
			maxPerRun: 10,
			wantErr:   true,
		},
		{
			name: "terminal tasks not counted",
			tasks: func() []models.Task {
				var tasks []models.Task
				for i := 0; i < 10; i++ {
					tasks = append(tasks, models.Task{
						ID:              fmt.Sprintf("task-%d", i),
						Status:          models.TaskStatusMerged,
						OriginFindingID: fmt.Sprintf("f-%d", i),
						Created:         now,
					})
				}
				return tasks
			}(),
			maxPerRun: 10,
			wantErr:   false,
		},
		{
			name: "tasks without finding ID not counted",
			tasks: func() []models.Task {
				var tasks []models.Task
				for i := 0; i < 15; i++ {
					tasks = append(tasks, models.Task{
						ID:      fmt.Sprintf("task-%d", i),
						Status:  models.TaskStatusReady,
						Created: now,
					})
				}
				return tasks
			}(),
			maxPerRun: 10,
			wantErr:   false,
		},
		{
			name:      "zero maxPerRun uses default",
			tasks:     []models.Task{},
			maxPerRun: 0,
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := CheckBudget(tt.tasks, tt.maxPerRun)

			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestSeverityToPriority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		severity string
		want     int
	}{
		{"HIGH", 1},
		{"MEDIUM", 2},
		{"LOW", 3},
		{"UNKNOWN", 2},
	}

	for _, tt := range tests {
		t.Run(tt.severity, func(t *testing.T) {
			t.Parallel()
			if got := severityToPriority(tt.severity); got != tt.want {
				t.Errorf("severityToPriority(%q) = %d, want %d", tt.severity, got, tt.want)
			}
		})
	}
}
