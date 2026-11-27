package host

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/state"
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
	return facet.Serve(Gate, func(_ *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
		doc := stateOf(self, modesDoc)
		return contracts.MembersImplementation{
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
		}, nil
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

// settled is Pi's `await transported()` after something an admission decides. Pi's `transported()` is six `flushed()`
// (test/support.ts:604-606), each draining the microtasks of every host of the process, so a frame a hop's delivery put on a link
// arrives in the next. Here a hop is `Transported()` (what is in flight arrived) and `deliveredOn` over the hosts as they are
// then (the settles and deliveries the arrival started ran to their end, a worker a settle made included); six hops, Pi's count,
// then what the last one sent arrived. No condition of its own.
func (g *gatedRelay) settled() {
	g.t.Helper()
	for range 6 {
		g.Transported()
		deliveredOn(append(hostsOf(g.relayed), g.tabs...)...)
	}
	g.Transported()
}

// settledOn is settled for a relay with no links of its own.
func settledOn(rel *relayed) {
	for range 6 {
		rel.Transported()
		deliveredOn(hostsOf(rel)...)
	}
	rel.Transported()
}

// wantKind is `expect(ref.state.value.kind).toBe(kind)`, read at once: Pi's test made its `await transported()` before it.
func wantKind(t testing.TB, ref *Reference, kind RefStateKind) {
	t.Helper()
	if got := kindOf(ref); got != kind {
		t.Fatalf("state = %s, want %s", got, kind)
	}
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
	g.tabs = append(g.tabs, tab)
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
// pi: packages/chord/src/host/route-admission.ts
func TestRouteAdmissionBeforeTheFirstAllowNothingIsResolvedOrForwardedADeniedHoldStartsNoHostAnAllowLaterBindsIt(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		g := routeGated(t, relayedOptions{}, nil)
		defer closeHosts(t, g.Tab, g.Server)
		// Hold and read the initial loading state in one client section so the memory link's denial
		// cannot land between Hold and kindOf (Pi: tab.hold(...) then expect(notes.state.value.kind) in one turn).
		var notes *Reference
		var initial RefStateKind
		g.Tab.(*chordHost).enter(func() {
			notes = mustHold(t, g.Tab, PlacedNotes, jsonObject("session", "S1"))
			initial = kindOf(notes)
		})
		if initial != RefStateLoading {
			t.Fatalf("state = %s, want loading", initial)
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

// --- a decision that changes while it is settled ------------------------------------------------------------------------

// deniedAfterItsRead is a decision that allows when first read and is denied by a commit queued in that very read.
type deniedAfterItsRead struct {
	state.Source
	host     *chordHost
	decision delta.JsonValue
	queued   bool
}

func newDeniedAfterItsRead(host *chordHost) *deniedAfterItsRead {
	source := &deniedAfterItsRead{host: host, decision: Allow(nil)}
	source.Init(host.batch, source)
	return source
}

func (s *deniedAfterItsRead) Value() (delta.JsonValue, error) {
	if !s.queued {
		s.queued = true
		s.host.loop().Queue(func() {
			_ = s.Batch().Within(func() error {
				s.decision = Deny("withdrawn")
				s.Changed()
				return nil
			}, background)
		})
	}
	return s.decision, nil
}

func (s *deniedAfterItsRead) Deliver(context.Context) error { return nil }

// Pi: "a decision that changes while it is settled › is not answered with what the settle read before: a denial between the
// read and its continuation starts nothing". Covers (host_route_admission.go): the `moved` flag of a settling decision, the settle
// loop; (part B) none: nothing is forwarded.
func TestRouteAdmissionADecisionThatChangesWhileItIsSettledIsNotAnsweredWithWhatTheSettleReadBeforeADenialBetweenTheReadAndItsContinuationStartsNothing(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		var source atomic.Pointer[deniedAfterItsRead]
		var rel *relayed
		rel = relay(t, relayedOptions{Admit: func(data.Principal, contracts.Address, AdmitSelf) (any, error) {
			// The server is a `chordHost`: its batch is what its sources commit in.
			if source.Load() == nil {
				source.Store(newDeniedAfterItsRead(rel.Server.(*chordHost)))
			}
			return source.Load(), nil
		}})
		defer closeHosts(t, rel.Tab, rel.Server)
		notes := mustHold(t, rel.Tab, PlacedNotes, jsonObject("session", "S1"))
		settledOn(rel)
		wantKind(t, notes, RefStateFailed)
		wantDenied(t, notes)
		if resolves, workers := rel.Resolves(), workerCount(rel); len(resolves) != 0 || workers != 0 {
			t.Fatalf("resolves = %v, workers = %d, want none", resolves, workers)
		}
	})
}

// Open is the store of the loop below: one flag the admission reads.
var Open = mustContract(contracts.Local("test.open", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("open", contracts.Str())},
}))

// Pi: the loop "ends denied whenever the denial arrives: ${turns} turns into the settle" (turns 0..7): the same through an
// ordinary source that changes `turns` microtasks after `admit` ran. A Go turn is one hop of the server's microtask queue
// (Loop.Queue); where the settle task's own sections interleave with them is the scheduler's, so the sweep covers whichever
// step of the settle the denial meets, as Pi's. Covers (host_route_admission.go): settle's moved/released checks; (part B) the
// release arm after a denial (relayedHold.withheld) when the denial lands after the forward.
func TestRouteAdmissionADecisionThatChangesWhileItIsSettledEndsDeniedWheneverTheDenialArrives(t *testing.T) {
	for turns := range 8 {
		t.Run(fmt.Sprintf("%d turns into the settle", turns), func(t *testing.T) {
			forEachCodec(t, func(t *testing.T) {
				var flag atomic.Pointer[state.Writable[any]]
				var rel *relayed
				rel = relay(t, relayedOptions{
					Server: func(h FacetHost) error {
						return h.Serve(Open, func(_ *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
							open := self.Writable("yes")
							flag.Store(&open)
							return contracts.MembersImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
								"open": self.Derive([]any{open}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
							}}, nil
						}, ServeOptions{})
					},
					Admit: func(_ data.Principal, _ contracts.Address, self AdmitSelf) (any, error) {
						loop := rel.Server.(*chordHost).loop()
						remaining := turns
						var flip func()
						flip = func() {
							if remaining > 0 {
								remaining--
								loop.Queue(flip)
							} else if open := flag.Load(); open != nil {
								_ = (*open).Set("no")
							}
						}
						loop.Queue(flip)
						open := self.Get(Open, jsonObject()).Member("open")
						return self.Derive([]any{open}, func(values []any) (delta.JsonValue, error) {
							if values[0] == "yes" {
								return Allow(nil), nil
							}
							return Deny("closed"), nil
						}), nil
					},
				})
				defer closeHosts(t, rel.Tab, rel.Server)
				notes := mustHold(t, rel.Tab, PlacedNotes, jsonObject("session", "S1"))
				g := &gatedRelay{relayed: rel, t: t}
				g.settled()
				wantKind(t, notes, RefStateFailed)
				wantDenied(t, notes)
				if references := workerReferences(rel, "session|S1"); references != 0 {
					t.Fatalf("worker references = %d, want 0", references)
				}
			})
		})
	}
}

