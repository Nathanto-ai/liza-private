package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
	"github.com/liza-mas/liza/internal/roles"
	"github.com/liza-mas/liza/internal/runtime"
)

// SupervisorConfig contains all configuration for the agent supervisor
type SupervisorConfig struct {
	AgentID          string
	Role             string // roles.RuntimeCoder, roles.RuntimeCodeReviewer, roles.RuntimePlanner, roles.RuntimeAuditor
	ProjectRoot      string
	StatePath        string
	LogPath          string
	SpecsDir         string // For prompt building
	CLIName          string // "claude", "codex", "gemini", "mistral", "kimi", "copilot"
	Interactive      bool   // Print prompt location, don't execute
	AutoApprove      bool   // Skip CLI permission prompts (--dangerously-skip-permissions)
	InitialTask      string // Optional task ID to resume
	Executor         CLIExecutor
	ExecutionTimeout time.Duration // Max time for agent execution before timeout
	MaxLoops         int           // Max supervisor loop iterations (0 = unlimited, uses budget tracker)
	CopilotModel     string        // Explicit Copilot model override (empty = use config default)
}

// crashRetryTracker tracks consecutive crash exits (non-0, non-42) for a given task.
// When the threshold is exceeded the task is transitioned to BLOCKED.
type crashRetryTracker struct {
	consecutiveCrashes int
	lastTaskID         string
	limit              int
	baseDelay          time.Duration
	maxDelay           time.Duration
}

const (
	// DefaultCrashRetryLimit is the maximum number of consecutive crash retries
	// before the supervisor transitions the task to BLOCKED.
	DefaultCrashRetryLimit = 5
	// DefaultCrashRetryBaseDelay is the initial delay for crash retry backoff.
	DefaultCrashRetryBaseDelay = 5 * time.Second
	// DefaultCrashRetryMaxDelay caps the exponential backoff for crash retries.
	DefaultCrashRetryMaxDelay = 120 * time.Second
)

func newCrashRetryTracker(cfg models.Config) *crashRetryTracker {
	limit := cfg.CrashRetryLimit
	if limit <= 0 {
		limit = DefaultCrashRetryLimit
	}
	baseDelay := time.Duration(cfg.CrashRetryBaseDelaySec) * time.Second
	if baseDelay <= 0 {
		baseDelay = DefaultCrashRetryBaseDelay
	}
	maxDelay := time.Duration(cfg.CrashRetryMaxDelaySec) * time.Second
	if maxDelay <= 0 {
		maxDelay = DefaultCrashRetryMaxDelay
	}
	return &crashRetryTracker{
		limit:     limit,
		baseDelay: baseDelay,
		maxDelay:  maxDelay,
	}
}

func (crt *crashRetryTracker) record(taskID string) (consecutive int, delay time.Duration) {
	if taskID != crt.lastTaskID {
		crt.consecutiveCrashes = 0
		crt.lastTaskID = taskID
	}
	crt.consecutiveCrashes++
	delay = crt.baseDelay
	for i := 1; i < crt.consecutiveCrashes; i++ {
		delay *= 2
		if delay > crt.maxDelay {
			delay = crt.maxDelay
			break
		}
	}
	return crt.consecutiveCrashes, delay
}

func (crt *crashRetryTracker) reset() {
	crt.consecutiveCrashes = 0
	crt.lastTaskID = ""
}

// progressTracker tracks consecutive iterations without meaningful task progress.
// When the threshold is exceeded, the task is transitioned to BLOCKED.
type progressTracker struct {
	lastTaskID    string
	lastIteration int
	lastCommit    string
	noProgressCount int
	limit         int
}

func newProgressTracker(cfg models.Config) *progressTracker {
	limit := cfg.MaxIterationsWithoutProgress
	if limit <= 0 {
		limit = models.DefaultMaxIterationsWithoutProgress
	}
	return &progressTracker{limit: limit}
}

// check compares current task state to the last snapshot.
// Returns true if the task should be blocked due to no progress.
func (pt *progressTracker) check(task *models.Task) bool {
	if task == nil {
		pt.reset()
		return false
	}

	commit := ""
	if task.ReviewCommit != nil {
		commit = *task.ReviewCommit
	}

	if task.ID != pt.lastTaskID {
		// New task — start tracking
		pt.lastTaskID = task.ID
		pt.lastIteration = task.Iteration
		pt.lastCommit = commit
		pt.noProgressCount = 0
		return false
	}

	// Same task — check for progress
	if task.Iteration != pt.lastIteration || commit != pt.lastCommit ||
		task.Status != models.TaskStatusImplementing {
		// Progress made or status changed
		pt.lastIteration = task.Iteration
		pt.lastCommit = commit
		pt.noProgressCount = 0
		return false
	}

	pt.noProgressCount++
	return pt.noProgressCount >= pt.limit
}

