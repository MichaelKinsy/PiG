package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// testServices builds a Services container rooted at dir, the shared input for
// the CLI model builder and startup selection.
func testServices(t *testing.T, dir string) *coding.Services {
	t.Helper()
	services, err := coding.NewServices(coding.ServicesOptions{AgentDir: dir, CWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	return services
}

// buildModel constructs a Model from a "provider/model[:thinking]" spec string.
func buildModel(spec string, services *coding.Services) (*ai.Model, string, string, error) {
	return buildModelContext(context.Background(), spec, services)
}

func buildModelContext(ctx context.Context, spec string, services *coding.Services) (*ai.Model, string, string, error) {
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
	model, err := buildModelFromRef(ctx, providerID, modelID, services)
	return model, thinkingOverride, "", err
}

// resolveModel runs startup model selection for a CLI --model and --provider
// and returns the model, its thinking level and the first warning.
func resolveModel(modelFlag, providerFlag string, settings codingagent.Settings, services *coding.Services) (*ai.Model, string, string, error) {
	selected, err := selectStartupModel(context.Background(), startupModelOptions{CLIProvider: providerFlag, CLIModel: modelFlag}, settings, services)
	warning := ""
	if len(selected.Warnings) > 0 {
		warning = selected.Warnings[0]
	}
	return selected.Model, selected.Thinking, warning, err
}
