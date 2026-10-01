package subprocess

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The unchanged Pi 0.99.1 example examples/extensions/jev-router.ts runs in the production Node subprocess. Its route function reads `ctx.modelRegistry.find`, `request.previous`, `request.state` and `request.messages`, so these cases drive every branch that does not need the Jev classifier (`ctx.modelRegistry.findOfType` and `classify`, which lane port-99-f6h-model-types owns). The two cases of test/jev-router-example.test.ts that go through the classifier are ported in test/extension-conformance/node_jev_router_test.go.
//
// upstream: .upstream/v0.99.1/packages/coding-agent/examples/extensions/jev-router.ts:11-140

func codexModel(id string) map[string]any {
	return map[string]any{"id": id, "modelId": id, "provider": "openai-codex", "name": id, "displayName": id, "api": "openai-codex-responses", "reasoning": true, "input": []string{"text"}, "contextWindow": 272000, "maxTokens": 128000, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}
}

func loadJevRouter(t *testing.T) (*Host, extension.PendingVirtualModelRegistration) {
	t.Helper()
	host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	bridge := NewUIBridge(func() {})
	bridge.SetActions(&HostCallbacks{GetModels: func() []map[string]any {
		return []map[string]any{codexModel("gpt-5.6-sol"), codexModel("gpt-5.6-terra"), codexModel("gpt-5.6-luna")}
	}})
	host.SetUIBridge(bridge)
	entry := filepath.Join(findModuleRoot(t), ".upstream", "current", "packages", "coding-agent", "examples", "extensions", "jev-router.ts")
	loaded, errs := host.LoadAll(t.Context(), []ExtConfig{{Name: "jev-router", Source: entry, Enabled: true}})
	if len(errs) != 0 || len(loaded) != 1 {
		t.Fatalf("load jev-router: %v (%d extensions)", errs, len(loaded))
	}
	pending := host.Runtime().PendingVirtualModelRegistrations()
	if len(pending) != 1 {
		t.Fatalf("registered %d virtual models, want jev/auto", len(pending))
	}
	return host, pending[0]
}

func jevRequest(reason extension.ModelRouteReason, previous string, state string, messages ...ai.Message) extension.ModelRouteRequest {
	request := extension.ModelRouteRequest{Model: &ai.Model{ID: "auto"}, ThinkingLevel: ai.ThinkingHigh, Reason: reason, Messages: messages}
	if previous != "" {
		request.Previous = &extension.ModelRoutePrevious{Model: &ai.Model{ID: previous, ProviderMeta: ai.ProviderMetadata{ProviderID: "openai-codex"}}, ThinkingLevel: ai.ThinkingMedium}
	}
	if state != "" {
		request.State = json.RawMessage(state)
	}
	return request
}

func editResult(tool string, failed bool) ai.Message {
	return ai.ToolResultMessage{ToolCallID: "call-1", ToolName: tool, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, IsError: failed}
}

func userMessage(text string) ai.Message {
	return ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: text}}}
}

// jev-router.ts:112-129: the example registers jev/auto with the limits it declares, without a route, which the Host calls in the extension.
func TestJevRouterExampleRegistersTheVirtualModel(t *testing.T) {
	_, pending := loadJevRouter(t)
	def := pending.Definition
	if def.Provider != "jev" || def.ID != "auto" || def.Name != "Auto (Jev)" || def.ContextWindow != 272_000 || def.MaxTokens != 128_000 || def.Route == nil {
		t.Fatalf("definition = %+v", def)
	}
	want := []ai.ModelThinkingLevel{ai.ThinkingLow, ai.ThinkingMedium, ai.ThinkingHigh, ai.ThinkingXHigh}
	if !slices.Equal(def.ThinkingLevels, want) {
		t.Fatalf("thinking levels = %v, want %v", def.ThinkingLevels, want)
	}
}

