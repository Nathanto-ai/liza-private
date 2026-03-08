package models

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestTaskStatusConstants(t *testing.T) {
	validStatuses := []TaskStatus{
		TaskStatusDraft,
		TaskStatusReady,
		TaskStatusImplementing,
		TaskStatusReadyForReview,
		TaskStatusReviewing,
		TaskStatusRejected,
		TaskStatusApproved,
		TaskStatusMerged,
		TaskStatusBlocked,
		TaskStatusAbandoned,
		TaskStatusSuperseded,
		TaskStatusIntegrationFailed,
	}

	for _, status := range validStatuses {
		if !status.IsValid() {
			t.Errorf("Status %s should be valid", status)
		}
	}

	invalidStatus := TaskStatus("INVALID")
	if invalidStatus.IsValid() {
		t.Errorf("Status %s should be invalid", invalidStatus)
	}
}

func TestTaskTerminalStates(t *testing.T) {
	terminalStates := []TaskStatus{
		TaskStatusAbandoned,
		TaskStatusSuperseded,
	}

	for _, status := range terminalStates {
		if !status.IsTerminal() {
			t.Errorf("Status %s should be terminal", status)
		}
	}

	nonTerminalStates := []TaskStatus{
		TaskStatusDraft,
		TaskStatusReady,
		TaskStatusImplementing,
		TaskStatusReadyForReview,
		TaskStatusReviewing,
		TaskStatusRejected,
		TaskStatusApproved,
		TaskStatusBlocked,
		TaskStatusIntegrationFailed,
		TaskStatusMerged, // MERGED can transition to READY via audit REOPEN_TASK
	}

	for _, status := range nonTerminalStates {
		if status.IsTerminal() {
			t.Errorf("Status %s should not be terminal", status)
		}
	}
}

func TestTaskTransitionMapCompleteness(t *testing.T) {
	// Every valid status must have an entry in the transition map.
	allStatuses := []TaskStatus{
		TaskStatusDraft, TaskStatusReady, TaskStatusImplementing,
		TaskStatusReadyForReview, TaskStatusReviewing, TaskStatusRejected,
		TaskStatusApproved, TaskStatusMerged, TaskStatusBlocked,
		TaskStatusAbandoned, TaskStatusSuperseded, TaskStatusIntegrationFailed,
	}
	for _, s := range allStatuses {
		if _, ok := taskTransitions[s]; !ok {
			t.Errorf("status %s missing from taskTransitions map", s)
		}
	}
}

func TestTaskTransitionMapTargetsValid(t *testing.T) {
	// Every target in the map must be a valid TaskStatus.
	for from, targets := range taskTransitions {
		for _, to := range targets {
			if !to.IsValid() {
				t.Errorf("taskTransitions[%s] contains invalid target %s", from, to)
			}
		}
	}
}

func TestTerminalStatesHaveNoTransitions(t *testing.T) {
	terminals := []TaskStatus{TaskStatusAbandoned, TaskStatusSuperseded}
	for _, s := range terminals {
		targets := taskTransitions[s]
		if len(targets) != 0 {
			t.Errorf("terminal status %s has non-empty transition targets: %v", s, targets)
		}
	}
}

