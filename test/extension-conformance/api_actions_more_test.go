package extensionconformance

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/extensiontest"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func (a runtimeBackedAPI) RegisterProvider(name string, config extension.ProviderConfig) {
	_ = a.runtime.RegisterProvider(name, config, "api-reference")
}

func (a runtimeBackedAPI) UnregisterProvider(name string) {
	a.runtime.UnregisterProvider(name, "api-reference")
}

func (a runtimeBackedAPI) RegisterMcpServer(name string, config extension.McpServerConfig) error {
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return a.runtime.RegisterMcpServer("api-reference", name, raw)
}

func (a runtimeBackedAPI) UnregisterMcpServer(name string) {
	a.runtime.UnregisterMcpServer("api-reference", name)
}

func (a runtimeBackedAPI) GetMcpServers() []extension.RegisteredMcpServer {
	return a.runtime.McpServers().List()
}

// TestConformanceAPIProviderRegistrationQueuesUntilBound is the Go reference for pi.registerProvider and pi.unregisterProvider (extensions/types.ts:1820-1835, loader.ts:213-221, runner.ts:481-490,534): a provider registered while extensions load waits for the host's bind and an unregister removes it from the queue; after binding a registration or removal reaches the host's action at once.
//
// mutation-checked: the mutant "ExtensionRuntime.UnregisterProvider leaves the pending queue untouched" fails it.
func TestConformanceAPIProviderRegistrationQueuesUntilBound(t *testing.T) {
	t.Parallel()
	runner := inproc.NewRunner(nil, t.TempDir())
	var api extension.API = runtimeBackedAPI{Fake: &extensiontest.Fake{}, runtime: runner.Runtime()}
	api.RegisterProvider("kept", extension.ProviderConfig{BaseURL: "https://kept.example"})
	api.RegisterProvider("dropped", extension.ProviderConfig{BaseURL: "https://dropped.example"})
	api.UnregisterProvider("dropped")

	var calls []string
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, &extension.ProviderActions{
		RegisterProvider: func(name string, config extension.ProviderConfig) error {
			calls = append(calls, "+"+name+"@"+config.BaseURL)
			return nil
		},
		UnregisterProvider: func(name string) { calls = append(calls, "-"+name) },
	})
	api.RegisterProvider("late", extension.ProviderConfig{BaseURL: "https://late.example"})
	api.UnregisterProvider("kept")
	if want := []string{"+kept@https://kept.example", "+late@https://late.example", "-kept"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("host saw %v, want %v: the unregistered provider must leave the queue", calls, want)
	}
}

// TestConformanceAPIMcpServersRegisterAndUnregister is the Go reference for pi.registerMcpServer, pi.unregisterMcpServer and pi.getMcpServers (extensions/types.ts:1855-1861, loader.ts:464-497): a server an extension registers is listed with its config and extension path, a repeat registration by the same extension replaces it, an unregister removes it, and an invalid config is rejected.
//
// mutation-checked: the mutant "ExtensionRuntime.RegisterMcpServer never registers the server" fails it.
func TestConformanceAPIMcpServersRegisterAndUnregister(t *testing.T) {
	t.Parallel()
	runner := inproc.NewRunner(nil, t.TempDir())
	var api extension.API = runtimeBackedAPI{Fake: &extensiontest.Fake{}, runtime: runner.Runtime()}
	if err := api.RegisterMcpServer("jira", extension.McpServerConfig{Type: "http", URL: "https://mcp.example.com/jira"}); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterMcpServer("jira", extension.McpServerConfig{Type: "http", URL: "https://mcp.example.com/jira2"}); err != nil {
		t.Fatal(err)
	}
	servers := api.GetMcpServers()
	if len(servers) != 1 || servers[0].Name != "jira" || servers[0].ExtensionPath != "api-reference" || servers[0].Config.URL != "https://mcp.example.com/jira2" {
		t.Fatalf("servers %+v, want one jira server with the replacing config from api-reference", servers)
	}
	if err := api.RegisterMcpServer("broken", extension.McpServerConfig{Type: "http"}); err == nil {
		t.Fatal("a server without a url registered")
	}
	api.UnregisterMcpServer("jira")
	if servers := api.GetMcpServers(); len(servers) != 0 {
		t.Fatalf("servers after unregister %+v, want none", servers)
	}
	_ = context.Background
}

// busAPI is an extension.API whose events property is the in-memory bus the loader shares between extensions (loader.ts createExtensionAPI `events: eventBus`).
type busAPI struct {
	*extensiontest.Fake
	bus extension.EventBus
}

func (a busAPI) Events() extension.EventBus { return a.bus }

// TestConformanceAPIEventsBusDeliversInOrder is the Go reference for pi.events (extensions/types.ts:1650 `events: EventBus`, event-bus.ts:12-33): listeners of a channel run in registration order before emit returns, an unsubscribe removes only its listener and is idempotent, and a listener that panics neither reaches the emitter nor stops the listeners after it.
//
// mutation-checked: the mutant "the event bus does not recover a panicking listener" fails it.
func TestConformanceAPIEventsBusDeliversInOrder(t *testing.T) {
	t.Parallel()
	var api extension.API = busAPI{Fake: &extensiontest.Fake{}, bus: extension.CreateEventBus()}
	var seen []string
	unsubscribeFirst := api.Events().On("topic", func(data any) { seen = append(seen, "first:"+data.(string)) })
	api.Events().On("topic", func(any) { panic("listener failed") })
	api.Events().On("topic", func(data any) { seen = append(seen, "third:"+data.(string)) })
	api.Events().On("other", func(any) { seen = append(seen, "other") })

	api.Events().Emit("topic", "a")
	unsubscribeFirst()
	unsubscribeFirst()
	api.Events().Emit("topic", "b")

	if want := []string{"first:a", "third:a", "third:b"}; !reflect.DeepEqual(seen, want) {
		t.Fatalf("listeners saw %v, want %v", seen, want)
	}
}
