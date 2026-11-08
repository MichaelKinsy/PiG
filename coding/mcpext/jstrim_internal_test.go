package mcpext

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// String.prototype.trim removes U+FEFF and keeps U+0085; Go's strings.TrimSpace does the reverse. These cases tell the
// two apart at every trim of oauth.ts.

// oauth.ts parseStates: `if (!content?.trim()) return {}`.
func TestParseStatesTreatsJavaScriptWhitespaceAsEmpty(t *testing.T) {
	states, err := parseStates("\uFEFF\n\u3000")
	if err != nil || states.Len() != 0 {
		t.Fatalf("parseStates(BOM) = %v, %v; want an empty object", states, err)
	}
	if _, err := parseStates("\u0085"); err == nil {
		t.Fatal("parseStates(U+0085) succeeded; JSON.parse of a non-whitespace character fails")
	}
}

type fixedPrompt string

func (fixedPrompt) ShowAuthorizationURL(oauth.URL) {}

func (p fixedPrompt) PromptForRedirectURL(context.Context) (string, error) { return string(p), nil }
