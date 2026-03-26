package runtime

import (
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
)

func TestBudgetTracker_Iterations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		maxIterations int
		recordCount   int
		wantErr       bool
	}{
		{
			name:          "under limit passes",
			maxIterations: 5,
			recordCount:   3,
			wantErr:       false,
		},
		{
			name:          "at limit fails",
			maxIterations: 5,
			recordCount:   5,
			wantErr:       true,
		},
		{
			name:          "over limit fails",
			maxIterations: 5,
			recordCount:   7,
			wantErr:       true,
		},
		{
			name:          "zero limit uses default",
			maxIterations: 0,
			recordCount:   3,
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bt := NewBudgetTracker(tt.maxIterations, 100, time.Hour)
			for i := 0; i < tt.recordCount; i++ {
				bt.RecordIteration()
			}

			err := bt.Check(bt.StartTime) // no runtime elapsed
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if bt.Iterations() != tt.recordCount {
				t.Errorf("Iterations() = %d, want %d", bt.Iterations(), tt.recordCount)
			}
		})
	}
}

func TestBudgetTracker_Tasks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		maxTasks  int
		taskCount int
		wantErr   bool
	}{
		{
			name:      "under limit passes",
			maxTasks:  10,
			taskCount: 5,
			wantErr:   false,
		},
		{
			name:      "at limit fails",
			maxTasks:  3,
			taskCount: 3,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bt := NewBudgetTracker(100, tt.maxTasks, time.Hour)
			for i := 0; i < tt.taskCount; i++ {
				bt.RecordTaskGenerated()
			}

			err := bt.Check(bt.StartTime) // no runtime elapsed
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if bt.TasksGenerated() != tt.taskCount {
				t.Errorf("TasksGenerated() = %d, want %d", bt.TasksGenerated(), tt.taskCount)
			}
		})
	}
}

func TestBudgetTracker_Runtime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		maxRuntime time.Duration
		elapsed    time.Duration
		wantErr    bool
	}{
		{
			name:       "within runtime passes",
			maxRuntime: time.Hour,
			elapsed:    30 * time.Minute,
			wantErr:    false,
		},
		{
			name:       "exceeded runtime fails",
			maxRuntime: 10 * time.Minute,
			elapsed:    15 * time.Minute,
			wantErr:    true,
		},
		{
			name:       "exactly at limit fails",
			maxRuntime: 10 * time.Minute,
			elapsed:    10 * time.Minute,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bt := NewBudgetTracker(100, 100, tt.maxRuntime)
			now := bt.StartTime.Add(tt.elapsed)

			err := bt.Check(now)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestNewBudgetTracker_Defaults(t *testing.T) {
	t.Parallel()

	bt := NewBudgetTracker(0, 0, 0)
	if bt.MaxIterations != DefaultMaxIterations {
		t.Errorf("MaxIterations = %d, want %d", bt.MaxIterations, DefaultMaxIterations)
	}
	if bt.MaxTasks != DefaultMaxTasksPerRun {
		t.Errorf("MaxTasks = %d, want %d", bt.MaxTasks, DefaultMaxTasksPerRun)
	}
	if bt.MaxRuntime != DefaultMaxRuntime {
		t.Errorf("MaxRuntime = %v, want %v", bt.MaxRuntime, DefaultMaxRuntime)
	}
}

func TestNewBudgetTrackerFromConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		cfg            models.Config
		wantIterations int
		wantTasks      int
		wantRuntime    time.Duration
	}{
		{
			name: "explicit values used",
			cfg: models.Config{
				MaxAgentIterations: 30,
				MaxTasksGenerated:  15,
				MaxRuntimeMinutes:  90,
			},
			wantIterations: 30,
			wantTasks:      15,
			wantRuntime:    90 * time.Minute,
		},
		{
			name:           "zero values use defaults",
			cfg:            models.Config{},
			wantIterations: DefaultMaxIterations,
			wantTasks:      DefaultMaxTasksPerRun,
			wantRuntime:    DefaultMaxRuntime,
		},
		{
			name: "partial config uses defaults for missing",
			cfg: models.Config{
				MaxAgentIterations: 50,
			},
			wantIterations: 50,
			wantTasks:      DefaultMaxTasksPerRun,
			wantRuntime:    DefaultMaxRuntime,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			bt := NewBudgetTrackerFromConfig(tt.cfg)
			if bt.MaxIterations != tt.wantIterations {
				t.Errorf("MaxIterations = %d, want %d", bt.MaxIterations, tt.wantIterations)
			}
			if bt.MaxTasks != tt.wantTasks {
				t.Errorf("MaxTasks = %d, want %d", bt.MaxTasks, tt.wantTasks)
			}
			if bt.MaxRuntime != tt.wantRuntime {
				t.Errorf("MaxRuntime = %v, want %v", bt.MaxRuntime, tt.wantRuntime)
			}
		})
	}
}

func TestBudgetTracker_CheckWithWarning_NoWarning(t *testing.T) {
	t.Parallel()

	bt := NewBudgetTracker(20, 10, time.Hour)
	// 3 of 20 iterations = 15%, well under 80%
	for i := 0; i < 3; i++ {
		bt.RecordIteration()
	}

	warning, err := bt.CheckWithWarning(bt.StartTime, 0.8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warning != nil {
		t.Errorf("expected no warning at 15%%, got: %s", warning.Message)
	}
}

func TestBudgetTracker_CheckWithWarning_IterationWarning(t *testing.T) {
	t.Parallel()

	bt := NewBudgetTracker(20, 10, time.Hour)
	// 16 of 20 iterations = 80%, at threshold
	for i := 0; i < 16; i++ {
		bt.RecordIteration()
	}

	warning, err := bt.CheckWithWarning(bt.StartTime, 0.8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warning == nil {
		t.Fatal("expected warning at 80%, got nil")
	}
	if !warning.IterationWarning {
		t.Error("expected IterationWarning=true")
	}
	if warning.Message == "" {
		t.Error("expected non-empty warning message")
	}
}

func TestBudgetTracker_CheckWithWarning_RuntimeWarning(t *testing.T) {
	t.Parallel()

	bt := NewBudgetTracker(100, 100, time.Hour)
	// Simulate 50 minutes into a 60 minute budget = 83%
	now := bt.StartTime.Add(50 * time.Minute)

	warning, err := bt.CheckWithWarning(now, 0.8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warning == nil {
		t.Fatal("expected warning at 83% runtime, got nil")
	}
	if !warning.RuntimeWarning {
		t.Error("expected RuntimeWarning=true")
	}
}

func TestBudgetTracker_CheckWithWarning_Exceeded(t *testing.T) {
	t.Parallel()

	bt := NewBudgetTracker(5, 100, time.Hour)
	for i := 0; i < 5; i++ {
		bt.RecordIteration()
	}

	warning, err := bt.CheckWithWarning(bt.StartTime, 0.8)
	if err == nil {
		t.Fatal("expected error for exceeded budget, got nil")
	}
	if warning != nil {
		t.Errorf("expected nil warning when budget exceeded, got: %v", warning)
	}
}