func TestMergedIsNotTerminalButIsComplete(t *testing.T) {
	if TaskStatusMerged.IsTerminal() {
		t.Error("MERGED should not be terminal (can be reopened by audit)")
	}
	if !TaskStatusMerged.IsComplete() {
		t.Error("MERGED should be complete (work is done)")
	}
	targets := taskTransitions[TaskStatusMerged]
	if len(targets) != 1 || targets[0] != TaskStatusReady {
		t.Errorf("MERGED transitions = %v, want [READY]", targets)
	}
}

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from TaskStatus
		to   TaskStatus
		want bool
	}{
		// Valid edges
		{TaskStatusDraft, TaskStatusReady, true},
		{TaskStatusDraft, TaskStatusAbandoned, true},
		{TaskStatusReady, TaskStatusImplementing, true},
		{TaskStatusReady, TaskStatusSuperseded, true},
		{TaskStatusReady, TaskStatusAbandoned, true},
		{TaskStatusImplementing, TaskStatusReadyForReview, true},
		{TaskStatusImplementing, TaskStatusBlocked, true},
		{TaskStatusImplementing, TaskStatusReady, true},
		{TaskStatusReadyForReview, TaskStatusReviewing, true},
		{TaskStatusReviewing, TaskStatusApproved, true},
		{TaskStatusReviewing, TaskStatusRejected, true},
		{TaskStatusReviewing, TaskStatusReadyForReview, true},
		{TaskStatusRejected, TaskStatusImplementing, true},
		{TaskStatusRejected, TaskStatusBlocked, true},
		{TaskStatusRejected, TaskStatusSuperseded, true},
		{TaskStatusRejected, TaskStatusAbandoned, true},
		{TaskStatusApproved, TaskStatusMerged, true},
		{TaskStatusApproved, TaskStatusIntegrationFailed, true},
		{TaskStatusBlocked, TaskStatusSuperseded, true},
		{TaskStatusBlocked, TaskStatusAbandoned, true},
		{TaskStatusIntegrationFailed, TaskStatusImplementing, true},
		{TaskStatusIntegrationFailed, TaskStatusAbandoned, true},

		// Invalid edges
		{TaskStatusDraft, TaskStatusImplementing, false},
		{TaskStatusReady, TaskStatusApproved, false},
		{TaskStatusImplementing, TaskStatusMerged, false},
		{TaskStatusReadyForReview, TaskStatusApproved, false},
		{TaskStatusApproved, TaskStatusReady, false},
		{TaskStatusMerged, TaskStatusReady, true}, // audit REOPEN_TASK
		{TaskStatusMerged, TaskStatusImplementing, false},
		{TaskStatusAbandoned, TaskStatusReady, false},
		{TaskStatusSuperseded, TaskStatusReady, false},

		// Unknown status
		{TaskStatus("UNKNOWN"), TaskStatusReady, false},
	}

	for _, tt := range tests {
		name := string(tt.from) + "→" + string(tt.to)
		t.Run(name, func(t *testing.T) {
			got := tt.from.CanTransition(tt.to)
			if got != tt.want {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestTaskTransition(t *testing.T) {
	t.Run("valid transition", func(t *testing.T) {
		task := Task{ID: "task-1", Status: TaskStatusDraft}
		err := task.Transition(TaskStatusReady)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if task.Status != TaskStatusReady {
			t.Errorf("status = %s, want READY", task.Status)
		}
	})

	t.Run("invalid transition returns error", func(t *testing.T) {
		task := Task{ID: "task-42", Status: TaskStatusDraft}
		err := task.Transition(TaskStatusMerged)
		if err == nil {
			t.Fatal("expected error for invalid transition")
		}
		want := "invalid task transition: DRAFT → MERGED (task task-42)"
		if err.Error() != want {
			t.Errorf("error = %q, want %q", err.Error(), want)
		}
		// Status must not change on error
		if task.Status != TaskStatusDraft {
			t.Errorf("status changed to %s on failed transition", task.Status)
		}
	})

	t.Run("terminal state rejects all transitions", func(t *testing.T) {
		task := Task{ID: "task-99", Status: TaskStatusAbandoned}
		err := task.Transition(TaskStatusReady)
		if err == nil {
			t.Fatal("expected error transitioning from terminal state")
		}
	})

	t.Run("MERGED to READY succeeds (audit reopen)", func(t *testing.T) {
		task := Task{ID: "task-100", Status: TaskStatusMerged}
		err := task.Transition(TaskStatusReady)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if task.Status != TaskStatusReady {
			t.Errorf("status = %s, want READY", task.Status)
		}
	})
}

func TestAgentStatusConstants(t *testing.T) {
	validStatuses := []AgentStatus{
		AgentStatusStarting,
		AgentStatusIdle,
		AgentStatusWorking,
		AgentStatusReviewing,
		AgentStatusWaiting,
		AgentStatusHandoff,
		AgentStatusPlanning,
	}

	for _, status := range validStatuses {
		if !status.IsValid() {
			t.Errorf("Agent status %s should be valid", status)
		}
	}

	invalidStatus := AgentStatus("INVALID")
	if invalidStatus.IsValid() {
		t.Errorf("Agent status %s should be invalid", invalidStatus)
	}
}

func TestAgentStatusPlanning(t *testing.T) {
	// Test that PLANNING constant exists and has correct value
	if AgentStatusPlanning != "PLANNING" {
		t.Errorf("AgentStatusPlanning value = %s, want PLANNING", AgentStatusPlanning)
	}

	// Test that PLANNING is valid
	if !AgentStatusPlanning.IsValid() {
		t.Error("AgentStatusPlanning should be valid")
	}

	// Test string representation
	status := AgentStatusPlanning
	if string(status) != "PLANNING" {
		t.Errorf("String representation = %s, want PLANNING", string(status))
	}
}

func TestGoalStatusConstants(t *testing.T) {
	validStatuses := []GoalStatus{
		GoalStatusInProgress,
		GoalStatusCompleted,
		GoalStatusAborted,
	}

	for _, status := range validStatuses {
		if !status.IsValid() {
			t.Errorf("Goal status %s should be valid", status)
		}
	}
}

func TestSprintStatusConstants(t *testing.T) {
	validStatuses := []SprintStatus{
		SprintStatusInProgress,
		SprintStatusCheckpoint,
		SprintStatusCompleted,
		SprintStatusAborted,
	}

	for _, status := range validStatuses {
		if !status.IsValid() {
			t.Errorf("Sprint status %s should be valid", status)
		}
	}
}

func TestTaskYAMLMarshaling(t *testing.T) {
	created, _ := time.Parse(time.RFC3339, "2025-01-17T14:05:00Z")
	task := Task{
		ID:          "task-1",
		Description: "Test task",
		Status:      TaskStatusReady,
		Priority:    1,
		Created:     created,
		SpecRef:     "specs/test.md",
		DoneWhen:    "Tests pass",
		DependsOn:   []string{},
		History: []TaskHistoryEntry{
			{
				Time:  created,
				Event: "created",
			},
		},
	}

	// Marshal to YAML
	data, err := yaml.Marshal(&task)
	if err != nil {
		t.Fatalf("Failed to marshal task: %v", err)
	}

	// Unmarshal back
	var unmarshaled Task
	err = yaml.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal task: %v", err)
	}

	// Verify fields
	if unmarshaled.ID != task.ID {
		t.Errorf("ID mismatch: got %s, want %s", unmarshaled.ID, task.ID)
	}
	if unmarshaled.Status != task.Status {
		t.Errorf("Status mismatch: got %s, want %s", unmarshaled.Status, task.Status)
	}
	if unmarshaled.Description != task.Description {
		t.Errorf("Description mismatch: got %s, want %s", unmarshaled.Description, task.Description)
	}
}

func TestAgentYAMLMarshaling(t *testing.T) {
	heartbeat, _ := time.Parse(time.RFC3339, "2025-01-17T14:50:00Z")
	leaseExpires, _ := time.Parse(time.RFC3339, "2025-01-17T15:00:00Z")

	agent := Agent{
		Role:            "coder",
		Status:          AgentStatusWorking,
		CurrentTask:     strPtr("task-1"),
		LeaseExpires:    &leaseExpires,
		Heartbeat:       heartbeat,
		Terminal:        "/dev/pts/2",
		IterationsTotal: 10,
		ContextPercent:  34,
	}

	// Marshal to YAML
	data, err := yaml.Marshal(&agent)
	if err != nil {
		t.Fatalf("Failed to marshal agent: %v", err)
	}

	// Unmarshal back
	var unmarshaled Agent
	err = yaml.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal agent: %v", err)
	}

	// Verify fields
	if unmarshaled.Role != agent.Role {
		t.Errorf("Role mismatch: got %s, want %s", unmarshaled.Role, agent.Role)
	}
	if unmarshaled.Status != agent.Status {
		t.Errorf("Status mismatch: got %s, want %s", unmarshaled.Status, agent.Status)
	}
	if *unmarshaled.CurrentTask != *agent.CurrentTask {
		t.Errorf("CurrentTask mismatch: got %s, want %s", *unmarshaled.CurrentTask, *agent.CurrentTask)
	}
}

func TestStateYAMLMarshaling(t *testing.T) {
	created, _ := time.Parse(time.RFC3339, "2025-01-17T14:00:00Z")
	state := State{
		Version: 1,
		Goal: Goal{
			ID:          "goal-1",
			Description: "Test goal",
			SpecRef:     "specs/vision.md",
			Created:     created,
			Status:      GoalStatusInProgress,
			AlignmentHistory: []AlignmentHistory{
				{
					Timestamp: created,
					Event:     "initialization",
					Summary:   "Initial setup",
				},
			},
		},
		Tasks: []Task{
			{
				ID:          "task-1",
				Description: "Test task",
				Status:      TaskStatusReady,
				Priority:    1,
				Created:     created,
				SpecRef:     "specs/test.md",
				DoneWhen:    "Tests pass",
				History:     []TaskHistoryEntry{},
			},
		},
		Agents: map[string]Agent{
			"coder-1": {
				Role:      "coder",
				Status:    AgentStatusIdle,
				Heartbeat: created,
			},
		},
		Discovered:  []Discovery{},
		Handoff:     map[string]HandoffNote{},
		HumanNotes:  []HumanNote{},
		SpecChanges: []SpecChange{},
		Anomalies:   []Anomaly{},
		Sprint: Sprint{
			ID:      "sprint-1",
			GoalRef: "goal-1",
			Status:  SprintStatusInProgress,
		},
		CircuitBreaker: CircuitBreaker{
			Status: "OK",
		},
		Config: Config{
			MaxCoderIterations: 10,
			MaxReviewCycles:    5,
			HeartbeatInterval:  60,
			LeaseDuration:      1800,
			CoderPollInterval:  30,
			CoderMaxWait:       300,
			IntegrationBranch:  "integration",
		},
	}

	// Marshal to YAML
	data, err := yaml.Marshal(&state)
	if err != nil {
		t.Fatalf("Failed to marshal state: %v", err)
	}

	// Unmarshal back
	var unmarshaled State
	err = yaml.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal state: %v", err)
	}

	// Verify top-level fields
	if unmarshaled.Version != state.Version {
		t.Errorf("Version mismatch: got %d, want %d", unmarshaled.Version, state.Version)
	}
	if unmarshaled.Goal.ID != state.Goal.ID {
		t.Errorf("Goal ID mismatch: got %s, want %s", unmarshaled.Goal.ID, state.Goal.ID)
	}
	if len(unmarshaled.Tasks) != len(state.Tasks) {
		t.Errorf("Tasks count mismatch: got %d, want %d", len(unmarshaled.Tasks), len(state.Tasks))
	}
	if len(unmarshaled.Agents) != len(state.Agents) {
		t.Errorf("Agents count mismatch: got %d, want %d", len(unmarshaled.Agents), len(state.Agents))
	}
}

func TestTaskTypeIsValid(t *testing.T) {
	if !TaskTypeCoding.IsValid() {
		t.Error("TaskTypeCoding should be valid")
	}
	if TaskType("unknown").IsValid() {
		t.Error("unknown task type should be invalid")
	}
	if TaskType("").IsValid() {
		t.Error("empty task type should be invalid (not in registry)")
	}
}

func TestRoleConstants(t *testing.T) {
	if RoleCoder != "coder" {
		t.Errorf("RoleCoder = %q, want %q", RoleCoder, "coder")
	}
	if RoleCodeReviewer != "code_reviewer" {
		t.Errorf("RoleCodeReviewer = %q, want %q", RoleCodeReviewer, "code_reviewer")
	}
	if RolePlanner != "planner" {
		t.Errorf("RolePlanner = %q, want %q", RolePlanner, "planner")
	}
}

func TestTaskTypeRoleWorkflow(t *testing.T) {
	workflow := TaskTypeCoding.RoleWorkflow()
	if len(workflow) != 2 {
		t.Fatalf("coding workflow should have 2 roles, got %d", len(workflow))
	}
	if workflow[0] != RoleCoder || workflow[1] != RoleCodeReviewer {
		t.Errorf("coding workflow = %v, want [coder code_reviewer]", workflow)
	}
}

func TestTaskTypeHasRole(t *testing.T) {
	if !TaskTypeCoding.HasRole(RoleCoder) {
		t.Error("coding type should have coder role")
	}
	if !TaskTypeCoding.HasRole(RoleCodeReviewer) {
		t.Error("coding type should have code_reviewer role")
	}
	if TaskTypeCoding.HasRole(RolePlanner) {
		t.Error("coding type should not have planner role")
	}
}

func TestEffectiveType(t *testing.T) {
	// Empty type defaults to coding
	task := Task{Type: ""}
	if task.EffectiveType() != TaskTypeCoding {
		t.Errorf("EffectiveType() for empty = %s, want %s", task.EffectiveType(), TaskTypeCoding)
	}

	// Explicit coding type
	task = Task{Type: TaskTypeCoding}
	if task.EffectiveType() != TaskTypeCoding {
		t.Errorf("EffectiveType() for coding = %s, want %s", task.EffectiveType(), TaskTypeCoding)
	}
}

func TestTaskTypeBackwardCompat(t *testing.T) {
	// YAML without type field should unmarshal with empty Type
	yamlData := `id: task-1
description: Test task
status: READY
priority: 1
`
	var task Task
	if err := yaml.Unmarshal([]byte(yamlData), &task); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if task.Type != "" {
		t.Errorf("Type should be empty for backward compat, got %q", task.Type)
	}
	if task.EffectiveType() != TaskTypeCoding {
		t.Errorf("EffectiveType() = %s, want %s", task.EffectiveType(), TaskTypeCoding)
	}
}

func TestIsClaimableWithRole(t *testing.T) {
	tests := []struct {
		name      string
		task      Task
		role      string
		claimable bool
	}{
		{
			name:      "coder can claim READY coding task",
			task:      Task{Status: TaskStatusReady, Type: TaskTypeCoding},
			role:      RoleCoder,
			claimable: true,
		},
		{
			name:      "coder can claim REJECTED coding task",
			task:      Task{Status: TaskStatusRejected, Type: TaskTypeCoding},
			role:      RoleCoder,
			claimable: true,
		},
		{
			name:      "coder cannot claim READY_FOR_REVIEW task",
			task:      Task{Status: TaskStatusReadyForReview, Type: TaskTypeCoding},
			role:      RoleCoder,
			claimable: false,
		},
		{
			name:      "code_reviewer can claim READY_FOR_REVIEW coding task",
			task:      Task{Status: TaskStatusReadyForReview, Type: TaskTypeCoding},
			role:      RoleCodeReviewer,
			claimable: true,
		},
		{
			name:      "code_reviewer cannot claim READY task",
			task:      Task{Status: TaskStatusReady, Type: TaskTypeCoding},
			role:      RoleCodeReviewer,
			claimable: false,
		},
		{
			name:      "planner cannot claim any task",
			task:      Task{Status: TaskStatusReady, Type: TaskTypeCoding},
			role:      RolePlanner,
			claimable: false,
		},
		{
			name:      "unknown type is not claimable",
			task:      Task{Status: TaskStatusReady, Type: TaskType("unknown")},
			role:      RoleCoder,
			claimable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.task.IsClaimable(tt.role, nil)
			if result != tt.claimable {
				t.Errorf("IsClaimable(%q) = %v, want %v", tt.role, result, tt.claimable)
			}
		})
	}
}

