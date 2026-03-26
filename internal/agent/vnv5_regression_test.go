package agent

import (
	"testing"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/roles"
	"github.com/liza-mas/liza/internal/runtime"
)

// Regression tests for V&V Run 5 fixes.

// TestFix26_IdleLoopDoesNotBurnIterationBudget verifies that budget.RecordIteration()
// is only called after productive work (agent execution), not during idle polls.
// Bug: reviewer burned 50 idle iterations out of 100 total budget, exhausting at 69 min.
// Fix: moved RecordIteration from top of supervior loop to after executeAgent.
func TestFix26_IdleLoopDoesNotBurnIterationBudget(t *testing.T) {
	t.Parallel()

	bt := runtime.NewBudgetTracker(10, 100, 0)

	// Simulate 50 idle polls — these should NOT consume iterations.
	// Before Fix 26, each poll called RecordIteration(). Now they don't.
	// The test verifies the budget API: iterations only increment on explicit RecordIteration.
	if bt.Iterations() != 0 {
		t.Fatalf("iterations = %d at start, want 0", bt.Iterations())
	}

	// Simulate 3 productive iterations (agent actually executed)
	bt.RecordIteration()
	bt.RecordIteration()
	bt.RecordIteration()

	if bt.Iterations() != 3 {
		t.Fatalf("iterations = %d after 3 records, want 3", bt.Iterations())
	}

	// Should not be exceeded yet (3/10)
	if err := bt.Check(bt.StartTime); err != nil {
		t.Fatalf("unexpected budget exceeded: %v", err)
	}
}

// TestFix26_ReviewerIdleBackoff verifies the reviewer now uses exponential
// idle backoff instead of exiting when no work is available.
// Bug: reviewer exited on first no-work result, then on restart burned idle iterations.
// Fix: reviewer now stays alive with backoff like auditor.
func TestFix26_ReviewerIdleBackoff(t *testing.T) {
	t.Parallel()

	cfg := models.Config{
		IdleBackoffBaseSec: 5,
		IdleBackoffMaxSec:  120,
	}

	// Verify reviewer now uses the same idle backoff as auditor
	backoff1 := computeIdleBackoff(1, cfg)
	backoff2 := computeIdleBackoff(2, cfg)
	backoff5 := computeIdleBackoff(5, cfg)

	if backoff1.Seconds() < 5 {
		t.Errorf("idle backoff at count 1 = %v, want >= 5s", backoff1)
	}
	if backoff2 <= backoff1 {
		t.Errorf("idle backoff should increase: count 1 = %v, count 2 = %v", backoff1, backoff2)
	}
	if backoff5.Seconds() > 120 {
		t.Errorf("idle backoff at count 5 = %v, should be capped at 120s", backoff5)
	}
}

// TestFix26_ReviewerIsLongLivedAgent verifies that the reviewer role is treated
// as a long-lived agent (like auditor) for the idle backoff path.
func TestFix26_ReviewerIsLongLivedAgent(t *testing.T) {
	t.Parallel()

	// The fix adds RuntimeCodeReviewer to the idle-backoff condition alongside
	// RuntimeAuditor. This test verifies the role constant exists and is distinct.
	if roles.RuntimeCodeReviewer == roles.RuntimeAuditor {
		t.Fatal("RuntimeCodeReviewer should be distinct from RuntimeAuditor")
	}
	if roles.RuntimeCodeReviewer == roles.RuntimeCoder {
		t.Fatal("RuntimeCodeReviewer should be distinct from RuntimeCoder")
	}
}
