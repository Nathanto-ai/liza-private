//go:build windows

package filelock

import (
	"golang.org/x/sys/windows"
)

// isProcessAlive checks if a process with the given PID is running.
// On Windows, Signal(0) is not supported, so we use OpenProcess with
// PROCESS_QUERY_LIMITED_INFORMATION and check the exit code.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)

	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false
	}

	// STILL_ACTIVE (259) means the process is still running
	return exitCode == 259
}
