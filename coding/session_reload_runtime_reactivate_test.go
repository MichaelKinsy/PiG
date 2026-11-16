package coding

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// A host keeps one runtime across reloads, where Pi's loader creates a new one per load. A reload that failed invalidated that runtime with the old runner (agent-session.ts
// :3825-3826, runner.ts:729-735), and the reload that follows has its extensions register against it again, so it makes the runtime active before the loader runs.
//
// mutation-checked: dropping the Reactivate before the loader runs leaves the runtime, and so the new runner, stale.
func TestAReloadAfterAFailedOneRunsOnTheSharedRuntimeAgain(t *testing.T) {
	shared := extension.CreateExtensionRuntime()
	old, fresh := newEmitCounter(), newEmitCounter()
	loader := &swapLoader{ResourceLoader: NoResources, runtime: shared, err: errors.New("resource reload failed")}
	services := newTestServices(t)
	session, err := NewSession(services, SessionOptions{
		Model: fakeModel(), NoSession: true, ResourceLoader: loader,
		Runner: inproc.NewRunner([]extension.Extension{old.extension("<old>")}, services.CWD(), shared),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	previous := session.ExtensionRunner()
	if err := session.Reload(t.Context()); !errors.Is(err, loader.err) {
		t.Fatalf("Reload error = %v, want %v", err, loader.err)
	}
	if err := shared.AssertActive(); err == nil {
		t.Fatal("a reload that failed must leave the old runner's runtime stale")
	}

	loader.err, loader.extensions = nil, []extension.Extension{fresh.extension("<new>")}
	if err := session.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := shared.AssertActive(); err != nil {
		t.Fatalf("the reload after the failed one left the shared runtime stale: %v", err)
	}
	if runner := session.ExtensionRunner(); runner == previous || runner.IsStale() || runner.Runtime() != shared {
		t.Fatal("the reload after the failed one must leave a live runner over the shared runtime")
	}
}
