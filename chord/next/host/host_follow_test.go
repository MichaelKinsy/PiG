package host

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/test/next/host/follow.test.ts: the cases that need a FollowSource, a Batch and the loop only (the
// describe "ends during activation and in one batch (review regressions)", whose cases build a FollowSource of their
// own). The other cases serve an object through a whole host (`loaded`, `serve`, `get`): they wait for the host core.
// Own tests below mirror them on FollowSource, a Derive and the fake runtime, each naming the case it mirrors.

// followRuntime is a runtime with synchronous observers, as Durable's views have them (follow.test.ts `Runtime`). Its
// state belongs to the loop: every method but attach runs in a section; attach is what FollowSource.attach calls.
type followRuntime struct {
	loop      *Loop
	value     *delta.JsonObject
	observers []FollowObserver[any]
	// attachments and detached count the attach calls and the detaches.
	attachments int
	detached    int
	// gate, when set, holds every attachment until it is closed (an attachment that takes time); entered gets one value
	// once an attachment registered its observer.
	gate    chan struct{}
	entered chan struct{}
	absent  bool
}

func newFollowRuntime(loop *Loop) *followRuntime {
	return &followRuntime{loop: loop, value: memberCount(0), entered: make(chan struct{}, 16)}
}

func (r *followRuntime) attach(observer FollowObserver[any], _ context.Context) (*FollowAttachment[delta.JsonValue], error) {
	var gate chan struct{}
	r.loop.Run(func() {
		r.observers = append(r.observers, observer)
		r.attachments++
		gate = r.gate
	})
	r.entered <- struct{}{}
	if gate != nil {
		<-gate
	}
	var absent bool
	var value *delta.JsonObject
	r.loop.Run(func() { absent, value = r.absent, r.value })
	if absent {
		return nil, nil
	}
	return &FollowAttachment[delta.JsonValue]{
		Value: value,
		Detach: func() {
			r.loop.Run(func() {
				for i, each := range r.observers {
					if each == observer {
						r.observers = append(r.observers[:i], r.observers[i+1:]...)
						break
					}
				}
				r.detached++
			})
		},
	}, nil
}

// set is one change: every observer advanced with its ops.
func (r *followRuntime) set(n float64, retired bool) {
	r.loop.Run(func() {
		r.value = memberCount(n)
		for _, observer := range append([]FollowObserver[any]{}, r.observers...) {
			observer.Advance(chord.WatchCall[delta.JsonValue]{
				Kind:    chord.WatchCallValue,
				Value:   r.value,
				Ops:     []delta.Op{{"s", delta.Path{"n"}, n}},
				Retired: retired,
			}, memberBackground)
		}
	})
}

// end is the runtime ending every observer.
func (r *followRuntime) end(end chord.WatchEnd) {
	r.loop.Run(func() {
		observers := append([]FollowObserver[any]{}, r.observers...)
		r.observers = nil
		for _, observer := range observers {
			observer.End(end)
		}
	})
}

// counts is how often the runtime attached and detached.
func (r *followRuntime) counts() [2]int {
	var counts [2]int
	r.loop.Run(func() { counts = [2]int{r.attachments, r.detached} })
	return counts
}

// followed is a followed source of a runtime and the ends its owner was told.
type followed struct {
	source *followSource
	ended  *[]chord.WatchEnd
}

// follow makes the followed source of runtime on h (not attached yet).
func (h *memberHarness) follow(runtime *followRuntime, label string) followed {
	var ends []chord.WatchEnd
	owner := followOwner{label: label, ctx: memberBackground, ended: func(end chord.WatchEnd) { ends = append(ends, end) }}
	return followed{source: newFollowSource(runtime.attach, owner, h.batch, h.loop), ended: &ends}
}

// summed is a derive over two followed sources, its computes counted (follow.test.ts `served`'s `sum`).
type summed struct {
	derive   *state.Derive
	computes *int
}

func (h *memberHarness) sum(t *testing.T, left, right *followSource) summed {
	t.Helper()
	computes := 0
	derive := state.NewDerive([]any{left, right}, func(values []any) (delta.JsonValue, error) {
		computes++
		return values[0].(*delta.JsonObject).Value("n").(float64) + values[1].(*delta.JsonObject).Value("n").(float64), nil
	}, h.batch)
	err := derive.Attach(func(source any, _ int) (state.DeriveInput, error) { return source.(state.DeriveInput), nil }, func(error) {})
	if err != nil {
		t.Fatalf("attach derive: %v", err)
	}
	return summed{derive: derive, computes: &computes}
}

// attachBoth attaches the sources of a and b, as the activation does, and returns the sum over them.
func (h *memberHarness) attachBoth(t *testing.T, a, b *followRuntime) (left, right followed, sum summed) {
	t.Helper()
	left, right = h.follow(a, "app.view(): follow #1"), h.follow(b, "app.view(): follow #2")
	for _, each := range []followed{left, right} {
		if err := each.source.attach(memberBackground); err != nil {
			t.Fatalf("attach: %v", err)
		}
	}
	return left, right, h.sum(t, left.source, right.source)
}

func (h *memberHarness) watch(t *testing.T, source state.SourceHandle) chord.WatchHandle[delta.JsonValue] {
	t.Helper()
	var watch chord.WatchHandle[delta.JsonValue]
	var err error
	h.loop.Run(func() { watch, err = source.Watch(memberBackground) })
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	return watch
}

func (h *memberHarness) read(t *testing.T, source state.DeriveSource) delta.JsonValue {
	t.Helper()
	var value delta.JsonValue
	var err error
	h.loop.Run(func() { value, err = source.Value() })
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	return value
}

// valueCall is a value call as a watch's listener gets it: a call without ops carries a nil slice (Pi's `[]`).
func valueCall(value delta.JsonValue, retired bool, ops ...delta.Op) chord.WatchCall[delta.JsonValue] {
	return chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallValue, Value: value, Ops: ops, Retired: retired}
}

