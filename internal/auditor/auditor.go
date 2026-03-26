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
	// AuditPhasePostMerge is after a task has been merged (implementation quality check).
	AuditPhasePostMerge AuditPhase = "post_merge"
)

// FindAuditTarget selects a task that needs auditing from the current state.
// Returns nil if no work is available.
//
// Selection priority:
// 1. MERGED tasks that have not been post-merge audited (implementation quality check)
// 2. READY_FOR_REVIEW tasks that have not been post-execution audited
// 3. READY tasks that have not been pre-execution audited
func FindAuditTarget(state *models.State) *AuditTarget {
	auditedTasks := buildAuditedSet(state.AuditFindings)

	// Priority 1: Post-merge audit for merged tasks
	for _, task := range state.Tasks {
		if task.Status == models.TaskStatusMerged {
			if !isPhaseAudited(auditedTasks, task.ID, string(AuditPhasePostMerge)) {
				return &AuditTarget{
					TaskID: task.ID,
					Phase:  AuditPhasePostMerge,
				}
			}
		}
	}

	// Priority 2: Post-execution audit for tasks ready for review
	for _, task := range state.Tasks {
		if task.Status == models.TaskStatusReadyForReview {
			if !isPhaseAudited(auditedTasks, task.ID, string(AuditPhasePostExecution)) {
				return &AuditTarget{
					TaskID: task.ID,
					Phase:  AuditPhasePostExecution,
				}
			}
		}
	}

	// Priority 3: Pre-execution audit for ready tasks
	for _, task := range state.Tasks {
		if task.Status == models.TaskStatusReady {
			if !isPhaseAudited(auditedTasks, task.ID, string(AuditPhasePreExecution)) {
				return &AuditTarget{
					TaskID: task.ID,
					Phase:  AuditPhasePreExecution,
				}
			}
		}
	}

	return nil
}

// buildAuditedSet returns a per-phase audit map: taskID → phase → true.
// A task must be audited in each phase independently.
func buildAuditedSet(findings []models.AuditFinding) map[string]map[string]bool {
	audited := make(map[string]map[string]bool)
	for _, f := range findings {
		if audited[f.TaskID] == nil {
			audited[f.TaskID] = make(map[string]bool)
		}
		if f.Phase != "" {
			audited[f.TaskID][f.Phase] = true
		} else {
			// Legacy findings without phase: mark as "any" so they count for
			// backward compatibility but don't block per-phase checks.
			audited[f.TaskID]["_legacy"] = true
		}
	}
	return audited
}

// isPhaseAudited checks if a task has been audited for a specific phase.
func isPhaseAudited(audited map[string]map[string]bool, taskID, phase string) bool {
	phases, ok := audited[taskID]
	if !ok {
		return false
	}
	return phases[phase]
}

// NewFinding creates a new AuditFinding with the given parameters and the current timestamp.
func NewFinding(id, taskID, severity, findingType, evidence, recommendedAction, specRef, phase, classification string) models.AuditFinding {
	return models.AuditFinding{
		ID:                id,
		TaskID:            taskID,
		Severity:          severity,
		Type:              findingType,
		Phase:             phase,
		Classification:    classification,
		SpecReference:     specRef,
		Evidence:          evidence,
		RecommendedAction: recommendedAction,
		Created:           time.Now().UTC(),
	}
}
