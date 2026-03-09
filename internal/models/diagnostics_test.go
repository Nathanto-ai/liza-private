package models

import (
	"strings"
	"testing"
	"time"
)

func TestCountClaimableTasks(t *testing.T) {
	tests := []struct {
		name  string
		state *State
		role  string
		want  int
	}{
		{
			name:  "empty state",
			state: &State{},
			role:  RoleCoder,
			want:  0,
		},
		{
			name: "one READY coding task for coder",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReady, Type: TaskTypeCoding},
				},
			},
			role: RoleCoder,
			want: 1,
		},
		{
			name: "READY task not claimable by reviewer",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReady, Type: TaskTypeCoding},
				},
			},
			role: RoleCodeReviewer,
			want: 0,
		},
		{
			name: "mixed statuses",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReady, Type: TaskTypeCoding},
					{ID: "t2", Status: TaskStatusImplementing, Type: TaskTypeCoding},
					{ID: "t3", Status: TaskStatusRejected, Type: TaskTypeCoding},
					{ID: "t4", Status: TaskStatusMerged, Type: TaskTypeCoding},
				},
			},
			role: RoleCoder,
			want: 2, // READY + REJECTED
		},
		{
			name: "READY_FOR_REVIEW claimable by reviewer",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReadyForReview, Type: TaskTypeCoding},
					{ID: "t2", Status: TaskStatusReadyForReview, Type: TaskTypeCoding},
				},
			},
			role: RoleCodeReviewer,
			want: 2,
		},
		{
			name: "blocked by unsatisfied dependency",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReady, Type: TaskTypeCoding, DependsOn: []string{"t2"}},
					{ID: "t2", Status: TaskStatusImplementing, Type: TaskTypeCoding},
				},
			},
			role: RoleCoder,
			want: 0,
		},
		{
			name: "dependency satisfied",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReady, Type: TaskTypeCoding, DependsOn: []string{"t2"}},
					{ID: "t2", Status: TaskStatusMerged, Type: TaskTypeCoding},
				},
			},
			role: RoleCoder,
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountClaimableTasks(tt.state, tt.role)
			if got != tt.want {
				t.Errorf("CountClaimableTasks() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCountReviewableTasks(t *testing.T) {
	tests := []struct {
		name  string
		state *State
		role  string
		want  int
	}{
		{
			name:  "empty state",
			state: &State{},
			role:  RoleCodeReviewer,
			want:  0,
		},
		{
			name: "one READY_FOR_REVIEW coding task",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReadyForReview, Type: TaskTypeCoding},
				},
			},
			role: RoleCodeReviewer,
			want: 1,
		},
		{
			name: "REVIEWING tasks not counted",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReviewing, Type: TaskTypeCoding},
				},
			},
			role: RoleCodeReviewer,
			want: 0,
		},
		{
			name: "wrong role not counted",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReadyForReview, Type: TaskTypeCoding},
				},
			},
			role: "planner",
			want: 0,
		},
		{
			name: "multiple reviewable tasks",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReadyForReview, Type: TaskTypeCoding},
					{ID: "t2", Status: TaskStatusReadyForReview, Type: TaskTypeCoding},
					{ID: "t3", Status: TaskStatusReady, Type: TaskTypeCoding},
				},
			},
			role: RoleCodeReviewer,
			want: 2,
		},
		{
			name: "READY_FOR_REVIEW blocked by unsatisfied dependency not counted",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReadyForReview, Type: TaskTypeCoding, DependsOn: []string{"t2"}},
					{ID: "t2", Status: TaskStatusImplementing, Type: TaskTypeCoding},
				},
			},
			role: RoleCodeReviewer,
			want: 0,
		},
		{
			name: "READY_FOR_REVIEW with satisfied dependency counted",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReadyForReview, Type: TaskTypeCoding, DependsOn: []string{"t2"}},
					{ID: "t2", Status: TaskStatusMerged, Type: TaskTypeCoding},
				},
			},
			role: RoleCodeReviewer,
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountReviewableTasks(tt.state, tt.role)
			if got != tt.want {
				t.Errorf("CountReviewableTasks() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGetCoderWorkDiagnostics(t *testing.T) {
	tests := []struct {
		name         string
		state        *State
		wantContains []string
	}{
		{
			name:         "empty state",
			state:        &State{},
			wantContains: []string{"No claimable tasks"},
		},
		{
			name: "claimable tasks found",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReady, Type: TaskTypeCoding},
					{ID: "t2", Status: TaskStatusReady, Type: TaskTypeCoding},
				},
			},
			wantContains: []string{"Found 2 claimable task(s)"},
		},
		{
			name: "blocked by dependencies reported",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReady, Type: TaskTypeCoding, DependsOn: []string{"t2"}},
					{ID: "t2", Status: TaskStatusImplementing, Type: TaskTypeCoding},
				},
			},
			wantContains: []string{"No claimable tasks", "1 blocked by dependencies"},
		},
		{
			name: "in-progress tasks reported",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusImplementing, Type: TaskTypeCoding},
					{ID: "t2", Status: TaskStatusReviewing, Type: TaskTypeCoding},
				},
			},
			wantContains: []string{"No claimable tasks", "2 in progress"},
		},
		{
			name: "both blocked and in-progress reported",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReady, Type: TaskTypeCoding, DependsOn: []string{"t3"}},
					{ID: "t2", Status: TaskStatusImplementing, Type: TaskTypeCoding},
					{ID: "t3", Status: TaskStatusApproved, Type: TaskTypeCoding},
				},
			},
			wantContains: []string{"No claimable tasks", "1 blocked by dependencies", "2 in progress"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetCoderWorkDiagnostics(tt.state)
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("GetCoderWorkDiagnostics() = %q, want it to contain %q", got, want)
				}
			}
		})
	}
}