func (pt *progressTracker) reset() {
	pt.lastTaskID = ""
	pt.lastIteration = 0
	pt.lastCommit = ""
	pt.noProgressCount = 0
}

type exit42RestartState struct {
	RestartCount int
	Signature    string
}

type exit42RestartOutcome struct {
	Delay        time.Duration
	RestartCount int
	BlockedTask  bool
}

type exit42RestartTracker struct {
	byKey map[string]exit42RestartState
}

func newExit42RestartTracker() *exit42RestartTracker {
	return &exit42RestartTracker{
		byKey: make(map[string]exit42RestartState),
	}
}

func (t *exit42RestartTracker) reset(taskID string) {
	if taskID == "" {
		return
	}
	delete(t.byKey, "task:"+taskID)
}

func (t *exit42RestartTracker) Handle(bb *db.Blackboard, role, taskID, agentID string) (exit42RestartOutcome, error) {
	state, err := bb.Read()
	if err != nil {
		return exit42RestartOutcome{}, fmt.Errorf("read state for exit-42 tracking: %w", err)
	}

	maxBackoff := effectiveExit42MaxBackoff(state.Config)
	restartLimit := effectiveExit42RestartLimit(state.Config)
	key := exit42TrackerKey(taskID, role, agentID)
	prev := t.byKey[key]

	var signature string
	if taskID != "" {
		task := state.FindTask(taskID)
		if task != nil {
			signature = exit42TaskProgressSignature(task)
		}
	}

	if prev.Signature != "" && signature != "" && prev.Signature != signature {
		prev.RestartCount = 0
	}

	prev.RestartCount++
	prev.Signature = signature

	outcome := exit42RestartOutcome{
		Delay:        computeExit42BackoffDelay(prev.RestartCount, maxBackoff),
		RestartCount: prev.RestartCount,
	}

	blockedTask := false
	if err := bb.Modify(func(s *models.State) error {
		if taskID == "" {
			return nil
		}

		task := s.FindTask(taskID)
		if task == nil {
			return nil
		}

		task.Exit42RestartCount = outcome.RestartCount

		if role != roles.RuntimeCoder {
			return nil
		}
		if outcome.RestartCount <= restartLimit {
			return nil
		}
		if task.Status != models.TaskStatusImplementing {
			return nil
		}
		if task.AssignedTo == nil || *task.AssignedTo != agentID {
			return nil
		}

		reason := fmt.Sprintf(
			"exit code 42 restart loop detected: %d consecutive restarts without progress (threshold=%d)",
			outcome.RestartCount,
			restartLimit,
		)
		questions := []string{
			"What task/environment issue is causing repeated exit code 42 without progress?",
			"Should this task be decomposed or the spec clarified before retrying?",
		}

		if err := task.Transition(models.TaskStatusBlocked); err != nil {
			return err
		}

		now := time.Now().UTC()
		task.BlockedReason = &reason
		task.BlockedQuestions = questions
		task.AssignedTo = nil
		task.LeaseExpires = nil
		task.History = append(task.History, models.TaskHistoryEntry{
			Time:   now,
			Event:  "blocked",
			Agent:  &agentID,
			Reason: &reason,
		})
		blockedTask = true
		return nil
	}); err != nil {
		return exit42RestartOutcome{}, fmt.Errorf("persist exit-42 tracking state: %w", err)
	}

	outcome.BlockedTask = blockedTask
	if blockedTask {
		outcome.Delay = 0
		delete(t.byKey, key)
		return outcome, nil
	}

	t.byKey[key] = prev
	return outcome, nil
}

func exit42TrackerKey(taskID, role, agentID string) string {
	if taskID != "" {
		return "task:" + taskID
	}
	return "agent:" + role + ":" + agentID
}

func effectiveExit42MaxBackoff(cfg models.Config) time.Duration {
	seconds := cfg.Exit42MaxBackoffSeconds
	if seconds <= 0 {
		seconds = models.DefaultExit42MaxBackoffSec
	}
	return time.Duration(seconds) * time.Second
}

func effectiveExit42RestartLimit(cfg models.Config) int {
	limit := cfg.Exit42RestartThreshold
	if limit <= 0 {
		limit = models.DefaultExit42RestartLimit
	}
	return limit
}

func computeExit42BackoffDelay(restartCount int, maxBackoff time.Duration) time.Duration {
	if restartCount <= 0 {
		restartCount = 1
	}
	if maxBackoff <= 0 {
		maxBackoff = time.Duration(models.DefaultExit42MaxBackoffSec) * time.Second
	}

	delay := 2 * time.Second
	if delay > maxBackoff {
		return maxBackoff
	}

	for i := 1; i < restartCount; i++ {
		if delay >= maxBackoff {
			return maxBackoff
		}
		if delay > maxBackoff/2 {
			return maxBackoff
		}
		delay *= 2
	}

	if delay > maxBackoff {
		return maxBackoff
	}
	return delay
}

