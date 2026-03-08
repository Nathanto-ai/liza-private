package ops

import (
	"fmt"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/errors"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
)

// DeleteAgentResult contains the outcome of deleting an agent.
type DeleteAgentResult struct {
	AgentID string
}

// DeleteAgent removes an agent from state. Without force, refuses if the agent
// has an active lease, current task, or running process. The allowRunningPID
// flag bypasses only the PID liveness check (for interactive CLI confirmation)
// without bypassing lease/task safety checks. Callers should check
// IsAgentProcessRunning for interactive confirmation first. No terminal I/O.
func DeleteAgent(projectRoot, agentID string, force, allowRunningPID bool, reason string) (*DeleteAgentResult, error) {
	if agentID == "" {
		return nil, fmt.Errorf("agent ID required")
	}

	lp := paths.New(projectRoot)
	bb := db.For(lp.StatePath())

	state, err := bb.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read state: %w", err)
	}

	agent, exists := state.Agents[agentID]
	if !exists {
		return nil, &errors.NotFoundError{Entity: "agent", ID: agentID}
	}

	now := time.Now().UTC()
	if !force {
		if agent.LeaseExpires != nil && agent.LeaseExpires.After(now) {
			return nil, fmt.Errorf("agent %s has active lease (expires %v), use --force to delete", agentID, agent.LeaseExpires.Format(time.RFC3339))
		}

		if agent.CurrentTask != nil {
			return nil, fmt.Errorf("agent %s is working on task %s, use --force to delete", agentID, *agent.CurrentTask)
		}

		if !allowRunningPID && agent.PID != 0 && IsProcessAlive(agent.PID) {
			return nil, fmt.Errorf("agent %s is still running with PID %d, use --force to delete or confirm interactively via CLI", agentID, agent.PID)
		}
	}

	err = bb.Modify(func(state *models.State) error {
		agent, exists := state.Agents[agentID]
		if !exists {
			return &errors.NotFoundError{Entity: "agent", ID: agentID}
		}

		now := time.Now().UTC()
		if !force {
			if agent.LeaseExpires != nil && agent.LeaseExpires.After(now) {
				return fmt.Errorf("agent %s has active lease (expires %v), use --force to delete", agentID, agent.LeaseExpires.Format(time.RFC3339))
			}

			if agent.CurrentTask != nil {
				return fmt.Errorf("agent %s is working on task %s, use --force to delete", agentID, *agent.CurrentTask)
			}
		}

		delete(state.Agents, agentID)

		humanNote := models.HumanNote{
			Timestamp: now,
			Message:   fmt.Sprintf("Agent %s deleted: %s", agentID, reason),
			For:       agentID,
		}
		state.HumanNotes = append(state.HumanNotes, humanNote)

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to delete agent: %w", err)
	}

	return &DeleteAgentResult{
		AgentID: agentID,
	}, nil
}

// IsAgentProcessRunning checks if the agent's registered PID is alive. Use before
// DeleteAgent to prompt for interactive confirmation.
func IsAgentProcessRunning(projectRoot, agentID string) (bool, int, error) {
	lp := paths.New(projectRoot)
	bb := db.For(lp.StatePath())

	state, err := bb.Read()
	if err != nil {
		return false, 0, fmt.Errorf("failed to read state: %w", err)
	}

	agent, exists := state.Agents[agentID]
	if !exists {
		return false, 0, &errors.NotFoundError{Entity: "agent", ID: agentID}
	}

	if agent.PID != 0 && IsProcessAlive(agent.PID) {
		return true, agent.PID, nil
	}

	return false, agent.PID, nil
}