func setOp(n float64) delta.Op { return delta.Op{"s", delta.Path{"n"}, n} }

func expectCalls(t *testing.T, got, want []chord.WatchCall[delta.JsonValue]) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %#v, want %#v", got, want)
	}
}

func expectEqual(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %#v, want %#v", what, got, want)
	}
}

// follow.test.ts › ends during activation and in one batch (review regressions) › an end before the attachment resolved
// keeps the attachment's value on the source
func TestEndsDuringActivationAndInOneBatchAnEndBeforeTheAttachmentResolvedKeepsTheAttachmentsValueOnTheSource(t *testing.T) {
	h := newMemberHarness(t)
	source := newFollowSource(
		func(observer FollowObserver[any], _ context.Context) (*FollowAttachment[delta.JsonValue], error) {
			h.loop.Run(func() { observer.End(chord.WatchEnd{Kind: chord.WatchEnded}) })
			return &FollowAttachment[delta.JsonValue]{Value: memberCount(7), Detach: func() {}}, nil
		},
		followOwner{label: "early end", ctx: memberBackground, ended: func(chord.WatchEnd) {}},
		h.batch, h.loop,
	)
	if err := source.attach(memberBackground); err != nil {
		t.Fatalf("attach: %v", err)
	}
	expectEqual(t, "value", h.read(t, source), delta.JsonValue(memberCount(7)))
}

