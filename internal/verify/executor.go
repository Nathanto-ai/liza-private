// Package verify provides deterministic verification of task outputs by
// executing verification commands and checking exit codes.
// PASS = all commands exit 0, FAIL = any command exits non-zero.
package verify

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Config holds verification execution settings.
type Config struct {
	// Timeout is the maximum duration for each individual command.
	// Zero means no timeout (use context deadline if set).
	Timeout time.Duration

	// MaxOutputBytes limits the captured stdout+stderr per command.
	// Zero means unlimited.
	MaxOutputBytes int
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Timeout:        5 * time.Minute,
		MaxOutputBytes: 1024 * 1024, // 1MB
	}
}

// CommandResult holds the outcome of a single verification command.
type CommandResult struct {
	Command  string        `json:"command"`
	ExitCode int           `json:"exit_code"`
	Output   string        `json:"output"`
	Duration time.Duration `json:"duration"`
	Error    string        `json:"error,omitempty"`
}

// Result holds the aggregate outcome of running all verification commands.
type Result struct {
	Passed  bool            `json:"passed"`
	Results []CommandResult `json:"results"`
}

// RunVerification executes each verify command in sequence in the given workdir.
// Stops on first failure. Returns Passed=true only if all commands exit 0.
// An empty command list is vacuously true (Passed=true).
func RunVerification(ctx context.Context, commands []string, workdir string, cfg Config) *Result {
	result := &Result{Passed: true}

	if len(commands) == 0 {
		return result
	}

	for _, cmdStr := range commands {
		cmdResult := runCommand(ctx, cmdStr, workdir, cfg)
		result.Results = append(result.Results, cmdResult)

		if cmdResult.ExitCode != 0 {
			result.Passed = false
			break // stop on first failure
		}
	}

	return result
}

// runCommand executes a single shell command and captures its output.
func runCommand(ctx context.Context, cmdStr, workdir string, cfg Config) CommandResult {
	start := time.Now()

	var cmdCtx context.Context
	var cancel context.CancelFunc

	if cfg.Timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, cfg.Timeout)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	// Use shell to run the command string
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cmdCtx, "cmd", "/C", cmdStr)
	} else {
		cmd = exec.CommandContext(cmdCtx, "sh", "-c", cmdStr)
	}
	cmd.Dir = workdir

	output, err := cmd.CombinedOutput()
	duration := time.Since(start)

	// Truncate output if needed
	outputStr := string(output)
	if cfg.MaxOutputBytes > 0 && len(outputStr) > cfg.MaxOutputBytes {
		outputStr = outputStr[:cfg.MaxOutputBytes] + "\n... (truncated)"
	}

	cr := CommandResult{
		Command:  cmdStr,
		Duration: duration,
		Output:   outputStr,
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			cr.ExitCode = exitErr.ExitCode()
		} else if ctx.Err() != nil {
			cr.ExitCode = -1
			cr.Error = fmt.Sprintf("timeout after %s", cfg.Timeout)
		} else {
			cr.ExitCode = -1
			cr.Error = err.Error()
		}
	}

	// Trim trailing whitespace for cleaner output
	cr.Output = strings.TrimRight(cr.Output, "\n\r\t ")

	return cr
}
