package mcp

import (
	"testing"
)

// TestServerInitialization verifies server starts without errors
func TestServerInitialization(t *testing.T) {
	server := NewServer("/tmp/test-project", "/tmp/test-project/.liza/log.yaml", "")

	if server == nil {
		t.Fatal("Expected server to be created")
	}

	if server.projectRoot != "/tmp/test-project" {
		t.Errorf("Expected projectRoot /tmp/test-project, got %s", server.projectRoot)
	}

	if server.logPath != "/tmp/test-project/.liza/log.yaml" {
		t.Errorf("Expected logPath /tmp/test-project/.liza/log.yaml, got %s", server.logPath)
	}
}

// TestCapabilitiesResponse verifies server reports correct capabilities
func TestCapabilitiesResponse(t *testing.T) {
	server := NewServer("/tmp/test-project", "/tmp/test-project/.liza/log.yaml", "")

	caps := server.GetCapabilities()

	if caps == nil {
		t.Fatal("Expected capabilities to be returned")
	}

	// Should have tools capability
	if _, ok := caps["tools"]; !ok {
		t.Error("Expected tools capability")
	}

	// Should have resources capability
	if _, ok := caps["resources"]; !ok {
		t.Error("Expected resources capability")
	}
}

// TestToolRegistration verifies tools registered correctly
func TestToolRegistration(t *testing.T) {
	server := NewServer("/tmp/test-project", "/tmp/test-project/.liza/log.yaml", "")

	// Phase 1: Should have read-only tools
	expectedTools := []string{
		"liza_get",
		"liza_status",
		"liza_validate",
		"liza_version",
	}

	tools := server.ListTools()

	if len(tools) == 0 {
		t.Fatal("Expected tools to be registered")
	}

	for _, expected := range expectedTools {
		found := false
		for _, tool := range tools {
			if tool.Name == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected tool %s to be registered", expected)
		}
	}
}

// TestResourceRegistration verifies resources registered correctly
func TestResourceRegistration(t *testing.T) {
	server := NewServer("/tmp/test-project", "/tmp/test-project/.liza/log.yaml", "")

	// Phase 1: Should have read-only resources
	expectedResources := []string{
		"liza://state",
		"liza://tasks",
		"liza://agents",
	}

	resources := server.ListResources()

	if len(resources) == 0 {
		t.Fatal("Expected resources to be registered")
	}

	for _, expected := range expectedResources {
		found := false
		for _, resource := range resources {
			if resource.URI == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected resource %s to be registered", expected)
		}
	}
}

// TestRoleFilteringEmptyRoleExposesAllTools verifies that an empty role
// registers the full tool set (backwards compatibility).
func TestRoleFilteringEmptyRoleExposesAllTools(t *testing.T) {
	server := NewServer("/tmp/test-project", "/tmp/test-project/.liza/log.yaml", "")
	tools := server.ListTools()

	// Should include read-only AND all mutation/complex tools
	expected := []string{
		"liza_get", "liza_status", "liza_validate", "liza_version",
		"liza_add_task", "liza_claim_task", "liza_submit_for_review",
		"liza_submit_verdict", "liza_exec", "liza_wt_create", "liza_wt_merge",
		"liza_submit_audit_finding", "liza_analyze",
	}
	toolNames := make(map[string]bool, len(tools))
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}
	for _, name := range expected {
		if !toolNames[name] {
			t.Errorf("Empty role should expose %s but it was missing", name)
		}
	}
}