// follow.test.ts › ends during activation and in one batch (review regressions) › a retirement and an end inside one
// batch: the watch gets the retirement, then ends
func TestEndsDuringActivationAndInOneBatchARetirementAndAnEndInsideOneBatchTheWatchGetsTheRetirementThenEnds(t *testing.T) {
	h := newMemberHarness(t)
	var observer FollowObserver[any]
	source := newFollowSource(
		func(attached FollowObserver[any], _ context.Context) (*FollowAttachment[delta.JsonValue], error) {
			observer = attached
			return &FollowAttachment[delta.JsonValue]{Value: memberCount(7), Detach: func() {}}, nil
		},
		followOwner{label: "retirement", ctx: memberBackground, ended: func(chord.WatchEnd) {}},
		h.batch, h.loop,
	)
	if err := source.attach(memberBackground); err != nil {
		t.Fatalf("attach: %v", err)
	}
	watch := h.watch(t, source)
	calls := nexttest.ChangesOf(watch)
	h.commit(t, func() {
		observer.Advance(valueCall(memberCount(7), true), memberBackground)
		observer.End(chord.WatchEnd{Kind: chord.WatchEnded})
	})
	<-watch.Closed()
	expectEqual(t, "end", watch.End(), chord.WatchEnd{Kind: chord.WatchEnded})
	expectCalls(t, calls.Items(), []chord.WatchCall[delta.JsonValue]{valueCall(memberCount(7), true)})
}

// follow.test.ts › ends during activation and in one batch (review regressions) › a call and an end outside a batch with
// nothing watching: a later watch opens at the value and ends
func TestEndsDuringActivationAndInOneBatchACallAndAnEndOutsideABatchWithNothingWatchingALaterWatchOpensAtTheValueAndEnds(t *testing.T) {
	h := newMemberHarness(t)
	var observer FollowObserver[any]
	source := newFollowSource(
		func(attached FollowObserver[any], _ context.Context) (*FollowAttachment[delta.JsonValue], error) {
			observer = attached
			return &FollowAttachment[delta.JsonValue]{Value: memberCount(0), Detach: func() {}}, nil
		},
		followOwner{label: "unwatched", ctx: memberBackground, ended: func(chord.WatchEnd) {}},
		h.batch, h.loop,
	)
	if err := source.attach(memberBackground); err != nil {
		t.Fatalf("attach: %v", err)
	}
	h.loop.Run(func() {
		observer.Advance(valueCall(memberCount(1), true, setOp(1)), memberBackground)
		observer.End(chord.WatchEnd{Kind: chord.WatchEnded})
	})
	h.delivered()
	watch := h.watch(t, source)
	expectEqual(t, "watch value", watch.Value(), delta.JsonValue(memberCount(1)))
	<-watch.Closed()
	expectEqual(t, "end", watch.End(), chord.WatchEnd{Kind: chord.WatchEnded})
	h.loop.Run(source.detach)
}

// Own test, mirrors follow.test.ts › types › infers the value type from the attachment, without a type argument: a
// runtime's own observe is typed by its value, and FollowAttach carries only that type.
func TestFollowAttachIsTypedByItsAttachment(t *testing.T) {
	var attach FollowAttach[*delta.JsonObject] = func(FollowObserver[any], context.Context) (*FollowAttachment[*delta.JsonObject], error) {
		return &FollowAttachment[*delta.JsonObject]{Value: memberCount(1), Detach: func() {}}, nil
	}
	attached, err := attach(nil, memberBackground)
	if err != nil || attached.Value.Value("n") != float64(1) {
		t.Fatalf("attach = %v, %v", attached, err)
	}
}

// Own test, mirrors follow.test.ts › calls › inside a running batch: one frame per commit, and a derive over two views
// computes once with both.
func TestFollowCallsInsideARunningBatchOneFramePerCommitAndADeriveOverTwoViewsComputesOnceWithBoth(t *testing.T) {
	h := newMemberHarness(t)
	a, b := newFollowRuntime(h.loop), newFollowRuntime(h.loop)
	left, _, sum := h.attachBoth(t, a, b)
	calls := nexttest.ChangesOf(h.watch(t, left.source))
	sums := nexttest.ChangesOf(h.watch(t, sum.derive))
	before := *sum.computes
	h.commit(t, func() {
		a.set(1, false)
		a.set(2, false)
		b.set(10, false)
	})
	h.delivered()
	expectCalls(t, calls.Items(), []chord.WatchCall[delta.JsonValue]{valueCall(memberCount(2), false, setOp(1), setOp(2))})
	expectEqual(t, "sums", nexttest.ValuesOf(sums.Items()), []any{float64(12)})
	expectEqual(t, "computes", *sum.computes-before, 1)
}

