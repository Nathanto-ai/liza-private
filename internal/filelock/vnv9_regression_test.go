package filelock

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
)

// TestFix48_SharingViolationClassifiedCorrectly verifies that Windows sharing
// violations (errno 32) are classified as LockErrorSharingViolation on Windows
// and as LockErrorFilesystem on other platforms.
func TestFix48_SharingViolationClassifiedCorrectly(t *testing.T) {
	// Construct an os.PathError with errno 32 (ERROR_SHARING_VIOLATION on Windows)
	sharingErr := &os.PathError{
		Op:   "open",
		Path: "state.yaml",
		Err:  syscall.Errno(32),
	}

	classified := ClassifyLockError(sharingErr)

	if runtime.GOOS == "windows" {
		if classified.Type != LockErrorSharingViolation {
			t.Errorf("ClassifyLockError on Windows: got %v, want LockErrorSharingViolation", classified.Type)
		}
	} else {
		// On Unix, errno 32 is EPIPE — should be classified as filesystem error
		if classified.Type != LockErrorFilesystem {
			t.Errorf("ClassifyLockError on Unix: got %v, want LockErrorFilesystem", classified.Type)
		}
	}
}

// TestFix48_SharingViolationTypeString verifies the string representation.
func TestFix48_SharingViolationTypeString(t *testing.T) {
	if got := LockErrorSharingViolation.String(); got != "sharing_violation" {
		t.Errorf("LockErrorSharingViolation.String() = %q, want %q", got, "sharing_violation")
	}
}

// TestFix48_WithRetryBackoffRetriesSharingViolation verifies that
// WithRetryBackoff retries when fn() returns a sharing violation error,
// rather than immediately returning.
func TestFix48_WithRetryBackoffRetriesSharingViolation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("sharing violation retries only apply on Windows")
	}

	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "data.yaml")
	if err := os.WriteFile(protectedPath, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	fl := New(protectedPath)

	var attempts int32
	sharingErr := &os.PathError{
		Op:   "open",
		Path: protectedPath,
		Err:  syscall.Errno(32), // ERROR_SHARING_VIOLATION
	}

	// Callback fails twice with sharing violation, then succeeds
	err := fl.WithRetryBackoff("test-sharing", 3, func() error {
		n := atomic.AddInt32(&attempts, 1)
		if n <= 2 {
			return sharingErr
		}
		return nil
	})

	if err != nil {
		t.Fatalf("WithRetryBackoff should succeed after retries, got: %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Errorf("expected 3 attempts (2 failures + 1 success), got %d", got)
	}
}

// TestFix48_WithRetryBackoffSharingViolationExhausted verifies that
// WithRetryBackoff properly fails after exhausting retries on persistent
// sharing violations.
func TestFix48_WithRetryBackoffSharingViolationExhausted(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("sharing violation retries only apply on Windows")
	}

	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "data.yaml")
	if err := os.WriteFile(protectedPath, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	fl := New(protectedPath)

	var attempts int32
	sharingErr := &os.PathError{
		Op:   "open",
		Path: protectedPath,
		Err:  syscall.Errno(32),
	}

	// Always fail with sharing violation
	err := fl.WithRetryBackoff("test-sharing-exhaust", 2, func() error {
		atomic.AddInt32(&attempts, 1)
		return sharingErr
	})

	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	// Should have tried 3 times (initial + 2 retries)
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Errorf("expected 3 attempts, got %d", got)
	}
}

// TestFix48_NonSharingViolationFromCallbackNotRetried verifies that non-sharing-violation
// errors from fn() are still returned immediately without retry.
func TestFix48_NonSharingViolationFromCallbackNotRetried(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "data.yaml")
	if err := os.WriteFile(protectedPath, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	fl := New(protectedPath)

	var attempts int32

	// Return a plain error (not a sharing violation)
	err := fl.WithRetryBackoff("test-no-retry", 3, func() error {
		atomic.AddInt32(&attempts, 1)
		return fmt.Errorf("application error")
	})

	if err == nil {
		t.Fatal("expected error")
	}
	// Should have tried only once — no retry for plain errors
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("expected 1 attempt (no retry), got %d", got)
	}
}
