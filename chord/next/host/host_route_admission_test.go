package host

import (
	"context"
	"errors"
	"strings"
	"testing"
	"weak"

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
// Forced Go differences. Pi's `await transported()` drains every microtask, the route's settle included:
// a Go settle is an owned task, so `settled` (below) waits for every decision of the route to leave `settling` (a condition on
// the admission's own state, no sleep) around the link flush. A positive expectation of a hold's state waits for it
// (untilKind); a negative one (nothing resolved, nothing forwarded) is read after `settled`. A hold's calls block, so a call in
// flight runs on a goroutine whose result the test receives.
// The settle of a decision is a task the route owns: what a hold shows because of it is read after `settled`.

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

// decisionTracker is Pi's `instancesOf(Decision, "declare")`: every decision of the route seen so far, weakly (a snapshot of
// the route's decisions: no hook in the code under test).
type decisionTracker struct {
	seen  []weak.Pointer[Decision]
	known map[weak.Pointer[Decision]]bool
}

// gatedRelay is Pi's `routeGated()`: a relay whose route asks the admission, and how the test sets a principal kind's mode.
type gatedRelay struct {
	*relayed
	t     testing.TB
	gate  *Reference
	raw   *rawWorkers
	links []*link.MemoryLink
	track decisionTracker
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
	return &gatedRelay{relayed: rel, t: t, gate: mustGet(t, rel.Server, Gate, jsonObject()), raw: raw, track: decisionTracker{known: map[weak.Pointer[Decision]]bool{}}}
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

// route is the server's route of `Session`.
func (g *gatedRelay) route() *route { return g.Server.(*chordHost).routes["session"] }

// settled is `await transported()` after something an admission decides: what is in flight arrived, every decision of the
// route left `settling`, and what that made arrived.
func (g *gatedRelay) settled() {
	g.t.Helper()
	g.Transported()
	chord := g.Server.(*chordHost)
	until(g.t, "every decision of the route settled", func() bool {
		idle := true
		chord.enter(func() {
			for _, asked := range g.route().admission.asked {
				if asked.state.kind == askedSettling {
					idle = false
				}
			}
		})
		return idle
	})
	g.Transported()
}

// decisions snapshots the route's decisions into the tracker and returns how many were seen so far.
func (g *gatedRelay) decisions() int {
	g.Server.(*chordHost).enter(func() {
		for _, asked := range g.route().admission.asked {
			made := weak.Make(asked.decision)
			if !g.track.known[made] {
				g.track.known[made] = true
				g.track.seen = append(g.track.seen, made)
			}
		}
	})
	return len(g.track.seen)
}

// tabOf is a client connected to `server` as principal.
func (g *gatedRelay) tabOf(principal data.Principal) Host {
	g.t.Helper()
	tab := newHost(g.t, HostOptions{OnError: func(HostError) {}})
	mustLoad(g.t)(tab.Load(nil, background))
	a, b := link.MemoryLinkPair()
	must(g.t, g.Server.Accept(a, LinkOptions{Principal: principal, Codec: testCodec()}, background))
	must(g.t, tab.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
	g.links = append(g.links, a, b)
	return tab
}

// rawTab is a raw tab on a new link the server accepts as principal.
func (g *gatedRelay) rawTab(principal data.Principal) *RawPeer {
	g.t.Helper()
	a, b := link.MemoryLinkPair()
	must(g.t, g.Server.Accept(a, LinkOptions{Principal: principal, Codec: testCodec()}, background))
	g.links = append(g.links, a, b)
	return newRawPeer(g.t, b, rawPeerOptions{})
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

// wireTold is toldOf of a wire error.
func wireTold(t testing.TB, wire link.WireError) errorTold {
	t.Helper()
	received, err := link.ErrorFromWire(wire)
	must(t, err)
	return toldOf(t, received)
}

func workerCount(rel *relayed) int {
	count := 0
	rel.Workers.Range(func(any, any) bool { count++; return true })
	return count
}

func workerReferences(rel *relayed, key string) int {
	worker := rel.Worker(key)
	if worker == nil {
		return 0
	}
	return activityOf(worker).References
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
		untilKind(t, notes, RefStateLive)
		g.settled()
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
		untilKind(t, notes, RefStateLive)
		g.settled()
		if resolves := g.Resolves(); len(resolves) != 1 || resolves[0] != "session|S1" {
			t.Fatalf("resolves = %v, want session|S1", resolves)
		}
	})
}

// Pi: "before the first allow › an admission holding a type placed behind its own route fails the hold invalid/cycle; a
// throwing admit breaks it". Covers: route.path's ADMITTING refusal (production draft), decision.declare's error.
func TestRouteAdmissionBeforeTheFirstAllowAnAdmissionHoldingATypePlacedBehindItsOwnRouteFailsTheHoldInvalidCycleAThrowingAdmitBreaksIt(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		cyclic := relay(t, relayedOptions{Admit: func(_ data.Principal, address contracts.Address, self AdmitSelf) (any, error) {
			behind := self.Get(PlacedNotes, jsonObject("session", address.Params.Value("id")))
			return self.Derive([]any{behind.Member("count")}, func([]any) (delta.JsonValue, error) { return Allow(nil), nil }), nil
		}})
		defer closeHosts(t, cyclic.Tab, cyclic.Server)
		notes := mustHold(t, cyclic.Tab, PlacedNotes, jsonObject("session", "S1"))
		untilKind(t, notes, RefStateFailed)
		cyclic.Transported()
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
		untilKind(t, broken, RefStateFailed)
		throwing.Transported()
		cause = failedWith(t, broken)
		if cause.Kind != data.FailureBroken || !strings.Contains(cause.Error.Message, "admit threw") {
			t.Fatalf("cause = %s %q, want broken: admit threw", cause.Kind, cause.Error.Message)
		}
		if resolves := throwing.Resolves(); len(resolves) != 0 {
			t.Fatalf("resolves = %v, want none", resolves)
		}
	})
}

