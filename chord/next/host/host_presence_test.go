package host

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// pi: packages/chord/src/host/presence.ts
// Ports packages/chord/test/host/presence.test.ts: presence (§19): `host.references(Type)`, its entries and
// incarnations, its batches (a link drop as a departure, one discovered while a commit sends lands in the next batch),
// derives over it, its cost and GC. The TestHostReferences cases run on whole hosts, as upstream does; the TestReferences
// cases are own tests of the list's bookkeeping (what each change records for the next fold) on a batch, without a host.

func presenceServe(h FacetHost) error {
	_, err := serveNotes(h, nil)
	return err
}

// presenceViewed is a remote type served by the test server with one `viewers` state member, as the race-9 cases use.
func presenceViewed(members ...string) *contracts.Contract {
	properties := make([]contracts.Property, len(members))
	for i, member := range members {
		properties[i] = contracts.Prop(member, contracts.Int())
	}
	return mustContract(contracts.Remote("app.viewed", contracts.RemoteDeclaration{
		Version:     1,
		Params:      contracts.Params{},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return serverAddress(), nil },
		Variants:    []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{State: properties})},
	}))
}

// presenceEntry is a presence entry as the list publishes it; principal nil has none.
func presenceEntry(session string, principal data.Principal, incarnation string) *delta.JsonObject {
	entry := jsonObject("params", jsonObject("session", session))
	if principal != nil {
		entry.Set("principal", principal)
	}
	entry.Set("incarnation", incarnation)
	return entry
}

func presenceList(t testing.TB, host Host, typ *contracts.Contract) []any {
	t.Helper()
	var value delta.JsonValue
	var err error
	host.(*chordHost).loop().Run(func() { value, err = host.References(typ).Value() })
	must(t, err)
	return value.([]any)
}

// presenceServer is a server host (address Server, principal server) loaded with setup.
func presenceServer(t testing.TB, setup func(h FacetHost) error, options HostOptions) Host {
	t.Helper()
	options.Address = new(Server.Address(nil))
	options.Principal = serverPrincipal()
	if options.OnError == nil {
		options.OnError = func(HostError) {}
	}
	server := newHost(t, options)
	mustLoad(t)(server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return setup(h) })}}, background))
	return server
}

