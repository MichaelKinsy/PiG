//go:build !race

package coding

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// A tool an extension registers after load makes the Session rebuild its registry, as Pi does, and the rebuild must not do again what it already did for the tools that did not change: a tool is wrapped, which decodes its schema, once per registration, not once per refresh. The allocations a refresh makes for each registered tool therefore stay a few, at any registry size, where wrapping every tool again made hundreds.
//
// upstream: agent-session.ts:3448 (_refreshToolRegistry)
func TestRefreshingTheToolRegistryDoesNotWrapUnchangedToolsAgain(t *testing.T) {
	perRegisteredTool := func(existing int) float64 {
		ext := extension.Extension{Path: "late"}
		ext.InitializeToolRegistry()
		runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
		session, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), NoSession: true, SkipBuiltinTools: true, Runner: runner})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = session.Close() })
		next := 0
		register := func() {
			next++
			definition := registryTool(fmt.Sprintf("dyn_%d", next), "Dynamic", "A dynamic tool", "Run it")
			definition.Parameters = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"The path to the file"},"offset":{"type":"number"}},"required":["path"]}`)
			ext.SetRegisteredTool(extension.RegisteredTool{Definition: definition})
			if err := session.RefreshTools(); err != nil {
				t.Fatal(err)
			}
		}
		for range existing {
			register()
		}
		return testing.AllocsPerRun(10, register) / float64(existing)
	}
	small, large := perRegisteredTool(50), perRegisteredTool(200)
	t.Logf("allocations per registered tool in one refresh: %.1f with 50 tools, %.1f with 200", small, large)
	if small > 30 || large > 30 {
		t.Fatalf("a refresh allocates %.1f times per registered tool with 50 tools and %.1f with 200; unchanged tools must not be wrapped again", small, large)
	}
}
