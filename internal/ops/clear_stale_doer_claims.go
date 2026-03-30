package ops

import (
	"fmt"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/log"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
)

// ClearStaleDoerClaims finds pipeline doer tasks in an executing state whose
// lease has expired and releases them back to their initial status so another
// agent can pick them up. This covers all pipeline roles (epic-planner,
// us-writer, code-planner, coder) and prevents tasks from being permanently
// stuck when an agent process dies.
// Returns the number of claims released.
func ClearStaleDoerClaims(projectRoot string) (int, error) {
	lp := paths.New(projectRoot)
	bb := db.For(lp.StatePath())
	logger := log.New(lp.LogPath())

	pb, err := loadPipelineBundle(projectRoot)
	if err != nil {
		return 0, fmt.Errorf("failed to load pipeline config: %w", err)
	}

	released := 0
	now := time.Now().UTC()

	err = bb.Modify(func(state *models.State) error {
		for i := range state.Tasks {
			task := &state.Tasks[i]

			// Only handle pipeline tasks with a role_pair.
			if task.RolePair == "" {
				continue
			}

			// Check if task is in an executing status for its role pair.
			executing, exErr := pb.resolver.ExecutingStatus(task.RolePair)
			if exErr != nil {
				continue
			}
			if task.Status != executing {
				continue
			}
			if task.AssignedTo == nil {
				continue
			}

			isExpired := false
			var staleAgent string
			var expiredAt string

			if task.LeaseExpires == nil {
				isExpired = true
				staleAgent = *task.AssignedTo
				expiredAt = "unknown (lease missing)"
			} else if !task.LeaseExpires.After(now) {
				isExpired = true
				staleAgent = *task.AssignedTo
				expiredAt = task.LeaseExpires.Format(time.RFC3339)
			}

			if !isExpired {
				continue
			}

			// Determine the initial status to revert to.
			initial, initErr := pb.resolver.InitialStatus(task.RolePair)
			if initErr != nil {
				continue
			}

			if err := task.TransitionWith(initial, pb.transitions); err != nil {
				return err
			}
			task.AssignedTo = nil
			task.LeaseExpires = nil

			// Release the agent if still assigned to this task.
			if a, ok := state.Agents[staleAgent]; ok {
				if a.CurrentTask != nil && *a.CurrentTask == task.ID {
					state.ReleaseAgent(staleAgent)
				}
			}

			detail := fmt.Sprintf("Doer claim expired at %s (agent: %s)", expiredAt, staleAgent)

			logEntry := log.Entry{
				Timestamp: now,
				Agent:     "system",
				Action:    "stale_doer_claim_cleared",
				Task:      &task.ID,
				Detail:    detail,
			}
			if err := logger.Append(logEntry); err != nil {
				return fmt.Errorf("failed to log stale doer claim cleanup for %s: %w", task.ID, err)
			}

			released++
		}

		return nil
	})

	if err != nil {
		return 0, fmt.Errorf("failed to clear stale doer claims: %w", err)
	}

	return released, nil
}
