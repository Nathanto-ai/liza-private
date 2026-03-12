package filelock

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestIsProcessAlive_CurrentProcess verifies that the current process is
// detected as alive. This was the core bug on Windows where Signal(0)
// always returned an error, making isProcessAlive report every process
// as dead — including the lock holder.
func TestIsProcessAlive_CurrentProcess(t *testing.T) {
	pid := os.Getpid()
	if !isProcessAlive(pid) {
		t.Errorf("isProcessAlive(%d) = false for current process; want true", pid)
	}
}

// TestIsProcessAlive_InvalidPID verifies that negative/zero PIDs are not alive.
func TestIsProcessAlive_InvalidPID(t *testing.T) {
	for _, pid := range []int{0, -1, -999} {
		if isProcessAlive(pid) {
			t.Errorf("isProcessAlive(%d) = true; want false", pid)
		}
	}
}

// TestIsProcessAlive_DeadProcess verifies that a dead process is detected correctly.
func TestIsProcessAlive_DeadProcess(t *testing.T) {
	// Start a subprocess and immediately kill it to get a dead PID
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "GO_TEST_SUBPROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start subprocess: %v", err)
	}
	pid := cmd.Process.Pid
	cmd.Process.Kill()
	cmd.Wait()

	// Give OS time to clean up the process
	time.Sleep(100 * time.Millisecond)

	if isProcessAlive(pid) {
		t.Errorf("isProcessAlive(%d) = true for dead process; want false", pid)
	}
}

// TestStaleLockDetection_LiveProcess_NotStale is a regression test for the
// Windows corruption bug. A lock held by a live process must NOT be detected
// as stale. The old Unix-only Signal(0) implementation always returned
// stale=true on Windows, causing valid locks to be cleaned up.
func TestStaleLockDetection_LiveProcess_NotStale(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "state.yaml")

	fl := New(protectedPath)

	// Write a PID file pointing to the current (live) process
	pid := os.Getpid()
	if err := os.WriteFile(fl.pidPath, []byte(strconv.Itoa(pid)), 0644); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}

	isStale, stalePID := fl.isLockStale()
	if isStale {
		t.Errorf("isLockStale() = (true, %d) for live process %d; want (false, _)", stalePID, pid)
	}
}

// TestStaleLockDetection_DeadProcess_IsStale verifies that a lock from a
// dead process IS detected as stale.
func TestStaleLockDetection_DeadProcess_IsStale(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "state.yaml")

	fl := New(protectedPath)

	// Start and kill a subprocess to get a dead PID
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "GO_TEST_SUBPROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start subprocess: %v", err)
	}
	deadPID := cmd.Process.Pid
	cmd.Process.Kill()
	cmd.Wait()
	time.Sleep(100 * time.Millisecond)

	if err := os.WriteFile(fl.pidPath, []byte(strconv.Itoa(deadPID)), 0644); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}

	isStale, stalePID := fl.isLockStale()
	if !isStale {
		t.Errorf("isLockStale() = (false, %d) for dead process %d; want (true, %d)", stalePID, deadPID, deadPID)
	}
	if stalePID != deadPID {
		t.Errorf("stalePID = %d, want %d", stalePID, deadPID)
	}
}

// TestStaleLockDetection_NoPIDFile verifies that missing PID file is not
// treated as stale (conservative default).
func TestStaleLockDetection_NoPIDFile(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "state.yaml")

	fl := New(protectedPath)

	isStale, _ := fl.isLockStale()
	if isStale {
		t.Error("isLockStale() = true with no PID file; want false")
	}
}

// TestLiveLockNotCleaned verifies that when lock acquisition times out,
// a lock held by a LIVE process is NOT cleaned up. This is the end-to-end
// regression test for the Windows corruption scenario.
func TestLiveLockNotCleaned(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "state.yaml")

	fl := New(protectedPath).WithTimeout(500 * time.Millisecond)

	// Acquire the lock from "this" process
	var lockHeld bool
	errCh := make(chan error, 1)

	go func() {
		errCh <- fl.WithLock(func() error {
			lockHeld = true
			// Hold lock for longer than the second lock's timeout
			time.Sleep(2 * time.Second)
			return nil
		})
	}()

	// Wait for lock to be acquired
	time.Sleep(200 * time.Millisecond)
	if !lockHeld {
		t.Fatal("first lock was not acquired")
	}

	// Now try to acquire with a short timeout — should fail with timeout,
	// NOT falsely detect the lock as stale and clean it up
	fl2 := New(protectedPath).WithTimeout(500 * time.Millisecond)
	err := fl2.WithLock(func() error {
		t.Error("second lock holder should not have acquired the lock")
		return nil
	})

	if err == nil {
		t.Error("second WithLock should have returned an error")
	}

	// The error should be a timeout, not a stale lock cleanup
	var lockErr *LockError
	if ok := isLockError(err, &lockErr); ok {
		if lockErr.Type == LockErrorStale {
			t.Error("lock from live process was incorrectly identified as stale — this is the Windows corruption bug")
		}
	}

	// Wait for the first lock to finish
	if firstErr := <-errCh; firstErr != nil {
		t.Errorf("first lock holder error: %v", firstErr)
	}
}

func isLockError(err error, target **LockError) bool {
	if le, ok := err.(*LockError); ok {
		*target = le
		return true
	}
	return false
}
