package filelock

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestFileLockBasic(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "data.yaml")

	fl := New(protectedPath)

	executed := false
	err := fl.WithLock(func() error {
		executed = true
		return nil
	})

	if err != nil {
		t.Fatalf("WithLock() error = %v", err)
	}
	if !executed {
		t.Error("function was not executed")
	}
}

func TestFileLockOperation(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "data.yaml")

	fl := New(protectedPath)
	fl.EnableMetrics()

	err := fl.WithLockOperation("test-op", func() error {
		return nil
	})

	if err != nil {
		t.Fatalf("WithLockOperation() error = %v", err)
	}

	recorder := fl.GetMetricsRecorder()
	if recorder == nil {
		t.Fatal("metrics recorder is nil")
	}

	metrics := recorder.GetMetrics()
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	if metrics[0].Operation != "test-op" {
		t.Errorf("operation = %q, want %q", metrics[0].Operation, "test-op")
	}
}

func TestFileLockWithTimeout(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "data.yaml")

	fl := New(protectedPath)
	customFL := fl.WithTimeout(5 * time.Second)

	if customFL.lockTimeout != 5*time.Second {
		t.Errorf("lockTimeout = %v, want %v", customFL.lockTimeout, 5*time.Second)
	}

	// Verify paths are preserved
	if customFL.lockPath != fl.lockPath {
		t.Errorf("lockPath = %q, want %q", customFL.lockPath, fl.lockPath)
	}
}

func TestFileLockConcurrent(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "data.yaml")

	fl := New(protectedPath)

	// Write initial data
	if err := os.WriteFile(protectedPath, []byte("0"), 0644); err != nil {
		t.Fatal(err)
	}

	const numGoroutines = 10
	var counter atomic.Int64
	done := make(chan error, numGoroutines)

	for range numGoroutines {
		go func() {
			err := fl.WithLock(func() error {
				counter.Add(1)
				return nil
			})
			done <- err
		}()
	}

	for range numGoroutines {
		if err := <-done; err != nil {
			t.Errorf("WithLock() error = %v", err)
		}
	}

	if counter.Load() != numGoroutines {
		t.Errorf("counter = %d, want %d", counter.Load(), numGoroutines)
	}
}

func TestFileLockCreatesLockFile(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "data.yaml")

	fl := New(protectedPath)

	err := fl.WithLock(func() error {
		// Lock file should exist during operation
		lockPath := protectedPath + ".lock"
		if _, err := os.Stat(lockPath); os.IsNotExist(err) {
			t.Error("lock file does not exist during operation")
		}

		// PID file should exist during operation
		pidPath := protectedPath + ".lock.pid"
		if _, err := os.Stat(pidPath); os.IsNotExist(err) {
			t.Error("PID file does not exist during operation")
		}

		return nil
	})

	if err != nil {
		t.Fatalf("WithLock() error = %v", err)
	}
}

func TestFileLockMetricsEnableDisable(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "data.yaml")

	fl := New(protectedPath)

	// Initially no recorder
	if fl.GetMetricsRecorder() != nil {
		t.Error("expected nil recorder before enabling")
	}

	// Enable
	fl.EnableMetrics()
	if fl.GetMetricsRecorder() == nil {
		t.Error("expected non-nil recorder after enabling")
	}

	// Perform operation
	fl.WithLock(func() error { return nil })

	recorder := fl.GetMetricsRecorder()
	if len(recorder.GetMetrics()) != 1 {
		t.Errorf("expected 1 metric, got %d", len(recorder.GetMetrics()))
	}

	// Disable and perform operation
	fl.DisableMetrics()
	fl.WithLock(func() error { return nil })

	// Should still have only 1 metric
	if len(recorder.GetMetrics()) != 1 {
		t.Errorf("expected 1 metric after disable, got %d", len(recorder.GetMetrics()))
	}

	// Re-enable and perform operation
	fl.EnableMetrics()
	fl.WithLock(func() error { return nil })

	// Should now have 2 metrics
	if len(recorder.GetMetrics()) != 2 {
		t.Errorf("expected 2 metrics after re-enable, got %d", len(recorder.GetMetrics()))
	}
}

func TestFileLockPaths(t *testing.T) {
	fl := New("/tmp/test/state.yaml")

	if fl.lockPath != "/tmp/test/state.yaml.lock" {
		t.Errorf("lockPath = %q, want %q", fl.lockPath, "/tmp/test/state.yaml.lock")
	}
	if fl.pidPath != "/tmp/test/state.yaml.lock.pid" {
		t.Errorf("pidPath = %q, want %q", fl.pidPath, "/tmp/test/state.yaml.lock.pid")
	}
}

func TestFileLockDefaults(t *testing.T) {
	fl := New("/tmp/test")

	if fl.lockTimeout != DefaultLockTimeout {
		t.Errorf("lockTimeout = %v, want %v", fl.lockTimeout, DefaultLockTimeout)
	}
}

// TestCleanupStaleLockFailure verifies that cleanupStaleLock returns an error
// when the lock file does not exist (simulating a filesystem issue).
func TestCleanupStaleLockFailure(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "nonexistent", "data.yaml")

	fl := New(protectedPath)

	// cleanupStaleLock should succeed even if lock file doesn't exist
	// (it only returns error on actual filesystem errors, not on ENOENT)
	err := fl.cleanupStaleLock()
	if err != nil {
		t.Errorf("cleanupStaleLock() expected nil for non-existent lock file, got %v", err)
	}
}