func TestTaskClaimability(t *testing.T) {
	tests := []struct {
		name      string
		task      Task
		claimable bool
	}{
		{
			name: "READY with no dependencies",
			task: Task{
				Status:    TaskStatusReady,
				DependsOn: []string{},
			},
			claimable: true,
		},
		{
			name: "READY with nil dependencies",
			task: Task{
				Status: TaskStatusReady,
			},
			claimable: true,
		},
		{
			name: "REJECTED is claimable",
			task: Task{
				Status:    TaskStatusRejected,
				DependsOn: []string{},
			},
			claimable: true,
		},
		{
			name: "INTEGRATION_FAILED is claimable",
			task: Task{
				Status:    TaskStatusIntegrationFailed,
				DependsOn: []string{},
			},
			claimable: true,
		},
		{
			name: "DRAFT is not claimable",
			task: Task{
				Status: TaskStatusDraft,
			},
			claimable: false,
		},
		{
			name: "IMPLEMENTING is not claimable",
			task: Task{
				Status: TaskStatusImplementing,
			},
			claimable: false,
		},
		{
			name: "MERGED is not claimable",
			task: Task{
				Status: TaskStatusMerged,
			},
			claimable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.task.IsClaimable(RoleCoder, nil) // nil means all dependencies are satisfied
			if result != tt.claimable {
				t.Errorf("IsClaimable() = %v, want %v", result, tt.claimable)
			}
		})
	}
}

