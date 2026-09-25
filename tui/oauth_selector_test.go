package tui

import (
	"fmt"
	"strings"
	"testing"
)

func TestOAuthSelector_Render(t *testing.T) {
	providers := []OAuthProvider{
		{ID: "anthropic", Name: "Anthropic (Claude Pro/Max)", AuthType: "oauth"},
		{ID: "github-copilot", Name: "GitHub Copilot", AuthType: "oauth", Stored: true, StoredType: "oauth", AuthStatusSource: "stored"},
		{ID: "openai", Name: "OpenAI", AuthType: "api_key", AuthStatusSource: "environment", AuthStatusLabel: "OPENAI_API_KEY"},
		{ID: "openrouter", Name: "OpenRouter", AuthType: "api_key"},
	}

	sel := NewOAuthSelector("login", providers)
	lines := sel.Render(80)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "Select provider to configure:") {
		t.Errorf("missing title in render:\n%s", joined)
	}
	if !strings.Contains(joined, "> ") {
		t.Errorf("missing search input in render:\n%s", joined)
	}
	if !strings.Contains(joined, "→") {
		t.Errorf("missing cursor arrow in render:\n%s", joined)
	}
	if !strings.Contains(joined, "✓ stored") {
		t.Errorf("missing stored marker:\n%s", joined)
	}
	if !strings.Contains(joined, "✓ env: OPENAI_API_KEY") {
		t.Errorf("missing env indicator:\n%s", joined)
	}
	if !strings.Contains(joined, "• unconfigured") {
		t.Errorf("missing unconfigured indicator:\n%s", joined)
	}
}

func TestOAuthSelector_StatusIndicators(t *testing.T) {
	cases := []struct {
		name string
		p    OAuthProvider
		want string
	}{
		{"stored same auth type default source", OAuthProvider{ID: "github-copilot", Name: "GitHub Copilot", AuthType: "oauth", Stored: true, StoredType: "oauth"}, "✓ configured"},
		{"stored same auth type stored source", OAuthProvider{ID: "github-copilot", Name: "GitHub Copilot", AuthType: "oauth", Stored: true, StoredType: "oauth", AuthStatusSource: "stored"}, "✓ stored"},
		{"stored other auth type oauth", OAuthProvider{ID: "anthropic", Name: "Anthropic", AuthType: "api_key", Stored: true, StoredType: "oauth"}, "subscription configured"},
		{"stored other auth type api key", OAuthProvider{ID: "openai", Name: "OpenAI", AuthType: "oauth", Stored: true, StoredType: "api_key"}, "API key configured"},
		{"runtime key", OAuthProvider{ID: "openai", Name: "OpenAI", AuthType: "api_key", AuthStatusSource: "runtime"}, "✓ runtime API key"},
		{"fallback key", OAuthProvider{ID: "openai", Name: "OpenAI", AuthType: "api_key", AuthStatusSource: "fallback"}, "✓ custom API key"},
		{"models.json key", OAuthProvider{ID: "openai", Name: "OpenAI", AuthType: "api_key", AuthStatusSource: "models_json_key"}, "✓ key in models.json"},
		{"models.json command", OAuthProvider{ID: "openai", Name: "OpenAI", AuthType: "api_key", AuthStatusSource: "models_json_command"}, "✓ command in models.json"},
		{"oauth unconfigured", OAuthProvider{ID: "anthropic", Name: "Anthropic", AuthType: "oauth"}, "• unconfigured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := authSelectorIndicator(tc.p)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("authSelectorIndicator(%+v) = %q, want substring %q", tc.p, got, tc.want)
			}
		})
	}
}

func TestOAuthSelector_Navigation(t *testing.T) {
	providers := []OAuthProvider{
		{ID: "anthropic", Name: "Anthropic", AuthType: "oauth"},
		{ID: "github-copilot", Name: "GitHub Copilot", AuthType: "oauth"},
		{ID: "custom-oauth", Name: "Custom OAuth", AuthType: "oauth"},
	}

	sel := NewOAuthSelector("login", providers)
	sel.HandleInput("\x1b[B")
	sel.HandleInput("\x1b[B")
	sel.HandleInput("\n")

	if !sel.Done() {
		t.Fatal("expected Done after Enter")
	}
	if sel.Cancelled() {
		t.Fatal("should not be cancelled")
	}
	if sel.SelectedID() != "custom-oauth" {
		t.Errorf("expected custom-oauth, got %q", sel.SelectedID())
	}
}

func TestOAuthSelector_Cancel(t *testing.T) {
	providers := []OAuthProvider{{ID: "anthropic", Name: "Anthropic", AuthType: "oauth"}}

	sel := NewOAuthSelector("logout", providers)
	sel.HandleInput("\x1b")

	if !sel.Done() {
		t.Fatal("expected Done after Esc")
	}
	if !sel.Cancelled() {
		t.Fatal("expected Cancelled")
	}
	if sel.SelectedID() != "" {
		t.Errorf("expected empty ID on cancel, got %q", sel.SelectedID())
	}
}

