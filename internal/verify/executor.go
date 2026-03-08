// Package verify provides deterministic verification of task outputs by
// executing verification commands and checking exit codes.
// PASS = all commands exit 0, FAIL = any command exits non-zero.
package verify

import (
	"context"
	"fmt"
	"os"
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

// SanitizeCommand cleans up a verify command string for safe shell execution.
// Agents often double-escape quotes in MCP JSON calls, producing literal
// backslash-quote (\" → \") in the stored command. Neither sh -c nor
// cmd /C treats \" as a grouping quote, so the arguments get split
// incorrectly.  Stripping the backslash before each quote restores the
// intended quoting behaviour on all platforms.
func SanitizeCommand(cmdStr string) string {
	return strings.ReplaceAll(cmdStr, `\"`, `"`)
}

// ShellCommand returns an exec.Cmd that runs cmdStr through the
// platform-appropriate shell (sh -c on Unix, cmd /C on Windows).
//
// On Windows, the command is written to a temporary .cmd file to avoid
// Go's exec.Command argument escaping (syscall.EscapeArg) which
// re-escapes quote characters, breaking commands that contain quoted
// arguments like: python todo.py add "Test task"
//
// The caller must call the returned cleanup function when done.
func ShellCommand(ctx context.Context, cmdStr, workdir string) (cmd *exec.Cmd, cleanup func()) {
	cleanup = func() {} // no-op default

	if runtime.GOOS == "windows" {
		// Write command to a temp .cmd file to bypass Go's argument escaping.
		// Go's EscapeArg would turn "Test task" into \"Test task\", which
		// cmd.exe then mishandles (strips outer quotes, leaves backslashes).
		tmpFile, err := os.CreateTemp(workdir, "liza-verify-*.cmd")
		if err == nil {
			_, _ = tmpFile.WriteString("@echo off\r\n" + cmdStr + "\r\n")
			tmpFile.Close()
			cmd = exec.CommandContext(ctx, "cmd", "/C", tmpFile.Name())
			cmd.Dir = workdir
			cleanup = func() { os.Remove(tmpFile.Name()) }
			return cmd, cleanup
		}
		// Fallback: if temp file creation fails, use direct invocation
		cmd = exec.CommandContext(ctx, "cmd", "/C", cmdStr)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", cmdStr)
	}
	cmd.Dir = workdir
	return cmd, cleanup
}

// runCommand executes a single shell command and captures its output.
func runCommand(ctx context.Context, cmdStr, workdir string, cfg Config) CommandResult {
	start := time.Now()

	// Sanitize command to handle agent double-escaping
	cmdStr = SanitizeCommand(cmdStr)

	var cmdCtx context.Context
	var cancel context.CancelFunc

	if cfg.Timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, cfg.Timeout)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	cmd, cleanup := ShellCommand(cmdCtx, cmdStr, workdir)
	defer cleanup()

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