func TestTaskClaimabilityWithDependencies(t *testing.T) {
	// Mock function that checks if dependencies are satisfied
	allTasks := []Task{
		{ID: "task-1", Status: TaskStatusMerged},
		{ID: "task-2", Status: TaskStatusReady},
		{ID: "task-3", Status: TaskStatusMerged},
	}

	tests := []struct {
		name      string
		task      Task
		claimable bool
	}{
		{
			name: "READY with all dependencies MERGED",
			task: Task{
				ID:        "task-4",
				Status:    TaskStatusReady,
				DependsOn: []string{"task-1", "task-3"},
			},
			claimable: true,
		},
		{
			name: "READY with unmet dependencies",
			task: Task{
				ID:        "task-5",
				Status:    TaskStatusReady,
				DependsOn: []string{"task-1", "task-2"}, // task-2 is READY
			},
			claimable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.task.IsClaimable(RoleCoder, allTasks)
			if result != tt.claimable {
				t.Errorf("IsClaimable() = %v, want %v", result, tt.claimable)
			}
		})
	}
}

func TestDiscoverySeverity(t *testing.T) {
	validSeverities := []string{"critical", "high", "medium", "low"}
	for _, sev := range validSeverities {
		d := Discovery{Severity: sev}
		if !d.IsValidSeverity() {
			t.Errorf("Severity %s should be valid", sev)
		}
	}

	d := Discovery{Severity: "invalid"}
	if d.IsValidSeverity() {
		t.Error("Invalid severity should not be valid")
	}
}