// presenceTab is a client host connected to server as its child, stamped owner.
func presenceTab(t testing.TB, server Host) (Host, *link.MemoryLink) {
	t.Helper()
	tab := newHost(t, HostOptions{OnError: func(HostError) {}})
	mustLoad(t)(tab.Load(nil, background))
	a, b := link.MemoryLinkPair()
	must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	must(t, tab.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
	return tab, a
}

// presence.test.ts › host.references › lists every reference with the object's params, its principal and its incarnation.
func TestHostReferencesListsEveryReferenceWithTheObjectsParamsItsPrincipalAndItsIncarnation(t *testing.T) {
	hosts := linked(t, presenceServe, linkedOptions{Child: guestPrincipal("S1")})
	server, client := hosts.Server, hosts.Client
	notes := server.References(Notes)
	nexttest.RequireJSONEqual(t, presenceList(t, server, Notes), []any{})
	_, err := client.Get(background, Notes, jsonObject("session", "S1"), GetOptions{})
	must(t, err)
	own, err := server.Get(background, Notes, jsonObject("session", "S2"), GetOptions{})
	must(t, err)
	nexttest.RequireJSONEqual(t, presenceList(t, server, Notes), []any{
		presenceEntry("S1", guestPrincipal("S1"), incarnationOf(client)),
		presenceEntry("S2", serverPrincipal(), incarnationOf(server)),
	})
	_ = notes
	// A local type's entries have no principal: the store is referenced by both views.
	nexttest.RequireJSONEqual(t, presenceList(t, server, NoteStore), []any{
		presenceEntry("S1", nil, incarnationOf(server)),
		presenceEntry("S2", nil, incarnationOf(server)),
	})
	own.Release()
	if got := presenceList(t, server, Notes); len(got) != 1 {
		t.Fatalf("references after the release = %d, want 1", len(got))
	}
}

// presence.test.ts › host.references › is made for the first ask with the references that exist then, and kept.
func TestHostReferencesIsMadeForTheFirstAskWithTheReferencesThatExistThenAndKept(t *testing.T) {
	hosts := linked(t, presenceServe, linkedOptions{})
	_, err := hosts.Client.Get(background, Notes, jsonObject("session", "S1"), GetOptions{})
	must(t, err)
	notes := hosts.Server.References(Notes)
	if got := presenceList(t, hosts.Server, Notes); len(got) != 1 {
		t.Fatalf("references = %d, want 1", len(got))
	}
	if hosts.Server.References(Notes) != notes {
		t.Fatalf("the second ask made another list")
	}
}

// presence.test.ts › host.references › changes in the batch where a reference binds or ends: a watch gets one splice per change.
func TestHostReferencesChangesInTheBatchWhereAReferenceBindsOrEndsAWatchGetsOneSplicePerChange(t *testing.T) {
	hosts := linked(t, presenceServe, linkedOptions{})
	server, client := hosts.Server, hosts.Client
	watch, err := server.References(Notes).Watch(background)
	must(t, err)
	frames := nexttest.FramesOf(watch)
	// Pi's `await` of each `get` lets the watch deliver its frame before the next change (a listener that keeps up gets one
	// call per batch); here the scheduler delivers on goroutines, so wait for the deliveries between the steps.
	one, err := client.Get(background, Notes, jsonObject("session", "S1"), GetOptions{})
	must(t, err)
	deliveredOn(server, client)
	_, err = client.Get(background, Notes, jsonObject("session", "S2"), GetOptions{})
	must(t, err)
	deliveredOn(server, client)
	one.Release()
	transported(hosts.Links[0], hosts.Links[1])
	deliveredOn(server, client)
	incarnation := incarnationOf(client)
	entry := func(session string) *delta.JsonObject { return presenceEntry(session, ownerPrincipal(), incarnation) }
	got := frames.Items()
	nexttest.RequireJSONEqual(t, opsOf(got), []any{
		[]any{[]any{"p", []any{}, 0, 0, []any{entry("S1")}}},
		[]any{[]any{"p", []any{}, 1, 0, []any{entry("S2")}}},
		[]any{[]any{"p", []any{}, 0, 1, []any{}}},
	})
	nexttest.RequireJSONEqual(t, got[len(got)-1].Value, []any{entry("S2")})
}

// presence.test.ts › host.references › feeds derives in factories: viewers per session, a link drop a departure in its
// batch (race 9). Go's links deliver their close on a goroutine of their own, so the departure is read once the link
// flushed.
func TestHostReferencesFeedsDerivesInFactoriesViewersPerSessionALinkDropADepartureInItsBatchRace9(t *testing.T) {
	viewers := mustContract(contracts.Local("app.viewers", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("session", contracts.Str())},
		Members: []contracts.LocalEntry{contracts.Mem("count", contracts.Int())},
	}))
	shown := mustContract(contracts.Remote("app.shown", contracts.RemoteDeclaration{
		Version:     1,
		Params:      contracts.Params{contracts.Prop("session", contracts.Str())},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return serverAddress(), nil },
		Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{
			State: []contracts.Property{contracts.Prop("here", contracts.Int())},
		})},
	}))
	server := presenceServer(t, func(h FacetHost) error {
		if err := h.Serve(shown, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"here": Expose(state.NewConstant(float64(1)))},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(viewers, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			session := params.Value("session")
			count := self.Derive([]any{h.References(shown)}, func(values []any) (delta.JsonValue, error) {
				matching := 0
				for _, ref := range values[0].([]any) {
					if ref.(*delta.JsonObject).Value("params").(*delta.JsonObject).Value("session") == session {
						matching++
					}
				}
				return float64(matching), nil
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"count": count},
			}}, nil
		}, ServeOptions{})
	}, HostOptions{})
	view, err := server.Get(background, viewers, jsonObject("session", "S1"), GetOptions{})
	must(t, err)
	watch, err := view.Member("count").Watch(background)
	must(t, err)
	counts := nexttest.FramesOf(watch)
	var tabs [2]Host
	var links [2]*link.MemoryLink
	for i := range tabs {
		tabs[i], links[i] = presenceTab(t, server)
	}
	for _, tab := range tabs {
		_, err := tab.Get(background, shown, jsonObject("session", "S1"), GetOptions{})
		must(t, err)
	}
	_, err = tabs[0].Get(background, shown, jsonObject("session", "S2"), GetOptions{})
	must(t, err)
	count := func() any {
		value, err := view.Member("count").Value()
		must(t, err)
		return value
	}
	nexttest.RequireJSONEqual(t, count(), float64(2))
	links[1].Close()
	transported(links[1])
	// The departure is in the batch of the link's close: the derive has it.
	nexttest.RequireJSONEqual(t, count(), float64(1))
	deliveredOn(server)
	got := counts.Items()
	nexttest.RequireJSONEqual(t, got[len(got)-1].Value, float64(1))
}

