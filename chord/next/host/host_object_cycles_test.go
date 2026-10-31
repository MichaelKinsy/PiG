package host

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports the `cycles across hosts (Q5)` and the two released-reference GC cases of packages/chord/test/next/host/object.test.ts:
// a tab, a server and its workers over trusted route links (relay), and raw workers that hold the server's types by hand.

func nVariant() []contracts.VariantEntry {
	return []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}})}
}

func sessionAddress(key string) func(params *delta.JsonObject) (contracts.Address, error) {
	return func(params *delta.JsonObject) (contracts.Address, error) {
		return Session.Address(jsonObject("id", params.Value(key))), nil
	}
}

var (
	// cycNear is on the server: its activation holds `Far` on the session's worker.
	cycNear = mustContract(contracts.Remote("app.near", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("session", contracts.Str())},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil }, Variants: nVariant(),
	}))
	// cycFar is on the session's worker: its activation holds `Near` back on the server, or `Middle`.
	cycFar = mustContract(contracts.Remote("app.far", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("session", contracts.Str())},
		HostAddress: sessionAddress("session"), Variants: nVariant(),
	}))
	// cycMiddle is on the server, held by `Far`: its activation holds `Hub` on its own host.
	cycMiddle = mustContract(contracts.Remote("app.middle", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("session", contracts.Str())},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil }, Variants: nVariant(),
	}))
	// cycHub is a local type on the server: its activation holds `Far` as the host.
	cycHub = mustContract(contracts.Local("app.hub", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("session", contracts.Str())}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	// cycLoop lives on `here`'s worker and holds `Loop({ here: there, there: here })`.
	cycLoop = mustContract(contracts.Remote("app.loop", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("here", contracts.Str()), contracts.Prop("there", contracts.Str())},
		HostAddress: sessionAddress("here"), Variants: nVariant(),
	}))
)

func cycImpl(member any) contracts.Implementation {
	return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"n": Expose(member)}}}
}

// cyclic is a tab, a server and its workers over trusted route links (Far holds back).
func cyclic(t *testing.T, back string) *relayed { return cyclicAttempting(t, back, nil) }

// cyclicAttempting is cyclic, with attempted told of the object of every Near and Far activation as its factory runs
// (Pi's `instancesOf(ChordObject, "attempt")` sees every object made, whichever host it is on and however briefly it lives).
func cyclicAttempting(t *testing.T, back string, attempted func(Self)) *relayed {
	t.Helper()
	attempt := func(self Self) {
		if attempted != nil {
			attempted(self)
		}
	}
	return relay(t, relayedOptions{
		Trusted: true,
		Server: func(h FacetHost) error {
			if err := h.Serve(cycNear, func(params *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
				attempt(self)
				return cycImpl(self.Get(cycFar, jsonObject("session", params.Value("session"))).Member("n")), nil
			}, ServeOptions{}); err != nil {
				return err
			}
			if err := h.Serve(cycMiddle, func(params *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
				return cycImpl(self.Get(cycHub, jsonObject("session", params.Value("session"))).Member("n")), nil
			}, ServeOptions{}); err != nil {
				return err
			}
			return h.Serve(cycHub, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
				return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
					"n": Expose(self.Get(cycFar, jsonObject("session", params.Value("session"))).Member("n")),
				}}}, nil
			}, ServeOptions{})
		},
		Worker: func(h FacetHost) error {
			if err := h.Serve(cycFar, func(params *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
				attempt(self)
				target := cycNear
				if back == "middle" {
					target = cycMiddle
				}
				return cycImpl(self.Get(target, jsonObject("session", params.Value("session"))).Member("n")), nil
			}, ServeOptions{}); err != nil {
				return err
			}
			return h.Serve(cycLoop, func(params *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
				return cycImpl(self.Get(cycLoop, jsonObject("here", params.Value("there"), "there", params.Value("here"))).Member("n")), nil
			}, ServeOptions{})
		},
	})
}

