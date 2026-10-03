package services

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourceStatePublishesInCommitOrderWithoutHoldingSourceLocks(t *testing.T) {
	state := &SourceState[int]{}
	var values []int
	var deliveries []ReplicatedStateDelivery
	ctx := t.Context()
	stop := state.Subscribe(func(value int, delivered context.Context, delivery ReplicatedStateDelivery) {
		values = append(values, value)
		deliveries = append(deliveries, delivery)
		if delivery.Kind == "update" && delivered != ctx {
			t.Error("publication context changed")
		}
		_ = state.Value()
	})
	first := state.replace(ctx, 1)
	second := state.replace(ctx, 2)
	second()
	first()
	state.replace(ctx, 2)()
	queued := state.replace(ctx, 3)
	stop()
	stop()
	queued()
	state.replace(ctx, 4)()
	if !reflect.DeepEqual(values, []int{0, 1, 2}) {
		t.Fatalf("state delivery = %v", values)
	}
	if !reflect.DeepEqual(deliveries, []ReplicatedStateDelivery{{Kind: "hydrate", Sequence: 0}, {Kind: "update", Sequence: 1}, {Kind: "update", Sequence: 2}}) {
		t.Fatalf("delivery metadata = %#v", deliveries)
	}
}

func TestSourceStateHydratesReentrantSubscriptionBeforeReturning(t *testing.T) {
	state := &SourceState[int]{}
	var events []string
	var stopInner func()
	stop := state.Subscribe(func(value int, _ context.Context, _ ReplicatedStateDelivery) {
		if value != 1 {
			return
		}
		events = append(events, "outer")
		stopInner = state.Subscribe(func(_ int, _ context.Context, delivery ReplicatedStateDelivery) {
			events = append(events, delivery.Kind)
		})
		events = append(events, "returned")
	})
	defer stop()
	state.replace(t.Context(), 1)()
	defer stopInner()
	if !reflect.DeepEqual(events, []string{"outer", "hydrate", "returned"}) {
		t.Fatalf("subscription hydration order = %v", events)
	}
}

// chord state.ts:131-141 pushes and drains the hydration without yielding, so subscribe returns after its hydration
// callback. A publication on another goroutine between those steps must queue behind the hydration, not run it on the
// publishing goroutine after Subscribe returned.
//
// The window is a few instructions wide, so the test repeats the race for a bounded time; without the claim it failed
// 13 of 15 runs (9 of 10 plain, 4 of 5 with -race).
func TestSourceStateSubscribeHydratesBeforeReturningUnderConcurrentPublication(t *testing.T) {
	deadline := time.Now().Add(2 * time.Second)
	for iteration := 0; iteration < 20000 && time.Now().Before(deadline); iteration++ {
		state := &SourceState[int]{}
		publishing, stop := make(chan struct{}), make(chan struct{})
		var publisher sync.WaitGroup
		publisher.Go(func() {
			for value := 1; ; value++ {
				select {
				case <-stop:
					return
				default:
				}
				state.replace(context.Background(), value)()
				if value == 1 {
					close(publishing)
				}
			}
		})
		<-publishing
		var hydrated atomic.Bool
		remove := state.Subscribe(func(_ int, _ context.Context, delivery ReplicatedStateDelivery) {
			if delivery.Kind == "hydrate" {
				hydrated.Store(true)
			}
		})
		returnedHydrated := hydrated.Load()
		close(stop)
		publisher.Wait()
		remove()
		if !returnedHydrated {
			t.Fatalf("iteration %d: Subscribe returned before its hydration callback ran", iteration)
		}
	}
}

// reportingState is a SourceState whose listener failures are collected instead of thrown asynchronously.
func reportingState[T comparable](reports *[]error) *SourceState[T] {
	state := &SourceState[T]{}
	state.reportError = func(err error) { *reports = append(*reports, err) }
	return state
}

