package ai

import "testing"

func TestAPIKeyProvidersStable(t *testing.T) {
	got := APIKeyProviders()
	if len(got) < 10 {
		t.Fatalf("APIKeyProviders too short: %d", len(got))
	}
	// No duplicates, every entry has both fields.
	seen := map[string]bool{}
	for _, p := range got {
		if p.ID == "" || p.Name == "" {
			t.Errorf("incomplete entry: %+v", p)
		}
		if seen[p.ID] {
			t.Errorf("duplicate id %q in APIKeyProviders", p.ID)
		}
		seen[p.ID] = true
	}
	// Returned slice must be independent of internal state.
	got[0].ID = "mutated"
	if APIKeyProviders()[0].ID == "mutated" {
		t.Errorf("APIKeyProviders returned internal slice; callers can mutate registry")
	}
}

func TestAPIKeyProviderNameLookup(t *testing.T) {
	if got := APIKeyProviderName("openai"); got != "OpenAI" {
		t.Errorf("openai display name: got %q want %q", got, "OpenAI")
	}
	if got := APIKeyProviderName("nonexistent-provider"); got != "" {
		t.Errorf("unknown provider should return empty, got %q", got)
	}
}

func TestAPIKeyProvidersOverlapOnlyForDualAuthProviders(t *testing.T) {
	// Upstream providers may deliberately support both a pasted API key and an
	// account OAuth flow. Every overlap must be named here so an accidental
	// duplicate such as the historical GitHub Copilot entry still fails.
	allowed := map[string]bool{"kimi-coding": true, "openrouter": true, "radius": true, "xai": true}
	oauth := map[string]bool{}
	for _, p := range GetOAuthProviders() {
		oauth[p.ID()] = true
	}
	for _, p := range APIKeyProviders() {
		if oauth[p.ID] && !allowed[p.ID] {
			t.Errorf("provider %q is in both API-key and OAuth catalogs without an explicit upstream dual-auth contract", p.ID)
		}
	}
	if !oauth["kimi-coding"] {
		t.Error("Kimi For Coding must expose its upstream account OAuth flow alongside KIMI_API_KEY")
	}
}
