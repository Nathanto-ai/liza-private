package commands

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

func TestInspectTraceability_NoTasks(t *testing.T) {
	t.Parallel()
	state := testhelpers.CreateValidState()
	state.Tasks = nil

	out, err := inspectTraceability(state, "table")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No tasks with requirement_refs found") {
		t.Errorf("expected 'No tasks' message, got:\n%s", out)
	}
	if !strings.Contains(out, "0 requirements") {
		t.Errorf("expected '0 requirements' in summary, got:\n%s", out)
	}
}

func TestInspectTraceability_SingleRequirement(t *testing.T) {
	t.Parallel()
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{
		{
			ID:              "task-1",
			Description:     "Implement R1",
			Status:          models.TaskStatusReady,
			RequirementRefs: []string{"R1"},
			VerifyCommands:  []string{"pytest tests/test_r1.py"},
			Priority:        1,
			Scope:           "module",
			SpecRef:         "specs/vision.md",
			DoneWhen:        "tests pass",
		},
	}

	out, err := inspectTraceability(state, "table")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "R1") {
		t.Errorf("expected R1 in output, got:\n%s", out)
	}
	if !strings.Contains(out, "task-1") {
		t.Errorf("expected task-1 in output, got:\n%s", out)
	}
	if !strings.Contains(out, "NONE") {
		t.Errorf("expected NONE coverage (not merged), got:\n%s", out)
	}
	if !strings.Contains(out, "1 requirements, 0 covered, 1 uncovered") {
		t.Errorf("expected summary '1 requirements, 0 covered, 1 uncovered', got:\n%s", out)
	}
}

func TestInspectTraceability_MergedFullCoverage(t *testing.T) {
	t.Parallel()
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{
		{
			ID:              "task-1",
			Description:     "Implement R1",
			Status:          models.TaskStatusMerged,
			RequirementRefs: []string{"R1"},
			VerifyCommands:  []string{"pytest tests/test_r1.py"},
			Priority:        1,
			Scope:           "module",
			SpecRef:         "specs/vision.md",
			DoneWhen:        "tests pass",
		},
	}

	out, err := inspectTraceability(state, "table")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "FULL") {
		t.Errorf("expected FULL coverage, got:\n%s", out)
	}
	if !strings.Contains(out, "1 requirements, 1 covered, 0 uncovered") {
		t.Errorf("expected summary '1 requirements, 1 covered', got:\n%s", out)
	}
}

func TestInspectTraceability_PartialCoverage(t *testing.T) {
	t.Parallel()
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{
		{
			ID:              "task-1",
			Description:     "Implement R1 part A",
			Status:          models.TaskStatusMerged,
			RequirementRefs: []string{"R1"},
			VerifyCommands:  []string{"pytest tests/test_a.py"},
			Priority:        1,
			Scope:           "module",
			SpecRef:         "specs/vision.md",
			DoneWhen:        "tests pass",
		},
		{
			ID:              "task-2",
			Description:     "Implement R1 part B",
			Status:          models.TaskStatusReady,
			RequirementRefs: []string{"R1"},
			VerifyCommands:  []string{"pytest tests/test_b.py"},
			Priority:        2,
			Scope:           "module",
			SpecRef:         "specs/vision.md",
			DoneWhen:        "tests pass",
		},
	}

	out, err := inspectTraceability(state, "table")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "PARTIAL") {
		t.Errorf("expected PARTIAL coverage, got:\n%s", out)
	}
}

func TestInspectTraceability_OrphanTasks(t *testing.T) {
	t.Parallel()
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{
		{
			ID:              "task-with-ref",
			Description:     "Implement R1",
			Status:          models.TaskStatusReady,
			RequirementRefs: []string{"R1"},
			Priority:        1,
			Scope:           "module",
			SpecRef:         "specs/vision.md",
			DoneWhen:        "tests pass",
		},
		{
			ID:          "orphan-task",
			Description: "Some orphan work",
			Status:      models.TaskStatusReady,
			Priority:    2,
			Scope:       "other",
			SpecRef:     "specs/vision.md",
			DoneWhen:    "done",
			// No RequirementRefs — orphan
		},
	}

	out, err := inspectTraceability(state, "table")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "orphan-task") {
		t.Errorf("expected orphan-task in orphans list, got:\n%s", out)
	}
	if !strings.Contains(out, "Orphan tasks") {
		t.Errorf("expected 'Orphan tasks' label, got:\n%s", out)
	}
}

func TestInspectTraceability_AbandonedTasksExcluded(t *testing.T) {
	t.Parallel()
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{
		{
			ID:              "abandoned-1",
			Description:     "Old work for R1",
			Status:          models.TaskStatusAbandoned,
			RequirementRefs: []string{"R1"},
			Priority:        1,
			Scope:           "module",
			SpecRef:         "specs/vision.md",
			DoneWhen:        "tests pass",
		},
	}

	out, err := inspectTraceability(state, "table")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No tasks with requirement_refs found") {
		t.Errorf("abandoned tasks should be excluded, got:\n%s", out)
	}
}

