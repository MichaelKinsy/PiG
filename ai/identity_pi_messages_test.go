package ai

import (
	"net/http"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

// Pi adc5fa9f: pi-messages.ts layers getIdentityHeaders below the fixed request headers and the request headers.
func TestPiMessagesSendsTheIdentityUserAgentBelowRequestHeaders(t *testing.T) {
	done := []any{map[string]any{"type": "start"}, map[string]any{"type": "done", "reason": "stop", "usage": piMessagesTestUsage}}
	run := func(t *testing.T, opts StreamOptions) http.Header {
		t.Helper()
		baseURL, requests := startPiMessagesServer(t, piMessagesResponder{events: done})
		collectPiMessages(t, newTestPiMessagesProvider(baseURL, nil), opts)
		got := requests()
		if len(got) != 1 {
			t.Fatalf("requests = %d, want 1", len(got))
		}
		return got[0].headers
	}
	t.Run("default identity", func(t *testing.T) {
		headers := run(t, StreamOptions{})
		if got, want := headers.Get("User-Agent"), pigidentity.UserAgentProduct+"/"+ProductVersion; !strings.HasPrefix(got, want) {
			t.Errorf("User-Agent = %q, want the detailed identity %q", got, want)
		}
		if headers.Get("Authorization") != "Bearer test-key" || headers.Get("Accept") != "text/event-stream" {
			t.Errorf("fixed headers = %v", headers)
		}
	})
	t.Run("custom identity", func(t *testing.T) {
		if got := run(t, StreamOptions{Identity: &ClientIdentity{UserAgent: new(UserAgentCustom("acme/1.0"))}}).Get("User-Agent"); got != "acme/1.0" {
			t.Errorf("User-Agent = %q, want acme/1.0", got)
		}
	})
	t.Run("request header outranks the identity", func(t *testing.T) {
		opts := StreamOptions{Identity: &ClientIdentity{UserAgent: new(UserAgentCustom("acme/1.0"))}, Headers: ProviderHeadersFromStrings(map[string]string{"User-Agent": "explicit/2.0"})}
		if got := run(t, opts).Get("User-Agent"); got != "explicit/2.0" {
			t.Errorf("User-Agent = %q, want explicit/2.0", got)
		}
	})
}
