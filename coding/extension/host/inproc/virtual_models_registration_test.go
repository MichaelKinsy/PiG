package inproc_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func virtualModel(provider, id string) extension.VirtualModelDefinition {
	return extension.VirtualModelDefinition{Provider: provider, ID: id, Name: id}
}

func virtualModelIDs(entries []extension.PendingVirtualModelRegistration) []string {
	out := make([]string, len(entries))
	for i, entry := range entries {
		out[i] = entry.ExtensionPath + ":" + entry.Definition.Provider + "/" + entry.Definition.ID
	}
	return out
}

// Upstream loader.ts:225-233: registrations queue while extensions load, and unregisterVirtualModel filters the queue by provider and id.
func TestRuntimeQueuesVirtualModelsUntilBound(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	for _, entry := range []struct{ path, provider, id string }{
		{"/ext/a.ts", "router", "auto"},
		{"/ext/b.ts", "anthropic", "smart"},
		{"/ext/a.ts", "router", "auto"}, // The queue keeps repeated registrations; the registry replaces at bind.
		{"/ext/a.ts", "router", "cheap"},
	} {
		if err := runtime.RegisterVirtualModel(virtualModel(entry.provider, entry.id), entry.path); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"/ext/a.ts:router/auto", "/ext/b.ts:anthropic/smart", "/ext/a.ts:router/auto", "/ext/a.ts:router/cheap"}
	if got := virtualModelIDs(runtime.PendingVirtualModelRegistrations()); !reflect.DeepEqual(got, want) {
		t.Fatalf("pending = %v, want %v", got, want)
	}
	runtime.UnregisterVirtualModel("router", "auto")
	want = []string{"/ext/b.ts:anthropic/smart", "/ext/a.ts:router/cheap"}
	if got := virtualModelIDs(runtime.PendingVirtualModelRegistrations()); !reflect.DeepEqual(got, want) {
		t.Fatalf("pending after unregister = %v, want %v", got, want)
	}
}

// Upstream runner.ts:497-541: bindCore flushes the queue in order, reports a failed registration as a `register_virtual_model` extension error without stopping, clears the queue, and applies later calls immediately.
func TestRunnerBindFlushesVirtualModelsAndAppliesLaterCallsImmediately(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	for _, id := range []string{"first", "broken", "third"} {
		if err := runtime.RegisterVirtualModel(virtualModel("router", id), "/ext/"+id+".ts"); err != nil {
			t.Fatal(err)
		}
	}
	sentinel := errors.New("Virtual model router/broken collides with a physical model")
	var registered, removed []string
	var reported []extension.ExtensionError
	runner := inproc.NewRunner(nil, t.TempDir(), runtime)
	runner.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, *err) })
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, &extension.ProviderActions{
		RegisterProvider: func(string, extension.ProviderConfig) error { return nil },
		RegisterVirtualModel: func(definition extension.VirtualModelDefinition) error {
			registered = append(registered, definition.Provider+"/"+definition.ID)
			if definition.ID == "broken" {
				return sentinel
			}
			return nil
		},
		UnregisterVirtualModel: func(provider, id string) { removed = append(removed, provider+"/"+id) },
	})
	if want := []string{"router/first", "router/broken", "router/third"}; !reflect.DeepEqual(registered, want) {
		t.Fatalf("flushed %v, want %v", registered, want)
	}
	want := extension.ExtensionError{ExtensionPath: "/ext/broken.ts", Event: "register_virtual_model", Error: sentinel.Error()}
	if len(reported) != 1 || reported[0].ExtensionPath != want.ExtensionPath || reported[0].Event != want.Event || reported[0].Error != want.Error {
		t.Fatalf("reported = %+v, want %+v", reported, want)
	}
	if left := runtime.PendingVirtualModelRegistrations(); len(left) != 0 {
		t.Fatalf("the queue was not cleared: %v", virtualModelIDs(left))
	}

	if err := runtime.RegisterVirtualModel(virtualModel("router", "late"), "/ext/late.ts"); err != nil {
		t.Fatal(err)
	}
	runtime.UnregisterVirtualModel("router", "late")
	if want := []string{"router/first", "router/broken", "router/third", "router/late"}; !reflect.DeepEqual(registered, want) {
		t.Fatalf("registered = %v, want %v", registered, want)
	}
	if want := []string{"router/late"}; !reflect.DeepEqual(removed, want) {
		t.Fatalf("removed = %v, want %v", removed, want)
	}
}

// Upstream loader.ts:157-159, 190 and runner.ts:436: the runtime's createContext throws while extensions load and returns a fresh extension context once the runner binds.
func TestRuntimeCreateContextFailsUntilTheRunnerBinds(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	_, err := runtime.CreateContext()
	if err == nil || err.Error() != "Extension runtime not initialized. Action methods cannot be called during extension loading." {
		t.Fatalf("CreateContext before bind = %v", err)
	}
	cwd := t.TempDir()
	runner := inproc.NewRunner(nil, cwd, runtime)
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, &extension.ProviderActions{RegisterProvider: func(string, extension.ProviderConfig) error { return nil }})
	ctx, err := runtime.CreateContext()
	if err != nil || ctx == nil {
		t.Fatalf("CreateContext after bind = %v, %v", ctx, err)
	}
	if got, err := ctx.CWD(); err != nil || got != cwd {
		t.Fatalf("context cwd = %q, %v; want %q", got, err, cwd)
	}
	other, _ := runtime.CreateContext()
	if other == ctx {
		t.Fatal("CreateContext returned a shared context; upstream returns a fresh object each call")
	}
}