// Own test, mirrors follow.test.ts › calls › outside any batch: queued and applied in one batch on the next microtask.
func TestFollowCallsOutsideAnyBatchQueuedAndAppliedInOneBatchOnTheNextMicrotask(t *testing.T) {
	h := newMemberHarness(t)
	a, b := newFollowRuntime(h.loop), newFollowRuntime(h.loop)
	left, _, sum := h.attachBoth(t, a, b)
	sums := nexttest.ChangesOf(h.watch(t, sum.derive))
	before := *sum.computes
	var during delta.JsonValue
	h.loop.Run(func() {
		a.set(1, false)
		b.set(2, false)
		during, _ = left.source.Value()
	})
	expectEqual(t, "value in the section of the calls", during, delta.JsonValue(memberCount(0)))
	expectEqual(t, "value after the microtask", h.read(t, left.source), delta.JsonValue(memberCount(1)))
	h.delivered()
	expectEqual(t, "sums", nexttest.ValuesOf(sums.Items()), []any{float64(3)})
	expectEqual(t, "computes", *sum.computes-before, 1)
}

// Own test, mirrors follow.test.ts › calls › before the attachment resolved: applied in order on its value.
func TestFollowCallsBeforeTheAttachmentResolvedAppliedInOrderOnItsValue(t *testing.T) {
	h := newMemberHarness(t)
	a := newFollowRuntime(h.loop)
	a.gate = make(chan struct{})
	left := h.follow(a, "app.view(): follow #1")
	attached := make(chan error, 1)
	h.loop.Go(func() { attached <- left.source.attach(memberBackground) })
	<-a.entered
	a.set(5, false)
	a.set(6, false)
	close(a.gate)
	if err := <-attached; err != nil {
		t.Fatalf("attach: %v", err)
	}
	expectEqual(t, "value", h.read(t, left.source), delta.JsonValue(memberCount(6)))
}

// Own test, mirrors follow.test.ts › activation › an absent address fails the activation transient; a failed activation
// detaches what attached.
func TestFollowActivationAnAbsentAddressFailsAndAFailedActivationDetachesWhatAttached(t *testing.T) {
	h := newMemberHarness(t)
	a, b := newFollowRuntime(h.loop), newFollowRuntime(h.loop)
	b.absent = true
	left, right := h.follow(a, "app.view(): follow #1"), h.follow(b, "app.view(): follow #2")
	if err := left.source.attach(memberBackground); err != nil {
		t.Fatalf("attach a: %v", err)
	}
	err := right.source.attach(memberBackground)
	chordErr, ok := data.IsChordErrorCode(err, data.CodeUnavailable)
	if !ok || chordErr.Message != "app.view(): follow #2: the followed address is absent" {
		t.Fatalf("attach b = %v, want unavailable: the followed address is absent", err)
	}
	h.loop.Run(func() {
		left.source.detach()
		right.source.detach()
	})
	expectEqual(t, "a attached and detached", a.counts(), [2]int{1, 1})
}

// Own test: a followed source read before it attached fails not_ready (follow.ts `value`).
func TestFollowValueBeforeItAttachedIsNotReady(t *testing.T) {
	h := newMemberHarness(t)
	left := h.follow(newFollowRuntime(h.loop), "app.view(): follow #1")
	_, err := left.source.Value()
	chordErr, ok := data.IsChordErrorCode(err, data.CodeNotReady)
	if !ok || chordErr.Message != "app.view(): follow #1: a followed source read before it attached" {
		t.Fatalf("value = %v, want not_ready", err)
	}
}

