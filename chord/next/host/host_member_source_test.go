package host

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Tests of packages/chord/test/next/host/member-source.test.ts. Every case of the upstream file serves objects through a
// whole host (`loaded`, `serve`, `ChordObject` attempts, `failLive`, `defineState` documents, `createHost` with links), so
// none is ported yet: they are `missing` for the host core. The tests below are own tests of memberSource over documents
// and derives of the state package; each names the upstream case whose behavior it mirrors.

// memberCountOf is the `n` of a test value {n}.
func memberCountOf(value delta.JsonValue) float64 {
	return value.(*delta.JsonObject).Value("n").(float64)
}

// derive makes a derive of sources computing compute, attached to them as an attempt does.
func (h *memberHarness) derive(t *testing.T, sources []any, compute func(values []any) (delta.JsonValue, error)) *state.Derive {
	t.Helper()
	derive := state.NewDerive(sources, compute, h.batch)
	err := derive.Attach(func(source any, _ int) (state.DeriveInput, error) { return source.(state.DeriveInput), nil }, func(error) {})
	if err != nil {
		t.Fatalf("attach derive: %v", err)
	}
	return derive
}

// counting is a derive of doc's n, its computes counted; it fails with "count broke" when n is brokenAt.
func (h *memberHarness) counting(t *testing.T, doc *memberDoc, brokenAt float64) (*state.Derive, *int) {
	t.Helper()
	computes := 0
	derive := h.derive(t, []any{doc}, func(values []any) (delta.JsonValue, error) {
		computes++
		n := memberCountOf(values[0])
		if n == brokenAt {
			return nil, errors.New("count broke")
		}
		return n, nil
	})
	return derive, &computes
}

// times is a derive of source's number times factor.
func (h *memberHarness) times(t *testing.T, source any, factor float64) *state.Derive {
	t.Helper()
	return h.derive(t, []any{source}, func(values []any) (delta.JsonValue, error) { return values[0].(float64) * factor, nil })
}

func (h *memberHarness) setDoc(t *testing.T, doc *memberDoc, n float64) {
	t.Helper()
	h.loop.Run(func() { doc.set(t, memberCount(n)) })
}

func (h *memberHarness) member(source state.DeriveSource) *memberSource {
	return newMemberSource(source, h.batch, h.scheduler)
}

// value is the value of source now, or its error.
func (h *memberHarness) value(source state.DeriveSource) (value delta.JsonValue, err error) {
	h.loop.Run(func() { value, err = source.Value() })
	return value, err
}

// recorded is a subscriber's events, as member-source.test.ts "subscribers, directly" records them.
type recorded struct{ events []any }

func (r *recorded) Advance(call chord.WatchCall[delta.JsonValue], _ context.Context) {
	if call.Kind == chord.WatchCallValue {
		r.events = append(r.events, []any{call.Value, call.Ops})
		return
	}
	r.events = append(r.events, call.Error.Error())
}

func (r *recorded) End(end chord.WatchEnd) { r.events = append(r.events, string(end.Kind)) }

func (r *recorded) EndAfterDelivery() { r.events = append(r.events, "ended") }

func rootReplacement(value float64) []any {
	return []any{value, []delta.Op{{"r", value}}}
}

// Own test, mirrors member-source.test.ts › following the live attempt › passes the member's frames on as they are, and
// keeps a derive member lazy while nothing watches it.
func TestMemberSourceFollowingTheLiveAttemptPassesTheMembersFramesOnAsTheyAreAndKeepsADeriveMemberLazyWhileNothingWatchesIt(t *testing.T) {
	h := newMemberHarness(t)
	doc := newMemberDoc(h.batch, memberCount(0))
	count, computes := h.counting(t, doc, -1)
	m := h.member(count)
	if v, _ := h.value(m); v != float64(0) {
		t.Fatalf("value = %v, want 0", v)
	}
	before := *computes
	if before == 0 {
		t.Fatalf("the first read computed nothing")
	}
	h.setDoc(t, doc, 2)
	h.setDoc(t, doc, 3)
	// Unwatched: marked only, computed by the next read.
	expectEqual(t, "computes while unwatched", *computes, before)
	if v, _ := h.value(m); v != float64(3) {
		t.Fatalf("value = %v, want 3", v)
	}
	watch := h.watch(t, m)
	counted := nexttest.ChangesOf(watch)
	h.setDoc(t, doc, 4)
	h.delivered()
	expectCalls(t, counted.Items(), []chord.WatchCall[delta.JsonValue]{valueCall(float64(4), false, delta.Op{"r", float64(4)})})
	if _, err := watch.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	h.delivered()
	if m.Watched() {
		t.Fatalf("a stopped watch still follows the member source")
	}
	stopped := *computes
	h.setDoc(t, doc, 5)
	expectEqual(t, "computes after the watch stopped", *computes, stopped)
}

// Own test, mirrors member-source.test.ts › following the live attempt: the member's frames reach the watch as they are,
// with no extra hop (`expect(lists[0]).toBe(own[0])`): the watch of the member source and the watch of the bound document
// get the same ops.
func TestMemberSourcePassesTheBoundDocumentsFramesOnWithoutACopy(t *testing.T) {
	h := newMemberHarness(t)
	doc := newMemberDoc(h.batch, memberCount(0))
	m := h.member(doc)
	through := nexttest.ChangesOf(h.watch(t, m))
	own := nexttest.ChangesOf(h.watch(t, doc))
	h.setDoc(t, doc, 1)
	h.delivered()
	if len(through.Items()) != 1 || len(own.Items()) != 1 {
		t.Fatalf("frames = %d and %d, want 1 and 1", len(through.Items()), len(own.Items()))
	}
	if reflect.ValueOf(through.Items()[0].Ops).Pointer() != reflect.ValueOf(own.Items()[0].Ops).Pointer() {
		t.Fatalf("the member source's frame is a copy of the document's")
	}
}

