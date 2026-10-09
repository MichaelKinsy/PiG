package coding

import (
	"context"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"testing"
)

// upstream: runner.ts:843-845 getModelRegistry() returns the registry the AgentSession passes to the runner's bindCore (agent-session.ts
// _bindExtensionCore), and ctx.modelRegistry reads the same one. Through a real Session bound to its extensions, the runner's registry is the
// Services-owned facade and answers with the real catalog, not a value the test supplies.
func TestSessionRunnerGetModelRegistryIsTheServicesFacade(t *testing.T) {
	services, model, _, _ := sessionManagerFixture(t)
	session, err := NewSession(services, SessionOptions{Model: model, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.BindExtensions(context.Background(), ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}
	runner := session.ExtensionRunner()
	if runner == nil {
		t.Fatal("a bound session has an extension runner")
	}
	registry := runner.GetModelRegistry()
	if registry != services.Registry() {
		t.Fatalf("runner registry = %T, want the Services facade", registry)
	}
	if found := registry.Find(model.ProviderID(), model.ID); found == nil || found.ID != model.ID {
		t.Fatalf("Find(%s, %s) = %+v, want the session's model", model.ProviderID(), model.ID, found)
	}
	if len(registry.GetAll()) == 0 {
		t.Fatal("the facade serves the real catalog")
	}
}

// upstream: runner.ts:233-242 constructor(extensions, runtime, cwd, sessionManager, modelRegistry): the registry the runner is constructed with is the one
// getModelRegistry() and ctx.modelRegistry read before any bindCore, the runtime it is given is the one it shares with its loader, The registry comes from real Services and the runtime's flag default is
// read back from the shared runtime, not from a test-held value.
func TestNewExtensionRunnerBindsItsConstructorArguments(t *testing.T) {
	services, model, _, _ := sessionManagerFixture(t)
	session, err := NewSession(services, SessionOptions{Model: model, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	runtime := extension.CreateExtensionRuntime()
	flagged := extension.Extension{Path: "flagged", Flags: map[string]extension.ExtensionFlag{"shared-flag": {Type: "boolean", Default: true}}}
	var seen extension.ModelRegistry
	var seenManager extension.ReadonlySessionManager
	flagged.Handlers = map[string][]extension.HandlerFn{"session_start": {func(args ...any) (any, error) {
		ctx := extension.FromContext(args[1].(context.Context))
		seen, _ = ctx.ModelRegistry()
		seenManager, _ = ctx.SessionManager()
		return nil, nil
	}}}
	runner := NewExtensionRunner([]extension.Extension{flagged}, runtime, services.CWD(), session.Inner(), services.Registry())
	if runner.GetModelRegistry() != services.Registry() {
		t.Fatalf("GetModelRegistry() = %T, want the registry the runner was constructed with", runner.GetModelRegistry())
	}
	if runner.Runtime() != runtime || runtime.FlagValues["shared-flag"] != true {
		t.Fatalf("the runner shares the given runtime: same=%v flags=%v", runner.Runtime() == runtime, runtime.FlagValues)
	}
	if _, err := runner.Emit(context.Background(), extension.SessionStartEvent{Type: "session_start", Reason: "startup"}); err != nil {
		t.Fatal(err)
	}
	if seen != services.Registry() {
		t.Fatalf("ctx.modelRegistry = %T, want the constructor's registry", seen)
	}
	if seenManager != session.Inner() {
		t.Fatalf("ctx.sessionManager = %T, want the SessionManager the runner was constructed with", seenManager)
	}
}
