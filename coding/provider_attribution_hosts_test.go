package coding

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// packages/coding-agent/src/core/provider-attribution.ts:12-18, 42-86: hosts match exactly (new URL(baseUrl).hostname === host, so a
// subdomain or a longer name is not the host), the OpenCode session pair also follows provider "opencode-go", and a later header source
// replaces an earlier one.
func TestProviderAttributionHostMatchingIsExact(t *testing.T) {
	for _, tc := range []struct {
		name      string
		provider  string
		baseURL   string
		telemetry bool
		session   string
		wantEmpty bool
	}{
		{"nvidia subdomain", "custom", "https://evil.integrate.api.nvidia.com/v1", true, "", true},
		{"cloudflare API subdomain", "custom", "https://evil.api.cloudflare.com/v1", true, "", true},
		{"cloudflare gateway subdomain", "custom", "https://evil.gateway.ai.cloudflare.com/v1", true, "", true},
		{"opencode subdomain", "custom", "https://zen.opencode.ai/v1", false, "s1", true},
		{"opencode lookalike", "custom", "https://notopencode.ai/v1", false, "s1", true},
		{"not a URL", "custom", "opencode.ai", false, "s1", true},
		// new URL throws for an out-of-range port, so the host never matches.
		{"invalid port", "custom", "https://opencode.ai:99999/v1", false, "s1", true},
		// A non-special scheme keeps the host's letter case (Node 24: new URL("foo://OpenCode.ai").hostname is "OpenCode.ai").
		{"non-special scheme keeps case", "custom", "foo://OpenCode.ai/v1", false, "s1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeProviderAttributionHeaders(tc.provider, tc.baseURL, tc.telemetry, tc.session)
			if tc.wantEmpty && got != nil {
				t.Fatalf("headers = %#v, want none", got)
			}
		})
	}
	for _, tc := range []struct {
		name, provider, baseURL string
	}{
		{"opencode-go provider", "opencode-go", "https://example.test/v1"},
		{"opencode provider", "opencode", "https://example.test/v1"},
		{"opencode host in any letter case", "custom", "HTTPS://OpenCode.AI/zen/v1"},
		{"cloudflare gateway host", "custom", "https://gateway.ai.cloudflare.com/v1"},
		// The WHATWG parser trims C0 controls and spaces, drops tabs and newlines, skips the slashes of a special scheme, treats "\\"
		// as "/", drops userinfo and port and percent-decodes the host (each hostname measured with Node 24 new URL).
		{"leading space", "custom", " https://opencode.ai/v1"},
		{"tab and newline", "custom", "\thttps://open\ncode.ai/v1"},
		{"no slashes after a special scheme", "custom", "https:opencode.ai/v1"},
		{"backslash path", "custom", "https://opencode.ai\\v1"},
		{"userinfo and port", "custom", "https://user@opencode.ai:8080/x"},
		{"percent-encoded host", "custom", "https://opencode%2Eai/"},
		{"special non-http scheme lowercases", "custom", "ws://OpenCode.ai"},
		{"non-special scheme", "custom", "foo://opencode.ai/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeProviderAttributionHeaders(tc.provider, tc.baseURL, true, "session-1")
			if tc.name == "cloudflare gateway host" {
				if got == nil || got["User-Agent"] == nil || got["x-opencode-session"] != nil {
					t.Fatalf("headers = %#v", got)
				}
				return
			}
			if got == nil || got["x-opencode-session"] == nil || *got["x-opencode-session"] != "session-1" || got["x-opencode-client"] == nil {
				t.Fatalf("headers = %#v, want the OpenCode session pair", got)
			}
		})
	}
}

// The wrapper merges configured headers before the request's, so the request wins on the same key (provider-attribution.ts:78-86).
func TestProviderAttributionWrapperRequestHeadersReplaceConfigured(t *testing.T) {
	capture := &attributionCaptureProvider{}
	provider := newProviderAttributionProvider(capture, "custom", "https://example.test/v1", func() bool { return false }, map[string]string{"X-Team": "configured"})
	if _, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{}), ai.StreamOptions{
		Headers: ai.ProviderHeadersFromStrings(map[string]string{"X-Team": "request"}),
	}); err != nil {
		t.Fatal(err)
	}
	want := ai.ProviderHeadersFromStrings(map[string]string{"X-Team": "request"})
	if !reflect.DeepEqual(capture.options.Headers, want) {
		t.Fatalf("headers = %#v, want %#v", capture.options.Headers, want)
	}
}
