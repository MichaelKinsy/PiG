package subprocess

import (
	"encoding/json"
	"maps"
	"net"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

type queuedProvider struct {
	name string
	cfg  extension.ProviderConfig
}

// loadAndApplyProviders loads ext in process and returns the provider registrations the host hands the registry, in the order it applies them.
func loadAndApplyProviders(t *testing.T, ext *sdk.Extension) []queuedProvider {
	t.Helper()
	var got []queuedProvider
	host := NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	host.SetProviderCallbacks(func(name string, c extension.ProviderConfig) error {
		got = append(got, queuedProvider{name, c})
		return nil
	}, func(string) {})
	if _, err := host.LoadInProcess(t.Context(), ExtConfig{Name: "order", Path: "/ext/order.go", Enabled: true}, func(conn net.Conn) error { return ext.RunWithConn(conn) }); err != nil {
		t.Fatal(err)
	}
	applyQueuedRegistrations(host)
	return got
}

func providerModel(id string) map[string]any {
	return map[string]any{"id": id, "name": id, "reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 1000, "maxTokens": 100}
}

// Pi pushes each registerProvider call of the load onto an array and unregisterProvider filters the name out (loader.ts:221-231), so after other, dyn A, unregister dyn, dyn B, dyn c the registry sees [other, dyn B, dyn c] in that order (runner.ts:471-487, agent-session-services.ts:158-169). A host that kept only the last declaration of a name saw [other, dyn c].
func TestEveryProviderDeclarationOfANameIsAppliedInOrder(t *testing.T) {
	ext := sdk.New("order")
	ext.RegisterProvider("other", sdk.ProviderConfig{"api": "other-api", "baseUrl": "https://other.test", "apiKey": "k", "models": []any{providerModel("o")}})
	ext.RegisterProvider("dyn", sdk.ProviderConfig{"api": "dyn-api", "baseUrl": "https://a.test", "apiKey": "k", "models": []any{providerModel("a")}})
	ext.UnregisterProvider("dyn")
	ext.RegisterProvider("dyn", sdk.ProviderConfig{"api": "dyn-api", "baseUrl": "https://b.test", "apiKey": "k", "models": []any{providerModel("b")}})
	ext.RegisterProvider("dyn", sdk.ProviderConfig{"api": "dyn-api", "baseUrl": "https://c.test", "apiKey": "k", "models": []any{providerModel("c")}})
	got := loadAndApplyProviders(t, ext)
	var names, urls []string
	for _, p := range got {
		names = append(names, p.name)
		urls = append(urls, p.cfg.BaseURL)
	}
	if want := []string{"other", "dyn", "dyn"}; !slices.Equal(names, want) {
		t.Fatalf("applied %v, want %v", names, want)
	}
	if want := []string{"https://other.test", "https://b.test", "https://c.test"}; !slices.Equal(urls, want) {
		t.Fatalf("applied base URLs %v, want %v", urls, want)
	}
}

// Two declarations of one name reach the registry as two registrations; the registry merge (model-runtime.ts:965-986) lets the later defined fields override the earlier and keeps the earlier values of the fields the later one leaves undefined.
func TestTwoDeclarationsOfANameMergeLaterDefinedFieldsOverEarlier(t *testing.T) {
	ext := sdk.New("order")
	ext.RegisterProvider("dyn", sdk.ProviderConfig{"api": "dyn-api", "baseUrl": "https://one.test", "apiKey": "key-one", "models": []any{providerModel("m")}})
	ext.RegisterProvider("dyn", sdk.ProviderConfig{"baseUrl": "https://two.test"})
	got := loadAndApplyProviders(t, ext)
	if len(got) != 2 || got[0].name != "dyn" || got[1].name != "dyn" {
		t.Fatalf("applied %d registrations %v, want two of dyn", len(got), got)
	}
	// Merge as the registry does: a defined value of the later registration replaces the earlier one, an undefined one (omitted on the wire) keeps it.
	merged := map[string]any{}
	for _, p := range got {
		raw, err := json.Marshal(p.cfg)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		maps.Copy(merged, fields)
	}
	if merged["baseUrl"] != "https://two.test" {
		t.Fatalf("baseUrl = %v, want the later https://two.test", merged["baseUrl"])
	}
	if merged["api"] != "dyn-api" || merged["apiKey"] != "key-one" {
		t.Fatalf("api = %v, apiKey = %v, want the earlier dyn-api and key-one kept", merged["api"], merged["apiKey"])
	}
	if models, _ := merged["models"].([]any); len(models) != 1 {
		t.Fatalf("models = %v, want the earlier one model kept", merged["models"])
	}
}