// rawViewedTab is a raw tab on server holding Viewed with the owner principal; its server end closes while it sends once
// drop (when given) is armed (a transport failing on send).
func rawViewedTab(t testing.TB, server Host, viewed *contracts.Contract, drop *atomic.Bool, params string) (*RawPeer, *link.MemoryLink) {
	t.Helper()
	a, b := link.MemoryLinkPair()
	var end link.Link = a
	if drop != nil {
		end = closingOnSend(a, drop)
	}
	must(t, server.Accept(end, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	return newRawPeer(t, b, rawPeerOptions{}), a
}

// presence.test.ts › host.references › puts a departure discovered while a commit sends into the next batch: one state
// message per commit (race 9).
func TestHostReferencesPutsADepartureDiscoveredWhileACommitSendsIntoTheNextBatchOneStateMessagePerCommitRace9(t *testing.T) {
	viewed := presenceViewed("count", "viewers")
	server := presenceServer(t, func(h FacetHost) error {
		if _, err := serveNotes(h, nil); err != nil {
			return err
		}
		return h.Serve(viewed, func(params *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			session := "S1"
			store := self.Get(NoteStore, jsonObject("session", session))
			count := self.Derive([]any{store.Member("list")}, func(values []any) (delta.JsonValue, error) {
				return float64(len(values[0].([]any))), nil
			})
			viewers := self.Derive([]any{h.References(viewed)}, func(values []any) (delta.JsonValue, error) {
				return float64(len(values[0].([]any))), nil
			})
			return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"count": count, "viewers": viewers},
			}}, nil
		}, ServeOptions{})
	}, HostOptions{})
	names := wireNames(t, viewed, "v")
	drop := &atomic.Bool{}
	tab := func(armed *atomic.Bool) (*RawPeer, *link.MemoryLink) {
		peer, end := rawViewedTab(t, server, viewed, armed, "{}")
		peer.Send(holdOf(viewed, 0, "{}"))
		return peer, end
	}
	// The dropped tab binds first, so its link sends (and fails) before the kept one's in the commit's flushes.
	dropped, droppedLink := tab(drop)
	dropped.Next("bound")
	kept, keptLink := tab(nil)
	kept.Next("bound")
	transported(droppedLink, keptLink)
	kept.takeReceived()
	drop.Store(true)
	addNote(t, server, "S1", "x")
	transported(droppedLink, keptLink)
	var changed [][]int
	for _, message := range kept.Received() {
		if states, ok := message.(*link.StateMessage); ok {
			var members []int
			for _, entry := range states.Holds {
				for _, member := range entry.Members {
					members = append(members, member.Member)
				}
			}
			changed = append(changed, members)
		}
	}
	// The commit's message carries the commit (the count); the departure is the next batch's (the viewers).
	want := [][]int{{names.State["count"]}, {names.State["viewers"]}}
	if len(changed) != 2 || !slices.Equal(changed[0], want[0]) || !slices.Equal(changed[1], want[1]) {
		t.Fatalf("members of the state messages = %v, want %v", changed, want)
	}
	if !dropped.Closed() {
		t.Fatalf("the dropped tab's link is open")
	}
}

// presence.test.ts › host.references › completes a hold's bookkeeping before its presence entry publishes: a link closing
// then leaks nothing.
func TestHostReferencesCompletesAHoldsBookkeepingBeforeItsPresenceEntryPublishesALinkClosingThenLeaksNothing(t *testing.T) {
	viewed := presenceViewed("viewers")
	linger := 60 * time.Second
	server := presenceServer(t, func(h FacetHost) error {
		return h.Serve(viewed, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			viewers := self.Derive([]any{h.References(viewed)}, func(values []any) (delta.JsonValue, error) {
				return float64(len(values[0].([]any))), nil
			})
			return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"viewers": viewers},
			}}, nil
		}, ServeOptions{Linger: &linger})
	}, HostOptions{})
	drop := &atomic.Bool{}
	peer, end := rawViewedTab(t, server, viewed, drop, "{}")
	peer.Send(holdOf(viewed, 0, "{}"))
	peer.Next("bound")
	peer.Send(&link.AckMessage{})
	transported(end)
	// The second hold's presence entry changes the first's state: sending it closes the link.
	drop.Store(true)
	peer.Send(holdOf(viewed, 1, "{}"))
	transported(end)
	if !peer.Closed() {
		t.Fatalf("the peer's link is open")
	}
	nexttest.RequireJSONEqual(t, presenceList(t, server, viewed), []any{})
	if references, lingering := countOf(t, server, "references"), countOf(t, server, "lingering"); references != 0 || lingering != 1 {
		t.Fatalf("activity = references %v lingering %v, want 0 and 1", references, lingering)
	}
	must(t, server.Close(background))
}

