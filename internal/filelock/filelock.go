package filelock

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/gofrs/flock"
)

const (
	// DefaultLockTimeout is the default maximum time to wait for a file lock.
	DefaultLockTimeout = 10 * time.Second
	// LockCheckInterval is how often to retry lock acquisition.
	LockCheckInterval = 100 * time.Millisecond
)

// FileLock provides file-based mutual exclusion with stale lock detection.
//
// It wraps flock(2) with a polling acquisition loop, PID-based stale lock
// recovery, classified error types, and optional metrics collection.
type FileLock struct {
	lockPath    string
	pidPath     string
	lockTimeout time.Duration

	// Metrics collection (optional)
	metricsRecorder *MetricsRecorder
	enableMetrics   bool
}

// New creates a FileLock that protects the given file path.
// Lock file: protectedPath + ".lock", PID file: protectedPath + ".lock.pid".
func New(protectedPath string) *FileLock {
	return &FileLock{
		lockPath:    protectedPath + ".lock",
		pidPath:     protectedPath + ".lock.pid",
		lockTimeout: DefaultLockTimeout,
	}
}

// WithTimeout returns a new FileLock with the given timeout.
// Metrics state is not shared with the original.
func (fl *FileLock) WithTimeout(timeout time.Duration) *FileLock {
	return &FileLock{
		lockPath:    fl.lockPath,
		pidPath:     fl.pidPath,
		lockTimeout: timeout,
	}
}

// EnableMetrics enables lock metrics collection.
func (fl *FileLock) EnableMetrics() {
	if fl.metricsRecorder == nil {
		fl.metricsRecorder = NewMetricsRecorder()
	}
	fl.enableMetrics = true
}

// DisableMetrics disables lock metrics collection.
func (fl *FileLock) DisableMetrics() {
	fl.enableMetrics = false
}

// GetMetricsRecorder returns the metrics recorder, or nil if not enabled.
func (fl *FileLock) GetMetricsRecorder() *MetricsRecorder {
	return fl.metricsRecorder
}

func (fl *FileLock) acquireLockWithPID() (*flock.Flock, error) {
	lock := flock.New(fl.lockPath)
	acquired, err := lock.TryLock()
	if err != nil {
		return nil, ClassifyLockError(err)
	}
	if !acquired {
		return nil, fmt.Errorf("lock not acquired")
	}

	pid := os.Getpid()
	pidData := []byte(strconv.Itoa(pid))
	if err := os.WriteFile(fl.pidPath, pidData, 0644); err != nil {
		lock.Unlock()
		return nil, ClassifyLockError(err)
	}

	return lock, nil
}

func (fl *FileLock) isLockStale() (bool, int) {
	pidData, err := os.ReadFile(fl.pidPath)
	if err != nil {
		// No PID file or can't read it - assume not stale
		return false, 0
	}

	pid, err := strconv.Atoi(string(pidData))
	if err != nil {
		// Invalid PID format - assume not stale
		return false, 0
	}

	return !isProcessAlive(pid), pid
}

// cleanupStaleLock cleans up after a dead process's lock.
// Only the PID file is removed. The lock file is truncated but preserved
// to maintain inode identity — deleting it would re-introduce the flock
// race described in WithLock's defer block.
func (fl *FileLock) cleanupStaleLock() error {
	os.Remove(fl.pidPath)
	// Truncate lock file (release flock state) without deleting the inode
	if err := os.Truncate(fl.lockPath, 0); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to truncate stale lock file: %w", err)
	}
	return nil
}

// WithLock executes fn while holding an exclusive file lock.
// Equivalent to WithLockOperation with operation "unknown".
func (fl *FileLock) WithLock(fn func() error) error {
	return fl.WithLockOperation("unknown", fn)
}

// WithLockOperation executes fn while holding an exclusive file lock.
// The operation name is recorded in metrics if enabled.
func (fl *FileLock) WithLockOperation(operation string, fn func() error) error {
	var lock *flock.Flock
	var err error

	now := time.Now()
	deadline := now.Add(fl.lockTimeout)
	locked := false

	for time.Now().Before(deadline) {
		lock, err = fl.acquireLockWithPID()
		if err == nil {
			locked = true
			break
		}
		// If it's a non-retryable error (permission, disk full, etc.), fail immediately
		var lockErr *LockError
		if errors.As(err, &lockErr) {
			switch lockErr.Type {
			case LockErrorPermission, LockErrorDiskFull, LockErrorFilesystem:
				return lockErr
			}
		}
		time.Sleep(LockCheckInterval)
	}

	if !locked {
		isStale, stalePID := fl.isLockStale()
		if isStale {
			// Propagate cleanup failure as a lock/filesystem error before retry
			if cleanupErr := fl.cleanupStaleLock(); cleanupErr != nil {
				return &LockError{
					Type:    LockErrorFilesystem,
					Message: fmt.Sprintf("failed to cleanup stale lock held by dead process (PID %d)", stalePID),
					Err:     cleanupErr,
				}
			}
			lock, err = fl.acquireLockWithPID()
			if err != nil {
				return NewLockStale(stalePID)
			}
			locked = true
		} else {
			return NewLockTimeout(fmt.Errorf("lock held by live process after %v", fl.lockTimeout))
		}
	}

	acquisitionTime := time.Since(now)
	holdStart := time.Now()

	// We intentionally do NOT remove the lock file or PID file here.
	// Removing the lock file after unlock creates a race: another process can
	// create a new file (different inode) and acquire flock on it, then this
	// process deletes that file, allowing a third process to create yet another
	// file — resulting in two processes holding flock on different inodes
	// simultaneously. Leaving the file in place ensures all processes flock
	// the same inode. Stale lock cleanup happens only in cleanupStaleLock().
	defer func() {
		lock.Unlock()

		if fl.enableMetrics && fl.metricsRecorder != nil {
			holdTime := time.Since(holdStart)
			fl.metricsRecorder.Record(&Metrics{
				Operation:       operation,
				AcquisitionTime: acquisitionTime,
				HoldTime:        holdTime,
			})
		}
	}()

	return fn()
}
