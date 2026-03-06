//go:build integration

package integration

// integration_audit_verify_test.go contains integration tests gated behind
// the "integration" build tag. Run with: go test -tags=integration ./internal/integration/...
//
// These tests exercise:
// - Auditor finding submission via ops layer (end-to-end state persistence)
// - Post-submission verification pipeline
// - Observability event emission and readback
// - Full audit → finding → task proposal → verify cycle

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/auditor"
	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/observability"
	"github.com/liza-mas/liza/internal/ops"
	"github.com/liza-mas/liza/internal/planner"
	"github.com/liza-mas/liza/internal/testhelpers"
	"github.com/liza-mas/liza/internal/verify"
)

// TestIntegration_AuditFindingSubmission exercises the full ops-layer
// audit finding submission and verifies state persistence.
func TestIntegration_AuditFindingSubmission(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	now := time.Now().UTC()
	state := &models.State{
		Goal: models.Goal{Description: "Integration audit test"},
		Tasks: []models.Task{
			{
				ID:                 "task-audit-1",
				Description:        "Implement login handler",
				DoneWhen:           "Handler returns 200 for valid credentials",
				Scope:              "auth/handler.go",
				Status:             models.TaskStatusReadyForReview,
				Priority:           1,
				Created:            now,
				SpecRef:            "specs/delivery-auth.md",
				AcceptanceCriteria: []string{"AC-1: valid credentials return 200", "AC-2: invalid credentials return 401"},
				VerifyCommands:     []string{"go test ./auth/..."},
			},
			{
				ID:          "task-audit-2",
				Description: "Implement rate limiter",
				DoneWhen:    "Rate limiter blocks excess requests",
				Scope:       "middleware/ratelimit.go",
				Status:      models.TaskStatusReady,
				Priority:    2,
				Created:     now,
			},
		},
	}
	testhelpers.WriteInitialState(t, stateFile, state)

	// Step 1: Submit a finding via ops layer
	result, err := ops.SubmitAuditFinding(tmpDir, ops.AuditFindingInput{
		FindingID:         "finding-integ-1",
		TaskID:            "task-audit-1",
		Severity:          "HIGH",
		Type:              "MISSING_TEST",
		Evidence:          "No test covers the 401 invalid credentials path",
		RecommendedAction: "Add test for AC-2 rejection case",
		SpecReference:     "AC-2",
	})
	if err != nil {
		t.Fatalf("SubmitAuditFinding failed: %v", err)
	}
	if result.FindingID != "finding-integ-1" {
		t.Errorf("result.FindingID = %q, want finding-integ-1", result.FindingID)
	}

	// Step 2: Verify finding persisted in state
	bb := db.For(stateFile)
	updated, err := bb.Read()
	if err != nil {
		t.Fatalf("Read state: %v", err)
	}
	if len(updated.AuditFindings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(updated.AuditFindings))
	}
	f := updated.AuditFindings[0]
	if f.Severity != "HIGH" || f.Type != "MISSING_TEST" {
		t.Errorf("finding mismatch: severity=%s type=%s", f.Severity, f.Type)
	}

	// Step 3: Submit a second finding, verify both persist
	_, err = ops.SubmitAuditFinding(tmpDir, ops.AuditFindingInput{
		FindingID: "finding-integ-2",
		TaskID:    "task-audit-1",
		Severity:  "MEDIUM",
		Type:      "QUALITY_ISSUE",
		Evidence:  "Error messages use generic 'internal error' instead of specific codes",
	})
	if err != nil {
		t.Fatalf("Second SubmitAuditFinding failed: %v", err)
	}

	updated, _ = bb.Read()
	if len(updated.AuditFindings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(updated.AuditFindings))
	}

	// Step 4: Duplicate finding ID is rejected
	_, err = ops.SubmitAuditFinding(tmpDir, ops.AuditFindingInput{
		FindingID: "finding-integ-1",
		TaskID:    "task-audit-1",
		Severity:  "LOW",
		Type:      "QUALITY_ISSUE",
		Evidence:  "duplicate attempt",
	})
	if err == nil {
		t.Fatal("expected error for duplicate finding ID")
	}
}