func TestGetReviewerWorkDiagnostics(t *testing.T) {
	now := time.Now().UTC()
	pastTime := now.Add(-10 * time.Minute)
	futureTime := now.Add(10 * time.Minute)

	tests := []struct {
		name         string
		state        *State
		wantContains []string
	}{
		{
			name:         "empty state",
			state:        &State{},
			wantContains: []string{"No reviewable tasks"},
		},
		{
			name: "unassigned reviewable tasks",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReadyForReview, Type: TaskTypeCoding},
					{ID: "t2", Status: TaskStatusReadyForReview, Type: TaskTypeCoding},
				},
			},
			wantContains: []string{"Found 2 reviewable task(s)"},
		},
		{
			name: "expired lease reported alongside reviewable",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReadyForReview, Type: TaskTypeCoding},
					{ID: "t2", Status: TaskStatusReviewing, Type: TaskTypeCoding, ReviewLeaseExpires: &pastTime},
				},
			},
			wantContains: []string{"Found 1 reviewable task(s)", "1 with stale leases"},
		},
		{
			name: "expired lease with no reviewable",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReviewing, Type: TaskTypeCoding, ReviewLeaseExpires: &pastTime},
				},
			},
			wantContains: []string{"No reviewable tasks", "1 with stale leases"},
		},
		{
			name: "actively reviewing reported",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReviewing, Type: TaskTypeCoding, ReviewLeaseExpires: &futureTime},
				},
			},
			wantContains: []string{"No reviewable tasks", "1 actively being reviewed"},
		},
		{
			name: "reviewing with nil lease counts as active",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReviewing, Type: TaskTypeCoding},
				},
			},
			wantContains: []string{"No reviewable tasks", "1 actively being reviewed"},
		},
		{
			name: "READY_FOR_REVIEW blocked by dependency reported",
			state: &State{
				Tasks: []Task{
					{ID: "t1", Status: TaskStatusReadyForReview, Type: TaskTypeCoding, DependsOn: []string{"t2"}},
					{ID: "t2", Status: TaskStatusImplementing, Type: TaskTypeCoding},
				},
			},
			wantContains: []string{"No reviewable tasks", "READY_FOR_REVIEW but blocked by dependencies"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetReviewerWorkDiagnostics(tt.state)
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("GetReviewerWorkDiagnostics() = %q, want it to contain %q", got, want)
				}
			}
		})
	}
}
