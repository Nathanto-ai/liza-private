package observability

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestNewEvent(t *testing.T) {
	t.Parallel()

	e := NewEvent(EventTaskCreated, "task created")
	if e.Type != EventTaskCreated {
		t.Errorf("Type = %q, want %q", e.Type, EventTaskCreated)
	}
	if e.Message != "task created" {
		t.Errorf("Message = %q, want %q", e.Message, "task created")
	}
	if e.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
}

func TestEventBuilders(t *testing.T) {
	t.Parallel()

	e := NewEvent(EventFindingCreated, "finding").
		WithAgent("agent-1").
		WithTask("task-1").
		WithFinding("finding-1").
		WithData("severity", "HIGH")

	if e.AgentID != "agent-1" {
		t.Errorf("AgentID = %q", e.AgentID)
	}
	if e.TaskID != "task-1" {
		t.Errorf("TaskID = %q", e.TaskID)
	}
	if e.FindingID != "finding-1" {
		t.Errorf("FindingID = %q", e.FindingID)
	}
	if e.Data["severity"] != "HIGH" {
		t.Errorf("Data[severity] = %q", e.Data["severity"])
	}
}

func TestEmitter_EmitAndRead(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	emitter := NewEmitterWriter(&buf)

	events := []Event{
		NewEvent(EventTaskCreated, "task 1 created").WithTask("t-1"),
		NewEvent(EventFindingCreated, "finding for t-1").WithTask("t-1").WithFinding("f-1"),
		NewEvent(EventAnomalyDetected, "stagnation").WithAgent("agent-1"),
	}

	for _, e := range events {
		if err := emitter.Emit(e); err != nil {
			t.Fatalf("Emit failed: %v", err)
		}
	}

	// Read all events back
	result, err := ReadEventsFrom(&buf, EventFilter{})
	if err != nil {
		t.Fatalf("ReadEventsFrom failed: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 events, got %d", len(result))
	}

	if result[0].Type != EventTaskCreated {
		t.Errorf("result[0].Type = %q", result[0].Type)
	}
	if result[1].FindingID != "f-1" {
		t.Errorf("result[1].FindingID = %q", result[1].FindingID)
	}
	if result[2].AgentID != "agent-1" {
		t.Errorf("result[2].AgentID = %q", result[2].AgentID)
	}
}

func TestReadEvents_FilterByType(t *testing.T) {
	t.Parallel()

	jsonl := strings.Join([]string{
		`{"timestamp":"2024-01-01T00:00:00Z","type":"TASK_CREATED","message":"a"}`,
		`{"timestamp":"2024-01-01T00:00:01Z","type":"FINDING_CREATED","message":"b"}`,
		`{"timestamp":"2024-01-01T00:00:02Z","type":"TASK_CREATED","message":"c"}`,
	}, "\n")

	result, err := ReadEventsFrom(strings.NewReader(jsonl), EventFilter{Type: EventTaskCreated})
	if err != nil {
		t.Fatalf("ReadEventsFrom failed: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 events, got %d", len(result))
	}
}

