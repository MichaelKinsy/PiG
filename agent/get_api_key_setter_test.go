package agent

import "testing"

// Pi packages/agent/src/agent.ts:197 `public getApiKey?` is a plain assignable property, :237 initialised from options and :495 read when a run builds its loop config. Assigning it after construction replaces the resolver for the next run; assigning undefined removes it.
func TestAgentSetGetAPIKeyReplacesAndRemovesTheResolverForTheNextRun(t *testing.T) {
	provider := &scriptedProvider{id: "acme", respond: replyText("done")}
	a := mustNewAgent(AgentOptions{Model: namedModel(provider)})
	if a.GetAPIKeyFunc() != nil {
		t.Fatal("a fresh Agent without getApiKey must report no resolver")
	}

	var asked []string
	a.SetGetAPIKey(func(id string) (string, error) {
		asked = append(asked, id)
		return "assigned-token", nil
	})
	if a.GetAPIKeyFunc() == nil {
		t.Fatal("GetAPIKeyFunc is nil after SetGetAPIKey")
	}
	mustSend(t, a, "one")
	if len(asked) != 1 || asked[0] != "acme" {
		t.Fatalf("assigned resolver asked for %v, want the model's provider once", asked)
	}
	if got := provider.request(1).opts.APIKey; got != "assigned-token" {
		t.Fatalf("request apiKey = %q, want the assigned resolver's token", got)
	}

	a.SetGetAPIKey(func(string) (string, error) { return "replacement-token", nil })
	mustSend(t, a, "two")
	if got := provider.request(2).opts.APIKey; got != "replacement-token" {
		t.Fatalf("request apiKey after replacement = %q, want replacement-token", got)
	}
	if len(asked) != 1 {
		t.Fatalf("the replaced resolver was asked again: %v", asked)
	}

	a.SetGetAPIKey(nil)
	if a.GetAPIKeyFunc() != nil {
		t.Fatal("GetAPIKeyFunc is non-nil after SetGetAPIKey(nil)")
	}
	mustSend(t, a, "three")
	if got := provider.request(3).opts.APIKey; got != "" {
		t.Fatalf("request apiKey after removal = %q, want the resolver gone", got)
	}
}