func failureSet(errs ...*hostErrors) []string {
	seen := map[string]bool{}
	for _, each := range errs {
		for _, failure := range failures(each.All()) {
			seen[failure] = true
		}
	}
	var out []string
	for failure := range seen {
		out = append(out, failure)
	}
	sort.Strings(out)
	return out
}

func TestObjectCyclesAcrossHostsRefuseAHoldWhoseChainNamesTheActivationItWouldBindServerWorkerServer(t *testing.T) {
	r := cyclic(t, "near")
	_, err := r.Tab.Get(cycNear, jsonObject("session", "S1"), background, GetOptions{})
	// The worker's hold back acts as the worker (upward boundary): it meets the cycle one turn later, at that principal's `Near`.
	wantChordError(t, err, data.CodeInvalid, data.ReasonCycle, "dependency cycle: app.near(S1) → … → app.near(S1)")
	// Every object on the cycle fails broken with the same error, none transient (nothing retries it).
	if got := failureSet(r.Errors.Server, r.Errors.Worker); !reflect.DeepEqual(got, []string{"app.far(S1): broken", "app.near(S1): broken"}) {
		t.Fatalf("failures = %v", got)
	}
}

func TestObjectCyclesAcrossHostsFindACycleClosedOnOneHostThroughAnotherServerWorkerServerThenALocalGet(t *testing.T) {
	r := cyclic(t, "middle")
	_, err := r.Server.Get(cycHub, jsonObject("session", "S1"), background, GetOptions{})
	wantChordError(t, err, data.CodeInvalid, data.ReasonCycle, "dependency cycle: app.hub(S1) → … → app.middle(S1) → app.hub(S1)")
}

func TestObjectCyclesAcrossHostsCarryTheChainThroughARelayRace7WorkerServerWorkerServerWorker(t *testing.T) {
	r := cyclic(t, "near")
	_, err := r.Tab.Get(cycLoop, jsonObject("here", "S1", "there", "S2"), background, GetOptions{})
	wantChordError(t, err, data.CodeInvalid, data.ReasonCycle, "")
	if got := failureSet(r.Errors.Worker); !reflect.DeepEqual(got, []string{"app.loop(S1|S2): broken", "app.loop(S2|S1): broken"}) {
		t.Fatalf("worker failures = %v", got)
	}
}

func farHold(t testing.TB, hold int, chain []string) *link.HoldMessage {
	t.Helper()
	params, err := link.JsonPart(jsonObject("session", "S1"))
	must(t, err)
	return &link.HoldMessage{Hold: hold, Type: cycFar.Name, Version: cycFar.Version, Fingerprint: cycFar.Fingerprint, Params: params,
		Address: link.WireAddress{Kind: "session", Params: jsonObject("id", "S1")}, Chain: chain}
}

func nearHold(t testing.TB, hold int, chain []string) *link.HoldMessage {
	t.Helper()
	params, err := link.JsonPart(jsonObject("session", "S1"))
	must(t, err)
	return &link.HoldMessage{Hold: hold, Type: cycNear.Name, Version: cycNear.Version, Fingerprint: cycNear.Fingerprint, Params: params,
		Address: serverWireAddress, Chain: chain}
}

