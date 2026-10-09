package factoryload_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

var providerConfig = extension.ProviderConfig{BaseURL: "https://provider.test/v1", APIKey: "provider-test-key"}

func pendingProviderNames(runtime *extension.ExtensionRuntime) []string {
	var names []string
	for _, pending := range runtime.PendingProviderRegistrations() {
		names = append(names, pending.Name)
	}
	return names
}

// panicMessage returns what call panics with, "" when it does not panic.
func panicMessage(call func()) (message string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			message = fmt.Sprint(recovered)
		}
	}()
	call()
	return ""
}

// TestFailedFactoryDiscardsRuntimeChangesAndDisablesItsAPI ports test/suite/regressions/8423-extension-factory-failure.test.ts
// ("discards runtime changes and disables the failed API") with the same inputs and expectations.
func TestFailedFactoryDiscardsRuntimeChangesAndDisablesItsAPI(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	bus := extension.CreateEventBus()
	var captured extension.API
	eventCalls := 0
	var flagDuringLoad any

	if _, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
		pi.RegisterProvider("working-provider", providerConfig)
		return nil
	}, ".", bus, runtime, "<working>"); err != nil {
		t.Fatal(err)
	}
	_, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
		captured = pi
		pi.Events().On("factory-failure", func(any) { eventCalls++ })
		pi.RegisterFlag("failed-flag", extension.FlagOptions{Type: extension.FlagBoolean, Default: true})
		flagDuringLoad = pi.GetFlag("failed-flag")
		pi.UnregisterProvider("working-provider")
		pi.RegisterProvider("failed-provider", providerConfig)
		return errors.New("factory failed")
	}, ".", bus, runtime, "<failing>")
	if err == nil || err.Error() != "factory failed" {
		t.Fatalf("load error = %v, want factory failed", err)
	}

	bus.Emit("factory-failure", nil)
	if flagDuringLoad != true {
		t.Errorf("flag during load = %v, want true", flagDuringLoad)
	}
	if _, has := runtime.FlagValue("failed-flag"); has {
		t.Error("the failed extension's flag default reached the runtime")
	}
	if got := pendingProviderNames(runtime); !slices.Equal(got, []string{"working-provider"}) {
		t.Errorf("pending providers = %v, want [working-provider]", got)
	}
	if eventCalls != 0 {
		t.Errorf("a subscription of the failed factory ran %d times", eventCalls)
	}
	want := `Extension "<failing>" failed to load and its API is no longer active.`
	if got := panicMessage(func() {
		captured.RegisterFlag("late-flag", extension.FlagOptions{Type: extension.FlagBoolean, Default: true})
	}); got != want {
		t.Errorf("late registerFlag panic = %q, want %q", got, want)
	}
}

// TestFailedFactoryKeepsAConcurrentlyLoadedProvider ports the second case of the same test file ("does not discard a concurrently
// loaded factory's provider"): the failing factory waits for the working one to load, then fails.
func TestFailedFactoryKeepsAConcurrentlyLoadedProvider(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	bus := extension.CreateEventBus()
	release := make(chan struct{})
	registered := make(chan struct{})
	var failing error
	var wg sync.WaitGroup
	wg.Go(func() {
		_, failing = factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
			pi.RegisterProvider("failed-provider", providerConfig)
			close(registered)
			<-release
			return errors.New("factory failed")
		}, ".", bus, runtime, "<failing>")
	})
	<-registered
	if _, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
		pi.RegisterProvider("working-provider", providerConfig)
		return nil
	}, ".", bus, runtime, "<working>"); err != nil {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if failing == nil || failing.Error() != "factory failed" {
		t.Fatalf("failing load error = %v, want factory failed", failing)
	}
	if got := pendingProviderNames(runtime); !slices.Equal(got, []string{"working-provider"}) {
		t.Errorf("pending providers = %v, want [working-provider]", got)
	}
}

// TestFactoryPanicIsALoadError: upstream catches whatever a factory throws (loader.ts:623-629), so a panic is the factory's load error and
// its changes are discarded.
func TestFactoryPanicIsALoadError(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	var captured extension.API
	_, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
		captured = pi
		pi.RegisterProvider("p", providerConfig)
		panic("boom")
	}, ".", extension.CreateEventBus(), runtime, "<panics>")
	if err == nil || err.Error() != "boom" {
		t.Fatalf("load error = %v, want boom", err)
	}
	if got := pendingProviderNames(runtime); len(got) != 0 {
		t.Errorf("pending providers = %v, want none", got)
	}
	if got := panicMessage(func() { captured.GetFlag("x") }); !strings.Contains(got, "failed to load and its API is no longer active") {
		t.Errorf("a captured API still works: %q", got)
	}
}