// Own test, mirrors member-source.test.ts › a failed object's members › keep their last values and get no frames; a new
// watch opens at the last value (slice 6a §1.4): frozen, then re-bound by the retried attempt.
func TestMemberSourceAFailedObjectsMembersKeepTheirLastValuesAndGetNoFramesANewWatchOpensAtTheLastValue(t *testing.T) {
	h := newMemberHarness(t)
	doc, retried := newMemberDoc(h.batch, memberCount(0)), newMemberDoc(h.batch, memberCount(0))
	count, _ := h.counting(t, doc, -1)
	m := h.member(count)
	h.setDoc(t, doc, 1)
	counted := nexttest.ChangesOf(h.watch(t, m))
	h.loop.Run(m.freeze)
	// The failed attempt's document goes on changing only through the attempt, which is sealed: nothing reaches the watch.
	h.setDoc(t, doc, 7)
	h.delivered()
	if v, _ := h.value(m); v != float64(1) {
		t.Fatalf("frozen value = %v, want 1", v)
	}
	late := h.watch(t, m)
	expectEqual(t, "late watch value", late.Value(), delta.JsonValue(float64(1)))
	lately := nexttest.ChangesOf(late)
	expectEqual(t, "frames while frozen", counted.Items(), []chord.WatchCall[delta.JsonValue]{})
	// A retry: the new attempt's member shows the document the retried attempt loaded.
	next, _ := h.counting(t, retried, -1)
	h.setDoc(t, retried, 2)
	h.within(t, func() { m.bind(next) })
	h.delivered()
	expectEqual(t, "frames of the old watch", nexttest.ValuesOf(counted.Items()), []any{float64(2)})
	expectEqual(t, "frames of the late watch", nexttest.ValuesOf(lately.Items()), []any{float64(2)})
}

// Own test, mirrors member-source.test.ts › re-binding when a new attempt goes live › recovers a consumer's derive that
// failed on the provider's earlier error, and the derives downstream.
func TestMemberSourceRebindingRecoversAConsumersDeriveThatFailedOnTheProvidersEarlierErrorAndTheDerivesDownstream(t *testing.T) {
	h := newMemberHarness(t)
	first, second := newMemberDoc(h.batch, memberCount(0)), newMemberDoc(h.batch, memberCount(1))
	count, _ := h.counting(t, first, 1)
	m := h.member(count)
	tens := h.times(t, m, 10)
	hundreds := h.times(t, tens, 10)
	h.setDoc(t, first, 1)
	named := []struct {
		name   string
		derive *state.Derive
		want   float64
	}{{"tens", tens, 10}, {"hundreds", hundreds, 100}}
	for _, each := range named {
		if _, err := h.value(each.derive); err == nil || err.Error() != "count broke" {
			t.Fatalf("%s = %v, want count broke", each.name, err)
		}
	}
	h.loop.Run(m.freeze)
	next, _ := h.counting(t, second, -1)
	h.within(t, func() { m.bind(next) })
	for _, each := range named {
		if v, err := h.value(each.derive); err != nil || v != each.want {
			t.Fatalf("%s = %v, %v, want %v", each.name, v, err, each.want)
		}
	}
	frames := nexttest.ChangesOf(h.watch(t, hundreds))
	h.setDoc(t, second, 2)
	h.delivered()
	expectEqual(t, "frames of hundreds", nexttest.ValuesOf(frames.Items()), []any{float64(200)})
}

// Own test, mirrors member-source.test.ts › re-binding when a new attempt goes live › re-binds a consumer's derive over a
// retried provider's derive member, and its watches get frames again.
func TestMemberSourceRebindingRebindsAConsumersDeriveOverARetriedProvidersDeriveMemberAndItsWatchesGetFramesAgain(t *testing.T) {
	h := newMemberHarness(t)
	first, second := newMemberDoc(h.batch, memberCount(0)), newMemberDoc(h.batch, memberCount(0))
	count, _ := h.counting(t, first, -1)
	m := h.member(count)
	tens := h.times(t, m, 10)
	tensFrames := nexttest.ChangesOf(h.watch(t, tens))
	counted := nexttest.ChangesOf(h.watch(t, m))
	h.setDoc(t, first, 1)
	h.delivered()
	h.loop.Run(m.freeze)
	h.delivered()
	for _, source := range []state.DeriveSource{m, tens} {
		if _, err := h.value(source); err != nil {
			t.Fatalf("frozen read: %v", err)
		}
	}
	next, _ := h.counting(t, second, -1)
	h.setDoc(t, second, 2)
	h.within(t, func() { m.bind(next) })
	h.delivered()
	if v, _ := h.value(m); v != float64(2) {
		t.Fatalf("member = %v, want 2", v)
	}
	if v, _ := h.value(tens); v != float64(20) {
		t.Fatalf("tens = %v, want 20", v)
	}
	expectEqual(t, "frames of tens", nexttest.ValuesOf(tensFrames.Items()), []any{float64(10), float64(20)})
	expectEqual(t, "frames of the member", nexttest.ValuesOf(counted.Items()), []any{float64(1), float64(2)})
}

// Own test, mirrors member-source.test.ts › re-binding when a new attempt goes live › sends one frame from the last value
// when the new attempt's member differs.
func TestMemberSourceRebindingSendsOneFrameFromTheLastValueWhenTheNewAttemptsMemberDiffers(t *testing.T) {
	h := newMemberHarness(t)
	label := func(text string) *state.Derive {
		return h.derive(t, nil, func([]any) (delta.JsonValue, error) { return text, nil })
	}
	m := h.member(label("old"))
	frames := nexttest.ChangesOf(h.watch(t, m))
	h.loop.Run(m.freeze)
	h.within(t, func() { m.bind(label("new")) })
	h.delivered()
	if v, _ := h.value(m); v != "new" {
		t.Fatalf("label = %v, want new", v)
	}
	expectCalls(t, frames.Items(), []chord.WatchCall[delta.JsonValue]{valueCall("new", false, delta.Op{"r", "new"})})
}

// Own test, mirrors member-source.test.ts › subscribers, directly › are fed the member's calls: a failure, then its
// recovery at the re-bind as a root replacement.
func TestMemberSourceSubscribersDirectlyAreFedTheMembersCallsAFailureThenItsRecoveryAtTheRebindAsARootReplacement(t *testing.T) {
	h := newMemberHarness(t)
	first, second := newMemberDoc(h.batch, memberCount(0)), newMemberDoc(h.batch, memberCount(2))
	count, _ := h.counting(t, first, 2)
	m := h.member(count)
	subscriber := &recorded{}
	if v, _ := h.value(m); v != float64(0) {
		t.Fatalf("value = %v, want 0", v)
	}
	h.loop.Run(func() { m.Subscribe(subscriber) })
	h.setDoc(t, first, 1)
	h.setDoc(t, first, 2)
	h.loop.Run(m.freeze)
	next, _ := h.counting(t, second, -1)
	h.within(t, func() { m.bind(next) })
	h.setDoc(t, second, 3)
	if v, _ := h.value(m); v != float64(3) {
		t.Fatalf("value = %v, want 3", v)
	}
	expectEqual(t, "events", subscriber.events, []any{rootReplacement(1), "count broke", rootReplacement(2), rootReplacement(3)})
}

