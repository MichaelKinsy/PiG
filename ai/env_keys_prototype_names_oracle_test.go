package ai_test

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// D645, the provider environment key row. Pi's findEnvKeys reads `envMap[provider]` on a plain object (packages/ai/src/env-api-keys.ts:126), so for a
// provider named like a member of Object.prototype it gets the inherited member where a list of variable names is promised and throws
// `TypeError: envVars.filter is not a function`; getEnvApiKey throws with it. PiG's table holds own entries only: such a provider has no environment
// key, like any provider the table does not hold. `openai` and `nosuchprovider` are the ordinary names and must be equal on both sides.
func TestAProviderNamedLikeAnObjectPrototypeMemberHasNoEnvironmentKey(t *testing.T) {
	const thrown = "THROWS TypeError: envVars.filter is not a function"
	cases := []struct{ provider, pi string }{
		{"constructor", thrown}, {"toString", thrown}, {"hasOwnProperty", thrown}, {"__proto__", thrown},
		{"openai", `["OPENAI_API_KEY"] "k"`}, {"nosuchprovider", "undefined undefined"},
	}
	env := map[string]string{"OPENAI_API_KEY": "k"}
	providers := make([]string, 0, len(cases))
	for _, c := range cases {
		providers = append(providers, c.provider)
	}
	var pi []string
	pioracle.Run(t, `
const { findEnvKeys, getEnvApiKey } = await load("pi-ai/env-api-keys.js");
const show = (value) => value === undefined ? "undefined" : JSON.stringify(value);
emit(input.providers.map((provider) => {
	try { return show(findEnvKeys(provider, input.env)) + " " + show(getEnvApiKey(provider, input.env)); } catch (error) { return "THROWS " + String(error); }
}));`, map[string]any{"providers": providers, "env": env}, &pi)
	if len(pi) != len(cases) {
		t.Fatalf("Pi answered %d of %d probes", len(pi), len(cases))
	}
	for i, c := range cases {
		keys, key := ai.FindEnvKeys(c.provider, env), ai.GetEnvAPIKey(c.provider, env)
		t.Logf("findEnvKeys(%q), getEnvApiKey(%q): Pi %s, PiG %v %q", c.provider, c.provider, pi[i], keys, key)
		if pi[i] != c.pi {
			t.Errorf("provider %q: Pi answers %s, the record says %s", c.provider, pi[i], c.pi)
		}
		wantKeys, wantKey := []string(nil), ""
		if c.provider == "openai" {
			wantKeys, wantKey = []string{"OPENAI_API_KEY"}, "k"
		}
		if !reflect.DeepEqual(keys, wantKeys) || key != wantKey {
			t.Errorf("provider %q: FindEnvKeys = %v, GetEnvAPIKey = %q, want %v and %q", c.provider, keys, key, wantKeys, wantKey)
		}
	}
}
