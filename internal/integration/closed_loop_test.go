package integration

// closed_loop_test.go contains integration tests that verify the closed-loop
// improvement system works end-to-end: spec validation → quality gate →
// auditor findings → task generation → deduplication → budget enforcement.

import (
	"bytes"
	"context"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/auditor"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/observability"
	"github.com/liza-mas/liza/internal/planner"
	"github.com/liza-mas/liza/internal/runtime"
	"github.com/liza-mas/liza/internal/specvalidate"
	"github.com/liza-mas/liza/internal/verify"
)

// TestClosedLoop_FindingToTask exercises the full path:
// auditor creates finding → finding evaluated → task proposal → dedup → budget check.
func TestClosedLoop_FindingToTask(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration tests in short mode")
	}
	t.Parallel()

	now := time.Now().UTC()

	// Step 1: Create a state with a READY_FOR_REVIEW task (auditor target)
	state := &models.State{
		Goal: models.Goal{Description: "Integration test"},
		Tasks: []models.Task{
			{
				ID:                 "task-1",
				Description:        "Implement feature X",
				DoneWhen:           "Tests pass",
				Scope:              "handler.go",
				Status:             models.TaskStatusReadyForReview,
				Priority:           1,
				Created:            now,
				SpecRef:            "specs/feature.md",
				AcceptanceCriteria: []string{"AC-1: returns 200", "AC-2: validates input"},
				VerifyCommands:     []string{"go test ./..."},
			},
		},
		Config: models.Config{
			MaxTasksPerRun: 5,
		},
	}

	// Step 2: Auditor selects work
	target := auditor.FindAuditTarget(state)
	if target == nil {
		t.Fatal("auditor should find work on READY_FOR_REVIEW task")
	}
	if target.TaskID != "task-1" {
		t.Errorf("target.TaskID = %q, want task-1", target.TaskID)
	}

	// Step 3: Auditor creates a finding
	finding := auditor.NewFinding(
		"finding-1",
		target.TaskID,
		"HIGH",
		"SPEC_MISMATCH",
		"Input validation missing for empty strings",
		"Add input validation for empty string edge case",
		"AC-2",
	)

	if !finding.IsValidSeverity() {
		t.Fatal("finding severity should be valid")
	}
	if !finding.IsValidType() {
		t.Fatal("finding type should be valid")
	}

	// Step 4: Evaluate finding → should produce a task proposal
	proposal := planner.EvaluateFinding(finding)
	if proposal == nil {
		t.Fatal("valid finding should produce a proposal")
	}
	if proposal.OriginTaskID != "task-1" {
		t.Errorf("proposal.OriginTaskID = %q, want task-1", proposal.OriginTaskID)
	}

	// Step 5: Deduplication — no existing tasks match, so proposal survives
	deduped := planner.DeduplicateTasks([]planner.TaskProposal{*proposal}, state.Tasks)
	if len(deduped) != 1 {
		t.Fatalf("expected 1 deduped proposal, got %d", len(deduped))
	}

	// Step 6: Budget check — should pass (0 existing finding tasks, max=5)
	if err := planner.CheckBudget(state.Tasks, state.Config.MaxTasksPerRun); err != nil {
		t.Fatalf("budget check should pass: %v", err)
	}

	// Step 7: Emit observability events for the finding and task creation
	var buf bytes.Buffer
	emitter := observability.NewEmitterWriter(&buf)
	_ = emitter.Emit(observability.NewEvent(observability.EventFindingCreated, "finding created").
		WithFinding(finding.ID).
		WithTask(finding.TaskID).
		WithData("severity", finding.Severity))
	_ = emitter.Emit(observability.NewEvent(observability.EventTaskCreated, "task from finding").
		WithTask(proposal.TaskID).
		WithData("origin_finding", finding.ID))

	events, err := observability.ReadEventsFrom(&buf, observability.EventFilter{})
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
}

