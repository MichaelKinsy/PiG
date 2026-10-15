package coding

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi: agent-session-services.ts:96-124 (applyExtensionFlagValues writes extensionsResult.runtime.flagValues) and :157-193 (createAgentSessionServices
// drains the runtime's pending provider registrations into the ModelRuntime, reports a failed one as `Extension "<path>" error: <message>`, and refreshes
// locally) after resourceLoader.reload. extensions/types.ts:2260 LoadExtensionsResult.runtime is that shared runtime.
// mutation-checked: skipping the drain, the diagnostic path, the local refresh or the flag-value write fails it.
func TestCreateAgentSessionServicesDrainsTheLoadedExtensionRuntime(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	model := extension.ProviderModelConfig{ID: "queued-model", Name: "Queued", API: ai.APIOpenAICompletions, ContextWindow: 1000, MaxTokens: 100}
	if err := runtime.RegisterProvider("queued", extension.ProviderConfig{BaseURL: "https://queued.invalid", APIKey: "key", API: ai.APIOpenAICompletions, Models: []extension.ProviderModelConfig{model}}, "/ext/good.ts"); err != nil {
		t.Fatal(err)
	}
	// Custom models need a baseUrl: this registration fails and is reported, the others still apply.
	if err := runtime.RegisterProvider("broken", extension.ProviderConfig{APIKey: "key", API: ai.APIOpenAICompletions, Models: []extension.ProviderModelConfig{model}}, "/ext/bad.ts"); err != nil {
		t.Fatal(err)
	}
	virtual := extension.VirtualModelDefinition{Provider: "queued", ID: "queued-virtual", Name: "Queued virtual", Route: func(context.Context, extension.ModelRouteRequest) (extension.ModelRoute, error) {
		return extension.ModelRoute{}, nil
	}}
	if err := runtime.RegisterVirtualModel(virtual, "/ext/good.ts"); err != nil {
		t.Fatal(err)
	}
	// agent-session-services.ts:170-181 drains the pending native provider registrations too, between the provider and the virtual model ones.
	native := &extension.NativeProvider{ID: "queued-native", Name: "Queued native", Models: []extension.ProviderModelConfig{{ID: "native-model", API: ai.APIOpenAICompletions, BaseURL: "https://native.invalid"}},
		CheckAuth: func(context.Context, *ai.Credential) (*ai.AuthCheck, error) {
			return &ai.AuthCheck{Type: ai.CredentialAPIKey}, nil
		},
		ResolveAuth: func(context.Context, *ai.Credential, ai.AuthResolutionOverrides) (*ai.AuthResult, *ai.Credential, error) {
			return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "key"}}, nil, nil
		},
		ResolveRefreshCredential: func(context.Context, *ai.Credential) (*ai.Credential, *ai.Credential, error) { return nil, nil, nil },
		Stream: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			return ai.NewAssistantMessageEventStream(), nil
		},
	}
	if err := runtime.RegisterNativeProviderCarrier(context.Background(), native, "/ext/good.ts"); err != nil {
		t.Fatal(err)
	}
	loaded := LoadExtensionsResult{
		Runtime:    runtime,
		Extensions: []extension.Extension{flagExtension(extension.ExtensionFlag{Name: "plan", Type: extension.FlagBoolean}, extension.ExtensionFlag{Name: "mode", Type: extension.FlagString})},
	}
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{
		CWD: t.TempDir(), AgentDir: t.TempDir(),
		ResourceLoaderOptions: &DefaultResourceLoaderOptions{LoadExtensions: func(context.Context, ExtensionLoadRequest) (LoadExtensionsResult, error) { return loaded, nil }},
		ExtensionFlagValues:   []ExtensionFlagValue{{Name: "plan", Value: true}, {Name: "mode", Value: "fast"}, {Name: "nope", Value: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)

	if pending := runtime.PendingProviderRegistrations(); len(pending) != 0 {
		t.Fatalf("pending provider registrations after create = %v, want drained", pending)
	}
	found := false
	for _, entry := range services.registry.ModelRegistry.GetAll() {
		if entry.ProviderID == "queued" && entry.ModelID == "queued-model" {
			found = true
		}
	}
	if !found {
		t.Fatal("the queued provider's model is not in the registry: the registrations were not applied before the Session")
	}
	if services.Registry().NativeProvider("queued-native") == nil || len(runtime.PendingNativeProviderRegistrations()) != 0 {
		t.Fatal("the queued native provider was not applied and drained")
	}
	if services.ModelRuntime().GetModel("queued", "queued-virtual") == nil || len(runtime.PendingVirtualModelRegistrations()) != 0 {
		t.Fatal("the queued virtual model was not applied and drained")
	}
	// The awaited local refresh (agent-session-services.ts:194) leaves the registered, keyed provider's model in the availability snapshot.
	if !slices.ContainsFunc(services.ModelRuntime().GetAvailableSnapshot(), func(m *ai.Model) bool { return m.Provider.ID() == "queued" && m.ID == "queued-model" }) {
		t.Fatal("the availability snapshot does not list the queued provider's model after the local refresh")
	}
	var messages []string
	for _, d := range services.Diagnostics() {
		messages = append(messages, d.Message)
	}
	if len(messages) != 2 || !strings.HasPrefix(messages[0], `Extension "/ext/bad.ts" error: `) || messages[1] != "Unknown option: --nope" {
		t.Fatalf("diagnostics = %q, want the failed registration then the unknown flag", messages)
	}
	if runtime.FlagValues["plan"] != true || runtime.FlagValues["mode"] != "fast" || len(runtime.FlagValues) != 2 {
		t.Fatalf("runtime flag values = %v, want plan=true mode=fast", runtime.FlagValues)
	}
}