// .upstream/v0.99.1/packages/chord/src/services/state.ts:52-77,90-98: a listener failure never reaches the publisher.
// The failing listener does not suppress later listeners or later publications, and publication does not throw.
func TestSourceStateReportsListenerPanicsAndContinuesDelivery(t *testing.T) {
	var reports []error
	state := reportingState[int](&reports)
	first, second := errors.New("first listener"), errors.New("second listener")
	stopFirst := state.Subscribe(func(value int, _ context.Context, _ ReplicatedStateDelivery) {
		if value != 0 {
			panic(first)
		}
	})
	stopSecond := state.Subscribe(func(value int, _ context.Context, _ ReplicatedStateDelivery) {
		if value != 0 {
			panic(second)
		}
	})
	var values []int
	stop := state.Subscribe(func(value int, _ context.Context, _ ReplicatedStateDelivery) { values = append(values, value) })
	defer stop()
	if failure := capturePanic(func() { state.replace(t.Context(), 1)() }); failure != nil {
		t.Fatalf("publication threw %v", failure)
	}
	if len(reports) != 2 || !errors.Is(reports[0], first) || !errors.Is(reports[1], second) {
		t.Fatalf("reports = %v", reports)
	}
	stopFirst()
	stopSecond()
	state.replace(t.Context(), 2)()
	if !reflect.DeepEqual(values, []int{0, 1, 2}) || len(reports) != 2 {
		t.Fatalf("delivery stopped after listener error: values=%v reports=%v", values, reports)
	}
}

// .upstream/v0.99.2/packages/chord/test/state-delivery.test.ts:188-202 "isolates a synchronous hydration failure without
// removing the subscription".
func TestSourceStateIsolatesHydrationFailureWithoutRemovingSubscription(t *testing.T) {
	var reports []error
	state := reportingState[int](&reports)
	failure := errors.New("sync hydration")
	var received []int
	got := capturePanic(func() {
		state.Subscribe(func(value int, _ context.Context, _ ReplicatedStateDelivery) {
			received = append(received, value)
			if value == 0 {
				panic(failure)
			}
		})
	})
	if got != nil {
		t.Fatalf("Subscribe threw %v", got)
	}
	state.replace(t.Context(), 1)()
	if len(reports) != 1 || !errors.Is(reports[0], failure) || !reflect.DeepEqual(received, []int{0, 1}) {
		t.Fatalf("reports=%v received=%v", reports, received)
	}
}

// .upstream/v0.99.2/packages/chord/test/state-delivery.test.ts:242-263: a mutable state reports listener failures through
// reportErrorAsync (state.ts:118-121,449-453), an uncaught error thrown outside the publishing call.
func TestSourceStateDefaultReporterThrowsAsynchronously(t *testing.T) {
	var thrown []error
	previous := throwAsync
	throwAsync = func(err error) { thrown = append(thrown, err) }
	defer func() { throwAsync = previous }()
	state := &SourceState[int]{}
	failure := errors.New("mutable listener")
	state.Subscribe(func(value int, _ context.Context, _ ReplicatedStateDelivery) {
		if value == 1 {
			panic(failure)
		}
	})
	if got := capturePanic(func() { state.replace(t.Context(), 1)() }); got != nil {
		t.Fatalf("replace threw %v", got)
	}
	if len(thrown) != 1 || !errors.Is(thrown[0], failure) {
		t.Fatalf("asynchronous reports = %v", thrown)
	}
}

