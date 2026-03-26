package models

import (
	"fmt"
	"slices"
	"time"
)

// State represents the complete Liza state.yaml structure
type State struct {
	Version         int                    `yaml:"version"`
	PipelineVersion int                    `yaml:"pipeline_version,omitempty"`
	Goal            Goal                   `yaml:"goal"`
	Tasks           []Task                 `yaml:"tasks"`
	Agents          map[string]Agent       `yaml:"agents"`
	Discovered      []Discovery            `yaml:"discovered"`
	Handoff         map[string]HandoffNote `yaml:"handoff"`
	HumanNotes      []HumanNote            `yaml:"human_notes"`
	SpecChanges     []SpecChange           `yaml:"spec_changes"`
	Anomalies       []Anomaly              `yaml:"anomalies"`
	Sprint          Sprint                 `yaml:"sprint"`
	SprintHistory   []SprintSummary        `yaml:"sprint_history,omitempty"`
	CircuitBreaker  CircuitBreaker         `yaml:"circuit_breaker"`
	AuditFindings   []AuditFinding         `yaml:"audit_findings,omitempty"`
	Config          Config                 `yaml:"config"`
	Extra           map[string]any         `yaml:",inline"`
}

// FindTask returns a pointer to the task with the given ID, or nil if not found.
// The returned pointer refers to the element within s.Tasks, so mutations are
// reflected in the state (useful inside Blackboard.Modify closures).
func (s *State) FindTask(taskID string) *Task {
	for i := range s.Tasks {
		if s.Tasks[i].ID == taskID {
			return &s.Tasks[i]
		}
	}
	return nil
}

// FindTaskIndex returns the index of the task with the given ID, or -1 if not found.
// Use when you need to remove a task from the slice.
func (s *State) FindTaskIndex(taskID string) int {
	for i := range s.Tasks {
		if s.Tasks[i].ID == taskID {
			return i
		}
	}
	return -1
}

// FindOrchestratorID returns the agent ID of the registered orchestrator.
// Returns an error if zero or more than one orchestrator is registered,
// since map iteration order is nondeterministic.
//
// Lease expiry is intentionally not checked here. This function answers
// "who is the orchestrator?" not "is the orchestrator alive?". The
// registration guard in agent.registerAgent prevents two live orchestrators;
// stale agents should be cleaned up via delete-agent.
func (s *State) FindOrchestratorID() (string, error) {
	var found string
	for id, agent := range s.Agents {
		if agent.Role == "orchestrator" {
			if found != "" {
				return "", fmt.Errorf("multiple orchestrators registered (%s, %s); pass --agent-id explicitly", found, id)
			}
			found = id
		}
	}
	if found == "" {
		return "", fmt.Errorf("no orchestrator agent registered; pass --agent-id explicitly")
	}
	return found, nil
}

// HandoffNote represents context handoff between agents
type HandoffNote struct {
	Agent         string         `yaml:"agent"`
	ContextUsed   int            `yaml:"context_used"`
	Timestamp     time.Time      `yaml:"timestamp"`
	Summary       string         `yaml:"summary"`
	NextAction    string         `yaml:"next_action"`
	Approach      *string        `yaml:"approach,omitempty"`
	Blockers      *string        `yaml:"blockers,omitempty"`
	FilesModified []string       `yaml:"files_modified,omitempty"`
	NextSteps     []string       `yaml:"next_steps,omitempty"`
	Extra         map[string]any `yaml:",inline"`
}

// VerificationResult records the outcome of running verify_commands during merge.
type VerificationResult struct {
	Passed    bool                    `yaml:"passed"`
	Output    string                  `yaml:"output,omitempty"`
	Timestamp time.Time               `yaml:"timestamp"`
	Commands  []VerificationCmdResult `yaml:"commands,omitempty"`
	Phase     string                  `yaml:"phase,omitempty"` // e.g. "merge", "post_submission"
}

// VerificationCmdResult records the outcome of a single verification command.
type VerificationCmdResult struct {
	Command  string        `yaml:"command"`
	ExitCode int           `yaml:"exit_code"`
	Output   string        `yaml:"output,omitempty"`
	Duration time.Duration `yaml:"duration"`
	Error    string        `yaml:"error,omitempty"`
}

// AuditFinding represents a structured finding from the auditor agent.
// Findings are advisory — the supervisor retains deterministic PASS/FAIL control.
type AuditFinding struct {
	ID                string         `yaml:"id"`
	TaskID            string         `yaml:"task_id"`
	Severity          string         `yaml:"severity"`                 // HIGH, MEDIUM, LOW
	Type              string         `yaml:"type"`                     // SPEC_MISMATCH, MISSING_TEST, MISSING_EDGE_CASE, QUALITY_ISSUE
	Phase             string         `yaml:"phase,omitempty"`          // pre_execution, post_execution, post_merge
	Classification    string         `yaml:"classification,omitempty"` // LOG_ONLY, REMEDIATE_WITH_TASK, REOPEN_TASK, REPLAN_REQUIRED
	SpecReference     string         `yaml:"spec_reference,omitempty"`
	Evidence          string         `yaml:"evidence"`
	RecommendedAction string         `yaml:"recommended_action,omitempty"`
	Created           time.Time      `yaml:"created"`
	Resolved          bool           `yaml:"resolved,omitempty"`
	LinkedTaskID      string         `yaml:"linked_task_id,omitempty"` // for REMEDIATE_WITH_TASK: the remediation task ID
	Extra             map[string]any `yaml:",inline"`
}

// IsValidSeverity checks if the audit finding severity is valid.
func (f *AuditFinding) IsValidSeverity() bool {
	return f.Severity == "HIGH" || f.Severity == "MEDIUM" || f.Severity == "LOW"
}

// IsValidType checks if the audit finding type is valid.
func (f *AuditFinding) IsValidType() bool {
	validTypes := []string{
		"SPEC_MISMATCH", "MISSING_TEST", "MISSING_EDGE_CASE", "QUALITY_ISSUE",
		"CAPABILITY_MISSING", "VERIFICATION_GAP", "ARCHITECTURE_DEBT", "SYSTEMIC_SPEC_DRIFT",
	}
	return slices.Contains(validTypes, f.Type)
}

// ValidAuditPhases are the allowed values for AuditFinding.Phase.
var ValidAuditPhases = []string{"pre_execution", "post_execution", "post_merge"}

// IsValidPhase checks if the audit finding phase is valid.
func (f *AuditFinding) IsValidPhase() bool {
	if f.Phase == "" {
		return true // phase is optional for backward compatibility
	}
	return slices.Contains(ValidAuditPhases, f.Phase)
}

// ValidClassifications are the allowed values for AuditFinding.Classification.
var ValidClassifications = []string{"LOG_ONLY", "REMEDIATE_WITH_TASK", "REOPEN_TASK", "REPLAN_REQUIRED"}

// IsValidClassification checks if the audit finding classification is valid.
func (f *AuditFinding) IsValidClassification() bool {
	if f.Classification == "" {
		return true // classification is optional for backward compatibility
	}
	return slices.Contains(ValidClassifications, f.Classification)
}