// Own test, mirrors follow.test.ts › ends (R4) › failed: the live object fails transient once, reported, and its retry
// attaches again: the owner is told once and the value stays; the attempt's end detaches, a new attachment follows.
func TestFollowEndsFailedTheOwnerIsToldOnceTheValueStaysAndARetryAttachesAgain(t *testing.T) {
	h := newMemberHarness(t)
	a, b := newFollowRuntime(h.loop), newFollowRuntime(h.loop)
	left, _, _ := h.attachBoth(t, a, b)
	failure := chord.WatchEnd{Kind: chord.WatchFailed, Error: data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "session failed"}, nil)}
	a.end(failure)
	expectEqual(t, "ends told", *left.ended, []chord.WatchEnd{failure})
	expectEqual(t, "value", h.read(t, left.source), delta.JsonValue(memberCount(0)))
	// Its attempt's end detaches the source, which the runtime ended without detaching it.
	h.loop.Run(left.source.detach)
	retried := h.follow(a, "app.view(): follow #1")
	if err := retried.source.attach(memberBackground); err != nil {
		t.Fatalf("attach again: %v", err)
	}
	expectEqual(t, "a attached and detached", a.counts(), [2]int{2, 1})
	a.set(3, false)
	h.delivered()
	expectEqual(t, "value", h.read(t, retried.source), delta.JsonValue(memberCount(3)))
}

// Own test, mirrors follow.test.ts › ends (R4) › ended: final; a queued retirement reaches the watch first, the value
// stays, a new watch ends at once.
func TestFollowEndsEndedIsFinalAQueuedRetirementReachesTheWatchFirstTheValueStaysAndANewWatchEndsAtOnce(t *testing.T) {
	h := newMemberHarness(t)
	a, b := newFollowRuntime(h.loop), newFollowRuntime(h.loop)
	left, _, _ := h.attachBoth(t, a, b)
	watch := h.watch(t, left.source)
	calls := nexttest.ChangesOf(watch)
	h.loop.Run(func() {
		a.set(4, true)
		a.end(chord.WatchEnd{Kind: chord.WatchEnded})
	})
	<-watch.Closed()
	expectEqual(t, "end", watch.End(), chord.WatchEnd{Kind: chord.WatchEnded})
	expectCalls(t, calls.Items(), []chord.WatchCall[delta.JsonValue]{valueCall(memberCount(4), true, setOp(4))})
	expectEqual(t, "value", h.read(t, left.source), delta.JsonValue(memberCount(4)))
	late := h.watch(t, left.source)
	expectEqual(t, "late value", late.Value(), delta.JsonValue(memberCount(4)))
	<-late.Closed()
	expectEqual(t, "late end", late.End(), chord.WatchEnd{Kind: chord.WatchEnded})
	expectEqual(t, "reported", h.reported(), []state.BatchError{})
	expectEqual(t, "ends told", *left.ended, []chord.WatchEnd{{Kind: chord.WatchEnded}})
}

// Own test, mirrors follow.test.ts › ends during activation and in one batch › an %s end before the attachment resolved
// fails the activation transient: the owner is told at once, the attachment that arrives is still detached with the
// source.
func TestFollowAnEndBeforeTheAttachmentResolvedTellsTheOwnerAndTheAttachmentIsDetachedWithTheSource(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  chord.WatchEnd
	}{
		{"ended", chord.WatchEnd{Kind: chord.WatchEnded}},
		{"failed", chord.WatchEnd{Kind: chord.WatchFailed, Error: data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "early failure"}, nil)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newMemberHarness(t)
			a := newFollowRuntime(h.loop)
			a.gate = make(chan struct{})
			left := h.follow(a, "app.view(): follow #1")
			attached := make(chan error, 1)
			h.loop.Go(func() { attached <- left.source.attach(memberBackground) })
			<-a.entered
			a.end(tc.end)
			close(a.gate)
			if err := <-attached; err != nil {
				t.Fatalf("attach: %v", err)
			}
			expectEqual(t, "ends told", *left.ended, []chord.WatchEnd{tc.end})
			h.loop.Run(left.source.detach)
			counts := a.counts()
			expectEqual(t, "detached", counts[1], counts[0])
		})
	}
}