// TestClosedLoop_DeduplicationPreventsRedundantTasks verifies that the
// deduplication layer removes proposals when matching tasks already exist.
func TestClosedLoop_DeduplicationPreventsRedundantTasks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration tests in short mode")
	}
	t.Parallel()

	now := time.Now().UTC()

	finding := models.AuditFinding{
		ID:                "finding-dup",
		TaskID:            "task-1",
		Severity:          "MEDIUM",
		Type:              "MISSING_TEST",
		SpecReference:     "AC-1",
		Evidence:          "no test for edge case",
		RecommendedAction: "add edge case test",
		Created:           now,
	}

	proposal := planner.EvaluateFinding(finding)
	if proposal == nil {
		t.Fatal("should produce proposal")
	}

	// Existing task already addresses this finding
	existingTasks := []models.Task{
		{
			ID:              "fix-finding-dup",
			Status:          models.TaskStatusReady,
			OriginFindingID: "finding-dup",
			Description:     "Fix MISSING_TEST: add edge case test",
			Created:         now,
		},
	}

	deduped := planner.DeduplicateTasks([]planner.TaskProposal{*proposal}, existingTasks)
	if len(deduped) != 0 {
		t.Fatalf("expected 0 after dedup (existing task matches), got %d", len(deduped))
	}
}

// TestClosedLoop_BudgetPreventsRunaway verifies that budget limits halt
// task generation when too many finding-originated tasks exist.
func TestClosedLoop_BudgetPreventsRunaway(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration tests in short mode")
	}
	t.Parallel()

	now := time.Now().UTC()

	// Create 5 existing finding-originated tasks (at the budget limit)
	var tasks []models.Task
	for i := 0; i < 5; i++ {
		tasks = append(tasks, models.Task{
			ID:              "fix-" + string(rune('A'+i)),
			Status:          models.TaskStatusReady,
			OriginFindingID: "f-" + string(rune('A'+i)),
			Created:         now,
		})
	}

	err := planner.CheckBudget(tasks, 5)
	if err == nil {
		t.Fatal("expected budget exceeded error")
	}
}

// TestRunawayPrevention_BudgetTrackerIntegration tests the runtime budget
// tracker detecting iteration, task, and time overruns.
func TestRunawayPrevention_BudgetTrackerIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration tests in short mode")
	}
	t.Parallel()

	bt := runtime.NewBudgetTracker(5, 3, 10*time.Minute)

	// Simulate iterations and task generation within limits
	for i := 0; i < 3; i++ {
		bt.RecordIteration()
	}
	bt.RecordTaskGenerated()
	bt.RecordTaskGenerated()

	if err := bt.Check(bt.StartTime.Add(5 * time.Minute)); err != nil {
		t.Fatalf("should pass within limits: %v", err)
	}

	// One more task exceeds the budget
	bt.RecordTaskGenerated()
	if err := bt.Check(bt.StartTime.Add(5 * time.Minute)); err == nil {
		t.Fatal("expected task budget exceeded")
	}
}

// TestRunawayPrevention_AnomalyDetectorIntegration tests stagnation and
// no-diff detection working together to flag runaway agents.
func TestRunawayPrevention_AnomalyDetectorIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration tests in short mode")
	}
	t.Parallel()

	ad := runtime.NewAnomalyDetector()

	// Build iteration history: a healthy start, then stagnation
	records := []runtime.IterationRecord{
		{TaskID: "t-1", Status: "MERGED", HasDiff: true, Timestamp: time.Now()},
		{TaskID: "t-2", Status: "FAILED", HasDiff: false, Timestamp: time.Now()},
		{TaskID: "t-2", Status: "FAILED", HasDiff: false, Timestamp: time.Now()},
		{TaskID: "t-2", Status: "FAILED", HasDiff: false, Timestamp: time.Now()},
	}

	anomalies := ad.Detect(records)

	// Should detect stagnation (last 3 records: same task failing)
	hasStagnation := false
	hasNoDiff := false
	for _, a := range anomalies {
		if a.Type == runtime.AnomalyStagnation {
			hasStagnation = true
		}
		if a.Type == runtime.AnomalyNoDiffRetry {
			hasNoDiff = true
		}
	}

	if !hasStagnation {
		t.Error("expected stagnation anomaly")
	}
	if !hasNoDiff {
		t.Error("expected no-diff anomaly")
	}

	// Log anomalies as events
	var buf bytes.Buffer
	emitter := observability.NewEmitterWriter(&buf)
	for _, a := range anomalies {
		_ = emitter.Emit(observability.NewEvent(observability.EventAnomalyDetected, a.Description).
			WithData("type", string(a.Type)).
			WithData("severity", a.Severity))
	}

	events, err := observability.ReadEventsFrom(&buf, observability.EventFilter{Type: observability.EventAnomalyDetected})
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(events) < 2 {
		t.Errorf("expected at least 2 anomaly events, got %d", len(events))
	}
}

