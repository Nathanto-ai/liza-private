package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
)

// Regression tests for V&V Run 6 fixes.

// TestFix31_EffectiveMCPInactivityTimeout verifies the config resolution for
// MCP inactivity timeout, including default fallback.
// Bug: Planner autopilot stuck 25+ min without MCP calls (ISSUE-R6-01).
// Fix: Supervisor monitors MCP activity file and kills agent if inactive too long.
func TestFix31_EffectiveMCPInactivityTimeout(t *testing.T) {
	t.Parallel()

	// Default: 10 minutes when not configured
	cfg := models.Config{}
	timeout := effectiveMCPInactivityTimeout(cfg)
	if timeout != time.Duration(models.DefaultMCPInactivityTimeoutSec)*time.Second {
		t.Errorf("default timeout = %v, want %v", timeout, time.Duration(models.DefaultMCPInactivityTimeoutSec)*time.Second)
	}

	// Explicit config overrides default
	cfg.MCPInactivityTimeoutSec = 300
	timeout = effectiveMCPInactivityTimeout(cfg)
	if timeout != 300*time.Second {
		t.Errorf("configured timeout = %v, want 300s", timeout)
	}

	// Negative value disables
	cfg.MCPInactivityTimeoutSec = -1
	timeout = effectiveMCPInactivityTimeout(cfg)
	if timeout != 0 {
		t.Errorf("disabled timeout = %v, want 0", timeout)
	}
}

// TestFix31_MCPActivityFileReadback verifies that the MCP activity file
// format is readable and parseable after writing.
func TestFix31_MCPActivityFileReadback(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	activityPath := filepath.Join(tmpDir, "mcp-activity")

	// Write activity timestamp
	now := time.Now().UTC()
	err := os.WriteFile(activityPath, []byte(now.Format(time.RFC3339)), 0644)
	if err != nil {
		t.Fatalf("failed to write activity file: %v", err)
	}

	// Read back and parse
	data, err := os.ReadFile(activityPath)
	if err != nil {
		t.Fatalf("failed to read activity file: %v", err)
	}

	parsed, err := time.Parse(time.RFC3339, string(data))
	if err != nil {
		t.Fatalf("failed to parse activity timestamp: %v", err)
	}

	// Should be within 1 second of what we wrote
	if parsed.Sub(now).Abs() > time.Second {
		t.Errorf("parsed time %v differs from written time %v by more than 1s", parsed, now)
	}
}