// Own test, mirrors follow.test.ts › ends during activation and in one batch › an end while the object activates (after
// its sources attached) fails the activation transient: an `ended` end of an attached source tells its owner.
func TestFollowAnEndWhileTheObjectActivatesTellsTheOwner(t *testing.T) {
	h := newMemberHarness(t)
	a, b := newFollowRuntime(h.loop), newFollowRuntime(h.loop)
	left, _, _ := h.attachBoth(t, a, b)
	a.end(chord.WatchEnd{Kind: chord.WatchEnded})
	expectEqual(t, "ends told", *left.ended, []chord.WatchEnd{{Kind: chord.WatchEnded}})
}

// Own test, mirrors follow.test.ts › ends during activation and in one batch › an end outside a batch after calls of two
// views: the views of one commit are delivered together.
func TestFollowAnEndOutsideABatchAfterCallsOfTwoViewsTheViewsOfOneCommitAreDeliveredTogether(t *testing.T) {
	h := newMemberHarness(t)
	a, b := newFollowRuntime(h.loop), newFollowRuntime(h.loop)
	_, _, sum := h.attachBoth(t, a, b)
	sums := nexttest.ChangesOf(h.watch(t, sum.derive))
	before := *sum.computes
	h.loop.Run(func() {
		a.set(1, true)
		b.set(2, false)
		a.end(chord.WatchEnd{Kind: chord.WatchEnded})
	})
	h.delivered()
	expectEqual(t, "sums", nexttest.ValuesOf(sums.Items()), []any{float64(3)})
	expectEqual(t, "computes", *sum.computes-before, 1)
}

// Own test: a call of a detached source is dropped, and an attachment that arrives after its attempt detached the source
// is detached at once, leaving no value (follow.ts `attach`).
func TestFollowAnAttachmentArrivingAfterTheSourceWasDetachedIsDetachedAtOnce(t *testing.T) {
	h := newMemberHarness(t)
	a := newFollowRuntime(h.loop)
	a.gate = make(chan struct{})
	left := h.follow(a, "app.view(): follow #1")
	attached := make(chan error, 1)
	h.loop.Go(func() { attached <- left.source.attach(memberBackground) })
	<-a.entered
	h.loop.Run(left.source.detach)
	a.set(1, false)
	close(a.gate)
	if err := <-attached; err != nil {
		t.Fatalf("attach: %v", err)
	}
	expectEqual(t, "attached and detached", a.counts(), [2]int{1, 1})
	if _, err := left.source.Value(); err == nil {
		t.Fatalf("a source detached before its attachment arrived has a value")
	}
}

// Own test, mirrors follow.test.ts › GC › a source its runtime ended is still detached at the object's end, then
// collected.
func TestFollowASourceItsRuntimeEndedIsStillDetachedAtTheObjectsEndThenCollected(t *testing.T) {
	h := newMemberHarness(t)
	a := newFollowRuntime(h.loop)
	ref := func() weak.Pointer[followSource] {
		left := h.follow(a, "app.view(): follow #1")
		if err := left.source.attach(memberBackground); err != nil {
			t.Fatalf("attach: %v", err)
		}
		a.end(chord.WatchEnd{Kind: chord.WatchEnded})
		h.delivered()
		expectEqual(t, "detached before the object's end", a.counts()[1], 0)
		h.loop.Run(left.source.detach)
		expectEqual(t, "detached at the object's end", a.counts()[1], 1)
		return weak.Make(left.source)
	}()
	nexttest.ExpectCollected(t, ref)
}