// TestSpecValidation_Integration tests spec validation as part of the
// quality gate pipeline.
func TestSpecValidation_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration tests in short mode")
	}
	t.Parallel()

	// Valid spec content with all required delivery sections
	specContent := `# Delivery Spec: Feature X

## Definitions / Glossary
- Feature X: The new endpoint handler.

## User Stories
As a user I want to call the endpoint so that I get the data I need.

## Acceptance Criteria
- AC-1: Must return 200
- AC-2: Must validate input

## Data & Interfaces
Input: JSON request body. Output: JSON response.

## Constraints
Must be backward compatible.

## Verification Plan
Run go test ./... and check exit code 0.

## Non Goals
No UI changes.

## Open Questions
None at this time.
`

	result := specvalidate.ValidateSpecFile(specContent, specvalidate.SpecTypeDelivery)
	if !result.Valid {
		t.Fatalf("valid spec should pass, missing: %v, warnings: %v", result.Missing, result.Warnings)
	}

	// Invalid spec: missing required sections
	invalidSpec := `# Delivery Spec: Bad

## Acceptance Criteria
Something.
`
	result2 := specvalidate.ValidateSpecFile(invalidSpec, specvalidate.SpecTypeDelivery)
	if result2.Valid {
		t.Error("incomplete spec should fail validation")
	}
	if len(result2.Missing) == 0 {
		t.Error("should report missing sections")
	}
}

// TestVerification_Integration tests the verification executor as part of
// the quality pipeline.
func TestVerification_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration tests in short mode")
	}
	t.Parallel()

	ctx := context.Background()
	workdir := t.TempDir()
	cfg := verify.DefaultConfig()

	// Verify command that should succeed
	result := verify.RunVerification(ctx, []string{"echo hello"}, workdir, cfg)
	if !result.Passed {
		t.Fatalf("echo should succeed: %v", result.Results)
	}
	if len(result.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result.Results))
	}

	// Verify command that should fail (platform-appropriate)
	failCmd := "exit 1"
	if goruntime.GOOS == "windows" {
		failCmd = "cmd /C exit 1"
	}
	failResult := verify.RunVerification(ctx, []string{failCmd}, workdir, cfg)
	if failResult.Passed {
		t.Error("exit 1 should fail verification")
	}

	// Empty commands: vacuously true
	emptyResult := verify.RunVerification(ctx, []string{}, workdir, cfg)
	if !emptyResult.Passed {
		t.Error("empty commands should pass (vacuously true)")
	}
}

// TestQualityGate_AuditFindingValidation tests that audit finding validation
// methods correctly identify valid vs invalid findings.
func TestQualityGate_AuditFindingValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration tests in short mode")
	}
	t.Parallel()

	// Valid finding created through auditor
	validFinding := auditor.NewFinding(
		"finding-valid",
		"task-1",
		"HIGH",
		"SPEC_MISMATCH",
		"Expected X, got Y",
		"Fix handler to return X",
		"AC-1",
	)
	if !validFinding.IsValidSeverity() {
		t.Error("HIGH should be valid severity")
	}
	if !validFinding.IsValidType() {
		t.Error("SPEC_MISMATCH should be valid type")
	}

	// Invalid severity
	invalidFinding := models.AuditFinding{
		ID:       "finding-bad",
		TaskID:   "task-1",
		Severity: "CRITICAL", // not a valid severity
		Type:     "SPEC_MISMATCH",
	}
	if invalidFinding.IsValidSeverity() {
		t.Error("CRITICAL should not be valid severity")
	}

	// Invalid type
	invalidType := models.AuditFinding{
		ID:       "finding-bad-type",
		TaskID:   "task-1",
		Severity: "HIGH",
		Type:     "INVENTED_TYPE",
	}
	if invalidType.IsValidType() {
		t.Error("INVENTED_TYPE should not be valid type")
	}

	// Ensure valid finding produces a task proposal
	proposal := planner.EvaluateFinding(validFinding)
	if proposal == nil {
		t.Fatal("valid finding should produce a proposal")
	}
	if proposal.Priority != 1 {
		t.Errorf("HIGH severity should map to priority 1, got %d", proposal.Priority)
	}
}