// --- a denial -----------------------------------------------------------------------------------------------------------

// Pi: "a denial › unbinds the principal's holds: released at the worker, replicas dropped, calls in flight forbidden/revoked".
// Covers (part B): relayedHold.withheld (release upstream, `failed{denied}` downstream), providerSide.withheld +
// rejectRelayed(all) (the call in flight: forbidden/revoked), relayedHold.heldBack/admitted (`loading` after the next allow),
// relayedHold.unreached via the count read of the denied hold; path.forward behind the fence (the worker answers the ping).
func TestRouteAdmissionADenialUnbindsThePrincipalsHoldsReleasedAtTheWorkerReplicasDroppedCallsInFlightForbiddenRevoked(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		g := routeGated(t, relayedOptions{}, nil)
		defer closeHosts(t, g.Tab, g.Server)
		g.set("yes")
		notes := mustGet(t, g.Tab, PlacedNotes, jsonObject("session", "S1"))
		g.WorkerLoaded(t, "session|S1")
		worker := g.Worker("session|S1")
		if references := activityOf(worker).References; references != 1 {
			t.Fatalf("worker references = %d, want 1", references)
		}
		if granted, _ := notes.Granted(); !granted["add"] {
			t.Fatalf("granted = %v, want add", granted)
		}
		waiting := startNotesCall(t, notes, "wait")
		g.settled()
		g.set("no")
		g.settled()
		_, waitErr := parkedResultOf(t, waiting)
		if got := toldOf(t, waitErr); got.Code != "forbidden" || got.Reason != "revoked" {
			t.Fatalf("wait = %+v, want forbidden/revoked", got)
		}
		g.settled()
		wantDenied(t, notes)
		_, err := notes.Member("count").Value()
		if got := toldOf(t, err); got != (errorTold{"forbidden", "", "no access"}) {
			t.Fatalf("count = %+v, want forbidden: no access", got)
		}
		if granted, _ := notes.Granted(); len(granted) != 0 {
			t.Fatal("a denied hold still has grants")
		}
		if references := activityOf(worker).References; references != 0 {
			t.Fatalf("worker references = %d, want 0 after the denial", references)
		}
		// Allowed again: `loading` at once, not "no access" until the hold is bound; then forwarded behind the fence (the
		// worker answers the `ping`), under the tab's hold.
		watch, err := notes.State().Watch(background)
		must(t, err)
		states := statesOf(t, watch)
		g.set("yes")
		g.settled()
		wantKind(t, notes, RefStateLive)
		got := states.All()
		// The collector reports the state it started on first (the denial): dropped, Pi's lists what changed after.
		if len(got) > 0 && strings.HasPrefix(got[0], "failed") {
			got = got[1:]
		}
		if len(got) != 2 || got[0] != "loading" || got[1] != "live" {
			t.Fatalf("states = %v, want loading, live", got)
		}
		if count, err := notes.Member("count").Value(); err != nil || count != float64(0) {
			t.Fatalf("count = %v, %v, want 0", count, err)
		}
		if references := activityOf(worker).References; references != 1 {
			t.Fatalf("worker references = %d, want 1 after the allow", references)
		}
	})
}

// Pi: "a denial › is per principal and address: another principal's holds on the address stay bound". Covers (part B):
// the withhold of ONE holder (relayedHold.withheld) leaves the other decision's holder bound (path.withhold per holder).
func TestRouteAdmissionADenialIsPerPrincipalAndAddressAnotherPrincipalsHoldsOnTheAddressStayBound(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		g := routeGated(t, relayedOptions{}, nil)
		defer closeHosts(t, g.Tab, g.Server)
		g.set("yes")
		g.set("yes", "guest")
		guest := g.tabOf(delta.JsonObjectOf("kind", "guest", "session", "S1"))
		defer closeHosts(t, guest)
		owners := mustGet(t, g.Tab, PlacedNotes, jsonObject("session", "S1"))
		guests := mustGet(t, guest, PlacedNotes, jsonObject("session", "S1"))
		ownerGranted, _ := owners.Granted()
		guestGranted, _ := guests.Granted()
		if !ownerGranted["add"] || guestGranted["add"] {
			t.Fatalf("granted = %v, %v, want add/none", ownerGranted, guestGranted)
		}
		g.WorkerLoaded(t, "session|S1")
		g.set("no", "guest")
		g.settled()
		wantDenied(t, guests)
		if got := kindOf(owners); got != RefStateLive {
			t.Fatalf("owner state = %s, want live", got)
		}
		if references := workerReferences(g.relayed, "session|S1"); references != 1 {
			t.Fatalf("worker references = %d, want 1", references)
		}
	})
}

