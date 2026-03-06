# Testing Guide

Running tests, coverage targets, and test utilities for the Liza Go codebase.

## Running Tests

### Quick Reference

| Command | What |
|---------|------|
| `go test ./...` | All tests |
| `go test -v ./...` | Verbose |
| `go test -race ./...` | Race detector |
| `go test -cover ./...` | Coverage summary |
| `go test -run TestFoo ./internal/db/` | Specific test |
| `go test -run "TestBlackboard.*" ./internal/db/` | Pattern match |
| `go test -short ./...` | Run tests with short-mode enabled (integration tests in `internal/integration/` are skipped via `testing.Short()` guards) |

### Coverage

```bash
go test -coverprofile=coverage.out ./...    # Generate profile
go tool cover -html=coverage.out            # View in browser
go tool cover -func=coverage.out            # Coverage by function
```

### Race Detection

**Always run with `-race` before merging code that uses goroutines or locks.**

```bash
go test -race ./...                # All packages
go test -race ./internal/db/       # Specific package (db has locks)
go test -race ./internal/mcp/      # MCP server (concurrent handlers)
```

### Benchmarks

```bash
go test -bench=. ./internal/db/                          # Run all
go test -bench=BenchmarkRead ./internal/db/               # Specific
go test -bench=. -benchmem ./internal/db/                 # Memory stats
go test -bench=. -benchtime=10s -count=5 ./internal/db/   # Stability
```

See [PERFORMANCE.md](PERFORMANCE.md) for benchmark targets and regression detection.

## Test Organization

```
internal/
├── db/
│   ├── blackboard.go           # Production code
│   ├── blackboard_test.go      # Unit tests
│   ├── metrics.go / metrics_test.go
│   ├── watcher.go / watcher_test.go
│   └── errors.go / errors_test.go
├── commands/
│   ├── add_task.go / add_task_test.go
│   ├── validate.go / validate_test.go
│   └── ... (each command has its test)
├── models/
│   └── state.go / state_test.go
├── mcp/
│   ├── server.go / server_test.go
│   ├── handlers.go / handlers_test.go
│   └── concurrency_test.go
├── git/
│   └── worktree.go / worktree_test.go
├── specvalidate/              # Spec validation (delivery/vision)
│   ├── validate.go / validate_test.go
│   ├── parser.go / parser_test.go
│   └── sections.go
├── statevalidate/             # State validators (incl. task quality gate)
│   ├── validate.go
│   └── task_quality.go / task_quality_test.go
├── verify/                    # Deterministic verification executor
│   └── executor.go / executor_test.go
├── auditor/                   # Auditor work selection & findings
│   └── auditor.go / auditor_test.go
├── planner/                   # Task creation policy, dedup, budget
│   └── task_policy.go / task_policy_test.go
├── runtime/                   # Budget tracking & anomaly detection
│   ├── budgets.go / budgets_test.go
│   └── anomaly_detector.go / anomaly_detector_test.go
├── observability/             # Structured event logging (JSONL)
│   ├── events.go
│   ├── emitter.go
│   ├── reader.go
│   └── observability_test.go
├── testhelpers/               # Shared test utilities
│   ├── fixtures.go            # State/task factory functions
│   ├── setup.go               # Git repo, .liza dir, worktree setup
│   ├── assertions.go          # Test assertion helpers
│   ├── git.go                 # Git operation helpers
│   └── utils.go               # Misc utilities
└── integration/               # End-to-end tests
    ├── e2e_workflow_test.go
    ├── concurrent_operations_test.go
    ├── lease_expiry_test.go
    ├── sprint_and_merge_test.go
    └── closed_loop_test.go    # Closed-loop integration (spec→audit→task→verify)
```

## Coverage Targets

**Project standards**: 80% minimum for core packages, 80% overall target.

| Package | Coverage | Status |
|---------|----------|--------|
| internal/prompts | 100% | Excellent |
| internal/analysis | ~96% | Excellent |
| internal/models | ~93% | Excellent |
| internal/git | ~88% | Very Good |
| internal/commands | ~84% | Very Good |
| internal/db | ~83% | Very Good |
| internal/specvalidate | — | New (table-driven) |
| internal/statevalidate | — | New (quality gate) |
| internal/verify | — | New (executor) |
| internal/auditor | — | New (audit target + findings) |
| internal/planner | — | New (task policy + dedup) |
| internal/runtime | — | New (budgets + anomaly) |
| internal/observability | — | New (JSONL emitter/reader) |
| internal/agent | ~62% | Adequate |

**Note**: High coverage != good tests. Tests must validate requirements, not just exercise code. Focus on critical paths (state machine, locking, validation), error handling, edge cases (empty lists, nil values, concurrent access), and integration points.

## Test Utilities

The `internal/testhelpers` package provides utilities for writing tests.

### Test State Fixtures

