package tui

// pi: packages/coding-agent/src/modes/interactive/components/oauth-selector.ts

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestOAuthSelector_Render(t *testing.T) {
	providers := []OAuthProvider{
		{ID: "anthropic", Name: "Anthropic (Claude Pro/Max)", AuthType: "oauth"},
		{ID: "github-copilot", Name: "GitHub Copilot", AuthType: "oauth", Status: testAuthCheck{"oauth", "stored"}},
		{ID: "openai", Name: "OpenAI", AuthType: "api_key", Status: testAuthCheck{"api_key", "OPENAI_API_KEY"}},
		{ID: "openrouter", Name: "OpenRouter", AuthType: "api_key"},
	}

	sel := NewOAuthSelectorComponent("login", providers, nil, nil)
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
	// .upstream/v1.0.0/packages/coding-agent/src/modes/interactive/components/oauth-selector.ts:37
	if !strings.Contains(joined, "• not configured") {
		t.Errorf("missing not configured indicator:\n%s", joined)
	}
}

func TestOAuthSelector_StatusIndicators(t *testing.T) {
	cases := []struct {
		name string
		p    OAuthProvider
		want string
	}{
		{"stored same auth type default source", OAuthProvider{ID: "github-copilot", Name: "GitHub Copilot", AuthType: "oauth", Status: testAuthCheck{"oauth", ""}}, "✓ configured"},
		{"stored same auth type stored source", OAuthProvider{ID: "github-copilot", Name: "GitHub Copilot", AuthType: "oauth", Status: testAuthCheck{"oauth", "stored"}}, "✓ stored"},
		{"logout stored credential source", OAuthProvider{ID: "openai", Name: "OpenAI", AuthType: "api_key", Status: testAuthCheck{"api_key", "stored credential"}}, "✓ configured"},
		{"stored other auth type oauth", OAuthProvider{ID: "anthropic", Name: "Anthropic", AuthType: "api_key", Status: testAuthCheck{"oauth", ""}}, "subscription configured"},
		{"stored other auth type api key", OAuthProvider{ID: "openai", Name: "OpenAI", AuthType: "oauth", Status: testAuthCheck{"api_key", ""}}, "API key configured"},
		{"runtime key", OAuthProvider{ID: "openai", Name: "OpenAI", AuthType: "api_key", Status: testAuthCheck{"api_key", "runtime"}}, "✓ runtime"},
		{"fallback key", OAuthProvider{ID: "openai", Name: "OpenAI", AuthType: "api_key", Status: testAuthCheck{"api_key", "fallback"}}, "✓ fallback"},
		{"models.json key", OAuthProvider{ID: "openai", Name: "OpenAI", AuthType: "api_key", Status: testAuthCheck{"api_key", "models_json_key"}}, "✓ models_json_key"},
		{"models.json command", OAuthProvider{ID: "openai", Name: "OpenAI", AuthType: "api_key", Status: testAuthCheck{"api_key", "models_json_command"}}, "✓ models_json_command"},
		// .upstream/v1.0.0/packages/coding-agent/src/modes/interactive/components/oauth-selector.ts:37
		{"oauth not configured", OAuthProvider{ID: "anthropic", Name: "Anthropic", AuthType: "oauth"}, "• not configured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatAuthSelectorProviderStatus(tc.p)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("FormatAuthSelectorProviderStatus(%+v) = %q, want substring %q", tc.p, got, tc.want)
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

	sel := NewOAuthSelectorComponent("login", providers, nil, nil)
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

	sel := NewOAuthSelectorComponent("logout", providers, nil, nil)
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
	sel := NewOAuthSelectorComponent("logout", []OAuthProvider{{ID: "x", Name: "X", AuthType: "oauth"}}, nil, nil)
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
	sel := NewOAuthSelectorComponent("login", providers, nil, nil)
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
	sel := NewOAuthSelectorComponent("login", providers, nil, nil)
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
	sel := NewOAuthSelectorComponent("login", providers, nil, nil)
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
	mixed := NewOAuthSelectorComponent("logout", []OAuthProvider{
		{ID: "anthropic", Name: "Anthropic", AuthType: "oauth"},
		{ID: "openai", Name: "OpenAI", AuthType: "api_key"},
	}, nil, nil)
	out := stripANSI(strings.Join(mixed.Render(100), "\n"))
	if !strings.Contains(out, "→ Anthropic [subscription]") || !strings.Contains(out, "  OpenAI [API key]") {
		t.Fatalf("mixed list lacks auth type labels:\n%s", out)
	}
	single := NewOAuthSelectorComponent("login", []OAuthProvider{{ID: "anthropic", Name: "Anthropic", AuthType: "oauth"}}, nil, nil)
	if out := stripANSI(strings.Join(single.Render(100), "\n")); strings.Contains(out, "[subscription]") {
		t.Fatalf("single-type list shows an auth type label:\n%s", out)
	}
}

// Upstream routes every non-navigation key to the search input, so "k" and
// "j" type instead of moving the selection.
func TestOAuthSelector_LettersEditSearch(t *testing.T) {
	sel := NewOAuthSelectorComponent("login", []OAuthProvider{
		{ID: "anthropic", Name: "Anthropic", AuthType: "oauth"},
		{ID: "kimi-coding", Name: "Kimi For Coding", AuthType: "oauth"},
	}, nil, nil)
	sel.HandleInput("k")
	if out := stripANSI(strings.Join(sel.Render(100), "\n")); !strings.Contains(out, "> k") || !strings.Contains(out, "→ Kimi For Coding") {
		t.Fatalf("typing k did not filter:\n%s", out)
	}
}

// OAuthSelectorComponent extends Container (oauth-selector.ts:63): border, spacer, title, spacer, search input, spacer, list
// container, spacer, border; moving the cursor rebuilds the list container's rows (updateList).
func TestOAuthSelectorComponentChildrenFollowUpstream(t *testing.T) {
	providers := []OAuthProvider{{ID: "a", Name: "Alpha", AuthType: "oauth"}, {ID: "b", Name: "Beta", AuthType: "oauth"}}
	s := NewOAuthSelectorComponent("login", providers, nil, nil)
	if got := len(s.Children()); got != 9 {
		t.Fatalf("children = %d, want 9", got)
	}
	if got := len(s.listContainer.Children()); got != 2 {
		t.Fatalf("list rows = %d, want 2", got)
	}
	before := strings.Join(s.Render(60), "\n")
	s.HandleInput("\x1b[B")
	if s.SelectedID() != "b" || strings.Join(s.Render(60), "\n") == before {
		t.Fatalf("down did not select b and change the render (selected %q)", s.SelectedID())
	}
}

// oauth-selector.ts handleInput: confirm calls onSelect(provider.id, provider.authType) for the highlighted filtered
// provider, an empty list calls nothing, and cancel calls onCancel.
func TestOAuthSelectorCallbacks(t *testing.T) {
	providers := []OAuthProvider{{ID: "a", Name: "A", AuthType: "oauth"}, {ID: "a", Name: "A", AuthType: "api_key"}}
	var selected []string
	cancels := 0
	newSel := func(ps []OAuthProvider) *OAuthSelectorComponent {
		return NewOAuthSelectorComponent("login", ps, func(id, authType string) { selected = append(selected, id+"/"+authType) }, func() { cancels++ })
	}

	sel := newSel(providers)
	sel.HandleInput("\x1b[B")
	sel.HandleInput("\r")
	if !reflect.DeepEqual(selected, []string{"a/api_key"}) || cancels != 0 {
		t.Fatalf("selected=%v cancels=%d", selected, cancels)
	}

	selected = nil
	empty := newSel(nil)
	empty.HandleInput("\r")
	if len(selected) != 0 || empty.Done() {
		t.Fatalf("an empty list selected %v or finished", selected)
	}
	empty.HandleInput("\x1b")
	if cancels != 1 || !empty.Cancelled() {
		t.Fatalf("cancel callbacks=%d cancelled=%v", cancels, empty.Cancelled())
	}
}

// packages/coding-agent/src/modes/interactive/components/oauth-selector.ts:193-223 handleInput boundary transitions: up and down clamp at
// the first and last provider without wrapping, arrows and confirm do nothing on an empty filtered list, cancel runs before the search
// input sees the key, and every other key edits the fuzzy search and re-filters (clamping the selection).
func TestOAuthSelectorBoundaryTransitions(t *testing.T) {
	providers := []OAuthProvider{
		{ID: "anthropic", Name: "Anthropic", AuthType: "oauth"},
		{ID: "github-copilot", Name: "GitHub Copilot", AuthType: "oauth"},
		{ID: "custom-oauth", Name: "Custom OAuth", AuthType: "oauth"},
	}
	const up, down, enter, escape = "\x1b[A", "\x1b[B", "\r", "\x1b"
	press := func(sel *OAuthSelectorComponent, keys ...string) {
		for _, key := range keys {
			sel.HandleInput(key)
		}
	}
	selected := func(providers []OAuthProvider, keys ...string) (id string, done bool) {
		sel := NewOAuthSelectorComponent("login", providers, func(providerID, _ string) { id = providerID }, nil)
		press(sel, keys...)
		return id, sel.Done()
	}

	if id, done := selected(providers, up, up, enter); id != "anthropic" || !done {
		t.Errorf("up at the first provider wrapped: selected %q, done %v", id, done)
	}
	if id, done := selected(providers, down, down, down, down, enter); id != "custom-oauth" || !done {
		t.Errorf("down at the last provider wrapped: selected %q, done %v", id, done)
	}
	if id, done := selected(providers, down, down, down, up, enter); id != "github-copilot" || !done {
		t.Errorf("one up from the clamped last provider: selected %q, done %v", id, done)
	}

	// A search that matches nothing leaves an empty list: arrows and confirm do nothing and the selector stays open.
	called := false
	sel := NewOAuthSelectorComponent("login", providers, func(string, string) { called = true }, nil)
	press(sel, "z", "z", "z", "q", down, up, enter)
	if called || sel.Done() || sel.SelectedID() != "" {
		t.Errorf("empty filtered list: called %v, done %v, selected %q", called, sel.Done(), sel.SelectedID())
	}
	// Deleting the search restores the list and its first row.
	press(sel, "\x7f", "\x7f", "\x7f", "\x7f", enter)
	if !called || sel.SelectedID() != "anthropic" {
		t.Errorf("restored list: called %v, selected %q", called, sel.SelectedID())
	}

	// Cancel is matched before the search input, so escape never reaches the search.
	cancelled := 0
	sel = NewOAuthSelectorComponent("login", providers, nil, func() { cancelled++ })
	press(sel, "c", "u", escape)
	if cancelled != 1 || !sel.Cancelled() || sel.SelectedID() != "" {
		t.Errorf("cancel: callbacks %d, cancelled %v, selected %q", cancelled, sel.Cancelled(), sel.SelectedID())
	}

	// Other keys edit the search and re-filter; the selection clamps to the filtered list.
	sel = NewOAuthSelectorComponent("login", providers, nil, nil)
	press(sel, down, down, "c", "u", "s")
	if got := sel.SelectedID(); got != "custom-oauth" {
		t.Errorf("search edit selected %q, want custom-oauth", got)
	}
}