// presence.test.ts › host.references › completes a release's bookkeeping before its departure publishes: a link closing
// then leaves no lingering. The 50 ms linger runs on the host's virtual timers.
func TestHostReferencesCompletesAReleasesBookkeepingBeforeItsDeparturePublishesALinkClosingThenLeavesNoLingering(t *testing.T) {
	viewed := presenceViewed("viewers")
	linger := 50 * time.Millisecond
	timers := newScopeLoop()
	server := presenceServer(t, func(h FacetHost) error {
		return h.Serve(viewed, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			viewers := self.Derive([]any{h.References(viewed)}, func(values []any) (delta.JsonValue, error) {
				return float64(len(values[0].([]any))), nil
			})
			return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"viewers": viewers},
			}}, nil
		}, ServeOptions{Linger: &linger})
	}, HostOptions{loop: timers})
	drop := &atomic.Bool{}
	peer, end := rawViewedTab(t, server, viewed, drop, "{}")
	for hold := range 2 {
		peer.Send(holdOf(viewed, hold, "{}"))
		peer.Next("bound")
		peer.Send(&link.AckMessage{})
	}
	transported(end)
	// The first release changes the second hold's state: sending it closes the link, which releases the second.
	drop.Store(true)
	peer.Send(&link.ReleaseMessage{Hold: 0})
	transported(end)
	if !peer.Closed() {
		t.Fatalf("the peer's link is open")
	}
	if references, lingering := countOf(t, server, "references"), countOf(t, server, "lingering"); references != 0 || lingering != 1 {
		t.Fatalf("activity = references %v lingering %v, want 0 and 1", references, lingering)
	}
	timers.AdvanceAsync(100)
	timers.barrier()
	nexttest.RequireJSONEqual(t, presenceList(t, server, viewed), []any{})
	if references, lingering := countOf(t, server, "references"), countOf(t, server, "lingering"); references != 0 || lingering != 0 {
		t.Fatalf("activity = references %v lingering %v, want 0 and 0", references, lingering)
	}
	must(t, server.Close(background))
}

// presence.test.ts › host.references › removes a bulk departure with one list and one splice op per entry, in one frame (a
// revoke of many holds).
func TestHostReferencesRemovesABulkDepartureWithOneListAndOneSpliceOpPerEntryInOneFrameARevokeOfManyHolds(t *testing.T) {
	server := presenceServer(t, presenceServe, HostOptions{
		RevokedBy: func(principal data.Principal) ([]delta.JsonValue, error) {
			if principalKind(principal) == "guest" {
				return []delta.JsonValue{jsonObject("session", principal.Value("session"))}, nil
			}
			return nil, nil
		},
	})
	a, b := link.MemoryLinkPair()
	must(t, server.Accept(a, LinkOptions{Principal: guestPrincipal("S1"), Codec: testCodec()}, background))
	peer := newRawPeer(t, b, rawPeerOptions{})
	for hold := range 4 {
		peer.Send(holdOf(Notes, hold, `{"session":"S1"}`))
	}
	transported(a, b)
	references := server.References(Notes)
	watch, err := references.Watch(background)
	must(t, err)
	frames := nexttest.FramesOf(watch)
	if got := presenceList(t, server, Notes); len(got) != 4 {
		t.Fatalf("references = %d, want 4", len(got))
	}
	must(t, server.Revoke(jsonObject("session", "S1"), background))
	deliveredOn(server)
	got := frames.Items()
	splice := []any{"p", []any{}, 0, 1, []any{}}
	nexttest.RequireJSONEqual(t, opsOf(got), []any{[]any{splice, splice, splice, splice}})
	nexttest.RequireJSONEqual(t, got[0].Value, []any{})
}

