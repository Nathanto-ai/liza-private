package agent

import (
	"testing"

	"github.com/liza-mas/liza/internal/models"
)

func TestIsCopilotModelSupported(t *testing.T) {
	tests := []struct {
		model    string
		expected bool
	}{
		{"gpt-5-mini", true},
		{"gpt-4.1", true},
		{"claude-opus-4.6", true},
		{"claude-sonnet-4.6", true},
		{"gemini-3-pro-preview", true},
		{"gpt-5.4", true},
		{"gpt-5.1-codex", true},
		{"raptor-mini", false},
		{"gpt-3.5-turbo", false},
		{"", false},
		{"unknown-model", false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := IsCopilotModelSupported(tt.model); got != tt.expected {
				t.Errorf("IsCopilotModelSupported(%q) = %v, want %v", tt.model, got, tt.expected)
			}
		})
	}
}

func TestResolveCopilotModel_DefaultModel(t *testing.T) {
	cfg := models.Config{} // empty config → uses global default
	result, err := ResolveCopilotModel(cfg, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Model != models.DefaultCopilotModel {
		t.Errorf("expected default model %q, got %q", models.DefaultCopilotModel, result.Model)
	}
	if result.WasFallback {
		t.Error("expected WasFallback=false for default model")
	}
	if result.ExplicitChoice {
		t.Error("expected ExplicitChoice=false for default model")
	}
}

func TestResolveCopilotModel_ConfiguredDefault(t *testing.T) {
	cfg := models.Config{
		CopilotDefaultModel: "claude-opus-4.6",
	}
	result, err := ResolveCopilotModel(cfg, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Model != "claude-opus-4.6" {
		t.Errorf("expected configured default %q, got %q", "claude-opus-4.6", result.Model)
	}
	if result.WasFallback {
		t.Error("expected WasFallback=false")
	}
}

func TestResolveCopilotModel_EasyConfigChange(t *testing.T) {
	// Verify that changing the config default changes the resolved model
	testModels := []string{"gpt-5.4", "claude-sonnet-4.6", "gpt-4.1"}
	for _, m := range testModels {
		cfg := models.Config{CopilotDefaultModel: m}
		result, err := ResolveCopilotModel(cfg, "")
		if err != nil {
			t.Fatalf("unexpected error for model %q: %v", m, err)
		}
		if result.Model != m {
			t.Errorf("config default %q not resolved; got %q", m, result.Model)
		}
	}
}

func TestResolveCopilotModel_FallbackWhenDefaultUnsupported(t *testing.T) {
	cfg := models.Config{
		CopilotDefaultModel:  "raptor-mini", // unsupported
		CopilotFallbackModel: "gpt-5-mini",  // supported
	}
	result, err := ResolveCopilotModel(cfg, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Model != "gpt-5-mini" {
		t.Errorf("expected fallback model %q, got %q", "gpt-5-mini", result.Model)
	}
	if !result.WasFallback {
		t.Error("expected WasFallback=true")
	}
}

func TestResolveCopilotModel_FallbackToGlobalDefault(t *testing.T) {
	// When configured default is unsupported and no explicit fallback, uses global fallback
	cfg := models.Config{
		CopilotDefaultModel: "raptor-mini", // unsupported, no explicit fallback
	}
	result, err := ResolveCopilotModel(cfg, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	globalFallback := models.DefaultCopilotFallbackModel
	if result.Model != globalFallback {
		t.Errorf("expected global fallback %q, got %q", globalFallback, result.Model)
	}
	if !result.WasFallback {
		t.Error("expected WasFallback=true when using fallback")
	}
}

func TestResolveCopilotModel_ExplicitOverrideSupported(t *testing.T) {
	cfg := models.Config{}
	result, err := ResolveCopilotModel(cfg, "claude-opus-4.6")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Model != "claude-opus-4.6" {
		t.Errorf("expected explicit model %q, got %q", "claude-opus-4.6", result.Model)
	}
	if !result.ExplicitChoice {
		t.Error("expected ExplicitChoice=true for explicit override")
	}
}

func TestResolveCopilotModel_ExplicitUnsupportedStrict(t *testing.T) {
	cfg := models.Config{
		CopilotStrictModelSelection: true,
	}
	_, err := ResolveCopilotModel(cfg, "nonexistent-model")
	if err == nil {
		t.Fatal("expected error for unsupported model in strict mode")
	}
}

func TestResolveCopilotModel_ExplicitUnsupportedNonStrict(t *testing.T) {
	cfg := models.Config{
		CopilotStrictModelSelection: false,
	}
	result, err := ResolveCopilotModel(cfg, "nonexistent-model")
	if err != nil {
		t.Fatalf("unexpected error in non-strict mode: %v", err)
	}
	if result.Model != "nonexistent-model" {
		t.Errorf("expected passthrough model %q, got %q", "nonexistent-model", result.Model)
	}
}

func TestResolveCopilotModel_StrictDefaultOnExplicit(t *testing.T) {
	// Default strict behavior: strict=false means explicit overrides pass through
	cfg := models.Config{} // strict defaults to false
	result, err := ResolveCopilotModel(cfg, "some-future-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Model != "some-future-model" {
		t.Errorf("expected passthrough in default (non-strict) mode, got %q", result.Model)
	}
}

func TestResolveCopilotModel_BothDefaultAndFallbackUnsupported(t *testing.T) {
	cfg := models.Config{
		CopilotDefaultModel:  "future-model-a",
		CopilotFallbackModel: "future-model-b",
	}
	result, err := ResolveCopilotModel(cfg, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Falls through to returning the configured default (Copilot CLI will error)
	if result.Model != "future-model-a" {
		t.Errorf("expected configured default passthrough %q, got %q", "future-model-a", result.Model)
	}
}

func TestResolveCopilotModel_RaptorMiniPreferredDefault(t *testing.T) {
	// When Raptor mini is not in the supported list, the system should still
	// use the global default (gpt-5-mini) and not error.
	cfg := models.Config{}
	result, err := ResolveCopilotModel(cfg, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The default should be gpt-5-mini (the GPT-5 family mini model)
	if result.Model != "gpt-5-mini" {
		t.Errorf("global default should be gpt-5-mini, got %q", result.Model)
	}
}

func TestResolveCopilotModel_AllSupportedModelsAccepted(t *testing.T) {
	cfg := models.Config{}
	for _, model := range CopilotSupportedModels {
		result, err := ResolveCopilotModel(cfg, model)
		if err != nil {
			t.Errorf("unexpected error for supported model %q: %v", model, err)
		}
		if result.Model != model {
			t.Errorf("expected model %q, got %q", model, result.Model)
		}
	}
}

func TestCopilotModelConfig_Fields(t *testing.T) {
	// Verify CopilotModelConfig struct has expected fields
	cfg := CopilotModelConfig{
		Model:          "gpt-5-mini",
		WasFallback:    true,
		ExplicitChoice: false,
	}
	if cfg.Model != "gpt-5-mini" {
		t.Error("Model field not set correctly")
	}
	if !cfg.WasFallback {
		t.Error("WasFallback not set correctly")
	}
	if cfg.ExplicitChoice {
		t.Error("ExplicitChoice should be false")
	}
}
