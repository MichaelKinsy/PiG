package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Tests of the launcher's credentials (slice 11 §4; fix round 1, Fable M2): a pairing answer the server sends is shown on the
// real terminal only when it is a plain code; anything else (an escape sequence) fails the pairing. Ports
// packages/chord/test/node/tui/credentials.test.ts.

// pairingServer answers every pairing request with answer.
func pairingServer(t *testing.T, answer any) *url.URL {
	t.Helper()
	text, err := jsstring.MarshalJSON(answer)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(text)
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return base
}

func TestPairingRefusesACodeThatIsNoPlainCodeShowingNothing(t *testing.T) {
	for _, code := range []any{"\x1b]52;c;Y3VybA==\x07", "AB\r\nCD", strings.Repeat("x", 40), float64(7)} {
		base := pairingServer(t, map[string]any{"code": code, "poll": "p"})
		var said []string
		_, err := TokenOf(context.Background(), TerminalCredentials{Kind: CredentialsPairing, Label: "t"}, base, func(line string) { said = append(said, line) }, nil)
		if err == nil || !strings.Contains(err.Error(), "pairing answer is malformed") {
			t.Fatalf("code %v: error = %v, want a malformed pairing answer", code, err)
		}
		if len(said) != 0 {
			t.Fatalf("code %v: said %v, want nothing", code, said)
		}
	}
}
