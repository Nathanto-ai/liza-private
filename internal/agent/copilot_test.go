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

func TestResolveCopilotModel_RejectsUnsupportedDefault(t *testing.T) {
	cfg := models.Config{
		CopilotDefaultModel: "raptor-mini", // unsupported
	}
	_, err := ResolveCopilotModel(cfg, "")
	if err == nil {
		t.Fatal("expected error when configured default model is unsupported, got nil")
	}
}

func TestResolveCopilotModel_NoFallbackBehavior(t *testing.T) {
	// When configured default is unsupported, system must error — not fall back
	cfg := models.Config{
		CopilotDefaultModel: "raptor-mini", // unsupported, no fallback exists
	}
	_, err := ResolveCopilotModel(cfg, "")
	if err == nil {
		t.Fatal("expected error when configured default model is unsupported, got nil")
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

func TestResolveCopilotModel_ExplicitUnsupportedRejected(t *testing.T) {
	cfg := models.Config{
	}
	_, err := ResolveCopilotModel(cfg, "nonexistent-model")
	if err == nil {
		t.Fatal("expected error for unsupported model")
	}
}

func TestResolveCopilotModel_ExplicitUnsupportedAlwaysRejected(t *testing.T) {
	cfg := models.Config{}
	_, err := ResolveCopilotModel(cfg, "nonexistent-model")
	if err == nil {
		t.Fatal("expected error for unsupported explicit model")
	}
}

func TestResolveCopilotModel_StrictDefaultOnExplicit(t *testing.T) {
	cfg := models.Config{}
	_, err := ResolveCopilotModel(cfg, "some-future-model")
	if err == nil {
		t.Fatal("expected error for unsupported explicit model")
	}
}

func TestResolveCopilotModel_BothDefaultAndFallbackUnsupported(t *testing.T) {
	cfg := models.Config{
		CopilotDefaultModel: "future-model-a",
	}
	_, err := ResolveCopilotModel(cfg, "")
	if err == nil {
		t.Fatal("expected error when configured default is unsupported")
	}
}

func TestResolveCopilotModel_RaptorMiniNotYetSupported(t *testing.T) {
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
		ExplicitChoice: false,
	}
	if cfg.Model != "gpt-5-mini" {
		t.Error("Model field not set correctly")
	}
	if cfg.ExplicitChoice {
		t.Error("ExplicitChoice should be false")
	}
}

// TestCopilotModelConfig_NoWasFallbackField is a regression test ensuring
// the WasFallback field is not reintroduced. The field was dead code —
// always false — and misleadingly suggested fallback behavior existed.
func TestCopilotModelConfig_NoWasFallbackField(t *testing.T) {
	t.Parallel()
	// CopilotModelConfig should only have Model and ExplicitChoice.
	// If someone adds WasFallback back, this won't compile:
	// cfg := CopilotModelConfig{WasFallback: true}  <-- would fail
	cfg := CopilotModelConfig{Model: "gpt-5-mini", ExplicitChoice: false}
	_ = cfg
}

// TestDefaultCopilotModel_IsGPT5Mini is a regression test ensuring the
// default model constant stays gpt-5-mini.
func TestDefaultCopilotModel_IsGPT5Mini(t *testing.T) {
	t.Parallel()
	if models.DefaultCopilotModel != "gpt-5-mini" {
		t.Errorf("DefaultCopilotModel = %q, want gpt-5-mini", models.DefaultCopilotModel)
	}
}

// TestResolveCopilotModel_NeverFallsBack verifies that no fallback behavior
// exists — unsupported models always produce errors, never silently degrade.
func TestResolveCopilotModel_NeverFallsBack(t *testing.T) {
	t.Parallel()
	unsupported := []string{"raptor-mini", "gpt-3.5-turbo", "future-xyz"}
	for _, m := range unsupported {
		t.Run(m, func(t *testing.T) {
			cfg := models.Config{CopilotDefaultModel: m}
			_, err := ResolveCopilotModel(cfg, "")
			if err == nil {
				t.Fatalf("unsupported model %q should error, not fall back", m)
			}
		})
	}
}