func exit42TaskProgressSignature(task *models.Task) string {
	// Only track fields that indicate real agent progress.
	// Exclude system-generated bookkeeping (history entries, lease timestamps,
	// handoff state, heartbeat, exit42 restart count) that change on every
	// claim/resume cycle — those changes do NOT indicate the agent made progress
	// and should not reset the exit-42 restart counter.
	snapshot := *task
	snapshot.Exit42RestartCount = 0
	snapshot.History = nil
	snapshot.LeaseExpires = nil
	snapshot.ReviewLeaseExpires = nil
	snapshot.HandoffPending = false

	payload, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Sprintf("%s|%d|%s", task.Status, task.Iteration, stringOrEmpty(task.ReviewCommit))
	}
	return string(payload)
}

// stringOrEmpty returns the value of a *string or "" if nil.
func stringOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// CLIExecutor interface for testing (mock vs real CLI)
type CLIExecutor interface {
	Execute(ctx context.Context, cliName string, agentID string, prompt string, projectRoot string, autoApprove bool) (exitCode int, err error)
	// ExecuteInteractive launches the CLI without a prompt arg, with stdin connected,
	// so the user can paste the prompt manually. Used by -i (interactive) mode.
	ExecuteInteractive(ctx context.Context, cliName string, projectRoot string) (exitCode int, err error)
}

// DefaultCLIExecutor implements real CLI execution
type DefaultCLIExecutor struct {
	outputsDir      string             // Directory to save agent outputs (if empty, output goes to stdout)
	copilotModelCfg CopilotModelConfig // Resolved Copilot model (only used when cliName is "copilot")
	role            string             // Agent role passed via LIZA_ROLE env var to liza-mcp
}

// NewDefaultCLIExecutor creates a new DefaultCLIExecutor with optional output directory
func NewDefaultCLIExecutor(outputsDir string) *DefaultCLIExecutor {
	return &DefaultCLIExecutor{outputsDir: outputsDir}
}

// NewDefaultCLIExecutorWithCopilot creates a DefaultCLIExecutor pre-configured for Copilot model selection.
func NewDefaultCLIExecutorWithCopilot(outputsDir string, copilotCfg CopilotModelConfig) *DefaultCLIExecutor {
	return &DefaultCLIExecutor{outputsDir: outputsDir, copilotModelCfg: copilotCfg}
}

// SetRole sets the agent role that will be passed to liza-mcp via the
// LIZA_ROLE environment variable for role-based tool filtering.
func (d *DefaultCLIExecutor) SetRole(role string) {
	d.role = role
}

