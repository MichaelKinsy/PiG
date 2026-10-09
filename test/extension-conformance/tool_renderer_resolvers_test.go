package extensionconformance

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi pi.registerToolRenderer (packages/coding-agent/src/core/extensions/types.ts:1685; runner.ts resolveToolRenderers): every SDK's common
// fixture registers one resolver that, by tool, draws calls itself ("conformance_tool_renderer"), draws none
// ("conformance_no_renderer"), fills in only when next() has none ("conformance_fill"), replaces next()'s call renderer
// in the SDK's own idiom ("conformance_wrap") and defers every other tool to next(). Each expectation differs from the
// next() a failed or skipped request falls back to. The host resolves a subprocess extension's resolvers off the UI
// loop, so a first resolution returns next() until the extension answered (D89).
func TestToolRendererResolversAcrossSDKs(t *testing.T) {
	requireAPIMember(t, "RegisterToolRenderer", extension.API.RegisterToolRenderer)
	t.Parallel()
	for _, tc := range allHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			resolved := watchToolRendererResolutions(h)
			base := &extension.ToolRenderers{RenderShell: extension.ToolRenderShellDefault}
			wrapBase := &extension.ToolRenderers{RenderShell: extension.ToolRenderShellSelf}
			resolve := func(tool string) *extension.ToolRenderers {
				return h.runner.ResolveToolRenderers(tool, func() *extension.ToolRenderers {
					switch tool {
					case "conformance_fill":
						return nil
					case "conformance_wrap":
						return wrapBase
					}
					return base
				})
			}
			var own *extension.ToolRenderers
			waitFor(t, func() bool {
				own = resolve("conformance_tool_renderer")
				return own != base
			})
			if own == nil || own.RenderCall == nil || own.RenderResult != nil {
				t.Fatalf("registerToolRenderer: resolved renderers = %+v, want the fixture's call renderer only", own)
			}
			expectCallLine(t, own, "resolved:conformance_tool_renderer:pi")
			// An answer that changes the renderers draws the tool's cards again.
			waitFor(t, func() bool { return resolved("conformance_tool_renderer") })

			// A resolver that returns none overrides next(); the card draws without renderers.
			waitFor(t, func() bool { return resolve("conformance_no_renderer") == nil })

			// next() ?? own: next() has none here, so the resolver's own renderers.
			var filled *extension.ToolRenderers
			waitFor(t, func() bool {
				filled = resolve("conformance_fill")
				return filled != nil
			})
			expectCallLine(t, filled, "filled:conformance_fill:pi")

			// next()'s renderers with the resolver's call renderer: next()'s renderShell, the resolver's call (D89: next()'s
			// own render functions are not carried).
			var wrapped *extension.ToolRenderers
			waitFor(t, func() bool {
				wrapped = resolve("conformance_wrap")
				return wrapped != wrapBase
			})
			if wrapped == nil || wrapped.RenderShell != extension.ToolRenderShellSelf || wrapped.RenderCall == nil {
				t.Fatalf("wrapped renderers = %+v, want next()'s self shell and the resolver's call renderer", wrapped)
			}
			expectCallLine(t, wrapped, "wrapped:conformance_wrap:pi")

			// next() defers to the remaining resolvers, then the registered tool: base here, for any other tool.
			for _, tool := range []string{"conformance_next", "mcp__docs__search"} {
				if first := resolve(tool); first != base {
					t.Fatalf("%s first resolution = %+v, want base", tool, first)
				}
			}
			for _, tool := range []string{"conformance_next", "mcp__docs__search"} {
				if got := resolve(tool); got != base {
					t.Fatalf("%s resolution = %+v, want base", tool, got)
				}
			}
		})
	}
}

// pi.registerToolRenderer after loading (loader.ts registerToolRenderer appends to the loaded extension): a command
// registers a second resolver, and later resolutions run it.
// Pi: packages/coding-agent/src/core/extensions/runner.ts:790 (Runner.resolveToolRenderers).
func TestToolRendererRegisteredAfterLoadingAcrossSDKs(t *testing.T) {
	t.Parallel()
	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				h.host.Shutdown("test done")
			})
			base := &extension.ToolRenderers{}
			resolve := func() *extension.ToolRenderers {
				return h.runner.ResolveToolRenderers("conformance_late", func() *extension.ToolRenderers { return base })
			}
			if !h.runner.ExecuteCommand(context.Background(), "late_tool_renderer", "") {
				t.Fatal("late_tool_renderer command not found")
			}
			var late *extension.ToolRenderers
			waitFor(t, func() bool {
				late = resolve()
				return late != base
			})
			if late == nil || late.RenderCall == nil {
				t.Fatalf("late renderers = %+v, want the late resolver's call renderer", late)
			}
			expectCallLine(t, late, "late:conformance_late:pi")
		})
	}
}

// expectCallLine draws a call with renderers and waits for the one line it shows.
func expectCallLine(t *testing.T, renderers *extension.ToolRenderers, want string) {
	t.Helper()
	if renderers == nil || renderers.RenderCall == nil {
		t.Fatalf("renderers = %+v, want a call renderer", renderers)
	}
	renderContext := extension.ToolRenderContext{
		Args: json.RawMessage(`{"q":"pi"}`), ToolCallID: "resolver-1", Card: "resolver-card-" + want,
		State: map[string]any{}, Invalidate: func() {}, ExecutionStarted: true, ArgsComplete: true,
	}
	call, ok := renderers.RenderCall(json.RawMessage(`{"q":"pi"}`), nil, renderContext).(interface{ Render(int) []string })
	if !ok {
		t.Fatal("renderCall did not return a component")
	}
	var lines []string
	waitFor(t, func() bool {
		lines = call.Render(72)
		return len(lines) == 1 && lines[0] == want
	})
}

// watchToolRendererResolutions reports, per tool, whether a subprocess extension's answer changed the tool's renderers,
// through the host's notification the interactive mode draws the tool's cards again on. In-process resolvers answer
// at once.
func watchToolRendererResolutions(h *harness) func(tool string) bool {
	if h.host == nil {
		return func(string) bool { return true }
	}
	var mu sync.Mutex
	seen := map[string]bool{}
	h.host.SetToolRenderersResolvedFunc(func(tool string) {
		mu.Lock()
		seen[tool] = true
		mu.Unlock()
	})
	return func(tool string) bool {
		mu.Lock()
		defer mu.Unlock()
		return seen[tool]
	}
}
