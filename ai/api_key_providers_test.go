package ai

import (
	"strings"
	"testing"
)

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
	allowed := map[string]bool{
		"anthropic":      true,
		"github-copilot": true,
		"kimi-coding":    true,
		"meta":           true,
		"openrouter":     true,
		"radius":         true,
		"xai":            true,
	}
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

// TestAPIKeyProvidersCoverCatalogEnvAuth locks the upstream
// getLoginProviderOptions("api_key") set: every catalog provider whose auth
// exposes an API-key method, with upstream provider.name labels. opencode-go
// regressed when this list was a hand-maintained 18-provider subset.
func TestAPIKeyProvidersCoverCatalogEnvAuth(t *testing.T) {
	got := APIKeyProviders()
	byID := make(map[string]string, len(got))
	for _, p := range got {
		byID[p.ID] = p.Name
	}
	want := map[string]string{
		"opencode":                   "OpenCode Zen",
		"opencode-go":                "OpenCode Go",
		"deepseek":                   "DeepSeek",
		"xai":                        "xAI",
		"cerebras":                   "Cerebras",
		"baseten":                    "Baseten",
		"cloudflare-ai-gateway":      "Cloudflare AI Gateway",
		"cloudflare-workers-ai":      "Cloudflare Workers AI",
		"nvidia":                     "NVIDIA",
		"together":                   "Together",
		"zai":                        "Z.AI",
		"zai-coding-cn":              "Z.AI Coding CN",
		"minimax-cn":                 "MiniMax CN",
		"moonshotai-cn":              "Moonshot AI CN",
		"google":                     "Google",
		"azure-openai-responses":     "Azure OpenAI",
		"anthropic":                  "Anthropic",
		"github-copilot":             "GitHub Copilot",
		"meta":                       "Meta",
		"qwen-token-plan":            "Qwen Token Plan",
		"qwen-token-plan-cn":         "Qwen Token Plan CN",
		"qwen-token-plan-individual": "Qwen Token Plan Individual",
	}
	for providerID, name := range want {
		if byID[providerID] != name {
			t.Errorf("APIKeyProviders[%q] = %q, want %q", providerID, byID[providerID], name)
		}
	}
	if _, ok := byID["openai-codex"]; ok {
		t.Error("openai-codex is OAuth-only upstream and must not expose an API-key login")
	}
}

// TestAPIKeyProvidersSortedByDisplayName locks upstream's
// provider.name.localeCompare order, including the case-insensitive xAI /
// Xiaomi and OpenCode Go / OpenCode Zen positions a byte sort would get wrong.
func TestAPIKeyProvidersSortedByDisplayName(t *testing.T) {
	got := APIKeyProviders()
	for i := 1; i < len(got); i++ {
		prev, cur := got[i-1], got[i]
		if strings.ToLower(prev.Name) > strings.ToLower(cur.Name) {
			t.Fatalf("APIKeyProviders not sorted by name: %q before %q", prev.Name, cur.Name)
		}
	}
	index := func(id string) int {
		for i, p := range got {
			if p.ID == id {
				return i
			}
		}
		return -1
	}
	if index("opencode-go") > index("opencode") {
		t.Error("OpenCode Go must sort before OpenCode Zen")
	}
	if index("xai") > index("xiaomi") {
		t.Error("xAI must sort before Xiaomi (case-insensitive locale order)")
	}
}
