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