func TestJevRouterExampleRoutes(t *testing.T) {
	_, pending := loadJevRouter(t)
	route := func(request extension.ModelRouteRequest) extension.ModelRoute {
		t.Helper()
		routed, err := pending.Definition.Route(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		return routed
	}
	// :113-114 Requests outside the agent loop, such as compaction summaries, go to Luna, and the thinking level passes through.
	if routed := route(jevRequest(extension.ModelRouteReasonDirect, "", "")); routed.Model.ID != "gpt-5.6-luna" || routed.Model.ProviderMeta.ProviderID != "openai-codex" || routed.ThinkingLevel != ai.ThinkingHigh || routed.State != nil {
		t.Fatalf("direct = %+v", routed)
	}
	// :83-85 A session that already uses a planning model keeps it, so switching to jev/auto costs no cache miss; the phase starts as planning.
	for _, previous := range []string{"gpt-5.6-sol", "gpt-5.6-terra"} {
		routed := route(jevRequest(extension.ModelRouteReasonUser, previous, "", userMessage("Refactor the cache layer.")))
		if routed.Model.ID != previous || string(routed.State) != `{"phase":"planning","model":"`+previous+`"}` {
			t.Fatalf("user with previous %s = model %s state %s", previous, routed.Model.ID, routed.State)
		}
	}
	// :117-123 The planning model made the first successful edit: the rest of the work goes to Luna, and the phase is stored.
	planning := `{"phase":"planning","model":"gpt-5.6-sol"}`
	for _, tool := range []string{"edit", "write"} {
		routed := route(jevRequest(extension.ModelRouteReasonContinuation, "gpt-5.6-sol", planning, userMessage("go"), editResult(tool, false)))
		if routed.Model.ID != "gpt-5.6-luna" || string(routed.State) != `{"phase":"implementation","model":"gpt-5.6-luna"}` {
			t.Fatalf("after a %s = model %s state %s", tool, routed.Model.ID, routed.State)
		}
	}
	// :47-53 A failed edit, a read, or an edit from an earlier turn does not end the planning phase; the state is kept, not stored again.
	for name, messages := range map[string][]ai.Message{
		"failed edit": {userMessage("go"), editResult("edit", true)},
		"read":        {userMessage("go"), editResult("read", false)},
		"earlier":     {userMessage("first"), editResult("edit", false), userMessage("second")},
	} {
		routed := route(jevRequest(extension.ModelRouteReasonContinuation, "gpt-5.6-sol", planning, messages...))
		if routed.Model.ID != "gpt-5.6-sol" || routed.State != nil {
			t.Fatalf("%s = model %s state %s", name, routed.Model.ID, routed.State)
		}
	}
	// :125 Once implementing, the router stays on Luna.
	implementation := `{"phase":"implementation","model":"gpt-5.6-luna"}`
	if routed := route(jevRequest(extension.ModelRouteReasonUser, "gpt-5.6-luna", implementation, userMessage("more"))); routed.Model.ID != "gpt-5.6-luna" || routed.State != nil {
		t.Fatalf("implementation = %+v", routed)
	}
	// :33-36 A model that is not in the catalog is an error the request reports.
	host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	host.SetUIBridge(NewUIBridge(func() {}))
	entry := filepath.Join(findModuleRoot(t), ".upstream", "current", "packages", "coding-agent", "examples", "extensions", "jev-router.ts")
	if _, errs := host.LoadAll(t.Context(), []ExtConfig{{Name: "jev-router", Source: entry, Enabled: true}}); len(errs) != 0 {
		t.Fatal(errs)
	}
	empty := host.Runtime().PendingVirtualModelRegistrations()[0]
	if _, err := empty.Definition.Route(t.Context(), jevRequest(extension.ModelRouteReasonDirect, "", "")); err == nil || !strings.Contains(err.Error(), "Model openai-codex/gpt-5.6-luna is not in the catalog") {
		t.Fatalf("missing model error = %v", err)
	}
}
