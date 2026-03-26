// Package runtime provides runaway prevention mechanisms including budget
// tracking, anomaly detection, and circuit-breaker logic for multi-agent runs.
package runtime

import (
	"fmt"
	"time"

	"github.com/liza-mas/liza/internal/models"
)

// Default budget limits applied when config values are zero.
const (
	DefaultMaxIterations  = 20
	DefaultMaxTasksPerRun = 10
	DefaultMaxRuntime     = 60 * time.Minute
)

// BudgetTracker monitors resource consumption against configured limits and
// returns errors when any budget is exceeded.
type BudgetTracker struct {
	MaxIterations int
	MaxTasks      int
	MaxRuntime    time.Duration
	StartTime     time.Time

	iterations     int
	tasksGenerated int
}

// NewBudgetTracker creates a tracker with the given limits.
// Zero values for limits are replaced with defaults.
func NewBudgetTracker(maxIterations, maxTasks int, maxRuntime time.Duration) *BudgetTracker {
	if maxIterations <= 0 {
		maxIterations = DefaultMaxIterations
	}
	if maxTasks <= 0 {
		maxTasks = DefaultMaxTasksPerRun
	}
	if maxRuntime <= 0 {
		maxRuntime = DefaultMaxRuntime
	}
	return &BudgetTracker{
		MaxIterations: maxIterations,
		MaxTasks:      maxTasks,
		MaxRuntime:    maxRuntime,
		StartTime:     time.Now().UTC(),
	}
}

// NewBudgetTrackerFromConfig creates a BudgetTracker from the state.yaml Config
// section. Zero/unset config values fall through to defaults via NewBudgetTracker.
func NewBudgetTrackerFromConfig(cfg models.Config) *BudgetTracker {
	return NewBudgetTracker(
		cfg.MaxAgentIterations,
		cfg.MaxTasksGenerated,
		time.Duration(cfg.MaxRuntimeMinutes)*time.Minute,
	)
}

// RecordIteration increments the iteration counter.
func (bt *BudgetTracker) RecordIteration() {
	bt.iterations++
}

// RecordTaskGenerated increments the task generation counter.
func (bt *BudgetTracker) RecordTaskGenerated() {
	bt.tasksGenerated++
}

// Iterations returns the current iteration count.
func (bt *BudgetTracker) Iterations() int {
	return bt.iterations
}

// TasksGenerated returns the current task generation count.
func (bt *BudgetTracker) TasksGenerated() int {
	return bt.tasksGenerated
}

// Check returns an error if any budget limit has been exceeded.
// The now parameter allows deterministic testing of runtime limits.
func (bt *BudgetTracker) Check(now time.Time) error {
	if bt.iterations >= bt.MaxIterations {
		return fmt.Errorf("iteration budget exceeded: %d/%d", bt.iterations, bt.MaxIterations)
	}
	if bt.tasksGenerated >= bt.MaxTasks {
		return fmt.Errorf("task generation budget exceeded: %d/%d", bt.tasksGenerated, bt.MaxTasks)
	}
	elapsed := now.Sub(bt.StartTime)
	if elapsed >= bt.MaxRuntime {
		return fmt.Errorf("runtime budget exceeded: %v/%v", elapsed.Truncate(time.Second), bt.MaxRuntime)
	}
	return nil
}

// BudgetWarning describes which budget dimensions are approaching their limit.
type BudgetWarning struct {
	IterationWarning bool
	TaskWarning      bool
	RuntimeWarning   bool
	Message          string
}

// CheckWithWarning checks budgets and returns a warning if any dimension
// exceeds the given threshold (e.g. 0.8 for 80%), plus an error if any
// budget is fully exceeded. Both can be non-nil simultaneously.
func (bt *BudgetTracker) CheckWithWarning(now time.Time, warnThreshold float64) (*BudgetWarning, error) {
	// Hard check first
	if err := bt.Check(now); err != nil {
		return nil, err
	}

	var w BudgetWarning
	if bt.MaxIterations > 0 && float64(bt.iterations)/float64(bt.MaxIterations) >= warnThreshold {
		w.IterationWarning = true
	}
	if bt.MaxTasks > 0 && float64(bt.tasksGenerated)/float64(bt.MaxTasks) >= warnThreshold {
		w.TaskWarning = true
	}
	elapsed := now.Sub(bt.StartTime)
	if bt.MaxRuntime > 0 && float64(elapsed)/float64(bt.MaxRuntime) >= warnThreshold {
		w.RuntimeWarning = true
	}

	if w.IterationWarning || w.TaskWarning || w.RuntimeWarning {
		w.Message = fmt.Sprintf("budget warning (%.0f%% threshold): iterations=%d/%d tasks=%d/%d runtime=%v/%v",
			warnThreshold*100,
			bt.iterations, bt.MaxIterations,
			bt.tasksGenerated, bt.MaxTasks,
			elapsed.Truncate(time.Second), bt.MaxRuntime)
		return &w, nil
	}

	return nil, nil
}
