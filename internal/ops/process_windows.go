//go:build windows

package ops

import (
	"os"

	"golang.org/x/sys/windows"
)

// IsProcessAlive checks if a process with the given PID is running.
// On Windows, we use OpenProcess with PROCESS_QUERY_LIMITED_INFORMATION
// since Signal(0) is not supported.
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	// First check os.FindProcess (always succeeds on Windows but needed for API compat)
	_, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)

	// Check if the process has exited
	var exitCode uint32
	err = windows.GetExitCodeProcess(handle, &exitCode)
	if err != nil {
		return false
	}

	// STILL_ACTIVE (259) means the process is still running
	return exitCode == 259
}