// .upstream/v0.99.2/packages/chord/test/state-delivery.test.ts:131-140 "serializes reentrant hydration and update callbacks".
func TestSourceStateSerializesReentrantHydrationAndUpdateCallbacks(t *testing.T) {
	state := &SourceState[int]{}
	var events []string
	state.Subscribe(func(value int, _ context.Context, _ ReplicatedStateDelivery) {
		events = append(events, "start:"+strconv.Itoa(value))
		if value < 2 {
			state.replace(t.Context(), value+1)()
		}
		events = append(events, "end:"+strconv.Itoa(value))
	})
	if want := []string{"start:0", "end:0", "start:1", "end:1", "start:2", "end:2"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

// .upstream/v0.99.2/packages/chord/test/state-delivery.test.ts:86-106 "bounds %i pending deliveries": the running hydration
// callback publishes count revisions; a subscriber keeps at most 100 pending deliveries and drops the older ones (state.ts:41-49).
func TestSourceStateBoundsPendingDeliveries(t *testing.T) {
	for _, count := range []int{100, 101, 102, 201, 202} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			state := &SourceState[int]{}
			var received []int
			state.Subscribe(func(value int, _ context.Context, _ ReplicatedStateDelivery) {
				received = append(received, value)
				if value == 0 {
					for next := 1; next <= count; next++ {
						state.replace(t.Context(), next)()
					}
				}
			})
			first := (count-1)/100*100 + 1
			want := []int{0}
			for value := first; value <= count; value++ {
				want = append(want, value)
			}
			if !reflect.DeepEqual(received, want) {
				t.Fatalf("received = %v, want %v", received, want)
			}
			if state.Value() != count {
				t.Fatalf("value = %d", state.Value())
			}
		})
	}
}

// .upstream/v0.99.2/packages/chord/test/state-delivery.test.ts:161-186 "unsubscribe drops queued callbacks without
// joining or aborting the running callback".
func TestSourceStateUnsubscribeDropsQueuedCallbacksWithoutAbortingRunningCallback(t *testing.T) {
	state := &SourceState[int]{}
	var received []int
	completed := false
	var stop func()
	// The upstream test awaits the hydration before it publishes; here the running callback is the update that publishes.
	stop = state.Subscribe(func(value int, _ context.Context, _ ReplicatedStateDelivery) {
		received = append(received, value)
		if value == 1 {
			state.replace(t.Context(), 2)()
			stop()
			state.replace(t.Context(), 3)()
			completed = true
		}
	})
	state.replace(t.Context(), 1)()
	if !completed || !reflect.DeepEqual(received, []int{0, 1}) {
		t.Fatalf("running callback completed=%v received=%v", completed, received)
	}
}

// A hydration callback that publishes queues the revisions on its own subscription; unsubscribing from inside the callback
// drops them (state.ts:80-86) while the callback finishes (state-delivery.test.ts:161-186).
func TestSourceStateUnsubscribeDropsDeliveriesQueuedBehindRunningHydration(t *testing.T) {
	state := &SourceState[int]{}
	var received []int
	completed := false
	state.Subscribe(func(value int, _ context.Context, _ ReplicatedStateDelivery) {
		received = append(received, value)
		state.replace(t.Context(), 1)()
		state.replace(t.Context(), 2)()
		if queued := len(state.subscribers[0].queue); queued != 2 {
			t.Errorf("queued deliveries = %d, want 2", queued)
		}
		state.unsubscribe(state.subscribers[0])
		completed = true
	})
	if !completed || !reflect.DeepEqual(received, []int{0}) {
		t.Fatalf("running callback completed=%v received=%v", completed, received)
	}
}

// blockingHydration subscribes listener on another goroutine and returns once its hydration callback is running; the
// callback stays running until release is closed. A Go callback cannot return a Promise, so a callback blocked on its
// own goroutine is the running delivery of upstream's pending Promise: the publisher's goroutine queues behind it.
func blockingHydration[T comparable](state *SourceState[T], listener func(T, context.Context, ReplicatedStateDelivery)) (release func(), done <-chan func()) {
	hydrating, gate, finished := make(chan struct{}), make(chan struct{}), make(chan func(), 1)
	go func() {
		first := true
		finished <- state.Subscribe(func(value T, ctx context.Context, delivery ReplicatedStateDelivery) {
			if first {
				first = false
				close(hydrating)
				<-gate
			}
			listener(value, ctx, delivery)
		})
	}()
	<-hydrating
	return func() { close(gate) }, finished
}

