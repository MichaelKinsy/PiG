package extension

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: loader.ts:163-200 assertActive throws the stale message after invalidate; only the first invalidate sets it and an omitted message selects the default.
func TestRuntimeInvalidateMakesItStale(t *testing.T) {
	runtime := CreateExtensionRuntime()
	if err := runtime.AssertActive(); err != nil {
		t.Fatalf("fresh runtime is stale: %v", err)
	}
	runtime.Invalidate()
	if err := runtime.AssertActive(); err == nil || err.Error() != DefaultStaleRuntimeMessage {
		t.Fatalf("AssertActive = %v", err)
	}
	runtime.Invalidate("second message")
	if err := runtime.AssertActive(); err == nil || err.Error() != DefaultStaleRuntimeMessage {
		t.Fatalf("a second Invalidate replaced the message: %v", err)
	}
	custom := CreateExtensionRuntime()
	custom.Invalidate("custom stale")
	if err := custom.AssertActive(); err == nil || err.Error() != "custom stale" {
		t.Fatalf("AssertActive = %v", err)
	}
}

// upstream: loader.ts:217-225 registerNativeProvider queues {provider, extensionPath = "<unknown>"} until bindCore, and unregisterProvider(name) removes queued registrations whose provider id is name.
func TestRuntimeQueuesNativeProvidersUntilBound(t *testing.T) {
	runtime := CreateExtensionRuntime()
	for _, entry := range []struct {
		id, path string
	}{{"a", "/ext/a"}, {"b", ""}, {"a", "/ext/a2"}} {
		var err error
		if entry.path == "" {
			err = runtime.RegisterNativeProvider(context.Background(), &ai.ModelsProvider{ID: entry.id})
		} else {
			err = runtime.RegisterNativeProvider(context.Background(), &ai.ModelsProvider{ID: entry.id}, entry.path)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	pending := runtime.PendingNativeProviderRegistrations()
	if len(pending) != 3 || pending[1].ExtensionPath != "<unknown>" || pending[2].ExtensionPath != "/ext/a2" {
		t.Fatalf("pending = %+v", pending)
	}
	runtime.UnregisterProvider("a")
	pending = runtime.PendingNativeProviderRegistrations()
	if len(pending) != 1 || pending[0].Provider.ID != "b" {
		t.Fatalf("after unregister = %+v", pending)
	}
}

// upstream: runner.ts:481-497 bindCore applies each queued native provider in order, reports a failure under its extension path as a register_provider error and continues, empties the queue, and later registrations apply at once.
func TestBindProviderActionsDrainsTheNativeQueue(t *testing.T) {
	runtime := CreateExtensionRuntime()
	for _, id := range []string{"first", "bad", "last"} {
		if err := runtime.RegisterNativeProvider(context.Background(), &ai.ModelsProvider{ID: id}, "/ext/"+id); err != nil {
			t.Fatal(err)
		}
	}
	var applied []string
	var reported []*ExtensionError
	runtime.BindProviderActions(ProviderActions{RegisterNativeProviderCarrier: func(_ context.Context, provider *NativeProvider) error {
		applied = append(applied, provider.ID)
		if provider.ID == "bad" {
			return errors.New("refused")
		}
		return nil
	}}, func(err *ExtensionError) { reported = append(reported, err) })
	if len(applied) != 3 || applied[0] != "first" || applied[1] != "bad" || applied[2] != "last" {
		t.Fatalf("applied = %v", applied)
	}
	if len(reported) != 1 || reported[0].ExtensionPath != "/ext/bad" || reported[0].Event != "register_provider" || reported[0].Error != "refused" {
		t.Fatalf("reported = %+v", reported)
	}
	if got := len(runtime.PendingNativeProviderRegistrations()); got != 0 {
		t.Fatalf("queue after bind = %d", got)
	}
	if err := runtime.RegisterNativeProvider(context.Background(), &ai.ModelsProvider{ID: "later"}); err != nil {
		t.Fatal(err)
	}
	if applied[len(applied)-1] != "later" || len(runtime.PendingNativeProviderRegistrations()) != 0 {
		t.Fatalf("a registration after bind did not apply at once: %v", applied)
	}
}

// upstream: types.ts:2136 unregisterProvider(name, extensionPath?): the path is accepted and neither the queued nor the bound branch reads it (loader.ts:220, runner.ts:534).
func TestExtensionRuntimeUnregisterProviderIgnoresTheExtensionPath(t *testing.T) {
	runtime := CreateExtensionRuntime()
	if err := runtime.RegisterProvider("proxy", ProviderConfig{}, "/ext/a.ts"); err != nil {
		t.Fatal(err)
	}
	runtime.UnregisterProvider("proxy", "/some/other/path.ts")
	if got := len(runtime.PendingProviderRegistrations()); got != 0 {
		t.Fatalf("queued registrations after unregister = %d, want 0", got)
	}
}

// upstream: types.ts:2102-2104 the thinking-level handlers use the agent ThinkingLevel union, not an opaque value.
func TestExtensionActionsThinkingLevelIsTyped(t *testing.T) {
	var actions ExtensionActions
	acceptGetter := func(func() ai.ModelThinkingLevel) {}
	acceptSetter := func(func(ai.ModelThinkingLevel)) {}
	acceptGetter(actions.GetThinkingLevel)
	acceptSetter(actions.SetThinkingLevel)
}

// runner.ts:481-497 pendingNativeProviderRegistrations holds { provider: Provider; extensionPath }: the object a compiled-in extension registered is the
// queued object itself, and a subprocess extension's provider is exposed as the object assembled from its carrier, with the carrier's members.
func TestPendingNativeProviderRegistrationsExposeTheProviderObject(t *testing.T) {
	runtime := CreateExtensionRuntime()
	object := &ai.ModelsProvider{ID: "compiled-in", Name: "Compiled in", Headers: ai.ProviderHeaders{"X-A": new("a")}}
	if err := runtime.RegisterNativeProvider(context.Background(), object, "/ext/object"); err != nil {
		t.Fatal(err)
	}
	carrier := &NativeProvider{ID: "subprocess", Name: "Subprocess", BaseURL: "https://sub.test", Headers: ai.ProviderHeaders{"X-B": new("b")}}
	if err := runtime.RegisterNativeProviderCarrier(context.Background(), carrier, "/ext/carrier"); err != nil {
		t.Fatal(err)
	}
	pending := runtime.PendingNativeProviderRegistrations()
	if len(pending) != 2 || pending[0].ExtensionPath != "/ext/object" || pending[1].ExtensionPath != "/ext/carrier" {
		t.Fatalf("pending = %+v", pending)
	}
	if pending[0].Provider != object {
		t.Fatalf("the queued object is %p, want the registered %p", pending[0].Provider, object)
	}
	got := pending[1].Provider
	if got.ID != "subprocess" || got.Name != "Subprocess" || got.BaseURL != "https://sub.test" || got.Headers["X-B"] == nil || *got.Headers["X-B"] != "b" {
		t.Fatalf("object assembled from the carrier = %+v", got)
	}
}

// upstream: loader.ts:219-221 registerProvider queues {name, config, extensionPath = "<unknown>"} when no path is given, and keeps a given one; each call appends,
// so two registrations of one name are both queued (the runner applies them in order).
// mutation-checked: a default of "" fails it.
func TestRuntimeQueuesLegacyProviderRegistrationsWithTheDefaultExtensionPath(t *testing.T) {
	runtime := CreateExtensionRuntime()
	if err := runtime.RegisterProvider("anon", ProviderConfig{BaseURL: "https://a.invalid"}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterProvider("named", ProviderConfig{BaseURL: "https://b.invalid"}, "/ext/named"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterProvider("anon", ProviderConfig{BaseURL: "https://c.invalid"}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, entry := range runtime.PendingProviderRegistrations() {
		got = append(got, entry.Name+"@"+entry.ExtensionPath+"@"+entry.Config.BaseURL)
	}
	want := []string{"anon@<unknown>@https://a.invalid", "named@/ext/named@https://b.invalid", "anon@<unknown>@https://c.invalid"}
	if !slices.Equal(got, want) {
		t.Fatalf("pending = %q, want %q", got, want)
	}
}

// upstream: loader.ts:538-540 `if (!runtime.flagValues.has(name)) runtime.flagValues.set(name, value)`: a flag's registered default never replaces a value
// the command line already set (even a falsy one), and fills a flag that has none. FlagValue reports presence, as flagValues.has/get.
// mutation-checked: an unconditional set, and a lookup of the wrong name, each fail it.
func TestRuntimeFlagDefaultsNeverReplaceACommandLineValue(t *testing.T) {
	runtime := CreateExtensionRuntime()
	runtime.FlagValues["from-cli"] = "cli"
	runtime.FlagValues["falsy-cli"] = false
	runtime.SetFlagDefault("from-cli", "default")
	runtime.SetFlagDefault("falsy-cli", true)
	runtime.SetFlagDefault("fresh", "default")
	for name, want := range map[string]any{"from-cli": "cli", "falsy-cli": false, "fresh": "default"} {
		if got, ok := runtime.FlagValue(name); !ok || got != want {
			t.Errorf("FlagValue(%q) = %v, %v; want %v, true", name, got, ok, want)
		}
	}
	if got, ok := runtime.FlagValue("absent"); ok || got != nil {
		t.Errorf("FlagValue(absent) = %v, %v; want nil, false", got, ok)
	}
}

// upstream: loader.ts:193-199 invalidate(message) stores `message ?? <default text>`; Go has no undefined, so Invalidate() and Invalidate("") both select
// the default and a non-empty message is kept.
// mutation-checked: treating "" as a message (a non-stale runtime) fails it.
func TestRuntimeInvalidateWithAnEmptyMessageSelectsTheDefault(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*ExtensionRuntime)
		want   string
	}{
		{"none", func(r *ExtensionRuntime) { r.Invalidate() }, DefaultStaleRuntimeMessage},
		{"empty", func(r *ExtensionRuntime) { r.Invalidate("") }, DefaultStaleRuntimeMessage},
		{"message", func(r *ExtensionRuntime) { r.Invalidate("closed") }, "closed"},
	} {
		runtime := CreateExtensionRuntime()
		tc.invoke(runtime)
		if err := runtime.AssertActive(); err == nil || err.Error() != tc.want {
			t.Errorf("%s: AssertActive = %v, want %q", tc.name, err, tc.want)
		}
	}
}

// upstream: loader.ts:229-233 unregisterVirtualModel keeps every queued registration whose provider differs OR whose id differs: only the entry matching both
// leaves the queue, so the same id under another provider and another id under the same provider stay.
// mutation-checked: matching on the provider alone, or on the id alone, fails it.
func TestRuntimeUnregisterVirtualModelRemovesOnlyTheProviderAndIDPair(t *testing.T) {
	runtime := CreateExtensionRuntime()
	for _, pair := range [][2]string{{"p1", "a"}, {"p2", "a"}, {"p1", "b"}} {
		if err := runtime.RegisterVirtualModel(VirtualModelDefinition{Provider: pair[0], ID: pair[1]}, "/ext"); err != nil {
			t.Fatal(err)
		}
	}
	runtime.UnregisterVirtualModel("p1", "a")
	var got []string
	for _, entry := range runtime.PendingVirtualModelRegistrations() {
		got = append(got, entry.Definition.Provider+"/"+entry.Definition.ID)
	}
	if want := []string{"p2/a", "p1/b"}; !slices.Equal(got, want) {
		t.Fatalf("queue after unregister = %q, want %q", got, want)
	}
}