// TestIntegration_AuditToVerifyPipeline tests the full cycle:
// auditor finds work → creates finding → planner evaluates → verify runs.
func TestIntegration_AuditToVerifyPipeline(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	state := &models.State{
		Goal: models.Goal{Description: "Pipeline test"},
		Tasks: []models.Task{
			{
				ID:                 "task-pipeline-1",
				Description:        "Add input validation",
				DoneWhen:           "Validation rejects empty strings",
				Scope:              "handlers/validate.go",
				Status:             models.TaskStatusReadyForReview,
				Priority:           1,
				Created:            now,
				SpecRef:            "specs/delivery.md",
				AcceptanceCriteria: []string{"AC-1: empty strings rejected"},
				VerifyCommands:     []string{"echo PASS"},
			},
		},
		Config: models.Config{MaxTasksPerRun: 10},
	}

	// Step 1: Auditor selects target
	target := auditor.FindAuditTarget(state)
	if target == nil {
		t.Fatal("auditor should find READY_FOR_REVIEW task")
	}
	if target.TaskID != "task-pipeline-1" {
		t.Errorf("target.TaskID = %q, want task-pipeline-1", target.TaskID)
	}
	if target.Phase != auditor.AuditPhasePostExecution {
		t.Errorf("phase = %q, want post_execution", target.Phase)
	}

	// Step 2: Auditor creates finding
	finding := auditor.NewFinding(
		"finding-pipe-1",
		target.TaskID,
		"HIGH",
		"MISSING_EDGE_CASE",
		"No validation for whitespace-only strings",
		"Add whitespace-only test case",
		"AC-1",
	)
	if !finding.IsValidSeverity() || !finding.IsValidType() {
		t.Fatal("finding should be valid")
	}

	// Step 3: Planner evaluates finding → produces task proposal
	proposal := planner.EvaluateFinding(finding)
	if proposal == nil {
		t.Fatal("valid HIGH finding should produce a proposal")
	}
	if proposal.OriginTaskID != "task-pipeline-1" {
		t.Errorf("proposal.OriginTaskID = %q, want task-pipeline-1", proposal.OriginTaskID)
	}

	// Step 4: Dedup passes (no existing matching task)
	deduped := planner.DeduplicateTasks([]planner.TaskProposal{*proposal}, state.Tasks)
	if len(deduped) != 1 {
		t.Fatalf("expected 1 after dedup, got %d", len(deduped))
	}

	// Step 5: Budget check passes
	if err := planner.CheckBudget(state.Tasks, state.Config.MaxTasksPerRun); err != nil {
		t.Fatalf("budget check should pass: %v", err)
	}

	// Step 6: Run verification on the original task
	ctx := context.Background()
	workdir := t.TempDir()
	cfg := verify.DefaultConfig()
	vResult := verify.RunVerification(ctx, state.Tasks[0].VerifyCommands, workdir, cfg)
	if !vResult.Passed {
		t.Errorf("verification should pass (echo PASS)")
	}

	// Step 7: Emit observability events for the full pipeline
	var buf bytes.Buffer
	emitter := observability.NewEmitterWriter(&buf)
	_ = emitter.Emit(observability.NewEvent(observability.EventFindingCreated, "audit finding").
		WithFinding(finding.ID).
		WithTask(finding.TaskID).
		WithData("severity", finding.Severity))
	_ = emitter.Emit(observability.NewEvent(observability.EventTaskCreated, "task from audit finding").
		WithTask(proposal.TaskID).
		WithData("origin_finding", finding.ID))
	_ = emitter.Emit(observability.NewEvent(observability.EventVerifyRun, "verification passed").
		WithTask(target.TaskID).
		WithData("result", "PASS"))

	events, err := observability.ReadEventsFrom(&buf, observability.EventFilter{})
	if err != nil {
		t.Fatalf("ReadEventsFrom: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Type != observability.EventFindingCreated {
		t.Errorf("event[0].Type = %s, want FINDING_CREATED", events[0].Type)
	}
	if events[1].Type != observability.EventTaskCreated {
		t.Errorf("event[1].Type = %s, want TASK_CREATED", events[1].Type)
	}
	if events[2].Type != observability.EventVerifyRun {
		t.Errorf("event[2].Type = %s, want VERIFY_RUN", events[2].Type)
	}
}

// TestIntegration_VerificationFailureFlow tests that verification failure
// is correctly captured and reported.
func TestIntegration_VerificationFailureFlow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workdir := t.TempDir()
	cfg := verify.DefaultConfig()

	// Failing verification command
	result := verify.RunVerification(ctx, []string{"exit 1"}, workdir, cfg)
	if result.Passed {
		t.Fatal("verification should fail on 'exit 1'")
	}
	if len(result.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result.Results))
	}
	if result.Results[0].ExitCode == 0 {
		t.Error("exit code should be non-zero")
	}

	// Emit FAIL event
	var buf bytes.Buffer
	emitter := observability.NewEmitterWriter(&buf)
	_ = emitter.Emit(observability.NewEvent(observability.EventVerifyRun, "verification failed").
		WithTask("task-fail-1").
		WithData("result", "FAIL").
		WithData("exit_code", "1"))

	events, err := observability.ReadEventsFrom(&buf, observability.EventFilter{Type: observability.EventVerifyRun})
	if err != nil {
		t.Fatalf("ReadEventsFrom: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Data["result"] != "FAIL" {
		t.Errorf("event result = %q, want FAIL", events[0].Data["result"])
	}
}

