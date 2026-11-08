package host

import (
	"context"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
)

// pi: packages/chord/test/remote/hold.test.ts
// Ports the describe 'calls over a link while a restart waits (decision "Reload restarts every affected object")' of
// hold.test.ts (Pi a5d209afd). A `Provider` and a remote `Gated` over it live on the server; a client holds `Gated` over a
// memory link. The describe it replaces (the swap's gate, Pi 0c2b568fd) goes with the swap; the call that waited at the gate
// across a `loading` unbind is in remote_hold_kept_test.go.

// gateProvider is `Provider`: its name, its keys and `set`, which writes key `x`.
var gateProvider = mustContract(declareLocal("app.gate-provider", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("name", contracts.Str()),
		contracts.Mem("entries", contracts.Keyed(contracts.Obj(contracts.Prop("n", contracts.Int())))),
		contracts.Mem("set", contracts.NewMethod(contracts.Int())),
	},
}))

// gateEntries is `Entries`; with persist, `PersistedEntries`.
func gateEntries(persist bool) *documents.StateDefinition {
	options := documents.StateOptions{
		Schema: contracts.Obj(contracts.Prop("n", contracts.Int())), Version: 1, Keyed: true,
		Initial: func() *delta.JsonObject { return jsonObject("n", 0.0) },
	}
	if persist {
		options.Persist = true
		options.CheckpointWhen = func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false }
	}
	definition, err := documents.DefineState("entries", options)
	if err != nil {
		panic(err)
	}
	return definition
}

var (
	gateEntriesDoc          = gateEntries(false)
	gatePersistedEntriesDoc = gateEntries(true)
)

// gateSlow is `Slow`: a local type whose native opens once `opened` fires.
var gateSlow = mustContract(declareLocal("app.gate-slow", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("name", contracts.Str())},
}))

// gateGated is `Gated`: a remote object over the provider; `bump` answers its code's number.
var gateGated = mustContract(declareRemote("app.gated", contracts.RemoteDeclaration{
	Version: 1, HostAddress: exServerAddress,
	Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{
		State:   []contracts.Property{rhProp("label", contracts.Str())},
		Methods: []contracts.MethodEntry{contracts.Meth("bump", contracts.NewMethod(contracts.Schema{}, contracts.Int()))},
	})},
}))

// gateServeSlow serves `Slow`, whose native opens once opened fires.
func gateServeSlow(t testing.TB, h FacetHost, opened signal) error {
	return h.Serve(gateSlow, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
		self.Open(func(ctx context.Context) (any, error) { opened.await(t, ctx); return nil, nil },
			func(any, NativeCloseReason, context.Context) error { return nil })
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"name": constant("slow")},
		}}, nil
	}, ServeOptions{})
}

// gateServeProvider serves the provider labelled name, needing `Slow` when slow, its keys persisted when persisted.
func gateServeProvider(h FacetHost, name string, slow, persisted bool) error {
	return h.Serve(gateProvider, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
		if slow {
			self.Get(gateSlow, jsonObject())
		}
		definition := gateEntriesDoc
		if persisted {
			definition = gatePersistedEntriesDoc
		}
		entries := self.State(definition).(*documents.Keyed)
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"name": constant(name)},
			Keyed: map[string]contracts.KeyedWatchable[delta.JsonValue]{"entries": ExposeKeyed(entries)},
			Methods: map[string]contracts.MethodFunction{"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				_, err := entries.Change(ctx, "x", func(draft *delta.Object) (any, error) { return nil, draft.Set("n", args) })
				return nil, err
			}},
		}}, nil
	}, ServeOptions{})
}

// gateServeGated serves `Gated` as code: its label over the provider's name (or its key `k` when pin), `bump` first
// awaiting gate (when not nil); needing `Slow` too when slow.
// Pi parks a call by awaiting a promise it resolves from the test; Go tests hold the call with a signal channel (`gate`) that
// the test fires, and learn that it arrived from a second one (`entered`), so no test waits on time.Sleep and a call's
// position (at the gate, in flight, answered) is a received event rather than a poll of timing.
func gateServeGated(t testing.TB, h FacetHost, code int, gate, entered signal, pin, slow bool) error {
	return h.Serve(gateGated, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
		provider := self.Get(gateProvider, jsonObject())
		if slow {
			self.Get(gateSlow, jsonObject())
		}
		var label chord.Watchable[delta.JsonValue]
		if pin {
			entry, err := provider.Keyed("entries").At("k")
			if err != nil {
				return contracts.Implementation{}, err
			}
			label = self.Derive([]any{Expose(entry)}, func(values []any) (delta.JsonValue, error) {
				return "v" + strconv.Itoa(code) + " " + strconv.Itoa(int(values[0].(*delta.JsonObject).Value("n").(float64))), nil
			})
		} else {
			label = self.Derive([]any{provider.Member("name")}, func(values []any) (delta.JsonValue, error) {
				return "v" + strconv.Itoa(code) + " " + values[0].(string), nil
			})
		}
		return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"label": label},
			Methods: map[string]contracts.MethodFunction{"bump": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				if entered != nil {
					entered.fire()
				}
				if gate != nil {
					gate.await(t, ctx)
				}
				return float64(code), nil
			}},
		}}, nil
	}, ServeOptions{})
}