// --- one decision per principal and address -----------------------------------------------------------------------------

// decisionTracker is Pi's `instancesOf(Decision, "declare")`: every decision of the route seen so far, weakly (a snapshot of
// the route's decisions: no hook in the code under test).
type decisionTracker struct {
	g     *gatedRelay
	seen  []weak.Pointer[Decision]
	known map[weak.Pointer[Decision]]bool
}

// route is the server's route of `Session`.
func (g *gatedRelay) route() *route { return g.Server.(*chordHost).routes["session"] }

func trackDecisions(g *gatedRelay) *decisionTracker {
	return &decisionTracker{g: g, known: map[weak.Pointer[Decision]]bool{}}
}

// count snapshots the route's decisions into the tracker and returns how many were seen so far.
func (d *decisionTracker) count() int {
	d.g.Server.(*chordHost).enter(func() {
		for _, asked := range d.g.route().admission.asked {
			made := weak.Make(asked.decision)
			if !d.known[made] {
				d.known[made] = true
				d.seen = append(d.seen, made)
			}
		}
	})
	return len(d.seen)
}

// Pi: "one decision per principal and address › is shared by its holds and released with the last (GC)". Covers
// (host_route_admission.go): joined's shared decision, left's release. Mutation target: decision.release not called at the last holder.
func TestRouteAdmissionOneDecisionPerPrincipalAndAddressIsSharedByItsHoldsAndReleasedWithTheLastGC(t *testing.T) {
	g := routeGated(t, relayedOptions{}, nil)
	defer closeHosts(t, g.Tab, g.Server)
	g.set("yes")
	second := g.tabOf(ownerPrincipal())
	defer closeHosts(t, second)
	tracked := trackDecisions(g)
	before := tracked.count()
	a := mustGet(t, g.Tab, PlacedNotes, jsonObject("session", "S1"))
	b := mustGet(t, second, PlacedNotes, jsonObject("session", "S1"))
	// Two holds of one principal on one address: one decision. Another address: another.
	if made := tracked.count() - before; made != 1 {
		t.Fatalf("decisions = %d, want 1", made)
	}
	other := mustGet(t, g.Tab, PlacedNotes, jsonObject("session", "S2"))
	if made := tracked.count() - before; made != 2 {
		t.Fatalf("decisions = %d, want 2", made)
	}
	a.Release()
	g.settled()
	// Still held by the second tab: denied with it.
	g.set("no")
	g.settled()
	wantDenied(t, b)
	b.Release()
	other.Release()
	g.settled()
	expectCollected(t, tracked.seen[before:]...)
}

// Pi: "one decision per principal and address › a failed source leaves bound holds alone and fails new ones waiting; it heals
// with the source". Covers (part B): relayedHold.withheld for a new hold with `waiting`, relayedHold.admitted after it.
func TestRouteAdmissionOneDecisionPerPrincipalAndAddressAFailedSourceLeavesBoundHoldsAloneAndFailsNewOnesWaitingItHealsWithTheSource(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		g := routeGated(t, relayedOptions{}, nil)
		defer closeHosts(t, g.Tab, g.Server)
		g.set("yes")
		bound := mustGet(t, g.Tab, PlacedNotes, jsonObject("session", "S1"))
		g.set("throw")
		g.settled()
		if got := kindOf(bound); got != RefStateLive {
			t.Fatalf("bound state = %s, want live", got)
		}
		second := g.tabOf(ownerPrincipal())
		defer closeHosts(t, second)
		late := mustHold(t, second, PlacedNotes, jsonObject("session", "S1"))
		g.settled()
		cause := failedWith(t, late)
		if cause.Kind != data.FailureWaiting || !strings.Contains(cause.Error.Message, "the gate threw") {
			t.Fatalf("cause = %s %q, want waiting: the gate threw", cause.Kind, cause.Error.Message)
		}
		g.set("yes")
		g.settled()
		wantKind(t, late, RefStateLive)
		if kindOf(bound) != RefStateLive || kindOf(late) != RefStateLive {
			t.Fatalf("states = %s, %s, want live, live", kindOf(bound), kindOf(late))
		}
	})
}

// Pi: "one decision per principal and address › a source that fails again, and again, still leaves the holds forwarded under
// the last allow alone". Covers (host_route_admission.go): apply's `again` branch (same failure: nothing new to tell); (part B)
// path.withhold NOT called for a forwarded holder.
func TestRouteAdmissionOneDecisionPerPrincipalAndAddressASourceThatFailsAgainAndAgainStillLeavesTheHoldsForwardedUnderTheLastAllowAlone(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		g := routeGated(t, relayedOptions{}, nil)
		defer closeHosts(t, g.Tab, g.Server)
		g.set("yes")
		bound := mustGet(t, g.Tab, PlacedNotes, jsonObject("session", "S1"))
		g.WorkerLoaded(t, "session|S1")
		g.set("throw")
		g.settled()
		// The source changes for another principal: this one's decision is read again, and throws again.
		g.set("no", "guest")
		g.set("yes", "guest")
		g.settled()
		if got := kindOf(bound); got != RefStateLive {
			t.Fatalf("bound state = %s, want live", got)
		}
		if references := workerReferences(g.relayed, "session|S1"); references != 1 {
			t.Fatalf("worker references = %d, want 1", references)
		}
		// A denial is what takes them back.
		g.set("no")
		g.settled()
		wantDenied(t, bound)
	})
}

