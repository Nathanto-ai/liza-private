package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/errors"
	"github.com/liza-mas/liza/internal/models"
)

const (
	// DefaultHeartbeatInterval is the default time between heartbeats.
	// Derived from models.DefaultHeartbeatIntervalSec to maintain a single source of truth.
	DefaultHeartbeatInterval = time.Duration(models.DefaultHeartbeatIntervalSec) * time.Second
	// DefaultLeaseDuration is the default lease duration
	DefaultLeaseDuration = time.Duration(models.DefaultLeaseDurationSeconds) * time.Second

	// evictionThreshold is the number of consecutive NotFoundErrors before
	// the heartbeat considers the agent evicted and returns a fatal error.
	evictionThreshold = 3
)

// HeartbeatConfig contains configuration for the heartbeat mechanism
type HeartbeatConfig struct {
	AgentID       string
	StatePath     string
	Interval      time.Duration
	LeaseDuration time.Duration
	State         *models.State // Optional: if provided, interval is read from state.Config.HeartbeatInterval
}

// Heartbeat manages background lease extension for an agent
type Heartbeat struct {
	agentID       string
	bb            *db.Blackboard
	interval      time.Duration
	leaseDuration time.Duration
}

// NewHeartbeat creates a new heartbeat instance
func NewHeartbeat(config HeartbeatConfig) *Heartbeat {
	interval := config.Interval

	// If state is provided, read interval from config with bounds validation
	if config.State != nil {
		interval = models.NormalizeHeartbeatInterval(config.State.Config.HeartbeatInterval)
	}

	// Fall back to explicit config.Interval or default
	if interval == 0 {
		interval = DefaultHeartbeatInterval
	}

	leaseDuration := config.LeaseDuration
	if leaseDuration == 0 {
		leaseDuration = DefaultLeaseDuration
	}

	return &Heartbeat{
		agentID:       config.AgentID,
		bb:            db.For(config.StatePath),
		interval:      interval,
		leaseDuration: leaseDuration,
	}
}

// Start begins the heartbeat loop, extending the agent's lease periodically.
// Returns when the context is cancelled, or when the agent's registration is
// missing from state for evictionThreshold consecutive beats (zombie detection).
func (h *Heartbeat) Start(ctx context.Context) error {
	logger := GetLogger()
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	consecutiveMissing := 0

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := h.beat(); err != nil {
				if errors.IsNotFound(err) {
					consecutiveMissing++
					logger.Warn("Agent registration missing from state",
						"agent_id", h.agentID,
						"consecutive", consecutiveMissing,
						"threshold", evictionThreshold)
					if consecutiveMissing >= evictionThreshold {
						return fmt.Errorf("agent %s evicted: registration missing for %d consecutive heartbeats", h.agentID, consecutiveMissing)
					}
					continue
				}
				// Log non-eviction errors but continue
				logger.Error("Heartbeat update failed", "error", err, "agent_id", h.agentID)
				consecutiveMissing = 0
			} else {
				consecutiveMissing = 0
			}
		}
	}
}

// beat performs a single heartbeat update
func (h *Heartbeat) beat() error {
	now := time.Now().UTC()
	newLease := now.Add(h.leaseDuration)

	return h.bb.Modify(func(state *models.State) error {
		agent, exists := state.Agents[h.agentID]
		if !exists {
			return &errors.NotFoundError{Entity: "agent", ID: h.agentID}
		}

		agent.Heartbeat = now
		agent.LeaseExpires = &newLease
		state.Agents[h.agentID] = agent

		return nil
	})
}
