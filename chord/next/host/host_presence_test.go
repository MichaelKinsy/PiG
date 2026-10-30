package host

import (
	"context"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Own tests of host_presence.go (packages/chord/test/next/host/presence.test.ts). Every upstream case builds hosts and
// links (`linked`, `relayed`, `createHost`, raw peers), whose objects and holds are later lanes: they are listed `missing`
// in the lane's rows. These tests run References on a real state.Batch with fake objects and holders, and each names the
// Pi case or doc comment whose behavior it carries; none replaces a `missing` case.

type presenceFakeObject struct {
	params any
	kind   contracts.Kind
}

func (o presenceFakeObject) Params() any                { return o.params }
func (o presenceFakeObject) ServedKind() contracts.Kind { return o.kind }

type presenceFakeHolder struct{ from caller }

func (h *presenceFakeHolder) Caller() caller { return h.from }

// presenceRig is a References over a batch whose watches deliver on a scheduler of its own.
type presenceRig struct {
	refs      *references
	batch     *state.Batch
	scheduler *chord.DeliveryScheduler
}

func newPresenceRig() *presenceRig {
	scheduler := chord.NewDeliveryScheduler()
	batch := state.NewBatch(func(state.BatchError) {}, nil).UseScheduler(scheduler)
	return &presenceRig{refs: newReferences(batch), batch: batch, scheduler: scheduler}
}

func (r *presenceRig) frames(t testing.TB) *nexttest.Collector[nexttest.Frame[delta.JsonValue]] {
	t.Helper()
	watch, err := r.refs.Watch(chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	return nexttest.FramesOf(watch)
}

// batchSubscriber is a synchronous subscriber of the list (a hold over a link): it is fed once per batch that published,
// inside the batch, so it sees what a watch would fold.
type batchSubscriber struct {
	frames *nexttest.Collector[nexttest.Frame[delta.JsonValue]]
}

func (s batchSubscriber) Advance(call chord.WatchCall[delta.JsonValue], _ context.Context) {
	if call.Kind == chord.WatchCallValue {
		s.frames.Append(nexttest.Frame[delta.JsonValue]{Value: call.Value, Ops: call.Ops})
	}
}
func (batchSubscriber) End(chord.WatchEnd) {}
func (batchSubscriber) EndAfterDelivery()  {}

// batches follows the list as a subscriber does: after reading it, one frame per batch.
func (r *presenceRig) batches(t testing.TB) *nexttest.Collector[nexttest.Frame[delta.JsonValue]] {
	t.Helper()
	if _, err := r.refs.Value(); err != nil {
		t.Fatal(err)
	}
	frames := &nexttest.Collector[nexttest.Frame[delta.JsonValue]]{}
	r.refs.Subscribe(batchSubscriber{frames: frames})
	return frames
}

// entry is a presence entry as the list publishes it.
func presenceEntry(params *delta.JsonObject, principal *delta.JsonObject, incarnation string) *delta.JsonObject {
	object := delta.NewJsonObject(3)
	object.Set("params", params)
	if principal != nil {
		object.Set("principal", principal)
	}
	object.Set("incarnation", incarnation)
	return object
}

func presenceSession(id string) *delta.JsonObject { return delta.JsonObjectOf("session", id) }

func presenceOps(ops []delta.Op) []any {
	out := make([]any, len(ops))
	for i, op := range ops {
		out[i] = op
	}
	return out
}

func presenceList(t testing.TB, refs *references) []any {
	t.Helper()
	value, err := refs.Value()
	if err != nil {
		t.Fatal(err)
	}
	return value.([]any)
}

func presenceHolderOf(principal *delta.JsonObject, incarnation string) *presenceFakeHolder {
	return &presenceFakeHolder{from: caller{principal: principal, incarnation: incarnation}}
}

var presenceBackground = chordctx.Background()

// Mirrors "host.references › lists every reference with the object's params, its principal and its incarnation": a remote
// type's entry has the holder's principal when it has one, a local type's none.
func TestReferencesListsEveryReferenceWithTheObjectsParamsItsPrincipalAndItsIncarnation(t *testing.T) {
	rig := newPresenceRig()
	guest := delta.JsonObjectOf("kind", "guest", "session", "S1")
	server := delta.JsonObjectOf("kind", "server")
	remote := presenceFakeObject{params: presenceSession("S1"), kind: contracts.KindRemote}
	rig.refs.add(remote, presenceHolderOf(guest, "client"), presenceBackground)
	rig.refs.add(presenceFakeObject{params: presenceSession("S2"), kind: contracts.KindRemote}, presenceHolderOf(server, "server"), presenceBackground)
	rig.refs.add(remote, presenceHolderOf(nil, "anonymous"), presenceBackground)
	want := []any{
		presenceEntry(presenceSession("S1"), guest, "client"),
		presenceEntry(presenceSession("S2"), server, "server"),
		presenceEntry(presenceSession("S1"), nil, "anonymous"),
	}
	nexttest.RequireJSONEqual(t, presenceList(t, rig.refs), want)

	// A local type's entries have no principal.
	local := newPresenceRig()
	local.refs.add(presenceFakeObject{params: presenceSession("S1"), kind: contracts.KindLocal}, presenceHolderOf(server, "server"), presenceBackground)
	nexttest.RequireJSONEqual(t, presenceList(t, local.refs), []any{presenceEntry(presenceSession("S1"), nil, "server")})
}

// Mirrors "host.references › changes in the batch where a reference binds or ends: a watch gets one splice per change".
func TestReferencesChangeInTheBatchWhereAReferenceBindsOrEndsAWatchGetsOneSplicePerChange(t *testing.T) {
	rig := newPresenceRig()
	frames := rig.frames(t)
	owner := delta.JsonObjectOf("kind", "owner")
	object := func(id string) presenceFakeObject {
		return presenceFakeObject{params: presenceSession(id), kind: contracts.KindRemote}
	}
	one, two := presenceHolderOf(owner, "client"), presenceHolderOf(owner, "client")
	rig.refs.add(object("S1"), one, presenceBackground)
	rig.scheduler.Wait()
	rig.refs.add(object("S2"), two, presenceBackground)
	rig.scheduler.Wait()
	rig.refs.remove([]presenceHolder{one}, presenceBackground)
	rig.scheduler.Wait()
	s1, s2 := presenceEntry(presenceSession("S1"), owner, "client"), presenceEntry(presenceSession("S2"), owner, "client")
	got := frames.Items()
	if len(got) != 3 {
		t.Fatalf("frames = %d, want 3", len(got))
	}
	for i, ops := range [][]any{
		{[]any{"p", []any{}, 0, 0, []any{s1}}},
		{[]any{"p", []any{}, 1, 0, []any{s2}}},
		{[]any{"p", []any{}, 0, 1, []any{}}},
	} {
		nexttest.RequireJSONEqual(t, presenceOps(got[i].Ops), ops)
	}
	nexttest.RequireJSONEqual(t, got[2].Value, []any{s2})
}

// Mirrors "host.references › removes a bulk departure with one list and one splice op per entry, in one frame (a revoke of
// many holds)".
func TestReferencesRemovesABulkDepartureWithOneListAndOneSpliceOpPerEntryInOneFrame(t *testing.T) {
	rig := newPresenceRig()
	guest := delta.JsonObjectOf("kind", "guest", "session", "S1")
	var holders []presenceHolder
	for range 4 {
		each := presenceHolderOf(guest, "client")
		holders = append(holders, each)
		rig.refs.add(presenceFakeObject{params: presenceSession("S1"), kind: contracts.KindRemote}, each, presenceBackground)
	}
	frames := rig.batches(t)
	if len(presenceList(t, rig.refs)) != 4 {
		t.Fatalf("list = %v, want four entries", presenceList(t, rig.refs))
	}
	rig.refs.remove(holders, presenceBackground)
	rig.scheduler.Wait()
	got := frames.Items()
	if len(got) != 1 {
		t.Fatalf("frames = %d, want one", len(got))
	}
	removal := []any{"p", []any{}, 0, 1, []any{}}
	nexttest.RequireJSONEqual(t, presenceOps(got[0].Ops), []any{removal, removal, removal, removal})
	nexttest.RequireJSONEqual(t, got[0].Value, []any{})
}

// Mirrors "host.references › puts a departure discovered while a commit sends into the next batch: one state message per
// commit (race 9)": a removal made while a commit's flushes send lands in the next commit, so its frame is separate.
func TestReferencesPutsADepartureDiscoveredWhileACommitSendsIntoTheNextBatch(t *testing.T) {
	rig := newPresenceRig()
	owner := delta.JsonObjectOf("kind", "owner")
	object := presenceFakeObject{params: presenceSession("S1"), kind: contracts.KindRemote}
	first, second := presenceHolderOf(owner, "client"), presenceHolderOf(owner, "client")
	rig.refs.add(object, first, presenceBackground)
	frames := rig.batches(t)
	// One commit adds the second reference; while it sends, the first one's link closes.
	err := rig.batch.Commit(func() error {
		rig.refs.add(object, second, presenceBackground)
		rig.batch.Flushing(func(context.Context) error {
			rig.refs.remove([]presenceHolder{first}, presenceBackground)
			return nil
		})
		return nil
	}, presenceBackground)
	if err != nil {
		t.Fatal(err)
	}
	rig.scheduler.Wait()
	got := frames.Items()
	if len(got) != 2 {
		t.Fatalf("frames = %d, want the commit's and the departure's", len(got))
	}
	added := presenceEntry(presenceSession("S1"), owner, "client")
	nexttest.RequireJSONEqual(t, presenceOps(got[0].Ops), []any{[]any{"p", []any{}, 1, 0, []any{added}}})
	nexttest.RequireJSONEqual(t, presenceOps(got[1].Ops), []any{[]any{"p", []any{}, 0, 1, []any{}}})
}

// Mirrors the doc comment of References: while nobody watches, a change drops the list and it is built on the next read,
// in insertion order, a holder recorded again keeping its place.
func TestReferencesBuildsTheListOnItsNextReadInInsertionOrderWhileUnwatched(t *testing.T) {
	rig := newPresenceRig()
	owner := delta.JsonObjectOf("kind", "owner")
	holders := []*presenceFakeHolder{presenceHolderOf(owner, "a"), presenceHolderOf(owner, "b"), presenceHolderOf(owner, "c"), presenceHolderOf(owner, "d")}
	object := presenceFakeObject{params: presenceSession("S1"), kind: contracts.KindLocal}
	for _, each := range holders[:3] {
		rig.refs.add(object, each, presenceBackground)
	}
	nexttest.RequireJSONEqual(t, presenceList(t, rig.refs), []any{presenceEntry(presenceSession("S1"), nil, "a"), presenceEntry(presenceSession("S1"), nil, "b"), presenceEntry(presenceSession("S1"), nil, "c")})
	rig.refs.remove([]presenceHolder{holders[1]}, presenceBackground)
	rig.refs.add(object, holders[3], presenceBackground)
	// A holder recorded again keeps its place, with its new entry.
	rig.refs.record(object, holders[0])
	nexttest.RequireJSONEqual(t, presenceList(t, rig.refs), []any{presenceEntry(presenceSession("S1"), nil, "a"), presenceEntry(presenceSession("S1"), nil, "c"), presenceEntry(presenceSession("S1"), nil, "d")})
}

// Mirrors the doc comment of References: a holder added and removed in one batch is never published (its entry leaves
// `#added` instead of becoming a removal).
func TestReferencesAnEntryAddedAndRemovedInOneBatchPublishesNothing(t *testing.T) {
	rig := newPresenceRig()
	owner := delta.JsonObjectOf("kind", "owner")
	object := presenceFakeObject{params: presenceSession("S1"), kind: contracts.KindRemote}
	kept, gone := presenceHolderOf(owner, "kept"), presenceHolderOf(owner, "gone")
	rig.refs.add(object, kept, presenceBackground)
	frames := rig.batches(t)
	err := rig.batch.Commit(func() error {
		rig.refs.add(object, gone, presenceBackground)
		rig.refs.remove([]presenceHolder{gone}, presenceBackground)
		return nil
	}, presenceBackground)
	if err != nil {
		t.Fatal(err)
	}
	rig.scheduler.Wait()
	if got := frames.Items(); len(got) != 0 {
		t.Fatalf("frames = %d, want none", len(got))
	}
	nexttest.RequireJSONEqual(t, presenceList(t, rig.refs), []any{presenceEntry(presenceSession("S1"), owner, "kept")})
}

// Mirrors "host.references › has no entries for lingering objects": a released reference's entry is gone at once.
func TestReferencesHasNoEntryForAHolderThatEnded(t *testing.T) {
	rig := newPresenceRig()
	object := presenceFakeObject{params: presenceSession("S1"), kind: contracts.KindLocal}
	one := presenceHolderOf(nil, "client")
	rig.refs.add(object, one, presenceBackground)
	if len(presenceList(t, rig.refs)) != 1 {
		t.Fatal("no entry for a reference")
	}
	rig.refs.remove([]presenceHolder{one}, presenceBackground)
	nexttest.RequireJSONEqual(t, presenceList(t, rig.refs), []any{})
	// A holder that has no entry is skipped.
	rig.refs.remove([]presenceHolder{one}, presenceBackground)
	nexttest.RequireJSONEqual(t, presenceList(t, rig.refs), []any{})
}

// Mirrors "host.references › keeps no released reference (GC)": the entries hold no holder once it ended, watched or not.
func TestReferencesKeepsNoReleasedReferenceGC(t *testing.T) {
	for _, watched := range []bool{false, true} {
		rig := newPresenceRig()
		if watched {
			rig.frames(t)
		}
		object := presenceFakeObject{params: presenceSession("S1"), kind: contracts.KindRemote}
		ref := func() weak.Pointer[presenceFakeHolder] {
			ended := presenceHolderOf(delta.JsonObjectOf("kind", "owner"), "client")
			rig.refs.add(object, ended, presenceBackground)
			rig.refs.remove([]presenceHolder{ended}, presenceBackground)
			return weak.Make(ended)
		}()
		rig.scheduler.Wait()
		nexttest.ExpectCollected(t, ref)
		nexttest.RequireJSONEqual(t, presenceList(t, rig.refs), []any{})
	}
}