// Pi: "one decision per principal and address › a new link of the address holds nothing while the source is failed:
// forwarded holds wait until it recovers (fail closed)". Covers (host_route_admission.go): gate.unlinked/routeAdmission.unlinked;
// (part B) path.disconnected's gate.unlinked call, path.withhold on a path without a link, path.connected skipping withheld.
func TestRouteAdmissionOneDecisionPerPrincipalAndAddressANewLinkOfTheAddressHoldsNothingWhileTheSourceIsFailedForwardedHoldsWaitUntilItRecoversFailClosed(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		raw := newRawWorkers(t, rawWorkerOptions{})
		g := routeGated(t, relayedOptions{Resolve: raw.Resolve}, raw)
		defer closeHosts(t, g.Tab, g.Server)
		g.set("yes")
		notes := mustHold(t, g.Tab, PlacedNotes, jsonObject("session", "S1"))
		first := raw.At(t, 0)
		g.settled()
		g.set("throw")
		g.settled()
		// Still held on its link: left alone.
		if releases := peerKindCount(first, "release"); releases != 0 {
			t.Fatalf("releases = %d, want 0", releases)
		}
		first.Close()
		g.settled()
		// A new link is new work at the address's host: nothing is resolved without an allow that can be read.
		if peers := len(raw.Peers()); peers != 1 {
			t.Fatalf("raw workers = %d, want 1", peers)
		}
		cause := failedWith(t, notes)
		if cause.Kind != data.FailureWaiting || !strings.Contains(cause.Error.Message, "the gate threw") {
			t.Fatalf("cause = %s %q, want waiting: the gate threw", cause.Kind, cause.Error.Message)
		}
		g.set("yes")
		g.settled()
		// The source is back with its allow: resolved, and held once.
		second := raw.At(t, 1)
		g.settled()
		if kinds := second.Kinds(); !kindsEqual(kinds, "hello", "hold") {
			t.Fatalf("second worker kinds = %v, want hello, hold", kinds)
		}
		if got := kindOf(notes); got != RefStateLoading {
			t.Fatalf("state = %s, want loading", got)
		}
	})
}

// Pi: "one decision per principal and address › a source that fails while the address's link is down takes the forwarded holds
// back; one that only loads does not". Covers (host_route_admission.go): apply's `path.link() == nil` branch (forwarded cleared),
// path.wanted false at `dropped` (route.changed's new arm: nothing resolves for held-back holds).
func TestRouteAdmissionOneDecisionPerPrincipalAndAddressASourceThatFailsWhileTheAddressesLinkIsDownTakesTheForwardedHoldsBackOneThatOnlyLoadsDoesNot(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		raw := newRawWorkers(t, rawWorkerOptions{})
		pending := make(chan struct{})
		var resolving atomic.Int32
		g := routeGated(t, relayedOptions{Resolve: func(address contracts.Address, worker func() resolved, ctx context.Context) (resolved, error) {
			if resolving.Add(1) > 1 {
				_, _ = chordctx.Receive(ctx, pending)
			}
			return raw.Resolve(address, worker, ctx)
		}}, raw)
		defer closeHosts(t, g.Tab, g.Server)
		g.set("yes")
		notes := mustHold(t, g.Tab, PlacedNotes, jsonObject("session", "S1"))
		first := raw.At(t, 0)
		g.settled()
		// The link drops under an allow: the hold waits for the next link, which is being resolved.
		first.Close()
		g.settled()
		if got := kindOf(notes); got != RefStateLoading {
			t.Fatalf("state = %s, want loading", got)
		}
		// The source fails before that link is there: the link, once made, holds nothing.
		g.set("throw")
		g.settled()
		if cause := failedWith(t, notes); cause.Kind != data.FailureWaiting {
			t.Fatalf("cause = %s, want waiting", cause.Kind)
		}
		close(pending)
		second := raw.At(t, 1)
		g.settled()
		if kinds := second.Kinds(); !kindsEqual(kinds, "hello") {
			t.Fatalf("second worker kinds = %v, want hello", kinds)
		}
		g.set("yes")
		g.settled()
		if kinds := second.Kinds(); !kindsEqual(kinds, "hello", "hold") {
			t.Fatalf("second worker kinds = %v, want hello, hold", kinds)
		}
	})
}

// peerKindCount is how many messages of kind peer received.
func peerKindCount(peer *RawPeer, kind string) int {
	count := 0
	for _, got := range peer.Kinds() {
		if got == kind {
			count++
		}
	}
	return count
}

