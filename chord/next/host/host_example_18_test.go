package host

// pi: packages/chord/test/next/examples/18-routines.ts

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
)

// --- 18-routines.ts ------------------------------------------------------------------------------------------------

func TestExample18Routines(t *testing.T) {
	session := exObj(exProp("archived", exBool()))
	catalog := exLocal("example.catalog", contracts.Params{},
		contracts.Mem("sessions", contracts.Map(session)), contracts.Mem("archive", contracts.NewMethod(exObj(exProp("session", exStr())))))
	forwardStore := exLocal("example.forwards", contracts.Params{}, contracts.Mem("open", exArr(exStr())), contracts.Mem("cleanups", exArr(exStr())))
	sessions := exDefineState("example.sessions", documents.StateOptions{
		Schema: exObj(exProp("sessions", contracts.Map(session))),
		Initial: func() *delta.JsonObject {
			return jsonObject("sessions", jsonObject("s1", jsonObject("archived", false), "s2", jsonObject("archived", false)))
		},
	})
	forwardList := exDefineState("example.forward-list", documents.StateOptions{
		Schema:  exObj(exProp("open", exArr(exStr())), exProp("cleanups", exArr(exStr()))),
		Initial: func() *delta.JsonObject { return jsonObject("open", []any{"s1", "s2"}, "cleanups", []any{}) },
	})
	// 1. The host reports background failures: a routine call that threw arrives as `routine_failed`.
	var reported exCollector
	errs := &hostErrors{}
	host := newHost(t, HostOptions{OnError: func(err HostError) {
		if err.Kind == ErrRoutineFailed {
			reported.add(fmt.Sprintf("%s %s #%d failed: %v", err.Object, err.Routine.Kind, err.Routine.Position, err.Error))
			return
		}
		errs.push(err)
	}})
	var flaky = struct {
		sync.Mutex
		on bool
	}{on: true}
	var ons exCollector
	mustLoad(t)(host.Load([]HostFacet{{Name: "@example/forwards", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(catalog, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, sessions)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"sessions": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("sessions"), nil
				}),
			}, map[string]contracts.MethodFunction{
				"archive": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return nil, doc.Change(func(d *delta.Object) {
						_ = d.Object("sessions").Set(exParam(args, "session"), jsonObject("archived", true))
					}, ctx)
				}),
			}), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(forwardStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			forwards := stateOf(self, forwardList)
			catalogRef := self.Get(catalog, jsonObject())
			// 2. Reconcile: close every forward of an archived session. Idempotent, from the current value; the first
			// close fails once (a flaky remote), and the retry comes with the whole value.
			self.On(catalogRef.Member("sessions"), func(call chord.WatchCall[delta.JsonValue], ctx Ctx) *Handled {
				ons.add(exText(call.Ops))
				current := call.Value.(*delta.JsonObject)
				open := forwards.Value().Value("open").([]any)
				// The handler's awaits (Pi's async function) run off the loop.
				return Awaiting(exLoop(ctx), func() error {
					for _, name := range open {
						archived, _ := current.Value(name.(string)).(*delta.JsonObject)
						if archived == nil || archived.Value("archived") != true {
							continue
						}
						flaky.Lock()
						fail := flaky.on
						flaky.on = false
						flaky.Unlock()
						if fail {
							return fmt.Errorf("closing the forward of %s timed out", name)
						}
						if err := forwards.Change(func(d *delta.Object) {
							var kept []any
							for _, existing := range d.Array("open").Snapshot() {
								if existing != name {
									kept = append(kept, existing)
								}
							}
							_ = d.Set("open", kept)
						}, ctx); err != nil {
							return err
						}
					}
					return nil
				})
			})
			// 3. A periodic task: a loop with `sleep`, ended by the object's end (the abort rejects `sleep`).
			self.OnLive(func(ctx Ctx) *Handled {
				return Awaiting(exLoop(ctx), func() error {
					for {
						if err := Sleep(400*time.Millisecond, ctx); err != nil {
							return nil
						}
						if err := forwards.Change(func(d *delta.Object) { _, _ = d.Array("cleanups").Push("tick") }, ctx); err != nil {
							return nil
						}
					}
				})
			})
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"open": self.Derive([]any{forwards.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("open"), nil
				}),
				"cleanups": self.Derive([]any{forwards.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("cleanups"), nil
				}),
			}, nil), nil
		}, ServeOptions{})
	})}}, background))
	store := exGet(t, host, forwardStore, jsonObject())
	catalogRef := exGet(t, host, catalog, jsonObject())
	// 4. Archive s2: the routine's call throws once, is retried after 1 s with the whole value, and closes the forward.
	exCall(t, catalogRef, "archive", jsonObject("session", "s2"))
	scopeEventually(t, "the failed close was reported", func() bool { return len(reported.all()) == 1 })
	exEqual(t, "reported", reported.all(), []any{"example.forwards on #1 failed: closing the forward of s2 timed out"})
	scopeEventually(t, "open forwards", func() bool { return exJSONEqual(exValue(t, store, "open"), exJSON(t, `["s1"]`)) })
	scopeEventually(t, "periodic cleanups so far", func() bool { return len(exValue(t, store, "cleanups").([]any)) > 0 })
	if got := ons.all(); len(got) < 2 {
		t.Fatalf("on calls = %v, want the first value and the archive's", got)
	}
	// 5. Closing the host aborts the routines: the periodic loop ends in its `sleep`, nothing is reported.
	exCloseHost(t, host)
	if len(reported.all()) != 1 || errs.Len() > 0 {
		t.Fatalf("closed with reports %v %v", reported.all(), errs.All())
	}
}
