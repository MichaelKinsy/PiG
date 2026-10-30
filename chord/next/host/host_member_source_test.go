package host

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
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
	nexttest.ExpectCollected(t, oldDoc)
	nexttest.ExpectCollected(t, oldMember)
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
	nexttest.ExpectCollected(t, ref)
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
