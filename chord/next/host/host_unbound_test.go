package host

import (
	"context"
	"reflect"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/test/next/host/unbound.test.ts: the index (`describe("the index")`). The cases of "references waiting
// in the index" and "holds waiting in the index" build a whole host (createHost, host.hold, memoryLinkPair, a RawPeer) and
// belong to the host core and the remote lane.

// recordingRequest is a request recording its wakes and ends (upstream's `request()`).
type recordingRequest struct {
	who data.Principal
	log []string
}

func (r *recordingRequest) principal() data.Principal { return r.who }
func (r *recordingRequest) wake()                     { r.log = append(r.log, "wake") }
func (r *recordingRequest) end(err *data.ChordError)  { r.log = append(r.log, "end "+err.Message) }

func unboundRequestOf(principal data.Principal) *recordingRequest {
	return &recordingRequest{who: principal}
}

func unboundGuest(kind string) data.Principal { return delta.JsonObjectOf("kind", kind) }

func unboundLogsOf(requests ...*recordingRequest) [][]string {
	logs := make([][]string, len(requests))
	for i, request := range requests {
		logs[i] = request.log
	}
	return logs
}

func TestTheIndexWakesEveryRequestAtThePublicationOutOfTheIndexFirstADeletedOneIsNotWoken(t *testing.T) {
	index := newUnboundIndex()
	a, b, c := unboundRequestOf(nil), unboundRequestOf(nil), unboundRequestOf(nil)
	index.add("app.a", a)
	index.add("app.a", b)
	index.add("app.b", c)
	index.delete("app.a", b)
	index.wakeAll()
	if got, want := unboundLogsOf(a, b, c), [][]string{{"wake"}, nil, {"wake"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("logs = %v, want %v", got, want)
	}
	// Out of it: a second publication wakes nobody.
	index.wakeAll()
	if !reflect.DeepEqual(a.log, []string{"wake"}) {
		t.Fatalf("a.log = %v, want [wake]", a.log)
	}
}

func TestTheIndexEndsTheRequestsOfARevokedPrincipalOnlyAndEveryRequestAtTheHostsClose(t *testing.T) {
	index := newUnboundIndex()
	guest := unboundRequestOf(unboundGuest("guest"))
	owner := unboundRequestOf(unboundGuest("owner"))
	own := unboundRequestOf(nil)
	for _, each := range []*recordingRequest{guest, owner, own} {
		index.add("app.a", each)
	}
	revoked := data.NewChordError(data.ChordErrorInit{Code: data.CodeForbidden, Reason: data.ReasonRevoked, Message: "revoked"}, nil)
	index.revoke(func(principal data.Principal) bool { return principal.Value("kind") == "guest" }, revoked)
	if got, want := unboundLogsOf(guest, owner, own), [][]string{{"end revoked"}, nil, nil}; !reflect.DeepEqual(got, want) {
		t.Fatalf("logs = %v, want %v", got, want)
	}
	index.close(data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonClosed, Message: "closed"}, nil))
	if got, want := unboundLogsOf(owner, own), [][]string{{"end closed"}, {"end closed"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("logs = %v, want %v", got, want)
	}
}

// Own test, mirroring the wake order Pi's `Map` of names gives (insertion order of the names, the requests of a name in the
// order they were added): a name that left the index and came back is woken last.
func TestTheIndexWakesInInsertionOrderOfNamesAndRequests(t *testing.T) {
	index := newUnboundIndex()
	var order []string
	named := func(name string) *funcRequest {
		return &funcRequest{wakeFn: func() { order = append(order, name) }}
	}
	a1, b1, a2, c1 := named("a1"), named("b1"), named("a2"), named("c1")
	index.add("app.a", a1)
	index.add("app.b", b1)
	index.add("app.a", a2)
	index.add("app.c", c1)
	// app.a leaves and comes back: it is the last name now.
	index.delete("app.a", a1)
	index.delete("app.a", a2)
	index.add("app.a", a1)
	index.wakeAll()
	if want := []string{"b1", "c1", "a1"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("wake order = %v, want %v", order, want)
	}
}

// Own test, mirroring `revoke`: a request an earlier end removed from the index is not visited again, though it was in
// the index under two names when the walk began.
func TestTheIndexRevokeSkipsARequestAnEarlierEndRemoved(t *testing.T) {
	index := newUnboundIndex()
	var ends []string
	var shared *funcRequest
	shared = &funcRequest{who: unboundGuest("guest"), endFn: func(err *data.ChordError) {
		ends = append(ends, err.Message)
		index.delete("app.a", shared)
		index.delete("app.b", shared)
	}}
	index.add("app.a", shared)
	index.add("app.b", shared)
	index.revoke(func(data.Principal) bool { return true }, data.NewChordError(data.ChordErrorInit{Code: data.CodeForbidden, Reason: data.ReasonRevoked, Message: "revoked"}, nil))
	if want := []string{"revoked"}; !reflect.DeepEqual(ends, want) {
		t.Fatalf("ends = %v, want %v", ends, want)
	}
}

// funcRequest is a request whose wake and end are functions of the test.
type funcRequest struct {
	who    data.Principal
	wakeFn func()
	endFn  func(*data.ChordError)
}

func (r *funcRequest) principal() data.Principal { return r.who }
func (r *funcRequest) wake() {
	if r.wakeFn != nil {
		r.wakeFn()
	}
}
func (r *funcRequest) end(err *data.ChordError) {
	if r.endFn != nil {
		r.endFn(err)
	}
}

// --- upstream unbound.test.ts cases that run on hosts -------------------------------------------------------------------

var (
	unboundInfo = mustContract(declareLocal("app.info", contracts.LocalDeclaration{
		Version: 1,
		Members: []contracts.LocalEntry{contracts.Mem("title", contracts.Str())},
	}))
	unboundMissing = mustContract(declareLocal("app.missing", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
)

// serveInfo is `h.serve(Info, () => ({ title: constant("t") }))`.
func serveInfo(h FacetHost) error {
	return h.Serve(unboundInfo, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"title": constant("t")},
		}}, nil
	}, ServeOptions{})
}

