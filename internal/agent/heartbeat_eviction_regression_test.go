package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// TestHeartbeatEvictionDetection is a regression test for the agent zombie
// problem found during the tasktrack live test. When an agent's registration
// is removed from state.yaml (e.g., via git restore), the heartbeat should
// detect the eviction and return a fatal error instead of running forever
// as a zombie process.
func TestHeartbeatEvictionDetection(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	initialState := testhelpers.CreateValidState()
	now := time.Now().UTC()
	lease := now.Add(30 * time.Minute)
	initialState.Agents = map[string]models.Agent{
		"coder-1": {
			Role:         "coder",
			Status:       models.AgentStatusIdle,
			Heartbeat:    now,
			LeaseExpires: &lease,
			PID:          1234,
		},
	}
	bb := testhelpers.WriteInitialState(t, stateFile, initialState)

	config := HeartbeatConfig{
		AgentID:       "coder-1",
		StatePath:     stateFile,
		Interval:      50 * time.Millisecond,
		LeaseDuration: 30 * time.Minute,
	}

	hb := NewHeartbeat(config)

	// Verify heartbeat works initially
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := hb.Start(ctx)
	if err != nil && err != context.DeadlineExceeded {
		t.Fatalf("initial heartbeat failed: %v", err)
	}

	// Simulate state restoration: remove agent registration
	err = bb.Modify(func(state *models.State) error {
		state.Agents = map[string]models.Agent{}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to clear agents: %v", err)
	}

	// Re-create heartbeat (fresh instance to avoid singleton caching effects)
	db.ResetInstances()
	hb2 := NewHeartbeat(config)

	// Heartbeat should now detect eviction and return an error
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	err = hb2.Start(ctx2)

	if err == nil {
		t.Error("heartbeat should return eviction error after agent removed")
	}
	if err == context.DeadlineExceeded {
		t.Error("heartbeat should detect eviction before context deadline")
	}
	if err != nil && !strings.Contains(err.Error(), "evicted") {
		t.Errorf("expected eviction error, got: %v", err)
	}
}

// TestHeartbeatEvictionRecovers verifies that if an agent's registration
// temporarily disappears but comes back, the counter resets and no eviction
// is triggered.
func TestHeartbeatEvictionRecovers(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)

	initialState := testhelpers.CreateValidState()
	now := time.Now().UTC()
	lease := now.Add(30 * time.Minute)
	initialState.Agents = map[string]models.Agent{
		"coder-1": {
			Role:         "coder",
			Status:       models.AgentStatusIdle,
			Heartbeat:    now,
			LeaseExpires: &lease,
			PID:          1234,
		},
	}
	bb := testhelpers.WriteInitialState(t, stateFile, initialState)

	config := HeartbeatConfig{
		AgentID:       "coder-1",
		StatePath:     stateFile,
		Interval:      50 * time.Millisecond,
		LeaseDuration: 30 * time.Minute,
	}

	hb := NewHeartbeat(config)

	// Remove agent to trigger 1-2 missing heartbeats
	go func() {
		time.Sleep(20 * time.Millisecond)
		bb.Modify(func(state *models.State) error {
			state.Agents = map[string]models.Agent{}
			return nil
		})

		// Restore agent before eviction threshold (3) is reached
		time.Sleep(100 * time.Millisecond)
		bb.Modify(func(state *models.State) error {
			lease := time.Now().Add(30 * time.Minute)
			state.Agents = map[string]models.Agent{
				"coder-1": {
					Role:         "coder",
					Status:       models.AgentStatusIdle,
					Heartbeat:    time.Now().UTC(),
					LeaseExpires: &lease,
					PID:          1234,
				},
			}
			return nil
		})
	}()

	// Heartbeat should survive (context timeout reached, not eviction)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := hb.Start(ctx)

	// Should timeout, not evict
	if err != nil && !strings.Contains(err.Error(), "evicted") {
		// Non-eviction error is OK (could be context cancel)
	}
	if err != nil && strings.Contains(err.Error(), "evicted") {
		t.Errorf("heartbeat should have recovered, but got eviction: %v", err)
	}
}
