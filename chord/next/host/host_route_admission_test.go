package host

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/test/host/route-admission.test.ts @7cc21c24f6b5 (slice 14 §2.3; test list 15): nothing is resolved or
// forwarded before the first allow, so a denied hold starts no host; an allow binds; a denial releases the principal's holds
// upstream and rejects their calls `forbidden/revoked`; the fence after a denial; one decision per principal and address; a
// failed source; `RouteHandle.retry`; a hold behind the route's own route (`invalid/cycle`).
//
// Forced Go differences. Pi's `await transported()` drains every microtask, the route's settle included: a Go settle is a
// joined hop of the server's loop (host_route_admission.go), so `settled` (below) is `transported()` plus `deliveredOn`: no
// wait of its own, and a hold's state is read right after it. A hold's calls block, so a call in flight is started in one
// section (startCallWith) and its result awaited after, as Pi awaits the promise `notes.add(...)` returned.

// --- the gate -----------------------------------------------------------------------------------------------------------

var modesDoc = func() *documents.StateDefinition {
	definition, err := documents.DefineState("gate.modes", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("modes", contracts.Map(contracts.Str()))),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("modes", jsonObject()) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

// Gate is who may reach which session, by principal kind: the store the route's admission reads.
var Gate = mustContract(contracts.Local("test.gate", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("modes", contracts.Map(contracts.Str())),
		contracts.Mem("set", contracts.NewMethod(contracts.Obj(contracts.Prop("who", contracts.Str()), contracts.Prop("mode", contracts.Str())))),
	},
}))

// serveGate serves Gate on the server.
func serveGate(facet FacetHost) error {
	return facet.Serve(Gate, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
		doc := stateOf(self, modesDoc)
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"modes": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("modes"), nil
				}),
			},
			Methods: map[string]contracts.MethodFunction{
				"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					who := args.(*delta.JsonObject).Value("who").(string)
					mode := args.(*delta.JsonObject).Value("mode")
					return nil, doc.Change(func(draft *delta.Object) { _ = draft.Object("modes").Set(who, mode) }, ctx)
				},
			},
		}}, nil
	}, ServeOptions{})
}

// gateAdmit is the route's admission: `yes` in the gate for the principal's kind allows; `throw` makes the decision throw.
func gateAdmit(principal data.Principal, _ contracts.Address, self AdmitSelf) (any, error) {
	modes := self.Get(Gate, jsonObject()).Member("modes")
	return self.Derive([]any{modes}, func(values []any) (delta.JsonValue, error) {
		mode, _ := values[0].(*delta.JsonObject).Value(principalKind(principal)).(string)
		if mode == "throw" {
			return nil, errors.New("the gate threw")
		}
		if mode == "yes" {
			return Allow(nil), nil
		}
		return Deny("no access"), nil
	}), nil
}

// --- the rig ------------------------------------------------------------------------------------------------------------

// gatedRelay is Pi's `routeGated()`: a relay whose route asks the admission, and how the test sets a principal kind's mode.
type gatedRelay struct {
	*relayed
	t     testing.TB
	gate  *Reference
	raw   *rawWorkers
	links []*link.MemoryLink
	// tabs are the hosts of the extra tabs (tabOf).
	tabs []Host
}

// gated is `routeGated(options)`: options override the route's admission and the server's facet. raw is the rawWorkers the
// options' Resolve makes peers with, when it does.
func routeGated(t testing.TB, options relayedOptions, raw *rawWorkers) *gatedRelay {
	t.Helper()
	if options.Admit == nil {
		options.Admit = gateAdmit
	}
	if options.Server == nil {
		options.Server = serveGate
	}
	rel := relay(t, options)
	return &gatedRelay{relayed: rel, t: t, gate: mustGet(t, rel.Server, Gate, jsonObject()), raw: raw}
}

// set is `set(mode, who)`: who's mode in the gate (the owner's by default).
func (g *gatedRelay) set(mode string, who ...string) {
	g.t.Helper()
	name := "owner"
	if len(who) > 0 {
		name = who[0]
	}
	_, err := g.gate.Call("set", jsonObject("who", name, "mode", mode), background)
	must(g.t, err)
}

// Transported is Pi's `transported()` for every link of the rig: the relay's, the raw workers', the extra tabs'.
func (g *gatedRelay) Transported() {
	g.relayed.Transported()
	if g.raw != nil {
		g.raw.Transported()
	}
	transported(g.links...)
}

