package models

import (
	"fmt"
	"strings"
	"time"
)

// CountClaimableTasks counts tasks claimable by the given role.
// Uses IsClaimable which checks task type, status, and dependencies.
func CountClaimableTasks(state *State, role string) int {
	count := 0
	for i := range state.Tasks {
		if state.Tasks[i].IsClaimable(role, state.Tasks) {
			count++
		}
	}
	return count
}

// CountReviewableTasks counts tasks that are immediately claimable by a reviewer.
// Uses IsClaimable which checks task type, status, AND dependencies — ensuring the
// reviewer only wakes when there are tasks it can actually claim. Without the
// dependency check the reviewer enters a claim-fail loop that burns its iteration
// budget (see Bug #8).
func CountReviewableTasks(state *State, role string) int {
	count := 0
	for i := range state.Tasks {
		if state.Tasks[i].IsClaimable(role, state.Tasks) {
			count++
		}
	}
	return count
}

// GetCoderWorkDiagnostics returns detailed diagnostic information about task availability for coders.
func GetCoderWorkDiagnostics(state *State) string {
	claimable := CountClaimableTasks(state, RoleCoder)

	if claimable > 0 {
		return fmt.Sprintf("Found %d claimable task(s)", claimable)
	}

	blockedByDeps := 0
	inProgress := 0

	mergedIDs := make(map[string]bool)
	for _, task := range state.Tasks {
		if task.Status == TaskStatusMerged {
			mergedIDs[task.ID] = true
		}
	}

	for _, task := range state.Tasks {
		if task.Status == TaskStatusReady ||
			task.Status == TaskStatusRejected ||
			task.Status == TaskStatusIntegrationFailed {
			hasUnsatisfiedDeps := false
			for _, depID := range task.DependsOn {
				if !mergedIDs[depID] {
					hasUnsatisfiedDeps = true
					break
				}
			}
			if hasUnsatisfiedDeps {
				blockedByDeps++
			}
		}

		if task.Status == TaskStatusImplementing ||
			task.Status == TaskStatusReadyForReview ||
			task.Status == TaskStatusReviewing ||
			task.Status == TaskStatusApproved {
			inProgress++
		}
	}

	parts := []string{"No claimable tasks"}
	if blockedByDeps > 0 {
		parts = append(parts, fmt.Sprintf("%d blocked by dependencies", blockedByDeps))
	}
	if inProgress > 0 {
		parts = append(parts, fmt.Sprintf("%d in progress", inProgress))
	}

	return strings.Join(parts, "; ")
}

// GetReviewerWorkDiagnostics returns detailed diagnostic information about review availability.
func GetReviewerWorkDiagnostics(state *State) string {
	now := time.Now().UTC()

	claimable := 0
	blockedByDeps := 0
	expiredLeases := 0
	activelyReviewing := 0

	for i := range state.Tasks {
		task := &state.Tasks[i]
		if task.Status == TaskStatusReadyForReview && task.EffectiveType().HasRole(RoleCodeReviewer) {
			if task.IsClaimable(RoleCodeReviewer, state.Tasks) {
				claimable++
			} else {
				blockedByDeps++
			}
		}
		if task.Status == TaskStatusReviewing && task.EffectiveType().HasRole(RoleCodeReviewer) {
			if task.ReviewLeaseExpires != nil && task.ReviewLeaseExpires.Before(now) {
				expiredLeases++
			} else {
				activelyReviewing++
			}
		}
	}

	if claimable > 0 {
		parts := []string{fmt.Sprintf("Found %d reviewable task(s)", claimable)}
		if blockedByDeps > 0 {
			parts = append(parts, fmt.Sprintf("%d blocked by dependencies", blockedByDeps))
		}
		if expiredLeases > 0 {
			parts = append(parts, fmt.Sprintf("%d with stale leases (pending reclamation)", expiredLeases))
		}
		return strings.Join(parts, "; ")
	}

	parts := []string{"No reviewable tasks"}
	if blockedByDeps > 0 {
		parts = append(parts, fmt.Sprintf("%d READY_FOR_REVIEW but blocked by dependencies", blockedByDeps))
	}
	if expiredLeases > 0 {
		parts = append(parts, fmt.Sprintf("%d with stale leases (pending reclamation)", expiredLeases))
	}
	if activelyReviewing > 0 {
		parts = append(parts, fmt.Sprintf("%d actively being reviewed", activelyReviewing))
	}

	return strings.Join(parts, "; ")
}
