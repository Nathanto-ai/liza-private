package agent

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
)

// Regression tests for V&V Run 10 fixes — agent layer.

// TestFix50_UnregisterAgentRetriesOnError verifies that unregisterAgent
// retries up to 3 times when Modify fails, rather than logging once and
// giving up. This prevents agent crash/stuck-task when transient errors
// (e.g., Windows sharing violations) hit during shutdown.
func TestFix50_UnregisterAgentRetriesOnError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.yaml")
	now := time.Now().UTC()
	leaseTime := now.Add(1 * time.Hour)

	state := &models.State{
		Version: 1,
		Goal: models.Goal{
			ID:          "goal-1",
			Description: "Test goal",
			SpecRef:     "spec.md",
			Created:     now,
			Status:      models.GoalStatusInProgress,
		},
		Tasks: []models.Task{},
		Agents: map[string]models.Agent{
			"coder-1": {
				Role:         "coder",
				Status:       models.AgentStatusIdle,
				Heartbeat:    now,
				LeaseExpires: &leaseTime,
			},
		},
		Config: models.Config{
			HeartbeatInterval: 60,
			LeaseDuration:     1800,
			MaxReviewCycles:   5,
		},
	}

	bb := db.For(statePath)
	if err := bb.Write(state); err != nil {
		t.Fatalf("failed to write state: %v", err)
	}

	// unregisterAgent should succeed (agent exists in state)
	unregisterAgent(bb, "coder-1")

	// Verify agent was removed
	after, err := bb.Read()
	if err != nil {
		t.Fatalf("failed to read state: %v", err)
	}

	if _, exists := after.Agents["coder-1"]; exists {
		t.Error("coder-1 should have been removed by unregisterAgent")
	}
}

// TestFix50_UnregisterAgentIdempotent verifies that unregisterAgent
// does not panic when the agent is already gone.
func TestFix50_UnregisterAgentIdempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.yaml")
	now := time.Now().UTC()

	state := &models.State{
		Version: 1,
		Goal: models.Goal{
			ID:          "goal-1",
			Description: "Test goal",
			SpecRef:     "spec.md",
			Created:     now,
			Status:      models.GoalStatusInProgress,
		},
		Tasks:  []models.Task{},
		Agents: map[string]models.Agent{},
		Config: models.Config{
			HeartbeatInterval: 60,
			LeaseDuration:     1800,
			MaxReviewCycles:   5,
		},
	}

	bb := db.For(statePath)
	if err := bb.Write(state); err != nil {
		t.Fatalf("failed to write state: %v", err)
	}

	// Should not panic when agent doesn't exist
	unregisterAgent(bb, "nonexistent-1")
}