// indexedBindings are the requests waiting in host's unbound index, as weak pointers to the bindings.
func indexedBindings(host Host) []weak.Pointer[binding] {
	h := host.(*chordHost)
	var weaks []weak.Pointer[binding]
	h.enter(func() {
		for _, name := range h.unbound.names.items() {
			for _, request := range h.unbound.byName[name].items() {
				if b, ok := request.(*binding); ok {
					weaks = append(weaks, weak.Make(b))
				}
			}
		}
	})
	return weaks
}

// indexedHolds are the requests waiting in host's unbound index, as weak pointers to the holds.
func indexedHolds(host Host) []weak.Pointer[hold] {
	h := host.(*chordHost)
	var weaks []weak.Pointer[hold]
	h.enter(func() {
		for _, name := range h.unbound.names.items() {
			for _, request := range h.unbound.byName[name].items() {
				if each, ok := request.(*hold); ok {
					weaks = append(weaks, weak.Make(each))
				}
			}
		}
	})
	return weaks
}

// Mirrors unbound.test.ts "references waiting in the index › before the publication: loading, placed by it; a type not served then waits failed { waiting }".
func TestHostReferencesWaitingInTheIndexBeforeThePublicationAreLoadingPlacedByIt(t *testing.T) {
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	info, err := host.Hold(unboundInfo, jsonObject(), background, GetOptions{})
	must(t, err)
	missing, err := host.Hold(unboundMissing, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	for _, ref := range []*Reference{info, missing} {
		if kind := ref.State().Value().Kind; kind != RefStateLoading {
			t.Fatalf("state before the publication = %s, want loading", kind)
		}
	}
	must(t, host.Load([]HostFacet{{Name: "@test/a", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return serveInfo(h) })}}, background))
	turn(host)
	if kind := info.State().Value().Kind; kind != RefStateLive {
		t.Fatalf("info state = %s, want live", kind)
	}
	got := missing.State().Value()
	if got.Kind != RefStateFailed || got.Cause.Kind != data.FailureWaiting || got.Cause.Error.Code != data.CodeUnavailable ||
		got.Cause.Error.Message != "app.missing is not served here" || got.RetryAt != nil {
		t.Fatalf("missing state = %+v, want failed waiting unavailable without retryAt", got)
	}
	// A retry leaves it waiting: an event wakes it, nothing else.
	missing.Retry()
	if got := missing.State().Value(); got.Kind != RefStateFailed || got.Cause.Kind != data.FailureWaiting {
		t.Fatalf("missing state after retry = %+v, want failed waiting", got)
	}
	must(t, host.Close(background))
	if got := missing.State().Value(); got.Kind != RefStateEnded || got.Error.Reason != data.ReasonClosed {
		t.Fatalf("missing state after close = %+v, want ended closed", got)
	}
}

// Mirrors "references waiting in the index › leave nothing behind once released, before and after the publication (GC)".
func TestHostReferencesWaitingInTheIndexLeaveNothingBehindOnceReleased(t *testing.T) {
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	var weaks []weak.Pointer[binding]
	func() {
		first, err := host.Hold(unboundMissing, jsonObject("id", float64(1)), background, GetOptions{})
		must(t, err)
		weaks = append(weaks, weak.Make(first.binding))
		first.Release()
		must(t, host.Load(nil, background))
		second, err := host.Hold(unboundMissing, jsonObject("id", float64(2)), background, GetOptions{})
		must(t, err)
		weaks = append(weaks, weak.Make(second.binding))
		second.Release()
		readyTimeout := time.Millisecond
		got := make(chan error, 1)
		go func() {
			_, err := host.Get(unboundMissing, jsonObject("id", float64(3)), background, GetOptions{ReadyTimeout: &readyTimeout})
			got <- err
		}()
		waiting := indexedBindings(host)
		for len(waiting) == 0 {
			select {
			case <-got:
				return
			default:
			}
			waiting = indexedBindings(host)
		}
		weaks = append(weaks, waiting...)
		<-got
	}()
	nexttest.ExpectCollected(t, weaks...)
	var left int
	h := host.(*chordHost)
	h.enter(func() { left = h.unbound.names.size() })
	if left != 0 {
		t.Fatalf("names left in the index = %d, want 0", left)
	}
}