// Own test, mirrors follow.test.ts › GC › an ended object's followed sources are collected, and their observers
// detached.
func TestFollowAnEndedObjectsFollowedSourcesAreCollectedAndTheirObserversDetached(t *testing.T) {
	h := newMemberHarness(t)
	a := newFollowRuntime(h.loop)
	ref := func() weak.Pointer[followSource] {
		left := h.follow(a, "app.view(): follow #1")
		if err := left.source.attach(memberBackground); err != nil {
			t.Fatalf("attach: %v", err)
		}
		h.loop.Run(left.source.detach)
		return weak.Make(left.source)
	}()
	var observers int
	h.loop.Run(func() { observers = len(a.observers) })
	expectEqual(t, "observers", observers, 0)
	nexttest.ExpectCollected(t, ref)
}

// Own test, mirrors follow.ts `subscribe` and `deliver`: a member source over a followed source passes its frames on, its
// watch ends `ended` once what the runtime gave is delivered, and a subscriber of an ended source ends at once (a
// reference's watch through its member source, follow.test.ts › ends (R4) › ended).
func TestFollowASubscriberOfAnEndedSourceEndsAtOnceAndAMemberSourceOverItEndsItsWatchesAfterTheirFrames(t *testing.T) {
	h := newMemberHarness(t)
	a := newFollowRuntime(h.loop)
	left := h.follow(a, "app.view(): follow #1")
	if err := left.source.attach(memberBackground); err != nil {
		t.Fatalf("attach: %v", err)
	}
	m := h.member(left.source)
	watch := h.watch(t, m)
	calls := nexttest.ChangesOf(watch)
	h.loop.Run(func() {
		a.set(1, false)
		a.end(chord.WatchEnd{Kind: chord.WatchEnded})
	})
	<-watch.Closed()
	expectEqual(t, "end", watch.End(), chord.WatchEnd{Kind: chord.WatchEnded})
	expectCalls(t, calls.Items(), []chord.WatchCall[delta.JsonValue]{valueCall(memberCount(1), false, setOp(1))})
	subscriber := &recorded{}
	h.loop.Run(func() { left.source.Subscribe(subscriber) })
	expectEqual(t, "events", subscriber.events, []any{"ended"})
}

// Own test, mirrors follow.test.ts › calls › before the attachment resolved: applied in order on its value, for an
// attachment whose value is older than the calls that came meanwhile (Durable's observation starts from the value at the
// attach call; the test's Runtime reads its value after its gate): the calls move the attachment's value on, in order,
// and calls of another kind are not applied.
func TestFollowCallsBeforeTheAttachmentResolvedMoveTheAttachmentsOlderValueOn(t *testing.T) {
	h := newMemberHarness(t)
	var observer FollowObserver[any]
	registered := make(chan struct{})
	gate := make(chan struct{})
	source := newFollowSource(
		func(attached FollowObserver[any], _ context.Context) (*FollowAttachment[delta.JsonValue], error) {
			observer = attached
			registered <- struct{}{}
			<-gate
			return &FollowAttachment[delta.JsonValue]{Value: memberCount(0), Detach: func() {}}, nil
		},
		followOwner{label: "stale", ctx: memberBackground, ended: func(chord.WatchEnd) {}},
		h.batch, h.loop,
	)
	attached := make(chan error, 1)
	h.loop.Go(func() { attached <- source.attach(memberBackground) })
	<-registered
	h.loop.Run(func() {
		observer.Advance(valueCall(memberCount(5), false, setOp(5)), memberBackground)
		observer.Advance(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallFailed, Error: errors.New("ignored")}, memberBackground)
		observer.Advance(valueCall(memberCount(6), false, setOp(6)), memberBackground)
	})
	close(gate)
	if err := <-attached; err != nil {
		t.Fatalf("attach: %v", err)
	}
	expectEqual(t, "value", h.read(t, source), delta.JsonValue(memberCount(6)))
	h.loop.Run(source.detach)
}
