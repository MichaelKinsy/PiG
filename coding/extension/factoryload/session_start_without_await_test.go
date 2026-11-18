package factoryload_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/extensiontest"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func loadWithRunner(t *testing.T, factory extension.ExtensionFactory) *inproc.Runner {
	t.Helper()
	runtime := extension.CreateExtensionRuntime()
	loaded, err := factoryload.LoadExtensionFromFactory(factory, t.TempDir(), extension.CreateEventBus(), runtime, "returns-without-await.go")
	if err != nil {
		t.Fatal(err)
	}
	return inproc.NewRunner([]extension.Extension{loaded}, t.TempDir(), runtime)
}

// A factory that registers its session_start handler as one that returns without awaiting leaves a runner whose session_start handlers cannot pend; one that registers it
// through OnSessionStart leaves a runner whose handlers can, and so does a factory with both. Removing the handler through the function the registration returned removes it.
func TestSessionStartRegisteredAsReturningWithoutAwaitingDoesNotMakeTheRunnerAwait(t *testing.T) {
	var remove func()
	runner := loadWithRunner(t, func(pi extension.API) error {
		remove = extension.OnSessionStartReturningWithoutAwait(pi, func(context.Context, extension.SessionStartEvent) error { return nil })
		return nil
	})
	if !runner.HasHandlers("session_start") || runner.HasHandlersThatMayAwait("session_start") {
		t.Fatalf("handler that returns without awaiting: HasHandlers = %v, HasHandlersThatMayAwait = %v, want true, false", runner.HasHandlers("session_start"), runner.HasHandlersThatMayAwait("session_start"))
	}
	remove()
	if runner.HasHandlers("session_start") {
		t.Fatal("the function the registration returned did not remove the handler")
	}

	plain := loadWithRunner(t, func(pi extension.API) error {
		pi.OnSessionStart(func(context.Context, extension.SessionStartEvent) error { return nil })
		return nil
	})
	if !plain.HasHandlersThatMayAwait("session_start") {
		t.Fatal("a handler registered with OnSessionStart cannot pend")
	}

	mixed := loadWithRunner(t, func(pi extension.API) error {
		extension.OnSessionStartReturningWithoutAwait(pi, func(context.Context, extension.SessionStartEvent) error { return nil })
		pi.OnSessionStart(func(context.Context, extension.SessionStartEvent) error { return nil })
		return nil
	})
	if !mixed.HasHandlersThatMayAwait("session_start") {
		t.Fatal("a mixed set of handlers cannot pend")
	}
}

// An API that cannot record the registration (a test double: no runner asks it) registers the handler as OnSessionStart does.
func TestSessionStartReturningWithoutAwaitFallsBackToOnSessionStart(t *testing.T) {
	fake := extensiontest.NewFake()
	extension.OnSessionStartReturningWithoutAwait(fake, func(context.Context, extension.SessionStartEvent) error { return nil })
	if len(fake.OnSessionStartHandlers) != 1 {
		t.Fatalf("OnSessionStartHandlers = %d, want 1", len(fake.OnSessionStartHandlers))
	}
}
