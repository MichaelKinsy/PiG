package extension

import (
	"context"
	"errors"
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