// .upstream/v0.99.2/packages/chord/test/state-delivery.test.ts:108-129 "excludes a running update from overflow and
// retains exact value/context/delivery" (state.ts:41-49: a started subscriber keeps no older pending delivery).
func TestSourceStateExcludesRunningUpdateFromOverflow(t *testing.T) {
	type delivered struct {
		value    int
		ctx      context.Context
		delivery ReplicatedStateDelivery
	}
	state := &SourceState[int]{}
	var received []delivered
	running, gate := make(chan struct{}), make(chan struct{})
	release, done := blockingHydration(state, func(value int, ctx context.Context, delivery ReplicatedStateDelivery) {
		received = append(received, delivered{value, ctx, delivery})
		if value == 1 {
			close(running)
			<-gate
		}
	})
	state.replace(context.Background(), 1)()
	release()
	<-running
	for value := 2; value < 102; value++ {
		state.replace(context.Background(), value)()
	}
	ctx := t.Context()
	state.replace(ctx, 102)()
	state.replace(context.Background(), 103)()
	close(gate)
	(<-done)()
	var values []int
	for _, delivery := range received {
		values = append(values, delivery.value)
	}
	if !reflect.DeepEqual(values, []int{0, 1, 102, 103}) {
		t.Fatalf("received = %v", values)
	}
	if received[2].ctx != ctx || received[2].delivery != (ReplicatedStateDelivery{Kind: "update", Sequence: 102}) {
		t.Fatalf("newest pending delivery = %+v", received[2])
	}
}

// .upstream/v0.99.2/packages/chord/test/state-delivery.test.ts:204-222 "observes hydration rejection, synchronous throw, and
// update rejection while continuing delivery": failures of a running hydration and of queued updates are reported in
// order, and neither the failing subscription nor a sibling misses a delivery.
func TestSourceStateReportsRunningAndQueuedFailuresWhileContinuingDelivery(t *testing.T) {
	var reports []error
	state := reportingState[int](&reports)
	var received, fast []int
	release, done := blockingHydration(state, func(value int, _ context.Context, _ ReplicatedStateDelivery) {
		received = append(received, value)
		switch value {
		case 0:
			panic(errors.New("async hydration"))
		case 1:
			panic(errors.New("sync update"))
		case 2:
			panic(errors.New("async update"))
		}
	})
	stopFast := state.Subscribe(func(value int, _ context.Context, _ ReplicatedStateDelivery) { fast = append(fast, value) })
	defer stopFast()
	for value := 1; value <= 3; value++ {
		state.replace(t.Context(), value)()
	}
	release()
	(<-done)()
	var messages []string
	for _, err := range reports {
		messages = append(messages, err.Error())
	}
	if !reflect.DeepEqual(received, []int{0, 1, 2, 3}) || !reflect.DeepEqual(fast, []int{0, 1, 2, 3}) || !reflect.DeepEqual(messages, []string{"async hydration", "sync update", "async update"}) {
		t.Fatalf("received=%v fast=%v reports=%v", received, fast, messages)
	}
}

// .upstream/v0.99.2/packages/chord/test/state-delivery.test.ts:224-240 "still observes a callback rejection after
// unsubscribe" (state.ts:52-77: close drops the queue, not the running callback's report).
func TestSourceStateReportsRunningCallbackFailureAfterUnsubscribe(t *testing.T) {
	var reports []error
	state := reportingState[int](&reports)
	failure := errors.New("stopped callback")
	var received []int
	stopped := make(chan struct{})
	release, done := blockingHydration(state, func(value int, _ context.Context, _ ReplicatedStateDelivery) {
		received = append(received, value)
		<-stopped
		panic(failure)
	})
	state.replace(t.Context(), 1)()
	release()
	// Subscribe returns its removal function only after hydration, so remove the subscription the way that function does.
	state.mu.Lock()
	subscriber := state.subscribers[0]
	state.mu.Unlock()
	state.unsubscribe(subscriber)
	close(stopped)
	(<-done)()
	if !reflect.DeepEqual(received, []int{0}) || len(reports) != 1 || !errors.Is(reports[0], failure) {
		t.Fatalf("received=%v reports=%v", received, reports)
	}
}