// Own test, mirrors member-source.test.ts › a member the new attempt's variant lacks › fails: its watch gets one failure
// call and stays open, reads fail invalid/member; a later attempt brings it back.
func TestMemberSourceAMemberTheNewAttemptsVariantLacksFailsItsWatchGetsOneFailureCallAndStaysOpenAndALaterAttemptBringsItBack(t *testing.T) {
	h := newMemberHarness(t)
	m := h.member(state.NewConstant(float64(1)))
	calls := nexttest.ChangesOf(h.watch(t, m))
	lacking := data.Invalid(data.ReasonMember, `app.two (variant view) has no state member "n"`)
	h.loop.Run(m.freeze)
	h.within(t, func() { m.vanish(lacking, memberBackground) })
	h.delivered()
	got := calls.Items()
	if len(got) != 1 || got[0].Kind != chord.WatchCallFailed {
		t.Fatalf("calls = %#v, want one failure call", got)
	}
	if err, ok := data.IsChordErrorCode(got[0].Error, data.CodeInvalid); !ok || err.Reason != data.ReasonMember {
		t.Fatalf("failure = %v, want invalid/member", got[0].Error)
	}
	if _, err := h.value(m); err == nil || err.Error() != `app.two (variant view) has no state member "n"` {
		t.Fatalf("read = %v, want the missing member", err)
	}
	// The same as over a link: the member comes back with the next attempt that has it.
	h.loop.Run(m.freeze)
	h.within(t, func() { m.bind(state.NewConstant(float64(3))) })
	h.delivered()
	if v, _ := h.value(m); v != float64(3) {
		t.Fatalf("value = %v, want 3", v)
	}
	last := calls.Items()[len(calls.Items())-1]
	expectCalls(t, []chord.WatchCall[delta.JsonValue]{last}, []chord.WatchCall[delta.JsonValue]{valueCall(float64(3), false, delta.Op{"r", float64(3)})})
}

// Own test, mirrors member-source.test.ts › an ended object's members › mark the derives over them: a consumer's derive
// keeps no cached value, its read fails released; and the watches of the member end `ended`.
func TestMemberSourceAnEndedObjectsMembersMarkTheDerivesOverThemAndEndTheirWatches(t *testing.T) {
	h := newMemberHarness(t)
	m := h.member(state.NewConstant(float64(4)))
	twice := h.times(t, m, 2)
	if v, _ := h.value(twice); v != float64(8) {
		t.Fatalf("twice = %v, want 8", v)
	}
	watch := h.watch(t, m)
	released := data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonEnded, Message: "app.paged(1): ended"}, nil)
	h.within(t, func() { m.objectEnded(released) })
	_, err := h.value(twice)
	if _, ok := data.IsChordErrorCode(err, data.CodeReleased); !ok {
		t.Fatalf("read = %v, want released", err)
	}
	<-watch.Closed()
	expectEqual(t, "end", watch.End(), chord.WatchEnd{Kind: chord.WatchEnded})
	if m.Watched() {
		t.Fatalf("an ended member source still has subscribers")
	}
	// An ended member source keeps no re-bind.
	h.within(t, func() { m.bind(state.NewConstant(float64(9))) })
	if _, err := h.value(m); !errors.Is(err, released) {
		t.Fatalf("read after a bind = %v, want the end", err)
	}
}

// Own test: a watch opened on a failed member starts at its last good value and its first call is the failure; one with
// no last value cannot open (member-source.ts `openWatch`, as `Source.openWatch` starts one).
func TestMemberSourceAWatchOpenedOnAFailedMemberStartsAtItsLastGoodValueAndGetsTheFailureFirst(t *testing.T) {
	h := newMemberHarness(t)
	doc := newMemberDoc(h.batch, memberCount(1))
	count, _ := h.counting(t, doc, 9)
	m := h.member(count)
	if v, _ := h.value(m); v != float64(1) {
		t.Fatalf("value = %v, want 1", v)
	}
	h.setDoc(t, doc, 9)
	h.loop.Run(m.freeze)
	watch := h.watch(t, m)
	expectEqual(t, "watch value", watch.Value(), delta.JsonValue(float64(1)))
	calls := nexttest.ChangesOf(watch)
	h.delivered()
	got := calls.Items()
	if len(got) != 1 || got[0].Kind != chord.WatchCallFailed || got[0].Error.Error() != "count broke" {
		t.Fatalf("calls = %#v, want the failure first", got)
	}
	// Never had a value: nothing to start at.
	never, _ := h.counting(t, newMemberDoc(h.batch, memberCount(9)), 9)
	broken := h.member(never)
	h.loop.Run(broken.freeze)
	var err error
	h.loop.Run(func() { _, err = broken.Watch(memberBackground) })
	if err == nil || err.Error() != "count broke" {
		t.Fatalf("watch = %v, want count broke", err)
	}
}

// Own test, mirrors member-source.test.ts › member sources and memory › a failed attempt is collected after a retry while
// the object, its member source and a retained watch live.
func TestMemberSourceAFailedAttemptIsCollectedAfterARebindWhileTheMemberSourceAndARetainedWatchLive(t *testing.T) {
	h := newMemberHarness(t)
	second := newMemberDoc(h.batch, memberCount(0))
	next, _ := h.counting(t, second, -1)
	var m *memberSource
	var oldDoc weak.Pointer[memberDoc]
	var oldMember weak.Pointer[state.Derive]
	func() {
		first := newMemberDoc(h.batch, memberCount(0))
		count, _ := h.counting(t, first, -1)
		m = h.member(count)
		oldDoc, oldMember = weak.Make(first), weak.Make(count)
	}()
	frames := nexttest.ChangesOf(h.watch(t, m))
	h.loop.Run(m.freeze)
	h.within(t, func() { m.bind(next) })
	h.setDoc(t, second, 1)
	h.delivered()
	expectEqual(t, "frames", nexttest.ValuesOf(frames.Items()), []any{float64(1)})
	expectCollectedEventually(t, oldDoc)
	expectCollectedEventually(t, oldMember)
}

// Own test, mirrors member-source.test.ts › member sources and memory › an ended object's member sources are collected
// with it.
func TestMemberSourceAnEndedObjectsMemberSourcesAreCollectedWithIt(t *testing.T) {
	h := newMemberHarness(t)
	ref := func() weak.Pointer[memberSource] {
		doc := newMemberDoc(h.batch, memberCount(0))
		m := h.member(doc)
		if _, err := h.value(m); err != nil {
			t.Fatalf("value: %v", err)
		}
		h.within(t, func() {
			m.objectEnded(data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonEnded, Message: "ended"}, nil))
		})
		return weak.Make(m)
	}()
	expectCollectedEventually(t, ref)
}

