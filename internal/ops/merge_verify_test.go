package ops

import (
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"

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
