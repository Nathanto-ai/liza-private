// Package observability provides structured event logging for the Liza
// multi-agent system, writing JSONL events for traceability and debugging.
package observability

import (
	"strconv"
	"time"
)

// EventType classifies what happened.
type EventType string

const (
	EventTaskCreated       EventType = "TASK_CREATED"
	EventTaskStatusChanged EventType = "TASK_STATUS_CHANGED"
	EventFindingCreated    EventType = "FINDING_CREATED"
	EventFindingResolved   EventType = "FINDING_RESOLVED"
	EventBudgetExceeded    EventType = "BUDGET_EXCEEDED"
	EventAnomalyDetected   EventType = "ANOMALY_DETECTED"
	EventSpecValidated     EventType = "SPEC_VALIDATED"
	EventVerifyRun         EventType = "VERIFY_RUN"
	EventAgentClaimed      EventType = "AGENT_CLAIMED"
	EventAgentReleased     EventType = "AGENT_RELEASED"
	EventRunSummary        EventType = "RUN_SUMMARY"
)

// NewRunSummaryEvent creates a summary event emitted at the end of a run.
// passed indicates whether the run succeeded overall.
// cause describes why it ended (e.g., "all tasks done", "budget exceeded", "blocked").
// evidencePaths lists log/artifact files relevant to the outcome.
func NewRunSummaryEvent(passed bool, cause string, evidencePaths []string) Event {
	result := "PASS"
	if !passed {
		result = "FAIL"
	}
	e := NewEvent(EventRunSummary, cause).
		WithData("result", result).
		WithData("cause", cause)
	for i, p := range evidencePaths {
		e = e.WithData("evidence_"+strconv.Itoa(i), p)
	}
	return e
}

// Event is a structured log entry for the observability system.
type Event struct {
	Timestamp time.Time         `json:"timestamp"`
	Type      EventType         `json:"type"`
	AgentID   string            `json:"agent_id,omitempty"`
	TaskID    string            `json:"task_id,omitempty"`
	FindingID string            `json:"finding_id,omitempty"`
	Message   string            `json:"message"`
	Data      map[string]string `json:"data,omitempty"`
}

// NewEvent creates an event with the current UTC timestamp.
func NewEvent(eventType EventType, message string) Event {
	return Event{
		Timestamp: time.Now().UTC(),
		Type:      eventType,
		Message:   message,
	}
}

// WithAgent adds the agent ID to the event.
func (e Event) WithAgent(agentID string) Event {
	e.AgentID = agentID
	return e
}

// WithTask adds the task ID to the event.
func (e Event) WithTask(taskID string) Event {
	e.TaskID = taskID
	return e
}

// WithFinding adds the finding ID to the event.
func (e Event) WithFinding(findingID string) Event {
	e.FindingID = findingID
	return e
}

// WithData adds key-value metadata to the event.
func (e Event) WithData(key, value string) Event {
	if e.Data == nil {
		e.Data = make(map[string]string)
	}
	e.Data[key] = value
	return e
}