// Own test of memberSource.OpenWatch (member-source.ts `openWatch`: `watch.observeAbort`): aborting the context a watch was
// opened with ends it `aborted`, and it stops following the member source.
func TestMemberSourceAWatchEndsAbortedWithItsContextAndStopsFollowing(t *testing.T) {
	h := newMemberHarness(t)
	m := h.member(newMemberDoc(h.batch, memberCount(0)))
	abort, cancel := chordctx.WithCancel(memberBackground)
	var watch *chord.Watch[delta.JsonValue]
	var err error
	h.loop.Run(func() { watch, err = m.OpenWatch(abort) })
	if err != nil {
		t.Fatalf("open watch: %v", err)
	}
	reason := errors.New("viewer left")
	cancel(reason)
	<-watch.Closed()
	expectEqual(t, "end kind", watch.End().Kind, chord.WatchAborted)
	h.delivered()
	if m.Watched() {
		t.Fatalf("an aborted watch still follows the member source")
	}
}

// --- member-source.test.ts on whole hosts -----------------------------------------------------------------------------------
//
// The upstream cases serve objects through a host (`loaded`, `serve`, `failLive`, `defineState` documents, `linked`), as
// the tests below do; they carry the Go name of their title behind Host.

var (
	msItems = contracts.Obj(contracts.Prop("items", contracts.Arr(contracts.Str())))
	msList  = func() *documents.StateDefinition {
		definition, err := documents.DefineState("list", documents.StateOptions{
			Schema: msItems, Version: 1, Initial: func() *delta.JsonObject { return jsonObject("items", []any{}) },
		})
		if err != nil {
			panic(err)
		}
		return definition
	}()
	msStore = mustContract(contracts.Local("app.store", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1,
		Members: []contracts.LocalEntry{
			contracts.Mem("list", msItems), contracts.Mem("count", contracts.Int()),
			contracts.Mem("add", contracts.NewMethod(contracts.Obj(contracts.Prop("text", contracts.Str())))),
		},
	}))
	msViewer = mustContract(contracts.Local("app.count-viewer", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("tens", contracts.Int())},
	}))
)

// msItemCount is the number of items of a list value.
func msItemCount(value any) float64 {
	return float64(len(value.(*delta.JsonObject).Value("items").([]any)))
}

// msPush is a method that pushes its text argument onto `list`.
func msPush(list *documents.Singleton, field string) contracts.MethodFunction {
	return func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
		text := args
		if object, ok := args.(*delta.JsonObject); ok {
			text = object.Value(field)
		}
		_, err := list.Change(func(draft *delta.Object) (any, error) {
			_, err := draft.Array("items").Push(text)
			return nil, err
		}, ctx)
		return nil, err
	}
}

// msServed is Pi's `served`: Store (a list, its count, add) and Viewer (a derive over the store's count).
type msServed struct {
	Host     Host
	Errors   *hostErrors
	Store    *Reference
	Attempts *attemptList
	mu       sync.Mutex
	lists    []*documents.Singleton
}

func (s *msServed) list(index int) *documents.Singleton {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lists[index]
}

func (s *msServed) fail() {
	failLive(s.Attempts.at(-1), data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
}

// msStoreFactory is the Store's factory: its attempts and lists recorded in s.
func (s *msServed) factory(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
	s.Attempts.add(self)
	list := self.State(msList).(*documents.Singleton)
	s.mu.Lock()
	s.lists = append(s.lists, list)
	s.mu.Unlock()
	return contracts.Implementation{Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{
			"list": Expose(list),
			"count": self.Derive([]any{list}, func(values []any) (delta.JsonValue, error) {
				return msItemCount(values[0]), nil
			}),
		},
		Methods: map[string]contracts.MethodFunction{"add": msPush(list, "text")},
	}}, nil
}

func msServe(t *testing.T, options loadedOptions) *msServed {
	t.Helper()
	s := &msServed{Attempts: &attemptList{}}
	options.Serve = nil
	s.Host, s.Errors = loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(msStore, s.factory, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(msViewer, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			tens := self.Derive([]any{self.Get(msStore, jsonObject()).Member("count")}, func(values []any) (delta.JsonValue, error) {
				return values[0].(float64) * 10, nil
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"tens": tens},
			}}, nil
		}, ServeOptions{})
	}, options)
	var err error
	s.Store, err = s.Host.Get(msStore, jsonObject(), background, GetOptions{})
	must(t, err)
	return s
}

func msText(text string) *delta.JsonObject { return jsonObject("text", text) }

func msAdd(t testing.TB, ref *Reference, text string) {
	t.Helper()
	_, err := ref.Call("add", msText(text), background)
	must(t, err)
}

// msCall is the call as upstream's `{ kind, value, ops, retired }`.
func msCall(call chord.WatchCall[delta.JsonValue]) []any {
	ops := make([]any, len(call.Ops))
	for i, op := range call.Ops {
		ops[i] = []any(op)
	}
	return []any{string(call.Kind), call.Value, ops, call.Retired}
}

// msValue is `{ kind: "value", value, ops: [["r", value]], retired: false }`.
func msValue(value any) []any {
	return []any{string(chord.WatchCallValue), value, []any{[]any{"r", value}}, false}
}

func msCalls(calls []chord.WatchCall[delta.JsonValue]) []any {
	out := make([]any, len(calls))
	for i, call := range calls {
		out[i] = msCall(call)
	}
	return out
}

// msRetry is `ref.retry(); (await host.get(Type, params)).release()`: the next attempt, live.
func msRetry(t testing.TB, host Host, ref *Reference, typ *contracts.Contract, params *delta.JsonObject) {
	t.Helper()
	ref.Retry()
	again, err := host.Get(typ, params, background, GetOptions{})
	must(t, err)
	again.Release()
}

// member-source.test.ts › following the live attempt › passes the member's frames on as they are, and keeps a derive member
// lazy while nothing watches it.
func TestHostMemberSourceFollowingTheLiveAttemptPassesTheMembersFramesOnAsTheyAreAndKeepsADeriveMemberLazyWhileNothingWatchesIt(t *testing.T) {
	meter := nexttest.Metered(t)
	s := msServe(t, loadedOptions{Telemetry: meter.Telemetry})
	nexttest.RequireJSONEqual(t, kmMember(t, s.Store, "count"), float64(0))
	computes := func() float64 { return meter.Counts()["chord.derive.computes"] }
	before := computes()
	if before <= 0 {
		t.Fatalf("computes before = %v, want some", before)
	}
	msAdd(t, s.Store, "a")
	msAdd(t, s.Store, "b")
	// Unwatched: marked only, computed by the next read.
	if got := computes(); got != before {
		t.Fatalf("computes = %v, want %v while unwatched", got, before)
	}
	nexttest.RequireJSONEqual(t, kmMember(t, s.Store, "count"), float64(2))
	watch, err := s.Store.Member("count").Watch(background)
	must(t, err)
	counted := nexttest.ChangesOf(watch)
	lists := kmWatch(t, s.Store.Member("list"))
	ownWatch, err := s.list(0).Watch(background)
	must(t, err)
	own := nexttest.ChangesOf(ownWatch)
	msAdd(t, s.Store, "c")
	deliveredOn(s.Host)
	nexttest.RequireJSONEqual(t, msCalls(counted.Items()), []any{[]any{"value", float64(3), []any{[]any{"r", float64(3)}}, false}})
	// The slot's own change, not a copy.
	if lists.Len() != 1 || own.Len() == 0 || lists.Items()[0].Value != own.Items()[0].Value {
		t.Fatalf("the member's frame is not the slot's own change: %d frames", lists.Len())
	}
	_, err = watch.Stop()
	must(t, err)
	deliveredOn(s.Host)
	stopped := computes()
	msAdd(t, s.Store, "d")
	if got := computes(); got != stopped {
		t.Fatalf("computes = %v, want %v after the watch stopped", got, stopped)
	}
	must(t, s.Host.Close(background))
}

