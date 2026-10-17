package extension

import (
	"errors"
	"slices"
	"testing"
)

type stackedError struct{ stack string }

func (e stackedError) Error() string      { return "stacked" }
func (e stackedError) ErrorStack() string { return e.stack }

// runner.ts:265-294 bindCore and the loader's pre-bind queues: ReportError reaches the error listener the runner installed, reports whether one is installed,
// and the queue read hands out a copy.
// mutation-checked: dropping the stored reporter, the reported flag or the copy fails.
func TestExtensionRuntimeReportsToTheBoundRunnerAndCopiesItsQueue(t *testing.T) {
	runtime := CreateExtensionRuntime()
	if runtime.ReportError(&ExtensionError{Error: "early"}) {
		t.Fatal("ReportError claimed delivery before a runner bound")
	}
	var reported []string
	runtime.SetErrorReporter(func(err *ExtensionError) { reported = append(reported, err.Error) })
	if !runtime.ReportError(&ExtensionError{Error: "late"}) || !slices.Equal(reported, []string{"late"}) {
		t.Fatalf("reported %v, want the bound reporter to receive late", reported)
	}

	if err := runtime.RegisterProvider("p", ProviderConfig{}, "p.ts"); err != nil {
		t.Fatal(err)
	}
	pending := runtime.PendingProviderRegistrations()
	pending[0].Name = "changed"
	if got := runtime.PendingProviderRegistrations(); len(got) != 1 || got[0].Name != "p" || got[0].ExtensionPath != "p.ts" {
		t.Fatalf("the queue changed through the returned slice: %+v", got)
	}
}

// runner.ts:497-541 bindCore flushes every queue, falling back to the model registry for an action the caller did not give. Pig's runtime is bound by
// more than one host, and each binder supplies the registry fallback itself (inproc runner.go, services_extension_runtime.go), so BindProviderActions
// flushes only the queues it has actions for: a provider-only bind leaves queued virtual models pending, and a virtual-model-only bind leaves queued
// providers pending; the bound actions then include the unregister half that was bound, and an unregister after binding goes to the registry.
// mutation-checked: binding a queue whose action is nil, not installing the unregister action or returning no bound actions fails.
func TestExtensionRuntimeBindsOnlyTheQueuesItHasActionsForAndKeepsTheBoundActions(t *testing.T) {
	runtime := CreateExtensionRuntime()
	if err := runtime.RegisterProvider("p", ProviderConfig{}); err != nil {
		t.Fatal(err)
	}
	runtime.pendingVirtualModelRegistrations.entries = append(runtime.pendingVirtualModelRegistrations.entries, PendingVirtualModelRegistration{ExtensionPath: "v.ts"})

	var registered, unregistered []string
	runtime.BindProviderActions(ProviderActions{
		RegisterProvider:   func(name string, _ ProviderConfig) error { registered = append(registered, name); return nil },
		UnregisterProvider: func(name string) { unregistered = append(unregistered, name) },
	}, nil)
	if !slices.Equal(registered, []string{"p"}) || len(runtime.pendingVirtualModelRegistrations.entries) != 1 {
		t.Fatalf("provider-only bind registered %v and left %d virtual models queued, want [p] and 1", registered, len(runtime.pendingVirtualModelRegistrations.entries))
	}
	bound := runtime.BoundProviderActions()
	if bound.RegisterProvider == nil || bound.UnregisterProvider == nil || bound.RegisterVirtualModel != nil {
		t.Fatalf("bound actions = %+v, want the provider pair only", bound)
	}
	runtime.UnregisterProvider("p")
	if !slices.Equal(unregistered, []string{"p"}) {
		t.Fatalf("post-bind unregister reached the registry as %v, want [p]", unregistered)
	}
	// Before any unregister action is bound, an unregister after binding is a no-op, not a call through nil.
	partial := CreateExtensionRuntime()
	partial.BindProviderActions(ProviderActions{RegisterProvider: func(string, ProviderConfig) error { return nil }}, nil)
	partial.UnregisterProvider("p")

	vm := CreateExtensionRuntime()
	if err := vm.RegisterProvider("q", ProviderConfig{}); err != nil {
		t.Fatal(err)
	}
	var virtuals int
	vm.BindProviderActions(ProviderActions{RegisterVirtualModel: func(VirtualModelDefinition) error { virtuals++; return nil }}, nil)
	if len(vm.PendingProviderRegistrations()) != 1 || vm.BoundProviderActions().RegisterVirtualModel == nil {
		t.Fatalf("virtual-model-only bind: %d providers still queued (want 1), virtual action bound %v", len(vm.PendingProviderRegistrations()), vm.BoundProviderActions().RegisterVirtualModel != nil)
	}
}

// runner.ts:497-541: a registration that fails during the bind is reported with the extension path, the event name and the error's stack, and a bind without a
// reporter still completes the queue.
// mutation-checked: a nil reporter called anyway, a missing stack, or a wrong event/path fails.
func TestExtensionRuntimeBindReportsFailuresWithTheirStackAndSurvivesNoReporter(t *testing.T) {
	failure := stackedError{stack: "at register (p.ts:1)"}
	runtime := CreateExtensionRuntime()
	if err := runtime.RegisterProvider("p", ProviderConfig{}, "p.ts"); err != nil {
		t.Fatal(err)
	}
	runtime.pendingVirtualModelRegistrations.entries = append(runtime.pendingVirtualModelRegistrations.entries, PendingVirtualModelRegistration{ExtensionPath: "v.ts"})
	var reports []ExtensionError
	runtime.BindProviderActions(ProviderActions{
		RegisterProvider:     func(string, ProviderConfig) error { return failure },
		RegisterVirtualModel: func(VirtualModelDefinition) error { return failure },
	}, func(err *ExtensionError) { reports = append(reports, *err) })
	want := []ExtensionError{
		{ExtensionPath: "p.ts", Event: "register_provider", Error: "stacked", Stack: "at register (p.ts:1)"},
		{ExtensionPath: "v.ts", Event: "register_virtual_model", Error: "stacked", Stack: "at register (p.ts:1)"},
	}
	if !slices.Equal(reports, want) {
		t.Fatalf("reports = %+v, want %+v", reports, want)
	}

	silent := CreateExtensionRuntime()
	if err := silent.RegisterProvider("p", ProviderConfig{}); err != nil {
		t.Fatal(err)
	}
	silent.pendingVirtualModelRegistrations.entries = append(silent.pendingVirtualModelRegistrations.entries, PendingVirtualModelRegistration{})
	silent.BindProviderActions(ProviderActions{
		RegisterProvider:     func(string, ProviderConfig) error { return errors.New("no reporter") },
		RegisterVirtualModel: func(VirtualModelDefinition) error { return errors.New("no reporter") },
	}, nil)
	if len(silent.PendingProviderRegistrations()) != 0 || len(silent.pendingVirtualModelRegistrations.entries) != 0 {
		t.Fatal("a bind with failing registrations and no reporter left entries queued")
	}
}
