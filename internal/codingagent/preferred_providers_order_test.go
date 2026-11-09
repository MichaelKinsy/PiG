package codingagent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring/localecompare"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi 683a7a868 interactive-mode.ts getLoginProviderOptions sorts with compareProviders: Radius and OpenRouter first, in
// that order, then every other provider alphabetically by name; a provider's account option stays above its API key option.
func TestLoginProviderOptionsListPreferredPartnersFirstThenTheRestAlphabetically(t *testing.T) {
	m := NewInteractiveMode(nil, InteractiveModeOptions{AgentDir: t.TempDir()})
	options := m.getLoginProviderOptions(false)
	assertPartnersFirst(t, "login", options)
}

// Port of oauth-selector.test.ts "lists preferred partners first, then the rest alphabetically": Pi's five providers in its
// fixture order, as getLoginProviderOptions concatenates them (a provider's account option, then its API key option),
// sorted by the shared comparator.
func TestSortAuthProvidersMatchesPisPartnersFirstFixture(t *testing.T) {
	providers := []tui.OAuthProvider{
		{ID: "anthropic", Name: "Anthropic", AuthType: "api_key"},
		{ID: "openrouter", Name: "OpenRouter", AuthType: "oauth"},
		{ID: "openrouter", Name: "OpenRouter", AuthType: "api_key"},
		{ID: "zai", Name: "Z.AI", AuthType: "api_key"},
		{ID: "radius", Name: "Radius", AuthType: "oauth"},
		{ID: "google", Name: "Google", AuthType: "api_key"},
	}
	sortAuthProviders(providers)
	var got []string
	for _, provider := range providers {
		got = append(got, provider.ID+":"+provider.AuthType)
	}
	want := []string{"radius:oauth", "openrouter:oauth", "openrouter:api_key", "anthropic:api_key", "google:api_key", "zai:api_key"}
	if !slices.Equal(got, want) {
		t.Errorf("sorted login options = %v, want %v", got, want)
	}
}

func assertPartnersFirst(t *testing.T, label string, options []tui.OAuthProvider) {
	t.Helper()
	partners := len(ai.PreferredProviderIDs)
	var ids []string
	for _, option := range options {
		ids = append(ids, option.ID)
	}
	rank := func(id string) int {
		if index := slices.Index(ai.PreferredProviderIDs, id); index >= 0 {
			return index
		}
		return partners
	}
	for i := 1; i < len(options); i++ {
		a, b := options[i-1], options[i]
		if rank(a.ID) > rank(b.ID) {
			t.Fatalf("%s: %q (partner rank %d) is listed before %q (rank %d): %v", label, a.ID, rank(a.ID), b.ID, rank(b.ID), ids)
		}
		if rank(a.ID) == partners && rank(b.ID) == partners && localecompare.Compare(a.Name, b.Name) > 0 {
			t.Fatalf("%s: %q is listed before %q, not alphabetical: %v", label, a.Name, b.Name, ids)
		}
	}
	if !slices.Contains(ids, ai.RadiusProviderID) || ids[0] != ai.RadiusProviderID {
		t.Fatalf("%s: the first provider is %q, want %q: %v", label, ids[0], ai.RadiusProviderID, ids)
	}
}

// /logout lists the stored credentials with the same order.
func TestLogoutProviderOptionsListPreferredPartnersFirst(t *testing.T) {
	dir := t.TempDir()
	content := `{"zai":{"type":"api_key","key":"k"},"openrouter":{"type":"api_key","key":"k"},"anthropic":{"type":"api_key","key":"k"},"radius":{"type":"api_key","key":"k"},"google":{"type":"api_key","key":"k"}}`
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &InteractiveMode{opts: InteractiveModeOptions{AgentDir: dir}}
	providers, err := m.getLogoutProviderOptions()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, provider := range providers {
		got = append(got, provider.ID)
	}
	if want := []string{"radius", "openrouter", "anthropic", "google", "zai"}; !slices.Equal(got, want) {
		t.Fatalf("logout order = %v, want %v", got, want)
	}
}

// /login <provider> completions list the preferred partners first as well.
func TestLoginArgCompletionsListPreferredPartnersFirst(t *testing.T) {
	m := NewInteractiveMode(nil, InteractiveModeOptions{AgentDir: t.TempDir()})
	items := m.loginArgCompletions("")
	if len(items) < 2 || items[0].Value != ai.RadiusProviderID || items[1].Value != "openrouter" {
		var values []string
		for _, item := range items {
			values = append(values, item.Value)
		}
		t.Fatalf("completions start %v, want radius then openrouter", strings.Join(values[:min(4, len(values))], ", "))
	}
}