// TestRegistrationsReachTheExtensionAndCommitAtTheEnd checks what a successful load leaves: handlers, tool, command and flag on the
// extension, and the runtime changes applied only after the factory returned.
func TestRegistrationsReachTheExtensionAndCommitAtTheEnd(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	var pendingDuringLoad int
	ext, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
		pi.RegisterTool(extension.ToolDefinition{Name: "t", Parameters: []byte(`{"type":"object"}`)})
		pi.RegisterCommand("c", extension.CommandOptions{Description: "d", Handler: func(context.Context, string) error { return nil }})
		pi.RegisterFlag("f", extension.FlagOptions{Type: extension.FlagString, Default: "x"})
		pi.RegisterProvider("p", providerConfig)
		pendingDuringLoad = len(runtime.PendingProviderRegistrations())
		return nil
	}, ".", extension.CreateEventBus(), runtime, "<ok>")
	if err != nil {
		t.Fatal(err)
	}
	if pendingDuringLoad != 0 {
		t.Errorf("a provider registration reached the runtime before the factory returned")
	}
	if got := pendingProviderNames(runtime); !slices.Equal(got, []string{"p"}) {
		t.Errorf("pending providers = %v, want [p]", got)
	}
	if value, has := runtime.FlagValue("f"); !has || value != "x" {
		t.Errorf("flag value = %v, %v, want x", value, has)
	}
	if _, ok := ext.RegisteredTool("t"); !ok {
		t.Error("tool not registered")
	}
	if got := ext.CommandOrder; !slices.Equal(got, []string{"c"}) {
		t.Errorf("command order = %v", got)
	}
	if ext.Path != "<ok>" {
		t.Errorf("path = %q", ext.Path)
	}
}

