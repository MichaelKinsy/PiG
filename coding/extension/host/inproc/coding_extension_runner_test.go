package inproc_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// runner.ts:233-242 constructor(extensions, runtime, cwd, sessionManager, modelRegistry): the runner shares the runtime it is given, creates one when none is, and
// ctx.sessionManager and ctx.modelRegistry are the ones it was constructed with. A SessionManager that is absent stays absent in the context: an unbound
// manager must not reach a handler as a non-nil interface that holds a nil pointer.
// mutation-checked: dropping the nil guard, the runtime, the cwd, the registry or the manager fails.
func TestNewExtensionRunnerBindsItsRuntimeCwdAndSessionManager(t *testing.T) {
	var sawManager extension.ReadonlySessionManager
	var sawRegistry extension.ModelRegistry
	var sawCwd string
	probe := extension.Extension{Path: "probe", Handlers: map[string][]extension.HandlerFn{"session_start": {func(args ...any) (any, error) {
		ctx := extension.FromContext(args[1].(context.Context))
		sawManager, _ = ctx.SessionManager()
		sawRegistry, _ = ctx.ModelRegistry()
		sawCwd, _ = ctx.CWD()
		return nil, nil
	}}}}
	start := extension.SessionStartEvent{Type: "session_start", Reason: "startup"}

	unbound := coding.NewExtensionRunner([]extension.Extension{probe}, nil, "/work", nil, nil)
	if unbound.Runtime() == nil {
		t.Fatal("a runner constructed without a runtime has none; Pi's constructor shares or creates one")
	}
	if _, err := unbound.Emit(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	if sawManager != nil || sawRegistry != nil {
		t.Fatalf("unbound context holds manager %v registry %v, want both absent", sawManager, sawRegistry)
	}
	if sawCwd != "/work" {
		t.Fatalf("ctx.cwd = %q, want the constructor's /work", sawCwd)
	}

	manager, err := coding.NewInMemorySessionManager("/bound")
	if err != nil {
		t.Fatal(err)
	}
	runtime := extension.CreateExtensionRuntime()
	registry := &registryStub{}
	bound := coding.NewExtensionRunner([]extension.Extension{probe}, runtime, "/bound", manager, registry)
	if bound.Runtime() != runtime {
		t.Fatal("the runner does not share the runtime it was constructed with")
	}
	if _, err := bound.Emit(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	if sawManager == nil || sawCwd != "/bound" || sawRegistry != extension.ModelRegistry(registry) {
		t.Fatalf("bound context holds manager %v cwd %q registry %v, want the constructor's", sawManager, sawCwd, sawRegistry)
	}
}
