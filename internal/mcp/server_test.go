package mcp

import (
	"os"
	"path/filepath"
	"strings"
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

// TestNilResolverSurfacesPipelineLoadError verifies that when the pipeline
// config fails to load, operation-checked tool errors include the original
// load error text, not just "pipeline resolver not loaded".
func TestNilResolverSurfacesPipelineLoadError(t *testing.T) {
	tmpDir := t.TempDir()

	// Create .liza dir with invalid pipeline.yaml
	lizaDir := filepath.Join(tmpDir, ".liza")
	if err := os.MkdirAll(lizaDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lizaDir, "pipeline.yaml"), []byte("not: valid: yaml: {{{"), 0644); err != nil {
		t.Fatalf("write pipeline: %v", err)
	}
	// Write minimal state so blackboard init succeeds
	if err := os.WriteFile(filepath.Join(lizaDir, "state.yaml"), []byte("tasks: []\nagents: {}\n"), 0644); err != nil {
		t.Fatalf("write state: %v", err)
	}

	server := NewServer(tmpDir, filepath.Join(lizaDir, "log.yaml"))

	// Server should be created (fail-closed, not crash)
	if server == nil {
		t.Fatal("expected server to be created even with invalid pipeline")
	}
	if server.resolver != nil {
		t.Fatal("expected nil resolver with invalid pipeline config")
	}

	// Call an operation-checked tool handler (liza_submit_for_review)
	handler, ok := server.GetHandler("liza_submit_for_review")
	if !ok {
		t.Fatal("expected liza_submit_for_review handler to be registered")
	}

	_, err := handler(map[string]any{
		"task_id":    "task-1",
		"commit_sha": "abc123",
		"agent_id":   "coder-1",
	})
	if err == nil {
		t.Fatal("expected error from operation-checked tool with nil resolver")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "pipeline resolver not loaded") {
		t.Errorf("expected 'pipeline resolver not loaded' in error, got: %s", errMsg)
	}
	// The key assertion: error must include the original load error, not just the generic message
	if !strings.Contains(errMsg, "parsing pipeline config") {
		t.Errorf("expected original load error text in message, got: %s", errMsg)
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