func TestInspectTraceability_JSONFormat(t *testing.T) {
	t.Parallel()
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{
		{
			ID:              "task-1",
			Description:     "Implement R1",
			Status:          models.TaskStatusReady,
			RequirementRefs: []string{"R1"},
			VerifyCommands:  []string{"make test"},
			Priority:        1,
			Scope:           "module",
			SpecRef:         "specs/vision.md",
			DoneWhen:        "tests pass",
		},
	}

	out, err := inspectTraceability(state, "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var matrix TraceabilityMatrix
	if err := json.Unmarshal([]byte(out), &matrix); err != nil {
		t.Fatalf("failed to parse JSON: %v\noutput: %s", err, out)
	}
	if matrix.TotalReqs != 1 {
		t.Errorf("expected 1 total req, got %d", matrix.TotalReqs)
	}
	if len(matrix.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(matrix.Entries))
	}
	if matrix.Entries[0].Requirement != "R1" {
		t.Errorf("expected R1, got %s", matrix.Entries[0].Requirement)
	}
	if len(matrix.Entries[0].VerifyCmds) != 1 || matrix.Entries[0].VerifyCmds[0] != "make test" {
		t.Errorf("expected [make test], got %v", matrix.Entries[0].VerifyCmds)
	}
}

func TestInspectTraceability_MultipleRequirements(t *testing.T) {
	t.Parallel()
	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{
		{
			ID:              "task-1",
			Description:     "Implement crawling",
			Status:          models.TaskStatusMerged,
			RequirementRefs: []string{"R1", "R2"},
			VerifyCommands:  []string{"pytest tests/"},
			Priority:        1,
			Scope:           "crawler",
			SpecRef:         "specs/vision.md",
			DoneWhen:        "tests pass",
		},
		{
			ID:              "task-2",
			Description:     "Implement dedup",
			Status:          models.TaskStatusReady,
			RequirementRefs: []string{"R2"},
			VerifyCommands:  []string{"pytest tests/test_dedup.py"},
			Priority:        2,
			Scope:           "dedup",
			SpecRef:         "specs/vision.md",
			DoneWhen:        "tests pass",
		},
	}

	out, err := inspectTraceability(state, "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var matrix TraceabilityMatrix
	if err := json.Unmarshal([]byte(out), &matrix); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if matrix.TotalReqs != 2 {
		t.Errorf("expected 2 total reqs, got %d", matrix.TotalReqs)
	}

	// R1 should have FULL coverage (only task-1, which is merged)
	// R2 should have PARTIAL coverage (task-1 merged, task-2 ready)
	for _, entry := range matrix.Entries {
		switch entry.Requirement {
		case "R1":
			if entry.Coverage != "FULL" {
				t.Errorf("R1 coverage = %s, want FULL", entry.Coverage)
			}
			if len(entry.Tasks) != 1 {
				t.Errorf("R1 should have 1 task, got %d", len(entry.Tasks))
			}
		case "R2":
			if entry.Coverage != "PARTIAL" {
				t.Errorf("R2 coverage = %s, want PARTIAL", entry.Coverage)
			}
			if len(entry.Tasks) != 2 {
				t.Errorf("R2 should have 2 tasks, got %d", len(entry.Tasks))
			}
			// Verify commands should be deduplicated
			if len(entry.VerifyCmds) != 2 {
				t.Errorf("R2 verify_commands = %v, expected 2 unique cmds", entry.VerifyCmds)
			}
		default:
			t.Errorf("unexpected requirement: %s", entry.Requirement)
		}
	}
}

func TestComputeCoverage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		statuses []string
		want     string
	}{
		{"all merged", []string{"MERGED", "MERGED"}, "FULL"},
		{"some merged", []string{"MERGED", "READY"}, "PARTIAL"},
		{"none merged", []string{"READY", "IMPLEMENTING"}, "NONE"},
		{"single merged", []string{"MERGED"}, "FULL"},
		{"single ready", []string{"READY"}, "NONE"},
		{"empty", []string{}, "NONE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeCoverage(tt.statuses)
			if got != tt.want {
				t.Errorf("computeCoverage(%v) = %s, want %s", tt.statuses, got, tt.want)
			}
		})
	}
}

func TestTruncateStr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		max   int
		want  string
	}{
		{"short", 10, "short"},
		{"exactly10c", 10, "exactly10c"},
		{"this is way too long", 10, "this is..."},
		{"abc", 3, "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := truncateStr(tt.input, tt.max)
			if got != tt.want {
				t.Errorf("truncateStr(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
			}
		})
	}
}

func TestAppendUnique(t *testing.T) {
	t.Parallel()
	slice := []string{"a", "b"}
	result := appendUnique(slice, "c")
	if len(result) != 3 {
		t.Errorf("expected 3 items, got %d", len(result))
	}
	result = appendUnique(result, "b") // duplicate
	if len(result) != 3 {
		t.Errorf("expected 3 items (no dup), got %d", len(result))
	}
}