// member-source.test.ts › a failed object's members › keep their last values and get no frames; a new watch opens at the
// last value (slice 6a §1.4).
func TestHostMemberSourceAFailedObjectsMembersKeepTheirLastValuesAndGetNoFramesANewWatchOpensAtTheLastValue(t *testing.T) {
	s := msServe(t, loadedOptions{})
	msAdd(t, s.Store, "a")
	counted := kmWatch(t, s.Store.Member("count"))
	s.fail()
	deliveredOn(s.Host)
	if kind := s.Store.State().Value().Kind; kind != RefStateFailed {
		t.Fatalf("store state = %s, want failed", kind)
	}
	late, err := s.Store.Member("count").Watch(background)
	must(t, err)
	nexttest.RequireJSONEqual(t, late.Value(), float64(1))
	lately := nexttest.ChangesOf(late)
	listed := kmWatch(t, s.Store.Member("list"))
	// The failed attempt's handle is sealed; the slot changes only through a new attempt.
	value, err := s.list(0).Value()
	must(t, err)
	nexttest.RequireJSONEqual(t, value, jsonObject("items", []any{"a"}))
	s.Store.Retry()
	again, err := s.Host.Get(msStore, jsonObject(), background, GetOptions{})
	must(t, err)
	msAdd(t, again, "b")
	deliveredOn(s.Host)
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(counted.Items()), []any{float64(2)})
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(listed.Items()), []any{jsonObject("items", []any{"a", "b"})})
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(lately.Items()), []any{float64(2)})
	must(t, s.Host.Close(background))
}

// member-source.test.ts › a failed object's members › re-exposed from another object freeze with the re-exposing object;
// the provider's source goes on.
func TestHostMemberSourceReExposedFromAnotherObjectFreezeWithTheReExposingObjectTheProvidersSourceGoesOn(t *testing.T) {
	note := contracts.Obj(contracts.Prop("title", contracts.Str()))
	memo, err := documents.DefineState("memo", documents.StateOptions{
		Schema: note, Version: 1, Keyed: true, Initial: func() *delta.JsonObject { return jsonObject("title", "") },
	})
	must(t, err)
	own := kmDefine("own", documents.StateOptions{})
	provider := mustContract(contracts.Local("app.provider", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1,
		Members: []contracts.LocalEntry{
			contracts.Mem("n", contracts.Int()), contracts.Mem("notes", contracts.Keyed(note)), contracts.Mem("set", contracts.NewMethod(contracts.Int())),
		},
	}))
	exposerType := mustContract(contracts.Local("app.exposer", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int()), contracts.Mem("note", note)},
	}))
	registry := newKmRegistry("")
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(provider, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			memos := self.State(memo).(*documents.Keyed)
			at, err := memos.At("a")
			if err != nil {
				return contracts.Implementation{}, err
			}
			n := self.Derive([]any{at}, func(values []any) (delta.JsonValue, error) {
				return float64(len(values[0].(*delta.JsonObject).Value("title").(string))), nil
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": n},
				Keyed: kmKeyed(memos),
				Methods: map[string]contracts.MethodFunction{"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					_, err := memos.Change("a", func(draft *delta.Object) (any, error) {
						return nil, draft.Set("title", strings.Repeat("x", int(args.(float64))))
					}, ctx)
					return nil, err
				}},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(exposerType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			registry.addExposer(self)
			self.State(own)
			held := self.Get(provider, jsonObject())
			noteAt, err := held.Keyed("notes").At("a")
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": Expose(held.Member("n")), "note": Expose(noteAt)},
			}}, err
		}, ServeOptions{})
	}, loadedOptions{})
	providerRef, err := host.Get(provider, jsonObject(), background, GetOptions{})
	must(t, err)
	exposed, err := host.Get(exposerType, jsonObject(), background, GetOptions{})
	must(t, err)
	_, err = providerRef.Call("set", float64(1), background)
	must(t, err)
	exposedN := kmWatch(t, exposed.Member("n"))
	exposedNote := kmWatch(t, exposed.Member("note"))
	providerN := kmWatch(t, providerRef.Member("n"))
	failLive(registry.exposer(0), data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
	deliveredOn(host)
	_, err = providerRef.Call("set", float64(7), background)
	must(t, err)
	deliveredOn(host)
	if kind := exposed.State().Value().Kind; kind != RefStateFailed {
		t.Fatalf("exposer state = %s, want failed", kind)
	}
	nexttest.RequireJSONEqual(t, []any{kmMember(t, exposed, "n"), kmMember(t, exposed, "note")}, []any{float64(1), jsonObject("title", "x")})
	nexttest.RequireJSONEqual(t, []any{msCalls(exposedN.Items()), msCalls(exposedNote.Items())}, []any{[]any{}, []any{}})
	nexttest.RequireJSONEqual(t, kmMember(t, providerRef, "n"), float64(7))
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(providerN.Items()), []any{float64(7)})
	must(t, host.Close(background))
}