func (d *DefaultCLIExecutor) Execute(ctx context.Context, cliName string, agentID string, prompt string, projectRoot string, autoApprove bool) (int, error) {
	// Map CLI names (mistral -> vibe)
	actualCLI := cliName
	if cliName == "mistral" {
		actualCLI = "vibe"
	}

	// Build command based on CLI.
	// Structured output flags (stream-json, --json, etc.) are only added when --log is
	// active (outputsDir != ""), so normal runs keep human-readable terminal output.
	//
	// Claude-compatible CLIs (claude, kimi, gemini, vibe) receive the prompt via
	// stdin instead of a positional argument.  This avoids the Windows cmd.exe
	// 8191-character command-line limit which is hit when .cmd wrappers are used
	// (e.g. kimi.cmd).  Using strings.NewReader delivers EOFafter the prompt,
	// so the subprocess never blocks waiting for input.
	var cmd *exec.Cmd
	useStdinForPrompt := false // when true, prompt is piped via stdin
	switch actualCLI {
	case "claude":
		args := []string{"-p"}
		if autoApprove {
			args = append(args, "--dangerously-skip-permissions")
		}
		if d.outputsDir != "" {
			args = append(args, "--verbose", "--output-format", "stream-json")
		}
		cmd = exec.CommandContext(ctx, "claude", args...)
		useStdinForPrompt = true
	case "codex":
		args := []string{"exec", prompt}
		if autoApprove {
			args = append(args, "--full-auto")
		}
		if d.outputsDir != "" {
			args = append(args, "--json")
		}
		cmd = exec.CommandContext(ctx, "codex", args...)
	case "gemini":
		args := []string{"-p"}
		if autoApprove {
			args = append(args, "--dangerously-skip-permissions")
		}
		if d.outputsDir != "" {
			args = append(args, "--output-format", "stream-json")
		}
		cmd = exec.CommandContext(ctx, "gemini", args...)
		useStdinForPrompt = true
	case "vibe":
		args := []string{"-p"}
		if autoApprove {
			args = append(args, "--dangerously-skip-permissions")
		}
		if d.outputsDir != "" {
			args = append(args, "--output", "streaming")
		}
		cmd = exec.CommandContext(ctx, "vibe", args...)
		useStdinForPrompt = true
	case "kimi":
		args := []string{"-p"}
		if autoApprove {
			args = append(args, "--dangerously-skip-permissions")
		}
		if d.outputsDir != "" {
			args = append(args, "--verbose", "--output-format", "stream-json")
		}
		cmd = exec.CommandContext(ctx, "kimi", args...)
		useStdinForPrompt = true
	case "copilot":
		// Copilot CLI is invoked via "gh copilot" which wraps the copilot binary.
		// It supports -p for non-interactive mode, --allow-all-tools for auto-approve,
		// --model for model selection, and --output-format json for structured output.
		// MCP config is passed via --additional-mcp-config pointing to .mcp.json.
		//
		// --autopilot enables automatic continuation in prompt mode: without it
		// the CLI exits as soon as the model produces a text-only response (no
		// tool calls), which causes many sessions to end after context-reading
		// without ever writing code.
		//
		// Prompt delivery: On Windows, gh.exe may hit the 8191-character cmd.exe
		// command-line limit when the prompt is passed as a -p argument. To avoid
		// this, we write the prompt to a temp file and pass a short -p instruction
		// that references the file, plus pipe the full prompt via stdin as backup.
		args := []string{"copilot", "--autopilot"}
		if autoApprove {
			args = append(args, "--allow-all-tools")
		}
		if d.copilotModelCfg.Model != "" {
			args = append(args, "--model", d.copilotModelCfg.Model)
		}
		if d.outputsDir != "" {
			args = append(args, "--output-format", "json")
		}
		// Pass project-level MCP config if it exists
		mcpConfigPath := filepath.Join(projectRoot, ".mcp.json")
		if _, err := os.Stat(mcpConfigPath); err == nil {
			args = append(args, "--additional-mcp-config", "@"+mcpConfigPath)
		}
		// Write prompt to temp file to avoid Windows command-line length limits.
		// The copilot agent reads the file via the short -p instruction.
		promptTmpFile, err := os.CreateTemp("", "liza-copilot-prompt-*.md")
		if err != nil {
			return 0, fmt.Errorf("create copilot prompt temp file: %w", err)
		}
		promptTmpPath := promptTmpFile.Name()
		defer os.Remove(promptTmpPath)
		if _, err := promptTmpFile.WriteString(prompt); err != nil {
			promptTmpFile.Close()
			return 0, fmt.Errorf("write copilot prompt temp file: %w", err)
		}
		promptTmpFile.Close()
		// -p with a short instruction to read the full prompt from the temp file.
		args = append(args, "-p", "Read and follow the instructions in "+promptTmpPath)
		cmd = exec.CommandContext(ctx, "gh", args...)
		// Also pipe the prompt via stdin as backup (copilot processes both -p and stdin).
		useStdinForPrompt = true
	default:
		return 0, fmt.Errorf("unknown CLI: %s", cliName)
	}

	// Set working directory to project root so claude can find .mcp.json and .claude/settings.json
	cmd.Dir = projectRoot

	// Pass agent role to liza-mcp via environment variable for role-based
	// tool filtering. The MCP server reads LIZA_ROLE and only exposes tools
	// that the role is allowed to use.
	if d.role != "" {
		cmd.Env = append(os.Environ(), "LIZA_ROLE="+d.role)
	}

	// Pipe prompt via stdin for Claude-compatible CLIs to avoid command-line
	// length limits.  strings.NewReader delivers EOF after the prompt so the
	// subprocess exits cleanly.  For codex the prompt is a required positional
	// arg, so stdin stays nil to prevent indefinite blocking.
	if useStdinForPrompt {
		cmd.Stdin = strings.NewReader(prompt)
	} else {
		cmd.Stdin = nil
	}

	// Handle output: either save to file or stream to stdout/stderr.
	// Separate buffers avoid the concurrency issue: exec.Cmd drains each pipe
	// in its own goroutine, so each buffer is written by exactly one goroutine.
	var stdoutBuf, stderrBuf strings.Builder
	if d.outputsDir != "" {
		cmd.Stdout = io.MultiWriter(os.Stdout, &stdoutBuf)
		cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	err := cmd.Run()

	// Save stdout and stderr to separate files if logging is enabled.
	if d.outputsDir != "" {
		save := func(ext, content string) {
			if content == "" {
				return
			}
			if _, saveErr := saveOutput(d.outputsDir, agentID, ext, content); saveErr != nil {
				GetLogger().Warn("Failed to save agent output", "error", saveErr, "agent_id", agentID, "ext", ext)
			}
		}
		save("txt", stdoutBuf.String())
		save("err", stderrBuf.String())
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 0, err
	}

	return 0, nil
}

func (d *DefaultCLIExecutor) ExecuteInteractive(ctx context.Context, cliName string, projectRoot string) (int, error) {
	// Map CLI names (mistral -> vibe)
	actualCLI := cliName
	if cliName == "mistral" {
		actualCLI = "vibe"
	}

	// Launch CLI without prompt arg — user pastes prompt manually
	var cmd *exec.Cmd
	switch actualCLI {
	case "codex":
		cmd = exec.CommandContext(ctx, "codex")
	case "copilot":
		args := []string{"copilot"}
		if d.copilotModelCfg.Model != "" {
			args = append(args, "--model", d.copilotModelCfg.Model)
		}
		cmd = exec.CommandContext(ctx, "gh", args...)
	default:
		cmd = exec.CommandContext(ctx, actualCLI)
	}

	cmd.Dir = projectRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 0, err
	}

	return 0, nil
}

