package inproc

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: runner.ts:666-670 setFlagValue/getFlagValues read and write runtime.flagValues, the map the loader fills with flag defaults and every runner sharing the runtime sees.
func TestRunnerFlagValuesLiveInTheSharedRuntime(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	runtime.FlagValues["preset"] = "from-loader"
	runner := NewRunner([]extension.Extension{{Flags: map[string]extension.ExtensionFlag{"mode": {Name: "mode", Type: "string", Default: "fast"}, "preset": {Name: "preset", Type: "string", Default: "ignored"}}}}, t.TempDir(), runtime)
	if runtime.FlagValues["mode"] != "fast" || runtime.FlagValues["preset"] != "from-loader" {
		t.Fatalf("defaults in the runtime = %#v", runtime.FlagValues)
	}
	runner.SetFlagValue("mode", "slow")
	if runtime.FlagValues["mode"] != "slow" {
		t.Fatalf("SetFlagValue did not reach the runtime: %#v", runtime.FlagValues)
	}
	if runner.GetFlagValues()["preset"] != "from-loader" {
		t.Fatalf("GetFlagValues = %#v", runner.GetFlagValues())
	}
}

// upstream: runner.ts:722-729 Runner.invalidate sets its own stale message and calls runtime.invalidate(message); loader.ts assertActive then reports it to every pi.* method.
func TestRunnerInvalidateInvalidatesTheRuntime(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	runner := NewRunner(nil, t.TempDir(), runtime)
	runner.Invalidate("replaced")
	if err := runtime.AssertActive(); err == nil || err.Error() != "replaced" {
		t.Fatalf("runtime AssertActive = %v, want the runner's message", err)
	}
	defaulted := extension.CreateExtensionRuntime()
	NewRunner(nil, t.TempDir(), defaulted).Invalidate("")
	if err := defaulted.AssertActive(); err == nil || err.Error() != extension.DefaultStaleRuntimeMessage {
		t.Fatalf("default message = %v", err)
	}
}

// A runtime invalidated by the loader or host makes the runner that shares it stale, as the API methods guarded by runtime.assertActive do.
func TestRunnerReportsARuntimeInvalidatedDirectly(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	runner := NewRunner(nil, t.TempDir(), runtime)
	if err := runner.assertActive(); err != nil {
		t.Fatal(err)
	}
	runtime.Invalidate("loader invalidated")
	var stale *StaleError
	if err := runner.assertActive(); !errors.As(err, &stale) || stale.Message != "loader invalidated" {
		t.Fatalf("assertActive = %v", err)
	}
}

type nativeRegistryStub struct {
	extension.ModelRegistry // the catalog members the runner never calls
	registered              []string
}

func (s *nativeRegistryStub) RegisterNativeProvider(_ context.Context, provider *extension.NativeProvider) error {
	s.registered = append(s.registered, provider.ID)
	return nil
}

// upstream: runner.ts:481-497 bindCore applies the native providers queued while extensions loaded to the model registry, and later registrations reach it at once.
func TestBindCoreFlushesQueuedNativeProvidersToTheModelRegistry(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	if err := runtime.RegisterNativeProvider(context.Background(), &ai.ModelsProvider{ID: "queued"}, "/ext/a"); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(nil, t.TempDir(), runtime)
	registry := &nativeRegistryStub{}
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{ModelRegistry: registry}, nil)
	if len(registry.registered) != 1 || registry.registered[0] != "queued" {
		t.Fatalf("flushed = %v", registry.registered)
	}
	if err := runtime.RegisterNativeProvider(context.Background(), &ai.ModelsProvider{ID: "after"}); err != nil {
		t.Fatal(err)
	}
	if len(registry.registered) != 2 || registry.registered[1] != "after" {
		t.Fatalf("after bind = %v", registry.registered)
	}
}

type providerRegistryStub struct {
	nativeRegistryStub
	providers   []string
	unregistred []string
}

func (s *providerRegistryStub) RegisterExtensionProvider(name string, _ extension.ProviderConfig) error {
	s.providers = append(s.providers, name)
	return nil
}
func (s *providerRegistryStub) UnregisterProvider(name string) {
	s.unregistred = append(s.unregistred, name)
}

// A host binds its own provider actions to the shared runtime (cmd/pig wraps the registry calls so each registration starts the registry refresh). Runner.BindCore on that runtime composes with them: the model registry never replaces a host-bound action, so the host's work after each registration still runs.
func TestBindCoreKeepsTheActionsAHostBoundToTheSharedRuntime(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	var hostCalls []string
	runtime.BindProviderActions(extension.ProviderActions{
		RegisterProvider: func(name string, _ extension.ProviderConfig) error {
			hostCalls = append(hostCalls, "register:"+name)
			return nil
		},
		UnregisterProvider: func(name string) { hostCalls = append(hostCalls, "unregister:"+name) },
		RegisterNativeProviderCarrier: func(_ context.Context, p *extension.NativeProvider) error {
			hostCalls = append(hostCalls, "native:"+p.ID)
			return nil
		},
	}, nil)
	runner := NewRunner(nil, t.TempDir(), runtime)
	registry := &providerRegistryStub{}
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{ModelRegistry: registry}, nil)

	if err := runtime.RegisterNativeProvider(context.Background(), &ai.ModelsProvider{ID: "n"}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterProvider("p", extension.ProviderConfig{}); err != nil {
		t.Fatal(err)
	}
	runtime.UnregisterProvider("p")
	want := []string{"native:n", "register:p", "unregister:p"}
	if len(hostCalls) != len(want) || hostCalls[0] != want[0] || hostCalls[1] != want[1] || hostCalls[2] != want[2] {
		t.Fatalf("host-bound actions ran %v, want %v", hostCalls, want)
	}
	if len(registry.registered)+len(registry.providers)+len(registry.unregistred) != 0 {
		t.Fatalf("the registry replaced the host's actions: %+v", registry)
	}
}

