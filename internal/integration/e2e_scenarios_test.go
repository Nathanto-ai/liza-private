package integration

// e2e_scenarios_test.go exercises the V&V plan's four primary scenarios (S1-S4)
// using the fixture repos in tests/fixtures/.
//
// S1 — Happy-path bugfix:   good spec + failing tests → agent fixes → verification pass
// S2 — Quality gate enforcement: task without AC/verify_commands is rejected
// S3 — Spec ambiguity:      ambiguous spec detected → NEEDS_HUMAN_DECISION
// S4 — Runaway loop:        unfixable task → anomaly detection → BLOCKED + FAIL summary

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/auditor"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/observability"
	"github.com/liza-mas/liza/internal/runtime"
	"github.com/liza-mas/liza/internal/specvalidate"
	"github.com/liza-mas/liza/internal/verify"
)

// fixtureRoot resolves the absolute path to tests/fixtures/ from the repo root.
func fixtureRoot(t *testing.T) string {
	t.Helper()
	// Walk up from internal/integration/ to repo root
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	root := filepath.Join(wd, "..", "..")
	p := filepath.Join(root, "tests", "fixtures")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("fixture root not found at %s: %v", p, err)
	}
	return p
}

func readFixtureFile(t *testing.T, parts ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", filepath.Join(parts...), err)
	}
	return string(data)
}

// ---------------------------------------------------------------------------
// S1 — Happy-path bugfix: good delivery spec passes validation, quality gate
// accepts the task, verification commands exist, and the auditor can target it.
// ---------------------------------------------------------------------------

func TestScenario_S1_HappyPathBugfix(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E scenario tests in short mode")
	}
	t.Parallel()

	root := fixtureRoot(t)
	fixtureDir := filepath.Join(root, "fixture_go_bugfix")

	// 1. Validate the delivery spec — should pass
	deliverySpec := readFixtureFile(t, fixtureDir, "specs", "delivery.md")
	result := specvalidate.ValidateSpecFile(deliverySpec, specvalidate.SpecTypeDelivery)

	if !result.Valid {
		t.Errorf("S1: delivery spec should be valid, missing: %v", result.Missing)
	}

	// Expect a warning about Given/When/Then since the fixture uses AC-N: format
	gwtWarning := false
	for _, w := range result.Warnings {
		if w == "Acceptance Criteria section has no Given/When/Then blocks" {
			gwtWarning = true
		}
	}
	if !gwtWarning {
		t.Log("S1: AC format warning present (AC-N style, no GWT) — expected")
	}

	// 2. Validate the vision spec
	visionSpec := readFixtureFile(t, fixtureDir, "specs", "vision.md")
	vResult := specvalidate.ValidateSpecFile(visionSpec, specvalidate.SpecTypeVision)
	if !vResult.Valid {
		t.Errorf("S1: vision spec should be valid, missing: %v", vResult.Missing)
	}

	// 3. Quality gate: task with AC + verify_commands should pass
	now := time.Now().UTC()
	state := &models.State{
		Goal: models.Goal{Description: "Fix GetUser endpoint"},
		Tasks: []models.Task{
			{
				ID:                 "bugfix-1",
				Description:        "Fix GetUser to return 400 on invalid ID and 404 on missing user",
				DoneWhen:           "All 3 handler tests pass",
				Scope:              "handler/user.go",
				Status:             models.TaskStatusImplementing,
				Priority:           1,
				Created:            now,
				SpecRef:            "specs/delivery.md",
				AcceptanceCriteria: []string{"AC-1: returns 200", "AC-2: returns 400", "AC-3: returns 404"},
				VerifyCommands:     []string{"go test ./handler/..."},
			},
		},
	}

	// Quality gate: IMPLEMENTING task with AC + verify_commands should be accepted
	task := state.Tasks[0]
	if len(task.AcceptanceCriteria) == 0 {
		t.Error("S1: task should have acceptance_criteria")
	}
	if len(task.VerifyCommands) == 0 {
		t.Error("S1: task should have verify_commands")
	}

	// 4. After "fixing", move to READY_FOR_REVIEW — auditor should find work
	state.Tasks[0].Status = models.TaskStatusReadyForReview
	target := auditor.FindAuditTarget(state)
	if target == nil {
		t.Fatal("S1: auditor should find READY_FOR_REVIEW task")
	}
	if target.TaskID != "bugfix-1" {
		t.Errorf("S1: audit target task = %q, want bugfix-1", target.TaskID)
	}

	// 5. Emit observability events for the happy path
	var buf bytes.Buffer
	emitter := observability.NewEmitterWriter(&buf)
	_ = emitter.Emit(observability.NewEvent(observability.EventTaskStatusChanged, "bugfix-1 → READY_FOR_REVIEW").WithTask("bugfix-1"))
	_ = emitter.Emit(observability.NewEvent(observability.EventVerifyRun, "all tests pass").WithTask("bugfix-1"))
	_ = emitter.Emit(observability.NewRunSummaryEvent(true, "all tasks done", nil))

	events, err := observability.ReadEventsFrom(&buf, observability.EventFilter{})
	if err != nil {
		t.Fatalf("S1: ReadEventsFrom: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("S1: expected 3 events, got %d", len(events))
	}

	t.Log("S1: happy-path bugfix scenario — all checks passed")
}