// RunSupervisor is the main entry point for the agent supervisor
func RunSupervisor(ctx context.Context, config SupervisorConfig) error {
	bb := db.For(config.StatePath)
	lizaPaths := paths.New(config.ProjectRoot)

	// Initialize observability emitter
	events := newSupervisorEmitter(config.ProjectRoot)
	defer events.Close()

	// Validate identity
	if err := validateIdentity(config.AgentID, config.Role); err != nil {
		return err
	}

	// Register agent (sets STARTING → IDLE)
	if err := registerAgent(bb, config.ProjectRoot, config.AgentID, config.Role, "terminal-1", 1800); err != nil {
		return err
	}
	defer func() {
		unregisterAgent(bb, config.AgentID)
		events.emitAgentReleased(config.AgentID)
	}()
	events.emitAgentRegistered(config.AgentID, config.Role)

	// Load config from state
	state, err := bb.Read()
	if err != nil {
		return fmt.Errorf("failed to read state: %w", err)
	}

	// Start supervisor-level heartbeat that runs continuously (including idle periods).
	// This replaces the per-execution heartbeat in executeAgent() to prevent
	// stale heartbeat timestamps when agents are waiting for work.
	supervisorHeartbeatCtx, cancelSupervisorHeartbeat := context.WithCancel(ctx)
	defer cancelSupervisorHeartbeat()

	supervisorHB := NewHeartbeat(HeartbeatConfig{
		AgentID:   config.AgentID,
		StatePath: config.StatePath,
		State:     state,
	})
	go func() {
		if err := supervisorHB.Start(supervisorHeartbeatCtx); err != nil && err != context.Canceled {
			GetLogger().Error("Supervisor heartbeat error", "error", err, "agent_id", config.AgentID)
		}
	}()

	pollInterval, maxWait := getRoleWaitConfig(state, config.Role)

	// Set execution timeout if not configured
	if config.ExecutionTimeout == 0 {
		// Default timeouts based on role
		switch config.Role {
		case roles.RuntimeCodeReviewer:
			config.ExecutionTimeout = 30 * time.Minute
		case roles.RuntimeCoder:
			config.ExecutionTimeout = 2 * time.Hour
		case roles.RuntimePlanner:
			config.ExecutionTimeout = 4 * time.Hour
		case roles.RuntimeAuditor:
			config.ExecutionTimeout = 1 * time.Hour
		default:
			config.ExecutionTimeout = 2 * time.Hour
		}
	}

	const maxMergeRetries = 3
	mergeRetries := 0
	exit42Tracker := newExit42RestartTracker()
	crashTracker := newCrashRetryTracker(state.Config)
	progTracker := newProgressTracker(state.Config)
	consecutiveIdleCount := 0

	// Wire budget tracker for runaway prevention
	budget := runtime.NewBudgetTrackerFromConfig(state.Config)
	anomaly := runtime.NewAnomalyDetector()
	var iterationHistory []runtime.IterationRecord

	for {
		// Check context cancellation (signal received)
		if ctx.Err() != nil {
			GetLogger().Info("Signal received, shutting down")
			return nil
		}

		// Check ABORT
		if checkAbort(config.ProjectRoot) {
			GetLogger().Info("ABORT signal received, system shutting down")
			return nil
		}

		// Budget check — enforce iteration, runtime, and task-generation limits
		budget.RecordIteration()
		if warning, budgetErr := budget.CheckWithWarning(time.Now().UTC(), 0.8); budgetErr != nil {
			GetLogger().Warn("Budget exceeded, supervisor shutting down",
				"reason", budgetErr.Error(),
				"iterations", budget.Iterations(),
				"agent_id", config.AgentID)
			events.emitBudgetExceeded(config.AgentID, budgetErr.Error())
			return nil
		} else if warning != nil {
			GetLogger().Warn("Budget approaching limit",
				"warning", warning.Message,
				"agent_id", config.AgentID)
			events.emitBudgetWarning(config.AgentID, warning.Message)
		}

		// Anomaly detection — check for stagnation / no-diff patterns
		if anomalies := anomaly.Detect(iterationHistory); len(anomalies) > 0 {
			for _, a := range anomalies {
				GetLogger().Warn("Anomaly detected",
					"type", string(a.Type),
					"description", a.Description,
					"severity", a.Severity,
					"agent_id", config.AgentID)
				events.emitAnomalyDetected(config.AgentID, a)
			}
			// HIGH severity anomalies trigger supervisor shutdown
			for _, a := range anomalies {
				if a.Severity == "HIGH" {
					GetLogger().Error("HIGH severity anomaly, supervisor shutting down",
						"type", string(a.Type),
						"agent_id", config.AgentID)
					return nil
				}
			}
		}

		// MaxLoops cap (operator-provided via --max-loops flag)
		if config.MaxLoops > 0 && budget.Iterations() > config.MaxLoops {
			GetLogger().Info("Max loops reached, supervisor exiting",
				"max_loops", config.MaxLoops,
				"agent_id", config.AgentID)
			return nil
		}

		// Wait while PAUSE/CHECKPOINT
		if err := waitWhilePaused(ctx, config.ProjectRoot); err != nil {
			return err
		}

		// Handle approved merges (reviewer only)
		if config.Role == roles.RuntimeCodeReviewer {
			if err := handleApprovedMerges(config.ProjectRoot, config.AgentID, bb); err != nil {
				GetLogger().Warn("Merge handler error", "error", err)
			}

			// If there are still pending merges (transient errors), retry with
			// backoff up to a max count, then proceed to waitForWork
			if hasPendingMerges(bb, config.AgentID) {
				mergeRetries++
				if mergeRetries <= maxMergeRetries {
					delay := time.Duration(mergeRetries) * time.Second
					GetLogger().Info("Pending merges remain, retrying after delay",
						"agent_id", config.AgentID,
						"retry", mergeRetries,
						"delay", delay)
					time.Sleep(delay)
					continue
				}
				GetLogger().Warn("Max merge retries reached, proceeding to wait for work",
					"agent_id", config.AgentID,
					"retries", mergeRetries)
				mergeRetries = 0
			} else {
				mergeRetries = 0
			}
		}

		// Wait for work
		hasWork, err := waitForWork(ctx, bb, config.ProjectRoot, config.Role, config, pollInterval, maxWait)
		if err != nil {
			return err
		}
		if !hasWork {
			// Auditor stays idle and re-waits: it should not exit after a
			// normal no-work timeout. Other long-lived agents (planner)
			// already re-enter via wake triggers; auditor should behave
			// the same way—only exiting on shutdown/abort/budget/anomaly.
			if config.Role == roles.RuntimeAuditor {
				consecutiveIdleCount++
				backoff := computeIdleBackoff(consecutiveIdleCount, state.Config)
				GetLogger().Info("Auditor: no work available, backing off before retry",
					"idle_count", consecutiveIdleCount,
					"backoff", backoff)
				time.Sleep(backoff)
				continue
			}
			GetLogger().Info("No work available, supervisor exiting")
			return nil
		}
		consecutiveIdleCount = 0 // Reset on work found

		// Claim task (coder/reviewer only)
		var taskID string
		var claimedTaskID string // Track claimed task for completion logging
		if config.Role == roles.RuntimeCoder {
			taskID, _, err = claimCoderTask(config.ProjectRoot, config.AgentID, bb)
			if err != nil {
				// Error already logged in claimCoderTask
				time.Sleep(5 * time.Second)
				continue
			}
			claimedTaskID = taskID
			events.emitTaskClaimed(config.AgentID, taskID)
		} else if config.Role == roles.RuntimeCodeReviewer {
			var reviewCommit string
			taskID, _, reviewCommit, err = claimReviewerTask(config.ProjectRoot, config.AgentID, 1800, bb)
			if err != nil {
				// Error already logged in claimReviewerTask
				time.Sleep(5 * time.Second)
				continue // Race condition, retry
			}

			// Log successful reviewer claim
			GetLogger().Info("Reviewer claimed task for review",
				"agent_id", config.AgentID,
				"task_id", taskID,
				"review_commit", reviewCommit)
		}

		// Set planner status to PLANNING
		if config.Role == roles.RuntimePlanner {
			if err := setAgentToPlanningStatus(bb, config.AgentID); err != nil {
				GetLogger().Warn("Failed to set planner status", "error", err, "agent_id", config.AgentID)
			}
		}

		// Build and save prompt
		state, err := bb.Read()
		if err != nil {
			return fmt.Errorf("failed to read state for prompt: %w", err)
		}

		prompt, err := buildPrompt(state, config, taskID)
		if err != nil {
			return fmt.Errorf("failed to build prompt: %w", err)
		}

		promptFile, err := savePrompt(lizaPaths.AgentPromptsDir(), config.AgentID, prompt)
		if err != nil {
			return fmt.Errorf("failed to save prompt: %w", err)
		}
		GetLogger().Info("Prompt saved", "file", promptFile)

		// Execute agent
		exitCode, err := executeAgent(ctx, config, prompt)
		if err != nil {
			return fmt.Errorf("agent execution error: %w", err)
		}

		// Reset runtime status after CLI exits, but preserve explicit command-driven
		// states such as WAITING and HANDOFF.
		if err := resetAgentAfterExit(bb, config.AgentID); err != nil {
			GetLogger().Warn("Failed to reset agent status after exit", "error", err, "agent_id", config.AgentID)
		}

		// Handle exit code
		// Record iteration for anomaly detection
		iterRec := runtime.IterationRecord{
			TaskID:    taskID,
			Timestamp: time.Now().UTC(),
		}

		switch exitCode {
		case 0:
			GetLogger().Info("Agent completed, checking for more work")
			events.emitAgentExited(config.AgentID, 0)

			// Log task submission if it happened (coder role only)
			if config.Role == roles.RuntimeCoder && claimedTaskID != "" {
				if err := logTaskSubmissionIfCompleted(bb, claimedTaskID, config.AgentID); err != nil {
					GetLogger().Warn("Failed to log task submission", "error", err, "task_id", claimedTaskID)
				}

				// Run deterministic verification on submitted tasks
				verifyResult := runPostSubmissionVerification(ctx, bb, config.ProjectRoot, claimedTaskID)
				if verifyResult != nil {
					events.emitVerifyRun(claimedTaskID, verifyResult.Passed, len(verifyResult.Results))
				}
			}

			// Verify expected state changes for planner
			if config.Role == roles.RuntimePlanner {
				if err := verifyPlannerStateChanges(bb, state); err != nil {
					GetLogger().Warn("Planner state verification failed",
						"error", err,
						"hint", "Agent may not have executed required commands - check prompt file")
				}
				// Cooldown: let other agents act on planner's changes
				// before re-checking triggers to prevent busy-loop.
				GetLogger().Info("Planner cooldown after session", "delay", pollInterval)
				time.Sleep(pollInterval)
			}

			exit42Tracker.reset(taskID)
			crashTracker.reset()

			// Stuck-coder detection: check for progress on coder tasks
			if config.Role == roles.RuntimeCoder && claimedTaskID != "" {
				postState, readErr := bb.Read()
				if readErr == nil {
					task := postState.FindTask(claimedTaskID)
					if progTracker.check(task) {
						GetLogger().Warn("Task stuck: no progress after multiple iterations, blocking",
							"task_id", claimedTaskID,
							"iterations_without_progress", progTracker.noProgressCount,
							"agent_id", config.AgentID)
						blockTaskOnNoProgress(bb, claimedTaskID, config.AgentID, progTracker.noProgressCount, progTracker.limit)
						progTracker.reset()
					}
				}
			}

			iterRec.Status = "SUCCESS"
			iterRec.HasDiff = true // assume success means progress
		case 42:
			restartTaskID := claimedTaskID
			if restartTaskID == "" {
				restartTaskID = taskID
			}

			outcome, trackErr := exit42Tracker.Handle(bb, config.Role, restartTaskID, config.AgentID)
			if trackErr != nil {
				GetLogger().Warn("Exit-42 tracker failed, using default retry delay",
					"error", trackErr,
					"task_id", restartTaskID)
				time.Sleep(2 * time.Second)
				break
			}

			if outcome.BlockedTask {
				GetLogger().Warn("Task transitioned to BLOCKED after repeated exit 42 restarts",
					"task_id", restartTaskID,
					"restart_count", outcome.RestartCount)
				break
			}

			GetLogger().Info("Agent aborted gracefully, restarting",
				"exit_code", 42,
				"task_id", restartTaskID,
				"restart_count", outcome.RestartCount,
				"delay_seconds", int(outcome.Delay/time.Second))
			events.emitAgentAborted(config.AgentID, restartTaskID, outcome.RestartCount)
			time.Sleep(outcome.Delay)
			iterRec.Status = "EXIT42"
		default:
			exit42Tracker.reset(taskID)
			crashes, delay := crashTracker.record(taskID)

			if crashes > crashTracker.limit {
				// Transition task to BLOCKED after too many consecutive crashes
				GetLogger().Error("Crash retry limit exceeded, blocking task",
					"exit_code", exitCode,
					"consecutive_crashes", crashes,
					"limit", crashTracker.limit,
					"task_id", taskID,
					"agent_id", config.AgentID)
				events.emitCrashLimitExceeded(config.AgentID, taskID, crashes, crashTracker.limit)
				blockTaskOnCrashLimit(bb, taskID, config.AgentID, crashes, crashTracker.limit)
				crashTracker.reset()
			} else {
				GetLogger().Error("Agent crashed, restarting with backoff",
					"exit_code", exitCode,
					"consecutive_crashes", crashes,
					"delay", delay,
					"agent_id", config.AgentID)
				events.emitCrashRetry(config.AgentID, taskID, crashes)
				time.Sleep(delay)
			}

			iterRec.Status = "CRASHED"
		}

		iterationHistory = append(iterationHistory, iterRec)

		// Clear initial task after first run
		config.InitialTask = ""
	}
}