func kindsEqual(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// --- the fence after a denial -------------------------------------------------------------------------------------------

// since is the kinds of the messages peer received since from.
func since(peer *RawPeer, from int) []string {
	kinds := peer.Kinds()
	if from > len(kinds) {
		return nil
	}
	return kinds[from:]
}

// withoutKind is kinds without the entries of kind.
func withoutKind(kinds []string, kind string) []string {
	var kept []string
	for _, got := range kinds {
		if got != kind {
			kept = append(kept, got)
		}
	}
	return kept
}

// boundOf is `bound(held, count)`: held's binding on the raw worker, the editor variant with `count`.
func boundOf(t testing.TB, held *link.HoldMessage, count int) link.Message {
	t.Helper()
	editor := editorWire(t)
	notes, err := link.JsonPart([]any{})
	must(t, err)
	counted, err := link.JsonPart(float64(count))
	must(t, err)
	return &link.BoundMessage{Origin: held.Origin, Holds: []link.BoundEntry{{
		Hold: held.Hold, Granted: editor.Granted,
		Members: []link.BoundMember{{Member: editor.State["notes"], Value: notes}, {Member: editor.State["count"], Value: counted}},
		Failed:  []link.FailedMember{},
	}}}
}

// stateMessage is `state(held, count)`: a `state` of `count` for held on the raw worker.
func stateMessage(t testing.TB, held *link.HoldMessage, count int) link.Message {
	t.Helper()
	editor := editorWire(t)
	ops, err := link.JsonPart([]any{[]any{"r", float64(count)}})
	must(t, err)
	return &link.StateMessage{Origin: held.Origin, Holds: []link.StateEntry{{
		Hold: held.Hold, Members: []link.StateMember{{Member: editor.State["count"], Ops: ops}}, Failed: []link.FailedMember{}, Retired: []int{},
	}}}
}

// holdsOf are the `hold` messages a raw worker received, in order.
func holdsOf(peer *RawPeer) []*link.HoldMessage {
	var holds []*link.HoldMessage
	for _, message := range peer.Received() {
		if hold, ok := message.(*link.HoldMessage); ok {
			holds = append(holds, hold)
		}
	}
	return holds
}

// fencedRig is `fenced()`: a server routing to a raw worker, an owner's raw tab and a guest's, each holding S1 as its hold 0,
// both bound (count 0); the worker's holds by tab.
type fencedRig struct {
	*gatedRelay
	raw          *rawWorkers
	worker       *RawPeer
	owner, guest *RawPeer
	mine, theirs *link.HoldMessage
}

func fenced(t testing.TB) *fencedRig {
	t.Helper()
	raw := newRawWorkers(t, rawWorkerOptions{})
	g := routeGated(t, relayedOptions{Resolve: raw.Resolve}, raw)
	g.set("yes")
	g.set("yes", "guest")
	owner := g.rawTab(ownerPrincipal())
	guest := g.rawTab(delta.JsonObjectOf("kind", "guest", "session", "S1"))
	holdOfPlaced(t, owner, "S1", "S1", PlacedNotes.Name, 0)
	g.settled()
	holdOfPlaced(t, guest, "S1", "S1", PlacedNotes.Name, 0)
	g.settled()
	worker := raw.At(t, 0)
	holds := holdsOf(worker)
	if len(holds) != 2 {
		t.Fatalf("worker holds = %d, want 2", len(holds))
	}
	mine, theirs := holds[0], holds[1]
	worker.Send(boundOf(t, mine, 0))
	worker.Send(boundOf(t, theirs, 0))
	g.settled()
	if got := owner.Kinds(); !kindsEqual(got, "hello", "bound") {
		t.Fatalf("owner kinds = %v, want hello, bound", got)
	}
	if got := guest.Kinds(); !kindsEqual(got, "hello", "bound") {
		t.Fatalf("guest kinds = %v, want hello, bound", got)
	}
	return &fencedRig{gatedRelay: g, raw: raw, worker: worker, owner: owner, guest: guest, mine: mine, theirs: theirs}
}

// Pi: "the fence after a denial › drops what was in transit for the released hold, and forwards it again under its id only after
// the pong". Covers (part B): relayedHold.withheld (release, error then failed), providerSide.withheld/rejectRelayed(all),
// relayedHold.admitted (`loading` downstream), path.forward's fence (hostLink.fence), the relay's drop of a released hold's
// replies and the ack of the dropped state.
func TestRouteAdmissionTheFenceAfterADenialDropsWhatWasInTransitForTheReleasedHoldAndForwardsItAgainUnderItsIdOnlyAfterThePong(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		f := fenced(t)
		defer closeHosts(t, f.Server)
		editor := editorWire(t)
		f.owner.Send(&link.CallMessage{Call: 7, Hold: 0, Method: editor.Methods["wait"]})
		f.settled()
		seen := len(f.worker.Kinds())
		f.set("no")
		f.settled()
		// The denial: released upstream, the call in flight answered, the hold failed.
		if got := since(f.worker, seen); !kindsEqual(got, "release") {
			t.Fatalf("worker since denial = %v, want release", got)
		}
		if got := f.owner.Kinds()[2:]; !kindsEqual(got, "error", "failed") {
			t.Fatalf("owner after bound = %v, want error, failed", got)
		}
		received := f.owner.Received()
		if got := wireTold(t, received[2].(*link.ErrorMessage).Error); got.Code != "forbidden" || got.Reason != "revoked" {
			t.Fatalf("call error = %+v, want forbidden/revoked", got)
		}
		if cause := received[3].(*link.FailedMessage).Cause; cause.Kind != "denied" {
			t.Fatalf("failed cause = %s, want denied", cause.Kind)
		}
		// What the worker sent before it saw the release, arriving now: a state, an event, the call's result.
		eventValue, err := link.JsonPart(float64(1))
		must(t, err)
		stale := []link.Message{
			stateMessage(t, f.mine, 1),
			&link.EventMessage{Origin: f.mine.Origin, Events: []link.WireEvent{{Hold: f.mine.Hold, Member: 0, At: 1, Value: eventValue}}},
			&link.ResultMessage{Origin: f.mine.Origin, Call: 7},
		}
		tabSeen := len(f.owner.Kinds())
		acked := peerKindCount(f.worker, "ack")
		for _, message := range stale {
			f.worker.Send(message)
		}
		f.settled()
		// An `event` message is forwarded with what is left of it (its ticket is the downstream's to return): nothing.
		after := f.owner.Received()[tabSeen:]
		if len(after) != 1 || after[0].Kind() != "event" || len(after[0].(*link.EventMessage).Events) != 0 {
			t.Fatalf("owner after the stale messages = %v, want one empty event", after)
		}
		// The dropped state is still acknowledged: the worker's window does not stall.
		if acks := peerKindCount(f.worker, "ack") - acked; acks != 1 {
			t.Fatalf("acks = %d, want 1", acks)
		}
		f.set("yes")
		f.settled()
		// Allowed: the tab is told `loading` at once (the person has access again), the worker gets a `ping`, and no hold
		// before its `pong`.
		if got := since(f.owner, tabSeen); !kindsEqual(got, "event", "loading") {
			t.Fatalf("owner since = %v, want event, loading", got)
		}
		if got := withoutKind(since(f.worker, seen), "ack"); !kindsEqual(got, "release", "ping") {
			t.Fatalf("worker since = %v, want release, ping", got)
		}
		f.worker.Send(stateMessage(t, f.mine, 2))
		f.worker.Send(boundOf(t, f.mine, 3))
		// The other principal's hold of the same id is undisturbed meanwhile.
		f.worker.Send(stateMessage(t, f.theirs, 5))
		f.settled()
		if got := since(f.owner, tabSeen); !kindsEqual(got, "event", "loading") {
			t.Fatalf("owner since = %v, want event, loading", got)
		}
		if got := f.guest.Kinds(); !kindsEqual(got, "hello", "bound", "state") {
			t.Fatalf("guest kinds = %v, want hello, bound, state", got)
		}
		f.worker.Send(&link.PongMessage{})
		f.settled()
		holds := holdsOf(f.worker)
		again := holds[len(holds)-1]
		if f.worker.Kinds()[len(f.worker.Kinds())-1] != "hold" || again.Hold != f.mine.Hold || again.Origin != f.mine.Origin {
			t.Fatalf("last worker message = %v hold %d origin %v, want hold %d origin %v", f.worker.Kinds()[len(f.worker.Kinds())-1], again.Hold, again.Origin, f.mine.Hold, f.mine.Origin)
		}
		f.worker.Send(boundOf(t, again, 9))
		f.settled()
		if got := since(f.owner, tabSeen); !kindsEqual(got, "event", "loading", "bound") {
			t.Fatalf("owner since = %v, want event, loading, bound", got)
		}
	})
}

