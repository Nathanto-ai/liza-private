package observability

import (
	"testing"
)

func TestEventTypes_CrashAndExitDefined(t *testing.T) {
	t.Parallel()

	// These event types must exist for structured crash/exit reporting.
	requiredTypes := map[EventType]string{
		EventAgentExited:  "AGENT_EXITED",
		EventAgentCrashed: "AGENT_CRASHED",
		EventAgentAborted: "AGENT_ABORTED",
	}
	for et, expected := range requiredTypes {
		if string(et) != expected {
			t.Errorf("EventType %q has value %q, want %q", expected, string(et), expected)
		}
	}
}

func TestEventTypes_AllDefined(t *testing.T) {
	t.Parallel()

	// Regression: ensure all event types have non-empty string values.
	allEvents := []EventType{
		EventTaskCreated,
		EventTaskStatusChanged,
		EventFindingCreated,
		EventFindingResolved,
		EventBudgetExceeded,
		EventAnomalyDetected,
		EventSpecValidated,
		EventVerifyRun,
		EventAgentClaimed,
		EventAgentReleased,
		EventAgentExited,
		EventAgentCrashed,
		EventAgentAborted,
		EventRunSummary,
	}
	for _, et := range allEvents {
		if string(et) == "" {
			t.Errorf("EventType has empty string value")
		}
	}
}

func TestNewRunSummaryEvent_PassedTrue(t *testing.T) {
	t.Parallel()
	e := NewRunSummaryEvent(true, "all tasks done", []string{"/log.jsonl"})
	if e.Type != EventRunSummary {
		t.Errorf("type = %v, want %v", e.Type, EventRunSummary)
	}
	if e.Data["result"] != "PASS" {
		t.Errorf("result = %q, want PASS", e.Data["result"])
	}
	if e.Data["evidence_0"] != "/log.jsonl" {
		t.Errorf("evidence_0 = %q, want /log.jsonl", e.Data["evidence_0"])
	}
}

func TestNewRunSummaryEvent_PassedFalse(t *testing.T) {
	t.Parallel()
	e := NewRunSummaryEvent(false, "budget exceeded", nil)
	if e.Data["result"] != "FAIL" {
		t.Errorf("result = %q, want FAIL", e.Data["result"])
	}
}

func TestNewEvent_SetsTimestamp(t *testing.T) {
	t.Parallel()
	e := NewEvent(EventAgentCrashed, "test crash")
	if e.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
	if e.Type != EventAgentCrashed {
		t.Errorf("type = %v, want %v", e.Type, EventAgentCrashed)
	}
	if e.Message != "test crash" {
		t.Errorf("message = %q, want %q", e.Message, "test crash")
	}
}

func TestEvent_WithData(t *testing.T) {
	t.Parallel()
	e := NewEvent(EventAgentExited, "clean exit").
		WithAgent("coder-1").
		WithData("exit_code", "0")
	if e.AgentID != "coder-1" {
		t.Errorf("agent_id = %q, want coder-1", e.AgentID)
	}
	if e.Data["exit_code"] != "0" {
		t.Errorf("exit_code = %q, want 0", e.Data["exit_code"])
	}
}
