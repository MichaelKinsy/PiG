package inproc_test

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// .upstream/v1.0.1/packages/coding-agent/test/extensions-runner.test.ts:892 (#10285): the MCP extension renders calls to
// tools that are not registered.
// Pi: packages/coding-agent/src/core/extensions/runner.ts:790 (Runner.resolveToolRenderers).
func TestUpstreamRunnerResolvesToolRenderersInExtensionLoadOrderEachAbleToDeferToTheNext(t *testing.T) {
	renderCall := func(json.RawMessage, extension.Theme, extension.ToolRenderContext) extension.Component { return nil }
	isRenderCall := func(r *extension.ToolRenderers) bool {
		return r != nil && r.RenderCall != nil && r.RenderShell == "" && r.RenderResult == nil
	}
	first := extension.Extension{Path: "first", ToolRenderers: []extension.ToolRendererResolver{
		func(toolName string, next func() *extension.ToolRenderers) *extension.ToolRenderers {
			if toolName == "a" {
				return &extension.ToolRenderers{RenderCall: renderCall}
			}
			return next()
		},
	}}
	second := extension.Extension{Path: "second", ToolRenderers: []extension.ToolRendererResolver{
		func(_ string, next func() *extension.ToolRenderers) *extension.ToolRenderers {
			if renderers := next(); renderers != nil {
				return renderers
			}
			return &extension.ToolRenderers{RenderShell: extension.ToolRenderShellSelf}
		},
	}}
	runner := inproc.NewRunner([]extension.Extension{first, second}, t.TempDir())
	none := func() *extension.ToolRenderers { return nil }

	if got := runner.ResolveToolRenderers("a", none); !isRenderCall(got) {
		t.Fatalf(`resolve("a") = %+v, want { renderCall }`, got)
	}
	if got := runner.ResolveToolRenderers("b", none); got == nil || got.RenderShell != extension.ToolRenderShellSelf || got.RenderCall != nil || got.RenderResult != nil {
		t.Fatalf(`resolve("b") = %+v, want { renderShell: "self" }`, got)
	}
	if got := runner.ResolveToolRenderers("b", func() *extension.ToolRenderers { return &extension.ToolRenderers{RenderCall: renderCall} }); !isRenderCall(got) {
		t.Fatalf(`resolve("b", base) = %+v, want { renderCall }`, got)
	}
}