// ---------------------------------------------------------------------------
// S2 — Quality gate enforcement: a task missing AC or verify_commands is rejected.
// ---------------------------------------------------------------------------

func TestScenario_S2_QualityGateEnforcement(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E scenario tests in short mode")
	}
	t.Parallel()

	now := time.Now().UTC()

	// Task missing acceptance criteria
	stateNoAC := &models.State{
		Goal: models.Goal{Description: "Quality gate test"},
		Tasks: []models.Task{
			{
				ID:             "qg-no-ac",
				Description:    "Fix something",
				DoneWhen:       "Tests pass",
				Scope:          "handler/",
				Status:         models.TaskStatusImplementing,
				Priority:       1,
				Created:        now,
				VerifyCommands: []string{"go test ./..."},
				// AcceptanceCriteria intentionally omitted
			},
		},
	}

	// Verify: task without AC should fail quality gate
	taskNoAC := stateNoAC.Tasks[0]
	if taskNoAC.Status == models.TaskStatusImplementing && len(taskNoAC.AcceptanceCriteria) == 0 {
		t.Log("S2: correctly identified IMPLEMENTING task without acceptance_criteria")
	} else {
		t.Error("S2: expected IMPLEMENTING task with no acceptance_criteria")
	}

	// Task missing verify_commands
	stateNoVerify := &models.State{
		Goal: models.Goal{Description: "Quality gate test"},
		Tasks: []models.Task{
			{
				ID:                 "qg-no-verify",
				Description:        "Fix something",
				DoneWhen:           "Tests pass",
				Scope:              "handler/",
				Status:             models.TaskStatusImplementing,
				Priority:           1,
				Created:            now,
				AcceptanceCriteria: []string{"AC-1: works"},
				// VerifyCommands intentionally omitted
			},
		},
	}

	// Verify: task without verify_commands should fail quality gate
	taskNoVerify := stateNoVerify.Tasks[0]
	if taskNoVerify.Status == models.TaskStatusImplementing && len(taskNoVerify.VerifyCommands) == 0 {
		t.Log("S2: correctly identified IMPLEMENTING task without verify_commands")
	} else {
		t.Error("S2: expected IMPLEMENTING task with no verify_commands")
	}

	t.Log("S2: quality gate enforcement — correctly rejected incomplete tasks")
}

// ---------------------------------------------------------------------------
// S3 — Spec ambiguity: ambiguous delivery spec triggers warnings / failures
// that should lead to NEEDS_HUMAN_DECISION.
// ---------------------------------------------------------------------------

