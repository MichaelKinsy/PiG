package ai

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

// Pi's openai-codex login sends its own client name as the authorize URL's
// originator (packages/ai/src/auth/oauth/openai-codex.ts createAuthorizationFlow
// originator = "pi"). PiG sends its own (D26): OpenAI must not attribute PiG's
// login to Pi.
func TestLoginOpenAICodexAuthorizeURLNamesPiGAsOriginator(t *testing.T) {
	holdCodexCallbackPort(t)
	stubCodexTokenEndpoint(t)
	var authURL string
	if _, err := LoginOpenAICodex(context.Background(), OAuthLoginCallbacks{
		OnAuth:            func(info OAuthAuthInfo) { authURL = info.URL },
		OnManualCodeInput: func() (string, error) { return "manual-code", nil },
	}); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Query().Get("originator"); got != pigidentity.CodexOriginator || got == "" || strings.EqualFold(got, "pi") {
		t.Fatalf("authorize originator = %q, want PiG's %q", got, pigidentity.CodexOriginator)
	}
}

// The Codex websocket handshake sets originator itself (openai-codex-responses.ts
// buildBaseCodexHeaders), separately from the SSE path: both carry PiG's value.
func TestCodexWebSocketHeadersNamePiGAsOriginator(t *testing.T) {
	headers := codexWebSocketHeaders(nil, nil, "token", "acct", "request")
	if got := headers.Get("originator"); got != pigidentity.CodexOriginator || got == "" || strings.EqualFold(got, "pi") {
		t.Fatalf("websocket originator = %q, want PiG's %q", got, pigidentity.CodexOriginator)
	}
}