func TestOAuthSelector_LogoutTitle(t *testing.T) {
	sel := NewOAuthSelector("logout", []OAuthProvider{{ID: "x", Name: "X", AuthType: "oauth"}})
	lines := sel.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Select provider to logout:") {
		t.Errorf("expected logout title, got:\n%s", joined)
	}
}

func TestOAuthSelector_SearchFilters(t *testing.T) {
	providers := []OAuthProvider{
		{ID: "anthropic", Name: "Anthropic", AuthType: "oauth"},
		{ID: "openai", Name: "OpenAI", AuthType: "api_key"},
		{ID: "openrouter", Name: "OpenRouter", AuthType: "api_key"},
	}
	sel := NewOAuthSelector("login", providers)
	sel.HandleInput("t")
	sel.HandleInput("e")
	sel.HandleInput("r")
	if got := sel.SelectedID(); got != "openrouter" {
		t.Fatalf("selected after filter = %q, want openrouter", got)
	}
	joined := strings.Join(sel.Render(80), "\n")
	if strings.Contains(joined, "Anthropic") || strings.Contains(joined, "OpenAI") {
		t.Fatalf("filtered render should only show OpenRouter, got:\n%s", joined)
	}
}

func TestOAuthSelector_SearchNoMatches(t *testing.T) {
	providers := []OAuthProvider{{ID: "openai", Name: "OpenAI", AuthType: "api_key"}}
	sel := NewOAuthSelector("login", providers)
	sel.HandleInput("z")
	joined := strings.Join(sel.Render(60), "\n")
	if !strings.Contains(joined, "No matching providers") {
		t.Fatalf("expected no matching providers message, got:\n%s", joined)
	}
}

// Upstream oauth-selector.ts shows at most 8 rows centered on the selection
// and a "(n/total)" row when the list is clipped.
func TestOAuthSelector_MaxVisibleWindowAndScrollRow(t *testing.T) {
	var providers []OAuthProvider
	for i := range 12 {
		providers = append(providers, OAuthProvider{ID: fmt.Sprintf("p%02d", i), Name: fmt.Sprintf("Provider %02d", i), AuthType: "oauth"})
	}
	sel := NewOAuthSelector("login", providers)
	rows := func() []string {
		var out []string
		for _, line := range sel.Render(80) {
			if p := strings.TrimSpace(stripANSI(line)); strings.Contains(p, "Provider ") || strings.HasPrefix(p, "(") {
				out = append(out, p)
			}
		}
		return out
	}
	got := rows()
	if len(got) != 9 || !strings.HasPrefix(got[0], "→ Provider 00") || !strings.HasPrefix(got[7], "Provider 07") || got[8] != "(1/12)" {
		t.Fatalf("initial window = %q", got)
	}
	for range 9 {
		sel.HandleInput("\x1b[B")
	}
	got = rows()
	if !strings.HasPrefix(got[0], "Provider 04") || !strings.HasPrefix(got[5], "→ Provider 09") || got[8] != "(10/12)" {
		t.Fatalf("window after 9 Down = %q", got)
	}
}

// Rows carry an auth type label only when the list mixes auth types.
func TestOAuthSelector_AuthTypeLabelsForMixedLists(t *testing.T) {
	mixed := NewOAuthSelector("logout", []OAuthProvider{
		{ID: "anthropic", Name: "Anthropic", AuthType: "oauth"},
		{ID: "openai", Name: "OpenAI", AuthType: "api_key"},
	})
	out := stripANSI(strings.Join(mixed.Render(100), "\n"))
	if !strings.Contains(out, "→ Anthropic [subscription]") || !strings.Contains(out, "  OpenAI [API key]") {
		t.Fatalf("mixed list lacks auth type labels:\n%s", out)
	}
	single := NewOAuthSelector("login", []OAuthProvider{{ID: "anthropic", Name: "Anthropic", AuthType: "oauth"}})
	if out := stripANSI(strings.Join(single.Render(100), "\n")); strings.Contains(out, "[subscription]") {
		t.Fatalf("single-type list shows an auth type label:\n%s", out)
	}
}

// Upstream routes every non-navigation key to the search input, so "k" and
// "j" type instead of moving the selection.
func TestOAuthSelector_LettersEditSearch(t *testing.T) {
	sel := NewOAuthSelector("login", []OAuthProvider{
		{ID: "anthropic", Name: "Anthropic", AuthType: "oauth"},
		{ID: "kimi-coding", Name: "Kimi For Coding", AuthType: "oauth"},
	})
	sel.HandleInput("k")
	if out := stripANSI(strings.Join(sel.Render(100), "\n")); !strings.Contains(out, "> k") || !strings.Contains(out, "→ Kimi For Coding") {
		t.Fatalf("typing k did not filter:\n%s", out)
	}
}