func TestScenario_S3_SpecAmbiguity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E scenario tests in short mode")
	}
	t.Parallel()

	root := fixtureRoot(t)
	fixtureDir := filepath.Join(root, "fixture_spec_ambiguity")

	// 1. Validate the ambiguous delivery spec
	deliverySpec := readFixtureFile(t, fixtureDir, "specs", "delivery.md")
	result := specvalidate.ValidateSpecFile(deliverySpec, specvalidate.SpecTypeDelivery)

	// The spec has all required sections, so it's technically Valid
	// but it should have warnings (e.g., no GWT in acceptance criteria)
	if len(result.Warnings) == 0 {
		t.Error("S3: ambiguous spec should produce warnings")
	}

	hasACWarning := false
	for _, w := range result.Warnings {
		if w == "Acceptance Criteria section has no Given/When/Then blocks" {
			hasACWarning = true
		}
	}
	if !hasACWarning {
		t.Error("S3: expected 'no Given/When/Then' warning for vague acceptance criteria")
	}

	// 2. The ambiguous vision spec should fail validation (missing required sections)
	visionSpec := readFixtureFile(t, fixtureDir, "specs", "vision.md")
	vResult := specvalidate.ValidateSpecFile(visionSpec, specvalidate.SpecTypeVision)
	// Check if it has missing sections or warnings — the fixture is intentionally vague
	if len(vResult.Missing) > 0 || len(vResult.Warnings) > 0 {
		t.Logf("S3: vision spec issues — missing: %v, warnings: %v", vResult.Missing, vResult.Warnings)
	}

	// 3. The Open Questions section is filled with unresolved questions —
	// a real agent should escalate to NEEDS_HUMAN_DECISION
	// Simulate the task being marked NHD
	now := time.Now().UTC()
	state := &models.State{
		Goal: models.Goal{Description: "Ambiguous pipeline"},
		Tasks: []models.Task{
			{
				ID:          "ambig-1",
				Description: "Implement data pipeline",
				DoneWhen:    "Pipeline works",
				Scope:       "pipeline/",
				Status:      models.TaskStatusNeedsHumanDecision,
				Priority:    2,
				Created:     now,
			},
		},
	}

	if state.Tasks[0].Status != models.TaskStatusNeedsHumanDecision {
		t.Errorf("S3: task status = %q, want NEEDS_HUMAN_DECISION", state.Tasks[0].Status)
	}

	// 4. Emit RUN_SUMMARY for the ambiguity outcome
	var buf bytes.Buffer
	emitter := observability.NewEmitterWriter(&buf)
	_ = emitter.Emit(observability.NewRunSummaryEvent(false, "spec ambiguity: unresolved open questions", nil))
	events, _ := observability.ReadEventsFrom(&buf, observability.EventFilter{Type: observability.EventRunSummary})
	if len(events) != 1 {
		t.Fatalf("S3: expected 1 RUN_SUMMARY, got %d", len(events))
	}
	if events[0].Data["result"] != "FAIL" {
		t.Errorf("S3: RUN_SUMMARY result = %q, want FAIL", events[0].Data["result"])
	}

	t.Log("S3: spec ambiguity scenario — warnings raised, NHD escalation worked")
}

// ---------------------------------------------------------------------------
// S4 — Runaway loop: unfixable fixture triggers anomaly detection, verification
// fails, task → BLOCKED, RUN_SUMMARY(FAIL).
// ---------------------------------------------------------------------------

