package agent

import (
	"fmt"

	"github.com/liza-mas/liza/internal/observability"
	"github.com/liza-mas/liza/internal/paths"
	"github.com/liza-mas/liza/internal/runtime"
)

// supervisorEmitter wraps the observability.Emitter to provide a
// non-fatal logging interface. If the emitter fails to initialize,
// events are silently dropped rather than crashing the supervisor.
type supervisorEmitter struct {
	emitter *observability.Emitter
}

// newSupervisorEmitter creates an emitter that writes to .liza/events.jsonl.
// Returns a no-op emitter if the file cannot be opened (logs a warning).
func newSupervisorEmitter(projectRoot string) *supervisorEmitter {
	lp := paths.New(projectRoot)
	emitter, err := observability.NewEmitter(lp.EventsLogPath())
	if err != nil {
		GetLogger().Warn("Failed to initialize event emitter, events will not be logged",
			"error", err)
		return &supervisorEmitter{}
	}
	return &supervisorEmitter{emitter: emitter}
}

// Emit writes an event. Errors are logged but do not propagate.
func (se *supervisorEmitter) Emit(event observability.Event) {
	if se.emitter == nil {
		return
	}
	if err := se.emitter.Emit(event); err != nil {
		GetLogger().Warn("Failed to emit event", "error", err, "type", event.Type)
	}
}

// Close closes the underlying emitter.
func (se *supervisorEmitter) Close() {
	if se.emitter != nil {
		if err := se.emitter.Close(); err != nil {
			GetLogger().Warn("Failed to close event emitter", "error", err)
		}
	}
}

// emitAgentRegistered emits an AGENT_CLAIMED event for agent registration.
func (se *supervisorEmitter) emitAgentRegistered(agentID, role string) {
	se.Emit(observability.NewEvent(observability.EventAgentClaimed, "Agent registered").
		WithAgent(agentID).
		WithData("role", role))
}

// emitAgentReleased emits an AGENT_RELEASED event for agent deregistration.
func (se *supervisorEmitter) emitAgentReleased(agentID string) {
	se.Emit(observability.NewEvent(observability.EventAgentReleased, "Agent deregistered").
		WithAgent(agentID))
}

// emitTaskClaimed emits a TASK_STATUS_CHANGED event when a task is claimed.
func (se *supervisorEmitter) emitTaskClaimed(agentID, taskID string) {
	se.Emit(observability.NewEvent(observability.EventTaskStatusChanged, "Task claimed").
		WithAgent(agentID).
		WithTask(taskID).
		WithData("new_status", "IMPLEMENTING"))
}

// emitVerifyRun emits a VERIFY_RUN event with the verification result.
func (se *supervisorEmitter) emitVerifyRun(taskID string, passed bool, commandCount int) {
	result := "PASS"
	if !passed {
		result = "FAIL"
	}
	se.Emit(observability.NewEvent(observability.EventVerifyRun, "Post-submission verification").
		WithTask(taskID).
		WithData("result", result).
		WithData("commands", fmt.Sprintf("%d", commandCount)))
}

// emitAgentExited emits an AGENT_EXITED event when an agent exits cleanly.
func (se *supervisorEmitter) emitAgentExited(agentID string, exitCode int) {
	se.Emit(observability.NewEvent(observability.EventAgentExited, "Agent exited").
		WithAgent(agentID).
		WithData("exit_code", fmt.Sprintf("%d", exitCode)))
}

// emitBudgetExceeded emits a BUDGET_EXCEEDED event when a budget limit is hit.
func (se *supervisorEmitter) emitBudgetExceeded(agentID, reason string) {
	se.Emit(observability.NewEvent(observability.EventBudgetExceeded, "Budget limit exceeded").
		WithAgent(agentID).
		WithData("reason", reason))
}

// emitAnomalyDetected emits an ANOMALY_DETECTED event when stagnation or
// a no-diff retry pattern is found by the AnomalyDetector.
func (se *supervisorEmitter) emitAnomalyDetected(agentID string, a runtime.Anomaly) {
	se.Emit(observability.NewEvent(observability.EventAnomalyDetected, a.Description).
		WithAgent(agentID).
		WithData("anomaly_type", string(a.Type)).
		WithData("severity", a.Severity))
}

// emitCrashRetry emits an AGENT_CRASHED event when a crash retry occurs.
func (se *supervisorEmitter) emitCrashRetry(agentID, taskID string, crashes int) {
	se.Emit(observability.NewEvent(observability.EventAgentCrashed, "Crash retry").
		WithAgent(agentID).
		WithTask(taskID).
		WithData("consecutive_crashes", fmt.Sprintf("%d", crashes)))
}

// emitAgentAborted emits an AGENT_ABORTED event for exit code 42 (graceful abort).
func (se *supervisorEmitter) emitAgentAborted(agentID, taskID string, restartCount int) {
	se.Emit(observability.NewEvent(observability.EventAgentAborted, "Agent aborted gracefully").
		WithAgent(agentID).
		WithTask(taskID).
		WithData("restart_count", fmt.Sprintf("%d", restartCount)))
}

// emitCrashLimitExceeded emits an AGENT_CRASHED event when crash retry limit is exceeded.
func (se *supervisorEmitter) emitCrashLimitExceeded(agentID, taskID string, crashes, limit int) {
	se.Emit(observability.NewEvent(observability.EventAgentCrashed, "Crash retry limit exceeded").
		WithAgent(agentID).
		WithTask(taskID).
		WithData("consecutive_crashes", fmt.Sprintf("%d", crashes)).
		WithData("limit", fmt.Sprintf("%d", limit)))
}
