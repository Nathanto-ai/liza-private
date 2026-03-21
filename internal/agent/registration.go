package agent

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/errors"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/ops"
	"github.com/liza-mas/liza/internal/roles"
)

// validateIdentity validates agent ID format: {role}-{number}
func validateIdentity(agentID, role string) error {
	if agentID == "" {
		return fmt.Errorf("agent ID required")
	}

	// Split on last hyphen
	lastHyphen := -1
	for i := len(agentID) - 1; i >= 0; i-- {
		if agentID[i] == '-' {
			lastHyphen = i
			break
		}
	}

	if lastHyphen == -1 {
		return fmt.Errorf("invalid agent ID format (expected {role}-{number}): %s", agentID)
	}

	idRole := agentID[:lastHyphen]
	numStr := agentID[lastHyphen+1:]

	// Validate number is numeric
	if _, err := strconv.Atoi(numStr); err != nil {
		return fmt.Errorf("agent ID suffix must be numeric: %s", agentID)
	}

	// Validate role matches
	if idRole != role {
		return fmt.Errorf("agent ID role mismatch (ID=%s, config=%s)", idRole, role)
	}

	return nil
}

// registerAgent registers an agent with collision detection.
// If an existing agent has a valid lease but its process is dead, the
// registration auto-recovers (releases claims, deletes old entry) instead
// of requiring manual `liza recover-agent`.
func registerAgent(bb *db.Blackboard, projectRoot, agentID, role, terminal string, leaseDuration int) error {
	logger := GetLogger()
	now := time.Now().UTC()
	leaseExpires := now.Add(time.Duration(leaseDuration) * time.Second)

	// Single atomic registration - skip STARTING state, go directly to IDLE
	err := bb.Modify(func(state *models.State) error {
		// Check for collision
		if existing, exists := state.Agents[agentID]; exists {
			// Check if lease is still valid
			if existing.LeaseExpires != nil && existing.LeaseExpires.After(now) {
				// Lease valid — check if the process is actually alive
				if existing.PID > 0 && !ops.IsProcessAlive(existing.PID) {
					// Process is dead but lease hasn't expired.
					// Auto-recover: release task claims and delete stale entry.
					logger.Info("Auto-recovering dead agent with valid lease",
						"agent_id", agentID,
						"dead_pid", existing.PID,
						"lease_expires", existing.LeaseExpires.Format(time.RFC3339))
					if existing.CurrentTask != nil {
						taskID := *existing.CurrentTask
						if task := state.FindTask(taskID); task != nil {
							releaseTaskClaim(state, task, existing.Role, agentID, now)
						}
					}
					delete(state.Agents, agentID)
					// Fall through to register the new agent below
				} else {
					return fmt.Errorf("agent ID collision: %s already registered with valid lease (expires %s)",
						agentID, existing.LeaseExpires.Format(time.RFC3339))
				}
			} else {
				logger.Info("Taking over expired agent lease", "agent_id", agentID)
			}
		}

		// Register agent directly as IDLE (atomic operation)
		pid := os.Getpid()
		state.Agents[agentID] = models.Agent{
			Role:         role,
			Status:       models.AgentStatusIdle,
			Heartbeat:    now,
			Terminal:     terminal,
			LeaseExpires: &leaseExpires,
			PID:          pid,
		}

		return nil
	})

	if err != nil {
		return err
	}

	// If code-reviewer: clear stale review claims
	if role == roles.RuntimeCodeReviewer {
		if _, err := ops.ClearStaleReviewClaims(projectRoot); err != nil {
			logger.Warn("Failed to clear stale review claims", "error", err, "role", role)
		}
	}

	return nil
}

// unregisterAgent releases any task claim held by the agent, then removes
// the agent from state. Both operations happen in a single atomic modify
// so that an interrupt between them cannot leave a stuck task.
// Retries up to 3 times on transient errors (e.g., Windows sharing violations).
func unregisterAgent(bb *db.Blackboard, agentID string) {
	logger := GetLogger()

	const maxAttempts = 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		now := time.Now().UTC()

		err := bb.Modify(func(state *models.State) error {
			agent, exists := state.Agents[agentID]
			if !exists {
				return nil
			}

			// Release task claim if agent held one
			if agent.CurrentTask != nil {
				taskID := *agent.CurrentTask
				if task := state.FindTask(taskID); task != nil {
					releaseTaskClaim(state, task, agent.Role, agentID, now)
				}
			}

			delete(state.Agents, agentID)
			return nil
		})

		if err == nil {
			return
		}

		logger.Warn("Failed to unregister agent",
			"error", err, "agent_id", agentID,
			"attempt", attempt, "max_attempts", maxAttempts)

		if attempt < maxAttempts {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
	}
}

