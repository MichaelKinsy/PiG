package clienttest

// test/p2/tui-render.test.ts, the cases that mount the footer on a topology: its numbers come from the owner's and the guest's own sources, never from what a guest may not read.

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	statustui "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/tui"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
)

func TestFooterProvenanceOwnerSeesContextAgainstTheModelWindowGuestsGetTokensWithoutADenominator(t *testing.T) {
	session := fixtureSession()
	session.Title, session.ThinkingLevel = "Footer fixture", "medium"
	usage := func(cacheRead, total float64) map[string]any {
		return map[string]any{
			"input": float64(12), "output": float64(3), "cacheRead": cacheRead, "cacheWrite": float64(0), "totalTokens": float64(15),
			"cost": map[string]any{"input": float64(0), "output": float64(0), "cacheRead": float64(0), "cacheWrite": float64(0), "total": total},
		}
	}
	view := tuitest.View(t, 1, []durable.EntryRecord{tuitest.AnswerEntry(1, "hello", 1)}, tuitest.DocsOf(t,
		"pi.agent", map[string]any{"model": map[string]any{"provider": "faux", "modelId": "faux-1"}, "thinkingLevel": "medium"},
		"pi.usage", map[string]any{"models": map[string]any{"faux/faux-1": usage(30, 0.25)}, "tools": map[string]any{}},
	), 0)
	stats := map[string]any{"contextTokens": float64(42), "latestAssistantUsage": usage(0, 0)}
	var modelsRequested atomic.Int64
	chat := tuitest.NewChatStub(t, tuitest.ChatStubOptions{Variant: "viewer", Views: func(int) delta.JsonValue { return view }, Stats: stats})
	metrics := p2test.Stub{Contract: statuscontract.SessionMetrics, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
		return p2test.StubMembers{Variant: "reader", State: map[string]any{"clients": float64(1), "usage": usage(0, 1.5)}}
	}}
	models := p2test.Stub{Contract: modelscontract.Models, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
		modelsRequested.Add(1)
		return p2test.StubMembers{Variant: "owner", State: map[string]any{
			"models": []any{
				delta.JsonObjectOf("provider", "faux", "modelId", "faux-1", "name", "Faux", "thinkingLevels", []any{"off"}, "contextWindow", float64(1000)),
			},
			"resources":     delta.JsonObjectOf("version", float64(1), "settings", delta.JsonObjectOf(), "credentials", []any{}, "providers", []any{}, "models", []any{}, "errors", []any{}),
			"defaultModel":  nil,
			"errors":        []any{},
			"enabledModels": []any{},
			"scopedModels":  []any{},
		}, Methods: map[string]p2test.StubMethod{
			"setDefault":       func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
			"setEnabledModels": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
		}}
	}}
	footerOf := func(role string, ready string) string {
		s := tuitest.NewTopology(t, tuitest.TopologyOptions{
			Server: []p2test.Stub{tuitest.SessionsStub(session), metrics, models}, Worker: []p2test.Stub{chat.Stub},
			Facets: []host.Facet{tui.CoreFacet, statustui.Facet}, Role: roleOf(role), Columns: 100,
		})
		pollScreen(t, s.Fixture, matches(ready))
		return s.Screen()
	}
	guest := footerOf("guest", `Footer fixture[\s\S]*\$0\.250`)
	if got := modelsRequested.Load(); got != 0 {
		t.Fatalf("guests never request the owner-only catalog (%d requests)", got)
	}
	mustMatch(t, `/work • Footer fixture`, guest)
	mustMatch(t, `↑12 ↓3 R30 CH0\.0% \$0\.250 \d+ ctx session \$1\.500`, guest)
	mustMatch(t, `\(faux\) faux-1 • medium`, guest)
	if matches(`%/1\.0k`)(guest) {
		t.Fatalf("a guest's footer has the model window as a denominator:\n%s", guest)
	}
	footerOf("owner", `\d+\.\d%/1\.0k`)
	if modelsRequested.Load() == 0 {
		t.Fatal("the owner's footer never asked for the catalog")
	}
}