// member-source.test.ts › the window of a live failure › members following another object's sources freeze at once, while
// an admitted commit of the object runs.
func TestHostMemberSourceMembersFollowingAnotherObjectsSourcesFreezeAtOnceWhileAnAdmittedCommitOfTheObjectRuns(t *testing.T) {
	counter := kmDefine("counter", documents.StateOptions{})
	persisted := kmDefine("own", documents.StateOptions{
		Persist: true, CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	})
	provider := mustContract(contracts.Local("app.provider", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int()), contracts.Mem("set", contracts.NewMethod(contracts.Int()))},
	}))
	exposerType := mustContract(contracts.Local("app.exposer", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int()), contracts.Mem("own", kmSchema)},
	}))
	st, _, holdNextCommit := faultyStore(t)
	registry := newKmRegistry("")
	var own atomic.Pointer[documents.Singleton]
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(provider, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := self.State(counter).(*documents.Singleton)
			n := self.Derive([]any{doc}, func(values []any) (delta.JsonValue, error) { return values[0].(*delta.JsonObject).Value("n"), nil })
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": n},
				Methods: map[string]contracts.MethodFunction{"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					_, err := doc.Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("n", args) }, ctx)
					return nil, err
				}},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(exposerType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			registry.addExposer(self)
			doc := self.State(persisted).(*documents.Singleton)
			own.Store(doc)
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"n": Expose(self.Get(provider, jsonObject()).Member("n")), "own": Expose(doc),
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{Store: st})
	providerRef, err := host.Get(provider, jsonObject(), background, GetOptions{})
	must(t, err)
	exposed, err := host.Get(exposerType, jsonObject(), background, GetOptions{})
	must(t, err)
	_, err = providerRef.Call("set", float64(1), background)
	must(t, err)
	frames := kmWatch(t, exposed.Member("n"))
	owned := kmWatch(t, exposed.Member("own"))
	held := holdNextCommit()
	changed := make(chan error, 1)
	go func() {
		_, err := own.Load().Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("n", float64(5)) }, background)
		changed <- err
	}()
	<-held.Entered()
	failLive(registry.exposer(0), data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
	_, err = providerRef.Call("set", float64(9), background)
	must(t, err)
	deliveredOn(host)
	nexttest.RequireJSONEqual(t, []any{kmMember(t, exposed, "n"), msCalls(frames.Items())}, []any{float64(1), []any{}})
	held.Release()
	must(t, <-changed)
	deliveredOn(host)
	// The object's own admitted commit still publishes once.
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(owned.Items()), []any{kmN(5)})
	must(t, host.Close(background))
}

// member-source.test.ts › re-binding when a new attempt goes live › recovers a consumer's derive that failed on the
// provider's earlier error, and the derives downstream.
func TestHostMemberSourceRebindingRecoversAConsumersDeriveThatFailedOnTheProvidersEarlierErrorAndTheDerivesDownstream(t *testing.T) {
	counted := mustContract(contracts.Local("app.counted", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("count", contracts.Int()), contracts.Mem("add", contracts.NewMethod(contracts.Str()))},
	}))
	tensType := mustContract(contracts.Local("app.tens", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("tens", contracts.Int()), contracts.Mem("hundreds", contracts.Int())},
	}))
	attempts := &attemptList{}
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(counted, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			attempts.add(self)
			first := attempts.len() == 1
			list := self.State(msList).(*documents.Singleton)
			count := self.Derive([]any{list}, func(values []any) (delta.JsonValue, error) {
				if first && msItemCount(values[0]) == 1 {
					return nil, errors.New("count broke")
				}
				return msItemCount(values[0]), nil
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State:   map[string]chord.Watchable[delta.JsonValue]{"count": count},
				Methods: map[string]contracts.MethodFunction{"add": msPush(list, "")},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(tensType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			tens := self.Derive([]any{self.Get(counted, jsonObject()).Member("count")}, func(values []any) (delta.JsonValue, error) {
				return values[0].(float64) * 10, nil
			})
			hundreds := self.Derive([]any{tens}, func(values []any) (delta.JsonValue, error) { return values[0].(float64) * 10, nil })
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"tens": tens, "hundreds": hundreds},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	countedRef, err := host.Get(counted, jsonObject(), background, GetOptions{})
	must(t, err)
	tens, err := host.Get(tensType, jsonObject(), background, GetOptions{})
	must(t, err)
	_, err = countedRef.Call("add", "a", background)
	must(t, err)
	for _, member := range []string{"tens", "hundreds"} {
		if _, err := tens.Member(member).Value(); err == nil || !strings.Contains(err.Error(), "count broke") {
			t.Fatalf("%s = %v, want it to throw count broke", member, err)
		}
	}
	failLive(attempts.at(0), data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
	msRetry(t, host, countedRef, counted, jsonObject())
	nexttest.RequireJSONEqual(t, []any{kmMember(t, tens, "tens"), kmMember(t, tens, "hundreds")}, []any{float64(10), float64(100)})
	frames := kmWatch(t, tens.Member("hundreds"))
	_, err = countedRef.Call("add", "b", background)
	must(t, err)
	deliveredOn(host)
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(frames.Items()), []any{float64(200)})
	must(t, host.Close(background))
}

// member-source.test.ts › re-binding when a new attempt goes live › re-binds a consumer's derive over a retried provider's
// derive member, and its watches get frames again.
func TestHostMemberSourceRebindingReBindsAConsumersDeriveOverARetriedProvidersDeriveMemberAndItsWatchesGetFramesAgain(t *testing.T) {
	s := msServe(t, loadedOptions{})
	viewer, err := s.Host.Get(msViewer, jsonObject(), background, GetOptions{})
	must(t, err)
	tens := kmWatch(t, viewer.Member("tens"))
	counted := kmWatch(t, s.Store.Member("count"))
	msAdd(t, s.Store, "a")
	deliveredOn(s.Host)
	s.fail()
	deliveredOn(s.Host)
	nexttest.RequireJSONEqual(t, []any{kmMember(t, s.Store, "count"), kmMember(t, viewer, "tens")}, []any{float64(1), float64(10)})
	s.Store.Retry()
	again, err := s.Host.Get(msStore, jsonObject(), background, GetOptions{})
	must(t, err)
	msAdd(t, again, "b")
	deliveredOn(s.Host)
	if kind := viewer.State().Value().Kind; kind != RefStateLive {
		t.Fatalf("viewer state = %s, want live", kind)
	}
	nexttest.RequireJSONEqual(t, []any{kmMember(t, s.Store, "count"), kmMember(t, viewer, "tens")}, []any{float64(2), float64(20)})
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(tens.Items()), []any{float64(10), float64(20)})
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(counted.Items()), []any{float64(1), float64(2)})
	must(t, s.Host.Close(background))
}

