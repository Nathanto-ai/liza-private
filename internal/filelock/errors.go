package filelock

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
)

// LockErrorType classifies different types of lock errors.
type LockErrorType int

const (
	// LockErrorTimeout indicates lock acquisition timed out.
	LockErrorTimeout LockErrorType = iota
	// LockErrorPermission indicates a permission denied error.
	LockErrorPermission
	// LockErrorDiskFull indicates the disk is full (ENOSPC).
	LockErrorDiskFull
	// LockErrorFilesystem indicates a filesystem error (I/O, read-only, etc.).
	LockErrorFilesystem
	// LockErrorStale indicates the lock is held by a dead process.
	LockErrorStale
	// LockErrorSharingViolation indicates a Windows file sharing violation (transient, retryable).
	LockErrorSharingViolation
)

// String returns a string representation of the error type.
func (t LockErrorType) String() string {
	switch t {
	case LockErrorTimeout:
		return "timeout"
	case LockErrorPermission:
		return "permission"
	case LockErrorDiskFull:
		return "disk_full"
	case LockErrorFilesystem:
		return "filesystem"
	case LockErrorStale:
		return "stale"
	case LockErrorSharingViolation:
		return "sharing_violation"
	default:
		return "unknown"
	}
}

// LockError is a custom error type for lock-related errors with classification.
type LockError struct {
	Type    LockErrorType
	Message string
	Err     error
}

// Error implements the error interface.
func (e *LockError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("lock error (%s): %s: %v", e.Type.String(), e.Message, e.Err)
	}
	return fmt.Sprintf("lock error (%s): %s", e.Type.String(), e.Message)
}

// Unwrap returns the underlying error for error chain support.
func (e *LockError) Unwrap() error {
	return e.Err
}

// IsLockErrorType checks if an error is a LockError of a specific type.
func IsLockErrorType(err error, errType LockErrorType) bool {
	var lockErr *LockError
	if errors.As(err, &lockErr) {
		return lockErr.Type == errType
	}
	return false
}

// ClassifyLockError examines an error and returns a classified LockError.
func ClassifyLockError(err error) *LockError {
	if err == nil {
		return &LockError{
			Type:    LockErrorFilesystem,
			Message: "unknown error",
			Err:     nil,
		}
	}

	// Unwrap PathError and LinkError to get the underlying errno
	var errno syscall.Errno
	var pathErr *os.PathError
	var linkErr *os.LinkError

	if errors.As(err, &pathErr) {
		if e, ok := pathErr.Err.(syscall.Errno); ok {
			errno = e
		}
	} else if errors.As(err, &linkErr) {
		if e, ok := linkErr.Err.(syscall.Errno); ok {
			errno = e
		}
	} else if e, ok := err.(syscall.Errno); ok {
		errno = e
	}

	// Windows ERROR_SHARING_VIOLATION (errno 32) is transient and retryable.
	// On Unix errno 32 is EPIPE (unrelated), so we gate on GOOS.
	if runtime.GOOS == "windows" && errno == 32 {
		return &LockError{
			Type:    LockErrorSharingViolation,
			Message: "file sharing violation (transient)",
			Err:     err,
		}
	}

	// Classify based on errno
	switch errno {
	case syscall.EACCES:
		return &LockError{
			Type:    LockErrorPermission,
			Message: "permission denied",
			Err:     err,
		}
	case syscall.EPERM:
		return &LockError{
			Type:    LockErrorPermission,
			Message: "operation not permitted",
			Err:     err,
		}
	case syscall.ENOSPC:
		return &LockError{
			Type:    LockErrorDiskFull,
			Message: "no space left on device",
			Err:     err,
		}
	case syscall.EIO:
		return &LockError{
			Type:    LockErrorFilesystem,
			Message: "I/O error",
			Err:     err,
		}
	case syscall.EROFS:
		return &LockError{
			Type:    LockErrorFilesystem,
			Message: "read-only file system",
			Err:     err,
		}
	}

	// Default to generic filesystem error
	return &LockError{
		Type:    LockErrorFilesystem,
		Message: "filesystem error",
		Err:     err,
	}
}

// NewLockTimeout creates a timeout LockError.
func NewLockTimeout(err error) *LockError {
	return &LockError{
		Type:    LockErrorTimeout,
		Message: "lock acquisition timed out",
		Err:     err,
	}
}

// NewLockStale creates a stale lock LockError.
func NewLockStale(pid int) *LockError {
	return &LockError{
		Type:    LockErrorStale,
		Message: fmt.Sprintf("lock held by dead process (PID %d)", pid),
		Err:     nil,
	}
}
