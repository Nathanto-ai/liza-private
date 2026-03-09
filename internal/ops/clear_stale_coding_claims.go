package ops

import (
	"fmt"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/log"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
)

// ClearStaleCodingClaims finds IMPLEMENTING tasks whose coder lease has
// expired and releases them back to READY so another coder can pick them up.
// This prevents tasks from being permanently stuck when a coder process dies.
// Returns the number of claims released.
func ClearStaleCodingClaims(projectRoot string) (int, error) {
	lp := paths.New(projectRoot)
	bb := db.For(lp.StatePath())
	logger := log.New(lp.LogPath())

	released := 0
	now := time.Now().UTC()

	err := bb.Modify(func(state *models.State) error {
		for i := range state.Tasks {
			task := &state.Tasks[i]

			if task.Status != models.TaskStatusImplementing {
				continue
			}
			if task.AssignedTo == nil {
				continue
			}

			isExpired := false
			var staleAgent string
			var expiredAt string

			if task.LeaseExpires == nil {
				// Malformed: assigned but no lease
				isExpired = true
				staleAgent = *task.AssignedTo
				expiredAt = "unknown (lease missing)"
			} else if task.LeaseExpires.Before(now) || task.LeaseExpires.Equal(now) {
				isExpired = true
				staleAgent = *task.AssignedTo
				expiredAt = task.LeaseExpires.Format(time.RFC3339)
			}

			if !isExpired {
				continue
			}

			// Release: transition back to READY and clear assignment
			if err := task.Transition(models.TaskStatusReady); err != nil {
				return err
			}
			task.AssignedTo = nil
			task.LeaseExpires = nil

			detail := fmt.Sprintf("Coder claim expired at %s (agent: %s)", expiredAt, staleAgent)

			logEntry := log.Entry{
				Timestamp: now,
				Agent:     "system",
				Action:    "stale_coding_claim_cleared",
				Task:      &task.ID,
				Detail:    detail,
			}
			if err := logger.Append(logEntry); err != nil {
				return fmt.Errorf("failed to log stale coding claim cleanup for %s: %w", task.ID, err)
			}

			released++
		}

		return nil
	})

	if err != nil {
		return 0, fmt.Errorf("failed to clear stale coding claims: %w", err)
	}

	return released, nil
}