// member-source.test.ts › re-binding when a new attempt goes live › sends one frame from the last value when the new
// attempt's member differs.
func TestHostMemberSourceRebindingSendsOneFrameFromTheLastValueWhenTheNewAttemptsMemberDiffers(t *testing.T) {
	shown := mustContract(contracts.Local("app.shown", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("label", contracts.Str())},
	}))
	labels := []string{"old", "new"}
	attempts := &attemptList{}
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(shown, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			label := labels[attempts.len()]
			attempts.add(self)
			self.State(msList)
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"label": self.Derive(nil, func([]any) (delta.JsonValue, error) { return label, nil }),
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	ref, err := host.Get(shown, jsonObject(), background, GetOptions{})
	must(t, err)
	frames := kmWatch(t, ref.Member("label"))
	failLive(attempts.at(0), data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
	msRetry(t, host, ref, shown, jsonObject())
	deliveredOn(host)
	nexttest.RequireJSONEqual(t, kmMember(t, ref, "label"), "new")
	nexttest.RequireJSONEqual(t, msCalls(frames.Items()), []any{msValue("new")})
	must(t, host.Close(background))
}

// member-source.test.ts › subscribers › a hold over a link on a remote object re-exposing a local member gets frames again
// after the local retry.
func TestHostMemberSourceSubscribersAHoldOverALinkOnARemoteObjectReExposingALocalMemberGetsFramesAgainAfterTheLocalRetry(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		count := mustContract(contracts.Local("app.count", contracts.LocalDeclaration{
			Params:  contracts.Params{},
			Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", kmSchema), contracts.Mem("add", contracts.NewMethod())},
		}))
		view := mustContract(contracts.Remote("app.view", contracts.RemoteDeclaration{
			Version: 1, Params: contracts.Params{},
			HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return serverAddress(), nil },
			Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{
				State: []contracts.Property{contracts.Prop("n", kmSchema)},
			})},
		}))
		counter := kmDefine("counter", documents.StateOptions{})
		attempts := &attemptList{}
		hosts := linked(t, func(h FacetHost) error {
			if err := h.Serve(count, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				attempts.add(self)
				doc := self.State(counter).(*documents.Singleton)
				n := self.Derive([]any{doc}, func(values []any) (delta.JsonValue, error) {
					return jsonObject("n", values[0].(*delta.JsonObject).Value("n").(float64)*10), nil
				})
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"n": n},
					Methods: map[string]contracts.MethodFunction{"add": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						_, err := doc.Change(func(draft *delta.Object) (any, error) {
							return nil, draft.Set("n", draft.Get("n").(float64)+1)
						}, ctx)
						return nil, err
					}},
				}}, nil
			}, ServeOptions{}); err != nil {
				return err
			}
			return h.Serve(view, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
				return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"n": Expose(self.Get(count, jsonObject()).Member("n"))},
				}}, nil
			}, ServeOptions{})
		}, linkedOptions{})
		server, client := hosts.Server, hosts.Client
		countRef, err := server.Get(count, jsonObject(), background, GetOptions{})
		must(t, err)
		viewRef, err := client.Get(view, jsonObject(), background, GetOptions{})
		must(t, err)
		if variant, _, _ := viewRef.Variant(); variant != "v" {
			t.Fatalf("variant = %q, want v", variant)
		}
		frames := kmWatch(t, viewRef.Member("n"))
		_, err = countRef.Call("add", nil, background)
		must(t, err)
		transported(hosts.Links[0], hosts.Links[1])
		scopeEventually(t, "the view shows the first count", func() bool {
			value, err := viewRef.Member("n").Value()
			return err == nil && nexttest.JSONEqual(value, kmN(10))
		})
		failLive(attempts.at(0), data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
		countRef.Retry()
		again, err := server.Get(count, jsonObject(), background, GetOptions{})
		must(t, err)
		_, err = again.Call("add", nil, background)
		must(t, err)
		transported(hosts.Links[0], hosts.Links[1])
		scopeEventually(t, "the view shows the retried count", func() bool {
			value, err := viewRef.Member("n").Value()
			return err == nil && nexttest.JSONEqual(value, kmN(20))
		})
		deliveredOn(server, client)
		nexttest.RequireJSONEqual(t, kmMember(t, viewRef, "n"), kmN(20))
		nexttest.RequireJSONEqual(t, nexttest.ValuesOf(frames.Items()), []any{kmN(10), kmN(20)})
		must(t, client.Close(background))
		must(t, server.Close(background))
	})
}

// msSubscriber is Pi's `Subscriber` of "subscribers, directly": the calls it is fed, as the test reads them.
type msSubscriber struct {
	mu     sync.Mutex
	events []any
}

func (s *msSubscriber) push(event any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *msSubscriber) Advance(call chord.WatchCall[delta.JsonValue], _ context.Context) {
	if call.Kind == chord.WatchCallValue {
		ops := make([]any, len(call.Ops))
		for i, op := range call.Ops {
			ops[i] = []any(op)
		}
		s.push([]any{call.Value, ops})
		return
	}
	s.push(call.Error.Error())
}

func (s *msSubscriber) End(end chord.WatchEnd) { s.push(string(end.Kind)) }
func (s *msSubscriber) EndAfterDelivery()      { s.push("ended") }