// TestIntegration_ObservabilityEventRoundtrip tests that events written
// by the emitter can be read back correctly with filtering.
func TestIntegration_ObservabilityEventRoundtrip(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	emitter := observability.NewEmitterWriter(&buf)

	// Emit a variety of events
	_ = emitter.Emit(observability.NewEvent(observability.EventAgentClaimed, "agent started").
		WithAgent("coder-1").
		WithData("role", "coder"))
	_ = emitter.Emit(observability.NewEvent(observability.EventTaskStatusChanged, "task claimed").
		WithAgent("coder-1").
		WithTask("task-1").
		WithData("new_status", "IMPLEMENTING"))
	_ = emitter.Emit(observability.NewEvent(observability.EventVerifyRun, "verify passed").
		WithTask("task-1").
		WithData("result", "PASS"))
	_ = emitter.Emit(observability.NewEvent(observability.EventAgentReleased, "agent stopped").
		WithAgent("coder-1"))

	// Read all events
	all, err := observability.ReadEventsFrom(&buf, observability.EventFilter{})
	if err != nil {
		t.Fatalf("ReadEventsFrom: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("expected 4 events, got %d", len(all))
	}

	// Verify event order and types
	expectedTypes := []observability.EventType{
		observability.EventAgentClaimed,
		observability.EventTaskStatusChanged,
		observability.EventVerifyRun,
		observability.EventAgentReleased,
	}
	for i, expected := range expectedTypes {
		if all[i].Type != expected {
			t.Errorf("event[%d].Type = %s, want %s", i, all[i].Type, expected)
		}
	}
}

// TestIntegration_MultipleAuditTargetPriority verifies that the auditor
// prioritizes READY_FOR_REVIEW tasks over READY tasks.
func TestIntegration_MultipleAuditTargetPriority(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	state := &models.State{
		Tasks: []models.Task{
			{
				ID:          "task-ready",
				Description: "A ready task",
				Status:      models.TaskStatusReady,
				Priority:    1,
				Created:     now,
			},
			{
				ID:          "task-rfr",
				Description: "A reviewed task",
				Status:      models.TaskStatusReadyForReview,
				Priority:    2,
				Created:     now,
			},
		},
	}

	// Post-execution (READY_FOR_REVIEW) should take priority
	target := auditor.FindAuditTarget(state)
	if target == nil {
		t.Fatal("should find audit target")
	}
	if target.TaskID != "task-rfr" {
		t.Errorf("target.TaskID = %q, want task-rfr (READY_FOR_REVIEW priority)", target.TaskID)
	}
	if target.Phase != auditor.AuditPhasePostExecution {
		t.Errorf("phase = %q, want post_execution", target.Phase)
	}

	// Mark RFR task as audited
	state.AuditFindings = append(state.AuditFindings, models.AuditFinding{
		ID:       "f-rfr",
		TaskID:   "task-rfr",
		Severity: "LOW",
		Type:     "QUALITY_ISSUE",
		Evidence: "already audited",
		Created:  now,
	})

	// Now READY task should be selected
	target = auditor.FindAuditTarget(state)
	if target == nil {
		t.Fatal("should find READY task after RFR is audited")
	}
	if target.TaskID != "task-ready" {
		t.Errorf("target.TaskID = %q, want task-ready", target.TaskID)
	}
	if target.Phase != auditor.AuditPhasePreExecution {
		t.Errorf("phase = %q, want pre_execution", target.Phase)
	}
}
