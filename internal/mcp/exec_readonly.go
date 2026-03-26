package mcp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/liza-mas/liza/internal/roles"
)

const (
	defaultExecTimeout = 30 * time.Second
	maxExecTimeout     = 120 * time.Second
)

func (s *Server) handleExec(params map[string]any) (any, error) {
	command, err := requireString(params, "command")
	if err != nil {
		return nil, err
	}

	if s.role == roles.RuntimeAuditor && !isReadOnlyCommand(command) {
		return nil, fmt.Errorf("auditor exec restricted to read-only commands (go test, go vet, go build, git log/show/diff, ls, cat, find). Blocked: %q", command)
	}

	cwd := s.projectRoot
	if dir, ok := params["cwd"].(string); ok && dir != "" {
		cwd = dir
	}

	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("invalid cwd: %w", err)
	}
	absRoot, err := filepath.Abs(s.projectRoot)
	if err != nil {
		return nil, fmt.Errorf("invalid project root: %w", err)
	}
	absCwd = filepath.Clean(absCwd)
	absRoot = filepath.Clean(absRoot)
	if !strings.EqualFold(absCwd, absRoot) && !strings.HasPrefix(strings.ToLower(absCwd), strings.ToLower(absRoot)+string(filepath.Separator)) {
		return nil, fmt.Errorf("cwd %q is outside project root %q", absCwd, absRoot)
	}

	timeout := defaultExecTimeout
	if value, ok := params["timeout_seconds"].(float64); ok && value > 0 {
		timeout = time.Duration(value) * time.Second
		if timeout > maxExecTimeout {
			timeout = maxExecTimeout
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		ensureWindowsPathext()
		shell := findWindowsShell()
		switch shell {
		case "cmd":
			cmd = exec.CommandContext(ctx, "cmd", "/C", command)
		default:
			cmd = exec.CommandContext(ctx, shell, "-NoProfile", "-NonInteractive", "-Command", command)
		}
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	cmd.Dir = absCwd

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() != nil {
			result := fmt.Sprintf("Command timed out after %s.\n", timeout)
			if stdout.Len() > 0 {
				result += fmt.Sprintf("--- partial stdout ---\n%s\n", stdout.String())
			}
			if stderr.Len() > 0 {
				result += fmt.Sprintf("--- partial stderr ---\n%s\n", stderr.String())
			}
			if stdout.Len() == 0 && stderr.Len() == 0 {
				result += "(no output captured before timeout)\n"
			}
			return textResult(result)
		} else {
			return nil, fmt.Errorf("exec failed: %w", runErr)
		}
	}

	result := fmt.Sprintf("Exit code: %d\n", exitCode)
	if stdout.Len() > 0 {
		result += fmt.Sprintf("--- stdout ---\n%s\n", stdout.String())
	}
	if stderr.Len() > 0 {
		result += fmt.Sprintf("--- stderr ---\n%s\n", stderr.String())
	}
	if stdout.Len() == 0 && stderr.Len() == 0 {
		result += "(no output)\n"
	}

	return textResult(result)
}

func ensureWindowsPathext() {
	pathext := os.Getenv("PATHEXT")
	if !strings.Contains(strings.ToUpper(pathext), ".EXE") {
		os.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD;.VBS;.VBE;.JS;.JSE;.WSF;.WSH;.MSC;.PS1;.CPL")
	}
}

func findWindowsShell() string {
	if _, err := exec.LookPath("pwsh"); err == nil {
		return "pwsh"
	}
	if _, err := exec.LookPath("powershell"); err == nil {
		return "powershell"
	}
	return "cmd"
}

func isReadOnlyCommand(command string) bool {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return false
	}
	if strings.Contains(cmd, ">") || strings.Contains(cmd, ">>") {
		return false
	}

	parts := []string{cmd}
	for _, sep := range []string{"|", ";", "&&", "||"} {
		var expanded []string
		for _, part := range parts {
			expanded = append(expanded, strings.Split(part, sep)...)
		}
		parts = expanded
	}

	for _, part := range parts {
		fields := strings.Fields(strings.ToLower(strings.TrimSpace(part)))
		if len(fields) == 0 {
			continue
		}
		base := fields[0]
		allowed := false
		switch {
		case base == "go" && len(fields) > 1 && (fields[1] == "test" || fields[1] == "vet" || fields[1] == "build" || fields[1] == "version" || fields[1] == "env"):
			allowed = true
		case base == "git" && len(fields) > 1 && (fields[1] == "log" || fields[1] == "show" || fields[1] == "diff" || fields[1] == "status" || fields[1] == "rev-parse" || fields[1] == "branch" || fields[1] == "remote" || fields[1] == "ls-files" || fields[1] == "cat-file" || fields[1] == "describe"):
			allowed = true
		case base == "ls" || base == "dir" || base == "cat" || base == "find" || base == "head" || base == "tail" || base == "wc" || base == "grep" || base == "tree" || base == "file" || base == "which" || base == "where" || base == "type" || base == "echo" || base == "pwd" || base == "date":
			allowed = true
		case base == "get-childitem" || base == "get-content" || base == "get-item" || base == "test-path" || base == "select-string" || base == "get-location":
			allowed = true
		}
		if !allowed {
			return false
		}
	}

	return true
}