// Mirrors "references waiting in the index › end at the start of the host's close, not after its slow native closes (fix round 1, Sol 6)".
func TestHostReferencesWaitingInTheIndexEndAtTheStartOfTheHostsClose(t *testing.T) {
	core := mustContract(declareLocal("app.core", contracts.LocalDeclaration{
		Version: 1,
		Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	closeGate := make(chan struct{})
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(core, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			selfOpen(self, func(context.Context) error { return nil }, func(NativeCloseReason) { <-closeGate })
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(float64(1))},
			}}, nil
		}, ServeOptions{})
	})
	must(t, host.Load([]HostFacet{{Name: "@test/a", Facet: facet}}, background))
	outcome := make(chan error, 1)
	go func() {
		_, err := host.Get(unboundMissing, jsonObject("id", float64(1)), background, GetOptions{})
		outcome <- err
	}()
	held, err := host.Hold(unboundMissing, jsonObject("id", float64(2)), background, GetOptions{})
	must(t, err)
	for len(indexedBindings(host)) < 2 {
		turn(host)
	}
	closing := make(chan error, 1)
	go func() { closing <- host.Close(background) }()
	err = <-outcome
	if chordErr, ok := data.IsChordError(err); !ok || chordErr.Code != data.CodeReleased || chordErr.Reason != data.ReasonClosed {
		t.Fatalf("the get's outcome = %v, want released/closed", err)
	}
	turn(host)
	if got := held.State().Value(); got.Kind != RefStateEnded || got.Error.Reason != data.ReasonClosed {
		t.Fatalf("held state = %+v, want ended closed", got)
	}
	close(closeGate)
	must(t, <-closing)
}

// early is a server not loaded yet, a raw consumer peer on a child link, and a hold of `app.missing` it sent.
func early(t *testing.T) (Host, *RawPeer, *link.MemoryLink, *link.MemoryLink) {
	t.Helper()
	address := serverAddress()
	host := newHost(t, HostOptions{Address: &address, Principal: serverPrincipal(), OnError: func(HostError) {}})
	a, b := link.MemoryLinkPair()
	must(t, host.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	peer := newRawPeer(t, b, rawPeerOptions{})
	params, err := link.JsonPart(jsonObject())
	must(t, err)
	peer.Send(&link.HoldMessage{
		Hold: 0, Type: "app.missing", Version: 1, Fingerprint: "0123456789abcdef", Params: params, Address: serverWireAddress,
	})
	transported(a, b)
	return host, peer, a, b
}

// Mirrors "holds waiting in the index › wait for the publication without a word, then are refused failed { waiting } and kept".
func TestHostHoldsWaitingInTheIndexWaitForThePublicationWithoutAWordThenAreRefused(t *testing.T) {
	host, peer, _, _ := early(t)
	if kinds := peer.Kinds(); !reflect.DeepEqual(kinds, []string{"hello"}) {
		t.Fatalf("kinds = %v, want [hello]", kinds)
	}
	must(t, host.Load(nil, background))
	failed := peer.Next("failed").(*link.FailedMessage)
	if failed.Hold != 0 || failed.Cause.Kind != "waiting" {
		t.Fatalf("failed = %+v, want hold 0 waiting", failed)
	}
	if got := activityOf(host).References; got != 1 {
		t.Fatalf("references = %d, want 1", got)
	}
	closeHosts(t, host)
}

// Mirrors "holds waiting in the index › leave nothing behind once their link closed, before and after the publication (GC)".
func TestHostHoldsWaitingInTheIndexLeaveNothingBehindOnceTheirLinkClosed(t *testing.T) {
	before, _, beforeLink, beforePeer := early(t)
	var weaks []weak.Pointer[hold]
	weaks = append(weaks, indexedHolds(before)...)
	beforeLink.Close()
	transported(beforeLink, beforePeer)
	must(t, before.Load(nil, background))
	after, afterPeer, afterLink, afterEnd := early(t)
	weaks = append(weaks, indexedHolds(after)...)
	must(t, after.Load(nil, background))
	afterPeer.Next("failed")
	afterLink.Close()
	transported(afterLink, afterEnd)
	if got := []int{activityOf(before).References, activityOf(after).References}; !reflect.DeepEqual(got, []int{0, 0}) {
		t.Fatalf("references = %v, want [0 0]", got)
	}
	if len(weaks) == 0 {
		t.Fatal("no hold ever waited in the index")
	}
	nexttest.ExpectCollected(t, weaks...)
	closeHosts(t, before, after)
}