// presence.test.ts › host.references › counts two tabs through a relay as two incarnations on the worker.
func TestHostReferencesCountsTwoTabsThroughARelayAsTwoIncarnationsOnTheWorker(t *testing.T) {
	relayed := relay(t, relayedOptions{})
	second := newHost(t, HostOptions{OnError: func(HostError) {}})
	mustLoad(t)(second.Load(nil, background))
	a, b := link.MemoryLinkPair()
	must(t, relayed.Server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	must(t, second.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
	_, err := relayed.Tab.Get(background, PlacedNotes, jsonObject("session", "S1"), GetOptions{})
	must(t, err)
	_, err = second.Get(background, PlacedNotes, jsonObject("session", "S1"), GetOptions{})
	must(t, err)
	worker := relayed.Worker("session|S1")
	var incarnations []any
	for _, entry := range presenceList(t, worker, PlacedNotes) {
		incarnations = append(incarnations, entry.(*delta.JsonObject).Value("incarnation"))
	}
	nexttest.RequireJSONEqual(t, incarnations, []any{incarnationOf(relayed.Tab), incarnationOf(second)})
}

// presence.test.ts › host.references › has no entries for lingering objects.
func TestHostReferencesHasNoEntriesForLingeringObjects(t *testing.T) {
	kept := mustContract(contracts.Remote("app.kept", contracts.RemoteDeclaration{
		Version:     1,
		Params:      contracts.Params{},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return serverAddress(), nil },
		Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{
			State: []contracts.Property{contracts.Prop("n", contracts.Int())},
		})},
	}))
	linger := 60 * time.Second
	hosts := linked(t, func(h FacetHost) error {
		return h.Serve(kept, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": Expose(state.NewConstant(float64(1)))},
			}}, nil
		}, ServeOptions{Linger: &linger})
	}, linkedOptions{})
	held, err := hosts.Client.Get(background, kept, jsonObject(), GetOptions{})
	must(t, err)
	if got := presenceList(t, hosts.Server, kept); len(got) != 1 {
		t.Fatalf("references = %d, want 1", len(got))
	}
	held.Release()
	transported(hosts.Links[0], hosts.Links[1])
	nexttest.RequireJSONEqual(t, presenceList(t, hosts.Server, kept), []any{})
	if got := countOf(t, hosts.Server, "lingering"); got != 1 {
		t.Fatalf("lingering = %v, want 1", got)
	}
	must(t, hosts.Server.Close(background))
}

// presence.test.ts › host.references › keeps no released reference (GC).
func TestHostReferencesKeepsNoReleasedReferenceGC(t *testing.T) {
	hosts := linked(t, presenceServe, linkedOptions{})
	server := hosts.Server
	server.References(Notes)
	notes, err := hosts.Client.Get(background, Notes, jsonObject("session", "S1"), GetOptions{})
	must(t, err)
	if got := presenceList(t, server, Notes); len(got) != 1 {
		t.Fatalf("references = %d, want 1", len(got))
	}
	var refs []weak.Pointer[hold]
	for _, held := range serverHolds(server) {
		refs = append(refs, weak.Make(held))
	}
	if len(refs) == 0 {
		t.Fatalf("the server has no hold for the client's reference")
	}
	notes.Release()
	transported(hosts.Links[0], hosts.Links[1])
	nexttest.RequireJSONEqual(t, presenceList(t, server, Notes), []any{})
	expectCollectedEventually(t, refs...)
	nexttest.RequireJSONEqual(t, presenceList(t, server, Notes), []any{})
}

// referenceHolder is a holder that is only a caller: what `references` reads of one (the rest of holder is never called).
type referenceHolder struct {
	holder
	from caller
}

func (h *referenceHolder) caller() caller { return h.from }

// referencesHarness is a `references` of one remote type on a batch whose watches deliver on a scheduler of their own: the
// cases of presence.ts that are about the list itself (its bookkeeping per change), without a host.
type referencesHarness struct {
	t         *testing.T
	scheduler *chord.DeliveryScheduler
	refs      *references
	object    *chordObject
}

