package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// buildModel constructs a Model from a "provider/model[:thinking]" spec string.
func buildModel(spec string, registry *codingagent.ModelRegistry) (*ai.Model, string, string, error) {
	return buildModelContext(context.Background(), spec, registry)
}

func buildModelContext(ctx context.Context, spec string, registry *codingagent.ModelRegistry) (*ai.Model, string, string, error) {
	// Parse "provider/model[:thinking]": split on the last colon to extract an
	// optional thinking level suffix.
	var thinkingOverride string
	if idx := strings.LastIndex(spec, ":"); idx >= 0 && validThinkingLevels[spec[idx+1:]] {
		thinkingOverride = spec[idx+1:]
		spec = spec[:idx]
	}
	providerID, modelID, ok := strings.Cut(spec, "/")
	if !ok {
		return nil, "", "", fmt.Errorf("model %q has no provider prefix", spec)
	}
	model, _, warning, err := buildModelFromRef(ctx, providerID, modelID, registry)
	return model, thinkingOverride, warning, err
}

// resolveModel runs startup model selection for a CLI --model and --provider
// and returns the model, its thinking level and the first warning.
func resolveModel(modelFlag, providerFlag string, settings codingagent.Settings, registry *codingagent.ModelRegistry) (*ai.Model, string, string, error) {
	selected, err := selectStartupModel(context.Background(), startupModelOptions{CLIProvider: providerFlag, CLIModel: modelFlag}, settings, registry)
	warning := ""
	if len(selected.Warnings) > 0 {
		warning = selected.Warnings[0]
	}
	return selected.Model, selected.Thinking, warning, err
}
