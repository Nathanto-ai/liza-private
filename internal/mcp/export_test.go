package mcp

// Test helpers that export internal functions for cross-package tests.

// RoleAllowedToolsForTest exposes roleAllowedTools for testing.
func RoleAllowedToolsForTest(role string) map[string]bool {
	return roleAllowedTools(role)
}

// IsReadOnlyCommandForTest exposes isReadOnlyCommand for testing.
func IsReadOnlyCommandForTest(command string) bool {
	return isReadOnlyCommand(command)
}
