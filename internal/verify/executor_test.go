package verify

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRunVerification(t *testing.T) {
	t.Parallel()

	workdir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Timeout = 10 * time.Second

	// Determine platform-appropriate commands
	echoCmd := "echo hello"
	failCmd := "exit 1"
	if runtime.GOOS == "windows" {
		failCmd = "cmd /C exit 1"
	}

	tests := []struct {
		name       string
		commands   []string
		wantPassed bool
		wantCount  int // expected number of CommandResults
	}{
		{
			name:       "empty commands is vacuously true",
			commands:   []string{},
			wantPassed: true,
			wantCount:  0,
		},
		{
			name:       "single passing command",
			commands:   []string{echoCmd},
			wantPassed: true,
			wantCount:  1,
		},
		{
			name:       "single failing command",
			commands:   []string{failCmd},
			wantPassed: false,
			wantCount:  1,
		},
		{
			name:       "multiple passing commands",
			commands:   []string{echoCmd, echoCmd},
			wantPassed: true,
			wantCount:  2,
		},
		{
			name:       "first fails stops early",
			commands:   []string{failCmd, echoCmd},
			wantPassed: false,
			wantCount:  1, // second command never runs
		},
		{
			name:       "second fails",
			commands:   []string{echoCmd, failCmd},
			wantPassed: false,
			wantCount:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			result := RunVerification(ctx, tt.commands, workdir, cfg)

			if result.Passed != tt.wantPassed {
				t.Errorf("Passed = %v, want %v", result.Passed, tt.wantPassed)
			}

			if len(result.Results) != tt.wantCount {
				t.Errorf("Results count = %d, want %d", len(result.Results), tt.wantCount)
			}
		})
	}
}

func TestRunVerificationWithTimeout(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("timeout test uses sleep command not available on Windows cmd")
	}

	workdir := t.TempDir()
	cfg := Config{
		Timeout:        500 * time.Millisecond,
		MaxOutputBytes: 1024,
	}

	ctx := context.Background()
	result := RunVerification(ctx, []string{"sleep 10"}, workdir, cfg)

	if result.Passed {
		t.Error("expected timeout to cause failure")
	}

	if len(result.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result.Results))
	}

	if result.Results[0].ExitCode == 0 {
		t.Error("expected non-zero exit code for timeout")
	}
}

func TestRunVerificationWorkdir(t *testing.T) {
	t.Parallel()

	workdir := t.TempDir()
	// Create a marker file in workdir
	markerPath := filepath.Join(workdir, "marker.txt")
	if err := os.WriteFile(markerPath, []byte("found"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.Timeout = 10 * time.Second

	var checkCmd string
	if runtime.GOOS == "windows" {
		checkCmd = "type marker.txt"
	} else {
		checkCmd = "cat marker.txt"
	}

	ctx := context.Background()
	result := RunVerification(ctx, []string{checkCmd}, workdir, cfg)

	if !result.Passed {
		t.Errorf("expected command to pass in correct workdir, got: %+v", result.Results)
	}

	if len(result.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result.Results))
	}

	if result.Results[0].Output == "" {
		t.Error("expected output from cat/type command")
	}
}

func TestRunVerificationOutputCapture(t *testing.T) {
	t.Parallel()

	workdir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Timeout = 10 * time.Second

	ctx := context.Background()
	result := RunVerification(ctx, []string{"echo verification-output"}, workdir, cfg)

	if !result.Passed {
		t.Fatal("expected pass")
	}

	if len(result.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result.Results))
	}

	if result.Results[0].Output == "" {
		t.Error("expected captured output")
	}

	if result.Results[0].Duration == 0 {
		t.Error("expected non-zero duration")
	}
}

// TestSanitizeCommand verifies that agent-double-escaped quotes are cleaned up.
// Agents often produce \"arg with spaces\" instead of "arg with spaces" when
// constructing MCP JSON calls, resulting in literal \\" in the stored command.
func TestSanitizeCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no escaping needed",
			input: `echo hello`,
			want:  `echo hello`,
		},
		{
			name:  "double-escaped quotes stripped",
			input: `python todo.py add \"Test task\"`,
			want:  `python todo.py add "Test task"`,
		},
		{
			name:  "multiple escaped pairs",
			input: `cmd \"arg one\" \"arg two\"`,
			want:  `cmd "arg one" "arg two"`,
		},
		{
			name:  "already correct quoting unchanged",
			input: `python -c "print('hi')"`,
			want:  `python -c "print('hi')"`,
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := SanitizeCommand(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeCommand(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestVerifyCommandWithEscapedQuotes is a regression test for the Windows
// verify-command escaping bug found during live pipeline testing.
// The agent stored: python todo.py add \"Test task\"
// Expected behavior: the sanitizer strips the \\ so the shell groups
// "Test task" as a single argument.
func TestVerifyCommandWithEscapedQuotes(t *testing.T) {
	t.Parallel()

	workdir := t.TempDir()

	// Create a tiny script that prints its argument count
	var script, cmd string
	if runtime.GOOS == "windows" {
		script = "@echo off\necho ARGS=%*"
		scriptPath := filepath.Join(workdir, "check.cmd")
		if err := os.WriteFile(scriptPath, []byte(script), 0644); err != nil {
			t.Fatal(err)
		}
		cmd = `check.cmd \"hello world\"`
	} else {
		script = "#!/bin/sh\necho \"ARGC=$#\""
		scriptPath := filepath.Join(workdir, "check.sh")
		if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
		cmd = `sh check.sh \"hello world\"`
	}

	cfg := DefaultConfig()
	cfg.Timeout = 10 * time.Second

	ctx := context.Background()
	result := RunVerification(ctx, []string{cmd}, workdir, cfg)

	if !result.Passed {
		t.Errorf("expected verify command to pass after sanitization, got: %s", result.Results[0].Output)
	}
}

// TestShellCommandPlatform verifies ShellCommand uses the correct shell.
func TestShellCommandPlatform(t *testing.T) {
	t.Parallel()

	cmd := ShellCommand(context.Background(), "echo test", t.TempDir())
	if runtime.GOOS == "windows" {
		if cmd.Path == "" || cmd.Args[0] != "cmd" {
			t.Errorf("expected cmd.exe on Windows, got: %v", cmd.Args)
		}
	} else {
		if cmd.Path == "" || cmd.Args[0] != "sh" {
			t.Errorf("expected sh on Unix, got: %v", cmd.Args)
		}
	}
}
