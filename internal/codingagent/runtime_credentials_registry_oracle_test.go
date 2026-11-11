package codingagent

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/core/runtime-credentials.ts
// ModelRuntime.setRuntimeApiKey / removeRuntimeApiKey sit on the RuntimeCredentials overlay (model-runtime.ts:622-641). Over one
// scripted sequence of runtime-key installs, replacements and removals above stored credentials, Pi's runtime and PiG's registry report the
// same provider auth status, the same enumeration order (overrides replace a stored entry in place and append when new) and the same
// configured-auth answer after every step.
func TestRegistryRuntimeKeyOverlayMatchesPi(t *testing.T) {
	type step struct {
		Op       string `json:"op"`
		Provider string `json:"provider"`
		Key      string `json:"key"`
	}
	type state struct {
		Status map[string]struct {
			Configured bool   `json:"configured"`
			Source     string `json:"source"`
		} `json:"status"`
		List []struct {
			ProviderID string `json:"providerId"`
			Type       string `json:"type"`
		} `json:"list"`
		Configured map[string]bool `json:"configured"`
	}
	steps := []step{
		{"none", "", ""},
		{"set", "anthropic", "runtime-key"},
		{"set", "groq", "other-runtime-key"},
		{"set", "openai", "masks-oauth"},
		{"set", "groq", "replaced"},
		{"remove", "anthropic", ""},
		{"remove", "openai", ""},
		{"remove", "never-set", ""},
		{"set", "anthropic", "again"},
		{"remove", "groq", ""},
	}
	probed := []string{"anthropic", "openai", "groq", "never-set"}
	// The provider that only a runtime key configures must not also be configured by the environment, in Pi or PiG.
	t.Setenv("GROQ_API_KEY", "")
	if err := os.Unsetenv("GROQ_API_KEY"); err != nil {
		t.Fatal(err)
	}
	var want struct {
		States []state `json:"states"`
	}
	pioracle.Run(t, `
const { ModelRuntime } = await load("pi-coding-agent/core/model-runtime.js");
const { AuthStorage } = await load("pi-coding-agent/core/auth-storage.js");
const { InMemoryCodingAgentModelsStore } = await load("pi-coding-agent/core/models-store.js");
const runtime = await ModelRuntime.create({
	credentials: AuthStorage.inMemory({
		anthropic: { type: "api_key", key: "stored-key" },
		openai: { type: "oauth", access: "access", refresh: "refresh", expires: Date.now() + 3600000 },
	}),
	modelsStore: new InMemoryCodingAgentModelsStore(),
	modelsPath: null,
	allowModelNetwork: false,
});
const states = [];
for (const step of input.steps) {
	if (step.op === "set") await runtime.setRuntimeApiKey(step.provider, step.key);
	if (step.op === "remove") await runtime.removeRuntimeApiKey(step.provider);
	const status = {};
	const configured = {};
	for (const id of input.probed) {
		const s = runtime.getProviderAuthStatus(id);
		status[id] = { configured: s.configured, source: s.source ?? "" };
		configured[id] = runtime.hasConfiguredAuth(id);
	}
	states.push({ status, list: (await runtime.listCredentials()).map((c) => ({ providerId: c.providerId, type: c.type })), configured });
}
emit({ states });`, map[string]any{"steps": steps, "probed": probed}, &want)

	ctx := context.Background()
	registry := NewModelRegistryWithModelsPath("")
	registry.SetCredentialStore(ai.NewInMemoryAuthStorage(map[string]ai.Credential{
		"anthropic": {Type: ai.CredentialAPIKey, Key: "stored-key"},
		"openai":    {Type: ai.CredentialOAuth, Access: "access", Refresh: "refresh", Expires: 1 << 50},
	}))
	for i, s := range steps {
		switch s.Op {
		case "set":
			if err := registry.SetRuntimeAPIKeyOperation(ctx, s.Provider, s.Key); err != nil {
				t.Fatal(err)
			}
		case "remove":
			if err := registry.RemoveRuntimeAPIKeyOperation(ctx, s.Provider); err != nil {
				t.Fatal(err)
			}
		}
		listed, err := registry.ListCredentials(ctx)
		if err != nil {
			t.Fatal(err)
		}
		type entry struct{ ProviderID, Type string }
		var gotList, wantList []entry
		for _, info := range listed {
			gotList = append(gotList, entry{info.ProviderID, string(info.Type)})
		}
		for _, info := range want.States[i].List {
			wantList = append(wantList, entry{info.ProviderID, info.Type})
		}
		if !reflect.DeepEqual(gotList, wantList) {
			t.Errorf("step %d %+v: credentials = %v, Pi %v", i, s, gotList, wantList)
		}
		for _, id := range probed {
			status := registry.GetProviderAuthStatus(id)
			if wantStatus := want.States[i].Status[id]; status.Configured != wantStatus.Configured || string(status.Source) != wantStatus.Source {
				t.Errorf("step %d %+v: auth status of %s = %+v, Pi %+v", i, s, id, status, wantStatus)
			}
			if got := registry.HasConfiguredAuth(id); got != want.States[i].Configured[id] {
				t.Errorf("step %d %+v: configured auth of %s = %v, Pi %v", i, s, id, got, want.States[i].Configured[id])
			}
		}
	}
}