// TestRoleFilteringPlannerTools verifies planner only sees planning tools.
func TestRoleFilteringPlannerTools(t *testing.T) {
	server := NewServer("/tmp/test-project", "/tmp/test-project/.liza/log.yaml", "planner")
	tools := server.ListTools()

	toolNames := make(map[string]bool, len(tools))
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}

	// Planner MUST have these
	mustHave := []string{
		"liza_get", "liza_status", "liza_validate", "liza_version", // read-only
		"liza_add_task", "liza_supersede_task", "liza_update_sprint_metrics",
		"liza_sprint_checkpoint", "liza_delete_agent", "liza_analyze",
	}
	for _, name := range mustHave {
		if !toolNames[name] {
			t.Errorf("Planner should have %s", name)
		}
	}

	// Planner MUST NOT have these
	mustNotHave := []string{
		"liza_exec", "liza_claim_task", "liza_submit_for_review",
		"liza_wt_create", "liza_wt_delete", "liza_wt_merge",
		"liza_write_checkpoint", "liza_handoff",
		"liza_submit_verdict", "liza_submit_audit_finding",
	}
	for _, name := range mustNotHave {
		if toolNames[name] {
			t.Errorf("Planner should NOT have %s", name)
		}
	}
}

// TestRoleFilteringCoderTools verifies coder sees implementation tools.
func TestRoleFilteringCoderTools(t *testing.T) {
	server := NewServer("/tmp/test-project", "/tmp/test-project/.liza/log.yaml", "coder")
	tools := server.ListTools()

	toolNames := make(map[string]bool, len(tools))
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}

	mustHave := []string{
		"liza_get", "liza_status", "liza_validate", "liza_version",
		"liza_claim_task", "liza_submit_for_review", "liza_handoff",
		"liza_mark_blocked", "liza_release_claim",
		"liza_wt_create", "liza_wt_delete", "liza_write_checkpoint", "liza_exec",
	}
	for _, name := range mustHave {
		if !toolNames[name] {
			t.Errorf("Coder should have %s", name)
		}
	}

	mustNotHave := []string{
		"liza_add_task", "liza_supersede_task", "liza_submit_verdict",
		"liza_wt_merge", "liza_submit_audit_finding",
	}
	for _, name := range mustNotHave {
		if toolNames[name] {
			t.Errorf("Coder should NOT have %s", name)
		}
	}
}

// TestRoleFilteringReviewerTools verifies reviewer sees review tools.
func TestRoleFilteringReviewerTools(t *testing.T) {
	server := NewServer("/tmp/test-project", "/tmp/test-project/.liza/log.yaml", "code-reviewer")
	tools := server.ListTools()

	toolNames := make(map[string]bool, len(tools))
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}

	mustHave := []string{
		"liza_get", "liza_status", "liza_validate", "liza_version",
		"liza_submit_verdict", "liza_wt_merge", "liza_clear_stale_review_claims",
		"liza_release_claim", "liza_mark_blocked", "liza_wt_create", "liza_wt_delete",
		"liza_exec",
	}
	for _, name := range mustHave {
		if !toolNames[name] {
			t.Errorf("Reviewer should have %s", name)
		}
	}

	mustNotHave := []string{
		"liza_add_task", "liza_claim_task",
		"liza_write_checkpoint", "liza_submit_audit_finding",
	}
	for _, name := range mustNotHave {
		if toolNames[name] {
			t.Errorf("Reviewer should NOT have %s", name)
		}
	}
}

// TestRoleFilteringAuditorTools verifies auditor sees only audit tools.
func TestRoleFilteringAuditorTools(t *testing.T) {
	server := NewServer("/tmp/test-project", "/tmp/test-project/.liza/log.yaml", "auditor")
	tools := server.ListTools()

	toolNames := make(map[string]bool, len(tools))
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}

	mustHave := []string{
		"liza_get", "liza_status", "liza_validate", "liza_version",
		"liza_submit_audit_finding", "liza_analyze", "liza_mark_blocked",
		"liza_exec",
	}
	for _, name := range mustHave {
		if !toolNames[name] {
			t.Errorf("Auditor should have %s", name)
		}
	}

	mustNotHave := []string{
		"liza_claim_task", "liza_add_task",
		"liza_wt_create", "liza_wt_merge", "liza_submit_verdict",
		"liza_write_checkpoint",
	}
	for _, name := range mustNotHave {
		if toolNames[name] {
			t.Errorf("Auditor should NOT have %s", name)
		}
	}
}
