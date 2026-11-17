package piglet

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// The Piglet extension's session_start handler reads the flag, resolves the Piglet and scopes the tools without awaiting anything, so it is registered as one that returns
// without awaiting; its before_agent_start handler is registered as any handler. Pi has no Piglet extension (pig additive), so no handler of Pi's is the reference.
func TestPigletSessionStartHandlerReturnsWithoutAwaiting(t *testing.T) {
	runner := inproc.NewRunner([]extension.Extension{BuildExtensionWithPiglet(&Piglet{Name: "research"})}, t.TempDir())
	if !runner.HasHandlers("session_start") || runner.HasHandlersThatMayAwait("session_start") {
		t.Fatalf("session_start: HasHandlers = %v, HasHandlersThatMayAwait = %v, want true, false", runner.HasHandlers("session_start"), runner.HasHandlersThatMayAwait("session_start"))
	}
	if !runner.HasHandlersThatMayAwait("before_agent_start") {
		t.Fatal("the before_agent_start handler is not registered, or is registered as one that returns without awaiting")
	}
}
