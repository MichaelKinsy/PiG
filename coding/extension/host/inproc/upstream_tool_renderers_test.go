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

// runner.ts resolveToolRenderers: a resolver that answers without calling next ends the chain, so of two resolvers that both claim a tool the one in the extension that
// loaded first wins, and one that never reaches the base keeps its answer over the base's.
func TestUpstreamRunnerToolRendererResolversThatClaimTheSameToolAreWonByTheFirstLoaded(t *testing.T) {
	claim := func(shell extension.ToolRenderShell) extension.ToolRendererResolver {
		return func(toolName string, next func() *extension.ToolRenderers) *extension.ToolRenderers {
			if toolName == "claimed" {
				return &extension.ToolRenderers{RenderShell: shell}
			}
			return next()
		}
	}
	runner := inproc.NewRunner([]extension.Extension{
		{Path: "first", ToolRenderers: []extension.ToolRendererResolver{claim(extension.ToolRenderShellSelf)}},
		{Path: "second", ToolRenderers: []extension.ToolRendererResolver{claim(extension.ToolRenderShellDefault)}},
	}, t.TempDir())
	base := func() *extension.ToolRenderers {
		return &extension.ToolRenderers{RenderShell: extension.ToolRenderShell("base")}
	}
	if got := runner.ResolveToolRenderers("claimed", base); got == nil || got.RenderShell != extension.ToolRenderShellSelf {
		t.Fatalf(`resolve("claimed") = %+v, want the first loaded extension's { renderShell: "self" }`, got)
	}
	if got := runner.ResolveToolRenderers("other", base); got == nil || got.RenderShell != "base" {
		t.Fatalf(`resolve("other") = %+v, want the base renderers once every resolver defers`, got)
	}
}