// member-source.test.ts › subscribers, directly › are fed the member's calls: a failure, then its recovery at the re-bind
// as a root replacement.
func TestHostMemberSourceSubscribersDirectlyAreFedTheMembersCallsAFailureThenItsRecoveryAtTheRebindAsARootReplacement(t *testing.T) {
	counted := mustContract(contracts.Local("app.counted", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("count", contracts.Int()), contracts.Mem("add", contracts.NewMethod(contracts.Str()))},
	}))
	attempts := &attemptList{}
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(counted, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			attempts.add(self)
			first := attempts.len() == 1
			list := self.State(msList).(*documents.Singleton)
			count := self.Derive([]any{list}, func(values []any) (delta.JsonValue, error) {
				if first && msItemCount(values[0]) == 2 {
					return nil, errors.New("count broke")
				}
				return msItemCount(values[0]), nil
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State:   map[string]chord.Watchable[delta.JsonValue]{"count": count},
				Methods: map[string]contracts.MethodFunction{"add": msPush(list, "")},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	ref, err := host.Get(counted, jsonObject(), background, GetOptions{})
	must(t, err)
	resolved, err := ref.Member("count").source()
	must(t, err)
	source := resolved.(state.SourceHandle)
	events := &msSubscriber{}
	var initial any
	host.(*chordHost).loop().Run(func() {
		initial, _ = source.Value()
		source.Subscribe(events)
	})
	nexttest.RequireJSONEqual(t, initial, float64(0))
	for _, text := range []string{"a", "b"} {
		_, err = ref.Call("add", text, background)
		must(t, err)
	}
	failLive(attempts.at(0), data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
	ref.Retry()
	again, err := host.Get(counted, jsonObject(), background, GetOptions{})
	must(t, err)
	_, err = again.Call("add", "c", background)
	must(t, err)
	nexttest.RequireJSONEqual(t, kmMember(t, ref, "count"), float64(3))
	row := func(n float64) []any { return []any{n, []any{[]any{"r", n}}} }
	events.mu.Lock()
	got := append([]any(nil), events.events...)
	events.mu.Unlock()
	nexttest.RequireJSONEqual(t, got, []any{row(1), "count broke", row(2), row(3)})
	must(t, host.Close(background))
}

// member-source.test.ts › a member the new attempt's variant lacks › fails: its watch gets one failure call and stays open,
// reads throw invalid/member; a later attempt brings it back.
func TestHostMemberSourceAMemberTheNewAttemptsVariantLacksFailsItsWatchGetsOneFailureCallAndStaysOpenReadsThrowInvalidMemberALaterAttemptBringsItBack(t *testing.T) {
	two := mustContract(contracts.Remote("app.two", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return serverAddress(), nil },
		Variants: []contracts.VariantEntry{
			contracts.Var("full", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}}),
			contracts.Var("view", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("m", contracts.Int())}}),
		},
	}))
	host := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: ownerPrincipal(), OnError: func(HostError) {}})
	attempts := &attemptList{}
	must(t, host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(two, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			attempts.add(self)
			self.State(msList)
			if attempts.len() == 2 {
				return contracts.Implementation{Variant: "view", Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"m": Expose(state.NewConstant(float64(2)))},
				}}, nil
			}
			return contracts.Implementation{Variant: "full", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": Expose(state.NewConstant(float64(attempts.len())))},
			}}, nil
		}, ServeOptions{})
	})}}, background))
	ref, err := host.Get(two, jsonObject(), background, GetOptions{})
	must(t, err)
	if variant, _, _ := ref.Variant(); variant != "full" {
		t.Fatalf("variant = %q, want full", variant)
	}
	n := ref.Member("n")
	calls := kmWatch(t, n)
	boom := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil)
	failLive(attempts.at(0), boom)
	msRetry(t, host, ref, two, jsonObject())
	deliveredOn(host)
	items := calls.Items()
	if len(items) != 1 || items[0].Kind != chord.WatchCallFailed {
		t.Fatalf("calls = %+v, want one failure", items)
	}
	if err := items[0].Error; err == nil || !strings.Contains(err.Error(), `app.two (variant view) has no state member "n"`) {
		t.Fatalf("failure = %v", err)
	} else if chordErr, ok := data.IsChordError(err); !ok || chordErr.Code != data.CodeInvalid || chordErr.Reason != data.ReasonMember {
		t.Fatalf("failure = %v, want invalid/member", err)
	}
	if _, err := n.Value(); err == nil || err.Error() != `app.two (variant view) has no state member "n"` {
		t.Fatalf("n = %v, want it to throw has no state member", err)
	}
	// The same as over a link: the member comes back with the next attempt that has it.
	failLive(attempts.at(1), boom)
	msRetry(t, host, ref, two, jsonObject())
	deliveredOn(host)
	nexttest.RequireJSONEqual(t, kmMember(t, ref, "n"), float64(3))
	all := calls.Items()
	nexttest.RequireJSONEqual(t, msCall(all[len(all)-1]), msValue(float64(3)))
	must(t, host.Close(background))
}

// member-source.test.ts › an ended object's members › mark the derives over them: a consumer's derive keeps no cached value,
// its read throws released.
func TestHostMemberSourceAnEndedObjectsMembersMarkTheDerivesOverThemAConsumersDeriveKeepsNoCachedValueItsReadThrowsReleased(t *testing.T) {
	paged := mustContract(contracts.Local("app.paged", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	readerType := mustContract(contracts.Local("app.reader", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("twice", contracts.Int())},
	}))
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(paged, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": Expose(state.NewConstant(float64(4)))},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(readerType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			twice := self.Derive([]any{self.Get(paged, jsonObject("id", float64(1))).Member("n")}, func(values []any) (delta.JsonValue, error) {
				return values[0].(float64) * 2, nil
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"twice": twice},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	reader, err := host.Get(readerType, jsonObject(), background, GetOptions{})
	must(t, err)
	nexttest.RequireJSONEqual(t, kmMember(t, reader, "twice"), float64(8))
	h := host.(*chordHost)
	h.loop().Run(func() {
		for _, object := range h.objectList() {
			if object.label() == "app.paged(1)" {
				object.end(data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonEnded, Message: "app.paged(1): ended"}, nil))
			}
		}
	})
	if _, err := reader.Member("twice").Value(); err == nil {
		t.Fatalf("twice read after the object ended, want it to throw released")
	} else if chordErr, ok := data.IsChordErrorCode(err, data.CodeReleased); !ok || chordErr == nil {
		t.Fatalf("twice = %v, want released", err)
	}
	must(t, host.Close(background))
}

// member-source.test.ts › member sources and memory › a failed attempt is collected after a retry while the object, its
// member source and a retained watch live.
func TestHostMemberSourceAFailedAttemptIsCollectedAfterARetryWhileTheObjectItsMemberSourceAndARetainedWatchLive(t *testing.T) {
	var mu sync.Mutex
	var attempts []weak.Pointer[attempt]
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(msStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			mu.Lock()
			attempts = append(attempts, weak.Make(self.(*attempt)))
			mu.Unlock()
			list := self.State(msList).(*documents.Singleton)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"list": Expose(list),
					"count": self.Derive([]any{list}, func(values []any) (delta.JsonValue, error) {
						return msItemCount(values[0]), nil
					}),
				},
				Methods: map[string]contracts.MethodFunction{"add": msPush(list, "text")},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	store := refGet(t, host, msStore, jsonObject())
	frames := kmWatch(t, store.Member("count"))
	attemptAt := func(index int) weak.Pointer[attempt] {
		mu.Lock()
		defer mu.Unlock()
		return attempts[index]
	}
	failLive(attemptAt(0).Value(), data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
	store.Retry()
	again := refGet(t, host, msStore, jsonObject())
	again.Release()
	msAdd(t, store, "a")
	deliveredOn(host)
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(frames.Items()), []any{float64(1)})
	expectCollectedEventually(t, attemptAt(0))
	if attemptAt(1).Value() == nil {
		t.Fatalf("the second attempt is gone")
	}
	must(t, host.Close(background))
}

// member-source.test.ts › member sources and memory › an ended object's member sources are collected with it.
func TestHostMemberSourceAnEndedObjectsMemberSourcesAreCollectedWithIt(t *testing.T) {
	var refs []weak.Pointer[memberSource]
	func() {
		s := msServe(t, loadedOptions{})
		source, err := s.Store.Member("count").source()
		must(t, err)
		if value, err := source.Value(); err != nil || value != float64(0) {
			t.Fatalf("count = %v, %v, want 0", value, err)
		}
		refs = append(refs, weak.Make(source.(*memberSource)))
		must(t, s.Host.Close(background))
	}()
	expectCollectedEventually(t, refs...)
}
