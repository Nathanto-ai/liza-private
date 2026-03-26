package ops

import (
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/verify"

	"gopkg.in/yaml.v3"
)

func TestVerificationResult_YAMLMarshalUnmarshal(t *testing.T) {
	t.Parallel()

	t.Run("passed=true marshals and unmarshals correctly", func(t *testing.T) {
		ts, _ := time.Parse(time.RFC3339, "2025-06-15T12:00:00Z")
		vr := models.VerificationResult{
			Passed:    true,
			Output:    "all tests passed",
			Timestamp: ts,
		}

		data, err := yaml.Marshal(&vr)
		if err != nil {
			t.Fatalf("Marshal error: %v", err)
		}

		var got models.VerificationResult
		if err := yaml.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal error: %v", err)
		}

		if got.Passed != true {
			t.Errorf("Passed = %v, want true", got.Passed)
		}
		if got.Output != "all tests passed" {
			t.Errorf("Output = %q, want %q", got.Output, "all tests passed")
		}
		if !got.Timestamp.Equal(ts) {
			t.Errorf("Timestamp = %v, want %v", got.Timestamp, ts)
		}
	})

	t.Run("passed=false marshals and unmarshals correctly", func(t *testing.T) {
		ts, _ := time.Parse(time.RFC3339, "2025-06-15T12:00:00Z")
		vr := models.VerificationResult{
			Passed:    false,
			Output:    "exit code 1: test_integration failed",
			Timestamp: ts,
		}

		data, err := yaml.Marshal(&vr)
		if err != nil {
			t.Fatalf("Marshal error: %v", err)
		}

		var got models.VerificationResult
		if err := yaml.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal error: %v", err)
		}

		if got.Passed != false {
			t.Errorf("Passed = %v, want false", got.Passed)
		}
		if got.Output != "exit code 1: test_integration failed" {
			t.Errorf("Output = %q, want %q", got.Output, "exit code 1: test_integration failed")
		}
	})
}

func TestTaskWithVerificationResult_YAMLRoundTrip(t *testing.T) {
	t.Parallel()

	ts, _ := time.Parse(time.RFC3339, "2025-06-15T12:00:00Z")
	created, _ := time.Parse(time.RFC3339, "2025-06-15T10:00:00Z")

	task := models.Task{
		ID:          "task-1",
		Type:        models.TaskTypeCoding,
		Description: "Test task with verification",
		Status:      models.TaskStatusMerged,
		Priority:    1,
		SpecRef:     "specs/test.md",
		DoneWhen:    "tests pass",
		Scope:       "test scope",
		Created:     created,
		History:     []models.TaskHistoryEntry{},
		VerificationResult: &models.VerificationResult{
			Passed:    true,
			Output:    "all checks green",
			Timestamp: ts,
		},
	}

	data, err := yaml.Marshal(&task)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var got models.Task
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if got.VerificationResult == nil {
		t.Fatal("VerificationResult is nil after round-trip")
	}
	if got.VerificationResult.Passed != true {
		t.Errorf("VerificationResult.Passed = %v, want true", got.VerificationResult.Passed)
	}
	if got.VerificationResult.Output != "all checks green" {
		t.Errorf("VerificationResult.Output = %q, want %q", got.VerificationResult.Output, "all checks green")
	}
}

func TestVerificationResult_RichSchema_YAMLRoundTrip(t *testing.T) {
	t.Parallel()

	ts, _ := time.Parse(time.RFC3339, "2025-06-15T12:00:00Z")
	vr := models.VerificationResult{
		Passed:    true,
		Output:    "[PASS] go test ./...\n[PASS] go vet ./...",
		Timestamp: ts,
		Phase:     "merge",
		Commands: []models.VerificationCmdResult{
			{
				Command:  "go test ./...",
				ExitCode: 0,
				Output:   "ok all",
				Duration: 5 * time.Second,
			},
			{
				Command:  "go vet ./...",
				ExitCode: 0,
				Output:   "",
				Duration: 2 * time.Second,
			},
		},
	}

	data, err := yaml.Marshal(&vr)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var got models.VerificationResult
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if !got.Passed {
		t.Error("Passed = false, want true")
	}
	if got.Phase != "merge" {
		t.Errorf("Phase = %q, want %q", got.Phase, "merge")
	}
	if len(got.Commands) != 2 {
		t.Fatalf("len(Commands) = %d, want 2", len(got.Commands))
	}
	if got.Commands[0].Command != "go test ./..." {
		t.Errorf("Commands[0].Command = %q, want %q", got.Commands[0].Command, "go test ./...")
	}
	if got.Commands[0].ExitCode != 0 {
		t.Errorf("Commands[0].ExitCode = %d, want 0", got.Commands[0].ExitCode)
	}
	if got.Commands[1].Duration != 2*time.Second {
		t.Errorf("Commands[1].Duration = %v, want %v", got.Commands[1].Duration, 2*time.Second)
	}
}

func TestToVerificationResult(t *testing.T) {
	t.Parallel()

	t.Run("converts passing result with per-command data", func(t *testing.T) {
		vr := &verify.Result{
			Passed: true,
			Results: []verify.CommandResult{
				{Command: "echo hello", ExitCode: 0, Output: "hello", Duration: time.Second},
			},
		}
		got := toVerificationResult(vr, "merge")
		if !got.Passed {
			t.Error("Passed = false, want true")
		}
		if got.Phase != "merge" {
			t.Errorf("Phase = %q, want %q", got.Phase, "merge")
		}
		if len(got.Commands) != 1 {
			t.Fatalf("len(Commands) = %d, want 1", len(got.Commands))
		}
		if got.Commands[0].Command != "echo hello" {
			t.Errorf("Commands[0].Command = %q, want %q", got.Commands[0].Command, "echo hello")
		}
		if got.Commands[0].ExitCode != 0 {
			t.Errorf("Commands[0].ExitCode = %d, want 0", got.Commands[0].ExitCode)
		}
	})

	t.Run("converts failing result with error details", func(t *testing.T) {
		vr := &verify.Result{
			Passed: false,
			Results: []verify.CommandResult{
				{Command: "go test ./...", ExitCode: 0, Output: "ok", Duration: time.Second},
				{Command: "false", ExitCode: 1, Output: "FAIL", Duration: 100 * time.Millisecond, Error: "exit status 1"},
			},
		}
		got := toVerificationResult(vr, "post_submission")
		if got.Passed {
			t.Error("Passed = true, want false")
		}
		if got.Phase != "post_submission" {
			t.Errorf("Phase = %q, want %q", got.Phase, "post_submission")
		}
		if len(got.Commands) != 2 {
			t.Fatalf("len(Commands) = %d, want 2", len(got.Commands))
		}
		if got.Commands[1].ExitCode != 1 {
			t.Errorf("Commands[1].ExitCode = %d, want 1", got.Commands[1].ExitCode)
		}
		if got.Commands[1].Error != "exit status 1" {
			t.Errorf("Commands[1].Error = %q, want %q", got.Commands[1].Error, "exit status 1")
		}
	})
}