// Pi: "the fence after a denial › a call that crossed the denial on the wire is answered forbidden/revoked by the relay, and
// forwarded nowhere". Covers (part B): relayedHold.unreached (heldBack set and denied: `revoked(hold 0)`), relayedHold.submit
// without an upstream, relayedHold.withheld's heldBack.
func TestRouteAdmissionTheFenceAfterADenialACallThatCrossedTheDenialOnTheWireIsAnsweredForbiddenRevokedByTheRelayAndForwardedNowhere(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		f := fenced(t)
		defer closeHosts(t, f.Server)
		f.set("no")
		f.settled()
		seen := len(f.worker.Kinds())
		// Sent by a tab that has not read its `failed` yet.
		f.owner.Send(&link.CallMessage{Call: 9, Hold: 0, Method: editorWire(t).Methods["wait"]})
		f.settled()
		received := f.owner.Received()
		last, ok := received[len(received)-1].(*link.ErrorMessage)
		if !ok || last.Call != 9 {
			t.Fatalf("last owner message = %v, want error for call 9", received[len(received)-1])
		}
		if got := wireTold(t, last.Error); got != (errorTold{"forbidden", "revoked", "hold 0: no access"}) {
			t.Fatalf("error = %+v, want forbidden/revoked: hold 0: no access", got)
		}
		if got := since(f.worker, seen); len(got) != 0 {
			t.Fatalf("worker since = %v, want none", got)
		}
	})
}

// Pi: "the fence after a denial › a denial before the pong ends the fence; the next allow sends a new one". Covers (part B):
// path.withhold cancelling a waiting fence (hostLink.fence cancel), path.forward's new fence.
func TestRouteAdmissionTheFenceAfterADenialADenialBeforeThePongEndsTheFenceTheNextAllowSendsANewOne(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		f := fenced(t)
		defer closeHosts(t, f.Server)
		f.set("no")
		f.set("yes")
		f.set("no")
		f.settled()
		seen := len(f.worker.Kinds())
		// The first fence's pong: nothing is forwarded for a denied principal.
		f.worker.Send(&link.PongMessage{})
		f.settled()
		if got := since(f.worker, seen); len(got) != 0 {
			t.Fatalf("worker since = %v, want none", got)
		}
		f.set("yes")
		f.settled()
		if got := since(f.worker, seen); !kindsEqual(got, "ping") {
			t.Fatalf("worker since = %v, want ping", got)
		}
		f.worker.Send(&link.PongMessage{})
		f.settled()
		holds := holdsOf(f.worker)
		if holds[len(holds)-1].Hold != f.mine.Hold {
			t.Fatalf("last hold = %d, want %d", holds[len(holds)-1].Hold, f.mine.Hold)
		}
	})
}

