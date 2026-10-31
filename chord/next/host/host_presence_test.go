package host

import (
	"slices"
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

// Ports packages/chord/test/next/host/presence.test.ts: presence (§19): `host.references(Type)`, its entries and
// incarnations, its batches (a link drop as a departure, one discovered while a commit sends lands in the next batch),
// derives over it, its cost and GC. Every case runs on whole hosts, as upstream does.

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
	must(t, server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return setup(h) })}}, background))
	return server
}

// presenceTab is a client host connected to server as its child, stamped owner.
func presenceTab(t testing.TB, server Host) (Host, *link.MemoryLink) {
	t.Helper()
	tab := newHost(t, HostOptions{OnError: func(HostError) {}})
	must(t, tab.Load(nil, background))
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
	_, err := client.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
	must(t, err)
	own, err := server.Get(Notes, jsonObject("session", "S2"), background, GetOptions{})
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
	_, err := hosts.Client.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
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
	one, err := client.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
	must(t, err)
	deliveredOn(server, client)
	_, err = client.Get(Notes, jsonObject("session", "S2"), background, GetOptions{})
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
	view, err := server.Get(viewers, jsonObject("session", "S1"), background, GetOptions{})
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
		_, err := tab.Get(shown, jsonObject("session", "S1"), background, GetOptions{})
		must(t, err)
	}
	_, err = tabs[0].Get(shown, jsonObject("session", "S2"), background, GetOptions{})
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
	must(t, second.Load(nil, background))
	a, b := link.MemoryLinkPair()
	must(t, relayed.Server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	must(t, second.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
	_, err := relayed.Tab.Get(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
	must(t, err)
	_, err = second.Get(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
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
	held, err := hosts.Client.Get(kept, jsonObject(), background, GetOptions{})
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
	notes, err := hosts.Client.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
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
