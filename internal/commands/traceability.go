package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/liza-mas/liza/internal/models"
)

// TraceabilityEntry represents one row in the traceability matrix.
type TraceabilityEntry struct {
	Requirement        string   `json:"requirement" yaml:"requirement"`
	Tasks              []string `json:"tasks" yaml:"tasks"`
	Statuses           []string `json:"statuses" yaml:"statuses"`
	AcceptanceCriteria []string `json:"acceptance_criteria" yaml:"acceptance_criteria"`
	VerifyCmds         []string `json:"verify_commands" yaml:"verify_commands"`
	Coverage           string   `json:"coverage" yaml:"coverage"` // FULL, PARTIAL, NONE
}

// TraceabilityMatrix is the full traceability output.
type TraceabilityMatrix struct {
	Entries       []TraceabilityEntry `json:"entries" yaml:"entries"`
	TotalReqs     int                 `json:"total_requirements" yaml:"total_requirements"`
	CoveredReqs   int                 `json:"covered_requirements" yaml:"covered_requirements"`
	UncoveredReqs int                 `json:"uncovered_requirements" yaml:"uncovered_requirements"`
	OrphanTasks   []string            `json:"orphan_tasks" yaml:"orphan_tasks"` // tasks with no requirement refs
}

// inspectTraceability builds the requirement → task → verification traceability matrix.
func inspectTraceability(state *models.State, format string) (string, error) {
	// Build requirement → tasks mapping
	reqTasks := make(map[string][]string)
	reqStatuses := make(map[string][]string)
	reqAC := make(map[string][]string)
	reqVerifyCmds := make(map[string][]string)
	taskHasReqs := make(map[string]bool)

	for _, task := range state.Tasks {
		if task.Status.IsTerminal() {
			continue // skip abandoned/superseded
		}
		if len(task.RequirementRefs) == 0 {
			taskHasReqs[task.ID] = false
			continue
		}
		taskHasReqs[task.ID] = true
		for _, ref := range task.RequirementRefs {
			ref = strings.TrimSpace(ref)
			reqTasks[ref] = append(reqTasks[ref], task.ID)
			reqStatuses[ref] = append(reqStatuses[ref], string(task.Status))
			for _, ac := range task.AcceptanceCriteria {
				reqAC[ref] = appendUnique(reqAC[ref], ac)
			}
			for _, cmd := range task.VerifyCommands {
				reqVerifyCmds[ref] = appendUnique(reqVerifyCmds[ref], cmd)
			}
		}
	}

	// Build sorted entries
	var entries []TraceabilityEntry
	var reqs []string
	for req := range reqTasks {
		reqs = append(reqs, req)
	}
	sort.Strings(reqs)

	for _, req := range reqs {
		coverage := computeCoverage(reqStatuses[req])
		entries = append(entries, TraceabilityEntry{
			Requirement:        req,
			Tasks:              reqTasks[req],
			Statuses:           reqStatuses[req],
			AcceptanceCriteria: reqAC[req],
			VerifyCmds:         reqVerifyCmds[req],
			Coverage:           coverage,
		})
	}

	// Find orphan tasks (no requirement refs)
	var orphans []string
	for _, task := range state.Tasks {
		if task.Status.IsTerminal() {
			continue
		}
		if !taskHasReqs[task.ID] {
			orphans = append(orphans, task.ID)
		}
	}

	matrix := TraceabilityMatrix{
		Entries:       entries,
		TotalReqs:     len(reqs),
		CoveredReqs:   countCovered(entries),
		UncoveredReqs: len(reqs) - countCovered(entries),
		OrphanTasks:   orphans,
	}

	switch format {
	case "json":
		return formatOutput(matrix, "json")
	case "yaml":
		return formatOutput(matrix, "yaml")
	default:
		return formatTraceabilityTable(matrix), nil
	}
}

func formatTraceabilityTable(m TraceabilityMatrix) string {
	var sb strings.Builder

	sb.WriteString("=== TRACEABILITY MATRIX ===\n\n")

	if len(m.Entries) == 0 {
		sb.WriteString("No tasks with requirement_refs found.\n")
	} else {
		sb.WriteString(fmt.Sprintf("%-12s %-25s %-15s %-10s %-30s %s\n",
			"REQUIREMENT", "TASKS", "STATUSES", "COVERAGE", "ACCEPTANCE CRITERIA", "VERIFY COMMANDS"))
		sb.WriteString(strings.Repeat("-", 130) + "\n")

		for _, e := range m.Entries {
			tasks := strings.Join(e.Tasks, ", ")
			statuses := strings.Join(e.Statuses, ", ")
			ac := strings.Join(e.AcceptanceCriteria, "; ")
			cmds := strings.Join(e.VerifyCmds, "; ")
			sb.WriteString(fmt.Sprintf("%-12s %-25s %-15s %-10s %-30s %s\n",
				e.Requirement, truncateStr(tasks, 23), truncateStr(statuses, 13), e.Coverage, truncateStr(ac, 28), truncateStr(cmds, 40)))
		}
	}

	sb.WriteString(fmt.Sprintf("\nSummary: %d requirements, %d covered, %d uncovered\n",
		m.TotalReqs, m.CoveredReqs, m.UncoveredReqs))

	if len(m.OrphanTasks) > 0 {
		sb.WriteString(fmt.Sprintf("Orphan tasks (no requirement_refs): %s\n", strings.Join(m.OrphanTasks, ", ")))
	}

	return sb.String()
}

func computeCoverage(statuses []string) string {
	allMerged := true
	anyMerged := false
	for _, s := range statuses {
		if s == string(models.TaskStatusMerged) {
			anyMerged = true
		} else {
			allMerged = false
		}
	}
	if allMerged && len(statuses) > 0 {
		return "FULL"
	}
	if anyMerged {
		return "PARTIAL"
	}
	return "NONE"
}

func countCovered(entries []TraceabilityEntry) int {
	n := 0
	for _, e := range entries {
		if e.Coverage == "FULL" || e.Coverage == "PARTIAL" {
			n++
		}
	}
	return n
}

func appendUnique(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
