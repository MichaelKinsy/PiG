package codingagent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi: packages/coding-agent/src/core/settings-manager.ts:1528 (SettingsManager.setWarnings).
func TestAnthropicSubscriptionWarningUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, provider, apiKey                           string
		oauth, disabled                                  bool
		invocations, wantCheck, wantResolve, wantWarning int
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-anthropic-warning.test.ts:18
		{"warns once when Anthropic subscription auth is detected", "anthropic", "sk-ant-oat01-test", false, false, 2, 1, 1, 1},
		// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-anthropic-warning.test.ts:38
		{"warns when Anthropic OAuth is stored even if token refresh lookup would fail", "anthropic", "", true, false, 1, 1, 0, 1},
		// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-anthropic-warning.test.ts:55
		{"does not warn for non-Anthropic models", "openai", "", false, false, 1, 0, 0, 0},
		// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-anthropic-warning.test.ts:72
		{"does not warn when Anthropic extra usage warning is disabled", "anthropic", "", false, true, 1, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newAnthropicWarningMode(t, footerTestModel(tc.provider, "model", 0, 0))
			checks, resolves := 0, 0
			m.opts.RequestAuthRuntime.providerByID[tc.provider].Auth = ai.ProviderAuth{APIKey: &ai.APIKeyAuth{
				Check: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
					checks++
					if tc.oauth {
						return &ai.AuthCheck{Type: ai.CredentialOAuth}, nil
					}
					return nil, nil
				},
				Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
					resolves++
					if tc.oauth {
						return nil, errors.New("token refresh lookup failed")
					}
					if tc.apiKey == "" {
						return nil, nil
					}
					return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: tc.apiKey}}, nil
				},
			}}
			if tc.disabled {
				if err := m.opts.SettingsManager.SetWarnings(WarningSettings{AnthropicExtraUsage: false}); err != nil {
					t.Fatal(err)
				}
			}
			for range tc.invocations {
				m.maybeWarnAboutAnthropicSubscriptionAuth(t.Context())
			}
			if checks != tc.wantCheck || resolves != tc.wantResolve {
				t.Fatalf("auth checks/resolves = %d/%d, want %d/%d", checks, resolves, tc.wantCheck, tc.wantResolve)
			}
			output := strings.Join(m.chatContainer.Render(500), "\n")
			if got := strings.Count(output, anthropicSubscriptionAuthWarning); got != tc.wantWarning {
				t.Fatalf("warnings = %d, want %d: %q", got, tc.wantWarning, output)
			}
		})
	}
}
