package coding

// Ports .upstream/current/packages/ai/test/opencode-provider-headers.test.ts.
// Pi adds x-opencode-session in ai/providers/opencode-headers.ts; PiG adds it
// in the provider attribution wrapper every OpenCode stream passes through.

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func streamOpenCodeAttribution(t *testing.T, providerID string, options ai.StreamOptions) ai.ProviderHeaders {
	t.Helper()
	capture := &attributionCaptureProvider{}
	provider := newProviderAttributionProvider(capture, providerID, "https://opencode.ai/zen/v1", func() bool { return false }, nil)
	if _, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{}), options); err != nil {
		t.Fatal(err)
	}
	return capture.options.Headers
}

func TestOpenCodeSessionHeaderMapsSessionIDWithoutCacheRetention(t *testing.T) {
	// Regression for earendil-works/pi#9326: the header must not depend on
	// prompt-cache retention.
	t.Setenv("PI_CACHE_RETENTION", "none")
	for _, providerID := range []string{"opencode", "opencode-go"} {
		headers := streamOpenCodeAttribution(t, providerID, ai.StreamOptions{SessionID: "conversation-1"})
		if value := headers["x-opencode-session"]; value == nil || *value != "conversation-1" {
			t.Fatalf("%s headers = %#v, want x-opencode-session conversation-1", providerID, headers)
		}
	}
}

func TestOpenCodeSessionHeaderPreservesCaseInsensitiveCallerOverride(t *testing.T) {
	for _, override := range []*string{new("caller-value"), nil} {
		headers := streamOpenCodeAttribution(t, "opencode", ai.StreamOptions{
			SessionID: "generated-value",
			Headers:   ai.ProviderHeaders{"X-OpenCode-Session": override},
		})
		if _, generated := headers["x-opencode-session"]; generated {
			t.Fatalf("generated session header survived caller override: %#v", headers)
		}
		value, present := headers["X-OpenCode-Session"]
		if !present || (override == nil) != (value == nil) || (override != nil && *value != *override) {
			t.Fatalf("caller override = %#v, want %v", headers, override)
		}
	}
}

func TestOpenCodeSessionHeaderIsNotFabricatedWithoutSessionID(t *testing.T) {
	headers := streamOpenCodeAttribution(t, "opencode", ai.StreamOptions{Headers: ai.ProviderHeadersFromStrings(map[string]string{"x-custom": "value"})})
	if len(headers) != 1 || headers["x-custom"] == nil || *headers["x-custom"] != "value" {
		t.Fatalf("headers = %#v, want only x-custom", headers)
	}
}
