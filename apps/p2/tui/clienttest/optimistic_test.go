package clienttest

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sessionstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/tui"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// catalogHeld is the owner's catalog with rename and pin held open by held; each change commits only once its call passes.
func catalogHeld(held *gate) p2test.Stub {
	return p2test.Stub{Contract: contract.Sessions, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		rows := self.Cell(rowsOf(fixtureSession()))
		update := func(key string, value any) {
			patchRows(rows, func(row *delta.JsonObject) { row.Set(key, value) })
		}
		return p2test.StubMembers{
			Variant: "owner",
			State:   map[string]any{"sessions": rows, "defaultCwd": "/work", "defaultEnv": nil, "operations": []any{}},
			Methods: map[string]p2test.StubMethod{
				"rename": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					chordctx.Suspend(ctx)
					if err := held.call(); err != nil {
						return nil, err
					}
					update("title", args.(*delta.JsonObject).Value("title"))
					return nil, nil
				},
				"pin": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					chordctx.Suspend(ctx)
					if err := held.call(); err != nil {
						return nil, err
					}
					update("pinned", args.(*delta.JsonObject).Value("pinned"))
					return nil, nil
				},
				"create":           func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return fixtureSession().JSON(), nil },
				"relocate":         func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
				"archive":          func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
				"restore":          func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
				"clone":            func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return fixtureSession().JSON(), nil },
				"fork":             func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return fixtureSession().JSON(), nil },
				"dismissOperation": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
			},
		}
	}}
}

func TestSessionSelectorARenameAndAPinShowBeforeTheirCallsAnswerARefusedRenameRollsBack(t *testing.T) {
	held := newGate()
	f := tuitest.NewTopology(t, tuitest.TopologyOptions{Server: []p2test.Stub{catalogHeld(held)}, Facets: []host.Facet{tui.CoreFacet, sessionstui.Facet}, Columns: 100})
	f.Do(func() { f.Runtime.Navigate("sessions", "s", true, nil) })
	listed := func(title string) func(string) bool { return matches(`(?m)^(?:  |› )  ` + title) }
	pollScreen(t, f.Fixture, listed("Fixture"))
	f.Send(down)
	f.Send(ctrlR)
	pollContains(t, f.Fixture, "Rename session")
	f.Send(ctrlA)
	f.Send(ctrlK)
	typed(f.Fixture, "Guessed title")
	f.Send(enter)
	// The call is held open: the row already shows the new title.
	pollScreen(t, f.Fixture, listed("Guessed title"))
	held.pass()
	pollContains(t, f.Fixture, "Session name: Guessed title")
	if !listed("Guessed title")(f.Screen()) {
		t.Fatal("the renamed row is not listed")
	}
	// A pin: with only pinned sessions listed, the row stays listed while the call is held open.
	held.reset()
	f.Send(altP)
	f.Send(ctrlN)
	pollContains(t, f.Fixture, "Pinned: Only")
	if !listed("Guessed title")(f.Screen()) {
		t.Fatal("the pinned row is not listed while its call is held")
	}
	held.pass()
	pollScreen(t, f.Fixture, matches(`(?m)^\s*Pinned\s*$`))
	f.Send(ctrlN)
	// A refused rename: shown, then the old title is back and the error is a status line.
	held.reset()
	f.Send(down)
	f.Send(ctrlR)
	pollContains(t, f.Fixture, "Rename session")
	f.Send(ctrlA)
	f.Send(ctrlK)
	typed(f.Fixture, "Refused title")
	f.Send(enter)
	pollScreen(t, f.Fixture, listed("Refused title"))
	held.refuse("titles are frozen")
	pollScreen(t, f.Fixture, listed("Guessed title"))
	tuitest.Poll(t, noticeWith(f.Fixture, components.NoticeError, "titles are frozen"))
}

func TestSessionSelectorLifecycleOperationsMarkTheirRowsAFailureIsAnnouncedAndDismissFailedClearsIt(t *testing.T) {
	var dismissed []string
	var mu sync.Mutex
	running := delta.JsonObjectOf("phase", "running", "id", "op1", "kind", "relocate", "session", "s", "target", nil, "copying", false)
	stub := p2test.Stub{Contract: contract.Sessions, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		operations := self.Cell([]any{running})
		noop := func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }
		return p2test.StubMembers{
			Variant: "owner",
			State:   map[string]any{"sessions": rowsOf(fixtureSession()), "defaultCwd": "/work", "defaultEnv": nil, "operations": operations},
			Methods: map[string]p2test.StubMethod{
				"dismissOperation": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					id := args.(*delta.JsonObject).Value("operation").(string)
					mu.Lock()
					dismissed = append(dismissed, id)
					mu.Unlock()
					_ = operations.Change(func(current any) any {
						var kept []any
						for _, each := range current.([]any) {
							if each.(*delta.JsonObject).Value("id") != id {
								kept = append(kept, each)
							}
						}
						return append([]any{}, kept...)
					})
					return nil, nil
				},
				// The worker refuses the move: the job ends failed.
				"rename": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					_ = operations.Replace([]any{delta.JsonObjectOf("phase", "failed", "id", "op1", "kind", "relocate", "session", "s", "target", nil, "error", "Stop the current run first")})
					return nil, nil
				},
				"create": noop, "relocate": noop, "pin": noop, "archive": noop, "restore": noop, "clone": noop, "fork": noop,
			},
		}
	}}
	f := tuitest.NewTopology(t, tuitest.TopologyOptions{Server: []p2test.Stub{stub}, Facets: []host.Facet{tui.CoreFacet, sessionstui.Facet}, Columns: 100})
	f.Do(func() { f.Runtime.Navigate("sessions", "s", true, nil) })
	pollScreen(t, f.Fixture, matches(`Fixture.*moving…`))
	// (The stub turns the running move into a failed one on any rename.)
	f.Do(func() { f.Runtime.Ctx.Surface("sessions").(*sessionstui.Surface).RenameSession("s", "x") })
	tuitest.Poll(t, noticeWith(f.Fixture, components.NoticeError, "Move failed: Stop the current run first"))
	pollScreen(t, f.Fixture, matches(`Fixture.*Move failed`))
	ran := false
	f.Do(func() { ran = f.Runtime.Ctx.Commands().Run("p2.session.dismissFailed", "") })
	if !ran {
		t.Fatal("/dismiss-failed did not run")
	}
	tuitest.Poll(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(dismissed) == 1 })
	pollScreen(t, f.Fixture, func(screen string) bool { return !containsText(screen, "Move failed ") })
}