```go
import "github.com/liza-mas/liza/internal/testhelpers"

// Complete valid state with sensible defaults
state := testhelpers.CreateValidState()

// Customize as needed
state.Goal.Description = "Custom goal"
state.Tasks = []models.Task{
    testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReady, time.Now().UTC()),
}

// Write state to blackboard
tmpDir := t.TempDir()
statePath, _ := testhelpers.SetupLizaDir(t, tmpDir)
bb := testhelpers.WriteInitialState(t, statePath, state)
```

### Task Fixtures by Status

```go
now := time.Now().UTC()

// BuildTaskByStatus sets all required fields for the given status
task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now)
// IMPLEMENTING task has AssignedTo, LeaseExpires, BaseCommit, Worktree set

task := testhelpers.BuildTaskByStatus("task-2", models.TaskStatusBlocked, now)
// BLOCKED task has BlockedReason, BlockedQuestions set

// Pointer helpers for optional fields
task.AssignedTo = testhelpers.StringPtr("coder-1")
task.LeaseExpires = testhelpers.TimePtr(now.Add(30 * time.Minute))
```

### Setup Helpers

```go
dir := t.TempDir()  // Automatic cleanup, parallel-safe

// Git repo with initial commit + integration branch
testhelpers.SetupTestGitRepo(t, dir)

// .liza directory with state.yaml and lock file
statePath, lockPath := testhelpers.SetupLizaDir(t, dir)

// Worktree directory (filesystem only, not git worktree)
testhelpers.CreateTestWorktree(t, dir, "task-1")

// Spec file
testhelpers.CreateSpecFile(t, dir, "vision.md", "# Vision\nTest spec")

// Register agent in blackboard
testhelpers.RegisterTestAgent(t, bb, "coder-1", "coder")
```

### Time Helpers

```go
// Fixed time for reproducible tests
now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

// For time-based tests, truncate to avoid flakiness
now := time.Now().UTC().Truncate(time.Second)
```

## Writing Tests

### Table-Driven Tests (Preferred Pattern)

```go
func TestAddTaskCommand(t *testing.T) {
    tests := []struct {
        name        string
        taskID      string
        wantErr     bool
        errContains string
    }{
        {name: "add basic task", taskID: "task-1", wantErr: false},
        {name: "duplicate ID", taskID: "task-1", wantErr: true, errContains: "already exists"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            dir := t.TempDir()
            bb := setupTestBlackboard(t, dir)

            err := AddTaskCommand(/* ... */)

            if tt.wantErr {
                if err == nil {
                    t.Errorf("Expected error containing %q, got nil", tt.errContains)
                }
            } else if err != nil {
                t.Errorf("Unexpected error: %v", err)
            }
        })
    }
}
```

### Assertion Guidelines

```go
// Use Fatal for setup failures (stops test immediately)
state, err := bb.Read()
if err != nil {
    t.Fatalf("Failed to read state: %v", err)
}

// Use Error for assertions (continues to report all failures)
if state.Config.Mode != models.SystemModeRunning {
    t.Errorf("Expected RUNNING mode, got %s", state.Config.Mode)
}
```

### Integration Tests

Located in `internal/integration/`. These tests are long-running end-to-end checks and skip automatically when `go test -short` is used.

```go
func TestFullWorkflow(t *testing.T) {
    dir := t.TempDir()
    // Set up real git repo, init liza, add tasks, claim, submit, review, merge
}
```

Integration tests cover: init -> add-task -> claim -> submit -> review -> merge, multiple agent interaction, concurrent operations, lease expiry, sprint lifecycle, and closed-loop improvement (spec → audit → task → verify).

### Closed-Loop Integration Tests

`internal/integration/closed_loop_test.go` contains cross-cutting tests that verify the full improvement pipeline working together:

- **FindingToTask**: auditor finding → task proposal → dedup → budget check → observability
- **DeduplicationPreventsRedundantTasks**: duplicate findings are suppressed
- **BudgetPreventsRunaway**: budget limits halt task generation
- **BudgetTrackerIntegration**: iteration, task, and time budget enforcement
- **AnomalyDetectorIntegration**: stagnation and no-diff detection
- **SpecValidation_Integration**: spec validation as quality gate
- **Verification_Integration**: deterministic command execution
- **QualityGate_AuditFindingValidation**: finding severity/type validation

## Verification & Validation (V&V)

The repo includes a V&V framework to prove the closed-loop system works end-to-end.

### V&V Script

```powershell
# PowerShell (Windows)
.\scripts\vnv.ps1

# Makefile target (CI)
make vnv
```

The V&V script checks:
1. All new packages compile
2. Unit tests pass for each package
3. Integration tests pass
4. Traceability matrix is complete (`specs/vnv-traceability.yaml`)

### V&V Plan

See [docs/VNV_PLAN.md](VNV_PLAN.md) for the full Verification & Validation plan covering objectives, test strategy, and release criteria.

### Traceability Matrix

`specs/vnv-traceability.yaml` maps each improvement task to its implementation package, test file, and acceptance criteria.
