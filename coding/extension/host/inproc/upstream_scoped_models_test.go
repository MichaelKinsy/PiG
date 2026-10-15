package inproc_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:114
func TestUpstreamRunnerScopedModels(t *testing.T) {
	r := inproc.NewRunner(nil, t.TempDir())
	cold := extension.FromContext(r.DispatchContext(t.Context()))
	initial, err := cold.ScopedModels()
	if err != nil || initial == nil || len(initial) != 0 {
		t.Fatalf("default=%v error=%v; want empty list", initial, err)
	}
	scoped := []extension.ScopedModel{{Model: &ai.Model{ID: "scoped-test"}, ThinkingLevel: "high"}}
	r.BindCore(extension.ExtensionActions{}, extension.ContextActions{GetScopedModels: func() []extension.ScopedModel { return scoped }}, nil)
	ctx := extension.FromContext(r.DispatchContext(t.Context()))
	got, err := ctx.ScopedModels()
	if err != nil || len(got) != len(scoped) || &got[0] != &scoped[0] {
		t.Fatalf("scoped=%v error=%v; want original slice", got, err)
	}
}

// runner.ts:811-844 captures the callback, not the result or the runner field. Command contexts keep that same getter (889-897).
func TestScopedModelsCaptureCallbackButReadLiveList(t *testing.T) {
	r := inproc.NewRunner(nil, t.TempDir())
	cold := r.CreateCommandContext()
	scoped := []extension.ScopedModel{{Model: &ai.Model{ID: "first"}}}
	r.BindCore(extension.ExtensionActions{}, extension.ContextActions{GetScopedModels: func() []extension.ScopedModel { return scoped }}, nil)
	contexts := []*extension.Context{extension.FromContext(r.DispatchContext(t.Context())), r.CreateCommandContext().Context}
	scoped = []extension.ScopedModel{{Model: &ai.Model{ID: "second"}, ThinkingLevel: "off"}}
	r.BindCore(extension.ExtensionActions{}, extension.ContextActions{GetScopedModels: func() []extension.ScopedModel { return []extension.ScopedModel{} }}, nil)
	for _, ctx := range contexts {
		got, err := ctx.ScopedModels()
		if err != nil || len(got) != 1 || &got[0] != &scoped[0] {
			t.Fatalf("live list=%v error=%v", got, err)
		}
	}
	if got, err := cold.ScopedModels(); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("old default=%v error=%v", got, err)
	}
	r.Invalidate("replaced")
	for _, ctx := range contexts {
		if _, err := ctx.ScopedModels(); err == nil {
			t.Error("stale getter did not fail")
		}
	}
}

// runner.ts:914 `get thinkingLevel()` asserts the runner is active and returns runtime.getThinkingLevel(): absent until a
// session runtime binds one, then the live level on every read, from the context actions or from the bound ExtensionActions.
func TestContextThinkingLevelReadsTheBoundRuntimeLive(t *testing.T) {
	r := inproc.NewRunner(nil, t.TempDir())
	ctx := extension.FromContext(r.DispatchContext(t.Context()))
	if got, err := ctx.ThinkingLevel(); err != nil || got != "" {
		t.Fatalf("unbound = %v, %v; want absent", got, err)
	}
	level := extension.ThinkingLevel("high")
	r.BindCore(extension.ExtensionActions{}, extension.ContextActions{GetThinkingLevel: func() extension.ThinkingLevel { return level }}, nil)
	ctx = extension.FromContext(r.DispatchContext(t.Context()))
	level = extension.ThinkingLevel("low")
	if got, err := ctx.ThinkingLevel(); err != nil || got != level {
		t.Fatalf("context action = %v, %v; want the live level %v", got, err, level)
	}
	r.BindCore(extension.ExtensionActions{GetThinkingLevel: func() extension.ThinkingLevel { return "minimal" }}, extension.ContextActions{}, nil)
	ctx = extension.FromContext(r.DispatchContext(t.Context()))
	if got, err := ctx.ThinkingLevel(); err != nil || got != extension.ThinkingLevel("minimal") {
		t.Fatalf("runtime action = %v, %v; want minimal", got, err)
	}
	r.Invalidate("replaced")
	if _, err := ctx.ThinkingLevel(); err == nil {
		t.Error("stale getter did not fail")
	}
}
