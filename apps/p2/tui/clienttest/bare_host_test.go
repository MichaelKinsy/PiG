package clienttest

// test/p2/tui.test.ts "bare-host TUI facets register, render session/model pickers and reload without a server".

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	modelstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sessionstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/tui"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui"
	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

func TestBareHostTUIFacetsRegisterRenderSessionModelPickersAndReloadWithoutAServer(t *testing.T) {
	session := fixtureSession()
	session.ID, session.Title, session.Cwd = "fixture-session", "Fixture session", "/fixture"
	var configured atomic.Value
	configured.Store("")
	noop := func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }
	sessions := p2test.Stub{Contract: contract.Sessions, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		return p2test.StubMembers{
			Variant: "owner",
			State:   map[string]any{"sessions": self.Cell(rowsOf(session)), "defaultCwd": "/fixture", "defaultEnv": nil, "operations": []any{}},
			Methods: map[string]p2test.StubMethod{
				"dismissOperation": noop, "create": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return session.JSON(), nil },
				"relocate": noop, "rename": noop, "pin": noop, "archive": noop, "restore": noop, "clone": unused, "fork": unused,
			},
		}
	}}
	models := p2test.Stub{Contract: modelscontract.Models, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
		return p2test.StubMembers{Variant: "owner", State: map[string]any{
			"models": []any{
				delta.JsonObjectOf("provider", "faux", "modelId", "faux-1", "name", "Fixture model", "thinkingLevels", []any{"off"}, "contextWindow", float64(128000)),
			},
			"resources":     delta.JsonObjectOf("version", float64(1), "settings", delta.JsonObjectOf(), "credentials", []any{}, "providers", []any{}, "models", []any{}, "errors", []any{}),
			"defaultModel":  nil,
			"errors":        []any{},
			"enabledModels": []any{},
			"scopedModels":  []any{},
		}, Methods: map[string]p2test.StubMethod{
			"setEnabledModels": noop,
			"setDefault": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				configured.Store(args.(*delta.JsonObject).Value("modelId"))
				return nil, nil
			},
		}}
	}}
	facets := []host.Facet{tui.CoreFacet, sessionstui.Facet, modelstui.Facet}
	f := tuitest.New(t, tuitest.Options{
		Facets: facets, Columns: 70, Rows: 24,
		Host: p2test.TestHostOptions{Stubs: []p2test.Stub{sessions, models}},
		Prepare: func(rt *tuiruntime.TuiRuntime) {
			rt.Connected = true
			rt.Role = tuiruntime.RoleOwner
		},
	})
	pollContains(t, f, "Fixture session")
	mustMatch(t, `New session`, f.Screen())
	f.Send(down)
	f.Send(enter)
	var viewed string
	f.Do(func() { viewed = f.Runtime.Session })
	if viewed != session.ID {
		t.Fatalf("the viewed session = %q", viewed)
	}
	// pi's model selector as an overlay; without a session it sets the default for new sessions.
	f.Do(func() { f.Runtime.Navigate("sessions", "", false, nil) })
	f.Poll(func() bool { return f.Runtime.Ctx.Commands().Run("app.model.select", "") })
	pollContains(t, f, "faux-1")
	mustMatch(t, `Choose default model for new sessions`, f.Screen())
	f.Send(enter)
	tuitest.Poll(t, func() bool { return configured.Load() == "faux-1" })
	if err := f.Host.ReloadFacets(facets); err != nil {
		t.Fatal(err)
	}
	f.Do(func() { f.Runtime.Navigate("sessions", "", false, nil) })
	pollContains(t, f, "Fixture session")
}
