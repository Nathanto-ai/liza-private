package paths

import (
	"path/filepath"
	"strings"
)

// NormalizeSpecRef strips worktree path prefixes from spec_ref values.
// Agents working in git worktrees see files at `.worktrees/<task-id>/specs/...`,
// but spec_ref should always be relative to the project root (e.g. `specs/...`).
//
// Pattern matched: anything up to and including `.worktrees/<segment>/` is removed.
// If no worktree prefix is found, the input is returned as-is.
//
// Safety: if the remainder after stripping is empty, absolute, starts with "..",
// or is a fragment-only ref (e.g. "#anchor"), the input is returned unchanged so
// downstream validators can reject it with the original context intact.
func NormalizeSpecRef(specRef string) string {
	if specRef == "" {
		return specRef
	}

	const marker = ".worktrees/"
	_, after, found := strings.Cut(specRef, marker)
	if !found {
		return specRef
	}

	// Find the end of the task-id segment after ".worktrees/"
	_, remainder, hasSlash := strings.Cut(after, "/")
	if !hasSlash {
		// ".worktrees/task-id" with no trailing path — nothing to normalize to
		return specRef
	}

	// Validate remainder is a safe repo-relative path. If not, return original
	// unchanged so downstream validators reject it with full context.
	if remainder == "" {
		return specRef
	}
	if isAbsAnyPlatform(remainder) {
		return specRef
	}
	if strings.HasPrefix(remainder, "#") {
		return specRef
	}
	if cleaned := filepath.Clean(remainder); strings.HasPrefix(cleaned, "..") {
		return specRef
	}

	return remainder
}

// isAbsAnyPlatform returns true if the path is absolute on any platform.
// filepath.IsAbs is platform-dependent, so we also check for Windows
// drive-letter prefixes (e.g. "C:\", "D:/") to catch cross-platform inputs.
func isAbsAnyPlatform(path string) bool {
	if filepath.IsAbs(path) {
		return true
	}
	// Unix absolute path: filepath.IsAbs("/path") is false on Windows.
	if len(path) > 0 && path[0] == '/' {
		return true
	}
	// Windows drive letter: letter + colon (e.g. "C:\", "C:/", "C:file")
	if len(path) >= 2 && path[1] == ':' {
		c := path[0]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			return true
		}
	}
	return false
}
