package host

import (
	"context"
	"reflect"
	"strconv"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	chordtelemetry "github.com/MichaelKinsy/PiG/chord/next/telemetry"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/test/host/events.test.ts (Pi 8d30e6306): the event flush (one flush per turn after the
// published state, listeners in entry then subscription order outside the fence), who gets an event (the emit counter
// per binding), GC and the cost of an emit.
//
// Pi's one turn is one host section (`turn`); `await flushed()` is `flushed(host)`.

func wantSeen(t testing.TB, seen []any, want ...any) {
	t.Helper()
	if want == nil {
		want = []any{}
	}
	if seen == nil {
		seen = []any{}
	}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("seen = %#v, want %#v", seen, want)
	}
}

// failLiveOf fails the live attempt self as a runtime failure of its owner does.
func failLiveOf(self Self) {
	failLive(self, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
}

func TestEventFlushDeliversATurnsEmitsInOneFlushInOrderStampedWithTheEmitsNow(t *testing.T) {
	k := ticking(t, loadedOptions{})
	ticker := mustGet(t, k.Host, Ticker, jsonObject())
	type seenEvent struct {
		value any
		at    int64
	}
	var seen []seenEvent
	on(t, ticker, "ticks", func(e contracts.EventDelivery[delta.JsonValue]) {
		seen = append(seen, seenEvent{e.Value.(*delta.JsonObject).Value("y"), e.At})
	}, background)
	before := time.Now().UnixMilli()
	hostTurn(k.Host, func() {
		_ = k.Source().Emit(sample(1))
		_ = k.Source().Emit(sample(2))
		// Nothing is delivered inside the turn that emitted.
		if len(seen) != 0 {
			t.Errorf("delivered inside the emitting turn: %v", seen)
		}
	})
	flushed(k.Host)
	if len(seen) != 2 || seen[0].value != 1.0 || seen[1].value != 2.0 {
		t.Fatalf("seen = %+v", seen)
	}
	for _, event := range seen {
		if event.at < before {
			t.Errorf("at = %d, before = %d", event.at, before)
		}
	}
}

func TestEventFlushPutsWhatAListenerEmitsIntoTheNextFlush(t *testing.T) {
	k := ticking(t, loadedOptions{})
	ticker := mustGet(t, k.Host, Ticker, jsonObject())
	var seen []any
	on(t, ticker, "ticks", func(e contracts.EventDelivery[delta.JsonValue]) {
		y := e.Value.(*delta.JsonObject).Value("y").(float64)
		seen = append(seen, y)
		if y < 3 {
			_ = k.Source().Emit(sample(y + 1))
		}
	}, background)
	hostTurn(k.Host, func() { _ = k.Source().Emit(sample(1)) })
	flushed(k.Host)
	wantSeen(t, seen, 1.0, 2.0, 3.0)
}

func TestEventFlushCallsListenersInEntryOrderThenSubscriptionOrderAcrossReferences(t *testing.T) {
	k := ticking(t, loadedOptions{})
	one := mustGet(t, k.Host, Ticker, jsonObject())
	two := mustGet(t, k.Host, Ticker, jsonObject())
	var seen []any
	on(t, two, "ticks", recorder(&seen, "a"), background)
	on(t, one, "ticks", recorder(&seen, "b"), background)
	on(t, two, "ticks", recorder(&seen, "c"), background)
	hostTurn(k.Host, func() {
		_ = k.Source().Emit(sample(1))
		_ = k.Source().Emit(sample(2))
	})
	flushed(k.Host)
	wantSeen(t, seen, "a1", "b1", "c1", "a2", "b2", "c2")
}

func TestEventFlushLetsAListenerStopReleaseSubscribeStartATransactionAndEmitACallDueWhenItStoppedIsSkipped(t *testing.T) {
	k := ticking(t, loadedOptions{})
	one := mustGet(t, k.Host, Ticker, jsonObject())
	two := mustGet(t, k.Host, Ticker, jsonObject())
	var seen []any
	var second contracts.EventSubscription
	var late contracts.EventSubscription
	first := on(t, one, "ticks", func(e contracts.EventDelivery[delta.JsonValue]) {
		seen = append(seen, "first"+strconv.Itoa(int(e.Value.(*delta.JsonObject).Value("y").(float64))))
		second.Stop()
		two.Release()
		var err error
		late, err = one.Events("ticks").On(recorder(&seen, "late"), background)
		must(t, err)
		self := k.Probe.lastSelf()
		go func() { _, _ = self.Tx(func(documents.Tx) (any, error) { return nil, nil }, background) }()
	}, background)
	second = on(t, one, "ticks", recorder(&seen, "second"), background)
	on(t, two, "ticks", recorder(&seen, "released"), background)
	hostTurn(k.Host, func() { _ = k.Source().Emit(sample(1)) })
	flushed(k.Host)
	first.Stop()
	if late == nil {
		t.Fatal("the listener's own subscription was not made")
	}
	hostTurn(k.Host, func() { _ = k.Source().Emit(sample(2)) })
	flushed(k.Host)
	wantSeen(t, seen, "first1", "late2")
}

func TestEventFlushReportsAThrowingListenerAndEndsOnlyItsSubscription(t *testing.T) {
	k := ticking(t, loadedOptions{})
	ticker := mustGet(t, k.Host, Ticker, jsonObject())
	var seen []any
	on(t, ticker, "ticks", func(contracts.EventDelivery[delta.JsonValue]) { panic(errListener) }, background)
	on(t, ticker, "ticks", recorder(&seen, ""), background)
	hostTurn(k.Host, func() {
		_ = k.Source().Emit(sample(1))
		_ = k.Source().Emit(sample(2))
	})
	flushed(k.Host)
	wantSeen(t, seen, 1.0, 2.0)
	all := k.Errors.All()
	if len(all) != 1 || all[0].Kind != "listener_error" || all[0].Error == nil || all[0].Error.Error() != "listener" {
		t.Fatalf("errors = %+v", all)
	}
}

type listenerError struct{}

func (listenerError) Error() string { return "listener" }

var errListener = listenerError{}

func TestEventFlushThrowsReleasedForAnEmitAfterItsAttemptsSeal(t *testing.T) {
	k := ticking(t, loadedOptions{})
	ref := mustGet(t, k.Host, NamedTicker, jsonObject("name", "a"))
	source := k.Probe.lastNamed()
	ref.Release()
	flushed(k.Host)
	var err error
	scopeEventually(t, "the attempt's seal", func() bool {
		err = source.Emit(sample(1))
		return err != nil
	})
	if chordErr, ok := err.(*data.ChordError); !ok || chordErr.Code != data.CodeReleased {
		t.Fatalf("err = %#v, want released", err)
	}
}

func TestEventFlushDeliversAnEmitMadeInsideATransactionThatFailsEmitIsNoTransactionWork(t *testing.T) {
	k := ticking(t, loadedOptions{})
	ticker := mustGet(t, k.Host, Ticker, jsonObject())
	var seen []any
	on(t, ticker, "ticks", recorder(&seen, ""), background)
	_, err := ticker.Call("failing", sample(7), background)
	if err == nil || err.Error() != "refused" {
		t.Fatalf("err = %v, want refused", err)
	}
	flushed(k.Host)
	wantSeen(t, seen, 7.0)
	count, err := ticker.Member("count").Value()
	must(t, err)
	if count != 0.0 {
		t.Fatalf("count = %v, want 0", count)
	}
}

func TestEventFlushLetsTheOwnerListenToItsOwnSourceFromItsSubscriptionOn(t *testing.T) {
	k := ticking(t, loadedOptions{})
	mustGet(t, k.Host, Ticker, jsonObject())
	var seen []any
	var own contracts.EventSubscription
	hostTurn(k.Host, func() {
		_ = k.Source().Emit(sample(1))
		var err error
		own, err = k.Source().On(recorder(&seen, ""), background)
		must(t, err)
		_ = k.Source().Emit(sample(2))
	})
	flushed(k.Host)
	own.Stop()
	hostTurn(k.Host, func() { _ = k.Source().Emit(sample(3)) })
	flushed(k.Host)
	wantSeen(t, seen, 2.0)
}

// --- who gets an event ---------------------------------------------------------------------------------------------

func TestEventsWhoGetsNothingForAReferenceBoundAfterTheEmitAlsoBeforeTheFlush(t *testing.T) {
	k := ticking(t, loadedOptions{})
	mustGet(t, k.Host, Ticker, jsonObject())
	var late *Reference
	var seen []any
	// Bound and subscribed in the emitting turn, before the flush: the emit is not its (it took the counter first).
	hostTurn(k.Host, func() {
		_ = k.Source().Emit(sample(1))
		var err error
		late, err = k.Host.Hold(background, Ticker, jsonObject(), GetOptions{})
		must(t, err)
		on(t, late, "ticks", recorder(&seen, ""), background)
	})
	waitLive(t, late)
	hostTurn(k.Host, func() { _ = k.Source().Emit(sample(2)) })
	flushed(k.Host)
	wantSeen(t, seen, 2.0)
}

func TestEventsWhoGetsAnEventForAReferenceBoundBeforeTheEmitAndSubscribedAfterItBeforeTheFlush(t *testing.T) {
	k := ticking(t, loadedOptions{})
	ticker := mustGet(t, k.Host, Ticker, jsonObject())
	var seen []any
	hostTurn(k.Host, func() {
		_ = k.Source().Emit(sample(1))
		on(t, ticker, "ticks", recorder(&seen, ""), background)
	})
	flushed(k.Host)
	wantSeen(t, seen, 1.0)
}

func TestEventsWhoGetsNothingForABindingMadeAtTheEmitsCounterValueASameHostReferenceAndAReexposingView(t *testing.T) {
	k := ticking(t, loadedOptions{})
	named, err := k.Host.Hold(background, NamedTicker, jsonObject("name", "a"), GetOptions{})
	must(t, err)
	waitLive(t, named)
	flushed(k.Host)
	source := k.Probe.lastNamed()
	var same, view *Reference
	var seen []any
	hostTurn(k.Host, func() {
		_ = source.Emit(sample(1))
		// Both bind after the emit, in the same turn: the counter has the emit's value. They subscribe before the flush.
		var err error
		same, err = k.Host.Hold(background, NamedTicker, jsonObject("name", "a"), GetOptions{})
		must(t, err)
		on(t, same, "ticks", recorder(&seen, "same"), background)
		view, err = k.Host.Hold(background, TickerView, jsonObject("name", "a"), GetOptions{})
		must(t, err)
		on(t, view, "ticks", recorder(&seen, "view"), background)
	})
	flushed(k.Host)
	hostTurn(k.Host, func() { _ = source.Emit(sample(2)) })
	flushed(k.Host)
	wantSeen(t, seen, "same2", "view2")
}

func TestEventsWhoGetsNothingForAReferenceReleasedBeforeTheFlush(t *testing.T) {
	k := ticking(t, loadedOptions{})
	ticker := mustGet(t, k.Host, Ticker, jsonObject())
	var seen []any
	on(t, ticker, "ticks", recorder(&seen, ""), background)
	hostTurn(k.Host, func() {
		_ = k.Source().Emit(sample(1))
		ticker.Release()
	})
	flushed(k.Host)
	wantSeen(t, seen)
}

func TestEventsWhoGetsNothingOnceItsObjectFailedBeforeTheFlushAndAgainOnceARetryIsLive(t *testing.T) {
	k := ticking(t, loadedOptions{})
	ticker := mustGet(t, k.Host, Ticker, jsonObject())
	var seen []any
	on(t, ticker, "ticks", recorder(&seen, ""), background)
	failing := k.Probe.lastSelf()
	hostTurn(k.Host, func() {
		_ = k.Source().Emit(sample(1))
		// A live failure of the owner (as a migrate failing on access): its references show it failed at once.
		failLiveOf(failing)
	})
	flushed(k.Host)
	if kind := ticker.State().Value().Kind; kind != RefStateFailed {
		t.Fatalf("state = %v, want failed", kind)
	}
	ticker.Retry()
	waitLive(t, ticker)
	hostTurn(k.Host, func() { _ = k.Source().Emit(sample(2)) })
	flushed(k.Host)
	wantSeen(t, seen, 2.0)
}

func TestEventsWhoGetsNothingThroughAReexposingViewOnceTheSourceFailedBeforeTheFlush(t *testing.T) {
	k := ticking(t, loadedOptions{})
	view := mustGet(t, k.Host, TickerView, jsonObject("name", "a"))
	var seen []any
	on(t, view, "ticks", recorder(&seen, ""), background)
	hostTurn(k.Host, func() {
		_ = k.Probe.lastNamed().Emit(sample(1))
		failLiveOf(k.Probe.lastNamedSelf())
	})
	flushed(k.Host)
	wantSeen(t, seen)
}

// --- GC ------------------------------------------------------------------------------------------------------------

func TestEventsGCStoppedSubscriptionsReleasedReferencesAndAFlushedListKeepNothing(t *testing.T) {
	k := ticking(t, loadedOptions{})
	type refs struct {
		listener, kept    weak.Pointer[int]
		subscription      weak.Pointer[subscription]
		keptSubscription  weak.Pointer[subscription]
		value             weak.Pointer[delta.JsonObject]
		other             weak.Pointer[Reference]
		subscriptionAlive func() bool
	}
	made := func() refs {
		ticker := mustGet(t, k.Host, Ticker, jsonObject())
		listener := new(int)
		sub := on(t, ticker, "ticks", func(contracts.EventDelivery[delta.JsonValue]) { _ = *listener }, background)
		other := mustGet(t, k.Host, Ticker, jsonObject())
		keptMarker := new(int)
		kept := on(t, other, "ticks", func(contracts.EventDelivery[delta.JsonValue]) { _ = *keptMarker }, background)
		value := sample(2)
		hostTurn(k.Host, func() {
			_ = k.Source().Emit(sample(1))
			_ = k.Source().Emit(value)
		})
		flushed(k.Host)
		sub.Stop()
		other.Release()
		return refs{
			listener: weak.Make(listener), kept: weak.Make(keptMarker),
			subscription: weak.Make(sub.(*subscription)), keptSubscription: weak.Make(kept.(*subscription)),
			value: weak.Make(value), other: weak.Make(other),
		}
	}
	r := made()
	expectCollected(t, r.listener, r.kept)
	expectCollected(t, r.subscription, r.keptSubscription)
	expectCollected(t, r.value)
	expectCollected(t, r.other)
	if r.subscription.Value() != nil {
		t.Fatal("the stopped subscription is kept")
	}
}

func TestEventsGCASubscriptionKeepsNoAbortListenerOnceStopped(t *testing.T) {
	k := ticking(t, loadedOptions{})
	ticker := mustGet(t, k.Host, Ticker, jsonObject())
	ctx, cancel := context.WithCancel(background)
	defer cancel()
	ref := func() weak.Pointer[subscription] {
		sub := on(t, ticker, "ticks", func(contracts.EventDelivery[delta.JsonValue]) {}, ctx)
		made, ok := sub.(*subscription)
		if !ok {
			t.Fatalf("subscription is %T", sub)
		}
		sub.Stop()
		return weak.Make(made)
	}()
	expectCollected(t, ref)
}

func TestEventsGCAStoppedSubscriptionKeptByItsHolderKeepsNeitherItsSourceNorItsReference(t *testing.T) {
	loop := NewLoop()
	owner := &eventSourceOwner{events: newHostEvents(loop, chordtelemetry.Metrics(nil), func(error) {}), sealed: func() error { return nil }}
	var kept []contracts.EventSubscription
	var sourceRef weak.Pointer[ownEventSource]
	var tickerRef weak.Pointer[Reference]
	func() {
		source := newOwnEventSource(owner)
		own, err := source.On(func(contracts.EventDelivery[delta.JsonValue]) {}, background)
		must(t, err)
		own.Stop()
		k := ticking(t, loadedOptions{})
		ticker := mustGet(t, k.Host, Ticker, jsonObject())
		ctx, cancel := context.WithCancel(background)
		defer cancel()
		fromReference := on(t, ticker, "ticks", func(contracts.EventDelivery[delta.JsonValue]) {}, ctx)
		fromReference.Stop()
		ticker.Release()
		kept = []contracts.EventSubscription{own, fromReference}
		sourceRef, tickerRef = weak.Make(source), weak.Make(ticker)
	}()
	expectCollected(t, sourceRef)
	expectCollected(t, tickerRef)
	if len(kept) != 2 {
		t.Fatalf("kept = %d", len(kept))
	}
}

// --- cost ----------------------------------------------------------------------------------------------------------

func TestEventsCostAContractWithoutEventMembersCostsNoEventBookkeepingPerBindingHereOrOverALink(t *testing.T) {
	l := linked(t, func(host FacetHost) error { _, err := serveNotes(host, nil); return err }, linkedOptions{})
	notes, err := l.Client.Get(background, Notes, jsonObject("session", "S1"), GetOptions{})
	must(t, err)
	if notes.binding.eventsOf != nil || notes.binding.events == nil || len(notes.binding.events) != 0 {
		t.Fatalf("client binding: eventsOf = %v, events = %v", notes.binding.eventsOf, notes.binding.events)
	}
	notes.Release()
	again, err := l.Client.Get(background, Notes, jsonObject("session", "S1"), GetOptions{})
	must(t, err)
	if kind := again.State().Value().Kind; kind != RefStateLive {
		t.Fatalf("state = %v", kind)
	}
	if again.binding.eventsOf != nil {
		t.Fatal("a binding of a contract without event members keeps event bookkeeping")
	}
	store, err := l.Server.Get(background, NoteStore, jsonObject("session", "S1"), GetOptions{})
	must(t, err)
	if store.binding.eventsOf != nil {
		t.Fatal("a local binding of a contract without event members keeps event bookkeeping")
	}
	store.Release()
	for _, h := range []Host{l.Client, l.Server} {
		if counter := h.(*chordHost).eventFlow.counter(); counter != 0 {
			t.Fatalf("counter = %d, want 0 (nothing emitted)", counter)
		}
	}
	// Their names share one empty list of event members.
	variants, err := contracts.CurrentVariants(Notes)
	must(t, err)
	editor, _ := variants.Get("editor")
	reader, _ := variants.Get("reader")
	editorNames, readerNames := contracts.VariantNamesOf(editor), contracts.VariantNamesOf(reader)
	if editorNames.Events == nil || readerNames.Events == nil || len(editorNames.Events)+len(readerNames.Events) != 0 {
		t.Fatalf("events = %#v, %#v", editorNames.Events, readerNames.Events)
	}
}

func TestEventsCostAnEmitWithNoHolderEncodesNothingWalksAndCopiesNothingItIsCountedEmitted(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	k := ticking(t, loadedOptions{Telemetry: recorder})
	mustGet(t, k.Host, Ticker, jsonObject())
	hostTurn(k.Host, func() { _ = k.Source().Emit(sample(1)) })
	flushed(k.Host)
	counts := map[string]float64{}
	for _, measurement := range recorder.GetMetrics() {
		counts[measurement.Name] += float64(measurement.Value)
	}
	if counts["chord.events.emitted"] != 1 {
		t.Fatalf("emitted = %v", counts["chord.events.emitted"])
	}
	if _, found := counts["chord.events.encodes"]; found {
		t.Fatal("an emit with no holder encoded")
	}
	if _, found := counts["chord.data.walks"]; found {
		t.Fatal("an emit with no holder walked")
	}
}