func TestCallsOverALinkWhileARestartWaitsACallArrivingWhileTheOldAttemptDrainsAndTheSuccessorsRequestOpensWaitsForTheNewCodeNeverNotReady(t *testing.T) {
	gate, opened, entered := newSignal(), newSignal(), newSignal()
	t.Cleanup(gate.fire)
	t.Cleanup(opened.fire)
	s := linked(t, func(h FacetHost) error {
		must(t, gateServeProvider(h, "p1", false, false))
		return gateServeGated(t, h, 1, gate, entered, false, false)
	}, linkedOptions{})
	defer closeHosts(t, s.Client, s.Server)
	gated := refGet(t, s.Client, gateGated, jsonObject())
	old := startReloadCall(gated, "bump", nil)
	entered.wait(t)
	rhLinks(t, s)
	report := reloadMust(t)(reloadApp(s.Server, func(h FacetHost) error {
		must(t, gateServeSlow(t, h, opened))
		must(t, gateServeProvider(h, "p2", false, false))
		return gateServeGated(t, h, 2, nil, nil, false, true)
	}))
	// The new code of `Gated` needs `Slow`, a new type still opening: its old attempt stays open meanwhile.
	reloadWantObjects(t, report, ReloadedObject{Object: "app.gate-provider", Kind: ReloadRestarted}, ReloadedObject{Object: "app.gated", Kind: ReloadRestarted})
	// The old attempt still runs its call, so the hold stays bound: the client sees live and sends new work.
	outcome := startReloadCall(gated, "bump", nil)
	// Not `rhLinks`: it waits for the teardowns the hosts still run, and the old attempt's drain waits for its running call.
	transported(s.Links[0], s.Links[1])
	deliveredOn(s.Server, s.Client)
	if state := stateOfRef(gated); state.Kind != RefStateLive {
		t.Fatalf("state = %+v, want live", state)
	}
	opened.fire()
	gate.fire()
	if got := old.wait(t); got != 1.0 {
		t.Fatalf("old bump = %v, want 1", got)
	}
	if got := outcome.wait(t); got != 2.0 {
		t.Fatalf("bump = %v, want 2", got)
	}
	rhLinks(t, s)
	scopeEventually(t, "the label follows the new code", func() bool {
		value, err := gated.Member("label").Value()
		return err == nil && value == "v2 p2"
	})
}

// A reload runs the new factory on a recording self first; its references are inert and take `at(key)` as Pi's INERT does.
func TestReloadARecordedFactoryMayPinAKeyOnItsInertReference(t *testing.T) {
	s := linked(t, func(h FacetHost) error {
		must(t, gateServeProvider(h, "p1", false, false))
		return gateServeGated(t, h, 1, nil, nil, false, false)
	}, linkedOptions{})
	defer closeHosts(t, s.Client, s.Server)
	gated := refGet(t, s.Client, gateGated, jsonObject())
	report := reloadMust(t)(reloadApp(s.Server, func(h FacetHost) error {
		must(t, gateServeProvider(h, "p1", false, false))
		return gateServeGated(t, h, 2, nil, nil, true, false)
	}))
	if len(report.Failed) != 0 {
		t.Fatalf("failed = %+v, want none", report.Failed)
	}
	reloadWantObjects(t, report, ReloadedObject{Object: "app.gate-provider", Kind: ReloadRestarted}, ReloadedObject{Object: "app.gated", Kind: ReloadRestarted})
	rhLinks(t, s)
	scopeEventually(t, "the label follows the pinned key", func() bool {
		value, err := gated.Member("label").Value()
		return err == nil && value == "v2 0"
	})
}