// Pi: "the fence after a denial › deny and allow three times in a row forward the hold once, after the last pong". Covers
// (part B): hostLink.fence counting (a ping per fence, one pong per fence), path.forward / path.withhold interplay.
func TestRouteAdmissionTheFenceAfterADenialDenyAndAllowThreeTimesInARowForwardTheHoldOnceAfterTheLastPong(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		f := fenced(t)
		defer closeHosts(t, f.Server)
		seen := len(f.worker.Kinds())
		for range 3 {
			f.set("no")
			f.set("yes")
		}
		f.settled()
		if got := since(f.worker, seen); !kindsEqual(got, "release", "ping", "ping", "ping") {
			t.Fatalf("worker since = %v, want release, ping, ping, ping", got)
		}
		f.worker.Send(&link.PongMessage{})
		f.worker.Send(&link.PongMessage{})
		f.settled()
		if slicesContains(since(f.worker, seen), "hold") {
			t.Fatalf("worker since = %v, want no hold before the last pong", since(f.worker, seen))
		}
		f.worker.Send(&link.PongMessage{})
		f.settled()
		if got := since(f.worker, seen); !kindsEqual(got, "release", "ping", "ping", "ping", "hold") {
			t.Fatalf("worker since = %v, want release, ping, ping, ping, hold", got)
		}
	})
}

func slicesContains(kinds []string, kind string) bool {
	return slices.Contains(kinds, kind)
}

// Pi: "the fence after a denial › the link lost before the pong: the next link holds what the decision allows then, with no
// fence". Covers (part B): path.disconnected dropping the fences (released cleared), path.connected holding every
// non-withheld holder with no fence.
func TestRouteAdmissionTheFenceAfterADenialTheLinkLostBeforeThePongTheNextLinkHoldsWhatTheDecisionAllowsThenWithNoFence(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		f := fenced(t)
		defer closeHosts(t, f.Server)
		f.set("no")
		f.set("yes")
		f.settled()
		f.worker.Close()
		f.settled()
		next := f.raw.At(t, 1)
		f.settled()
		// Both principals are allowed: both holds, no `ping` first.
		if got := next.Kinds(); !kindsEqual(got, "hello", "hold", "hold") {
			t.Fatalf("next kinds = %v, want hello, hold, hold", got)
		}
		if f.owner.Closed() {
			t.Fatal("the owner's tab closed")
		}
		// Denied while the link is down: the next link holds only the other principal's.
		f.set("no")
		next.Close()
		f.settled()
		if got := f.raw.At(t, 2).Kinds(); !kindsEqual(got, "hello", "hold") {
			t.Fatalf("third kinds = %v, want hello, hold", got)
		}
	})
}

// --- across reloads (test 21) -------------------------------------------------------------------------------------------

// againFacets is the server's facet again, as a new value: a reload restarts the gate's store.
func againFacets(setup func(facet FacetHost) error) []HostFacet {
	return []HostFacet{{Name: "@test/server", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return setup(h) })}}
}

// Pi: "across reloads (test 21) › the source's plugin reloaded to an equal allow: no resolve, no new upstream hold, the call
// across it completes". Covers (host_route_admission.go): the settled decision's `moved` -> `now` returning the same verdict (apply's
// identity early return); (part B) no withheld/admitted call.
func TestRouteAdmissionAcrossReloadsTheSourcesPluginReloadedToAnEqualAllowNoResolveNoNewUpstreamHoldTheCallAcrossItCompletes(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		g := routeGated(t, relayedOptions{}, nil)
		defer closeHosts(t, g.Tab, g.Server)
		g.set("yes")
		notes := mustGet(t, g.Tab, PlacedNotes, jsonObject("session", "S1"))
		g.WorkerLoaded(t, "session|S1")
		worker := g.Worker("session|S1")
		adding := startCallWith(t, notes, "add", jsonObject("text", "x"))
		_, err := g.Server.Reload(againFacets(serveGate), background, ReloadOptions{})
		must(t, err)
		g.settled()
		if value, err := adding.wait(background); err != nil || value != float64(1) {
			t.Fatalf("add = %v, %v, want 1", value, err)
		}
		wantKind(t, notes, RefStateLive)
		if count, err := notes.Member("count").Value(); err != nil || count != float64(1) {
			t.Fatalf("count = %v, %v, want 1", count, err)
		}
		if resolves, references := g.Resolves(), activityOf(worker).References; len(resolves) != 1 || resolves[0] != "session|S1" || references != 1 {
			t.Fatalf("resolves = %v, references = %d, want [session|S1], 1", resolves, references)
		}
	})
}

// Pi: "across reloads (test 21) › the source removed leaves bound holds alone and fails new ones; restored, its new store
// decides again". Covers (part B): relayedHold.withheld for the bound hold when the new store denies (`denied`),
// relayedHold.admitted + path.forward behind the fence when the new store allows.
func TestRouteAdmissionAcrossReloadsTheSourceRemovedLeavesBoundHoldsAloneAndFailsNewOnesRestoredItsNewStoreDecidesAgain(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		g := routeGated(t, relayedOptions{}, nil)
		defer closeHosts(t, g.Tab, g.Server)
		g.set("yes")
		bound := mustGet(t, g.Tab, PlacedNotes, jsonObject("session", "S1"))
		_, err := g.Server.Reload(againFacets(func(FacetHost) error { return nil }), background, ReloadOptions{})
		must(t, err)
		g.settled()
		if got := kindOf(bound); got != RefStateLive {
			t.Fatalf("bound state = %s, want live", got)
		}
		second := g.tabOf(ownerPrincipal())
		defer closeHosts(t, second)
		late := mustHold(t, second, PlacedNotes, jsonObject("session", "S1"))
		g.settled()
		if cause := failedWith(t, late); cause.Kind != data.FailureWaiting {
			t.Fatalf("late cause = %s, want waiting", cause.Kind)
		}
		_, err = g.Server.Reload(againFacets(serveGate), background, ReloadOptions{})
		must(t, err)
		g.settled()
		// A new store object: its modes start over, so the principal is denied until allowed again (the fence).
		wantDenied(t, bound)
		wantDenied(t, late)
		gate := mustGet(t, g.Server, Gate, jsonObject())
		_, err = gate.Call("set", jsonObject("who", "owner", "mode", "yes"), background)
		must(t, err)
		g.settled()
		wantKind(t, bound, RefStateLive)
		wantKind(t, late, RefStateLive)
	})
}