func newReferencesHarness(t *testing.T, contract *contracts.Contract) *referencesHarness {
	t.Helper()
	h := &referencesHarness{t: t, scheduler: chord.NewDeliveryScheduler()}
	batch := state.NewBatch(func(err state.BatchError) { t.Errorf("batch reported %s: %v", err.Kind, err.Error) }, nil).UseScheduler(h.scheduler)
	h.refs = newReferences(batch)
	h.object = &chordObject{params: jsonObject("session", "S1"), served: &served{contract: contract}}
	return h
}

func (h *referencesHarness) holder(session string) *referenceHolder {
	return &referenceHolder{from: caller{principal: guestPrincipal(session), incarnation: "inc-" + session}}
}

func (h *referencesHarness) watch() *nexttest.Collector[nexttest.Frame[delta.JsonValue]] {
	h.t.Helper()
	watch, err := h.refs.Watch(background)
	must(h.t, err)
	return nexttest.FramesOf(watch)
}

func (h *referencesHarness) list() []any {
	h.t.Helper()
	value, err := h.refs.Value()
	must(h.t, err)
	return value.([]any)
}

// presence.ts `remove`: `if (entry === undefined) continue`. A holder that has no entry (it was never listed, or already
// gone) changes nothing, and does not stop the walk over the holders after it.
func TestReferencesRemoveSkipsAHolderWithNoEntryAndGoesOnWithTheRest(t *testing.T) {
	h := newReferencesHarness(t, Notes)
	frames := h.watch()
	h.scheduler.Wait()
	first, second, stranger := h.holder("S1"), h.holder("S2"), h.holder("S9")
	h.refs.add(h.object, first, background)
	h.refs.add(h.object, second, background)
	h.scheduler.Wait()
	before := len(frames.Items())
	h.refs.remove([]holder{stranger}, background)
	h.scheduler.Wait()
	if got := len(frames.Items()); got != before {
		t.Fatalf("removing a holder with no entry published %d frames, want none", got-before)
	}
	nexttest.RequireJSONEqual(t, h.list(), []any{
		presenceEntry("S1", guestPrincipal("S1"), "inc-S1"),
		presenceEntry("S1", guestPrincipal("S2"), "inc-S2"),
	})
	h.refs.remove([]holder{stranger, first}, background)
	h.scheduler.Wait()
	nexttest.RequireJSONEqual(t, opsOf(frames.Items()[before:]), []any{[]any{[]any{"p", []any{}, 0, 1, []any{}}}})
	nexttest.RequireJSONEqual(t, h.list(), []any{presenceEntry("S1", guestPrincipal("S2"), "inc-S2")})
}

// presence.ts `remove`: an entry added in the same batch is spliced out of the pending additions, so the batch publishes
// neither its addition nor its removal.
func TestReferencesAnEntryAddedAndRemovedInOneBatchIsNeverPublished(t *testing.T) {
	h := newReferencesHarness(t, Notes)
	frames := h.watch()
	h.scheduler.Wait()
	stays, gone := h.holder("S1"), h.holder("S2")
	h.refs.add(h.object, stays, background)
	h.scheduler.Wait()
	before := len(frames.Items())
	must(t, h.refs.Batch().Within(func() error {
		h.refs.add(h.object, gone, background)
		h.refs.remove([]holder{gone}, background)
		return nil
	}, background))
	h.scheduler.Wait()
	if got := len(frames.Items()); got != before {
		t.Fatalf("an entry added and removed in one batch published %d frames, want none", got-before)
	}
	nexttest.RequireJSONEqual(t, h.list(), []any{presenceEntry("S1", guestPrincipal("S1"), "inc-S1")})
	// Two entries added in one batch, the first removed in it: one splice of the one that is left.
	first, second := h.holder("S3"), h.holder("S4")
	must(t, h.refs.Batch().Within(func() error {
		h.refs.add(h.object, first, background)
		h.refs.add(h.object, second, background)
		h.refs.remove([]holder{first}, background)
		return nil
	}, background))
	h.scheduler.Wait()
	nexttest.RequireJSONEqual(t, opsOf(frames.Items()[before:]), []any{
		[]any{[]any{"p", []any{}, 1, 0, []any{presenceEntry("S1", guestPrincipal("S4"), "inc-S4")}}},
	})
}

