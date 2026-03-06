// Package auditor provides auditor agent functionality for evaluating work
// quality and spec compliance. The auditor produces advisory findings but does
// not make PASS/FAIL decisions — the supervisor retains deterministic control.
package auditor

import (
	"time"

	"github.com/liza-mas/liza/internal/models"
)

// AuditTarget represents a task selected for auditing.
type AuditTarget struct {
	TaskID string
	Phase  AuditPhase
}

// AuditPhase indicates when the audit occurs in the task lifecycle.
type AuditPhase string

const (
	// AuditPhasePreExecution is before a coder starts work.
	AuditPhasePreExecution AuditPhase = "pre_execution"
	// AuditPhasePostExecution is after a coder submits work for review.
	AuditPhasePostExecution AuditPhase = "post_execution"
)

// FindAuditTarget selects a task that needs auditing from the current state.
// Returns nil if no work is available.
//
// Selection priority:
// 1. READY_FOR_REVIEW tasks that have not been audited (post-execution audit)
// 2. READY tasks that have not been audited (pre-execution audit)
func FindAuditTarget(state *models.State) *AuditTarget {
	auditedTasks := buildAuditedSet(state.AuditFindings)

	// Priority 1: Post-execution audit for tasks ready for review
	for _, task := range state.Tasks {
		if task.Status == models.TaskStatusReadyForReview {
			if !auditedTasks[task.ID] {
				return &AuditTarget{
					TaskID: task.ID,
					Phase:  AuditPhasePostExecution,
				}
			}
		}
	}

	// Priority 2: Pre-execution audit for ready tasks
	for _, task := range state.Tasks {
		if task.Status == models.TaskStatusReady {
			if !auditedTasks[task.ID] {
				return &AuditTarget{
					TaskID: task.ID,
					Phase:  AuditPhasePreExecution,
				}
			}
		}
	}

	return nil
}

// buildAuditedSet returns a set of task IDs that have already been audited.
func buildAuditedSet(findings []models.AuditFinding) map[string]bool {
	audited := make(map[string]bool)
	for _, f := range findings {
		audited[f.TaskID] = true
	}
	return audited
}

// NewFinding creates a new AuditFinding with the given parameters and the current timestamp.
func NewFinding(id, taskID, severity, findingType, evidence, recommendedAction, specRef string) models.AuditFinding {
	return models.AuditFinding{
		ID:                id,
		TaskID:            taskID,
		Severity:          severity,
		Type:              findingType,
		SpecReference:     specRef,
		Evidence:          evidence,
		RecommendedAction: recommendedAction,
		Created:           time.Now().UTC(),
	}
}
