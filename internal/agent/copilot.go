package agent

import (
	"fmt"
	"slices"
	"strings"

	"github.com/liza-mas/liza/internal/models"
)

// CopilotSupportedModels lists all models currently accepted by the Copilot CLI --model flag.
var CopilotSupportedModels = []string{
	"claude-sonnet-4.6",
	"claude-sonnet-4.5",
	"claude-haiku-4.5",
	"claude-opus-4.6",
	"claude-opus-4.6-fast",
	"claude-opus-4.5",
	"claude-sonnet-4",
	"gemini-3-pro-preview",
	"gpt-5.4",
	"gpt-5.3-codex",
	"gpt-5.2-codex",
	"gpt-5.2",
	"gpt-5.1-codex-max",
	"gpt-5.1-codex",
	"gpt-5.1",
	"gpt-5.1-codex-mini",
	"gpt-5-mini",
	"gpt-4.1",
}

// CopilotModelConfig holds the resolved model configuration for a Copilot CLI invocation.
type CopilotModelConfig struct {
	Model          string // the resolved model to use
	WasFallback    bool   // true if the fallback was used instead of the default
	ExplicitChoice bool   // true if the user explicitly chose this model
}

// IsCopilotModelSupported checks whether a model name is in the Copilot supported list.
func IsCopilotModelSupported(model string) bool {
	return slices.Contains(CopilotSupportedModels, model)
}

// ResolveCopilotModel determines which model to use for a Copilot CLI invocation.
//
// Resolution order:
//  1. If explicitModel is non-empty, use it. If it is unsupported and strict mode
//     is active, return an error. If strict mode is off, allow it through (the
//     Copilot CLI will fail with its own error if truly invalid).
//  2. If no explicit model, use the configured default (from Config or the global default).
//  3. If the default is unsupported and a fallback is configured and supported, use it.
//  4. If the default is unsupported and no valid fallback exists, return the default
//     anyway — the Copilot CLI will produce a clear error.
func ResolveCopilotModel(cfg models.Config, explicitModel string) (CopilotModelConfig, error) {
	// Case 1: explicit model override — always validate.
	if explicitModel != "" {
		if !IsCopilotModelSupported(explicitModel) {
			return CopilotModelConfig{}, fmt.Errorf(
				"copilot model %q is not supported; supported models: %s",
				explicitModel, strings.Join(CopilotSupportedModels, ", "))
		}
		return CopilotModelConfig{
			Model:          explicitModel,
			ExplicitChoice: true,
		}, nil
	}

	// Case 2: use configured default or global default — validate strictly.
	defaultModel := cfg.CopilotDefaultModel
	if defaultModel == "" {
		defaultModel = models.DefaultCopilotModel
	}

	if !IsCopilotModelSupported(defaultModel) {
		return CopilotModelConfig{}, fmt.Errorf(
			"configured Copilot default model %q is not supported; supported models: %s",
			defaultModel, strings.Join(CopilotSupportedModels, ", "))
	}
	return CopilotModelConfig{Model: defaultModel}, nil
}