// presence.ts `record`: a `Map.set` of a holder that is listed already keeps its place and takes the new entry.
func TestReferencesRecordingAHolderAgainKeepsItsPlaceWithTheNewEntry(t *testing.T) {
	h := newReferencesHarness(t, Notes)
	first, second := h.holder("S1"), h.holder("S2")
	h.refs.add(h.object, first, background)
	h.refs.add(h.object, second, background)
	first.from = caller{principal: guestPrincipal("S1"), incarnation: "moved"}
	h.refs.add(h.object, first, background)
	nexttest.RequireJSONEqual(t, h.list(), []any{
		presenceEntry("S1", guestPrincipal("S1"), "moved"),
		presenceEntry("S1", guestPrincipal("S2"), "inc-S2"),
	})
}

// presence.ts `#follows`: while nobody watches, a change is not folded: the list is dropped, nothing is kept of the change,
// and the next read builds the list from the entries.
func TestReferencesChangesWhileNobodyWatchesKeepNothingToFold(t *testing.T) {
	h := newReferencesHarness(t, Notes)
	first, second := h.holder("S1"), h.holder("S2")
	h.refs.add(h.object, first, background)
	h.refs.add(h.object, second, background)
	h.refs.remove([]holder{first}, background)
	h.scheduler.Wait()
	if len(h.refs.added) != 0 || len(h.refs.removed) != 0 || h.refs.hasList {
		t.Fatalf("unwatched changes left added=%d removed=%d hasList=%v, want nothing to fold and no list", len(h.refs.added), len(h.refs.removed), h.refs.hasList)
	}
	nexttest.RequireJSONEqual(t, h.list(), []any{presenceEntry("S1", guestPrincipal("S2"), "inc-S2")})
}

// presence.ts `record`: an entry has the principal only for a remote type; a local type's has none.
func TestReferencesAnEntryOfALocalTypeHasNoPrincipal(t *testing.T) {
	h := newReferencesHarness(t, NoteStore)
	h.refs.add(h.object, h.holder("S1"), background)
	nexttest.RequireJSONEqual(t, h.list(), []any{presenceEntry("S1", nil, "inc-S1")})
}

// presence.ts `deliver`: a frame of the list is a value, never a retirement of the source.
func TestReferencesFramesAreNotRetirements(t *testing.T) {
	h := newReferencesHarness(t, Notes)
	watch, err := h.refs.Watch(background)
	must(t, err)
	var retired []bool
	var mu sync.Mutex
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		mu.Lock()
		defer mu.Unlock()
		retired = append(retired, call.Retired)
		return nil
	})
	first := h.holder("S1")
	h.refs.add(h.object, first, background)
	h.scheduler.Wait()
	h.refs.remove([]holder{first}, background)
	h.scheduler.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(retired) < 2 {
		t.Fatalf("the watch got %d frames, want one per change", len(retired))
	}
	for i, each := range retired {
		if each {
			t.Fatalf("frame %d retired the list", i)
		}
	}
}

// presence.ts `#follows`: a list that was read (built) while nobody watches is dropped by the next change, and the change
// is not recorded for a fold; and a fold clears what it folded, a removal's entries included.
func TestReferencesALatelyReadListIsDroppedByTheNextChangeWhileNobodyWatches(t *testing.T) {
	h := newReferencesHarness(t, Notes)
	first := h.holder("S1")
	h.refs.add(h.object, first, background)
	h.list()
	if !h.refs.hasList {
		t.Fatal("reading the list kept no list")
	}
	h.refs.add(h.object, h.holder("S2"), background)
	if h.refs.hasList || len(h.refs.added) != 0 {
		t.Fatalf("a change with nobody watching left hasList=%v added=%d, want the list dropped and nothing recorded", h.refs.hasList, len(h.refs.added))
	}
	nexttest.RequireJSONEqual(t, h.list(), []any{
		presenceEntry("S1", guestPrincipal("S1"), "inc-S1"),
		presenceEntry("S1", guestPrincipal("S2"), "inc-S2"),
	})
	// Watched: a removal folds into the list and its entry is cleared from the removals.
	frames := h.watch()
	h.scheduler.Wait()
	h.refs.remove([]holder{first}, background)
	h.scheduler.Wait()
	if len(h.refs.removed) != 0 || len(h.refs.added) != 0 {
		t.Fatalf("a delivered batch left removed=%d added=%d, want both folded away", len(h.refs.removed), len(h.refs.added))
	}
	nexttest.RequireJSONEqual(t, frames.Items()[len(frames.Items())-1].Value, []any{presenceEntry("S1", guestPrincipal("S2"), "inc-S2")})
}

