package models

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCopilotConfigYAMLRoundTrip(t *testing.T) {
	cfg := Config{
		IntegrationBranch:           "main",
		CopilotDefaultModel:         "gpt-5-mini",
		CopilotFallbackModel:        "gpt-4.1",
		CopilotStrictModelSelection: true,
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}

	var roundTripped Config
	if err := yaml.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("failed to unmarshal config: %v", err)
	}

	if roundTripped.CopilotDefaultModel != "gpt-5-mini" {
		t.Errorf("CopilotDefaultModel = %q, want %q", roundTripped.CopilotDefaultModel, "gpt-5-mini")
	}
	if roundTripped.CopilotFallbackModel != "gpt-4.1" {
		t.Errorf("CopilotFallbackModel = %q, want %q", roundTripped.CopilotFallbackModel, "gpt-4.1")
	}
	if !roundTripped.CopilotStrictModelSelection {
		t.Error("CopilotStrictModelSelection should be true")
	}
}

func TestCopilotConfigDefaults(t *testing.T) {
	if DefaultCopilotModel != "gpt-5-mini" {
		t.Errorf("DefaultCopilotModel = %q, want %q", DefaultCopilotModel, "gpt-5-mini")
	}
	if DefaultCopilotFallbackModel != "gpt-4.1" {
		t.Errorf("DefaultCopilotFallbackModel = %q, want %q", DefaultCopilotFallbackModel, "gpt-4.1")
	}
}

func TestCopilotConfigOmitsEmptyFields(t *testing.T) {
	// Empty copilot fields should be omitted from YAML (omitempty)
	cfg := Config{
		IntegrationBranch: "main",
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}

	yamlStr := string(data)
	if contains(yamlStr, "copilot_default_model") {
		t.Error("empty copilot_default_model should be omitted from YAML")
	}
	if contains(yamlStr, "copilot_fallback_model") {
		t.Error("empty copilot_fallback_model should be omitted from YAML")
	}
	if contains(yamlStr, "copilot_strict_model_selection") {
		t.Error("false copilot_strict_model_selection should be omitted from YAML")
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