// blockTaskOnCrashLimit transitions a task to BLOCKED after exceeding the
// crash retry limit. This prevents infinite crash-retry loops.
func blockTaskOnCrashLimit(bb *db.Blackboard, taskID, agentID string, crashes, limit int) {
	if taskID == "" {
		return
	}
	err := bb.Modify(func(s *models.State) error {
		task := s.FindTask(taskID)
		if task == nil {
			return nil
		}
		if task.Status != models.TaskStatusImplementing {
			return nil
		}
		if task.AssignedTo == nil || *task.AssignedTo != agentID {
			return nil
		}

		reason := fmt.Sprintf(
			"crash retry limit exceeded: %d consecutive non-zero exits (limit=%d)",
			crashes, limit,
		)
		questions := []string{
			"Is the CLI binary installed and accessible?",
			"Is there a configuration or environment issue causing persistent crashes?",
			"Should this task be decomposed or the approach changed?",
		}

		if err := task.Transition(models.TaskStatusBlocked); err != nil {
			return err
		}

		now := time.Now().UTC()
		task.BlockedReason = &reason
		task.BlockedQuestions = questions
		task.AssignedTo = nil
		task.LeaseExpires = nil
		task.History = append(task.History, models.TaskHistoryEntry{
			Time:   now,
			Event:  "blocked",
			Agent:  &agentID,
			Reason: &reason,
		})
		return nil
	})
	if err != nil {
		GetLogger().Error("Failed to block task after crash limit", "error", err, "task_id", taskID)
	}
}