// An explicit providerActions entry still wins over a host-bound action, as upstream's providerActions win over the model registry (runner.ts:481-490).
func TestBindCoreExplicitProviderActionsWinOverTheHostsAndTheRegistrys(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	runtime.BindProviderActions(extension.ProviderActions{RegisterNativeProviderCarrier: func(context.Context, *extension.NativeProvider) error { t.Error("host action ran"); return nil }}, nil)
	runner := NewRunner(nil, t.TempDir(), runtime)
	var explicit []string
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{ModelRegistry: &providerRegistryStub{}}, &extension.ProviderActions{
		RegisterNativeProvider: func(_ context.Context, p *ai.ModelsProvider) error {
			explicit = append(explicit, p.ID)
			return nil
		},
	})
	if err := runtime.RegisterNativeProvider(context.Background(), &ai.ModelsProvider{ID: "x"}); err != nil {
		t.Fatal(err)
	}
	if len(explicit) != 1 || explicit[0] != "x" {
		t.Fatalf("explicit = %v", explicit)
	}
}

// upstream: runner.ts:410-438 bindCore copies the ExtensionActions into runtime, and a context reads them through runtime at call time: a later bind is seen by a context created before it.
func TestBindCoreInstallsExtensionActionsIntoTheRuntime(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	runner := NewRunner(nil, t.TempDir(), runtime)
	runner.BindCore(extension.ExtensionActions{
		GetActiveTools: func() []string { return []string{"first"} },
		AppendEntry:    func(string, any) error { return nil },
	}, extension.ContextActions{}, nil)
	if got := runtime.GetActiveTools(); len(got) != 1 || got[0] != "first" {
		t.Fatalf("runtime.GetActiveTools = %v", got)
	}
	ctx, err := runtime.CreateContext()
	if err != nil {
		t.Fatal(err)
	}
	if got := ctx.GetActiveTools(); len(got) != 1 || got[0] != "first" {
		t.Fatalf("ctx.GetActiveTools = %v", got)
	}
	runtime.GetActiveTools = func() []string { return []string{"second"} }
	if got := ctx.GetActiveTools(); len(got) != 1 || got[0] != "second" {
		t.Fatalf("ctx.GetActiveTools after rebinding the runtime handler = %v, want the runtime's current handler", got)
	}
}

// upstream: runner.ts:410-438 bindCore copies every ExtensionActions handler into the shared runtime (runtime.sendMessage, setLabel, getCommands, setModel, getThinkingLevel, setThinkingLevel, ...). Each handler must receive the call's arguments and its result must reach the caller.
func TestBindCoreRuntimeHandlersForwardArgumentsAndResults(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	runner := NewRunner(nil, t.TempDir(), runtime)
	var gotMessage extension.CustomMessageRef
	var gotOptions *extension.SendMessageOptions
	var gotEntry string
	var gotLabel *string
	var gotLevel extension.ThinkingLevel
	var gotModel extension.Model
	errBoom := errors.New("boom")
	runner.BindCore(extension.ExtensionActions{
		SendMessage: func(message extension.CustomMessageRef, options *extension.SendMessageOptions) error {
			gotMessage, gotOptions = message, options
			return errBoom
		},
		SetLabel: func(entryID string, label *string) error { gotEntry, gotLabel = entryID, label; return nil },
		GetCommands: func() []extension.SlashCommandInfo {
			return []extension.SlashCommandInfo{{Name: "deploy", Source: "extension"}}
		},
		SetModel:         func(_ context.Context, model extension.Model) (bool, error) { gotModel = model; return false, nil },
		GetThinkingLevel: func() extension.ThinkingLevel { return "high" },
		SetThinkingLevel: func(level extension.ThinkingLevel) { gotLevel = level },
	}, extension.ContextActions{}, nil)

	trigger := true
	if err := runtime.SendMessage(extension.CustomMessageRef{CustomType: "note"}, &extension.SendMessageOptions{TriggerTurn: &trigger}); !errors.Is(err, errBoom) {
		t.Fatalf("SendMessage error = %v, want the handler's", err)
	}
	if gotMessage.CustomType != "note" || gotOptions == nil || gotOptions.TriggerTurn == nil || !*gotOptions.TriggerTurn {
		t.Fatalf("SendMessage received %+v %+v", gotMessage, gotOptions)
	}
	label := "milestone"
	if err := runtime.SetLabel("entry-7", &label); err != nil || gotEntry != "entry-7" || gotLabel == nil || *gotLabel != "milestone" {
		t.Fatalf("SetLabel err=%v entry=%q label=%v", err, gotEntry, gotLabel)
	}
	if err := runtime.SetLabel("entry-7", nil); err != nil || gotLabel != nil {
		t.Fatalf("SetLabel(nil) must clear the label: err=%v label=%v", err, gotLabel)
	}
	if got := runtime.GetCommands(); len(got) != 1 || got[0].Name != "deploy" {
		t.Fatalf("GetCommands = %+v", got)
	}
	modelX := &ai.Model{ID: "model-x"}
	if ok, err := runtime.SetModel(t.Context(), modelX); ok || err != nil || gotModel != modelX {
		t.Fatalf("SetModel ok=%v err=%v model=%v, want the handler's false result and the model", ok, err, gotModel)
	}
	if got := runtime.GetThinkingLevel(); got != "high" {
		t.Fatalf("GetThinkingLevel = %q", got)
	}
	runtime.SetThinkingLevel("low")
	if gotLevel != "low" {
		t.Fatalf("SetThinkingLevel received %q", gotLevel)
	}
}