// Pi: "across reloads (test 21) › the source reloaded while a resolve is pending: the hold binds once the link is there".
// Covers (host_route_admission.go): a decision change while the route's resolve runs does not resolve twice.
func TestRouteAdmissionAcrossReloadsTheSourceReloadedWhileAResolveIsPendingTheHoldBindsOnceTheLinkIsThere(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		pending := make(chan struct{})
		var resolving atomic.Int32
		g := routeGated(t, relayedOptions{Resolve: func(_ contracts.Address, worker func() resolved, ctx context.Context) (resolved, error) {
			resolving.Add(1)
			_, _ = chordctx.Receive(ctx, pending)
			return worker(), nil
		}}, nil)
		defer closeHosts(t, g.Tab, g.Server)
		g.set("yes")
		notes := mustHold(t, g.Tab, PlacedNotes, jsonObject("session", "S1"))
		g.settled()
		_, err := g.Server.Reload(againFacets(serveGate), background, ReloadOptions{})
		must(t, err)
		g.settled()
		if got := kindOf(notes); got != RefStateLoading {
			t.Fatalf("state = %s, want loading", got)
		}
		close(pending)
		g.settled()
		wantKind(t, notes, RefStateLive)
		// One resolve, not one per decision change.
		if resolves := g.Resolves(); len(resolves) != 1 || resolves[0] != "session|S1" {
			t.Fatalf("resolves = %v, want one session|S1", resolves)
		}
	})
}

// Pi: "across reloads (test 21) › an object behind the route reloaded is an ordinary rebind: no decision is asked, nothing
// resolved". Covers: nothing of the route's admission is called again (decisions tracker).
func TestRouteAdmissionAcrossReloadsAnObjectBehindTheRouteReloadedIsAnOrdinaryRebindNoDecisionIsAskedNothingResolved(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		g := routeGated(t, relayedOptions{}, nil)
		defer closeHosts(t, g.Tab, g.Server)
		g.set("yes")
		notes := mustGet(t, g.Tab, PlacedNotes, jsonObject("session", "S1"))
		g.WorkerLoaded(t, "session|S1")
		tracked := trackDecisions(g)
		before := tracked.count()
		worker := g.Worker("session|S1")
		_, err := worker.Reload([]HostFacet{{Name: "@test/worker", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			_, err := serveNotes(h, PlacedNotes)
			return err
		})}}, background, ReloadOptions{})
		must(t, err)
		g.settled()
		wantKind(t, notes, RefStateLive)
		if made, resolves := tracked.count()-before, g.Resolves(); made != 0 || len(resolves) != 1 || resolves[0] != "session|S1" {
			t.Fatalf("decisions asked = %d, resolves = %v, want 0, [session|S1]", made, resolves)
		}
	})
}

// EXTRA (no Pi case): the server's own reference to an address of its gated route is a `remoteObject` holder, which no case
// above reaches (every hold there is a relayed one). Covers (part B): remoteObject.withheld (a denial: `failed{denied}`, the
// held replica dropped: its members fail `forbidden`), remoteObject.admitted (startOver: `loading` again), and the same
// through path.forward / path.withhold. Pi's own cover is admission.test.ts (part C, "over a link").
func TestRouteAdmissionAServersOwnReferenceToAGatedAddressIsWithheldAndAdmittedAsARemoteObject(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		g := routeGated(t, relayedOptions{}, nil)
		defer closeHosts(t, g.Tab, g.Server)
		// The server acts as itself (kind `server`): its decision is its own, apart from the owner's.
		// Acquire the state watch in the same section as Hold so the admission denial cannot precede Watch.
		own, states := admHoldStates(t, g.Server, PlacedNotes, jsonObject("session", "S1"))
		g.settled()
		wantDenied(t, own)
		if resolves := g.Resolves(); len(resolves) != 0 {
			t.Fatalf("resolves = %v, want none", resolves)
		}
		g.set("yes", "server")
		g.settled()
		wantKind(t, own, RefStateLive)
		if granted, err := own.Granted(); err != nil || !granted["add"] {
			t.Fatalf("granted = %v, %v, want add once it is live", granted, err)
		}
		g.set("no", "server")
		g.settled()
		wantDenied(t, own)
		if _, err := own.Member("count").Value(); err == nil {
			t.Fatal("a denied remote reference still reads its replica")
		}
		// remote-object.ts `withheld`: a denial drops what it bound; a refusal that only holds back (waiting) would keep it.
		if granted, err := own.Granted(); err != nil || len(granted) != 0 {
			t.Fatalf("granted after the denial = %v, %v, want empty", granted, err)
		}
		g.WorkerLoaded(t, "session|S1")
		if references := workerReferences(g.relayed, "session|S1"); references != 0 {
			t.Fatalf("worker references = %d, want 0 after the denial", references)
		}
		g.set("yes", "server")
		g.settled()
		wantKind(t, own, RefStateLive)
		// The allow holds it again as `loading` (admitted: startOver) until its next binding, which makes it live: it does not
		// jump from the denial to live.
		if all := states.All(); len(all) < 2 || all[len(all)-2] != "loading" || all[len(all)-1] != "live" {
			t.Fatalf("states after the allow = %v, want ... loading, live", all)
		}
		if references := workerReferences(g.relayed, "session|S1"); references != 1 {
			t.Fatalf("worker references = %d, want 1 after the allow", references)
		}
	})
}