// .upstream/v0.99.2/packages/chord/test/state-delivery.test.ts:142-159 "treats two subscriptions of the same callback
// independently".
func TestSourceStateSubscriptionsOfTheSameCallbackAreIndependent(t *testing.T) {
	state := &SourceState[int]{}
	var received []int
	listener := func(value int, _ context.Context, _ ReplicatedStateDelivery) { received = append(received, value) }
	stopFirst := state.Subscribe(listener)
	stopSecond := state.Subscribe(listener)
	stopFirst()
	stopFirst()
	state.replace(t.Context(), 1)()
	if !reflect.DeepEqual(received, []int{0, 0, 1}) {
		t.Fatalf("received = %v", received)
	}
	stopSecond()
}

// .upstream/v0.99.1/packages/chord/src/services/state.ts:41-42,80-86,131-141,171-176: unsubscribing closes the subscriber, so a sibling
// removed by an earlier listener of the same publication receives nothing from it, though the publication snapshot held it.
func TestSourceStateSiblingUnsubscribeMatchesPinnedChord(t *testing.T) {
	output, err := exec.CommandContext(t.Context(), "node", "testdata/state-listeners-oracle.mjs").Output()
	if err != nil {
		t.Fatalf("pinned Chord state probe: %v", err)
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("pinned Chord state JSON: %v\n%s", err, output)
	}
	state := &SourceState[int]{}
	var events []string
	var removeSecond func()
	state.Subscribe(func(value int, ctx context.Context, delivery ReplicatedStateDelivery) {
		if delivery.Kind != "update" {
			return
		}
		if value == 1 {
			events = append(events, "first:1")
			state.replace(ctx, 2)()
			removeSecond()
		} else {
			events = append(events, "first:2")
		}
	})
	removeSecond = state.Subscribe(func(value int, _ context.Context, delivery ReplicatedStateDelivery) {
		if delivery.Kind == "update" {
			if value == 1 {
				events = append(events, "second:1")
			} else {
				events = append(events, "second:2")
			}
		}
	})
	state.replace(t.Context(), 1)()
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("listener snapshot differs: Go %v; Chord %v", events, want)
	}
}

func TestSourceStateHydrationSkipsAlreadyCommittedPublications(t *testing.T) {
	state := &SourceState[int]{}
	state.replace(t.Context(), 1)
	state.replace(t.Context(), 2)
	var values []int
	stop := state.Subscribe(func(value int, _ context.Context, _ ReplicatedStateDelivery) { values = append(values, value) })
	defer stop()
	state.deliver()
	state.replace(t.Context(), 3)()
	if !reflect.DeepEqual(values, []int{2, 3}) {
		t.Fatalf("subscriber replayed state older than its hydration: %v", values)
	}
}

func capturePanic(call func()) (failure any) {
	defer func() { failure = recover() }()
	call()
	return nil
}

func TestConnectionStateJSON(t *testing.T) {
	cases := []struct {
		value any
		want  string
	}{
		{ServerConnectionState{Status: "connecting", Attempt: 2}, `{"status":"connecting","attempt":2}`},
		{ServerConnectionState{Status: "connected", Since: "now"}, `{"status":"connected","since":"now"}`},
		{ServerConnectionState{Status: "disconnected", Since: "now", Reason: "closed"}, `{"status":"disconnected","since":"now","reason":"closed","retryAt":null}`},
		{SessionAttachmentState{Status: "detached"}, `{"status":"detached"}`},
		{SessionAttachmentState{Status: "attaching", SessionID: "s"}, `{"status":"attaching","sessionId":"s"}`},
		{SessionAttachmentState{Status: "attached", SessionID: "s"}, `{"status":"attached","sessionId":"s"}`},
		{SessionAttachmentState{Status: "attached", SessionID: ""}, `{"status":"attached","sessionId":""}`},
		{SessionAttachmentState{Status: "degraded", SessionID: "s"}, `{"status":"degraded","sessionId":"s"}`},
	}
	for _, tt := range cases {
		got, err := json.Marshal(tt.value)
		if err != nil || string(got) != tt.want {
			t.Fatalf("state = %s, %v; want %s", got, err, tt.want)
		}
	}
}