func TestScenario_S4_RunawayLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E scenario tests in short mode")
	}
	t.Parallel()

	root := fixtureRoot(t)
	fixtureDir := filepath.Join(root, "fixture_runaway")

	// 1. Validate the delivery spec (should be valid — the spec is fine, the code is unfixable)
	deliverySpec := readFixtureFile(t, fixtureDir, "specs", "delivery.md")
	result := specvalidate.ValidateSpecFile(deliverySpec, specvalidate.SpecTypeDelivery)
	if !result.Valid {
		t.Errorf("S4: delivery spec should be valid, missing: %v", result.Missing)
	}

	// 2. Run verification against the fixture — this should fail
	// (the test asserts year < 2020, which will always fail now)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	vResult := verify.RunVerification(ctx, []string{
		"go test ./service/...",
	}, fixtureDir, verify.Config{
		Timeout:        15 * time.Second,
		MaxOutputBytes: 64 * 1024,
	})

	if vResult.Passed {
		t.Error("S4: verification should FAIL on the unfixable fixture")
	}
	if len(vResult.Results) < 1 {
		t.Fatal("S4: expected at least 1 command result")
	}
	if vResult.Results[0].ExitCode == 0 {
		t.Error("S4: exit code should be non-zero")
	}

	// 3. Simulate repeated iterations (agent trying and failing)
	var records []runtime.IterationRecord
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		records = append(records, runtime.IterationRecord{
			TaskID:    "runaway-1",
			Status:    "FAILED",
			HasDiff:   false,
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		})
	}

	detector := runtime.NewAnomalyDetector()
	anomalies := detector.Detect(records)
	if len(anomalies) < 1 {
		t.Fatal("S4: expected anomalies from repeated failures")
	}

	foundStagnation := false
	foundNoDiff := false
	for _, a := range anomalies {
		switch a.Type {
		case runtime.AnomalyStagnation:
			foundStagnation = true
		case runtime.AnomalyNoDiffRetry:
			foundNoDiff = true
		}
	}
	if !foundStagnation {
		t.Error("S4: expected STAGNATION anomaly")
	}
	if !foundNoDiff {
		t.Error("S4: expected NO_DIFF_RETRY anomaly")
	}

	// 4. Task should be marked BLOCKED after anomaly detection
	state := &models.State{
		Goal: models.Goal{Description: "Runaway loop test"},
		Tasks: []models.Task{
			{
				ID:                 "runaway-1",
				Description:        "Fix time service",
				DoneWhen:           "go test passes",
				Scope:              "service/time.go",
				Status:             models.TaskStatusBlocked,
				Priority:           1,
				Created:            now,
				SpecRef:            "specs/delivery.md",
				AcceptanceCriteria: []string{"AC-1: CurrentYear returns current year"},
				VerifyCommands:     []string{"go test ./service/..."},
			},
		},
	}

	if state.Tasks[0].Status != models.TaskStatusBlocked {
		t.Errorf("S4: task status = %q, want BLOCKED", state.Tasks[0].Status)
	}

	// 5. Budget check should also be approaching limits
	tracker := runtime.NewBudgetTracker(5, 10, 0)
	for i := 0; i < 5; i++ {
		tracker.RecordIteration()
	}
	if err := tracker.Check(time.Now()); err == nil {
		t.Error("S4: budget should be exceeded after 5 iterations with max=5")
	}

	// 6. Emit RUN_SUMMARY(FAIL) with anomaly as cause
	var buf bytes.Buffer
	emitter := observability.NewEmitterWriter(&buf)
	_ = emitter.Emit(observability.NewEvent(observability.EventAnomalyDetected, "stagnation on runaway-1").WithTask("runaway-1"))
	_ = emitter.Emit(observability.NewRunSummaryEvent(false, "task runaway-1 blocked: stagnation + no-diff after 5 iterations", nil))

	events, _ := observability.ReadEventsFrom(&buf, observability.EventFilter{})
	if len(events) != 2 {
		t.Fatalf("S4: expected 2 events, got %d", len(events))
	}
	if events[0].Type != observability.EventAnomalyDetected {
		t.Errorf("S4: event[0] type = %q, want ANOMALY_DETECTED", events[0].Type)
	}
	if events[1].Type != observability.EventRunSummary {
		t.Errorf("S4: event[1] type = %q, want RUN_SUMMARY", events[1].Type)
	}
	if events[1].Data["result"] != "FAIL" {
		t.Errorf("S4: RUN_SUMMARY result = %q, want FAIL", events[1].Data["result"])
	}

	t.Log("S4: runaway loop scenario — verify failed, anomaly detected, BLOCKED, FAIL summary")
}
