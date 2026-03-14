package ops

import (
	"fmt"
	"strings"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
)

// WriteCheckpointInput contains the parameters for writing a pre-execution checkpoint.
type WriteCheckpointInput struct {
	TaskID         string
	AgentID        string
	Intent         string
	ValidationPlan string
	FilesToModify  []string
	Assumptions    []string
	Risks          string
	TDDNotRequired string
}

// WriteCheckpoint writes a pre-execution checkpoint to a task's history.
// The checkpoint must be written before submitting for review.
func WriteCheckpoint(projectRoot string, input *WriteCheckpointInput) error {
	if input.TaskID == "" {
		return fmt.Errorf("task_id is required")
	}
	if input.AgentID == "" {
		return fmt.Errorf("agent_id is required")
	}
	if input.Intent == "" {
		return fmt.Errorf("intent is required")
	}
	if input.ValidationPlan == "" {
		return fmt.Errorf("validation_plan is required")
	}
	if len(input.FilesToModify) == 0 {
		return fmt.Errorf("files_to_modify is required (at least one file)")
	}

	lp := paths.New(projectRoot)
	bb := db.For(lp.StatePath())

	now := time.Now().UTC()

	return bb.Modify(func(state *models.State) error {
		task := state.FindTask(input.TaskID)
		if task == nil {
			return fmt.Errorf("task %s not found", input.TaskID)
		}

		if task.Status == models.TaskStatusSuperseded {
			replacements := "unknown"
			if len(task.SupersededBy) > 0 {
				replacements = strings.Join(task.SupersededBy, ", ")
			}
			return &PreconditionError{
				Reason: fmt.Sprintf("task %s was SUPERSEDED (replaced by: %s) — stop work on this task and exit with code 0", input.TaskID, replacements),
			}
		}

		if task.Status != models.TaskStatusImplementing {
			return fmt.Errorf("task %s is not IMPLEMENTING (current status: %s)", input.TaskID, task.Status)
		}

		if task.AssignedTo == nil || *task.AssignedTo != input.AgentID {
			currentAgent := "none"
			if task.AssignedTo != nil {
				currentAgent = *task.AssignedTo
			}
			return fmt.Errorf("task %s is not assigned to agent %s (currently assigned to: %s)", input.TaskID, input.AgentID, currentAgent)
		}

		extra := map[string]any{
			"intent":          input.Intent,
			"validation_plan": input.ValidationPlan,
			"files_to_modify": input.FilesToModify,
		}
		if len(input.Assumptions) > 0 {
			extra["assumptions"] = input.Assumptions
		}
		if input.Risks != "" {
			extra["risks"] = input.Risks
		}
		if input.TDDNotRequired != "" {
			extra["tdd_not_required"] = input.TDDNotRequired
		}

		agentPtr := &input.AgentID
		task.History = append(task.History, models.TaskHistoryEntry{
			Time:  now,
			Event: "pre_execution_checkpoint",
			Agent: agentPtr,
			Extra: extra,
		})

		return nil
	})
}

// HasCheckpoint checks whether a task's history contains a pre_execution_checkpoint
// event from the specified agent.
func HasCheckpoint(history []models.TaskHistoryEntry, agentID string) bool {
	for _, entry := range history {
		if entry.Event == "pre_execution_checkpoint" && entry.Agent != nil && *entry.Agent == agentID {
			return true
		}
	}
	return false
}

// GetTDDWaiver returns the tdd_not_required justification from the latest
// pre_execution_checkpoint by agentID, or "" if none was declared.
func GetTDDWaiver(history []models.TaskHistoryEntry, agentID string) string {
	for i := len(history) - 1; i >= 0; i-- {
		entry := history[i]
		if entry.Event == "pre_execution_checkpoint" && entry.Agent != nil && *entry.Agent == agentID {
			if v, ok := entry.Extra["tdd_not_required"].(string); ok {
				return v
			}
			return ""
		}
	}
	return ""
}