// hostsOf are the hosts of a relay Pi's `transported()` runs the microtasks of: the server, the tab and the latest worker
// of every address.
func hostsOf(rel *relayed) []Host {
	hosts := []Host{rel.Server, rel.Tab}
	rel.Workers.Range(func(_, worker any) bool {
		hosts = append(hosts, worker.(Host))
		return true
	})
	return hosts
}

// settled is Pi's `await transported()` after something an admission decides: what is in flight arrived, the settles the
// change started (joined hops of the server's loop) and the deliveries they made ran to their end (deliveredOn), and what
// that made arrived. No condition of its own.
func (g *gatedRelay) settled() {
	g.t.Helper()
	g.Transported()
	deliveredOn(append(hostsOf(g.relayed), g.tabs...)...)
	g.Transported()
}

// settledOn is settled for a relay with no links of its own.
func settledOn(rel *relayed) {
	rel.Transported()
	deliveredOn(hostsOf(rel)...)
	rel.Transported()
}

// wantKind is `expect(ref.state.value.kind).toBe(kind)`, read at once: Pi's test made its `await transported()` before it.
func wantKind(t testing.TB, ref *Reference, kind RefStateKind) {
	t.Helper()
	if got := kindOf(ref); got != kind {
		t.Fatalf("state = %s, want %s", got, kind)
	}
}

// kindOf is `ref.state.value.kind`.
func kindOf(ref *Reference) RefStateKind { return stateOfRef(ref).Kind }

// failedWith is the cause `ref` is failed with; the test fails when it is not failed.
func failedWith(t testing.TB, ref *Reference) data.FailureCause {
	t.Helper()
	current := stateOfRef(ref)
	if current.Kind != RefStateFailed {
		t.Fatalf("state = %s, want failed", current.Kind)
	}
	return current.Cause
}

// wantDenied fails unless ref is failed `denied`.
func wantDenied(t testing.TB, ref *Reference) data.FailureCause {
	t.Helper()
	cause := failedWith(t, ref)
	if cause.Kind != data.FailureDenied {
		t.Fatalf("cause = %s, want denied", cause.Kind)
	}
	return cause
}

// errorTold is `{ code, reason, message }` of err (a ChordError), as Pi's `told`: no reason is "".
type errorTold struct{ Code, Reason, Message string }

func toldOf(t testing.TB, err error) errorTold {
	t.Helper()
	chordErr, ok := data.IsChordError(err)
	if !ok {
		t.Fatalf("error = %v, want a ChordError", err)
	}
	return errorTold{string(chordErr.Code), string(chordErr.Reason), chordErr.Message}
}

func workerCount(rel *relayed) int {
	count := 0
	rel.Workers.Range(func(any, any) bool { count++; return true })
	return count
}

// --- before the first allow ---------------------------------------------------------------------------------------------

// Pi: "before the first allow › nothing is resolved or forwarded: a denied hold starts no host; an allow later binds it".
// Covers (part B): relayedHold.withheld (a denial told to a hold that never left the server), providerSide.withheld,
// relayedHold.admitted (heldBack set, so `loading` goes downstream), path.forward at a first allow.
func TestRouteAdmissionBeforeTheFirstAllowNothingIsResolvedOrForwardedADeniedHoldStartsNoHostAnAllowLaterBindsIt(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		g := routeGated(t, relayedOptions{}, nil)
		defer closeHosts(t, g.Tab, g.Server)
		notes := mustHold(t, g.Tab, PlacedNotes, jsonObject("session", "S1"))
		if got := kindOf(notes); got != RefStateLoading {
			t.Fatalf("state = %s, want loading", got)
		}
		g.settled()
		cause := wantDenied(t, notes)
		if cause.Error.Code != data.CodeForbidden || cause.Error.Message != "no access" {
			t.Fatalf("cause = %+v, want forbidden: no access", cause.Error)
		}
		_, err := g.Tab.Get(background, PlacedNotes, jsonObject("session", "S1"), GetOptions{})
		if got := toldOf(t, err); got != (errorTold{"forbidden", "", "no access"}) {
			t.Fatalf("get = %+v, want forbidden: no access", got)
		}
		if resolves, workers := g.Resolves(), workerCount(g.relayed); len(resolves) != 0 || workers != 0 {
			t.Fatalf("resolves = %v, workers = %d, want none", resolves, workers)
		}
		g.set("yes")
		g.settled()
		wantKind(t, notes, RefStateLive)
		if count, err := notes.Member("count").Value(); err != nil || count != float64(0) {
			t.Fatalf("count = %v, %v, want 0", count, err)
		}
		if resolves := g.Resolves(); len(resolves) != 1 || resolves[0] != "session|S1" {
			t.Fatalf("resolves = %v, want session|S1", resolves)
		}
		for name, errs := range map[string]*hostErrors{"server": g.Errors.Server, "tab": g.Errors.Tab, "worker": g.Errors.Worker} {
			if errs.Len() != 0 {
				t.Fatalf("%s errors = %v, want none", name, errs.All())
			}
		}
	})
}

