package codingagent

import (
	"path/filepath"
	"strings"
	"testing"
)

// pi: packages/coding-agent/src/core/auth-guidance.ts

// getProviderLoginHelp (auth-guidance.ts:6-12): "Use /login ... See:" followed by the providers.md and models.md docs paths, one per line,
// indented by two spaces. The messages wrap it exactly as auth-guidance.ts:14-26 does.
func TestAuthGuidanceMessagesMatchPi(t *testing.T) {
	help := ProviderLoginHelp()
	providers, _ := PigDocsFile("providers.md")
	models, _ := PigDocsFile("models.md")
	wantHelp := "Use /login to log into a provider via OAuth or API key. See:\n  " + providers + "\n  " + models
	if help != wantHelp {
		t.Fatalf("ProviderLoginHelp() = %q, want %q", help, wantHelp)
	}
	if filepath.Base(providers) != "providers.md" || filepath.Base(models) != "models.md" {
		t.Fatalf("docs paths %q %q", providers, models)
	}
	cases := []struct{ name, got, want string }{
		{"no models", FormatNoModelsAvailableMessage(), "No models available. " + wantHelp},
		{"no model selected", FormatNoModelSelectedMessage(), "No model selected.\n\n" + wantHelp + "\n\nThen use /model to select a model."},
		{"api key named provider", FormatNoAPIKeyFoundMessage("anthropic"), "No API key found for anthropic.\n\n" + wantHelp},
		{"api key unknown provider", FormatNoAPIKeyFoundMessage("unknown"), "No API key found for the selected model.\n\n" + wantHelp},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, c.got, c.want)
		}
		if strings.Contains(c.got, "\r") {
			t.Errorf("%s contains a carriage return", c.name)
		}
	}
}