// releaseTaskClaim transitions a task back to its unclaimed status and clears
// the claim fields. Best-effort: logs warnings instead of failing.
func releaseTaskClaim(state *models.State, task *models.Task, role, agentID string, now time.Time) {
	logger := GetLogger()
	reason := "agent interrupted"

	switch role {
	case roles.RuntimeCoder:
		if task.Status == models.TaskStatusImplementing {
			if err := task.Transition(models.TaskStatusReady); err != nil {
				logger.Warn("Failed to transition task on unregister", "task_id", task.ID, "error", err)
			}
		}
		task.AssignedTo = nil
		task.LeaseExpires = nil

	case roles.RuntimeCodeReviewer:
		if task.Status == models.TaskStatusReviewing {
			if err := task.Transition(models.TaskStatusReadyForReview); err != nil {
				logger.Warn("Failed to transition task on unregister", "task_id", task.ID, "error", err)
			}
		}
		task.ReviewingBy = nil
		task.ReviewLeaseExpires = nil

	default:
		return
	}

	state.ReleaseAgent(agentID)

	task.History = append(task.History, models.TaskHistoryEntry{
		Time:   now,
		Event:  "claim_released",
		Agent:  &agentID,
		Reason: &reason,
	})
}

// resetAgentToIdle resets an agent's status to IDLE and clears CurrentTask
func resetAgentToIdle(bb *db.Blackboard, agentID string) error {
	now := time.Now().UTC()

	return bb.Modify(func(state *models.State) error {
		agent, exists := state.Agents[agentID]
		if !exists {
			return &errors.NotFoundError{Entity: "agent", ID: agentID}
		}

		// Reset to IDLE state
		agent.Status = models.AgentStatusIdle
		agent.CurrentTask = nil
		agent.Heartbeat = now

		state.Agents[agentID] = agent
		return nil
	})
}

// resetAgentAfterExit clears transient runtime states after CLI exit while preserving
// explicit command-driven states that are meaningful between loops.
func resetAgentAfterExit(bb *db.Blackboard, agentID string) error {
	now := time.Now().UTC()

	return bb.Modify(func(state *models.State) error {
		agent, exists := state.Agents[agentID]
		if !exists {
			return &errors.NotFoundError{Entity: "agent", ID: agentID}
		}

		switch agent.Status {
		case models.AgentStatusWaiting, models.AgentStatusHandoff:
			if agent.CurrentTask != nil {
				agent.Heartbeat = now
				state.Agents[agentID] = agent
				return nil
			}
			// CurrentTask already cleared — fall through to reset to IDLE
		}

		agent.Status = models.AgentStatusIdle
		agent.CurrentTask = nil
		agent.Heartbeat = now
		state.Agents[agentID] = agent
		return nil
	})
}

// setAgentToPlanningStatus sets a planner agent's status to PLANNING
func setAgentToPlanningStatus(bb *db.Blackboard, agentID string) error {
	now := time.Now().UTC()

	return bb.Modify(func(state *models.State) error {
		agent, exists := state.Agents[agentID]
		if !exists {
			return &errors.NotFoundError{Entity: "agent", ID: agentID}
		}

		// Set to PLANNING state
		agent.Status = models.AgentStatusPlanning
		planning := "planning"
		agent.CurrentTask = &planning
		agent.Heartbeat = now

		// Renew lease
		leaseDuration := state.Config.LeaseDuration
		if leaseDuration <= 0 {
			leaseDuration = models.DefaultLeaseDurationSeconds
		}
		leaseExpires := now.Add(time.Duration(leaseDuration) * time.Second)
		agent.LeaseExpires = &leaseExpires

		state.Agents[agentID] = agent
		return nil
	})
}
