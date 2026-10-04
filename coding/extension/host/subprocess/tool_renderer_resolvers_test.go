package subprocess

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// resolverFixture is an extension that registered one tool renderer resolver, without a process: every resolution
// request it starts answers next(), as a failed request does.
func resolverFixture(t *testing.T) (*Host, *managedExt, extension.ToolRendererResolver) {
	t.Helper()
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.closeToolRendererResolutions() })
	me := &managedExt{config: ExtConfig{Name: "resolver"}, host: h}
	ext := h.buildExtension(me, &RegisterPayload{Name: "resolver", ToolRenderers: 1})
	me.resetToolRenderers(1)
	if len(ext.ToolRenderers) != 1 {
		t.Fatalf("resolvers = %d", len(ext.ToolRenderers))
	}
	return h, me, ext.ToolRenderers[0]
}

func resolutionStarted(me *managedExt, tool string) bool {
	state := &me.toolRendererResolvers
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.tools[tool] != nil
}

// Pi's loader appends a runtime pi.registerTool to the extension without touching its tool renderers
// (loader.ts registerTool, registerToolRenderer). The host builds the registered tool from a payload that carries only
// the tool; the extension's resolvers must keep being asked.
func TestToolRendererResolverSurvivesARuntimeToolRegistration(t *testing.T) {
	h, me, resolve := resolverFixture(t)
	h.buildExtension(me, &RegisterPayload{Name: "resolver", Tools: []ToolDecl{{Name: "late_tool", Parameters: []byte(`{"type":"object"}`)}}})
	base := &extension.ToolRenderers{}
	if got := resolve("late_tool", func() *extension.ToolRenderers { return base }); got != base {
		t.Fatalf("first resolution = %+v, want next()", got)
	}
	if !resolutionStarted(me, "late_tool") {
		t.Fatal("the extension's resolvers were not asked after a runtime tool registration")
	}
}

// An extension that keeps next()'s renderers changes nothing the cards show, so the mode is not told to draw them
// again: drawing a card again starts its renderer state over, which Pi never does.
func TestToolRendererAnswerOfNextDoesNotRedrawCards(t *testing.T) {
	h, me, _ := resolverFixture(t)
	var notified atomic.Int32
	h.SetToolRenderersResolvedFunc(func(string) { notified.Add(1) })
	resolution := &toolRendererResolution{}
	h.resolveToolRenderers(me, "bash", &extension.ToolRenderers{}, resolution)
	if !resolution.done || resolution.use != "next" {
		t.Fatalf("resolution = %+v, want next", resolution)
	}
	if got := notified.Load(); got != 0 {
		t.Fatalf("redraw notifications = %d, want none", got)
	}
}

// The UI loop and an HTML export resolve tool renderers at the same time; the resolver shares nothing unguarded.
// Run with -race.
func TestToolRendererResolverIsSafeForConcurrentResolutions(t *testing.T) {
	_, _, resolve := resolverFixture(t)
	next := func() *extension.ToolRenderers { return nil }
	var wg sync.WaitGroup
	for worker := range 4 {
		wg.Go(func() {
			for i := range 50 {
				resolve("tool-"+strconv.Itoa((worker*50+i)%60), next)
			}
		})
	}
	wg.Wait()
}

// After Shutdown has waited for the resolution requests, a resolution keeps next() and starts no request.
func TestToolRendererResolutionAfterShutdownStartsNoRequest(t *testing.T) {
	h, me, resolve := resolverFixture(t)
	h.closeToolRendererResolutions()
	base := &extension.ToolRenderers{}
	if got := resolve("bash", func() *extension.ToolRenderers { return base }); got != base {
		t.Fatalf("resolution = %+v, want next()", got)
	}
	state := &me.toolRendererResolvers
	state.mu.Lock()
	resolution := state.tools["bash"]
	state.mu.Unlock()
	h.toolRendererResolutions.Wait()
	if resolution == nil || resolution.done {
		t.Fatalf("resolution = %+v, want one that was never asked", resolution)
	}
}