// presence.ts `#fold`: a list made from the entries starts clean: what an earlier watched change left pending (additions,
// removals, ops) is not folded again. A watch that stops in the middle of a batch leaves such changes, and the next change
// drops the list, which the batch's delivery builds again from the entries.
func TestReferencesAListBuiltFromTheEntriesForgetsTheChangesPendingForTheOldOne(t *testing.T) {
	h := newReferencesHarness(t, Notes)
	gone, stays, later, last := h.holder("S0"), h.holder("S1"), h.holder("S2"), h.holder("S3")
	h.refs.add(h.object, gone, background)
	old, err := h.refs.Watch(background)
	must(t, err)
	must(t, h.refs.Batch().Within(func() error {
		h.refs.remove([]holder{gone}, background)
		h.refs.add(h.object, stays, background)
		// Read in the middle of the batch: its changes are folded into the list and its ops.
		h.list()
		if len(h.refs.ops) == 0 {
			t.Error("reading the list in a batch left no ops to publish")
		}
		// Changes after the read are pending for the next fold.
		h.refs.add(h.object, later, background)
		h.refs.remove([]holder{stays}, background)
		if len(h.refs.added) != 1 || len(h.refs.removed) != 1 {
			t.Errorf("pending changes: added=%d removed=%d, want 1 and 1", len(h.refs.added), len(h.refs.removed))
		}
		// Nobody watches now: the next change drops the list.
		if _, err := old.Stop(); err != nil {
			t.Errorf("Stop: %v", err)
		}
		h.refs.add(h.object, last, background)
		if h.refs.hasList {
			t.Error("a change with nobody watching kept the list")
		}
		return nil
	}, background))
	h.scheduler.Wait()
	if len(h.refs.added) != 0 || len(h.refs.removed) != 0 || len(h.refs.ops) != 0 || !h.refs.hasList {
		t.Fatalf("the list built from the entries kept added=%d removed=%d ops=%d (hasList %v), want none and a list", len(h.refs.added), len(h.refs.removed), len(h.refs.ops), h.refs.hasList)
	}
	nexttest.RequireJSONEqual(t, h.list(), []any{
		presenceEntry("S1", guestPrincipal("S2"), "inc-S2"),
		presenceEntry("S1", guestPrincipal("S3"), "inc-S3"),
	})
}

// presence.ts `#fold` with no list: the list is built from the entries and what was pending for the list that is gone
// (additions, removals, ops) is dropped with it, in the one fold that builds it.
func TestReferencesFoldingWithNoListBuildsItFromTheEntriesAndDropsWhatWasPending(t *testing.T) {
	h := newReferencesHarness(t, Notes)
	h.refs.add(h.object, h.holder("S1"), background)
	stale := presenceEntry("S9", guestPrincipal("S9"), "inc-S9")
	h.refs.hasList, h.refs.list = false, nil
	h.refs.added = []*delta.JsonObject{stale}
	h.refs.removed[stale] = struct{}{}
	h.refs.ops = []delta.Op{{"p", []any{}, 0, 0, []any{stale}}}
	value, err := h.refs.Value()
	must(t, err)
	nexttest.RequireJSONEqual(t, value, []any{presenceEntry("S1", guestPrincipal("S1"), "inc-S1")})
	if len(h.refs.added) != 0 || len(h.refs.removed) != 0 || len(h.refs.ops) != 0 {
		t.Fatalf("the fold that built the list kept added=%d removed=%d ops=%d, want none", len(h.refs.added), len(h.refs.removed), len(h.refs.ops))
	}
}

// presence.ts `remove`: holders that end while nobody watches are all removed from the entries, not only the first.
func TestReferencesRemovingSeveralHoldersWhileNobodyWatchesRemovesEachOfThem(t *testing.T) {
	h := newReferencesHarness(t, Notes)
	first, second, third := h.holder("S1"), h.holder("S2"), h.holder("S3")
	for _, each := range []*referenceHolder{first, second, third} {
		h.refs.add(h.object, each, background)
	}
	h.refs.remove([]holder{first, second}, background)
	nexttest.RequireJSONEqual(t, h.list(), []any{presenceEntry("S1", guestPrincipal("S3"), "inc-S3")})
	if len(h.refs.byHolder) != 1 {
		t.Fatalf("holders left in the entries = %d, want 1", len(h.refs.byHolder))
	}
}
