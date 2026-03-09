package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAllMCPToolsHaveClaudePermissions is a regression test ensuring that
// every MCP tool registered by the server has a corresponding permission
// entry in the embedded claude-settings.json. Missing permissions cause
// Claude Code to prompt or block MCP tool calls, which breaks autonomous
// agent operation.
//
// Regression: liza_submit_audit_finding was missing from claude-settings.json,
// causing auditor agents to be unable to submit findings via MCP.
func TestAllMCPToolsHaveClaudePermissions(t *testing.T) {
	// Get all tools registered by the MCP server
	server := NewServer("/tmp/test-project", "/tmp/test-project/.liza/log.yaml", "")
	tools := server.ListTools()

	if len(tools) == 0 {
		t.Fatal("Expected MCP server to register at least one tool")
	}

	// Read the embedded claude-settings.json
	_, thisFile, _, _ := runtime.Caller(0)
	settingsPath := filepath.Join(filepath.Dir(thisFile), "..", "embedded", "claude-settings.json")
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("Failed to read claude-settings.json: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(content, &settings); err != nil {
		t.Fatalf("Failed to parse claude-settings.json: %v", err)
	}

	// Extract allowed permissions
	permissions, ok := settings["permissions"].(map[string]any)
	if !ok {
		t.Fatal("Expected permissions object in claude-settings.json")
	}
	allowList, ok := permissions["allow"].([]any)
	if !ok {
		t.Fatal("Expected permissions.allow array in claude-settings.json")
	}

	allowSet := make(map[string]bool, len(allowList))
	for _, item := range allowList {
		if str, ok := item.(string); ok {
			allowSet[str] = true
		}
	}

	// Verify every MCP tool has a corresponding permission
	var missing []string
	for _, tool := range tools {
		permName := "mcp__liza__" + tool.Name
		if !allowSet[permName] {
			missing = append(missing, permName)
		}
	}

	if len(missing) > 0 {
		t.Errorf("MCP tools missing from claude-settings.json permissions.allow:\n  %s\n"+
			"Add these to internal/embedded/claude-settings.json",
			strings.Join(missing, "\n  "))
	}
}

// TestClaudeSettingsHasLizaCLIPermission verifies that Bash(liza:*) is in
// the permissions allow list so agents can fall back to CLI commands when
// MCP tools are unavailable.
func TestClaudeSettingsHasLizaCLIPermission(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	settingsPath := filepath.Join(filepath.Dir(thisFile), "..", "embedded", "claude-settings.json")
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("Failed to read claude-settings.json: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(content, &settings); err != nil {
		t.Fatalf("Failed to parse claude-settings.json: %v", err)
	}

	permissions, ok := settings["permissions"].(map[string]any)
	if !ok {
		t.Fatal("Expected permissions object")
	}
	allowList, ok := permissions["allow"].([]any)
	if !ok {
		t.Fatal("Expected permissions.allow array")
	}

	found := false
	for _, item := range allowList {
		if str, ok := item.(string); ok && str == "Bash(liza:*)" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Bash(liza:*) missing from claude-settings.json permissions.allow — " +
			"agents cannot use liza CLI commands as MCP fallback")
	}
}

// TestMCPJsonHasLizaServer verifies the embedded mcp.json template
// configures the liza MCP server correctly.
func TestMCPJsonHasLizaServer(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	mcpPath := filepath.Join(filepath.Dir(thisFile), "..", "embedded", "mcp.json")
	content, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("Failed to read mcp.json: %v", err)
	}

	var mcpConfig map[string]any
	if err := json.Unmarshal(content, &mcpConfig); err != nil {
		t.Fatalf("Failed to parse mcp.json: %v", err)
	}

	servers, ok := mcpConfig["mcpServers"].(map[string]any)
	if !ok {
		t.Fatal("Expected mcpServers object in mcp.json")
	}

	lizaServer, ok := servers["liza"].(map[string]any)
	if !ok {
		t.Fatal("Expected liza server in mcpServers")
	}

	command, ok := lizaServer["command"].(string)
	if !ok || command != "liza-mcp" {
		t.Errorf("Expected liza server command to be 'liza-mcp', got %q", command)
	}

	args, ok := lizaServer["args"].([]any)
	if !ok || len(args) < 2 {
		t.Error("Expected liza server args to contain --project-root and path")
	}
}

// TestClaudeSettingsEnablesMCPServers verifies that the embedded settings
// enable MCP server discovery so agents can find the liza MCP server.
func TestClaudeSettingsEnablesMCPServers(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	settingsPath := filepath.Join(filepath.Dir(thisFile), "..", "embedded", "claude-settings.json")
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("Failed to read claude-settings.json: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(content, &settings); err != nil {
		t.Fatalf("Failed to parse claude-settings.json: %v", err)
	}

	// Verify enableAllProjectMcpServers is true
	enabled, ok := settings["enableAllProjectMcpServers"].(bool)
	if !ok || !enabled {
		t.Error("Expected enableAllProjectMcpServers to be true in claude-settings.json")
	}

	// Verify enabledMcpjsonServers contains "liza"
	servers, ok := settings["enabledMcpjsonServers"].([]any)
	if !ok {
		t.Fatal("Expected enabledMcpjsonServers array")
	}

	found := false
	for _, s := range servers {
		if str, ok := s.(string); ok && str == "liza" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected enabledMcpjsonServers to contain 'liza'")
	}
}