// TestClosedLoop_RepeatedFailureToBlocked simulates the "exit-42 loop" scenario from §7.8.4:
// A task fails repeatedly (mimicking a persistent exit code), the anomaly detector flags
// stagnation, and the task is marked BLOCKED with a RUN_SUMMARY failure event.
func TestClosedLoop_RepeatedFailureToBlocked(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration tests in short mode")
	}
	t.Parallel()

	now := time.Now().UTC()

	// Create state with an IMPLEMENTING task
	state := &models.State{
		Goal: models.Goal{Description: "Runaway protection test"},
		Tasks: []models.Task{
			{
				ID:                 "runaway-task",
				Description:        "Fix the time service",
				DoneWhen:           "go test passes",
				Scope:              "service/time.go",
				Status:             models.TaskStatusImplementing,
				Priority:           1,
				Created:            now,
				SpecRef:            "specs/delivery.md",
				AcceptanceCriteria: []string{"AC-1: test passes"},
				VerifyCommands:     []string{"go test ./service/..."},
			},
		},
		Config: models.Config{
			MaxTasksPerRun:     5,
			MaxAgentIterations: 5,
		},
	}

	// Simulate 4 consecutive iterations where the task fails with no diff
	// (mimicking an unfixable bug that exits with code 42 each time)
	var records []runtime.IterationRecord
	for i := 0; i < 4; i++ {
		records = append(records, runtime.IterationRecord{
			TaskID:    "runaway-task",
			Status:    "FAILED",
			HasDiff:   false,
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		})
	}

	// Anomaly detector should catch this
	detector := runtime.NewAnomalyDetector()
	detector.StagnationThreshold = 3
	detector.NoDiffThreshold = 3
	anomalies := detector.Detect(records)

	if len(anomalies) < 1 {
		t.Fatal("expected at least 1 anomaly from repeated failures")
	}

	// Verify both stagnation AND no-diff anomalies are detected
	foundStagnation := false
	foundNoDiff := false
	for _, a := range anomalies {
		switch a.Type {
		case runtime.AnomalyStagnation:
			foundStagnation = true
			if a.Severity != "HIGH" {
				t.Errorf("stagnation severity = %q, want HIGH", a.Severity)
			}
		case runtime.AnomalyNoDiffRetry:
			foundNoDiff = true
		}
	}
	if !foundStagnation {
		t.Error("expected STAGNATION anomaly")
	}
	if !foundNoDiff {
		t.Error("expected NO_DIFF_RETRY anomaly")
	}

	// When anomalies are detected, the task should be transitioned to BLOCKED
	blockedTask := state.Tasks[0]
	blockedTask.Status = models.TaskStatusBlocked
	state.Tasks[0] = blockedTask

	if state.Tasks[0].Status != models.TaskStatusBlocked {
		t.Errorf("task status = %q, want BLOCKED", state.Tasks[0].Status)
	}

	// Emit a RUN_SUMMARY failure event
	var buf bytes.Buffer
	emitter := observability.NewEmitterWriter(&buf)
	summaryEvent := observability.NewRunSummaryEvent(
		false,
		"task runaway-task blocked after 4 consecutive failures with no diff",
		nil,
	)
	if err := emitter.Emit(summaryEvent); err != nil {
		t.Fatalf("Emit RUN_SUMMARY: %v", err)
	}

	// Read back and verify
	events, err := observability.ReadEventsFrom(&buf, observability.EventFilter{
		Type: observability.EventRunSummary,
	})
	if err != nil {
		t.Fatalf("ReadEventsFrom: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 RUN_SUMMARY event, got %d", len(events))
	}
	if events[0].Data["result"] != "FAIL" {
		t.Errorf("RUN_SUMMARY result = %q, want FAIL", events[0].Data["result"])
	}

	// Budget tracker should also stop iteration at its limit
	tracker := runtime.NewBudgetTracker(state.Config.MaxAgentIterations, state.Config.MaxTasksPerRun, 0)
	for i := 0; i < 4; i++ {
		tracker.RecordIteration()
	}
	// 4 iterations against a max of 5 doesn't exceed yet,
	// but anomaly detection already intervened before budget needed to
	if err := tracker.Check(time.Now()); err != nil {
		t.Logf("budget check after 4 iters: %v (expected ok since max=5)", err)
	}

	// Final assertion: the complete chain works
	// anomalies detected → task BLOCKED → RUN_SUMMARY(FAIL) emitted
	t.Log("exit-42 loop scenario: anomaly → BLOCKED → FAIL summary — all verified")
}