func TestDiscoveryUrgency(t *testing.T) {
	validUrgencies := []string{"immediate", "deferred"}
	for _, urg := range validUrgencies {
		d := Discovery{Urgency: urg}
		if !d.IsValidUrgency() {
			t.Errorf("Urgency %s should be valid", urg)
		}
	}

	d := Discovery{Urgency: "invalid"}
	if d.IsValidUrgency() {
		t.Error("Invalid urgency should not be valid")
	}
}

func TestAnomalyTypes(t *testing.T) {
	validTypes := []string{
		"retry_loop",
		"trade_off",
		"spec_ambiguity",
		"external_blocker",
		"assumption_violated",
		"scope_deviation",
		"workaround",
		"debt_created",
		"spec_changed",
		"hypothesis_exhaustion",
		"spec_gap",
		"review_deadlock",
		"review_exhaustion",
		"reviewer_loop",
		"system_ambiguity",
	}

	for _, typ := range validTypes {
		a := Anomaly{Type: typ}
		if !a.IsValidType() {
			t.Errorf("Anomaly type %s should be valid", typ)
		}
	}

	a := Anomaly{Type: "invalid"}
	if a.IsValidType() {
		t.Error("Invalid anomaly type should not be valid")
	}
}

func TestCircuitBreakerStatus(t *testing.T) {
	validStatuses := []string{"OK", "TRIGGERED"}
	for _, status := range validStatuses {
		cb := CircuitBreaker{Status: status}
		if !cb.IsValidStatus() {
			t.Errorf("CircuitBreaker status %s should be valid", status)
		}
	}

	cb := CircuitBreaker{Status: "invalid"}
	if cb.IsValidStatus() {
		t.Error("Invalid circuit breaker status should not be valid")
	}
}