// Pi: "before the first allow › RouteHandle.retry resolves nothing while every hold of the address is denied or undecided".
// Covers (part B): path.wanted (a retry resolves only for a wanted holder: route.resolveIfHeld).
func TestRouteAdmissionBeforeTheFirstAllowRouteHandleRetryResolvesNothingWhileEveryHoldOfTheAddressIsDeniedOrUndecided(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		g := routeGated(t, relayedOptions{}, nil)
		defer closeHosts(t, g.Tab, g.Server)
		notes := mustHold(t, g.Tab, PlacedNotes, jsonObject("session", "S1"))
		address := Session.Address(jsonObject("id", "S1"))
		g.Transported()
		g.Route.Retry(address)
		g.settled()
		g.Route.Retry(address)
		g.settled()
		wantDenied(t, notes)
		if resolves := g.Resolves(); len(resolves) != 0 {
			t.Fatalf("resolves = %v, want none", resolves)
		}
		g.set("yes")
		g.settled()
		wantKind(t, notes, RefStateLive)
		if resolves := g.Resolves(); len(resolves) != 1 || resolves[0] != "session|S1" {
			t.Fatalf("resolves = %v, want session|S1", resolves)
		}
	})
}

// Pi: "before the first allow › an admission holding a type placed behind its own route fails the hold invalid/cycle; a
// throwing admit breaks it". Covers: route.path's ADMITTING refusal (host_route_admission.go), decision.declare's error.
func TestRouteAdmissionBeforeTheFirstAllowAnAdmissionHoldingATypePlacedBehindItsOwnRouteFailsTheHoldInvalidCycleAThrowingAdmitBreaksIt(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		cyclic := relay(t, relayedOptions{Admit: func(_ data.Principal, address contracts.Address, self AdmitSelf) (any, error) {
			behind := self.Get(PlacedNotes, jsonObject("session", address.Params.Value("id")))
			return self.Derive([]any{behind.Member("count")}, func([]any) (delta.JsonValue, error) { return Allow(nil), nil }), nil
		}})
		defer closeHosts(t, cyclic.Tab, cyclic.Server)
		notes := mustHold(t, cyclic.Tab, PlacedNotes, jsonObject("session", "S1"))
		settledOn(cyclic)
		wantKind(t, notes, RefStateFailed)
		cause := failedWith(t, notes)
		if cause.Kind != data.FailureBroken || cause.Error.Code != data.CodeInvalid || cause.Error.Reason != data.ReasonCycle {
			t.Fatalf("cause = %s %+v, want broken invalid/cycle", cause.Kind, cause.Error)
		}
		if resolves := cyclic.Resolves(); len(resolves) != 0 {
			t.Fatalf("resolves = %v, want none", resolves)
		}

		throwing := relay(t, relayedOptions{Admit: func(data.Principal, contracts.Address, AdmitSelf) (any, error) {
			return nil, errors.New("admit threw")
		}})
		defer closeHosts(t, throwing.Tab, throwing.Server)
		broken := mustHold(t, throwing.Tab, PlacedNotes, jsonObject("session", "S1"))
		settledOn(throwing)
		wantKind(t, broken, RefStateFailed)
		cause = failedWith(t, broken)
		if cause.Kind != data.FailureBroken || !strings.Contains(cause.Error.Message, "admit threw") {
			t.Fatalf("cause = %s %q, want broken: admit threw", cause.Kind, cause.Error.Message)
		}
		if resolves := throwing.Resolves(); len(resolves) != 0 {
			t.Fatalf("resolves = %v, want none", resolves)
		}
	})
}
