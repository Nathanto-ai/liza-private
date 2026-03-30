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
	"github.com/liza-mas/liza/internal/ops"
	"github.com/liza-mas/liza/internal/paths"
	"github.com/liza-mas/liza/internal/pipeline"
	"github.com/liza-mas/liza/internal/roles"
	"github.com/liza-mas/liza/internal/runtime"
)

// SupervisorConfig contains all configuration for the agent supervisor
type SupervisorConfig struct {
	AgentID          string
	Role             string // runtime role name (e.g. "coder", "code-reviewer", "orchestrator", "planner", "auditor")
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

// noSubmitTracker detects the "task_complete loop": the coder exits normally
// (exit 0) but never transitions the task from IMPLEMENTING to REVIEWING.
// After consecutiveNoSubmit reaches the limit the task is BLOCKED so it
// stops being re-claimed infinitely. This addresses ISSUE-R7-07.
type noSubmitTracker struct {
	lastTaskID         string
	consecutiveNoSubmit int
	limit              int
}

// DefaultNoSubmitLimit is the number of consecutive coder exits without
// submission before the supervisor blocks the task.
const DefaultNoSubmitLimit = 3

func newNoSubmitTracker(cfg models.Config) *noSubmitTracker {
	limit := cfg.MaxNoSubmitIterations
	if limit <= 0 {
		limit = DefaultNoSubmitLimit
	}
	return &noSubmitTracker{limit: limit}
}

// record increments the counter for the given task. Returns true when the
// threshold is reached and the task should be blocked.
func (ns *noSubmitTracker) record(taskID string) bool {
	if taskID != ns.lastTaskID {
		ns.lastTaskID = taskID
		ns.consecutiveNoSubmit = 0
	}
	ns.consecutiveNoSubmit++
	return ns.consecutiveNoSubmit >= ns.limit
}

func (ns *noSubmitTracker) reset() {
	ns.lastTaskID = ""
	ns.consecutiveNoSubmit = 0
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

func (t *exit42RestartTracker) Handle(bb *db.Blackboard, projectRoot, role, taskID, agentID string) (exit42RestartOutcome, error) {
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

		if role != "coder" {
			return nil
		}
		if outcome.RestartCount <= restartLimit {
			return nil
		}
		// Check if task is in an executing state using pipeline resolver
		pr, prErr := ops.LoadResolverForModels(projectRoot)
		if prErr != nil {
			return prErr
		}

		// After exit-42, resetAgentAfterExit may have already released the
		// claim, transitioning from executing back to initial state.
		// Re-claim it so we can block it (same pattern as blockTaskOnNoSubmit).
		if initialStatus, iErr := pr.InitialStatus(task.RolePair); iErr == nil && task.Status == initialStatus {
			executingStatus, eErr := pr.ExecutingStatus(task.RolePair)
			if eErr != nil {
				return eErr
			}
			pt, ptErr := ops.LoadPipelineTransitions(projectRoot)
			if ptErr != nil {
				return ptErr
			}
			if err := task.TransitionWith(executingStatus, pt); err != nil {
				return err
			}
			task.AssignedTo = &agentID
		}

		if !models.IsExecutingStatus(task, pr) {
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

		pipelineTransitions, ptErr := ops.LoadPipelineTransitions(projectRoot)
		if ptErr != nil {
			return ptErr
		}
		if err := task.TransitionWith(models.TaskStatusBlocked, pipelineTransitions); err != nil {
			return err
		}

		now := time.Now().UTC()
		task.BlockedReason = &reason
		task.BlockedQuestions = questions
		task.AssignedTo = nil
		task.LeaseExpires = nil
		task.History = append(task.History, models.TaskHistoryEntry{
			Time:   now,
			Event:  models.TaskEventBlocked,
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
	// handoff state, heartbeat, exit42 restart count, iteration counter,
	// assigned-to, status) that change on every claim/release cycle — those
	// changes do NOT indicate the agent made progress and should not reset
	// the exit-42 restart counter.
	snapshot := *task
	snapshot.Exit42RestartCount = 0
	snapshot.History = nil
	snapshot.LeaseExpires = nil
	snapshot.ReviewLeaseExpires = nil
	snapshot.HandoffPending = false
	snapshot.Iteration = 0
	snapshot.AssignedTo = nil
	snapshot.Status = ""

	payload, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Sprintf("%s|%s", task.ID, stringOrEmpty(task.ReviewCommit))
	}
	return string(payload)
}

// cliSupportsStdin returns true if the CLI can read the prompt from stdin
// instead of requiring it as a command-line argument. This avoids platform
// ARG_MAX limits (e.g. 32,767 chars on Windows).
func cliSupportsStdin(cliName string) bool {
	return cliName != "vibe"
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

type DefaultCLIExecutor struct {
	outputsDir      string             // Directory to save agent outputs (if empty, output goes to stdout)
	masker          *SecretMasker      // Masks secret values in persisted output (nil when logging disabled)
	copilotModelCfg CopilotModelConfig // Resolved Copilot model (only used when cliName is "copilot")
	role            string             // Agent role passed via LIZA_ROLE env var to liza-mcp
}

func NewDefaultCLIExecutor(outputsDir string) *DefaultCLIExecutor {
	var masker *SecretMasker
	if outputsDir != "" {
		masker = NewSecretMasker()
	}
	return &DefaultCLIExecutor{outputsDir: outputsDir, masker: masker}
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
	// Structured output flags (stream-json, --json, etc.) are only added when logging is
	// active (outputsDir != ""), so --no-log runs keep human-readable terminal output.
	//
	// For CLIs that support stdin (all except vibe), the prompt is piped via stdin
	// instead of passed as a CLI argument. This avoids platform ARG_MAX limits
	// (e.g. 32,767 chars on Windows with CreateProcess).
	useStdin := cliSupportsStdin(actualCLI)
	var cmd *exec.Cmd
	switch actualCLI {
	case "claude":
		args := []string{"-p"}
		if !useStdin {
			args = append(args, prompt)
		}
		if autoApprove {
			args = append(args, "--dangerously-skip-permissions")
		}
		if d.outputsDir != "" {
			args = append(args, "--verbose", "--output-format", "stream-json")
		}
		cmd = exec.CommandContext(ctx, "claude", args...)
	case "codex":
		args := []string{
			"-c", fmt.Sprintf("mcp_servers.liza.command=%q", "liza-mcp"),
			"-c", fmt.Sprintf("mcp_servers.liza.args=[%q,%q]", "--project-root", projectRoot),
		}
		if useStdin {
			args = append(args, "exec", "-")
		} else {
			args = append(args, "exec", prompt)
		}
		if autoApprove {
			args = append(args, "--full-auto")
		}
		if d.outputsDir != "" {
			args = append(args, "--json")
		}
		cmd = exec.CommandContext(ctx, "codex", args...)
	case "gemini":
		args := []string{"-p"}
		if !useStdin {
			args = append(args, prompt)
		}
		if autoApprove {
			args = append(args, "--dangerously-skip-permissions")
		}
		if d.outputsDir != "" {
			args = append(args, "--output-format", "stream-json")
		}
		cmd = exec.CommandContext(ctx, "gemini", args...)
	case "vibe":
		args := []string{"-p"}
		if autoApprove {
			args = append(args, "--dangerously-skip-permissions")
		}
		if d.outputsDir != "" {
			args = append(args, "--output", "streaming")
		}
		cmd = exec.CommandContext(ctx, "vibe", args...)
	case "kimi":
		args := []string{"-p"}
		if !useStdin {
			args = append(args, prompt)
		}
		if autoApprove {
			args = append(args, "--dangerously-skip-permissions")
		}
		if d.outputsDir != "" {
			args = append(args, "--verbose", "--output-format", "stream-json")
		}
		cmd = exec.CommandContext(ctx, "kimi", args...)
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

	// When the CLI supports stdin, pipe the prompt through it. Otherwise, don't
	// inherit stdin — agents are autonomous and inheriting stdin causes the
	// subprocess to block indefinitely waiting for EOF.
	if useStdin {
		cmd.Stdin = strings.NewReader(prompt)
	} else {
		cmd.Stdin = nil
	}

	// Ensure LIZA_AGENT_ID is available to child processes (hooks, MCP servers).
	// The agent ID may have been resolved from --agent-id flag rather than the
	// env var, so we set it explicitly to guarantee availability.
	cmd.Env = append(os.Environ(), "LIZA_AGENT_ID="+agentID)

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
			if _, saveErr := saveOutput(d.outputsDir, agentID, ext, content, d.masker); saveErr != nil {
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

	// Load pipeline resolver for role type classification.
	// The resolver reads role definitions from the pipeline YAML,
	// enabling custom YAML-defined roles without Go code changes.
	pipelineCfg, pipelineErr := pipeline.LoadFrozen(config.ProjectRoot)
	if pipelineErr != nil {
		return fmt.Errorf("loading pipeline config for strategy selection: %w", pipelineErr)
	}
	resolver := pipeline.NewResolver(pipelineCfg)

	strategy, err := NewRoleStrategy(config.Role, resolver)
	if err != nil {
		return err
	}
	defer func() {
		unregisterAgent(bb, config.AgentID, config.ProjectRoot)
		events.emitAgentReleased(config.AgentID)
	}()
	events.emitAgentRegistered(config.AgentID, config.Role)

	// Apply YAML-sourced timeouts from pipeline config to the strategy.
	if timeouts, tErr := resolver.RoleTimeouts(config.Role); tErr == nil {
		ApplyYAMLTimeouts(strategy, timeouts.Execution, timeouts.PollInterval, timeouts.MaxWait)
	}

	if err := registerAgent(bb, config.ProjectRoot, config.AgentID, config.Role, "terminal-1", 1800, config.CLIName, resolver); err != nil {
		return err
	}
	defer unregisterAgent(bb, config.AgentID, config.ProjectRoot)

	state, err := bb.Read()
	if err != nil {
		return fmt.Errorf("failed to read state: %w", err)
	}

	// Start supervisor-lifetime heartbeat to keep the lease alive across
	// the entire loop (including IDLE wait-for-work periods, not just
	// during CLI execution). Without this, an IDLE agent's lease can
	// expire, causing auto-assigned ID collision with new agents.
	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	defer cancelHeartbeat()

	hb := NewHeartbeat(HeartbeatConfig{
		AgentID:   config.AgentID,
		StatePath: config.StatePath,
		State:     state,
	})

	go func() {
		if err := hb.Start(heartbeatCtx); err != nil && err != context.Canceled {
			GetLogger().Error("Heartbeat error", "error", err, "agent_id", config.AgentID)
		}
	}()

	pollInterval, maxWait := strategy.WaitConfig(state)

	// Set execution timeout if not configured
	if config.ExecutionTimeout == 0 {
		config.ExecutionTimeout = strategy.DefaultTimeout()
	}

	exit42Tracker := newExit42RestartTracker()
	crashTracker := newCrashRetryTracker(state.Config)
	progTracker := newProgressTracker(state.Config)
	noSubmitTracker := newNoSubmitTracker(state.Config)
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

		// Budget check — enforce runtime and task-generation limits.
		// Note: iteration is recorded AFTER productive work (agent execution),
		// not here, so idle polls don't consume the iteration budget.
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

		// Pre-work (reviewer: merge handling; others: no-op)
		shouldContinue, err := strategy.PreWork(ctx, bb, config)
		if err != nil {
			return err
		}
		if shouldContinue {
			continue
		}

		// Wait for work
		hasWork, err := strategy.WaitForWork(ctx, bb, config, pollInterval, maxWait)
		if err != nil {
			return err
		}
		if !hasWork {
			// Auditor and reviewer stay idle and re-wait: they should not exit
			// after a normal no-work timeout. Planner already re-enters via
			// wake triggers. Long-lived agents use exponential backoff to
			// avoid busy-polling while preserving iteration budget.
			if config.Role == roles.RuntimeAuditor || config.Role == roles.RuntimeCodeReviewer {
				consecutiveIdleCount++
				backoff := computeIdleBackoff(consecutiveIdleCount, state.Config)
				roleName := string(config.Role)
				GetLogger().Info(roleName+": no work available, backing off before retry",
					"idle_count", consecutiveIdleCount,
					"backoff", backoff)
				if !sleepWithContext(ctx, backoff) {
					return nil // context cancelled
				}
				continue
			}

			// For doer and orchestrator roles, check if the sprint still has
			// non-terminal tasks (e.g. blocked by dependencies). If so, stay
			// alive with backoff — those tasks will become claimable once
			// their dependencies resolve. Give up after MaxIdleRetries to
			// avoid spinning indefinitely on work that can't be claimed.
			maxRetries := state.Config.MaxIdleRetries
			if maxRetries <= 0 {
				maxRetries = models.DefaultMaxIdleRetries
			}
			if sprintHasPendingWork(config.ProjectRoot) && consecutiveIdleCount < maxRetries {
				consecutiveIdleCount++
				backoff := computeIdleBackoff(consecutiveIdleCount, state.Config)
				roleName := string(config.Role)
				GetLogger().Info(roleName+": no immediate work but sprint has pending tasks, backing off",
					"idle_count", consecutiveIdleCount,
					"backoff", backoff)
				if !sleepWithContext(ctx, backoff) {
					return nil // context cancelled
				}
				continue
			}

			GetLogger().Info("No work available, supervisor exiting")
			return nil
		}
		consecutiveIdleCount = 0 // Reset on work found

		// Claim task
		taskID, claimedTaskID, err := strategy.ClaimTask(config, bb)
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}

		// Pre-execution (orchestrator: PLANNING status; others: no-op)
		if err := strategy.PreExecution(bb, config); err != nil {
			GetLogger().Warn("Pre-execution failed", "error", err, "agent_id", config.AgentID)
		}

		// Build and save prompt
		stateBefore, err := bb.Read()
		if err != nil {
			return fmt.Errorf("failed to read state for prompt: %w", err)
		}

		prompt, err := strategy.BuildPrompt(stateBefore, config, taskID)
		if err != nil {
			return fmt.Errorf("failed to build prompt: %w", err)
		}

		promptFile, err := savePrompt(lizaPaths.AgentPromptsDir(), config.AgentID, prompt)
		if err != nil {
			return fmt.Errorf("failed to save prompt: %w", err)
		}
		GetLogger().Info("Prompt saved", "file", promptFile)

		// Initialize MCP activity file before execution so the inactivity
		// monitor has a baseline timestamp.
		mcpActivityPath := lizaPaths.MCPActivityPath()
		os.WriteFile(mcpActivityPath, []byte(time.Now().UTC().Format(time.RFC3339)), 0644)

		// Start MCP inactivity monitor if configured.
		// If the agent runs for mcpInactivityTimeout without any MCP tool calls,
		// cancel the execution context to kill and restart the session.
		mcpTimeout := effectiveMCPInactivityTimeout(state.Config)
		var mcpCtx context.Context
		var mcpCancel context.CancelFunc
		if mcpTimeout > 0 {
			mcpCtx, mcpCancel = context.WithCancel(ctx)
			go monitorMCPActivity(mcpCtx, mcpCancel, mcpActivityPath, mcpTimeout, 30*time.Second, config.AgentID)
		} else {
			mcpCtx = ctx
			mcpCancel = func() {} // no-op
		}

		// Execute agent
		exitCode, err := executeAgent(mcpCtx, config, prompt)
		mcpCancel() // Stop MCP monitor goroutine
		if err != nil {
			return fmt.Errorf("agent execution error: %w", err)
		}

		// Clean up MCP activity file after execution
		os.Remove(mcpActivityPath)

		// Reset runtime status after CLI exits, but preserve explicit command-driven
		// states such as WAITING and HANDOFF.
		if err := resetAgentAfterExit(bb, config.AgentID, config.ProjectRoot); err != nil {
			GetLogger().Warn("Failed to reset agent status after exit", "error", err, "agent_id", config.AgentID)
		}

		// Record productive iteration — only after agent actually executed.
		// Idle polls (no work found) do NOT count toward the iteration budget.
		budget.RecordIteration()

		// Handle exit code
		// Record iteration for anomaly detection
		iterRec := runtime.IterationRecord{
			TaskID:    taskID,
			Timestamp: time.Now().UTC(),
		}

		switch exitCode {
		case 0:
			GetLogger().Info("Agent completed, checking for more work")
			if err := strategy.PostExecution(bb, config, taskID, claimedTaskID, stateBefore); err != nil {
				GetLogger().Warn("Post-execution error", "error", err)
			}
			events.emitAgentExited(config.AgentID, 0)

			// Log task submission if it happened (coder role only)
			if config.Role == roles.RuntimeCoder && claimedTaskID != "" {
				if err := logTaskSubmissionIfCompleted(bb, claimedTaskID, config.AgentID, resolver); err != nil {
					GetLogger().Warn("Failed to log task submission", "error", err, "task_id", claimedTaskID)
				}

				// Fix 37: Detect coder exiting without submission (task_complete loop).
				postCheck, postErr := bb.Read()
				if postErr == nil {
					postTask := postCheck.FindTask(claimedTaskID)
					if postTask != nil && postTask.Status == models.TaskStatusReady {
						if noSubmitTracker.record(claimedTaskID) {
							GetLogger().Warn("Task blocked: coder exited without submission too many times (possible task_complete loop)",
								"task_id", claimedTaskID,
								"consecutive_no_submit", noSubmitTracker.consecutiveNoSubmit,
								"limit", noSubmitTracker.limit,
								"agent_id", config.AgentID)
							blockTaskOnNoSubmit(bb, claimedTaskID, config.AgentID, noSubmitTracker.consecutiveNoSubmit, noSubmitTracker.limit)
							noSubmitTracker.reset()
						} else {
							GetLogger().Warn("Coder exited without submission, tracking",
								"task_id", claimedTaskID,
								"consecutive_no_submit", noSubmitTracker.consecutiveNoSubmit,
								"limit", noSubmitTracker.limit,
								"agent_id", config.AgentID)
						}
					} else {
						noSubmitTracker.reset()
					}
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

			outcome, trackErr := exit42Tracker.Handle(bb, config.ProjectRoot, config.Role, restartTaskID, config.AgentID)
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

		// After a crash, resetAgentAfterExit may have already released the
		// claim, transitioning from IMPLEMENTING_CODE back to DRAFT_CODE.
		// Re-claim it so we can block it (same pattern as blockTaskOnNoSubmit).
		if task.Status == models.TaskStatusReady {
			if err := task.Transition(models.TaskStatusImplementing); err != nil {
				return err
			}
			task.AssignedTo = &agentID
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

		// After exit, resetAgentAfterExit may have already released the
		// claim, transitioning from IMPLEMENTING_CODE back to DRAFT_CODE.
		// Re-claim it so we can block it (same pattern as blockTaskOnNoSubmit).
		if task.Status == models.TaskStatusReady {
			if err := task.Transition(models.TaskStatusImplementing); err != nil {
				return err
			}
			task.AssignedTo = &agentID
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

// blockTaskOnNoSubmit escalates a task after the coder repeatedly exits without
// submitting for review (the "task_complete loop" from ISSUE-R7-07).
// Fix 43: Use NEEDS_HUMAN_DECISION instead of BLOCKED so the planner does NOT
// wake and create useless meta-tasks (repair/investigate/test/fix cascades).
// The task may be in READY state (logTaskSubmissionIfCompleted already released it)
// so we transition READY → IMPLEMENTING → NEEDS_HUMAN_DECISION.
func blockTaskOnNoSubmit(bb *db.Blackboard, taskID, agentID string, count, limit int) {
	if taskID == "" {
		return
	}
	err := bb.Modify(func(s *models.State) error {
		task := s.FindTask(taskID)
		if task == nil {
			return nil
		}

		// The task was released to READY by logTaskSubmissionIfCompleted.
		// Re-claim it so we can escalate it.
		if task.Status == models.TaskStatusReady {
			if err := task.Transition(models.TaskStatusImplementing); err != nil {
				return err
			}
			task.AssignedTo = &agentID
		}

		if task.Status != models.TaskStatusImplementing {
			return nil
		}

		reason := fmt.Sprintf(
			"task_complete loop detected: coder exited %d consecutive times without calling "+
				"liza_submit_for_review (threshold=%d). The model is likely calling the copilot-native "+
				"task_complete tool instead of the MCP submit tool.",
			count, limit,
		)

		// Fix 43: NEEDS_HUMAN_DECISION instead of BLOCKED to avoid planner
		// wake cascade (BLOCKED_TASKS trigger creates repair/investigate tasks).
		if err := task.Transition(models.TaskStatusNeedsHumanDecision); err != nil {
			return err
		}

		now := time.Now().UTC()
		task.BlockedReason = &reason
		task.AssignedTo = nil
		task.LeaseExpires = nil
		task.History = append(task.History, models.TaskHistoryEntry{
			Time:   now,
			Event:  "needs_human_decision",
			Agent:  &agentID,
			Reason: &reason,
		})
		return nil
	})
	if err != nil {
		GetLogger().Error("Failed to escalate task on no-submit loop", "error", err, "task_id", taskID)
	}
}

// sprintHasPendingWork checks whether the current sprint still has non-terminal
// tasks. When true, agents should stay alive because blocked tasks may become
// claimable once their dependencies complete.
func sprintHasPendingWork(projectRoot string) bool {
	lp := paths.New(projectRoot)
	bb := db.For(lp.StatePath())
	state, err := bb.Read()
	if err != nil {
		return false
	}
	if state.Sprint.Status == models.SprintStatusCompleted {
		return false
	}
	detCtx, detErr := ops.LoadDetectionContext(projectRoot)
	var pipelineTerminals []models.TaskStatus
	if detErr == nil {
		pipelineTerminals = detCtx.SprintTerminals
	}
	return !state.AllPlannedTasksTerminalWith(pipelineTerminals)
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

// sleepWithContext sleeps for the given duration but wakes immediately if the
// context is cancelled. Returns true if the sleep completed, false if the
// context was cancelled.
func sleepWithContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// effectiveMCPInactivityTimeout returns the MCP inactivity timeout from config,
// falling back to the default. Returns 0 if disabled.
func effectiveMCPInactivityTimeout(cfg models.Config) time.Duration {
	sec := cfg.MCPInactivityTimeoutSec
	if sec < 0 {
		return 0 // explicitly disabled
	}
	if sec == 0 {
		sec = models.DefaultMCPInactivityTimeoutSec
	}
	return time.Duration(sec) * time.Second
}

// monitorMCPActivity periodically checks the MCP activity file and cancels the
// execution context if no MCP tool calls have been made within the timeout.
// This detects agents stuck in built-in tool loops (e.g., copilot's native file
// read/write) without calling any liza MCP tools.
func monitorMCPActivity(ctx context.Context, cancel context.CancelFunc, activityPath string, timeout time.Duration, checkInterval time.Duration, agentID string) {
	logger := GetLogger()
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	// Track when the monitor started as a fallback: if the activity file
	// never appears (e.g. MCP server failed to start, file write failed),
	// we still enforce a deadline of 2× the timeout.
	monitorStart := time.Now()
	fallbackDeadline := 2 * timeout

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			data, err := os.ReadFile(activityPath)
			if err != nil {
				// File missing — MCP server may not have started yet.
				// But enforce fallback deadline to prevent infinite waiting.
				if time.Since(monitorStart) > fallbackDeadline {
					logger.Warn("MCP activity file never appeared — killing agent session",
						"agent_id", agentID,
						"waited", time.Since(monitorStart).Round(time.Second),
						"fallback_deadline", fallbackDeadline)
					cancel()
					return
				}
				continue
			}
			lastActivity, err := time.Parse(time.RFC3339, string(data))
			if err != nil {
				continue
			}
			elapsed := time.Since(lastActivity)
			if elapsed > timeout {
				logger.Warn("MCP inactivity timeout — killing agent session",
					"agent_id", agentID,
					"last_mcp_call", lastActivity.Format(time.RFC3339),
					"elapsed", elapsed.Round(time.Second),
					"timeout", timeout)
				cancel()
				return
			}
		}
	}
}

// detectAndFixStaleClaim checks for IMPLEMENTING tasks assigned to agents with
// stale heartbeats and releases those claims so other coders can pick them up.
// Returns true if at least one stale claim was released (caller should retry).
func detectAndFixStaleClaim(bb *db.Blackboard, callerAgentID string) bool {
	logger := GetLogger()

	state, err := bb.Read()
	if err != nil {
		logger.Warn("detectAndFixStaleClaim: failed to read state", "error", err)
		return false
	}

	// Determine heartbeat stale threshold: 3× the configured interval.
	heartbeatInterval := models.NormalizeHeartbeatInterval(state.Config.HeartbeatInterval)
	staleThreshold := 3 * heartbeatInterval

	var staleTaskIDs []string
	for _, task := range state.Tasks {
		if task.Status != models.TaskStatusImplementing {
			continue
		}
		if task.AssignedTo == nil {
			continue
		}
		assignee := *task.AssignedTo
		if assignee == callerAgentID {
			// Own task — not stale, handled by normal re-claim path.
			continue
		}

		agent, exists := state.Agents[assignee]
		if !exists {
			// Assigned to unknown agent — treat as stale.
			staleTaskIDs = append(staleTaskIDs, task.ID)
			continue
		}
		if time.Since(agent.Heartbeat) > staleThreshold {
			staleTaskIDs = append(staleTaskIDs, task.ID)
		}
	}

	if len(staleTaskIDs) == 0 {
		return false
	}

	// Release stale claims.
	err = bb.Modify(func(s *models.State) error {
		for _, taskID := range staleTaskIDs {
			t := s.FindTask(taskID)
			if t == nil || t.Status != models.TaskStatusImplementing {
				continue
			}
			logger.Warn("Releasing stale claim on task",
				"task_id", taskID,
				"assigned_to", t.AssignedTo,
				"caller", callerAgentID)
			if err := t.Transition(models.TaskStatusReady); err != nil {
				logger.Warn("Failed to transition stale task to READY",
					"task_id", taskID, "error", err)
				continue
			}
			t.AssignedTo = nil
			t.LeaseExpires = nil
		}
		return nil
	})
	if err != nil {
		logger.Warn("detectAndFixStaleClaim: modify failed", "error", err)
		return false
	}

	return true
}

func verifyPlannerStateChanges(_ *db.Blackboard, _ *models.State) error {
	return nil
}
