package ai

// pi: packages/ai/src/auth/oauth/load.ts

import (
	"maps"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// TestOAuthFlowLoadersMatchPi runs every loader of Pi's auth/oauth/load.ts (loadAnthropicOAuth ... loadRadiusOAuth) and checks that the flow it returns has a Go counterpart registered under the same provider id, with the same display name and subscription flag. load.ts is a loader table: each entry returns the OAuthAuth of one flow module. The set of OAuth providers Go registers is exactly the set of Pi's built-in providers that carry auth.oauth (the flows lazy-oauth.ts registerLazyOAuthFlows registers and providers/*.ts load through lazyOAuth), read from Pi's providers/all.ts builtinProviders(), so a flow Pi adds or removes fails this test.
func TestOAuthFlowLoadersMatchPi(t *testing.T) {
	var piFlowIDs []string
	pioracle.Run(t, `
const all = await load("pi-ai/providers/all.js");
emit(all.builtinProviders().filter((p) => p.auth && p.auth.oauth).map((p) => p.id).sort());`, nil, &piFlowIDs)
	if got := slices.Sorted(maps.Keys(builtInOAuthProviders)); !slices.Equal(got, piFlowIDs) {
		t.Errorf("built-in OAuth providers = %v, Pi's providers with auth.oauth = %v", got, piFlowIDs)
	}

	ids := []string{"anthropic", "openai-codex", "openai", "github-copilot", "openrouter", "kimi-coding", "meta", "xai", "radius"}
	type flow struct {
		Name           string `json:"name"`
		IsSubscription bool   `json:"isSubscription"`
	}
	var want map[string]flow
	pioracle.Run(t, `
const mod = await load("pi-ai/auth/oauth/load.js");
const loaders = {
	"anthropic": () => mod.loadAnthropicOAuth(),
	"openai-codex": () => mod.loadOpenAICodexOAuth(),
	"openai": () => mod.loadOpenAIChatGPTOAuth(),
	"github-copilot": () => mod.loadGitHubCopilotOAuth(),
	"openrouter": () => mod.loadOpenRouterOAuth(),
	"kimi-coding": () => mod.loadKimiCodingOAuth(),
	"meta": () => mod.loadMetaOAuth(),
	"xai": () => mod.loadXaiOAuth(),
	"radius": () => mod.loadRadiusOAuth({ name: "Radius", gateway: input.gateway }),
};
const out = {};
for (const id of input.ids) {
	const auth = await loaders[id]();
	out[id] = { name: auth.name, isSubscription: auth.isSubscription === true };
}
emit(out);`, map[string]any{"ids": ids, "gateway": DefaultRadiusGateway}, &want)
	for _, id := range ids {
		got, ok := OAuthProviderAuth(id)
		if !ok {
			t.Errorf("no OAuth provider registered for %q, Pi's load.ts loads one", id)
			continue
		}
		if got.Name != want[id].Name || got.IsSubscription != want[id].IsSubscription {
			t.Errorf("%s: Name %q subscription %v, Pi flow %q subscription %v", id, got.Name, got.IsSubscription, want[id].Name, want[id].IsSubscription)
		}
	}
}