func TestObjectCyclesAcrossHostsDropTheChainFromHoldsArrivingOnUntrustedLinksAndSendNoneForHoldsMadeOutsideAnActivation(t *testing.T) {
	workers := newRawWorkers(t, rawWorkerOptions{})
	r := relay(t, relayedOptions{Resolve: workers.Resolve})
	a, b := link.MemoryLinkPair()
	must(t, r.Server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	tab := newRawPeer(t, b, rawPeerOptions{})
	tab.Send(farHold(t, 0, []string{"0190f5a2-7c3e-7d4a-9b1c-2d3e4f5a6b7c"}))
	held := workers.At(t, 0).Next("hold").(*link.HoldMessage)
	if len(held.Chain) != 0 {
		t.Fatalf("chain = %v, want none", held.Chain)
	}
	mustHold(t, r.Tab, cycFar, jsonObject("session", "S1"))
	if again := workers.At(t, 0).Next("hold").(*link.HoldMessage); len(again.Chain) != 0 {
		t.Fatalf("chain = %v, want none", again.Chain)
	}
}

// nearBehindRawWorker is a server serving `Near` (its activation holds `Far`) and a raw worker for S1 over a trusted route link,
// made by a first hold of `Far` and with that hold taken.
func nearBehindRawWorker(t *testing.T) (*relayed, *RawPeer) {
	t.Helper()
	workers := newRawWorkers(t, rawWorkerOptions{Trusted: true})
	r := relay(t, relayedOptions{
		Resolve: workers.Resolve,
		Server: func(h FacetHost) error {
			return h.Serve(cycNear, func(params *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
				return cycImpl(self.Get(cycFar, jsonObject("session", params.Value("session"))).Member("n")), nil
			}, ServeOptions{})
		},
	})
	mustHold(t, r.Tab, cycFar, jsonObject("session", "S1"))
	worker := workers.At(t, 0)
	worker.Next("hold")
	return r, worker
}

func TestObjectCyclesAcrossHostsNeverMatchARestartedObjectItsNewInstanceHasANewIdRace8(t *testing.T) {
	// The raw worker holds `Near` on the server itself (as the worker), whose activation holds `Far` back with its chain. The
	// raw worker never binds `Far`.
	_, worker := nearBehindRawWorker(t)
	chainOf := func() []string { return worker.Next("hold").(*link.HoldMessage).Chain }
	worker.Send(nearHold(t, 0, nil))
	old := chainOf()[0]
	// Released: that instance ends. The next hold makes a new instance with a new id.
	worker.Send(&link.ReleaseMessage{Hold: 0})
	worker.Send(nearHold(t, 1, nil))
	renewed := chainOf()[0]
	if renewed == old {
		t.Fatal("a restarted object kept its instance id")
	}
	// A hold naming the old instance binds to the new one (it is pending there); one naming the new one is a cycle.
	worker.Send(nearHold(t, 2, []string{old}))
	worker.Send(nearHold(t, 3, []string{renewed}))
	failed := worker.Next("failed").(*link.FailedMessage)
	if failed.Hold != 3 || failed.Cause.Kind != "broken" {
		t.Fatalf("failed = %+v, want hold 3 broken", failed)
	}
}

func TestObjectCyclesAcrossHostsKeepTheLast32EntriesOfALongerChainTheOldestIsDroppedALongerCycleGoesUndetected(t *testing.T) {
	_, worker := nearBehindRawWorker(t)
	forged := make([]string, 32)
	for index := range forged {
		forged[index] = "0190f5a2-7c3e-7d4a-9b1c-" + leftPad12(index)
	}
	worker.Send(nearHold(t, 0, forged))
	chain := worker.Next("hold").(*link.HoldMessage).Chain
	if len(chain) != 32 || !reflect.DeepEqual(chain[:31], forged[1:]) || slices.Contains(forged, chain[31]) {
		t.Fatalf("chain = %v, want the last 32 entries with the oldest dropped", chain)
	}
}

func leftPad12(i int) string { return fmt.Sprintf("%012d", i) }

func TestObjectCyclesAcrossHostsMissACycleEnteringAnActivationThatStartedAlreadyDocumentedLimitationBothWaitTransient(t *testing.T) {
	r, worker := nearBehindRawWorker(t)
	// The server's `Near` activates for the tab and holds `Far` with its chain.
	ms := 100 * time.Millisecond
	getting := make(chan error, 1)
	go func() {
		_, err := r.Tab.Get(cycNear, jsonObject("session", "S1"), background, GetOptions{ReadyTimeout: &ms})
		getting <- err
	}()
	chain := worker.Next("hold").(*link.HoldMessage).Chain
	// The worker's own activation, started at the same moment, holds that `Near` back with a chain of its own.
	worker.Send(nearHold(t, 0, []string{"0190f5a2-7c3e-7d4a-9b1c-000000000001"}))
	if len(chain) != 1 {
		t.Fatalf("chain = %v, want one entry", chain)
	}
	err := receive(t, getting)
	wantChordError(t, err, data.CodeUnavailable, "", "")
	for _, message := range worker.Received() {
		if message.Kind() == "failed" {
			t.Fatalf("the worker received a failed message: %+v", message)
		}
	}
}

func TestObjectCyclesAcrossHostsKeepNoObjectAndSoNoChainOnceReleasedGC(t *testing.T) {
	// As Pi's IIFE: only the weak references leave the function; the hosts are unreachable once closed.
	refs := func() []weak.Pointer[chordObject] {
		// Every object attempted meanwhile, weakly (Pi's `attempted`): they may be gone from the hosts when the get ends, and
		// the worker's host does not exist before the get reaches the server, so each activation reports its own object.
		var mu sync.Mutex
		seen := map[weak.Pointer[chordObject]]struct{}{}
		r := cyclicAttempting(t, "near", func(self Self) {
			mu.Lock()
			seen[weak.Make(self.(*attempt).object)] = struct{}{}
			mu.Unlock()
		})
		_, err := r.Tab.Get(cycNear, jsonObject("session", "S1"), background, GetOptions{})
		if err == nil {
			t.Fatal("the cycle was not refused")
		}
		mu.Lock()
		var refs []weak.Pointer[chordObject]
		for ref := range seen {
			refs = append(refs, ref)
		}
		mu.Unlock()
		if len(refs) < 2 {
			t.Fatalf("objects = %d, want at least 2 on the server and the worker", len(refs))
		}
		hosts := []Host{r.Tab, r.Server}
		r.Workers.Range(func(_, worker any) bool {
			hosts = append(hosts, worker.(Host))
			return true
		})
		closeHosts(t, hosts...)
		return refs
	}()
	expectCollected(t, refs...)
}

// GC of released references.

func TestObjectGCOfReleasedReferencesKeepsNothingInAReleasedReferenceOrAStoppedWatch(t *testing.T) {
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(objStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) { return serveStore(self), nil }, ServeOptions{})
	}, loadedOptions{})
	store := mustGet(t, host, objStore, jsonObject())
	held := mustGet(t, host, objStore, jsonObject())
	var refs []weak.Pointer[Reference]
	func() {
		watch, err := store.Member("count").Watch(background)
		must(t, err)
		_, _ = watch.Stop()
		reference := mustGet(t, host, objStore, jsonObject())
		_, err = reference.Member("count").Watch(background)
		must(t, err)
		reference.Release()
		refs = append(refs, weak.Make(reference))
	}()
	_, err := refCall(held, "add", jsonObject("text", "x"))
	must(t, err)
	untilEnded(t, host)
	expectCollected(t, refs...)
}

func TestObjectGCOfReleasedReferencesKeepsNeitherTheDeclaringObjectNorTheHostInAReleasedSelfGetReference(t *testing.T) {
	var mu sync.Mutex
	var attempts []weak.Pointer[attempt]
	var declared *Reference
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(objStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) { return serveStore(self), nil }, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(attemptPage, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			mu.Lock()
			declared = self.Get(objStore, jsonObject())
			attempts = append(attempts, weak.Make(self.(*attempt)))
			mu.Unlock()
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State:   map[string]chord.Watchable[delta.JsonValue]{"label": constant("")},
				Methods: map[string]contracts.MethodFunction{"slow": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	mustGet(t, host, attemptPage, jsonObject("id", 1.0)).Release()
	turn(host)
	untilEnded(t, host)
	mu.Lock()
	held := slices.Clone(attempts)
	mu.Unlock()
	expectCollected(t, held...)
	hostRef := weak.Make(host.(*chordHost))
	expectCollected(t, hostRef)
	if declared == nil {
		t.Fatal("the reference was not declared")
	}
}