func TestRegistrationValidationMatchesPi(t *testing.T) {
	load := func(factory extension.ExtensionFactory) error {
		_, err := factoryload.LoadExtensionFromFactory(factory, ".", extension.CreateEventBus(), extension.CreateExtensionRuntime(), "<v>")
		return err
	}
	for name, tc := range map[string]struct {
		register func(pi extension.API)
		want     string
	}{
		"tool without an object schema": {func(pi extension.API) { pi.RegisterTool(extension.ToolDefinition{Name: "t", Parameters: []byte(`[]`)}) }, `Tool "t" registered by extension "<v>" must define an object parameter schema.`},
		"command without a name":        {func(pi extension.API) { pi.RegisterCommand("", extension.CommandOptions{}) }, `Command registered by extension "<v>" must have a non-empty string name. Use pi.registerCommand("name", { description, handler }).`},
		"command without a handler":     {func(pi extension.API) { pi.RegisterCommand("c", extension.CommandOptions{}) }, `Command "/c" registered by extension "<v>" must define handler().`},
		"flag default of the wrong type": {func(pi extension.API) {
			pi.RegisterFlag("f", extension.FlagOptions{Type: extension.FlagBoolean, Default: "yes"})
		}, `Invalid default for flag "f": expected boolean, got string`},
		"action before the runner binds": {func(pi extension.API) { pi.GetActiveTools() }, "Extension runtime not initialized. Action methods cannot be called during extension loading."},
	} {
		t.Run(name, func(t *testing.T) {
			err := load(func(pi extension.API) error { tc.register(pi); return nil })
			if err == nil || err.Error() != tc.want {
				t.Errorf("load error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestInvalidatedRuntimeDisablesTheAPI: after runtime replacement or reload the runtime is stale and a captured API fails with its message
// (loader.ts:193-203, runtime.assertActive).
func TestInvalidatedRuntimeDisablesTheAPIAndItsSubscriptions(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	bus := extension.CreateEventBus()
	var captured extension.API
	calls := 0
	if _, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
		captured = pi
		pi.Events().On("ch", func(any) { calls++ })
		return nil
	}, ".", bus, runtime, "<live>"); err != nil {
		t.Fatal(err)
	}
	bus.Emit("ch", nil)
	runtime.Invalidate("stale!")
	bus.Emit("ch", nil)
	if calls != 1 {
		t.Errorf("subscription ran %d times, want 1 (dropped on invalidate)", calls)
	}
	if got := panicMessage(func() { captured.GetActiveTools() }); got != "stale!" {
		t.Errorf("captured API panic = %q, want stale!", got)
	}
}

// TestActionsGoThroughTheBoundRuntime: once the runner binds its core, an API action calls the runtime's handler.
func TestActionsGoThroughTheBoundRuntime(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	var captured extension.API
	if _, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error { captured = pi; return nil }, ".", extension.CreateEventBus(), runtime, "<a>"); err != nil {
		t.Fatal(err)
	}
	var entries []string
	runtime.ExtensionActions = extension.ExtensionActions{
		AppendEntry:    func(customType string, _ any) error { entries = append(entries, customType); return nil },
		GetActiveTools: func() []string { return []string{"x"} },
		GetSettings:    func() extension.Settings { return extension.Settings{"k": "v"} },
	}
	captured.AppendEntry("kind", nil)
	if !slices.Equal(entries, []string{"kind"}) || !slices.Equal(captured.GetActiveTools(), []string{"x"}) || captured.GetSettings()["k"] != "v" {
		t.Errorf("actions did not reach the runtime: %v", entries)
	}
}

// TestHandlersReachTheRunnerAsEveryOtherExtensionsDo drives a real in-process Runner over an extension loaded from a factory: a tool_call
// handler's block result, the session_start handler, and the unsubscribe function.
func TestHandlersReachTheRunnerAsEveryOtherExtensionsDo(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	started := 0
	var unsubscribe func()
	ext, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
		pi.OnSessionStart(func(context.Context, extension.SessionStartEvent) error { started++; return nil })
		unsubscribe = pi.OnToolCall(func(_ context.Context, evt extension.ToolCallEvent) (extension.ToolCallEventResult, error) {
			return extension.ToolCallEventResult{Block: true, Reason: "no " + evt.(extension.CustomToolCallEvent).ToolName}, nil
		})
		return nil
	}, ".", extension.CreateEventBus(), runtime, "<h>")
	if err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{ext}, ".", runtime)
	result, err := runner.EmitToolCall(context.Background(), extension.CustomToolCallEvent{ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: "c1"}, ToolName: "bash"})
	if err != nil || result == nil || !result.Block || result.Reason != "no bash" {
		t.Fatalf("EmitToolCall = %+v, %v", result, err)
	}
	if _, err := runner.Emit(context.Background(), extension.SessionStartEvent{Type: "session_start"}); err != nil || started != 1 {
		t.Fatalf("session_start: started=%d err=%v", started, err)
	}
	unsubscribe()
	unsubscribe()
	result, err = runner.EmitToolCall(context.Background(), extension.CustomToolCallEvent{ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: "c1"}, ToolName: "bash"})
	if err != nil || result != nil && result.Block {
		t.Fatalf("after unsubscribe EmitToolCall = %+v, %v", result, err)
	}
}

// TestReloadingAnExtensionRetiresTheAPIOfItsEarlierLoad: a reload runs a built-in factory again on the runtime the replacement runner keeps;
// the API the first run captured fails with the stale message, its event-bus subscription ends, and the new run's API works
// (spec docs/specs/extension-factory-trust.md test 4; upstream creates a new runtime and invalidates the old one).
func TestReloadingAnExtensionRetiresTheAPIOfItsEarlierLoad(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	bus := extension.CreateEventBus()
	var apis []extension.API
	runs := map[int]int{}
	load := func(path string) {
		t.Helper()
		index := len(apis)
		if _, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
			apis = append(apis, pi)
			pi.Events().On("ch", func(any) { runs[index]++ })
			return nil
		}, ".", bus, runtime, path); err != nil {
			t.Fatal(err)
		}
	}
	load("builtin:a")
	load("builtin:other")
	load("builtin:a")
	bus.Emit("ch", nil)
	if runs[0] != 0 || runs[1] != 1 || runs[2] != 1 {
		t.Errorf("subscriptions ran %v, want the retired load's subscription ended and the others live", runs)
	}
	if got := panicMessage(func() { apis[0].GetFlag("x") }); got != extension.DefaultStaleRuntimeMessage {
		t.Errorf("earlier API panic = %q, want the stale message", got)
	}
	for _, live := range []int{1, 2} {
		if got := panicMessage(func() { apis[live].GetFlag("x") }); got != "" {
			t.Errorf("API %d fails: %q", live, got)
		}
	}
}