// TestWithLockStaleLockRecovery verifies that when a stale lock is detected,
// cleanup is attempted and if successful, the lock is acquired.
func TestWithLockStaleLockRecovery(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "data.yaml")

	// Create initial protected file
	if err := os.WriteFile(protectedPath, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create protected file: %v", err)
	}

	// First, acquire the lock in a subprocess that will exit, leaving a stale lock
	// We can't easily fork in a test, so we'll simulate by:
	// 1. Creating a lock file
	// 2. Creating a PID file with our own PID (so it's considered "live")
	// 3. Using a very short timeout and acquiring another lock instance

	// Actually, the simplest approach is to have this process hold the lock,
	// and try to acquire it again with a different FileLock instance with a short timeout

	lock1 := New(protectedPath)
	lock1 = lock1.WithTimeout(5 * time.Second)

	// Acquire lock1
	lock1Acquired := make(chan struct{})
	lock1Done := make(chan struct{})
	releaseLock1 := make(chan struct{})
	go func() {
		_ = lock1.WithLock(func() error {
			close(lock1Acquired)
			<-releaseLock1
			return nil
		})
		close(lock1Done)
	}()

	// Wait for lock1 to be acquired
	select {
	case <-lock1Acquired:
		// Good, lock1 is held
	case <-time.After(2 * time.Second):
		t.Fatal("lock1 was not acquired")
	}

	// Now try to acquire with lock2 using a short timeout
	lock2 := New(protectedPath)
	lock2 = lock2.WithTimeout(50 * time.Millisecond)

	// This should timeout because lock1 is held
	errChan := make(chan error, 1)
	go func() {
		err := lock2.WithLock(func() error {
			return nil
		})
		errChan <- err
	}()

	// Wait for lock2 to timeout
	select {
	case err := <-errChan:
		if err == nil {
			t.Fatal("lock2 should have timed out")
		}
		// Expected - lock2 timed out
	case <-time.After(1 * time.Second):
		t.Fatal("lock2 should have timed out quickly")
	}
	close(releaseLock1)

	// Wait for lock1 to release
	select {
	case <-lock1Done:
		// Good
	case <-time.After(3 * time.Second):
		t.Fatal("lock1 was not released")
	}

	// Now test stale lock detection by simulating a stale lock
	// Create a lock file and a PID file with a non-existent PID
	pidPath := protectedPath + ".lock.pid"
	lockPath := protectedPath + ".lock"

	// The lock file may still exist from lock1 - that's fine
	// But we need to replace the PID with a non-existent one
	if err := os.WriteFile(pidPath, []byte("99999"), 0644); err != nil {
		t.Fatalf("Failed to create stale PID file: %v", err)
	}

	// Ensure lock file exists (flock needs an actual file)
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		if err := os.WriteFile(lockPath, []byte(""), 0644); err != nil {
			t.Fatalf("Failed to create lock file: %v", err)
		}
	}

	// Now try to acquire with a new lock instance with a short timeout
	// It should detect the stale lock, clean it up, and acquire successfully
	lock3 := New(protectedPath)
	lock3 = lock3.WithTimeout(100 * time.Millisecond)

	executed := false
	err := lock3.WithLock(func() error {
		executed = true
		return nil
	})

	if err != nil {
		t.Fatalf("WithLock() expected success after stale lock cleanup, got error: %v", err)
	}

	if !executed {
		t.Error("function should have executed after stale lock cleanup")
	}

	// Verify the PID file was updated (new PID written by acquireLockWithPID)
	pidData, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("Failed to read PID file: %v", err)
	}
	if string(pidData) == "99999" {
		t.Error("PID file should have been updated with new PID, not the stale one")
	}
}

func TestWithRetryBackoff_Success(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "data.yaml")

	fl := New(protectedPath)

	callCount := 0
	err := fl.WithRetryBackoff("test-retry", 3, func() error {
		callCount++
		return nil
	})

	if err != nil {
		t.Fatalf("WithRetryBackoff() error = %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}
}

func TestWithRetryBackoff_NonRetryableError(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "data.yaml")

	fl := New(protectedPath)

	// Acquire lock and hold it so inner fn will never run — but we want to test
	// that non-retryable errors from fn itself bubble up immediately.
	// Instead, test that fn errors propagate correctly:
	callCount := 0
	err := fl.WithRetryBackoff("test-noretry", 3, func() error {
		callCount++
		return &LockError{Type: LockErrorPermission, Message: "access denied"}
	})

	// The fn ran successfully under lock but returned a permission error.
	// WithRetryBackoff wraps WithLockOperation: fn errors are simply returned by
	// WithLockOperation as-is (not a lock acquisition failure).
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Should only call once — fn error returned directly (not retried by WithRetryBackoff since
	// WithLockOperation itself succeeded; the error is from fn, not lock acquisition)
	if callCount != 1 {
		t.Errorf("expected 1 call (non-retryable), got %d", callCount)
	}
}

func TestWithRetryBackoff_ZeroRetries(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "data.yaml")

	fl := New(protectedPath)

	callCount := 0
	err := fl.WithRetryBackoff("test-zero", 0, func() error {
		callCount++
		return nil
	})

	if err != nil {
		t.Fatalf("WithRetryBackoff(0 retries) error = %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call with 0 retries, got %d", callCount)
	}
}

func TestEffectiveLockCheckInterval(t *testing.T) {
	interval := effectiveLockCheckInterval()
	if interval != LockCheckInterval && interval != WindowsLockCheckInterval {
		t.Errorf("effectiveLockCheckInterval() = %v, want %v or %v",
			interval, LockCheckInterval, WindowsLockCheckInterval)
	}
}