func TestCircuitBreakerTriggerYAML(t *testing.T) {
	now := time.Date(2025, 1, 18, 17, 30, 0, 0, time.UTC)

	trigger := CircuitBreakerTrigger{
		Timestamp:  now,
		Pattern:    "retry_cluster",
		Severity:   "ARCHITECTURE_FLAW",
		ReportFile: ".liza/circuit_breaker_report.md",
	}

	yamlData, err := yaml.Marshal(&trigger)
	if err != nil {
		t.Fatalf("Failed to marshal CircuitBreakerTrigger: %v", err)
	}

	var unmarshaledTrigger CircuitBreakerTrigger
	err = yaml.Unmarshal(yamlData, &unmarshaledTrigger)
	if err != nil {
		t.Fatalf("Failed to unmarshal CircuitBreakerTrigger: %v", err)
	}

	if unmarshaledTrigger.Pattern != trigger.Pattern {
		t.Errorf("Pattern mismatch: got %s, want %s", unmarshaledTrigger.Pattern, trigger.Pattern)
	}

	if unmarshaledTrigger.Severity != trigger.Severity {
		t.Errorf("Severity mismatch: got %s, want %s", unmarshaledTrigger.Severity, trigger.Severity)
	}

	if unmarshaledTrigger.ReportFile != trigger.ReportFile {
		t.Errorf("ReportFile mismatch: got %s, want %s", unmarshaledTrigger.ReportFile, trigger.ReportFile)
	}
}

func TestCircuitBreakerHistoryYAML(t *testing.T) {
	now := time.Date(2025, 1, 18, 17, 30, 0, 0, time.UTC)
	resolvedAt := time.Date(2025, 1, 18, 19, 0, 0, 0, time.UTC)

	pattern := "retry_cluster"
	severity := "ARCHITECTURE_FLAW"
	resolution := "ADR-003 created, specs updated"

	history := CircuitBreakerHistory{
		Timestamp:  now,
		Pattern:    &pattern,
		Severity:   &severity,
		Result:     "TRIGGERED",
		Resolution: &resolution,
		ResolvedAt: &resolvedAt,
	}

	yamlData, err := yaml.Marshal(&history)
	if err != nil {
		t.Fatalf("Failed to marshal CircuitBreakerHistory: %v", err)
	}

	var unmarshaledHistory CircuitBreakerHistory
	err = yaml.Unmarshal(yamlData, &unmarshaledHistory)
	if err != nil {
		t.Fatalf("Failed to unmarshal CircuitBreakerHistory: %v", err)
	}

	if *unmarshaledHistory.Pattern != *history.Pattern {
		t.Errorf("Pattern mismatch: got %s, want %s", *unmarshaledHistory.Pattern, *history.Pattern)
	}

	if *unmarshaledHistory.Severity != *history.Severity {
		t.Errorf("Severity mismatch: got %s, want %s", *unmarshaledHistory.Severity, *history.Severity)
	}

	if unmarshaledHistory.Result != history.Result {
		t.Errorf("Result mismatch: got %s, want %s", unmarshaledHistory.Result, history.Result)
	}

	if *unmarshaledHistory.Resolution != *history.Resolution {
		t.Errorf("Resolution mismatch: got %s, want %s", *unmarshaledHistory.Resolution, *history.Resolution)
	}
}