func TestReadEvents_FilterByAgent(t *testing.T) {
	t.Parallel()

	jsonl := strings.Join([]string{
		`{"timestamp":"2024-01-01T00:00:00Z","type":"AGENT_CLAIMED","agent_id":"a1","message":"claimed"}`,
		`{"timestamp":"2024-01-01T00:00:01Z","type":"AGENT_CLAIMED","agent_id":"a2","message":"claimed"}`,
	}, "\n")

	result, err := ReadEventsFrom(strings.NewReader(jsonl), EventFilter{AgentID: "a1"})
	if err != nil {
		t.Fatalf("ReadEventsFrom failed: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result))
	}
	if result[0].AgentID != "a1" {
		t.Errorf("AgentID = %q", result[0].AgentID)
	}
}

func TestReadEvents_FilterByTask(t *testing.T) {
	t.Parallel()

	jsonl := strings.Join([]string{
		`{"timestamp":"2024-01-01T00:00:00Z","type":"TASK_STATUS_CHANGED","task_id":"t-1","message":"ready"}`,
		`{"timestamp":"2024-01-01T00:00:01Z","type":"TASK_STATUS_CHANGED","task_id":"t-2","message":"ready"}`,
		`{"timestamp":"2024-01-01T00:00:02Z","type":"VERIFY_RUN","task_id":"t-1","message":"pass"}`,
	}, "\n")

	result, err := ReadEventsFrom(strings.NewReader(jsonl), EventFilter{TaskID: "t-1"})
	if err != nil {
		t.Fatalf("ReadEventsFrom failed: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 events, got %d", len(result))
	}
}

func TestReadEvents_MalformedLinesSkipped(t *testing.T) {
	t.Parallel()

	jsonl := strings.Join([]string{
		`{"timestamp":"2024-01-01T00:00:00Z","type":"TASK_CREATED","message":"ok"}`,
		`this is not json`,
		`{"timestamp":"2024-01-01T00:00:02Z","type":"TASK_CREATED","message":"also ok"}`,
	}, "\n")

	result, err := ReadEventsFrom(strings.NewReader(jsonl), EventFilter{})
	if err != nil {
		t.Fatalf("ReadEventsFrom failed: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 events (malformed skipped), got %d", len(result))
	}
}

func TestEmitter_JSONLFormat(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	emitter := NewEmitterWriter(&buf)

	e := Event{
		Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Type:      EventBudgetExceeded,
		AgentID:   "agent-x",
		Message:   "budget exceeded",
	}

	if err := emitter.Emit(e); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	output := buf.String()
	if !strings.HasSuffix(output, "\n") {
		t.Error("JSONL line should end with newline")
	}
	if strings.Count(output, "\n") != 1 {
		t.Errorf("expected exactly one line, got %d", strings.Count(output, "\n"))
	}
	if !strings.Contains(output, `"BUDGET_EXCEEDED"`) {
		t.Errorf("output should contain event type: %s", output)
	}
}

func TestNewRunSummaryEvent_Pass(t *testing.T) {
	t.Parallel()

	e := NewRunSummaryEvent(true, "all tasks done", []string{"/tmp/log.jsonl"})
	if e.Type != EventRunSummary {
		t.Errorf("Type = %q, want %q", e.Type, EventRunSummary)
	}
	if e.Data["result"] != "PASS" {
		t.Errorf("result = %q, want PASS", e.Data["result"])
	}
	if e.Data["cause"] != "all tasks done" {
		t.Errorf("cause = %q", e.Data["cause"])
	}
	if e.Data["evidence_0"] != "/tmp/log.jsonl" {
		t.Errorf("evidence_0 = %q", e.Data["evidence_0"])
	}
}

func TestNewRunSummaryEvent_Fail(t *testing.T) {
	t.Parallel()

	e := NewRunSummaryEvent(false, "blocked", []string{"/tmp/a.log", "/tmp/b.log"})
	if e.Data["result"] != "FAIL" {
		t.Errorf("result = %q, want FAIL", e.Data["result"])
	}
	if e.Data["evidence_0"] != "/tmp/a.log" {
		t.Errorf("evidence_0 = %q", e.Data["evidence_0"])
	}
	if e.Data["evidence_1"] != "/tmp/b.log" {
		t.Errorf("evidence_1 = %q", e.Data["evidence_1"])
	}
}

func TestNewRunSummaryEvent_NoEvidence(t *testing.T) {
	t.Parallel()

	e := NewRunSummaryEvent(true, "done", nil)
	if e.Data["result"] != "PASS" {
		t.Errorf("result = %q, want PASS", e.Data["result"])
	}
	// Should have cause + result but no evidence keys
	if len(e.Data) != 2 {
		t.Errorf("expected 2 data entries, got %d: %v", len(e.Data), e.Data)
	}
}

func TestNewRunSummaryEvent_EmitAndRead(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	emitter := NewEmitterWriter(&buf)

	e := NewRunSummaryEvent(false, "budget exceeded", []string{"/tmp/events.jsonl"})
	if err := emitter.Emit(e); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	result, err := ReadEventsFrom(&buf, EventFilter{Type: EventRunSummary})
	if err != nil {
		t.Fatalf("ReadEventsFrom: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result))
	}
	if result[0].Data["result"] != "FAIL" {
		t.Errorf("result = %q", result[0].Data["result"])
	}
}