// blockTaskOnNoProgress transitions a task to BLOCKED after the coder runs
// multiple iterations without making any observable progress (no iteration
// change, no commit, status still IMPLEMENTING).
func blockTaskOnNoProgress(bb *db.Blackboard, taskID, agentID string, count, limit int) {
	if taskID == "" {
		return
	}
	err := bb.Modify(func(s *models.State) error {
		task := s.FindTask(taskID)
		if task == nil {
			return nil
		}
		if task.Status != models.TaskStatusImplementing {
			return nil
		}
		if task.AssignedTo == nil || *task.AssignedTo != agentID {
			return nil
		}

		reason := fmt.Sprintf(
			"no progress detected: %d consecutive iterations without task advancement (threshold=%d). "+
				"Possible cause: test deadlock, infinite loop, or repeated failure without code changes.",
			count, limit,
		)
		questions := []string{
			"Is the implementation approach fundamentally blocked (e.g., deadlocking tests)?",
			"Should this task be superseded with a new approach?",
		}

		if err := task.Transition(models.TaskStatusBlocked); err != nil {
			return err
		}

		now := time.Now().UTC()
		task.BlockedReason = &reason
		task.BlockedQuestions = questions
		task.AssignedTo = nil
		task.LeaseExpires = nil
		task.History = append(task.History, models.TaskHistoryEntry{
			Time:   now,
			Event:  "blocked",
			Agent:  &agentID,
			Reason: &reason,
		})
		return nil
	})
	if err != nil {
		GetLogger().Error("Failed to block task on no progress", "error", err, "task_id", taskID)
	}
}

// computeIdleBackoff returns an exponential backoff duration for idle agents.
func computeIdleBackoff(consecutiveIdleCount int, cfg models.Config) time.Duration {
	baseSec := cfg.IdleBackoffBaseSec
	if baseSec <= 0 {
		baseSec = models.DefaultIdleBackoffBaseSec
	}
	maxSec := cfg.IdleBackoffMaxSec
	if maxSec <= 0 {
		maxSec = models.DefaultIdleBackoffMaxSec
	}

	delay := time.Duration(baseSec) * time.Second
	for i := 1; i < consecutiveIdleCount; i++ {
		delay *= 2
		if delay > time.Duration(maxSec)*time.Second {
			return time.Duration(maxSec) * time.Second
		}
	}
	return delay
}