func TestSprintStalled(t *testing.T) {
	now := time.Now().UTC()
	mkTask := func(id string, status TaskStatus) Task {
		return Task{ID: id, Status: status, Created: now, Priority: 1, Iteration: 1}
	}

	tests := []struct {
		name    string
		planned []string
		tasks   []Task
		want    bool
	}{
		{
			name:    "empty planned list",
			planned: []string{},
			tasks:   []Task{mkTask("task-1", TaskStatusBlocked)},
			want:    false,
		},
		{
			name:    "all terminal — complete not stalled",
			planned: []string{"task-1", "task-2"},
			tasks:   []Task{mkTask("task-1", TaskStatusMerged), mkTask("task-2", TaskStatusAbandoned)},
			want:    false,
		},
		{
			name:    "all blocked",
			planned: []string{"task-1", "task-2"},
			tasks:   []Task{mkTask("task-1", TaskStatusBlocked), mkTask("task-2", TaskStatusBlocked)},
			want:    true,
		},
		{
			name:    "mix of terminal and blocked",
			planned: []string{"task-1", "task-2", "task-3"},
			tasks: []Task{
				mkTask("task-1", TaskStatusMerged),
				mkTask("task-2", TaskStatusBlocked),
				mkTask("task-3", TaskStatusSuperseded),
			},
			want: true,
		},
		{
			name:    "blocked plus in-progress — not stalled",
			planned: []string{"task-1", "task-2"},
			tasks:   []Task{mkTask("task-1", TaskStatusBlocked), mkTask("task-2", TaskStatusImplementing)},
			want:    false,
		},
		{
			name:    "single blocked single ready — not stalled",
			planned: []string{"task-1", "task-2"},
			tasks:   []Task{mkTask("task-1", TaskStatusBlocked), mkTask("task-2", TaskStatusReady)},
			want:    false,
		},
		{
			name:    "planned task missing from task list",
			planned: []string{"task-1", "task-2"},
			tasks:   []Task{mkTask("task-1", TaskStatusBlocked)},
			want:    false,
		},
		{
			name:    "extra non-planned tasks dont affect result",
			planned: []string{"task-1"},
			tasks: []Task{
				mkTask("task-1", TaskStatusBlocked),
				mkTask("task-2", TaskStatusImplementing),
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &State{
				Sprint: Sprint{Scope: SprintScope{Planned: tt.planned}},
				Tasks:  tt.tasks,
			}
			got := state.SprintStalled()
			if got != tt.want {
				t.Errorf("SprintStalled() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Helper function for creating string pointers
func strPtr(s string) *string {
	return &s
}

func TestAgentPIDMarshaling(t *testing.T) {
	heartbeat, _ := time.Parse(time.RFC3339, "2025-01-17T14:50:00Z")

	tests := []struct {
		name      string
		agent     Agent
		checkPID  bool
		wantPID   int
		hasPIDTag bool
	}{
		{
			name: "Agent with PID set",
			agent: Agent{
				Role:      "coder",
				Status:    AgentStatusIdle,
				Heartbeat: heartbeat,
				Terminal:  "/dev/pts/1",
				PID:       12345,
			},
			checkPID:  true,
			wantPID:   12345,
			hasPIDTag: true,
		},
		{
			name: "Agent with PID zero (not set)",
			agent: Agent{
				Role:      "coder",
				Status:    AgentStatusIdle,
				Heartbeat: heartbeat,
				Terminal:  "/dev/pts/1",
				PID:       0,
			},
			checkPID:  true,
			wantPID:   0,
			hasPIDTag: false, // omitempty should exclude it
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal to YAML
			data, err := yaml.Marshal(&tt.agent)
			if err != nil {
				t.Fatalf("Failed to marshal agent: %v", err)
			}

			// Check if PID appears in YAML when expected
			yamlStr := string(data)
			hasPID := containsField(yamlStr, "pid:")
			if hasPID != tt.hasPIDTag {
				t.Errorf("PID in YAML = %v, want %v\nYAML:\n%s", hasPID, tt.hasPIDTag, yamlStr)
			}

			// Unmarshal back
			var unmarshaled Agent
			err = yaml.Unmarshal(data, &unmarshaled)
			if err != nil {
				t.Fatalf("Failed to unmarshal agent: %v", err)
			}

			// Verify PID field
			if tt.checkPID && unmarshaled.PID != tt.wantPID {
				t.Errorf("PID mismatch: got %d, want %d", unmarshaled.PID, tt.wantPID)
			}
		})
	}
}

func TestAgentBackwardCompatibility(t *testing.T) {
	// YAML without PID field (backward compatibility test)
	yamlData := `role: coder
status: IDLE
heartbeat: 2025-01-17T14:50:00Z
terminal: /dev/pts/1
iterations_total: 0
context_percent: 0
`

	var agent Agent
	err := yaml.Unmarshal([]byte(yamlData), &agent)
	if err != nil {
		t.Fatalf("Failed to unmarshal agent without PID: %v", err)
	}

	// PID should be zero (default value)
	if agent.PID != 0 {
		t.Errorf("PID should be 0 for backward compatibility, got %d", agent.PID)
	}

	// Verify other fields were parsed correctly
	if agent.Role != "coder" {
		t.Errorf("Role mismatch: got %s, want coder", agent.Role)
	}
	if agent.Status != AgentStatusIdle {
		t.Errorf("Status mismatch: got %s, want IDLE", agent.Status)
	}
}

// Helper function to check if a YAML string contains a field
func containsField(yamlStr, field string) bool {
	// Simple check - look for the field name followed by a colon
	for i := 0; i < len(yamlStr)-len(field); i++ {
		if yamlStr[i:i+len(field)] == field {
			return true
		}
	}
	return false
}

func TestFindTask(t *testing.T) {
	now := time.Now().UTC()
	mkTask := func(id string) Task {
		return Task{ID: id, Status: TaskStatusReady, Created: now, Priority: 1, Iteration: 1}
	}

	state := &State{
		Tasks: []Task{mkTask("task-1"), mkTask("task-2"), mkTask("task-3")},
	}

	tests := []struct {
		name   string
		taskID string
		wantID string // empty means expect nil
	}{
		{"found first", "task-1", "task-1"},
		{"found middle", "task-2", "task-2"},
		{"found last", "task-3", "task-3"},
		{"not found", "task-99", ""},
		{"empty ID", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := state.FindTask(tt.taskID)
			if tt.wantID == "" {
				if got != nil {
					t.Errorf("FindTask(%q) = %v, want nil", tt.taskID, got)
				}
			} else {
				if got == nil {
					t.Fatalf("FindTask(%q) = nil, want task %q", tt.taskID, tt.wantID)
				}
				if got.ID != tt.wantID {
					t.Errorf("FindTask(%q).ID = %q, want %q", tt.taskID, got.ID, tt.wantID)
				}
			}
		})
	}

	// Verify returned pointer refers to slice element (mutations apply)
	t.Run("returned pointer is mutable", func(t *testing.T) {
		task := state.FindTask("task-1")
		task.Status = TaskStatusImplementing
		if state.Tasks[0].Status != TaskStatusImplementing {
			t.Error("mutation via FindTask pointer did not apply to slice element")
		}
		state.Tasks[0].Status = TaskStatusReady // restore
	})

	// Empty state
	t.Run("empty state", func(t *testing.T) {
		empty := &State{}
		if got := empty.FindTask("task-1"); got != nil {
			t.Errorf("FindTask on empty state = %v, want nil", got)
		}
	})
}

func TestFindTaskIndex(t *testing.T) {
	now := time.Now().UTC()
	mkTask := func(id string) Task {
		return Task{ID: id, Status: TaskStatusReady, Created: now, Priority: 1, Iteration: 1}
	}

	state := &State{
		Tasks: []Task{mkTask("task-1"), mkTask("task-2"), mkTask("task-3")},
	}

	tests := []struct {
		name   string
		taskID string
		want   int
	}{
		{"found first", "task-1", 0},
		{"found middle", "task-2", 1},
		{"found last", "task-3", 2},
		{"not found", "task-99", -1},
		{"empty ID", "", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := state.FindTaskIndex(tt.taskID)
			if got != tt.want {
				t.Errorf("FindTaskIndex(%q) = %d, want %d", tt.taskID, got, tt.want)
			}
		})
	}

	// Empty state
	t.Run("empty state", func(t *testing.T) {
		empty := &State{}
		if got := empty.FindTaskIndex("task-1"); got != -1 {
			t.Errorf("FindTaskIndex on empty state = %d, want -1", got)
		}
	})
}

func TestAllPlannedTasksTerminal(t *testing.T) {
	now := time.Now().UTC()
	mkTask := func(id string, status TaskStatus) Task {
		return Task{ID: id, Status: status, Created: now, Priority: 1, Iteration: 1}
	}

	tests := []struct {
		name    string
		planned []string
		tasks   []Task
		want    bool
	}{
		{
			name:    "empty planned list",
			planned: []string{},
			tasks:   []Task{mkTask("task-1", TaskStatusMerged)},
			want:    false,
		},
		{
			name:    "all merged",
			planned: []string{"task-1", "task-2"},
			tasks:   []Task{mkTask("task-1", TaskStatusMerged), mkTask("task-2", TaskStatusMerged)},
			want:    true,
		},
		{
			name:    "mixed terminal states",
			planned: []string{"task-1", "task-2", "task-3"},
			tasks: []Task{
				mkTask("task-1", TaskStatusMerged),
				mkTask("task-2", TaskStatusAbandoned),
				mkTask("task-3", TaskStatusSuperseded),
			},
			want: true,
		},
		{
			name:    "one still in progress",
			planned: []string{"task-1", "task-2"},
			tasks:   []Task{mkTask("task-1", TaskStatusMerged), mkTask("task-2", TaskStatusImplementing)},
			want:    false,
		},
		{
			name:    "planned task missing from task list",
			planned: []string{"task-1", "task-2"},
			tasks:   []Task{mkTask("task-1", TaskStatusMerged)},
			want:    false,
		},
		{
			name:    "extra non-planned tasks don't affect result",
			planned: []string{"task-1"},
			tasks: []Task{
				mkTask("task-1", TaskStatusMerged),
				mkTask("task-2", TaskStatusImplementing),
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &State{
				Sprint: Sprint{Scope: SprintScope{Planned: tt.planned}},
				Tasks:  tt.tasks,
			}
			got := state.AllPlannedTasksTerminal()
			if got != tt.want {
				t.Errorf("AllPlannedTasksTerminal() = %v, want %v", got, tt.want)
			}
		})
	}
}
