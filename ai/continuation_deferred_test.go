package ai

import (
	"context"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// deferredRecord is one streamed record of a random script: the provider's unobservable awaits before it, how the provider reaches the record's push, and the consumer's work on the record's event.
type deferredRecord struct {
	// awaits are the decoder's awaits of already resolved promises between the previous push and this one.
	awaits int
	// reach is the provider's first observable step after them: 0 its event await (awaitProviderEvent), 1 an explicit settle, 2 a read that waits for an I/O completion, 3 an await of a JavaScript value, 4 a provider-event observer that asks whether it is wanted (awaitResolvedUnless).
	reach int
	// consumer are the consumer's steps on the record's event: 0 Yield, 1 AwaitTick, 2 an extension handler that awaits inside a callback (AwaitContinuation), 3 a timer it arms (an I/O completion), 4 abort, 5 a handler for provider events that it subscribes or unsubscribes.
	consumer []int
}

func newDeferredScript(seed uint64) (records []deferredRecord, tail int) {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	records = make([]deferredRecord, 1+rng.IntN(16))
	for index := range records {
		record := &records[index]
		record.awaits = rng.IntN(8)
		record.reach = rng.IntN(5)
		for range rng.IntN(4) {
			record.consumer = append(record.consumer, rng.IntN(6))
		}
	}
	return records, rng.IntN(4)
}

// deferredSchedule runs a random stream and returns what a Pi program observes of it: each provider and consumer step in order, "·" for each microtask, "ext" for each I/O completion and "idle" for each drain that ran out of work (the point the tick oracles wait for). The provider makes each record's decoder awaits with deferContinuation when deferred is set and with suspendContinuation otherwise; switchEveryPause turns settle off. It also returns how many times a coroutine switched to its driver.
func deferredSchedule(t *testing.T, seed uint64, deferred, switchEveryPause bool) (log []string, pauses int) {
	records, tail := newDeferredScript(seed)
	ctx, executor := withContinuationExecutor(t.Context())
	add := func(step string) { log = append(log, step) }
	// A pause runs after its body released execution, so two may run at once.
	var paused atomic.Int64
	executor.trace = &executorTrace{microtask: func() { add("·") }, external: func() { add("ext") }, idle: func() { add("idle") }, pause: func() { paused.Add(1) }}
	executor.switchEveryPause = switchEveryPause
	abortCtx, abort := context.WithCancel(context.Background())
	defer abort()
	aborted, wanted := false, false
	disarm := executor.watch(abortCtx, func() {
		add("abort")
		aborted = true
	})
	defer disarm()
	stream := NewAssistantMessageEventStream()
	stream.executor = executor
	push := func(event AssistantMessageEvent) {
		if err := stream.Push(event); err != nil {
			t.Error(err)
		}
	}
	unobservable := func(turn *continuationTurn) {
		if deferred {
			deferContinuation(turn)
		} else {
			suspendContinuation(turn)
		}
	}

	// The consumer's turn is reserved before the provider's I/O grant is queued, so which goroutine starts first does not change the order.
	consumer := &StreamContinuation{turn: executor.newTurn()}
	provider := executor.newDeferredTurn()
	executor.postExternal(provider.grant)
	produced := make(chan struct{})
	go func() {
		defer close(produced)
		provider.run(func(turn *continuationTurn) {
			add("provider start")
			partial := testAssistant(StopReasonPending)
			push(StartEvent{Partial: partial})
			for index, record := range records {
				for range record.awaits {
					unobservable(turn)
				}
				switch record.reach {
				case 0:
					suspendContinuation(turn)
				case 1:
					turn.settleDeferred()
				case 2:
					// A body read: the awaits owed come before the read is issued.
					turn.settleDeferred()
					read := newContinuationPromise[int](executor)
					executor.postExternal(func() {
						add("io " + strconv.Itoa(index))
						read.resolve(index)
					})
					add(fmt.Sprint("provider read ", awaitContinuation(turn, read)))
				case 3:
					value, err := jsAwait(turn, jsValue(index))
					add(fmt.Sprint("provider js ", value, err))
				case 4:
					if turn.awaitResolvedUnless(func() bool { return wanted }) {
						add("provider observer " + strconv.Itoa(index))
						suspendContinuation(turn)
					}
				}
				if aborted {
					add("provider aborted")
					break
				}
				add("provider push " + strconv.Itoa(index))
				push(TextDeltaEvent{Delta: strconv.Itoa(index), Partial: partial})
			}
			add("provider done")
			push(DoneEvent{Reason: StopReasonStop, Message: testAssistant(StopReasonStop)})
			// Awaits after the last observable step are made before the body returns.
			for range tail {
				unobservable(turn)
			}
		})
	}()

	err := consumer.Run(func(observation *StreamObservation) error {
		add("consumer begin")
		deltas := 0
		for event := range stream.Events(observation.Context(ctx)) {
			add("consumer " + string(event.EventType()))
			if event.EventType() != EventTextDelta {
				continue
			}
			for _, step := range records[deltas].consumer {
				switch step {
				case 0:
					observation.Yield()
					add("consumer yielded")
				case 1:
					_ = observation.AwaitTick(func() error { return nil })
					add("consumer tick")
				case 2:
					callback := observation.PrepareContinuation()
					_ = observation.AwaitContinuation(callback, func() error {
						returned := make(chan struct{})
						go func() {
							defer close(returned)
							_ = callback.RunOwned(CurrentStreamOwner(), func(inner *StreamObservation) error {
								add("handler")
								inner.Yield()
								add("handler yielded")
								return nil
							})
						}()
						<-returned
						return nil
					})
					add("consumer awaited handler")
				case 3:
					executor.postExternal(func() { add("timer") })
					add("consumer armed timer")
				case 4:
					abort()
					add("consumer aborted")
				case 5:
					wanted = !wanted
					add(fmt.Sprint("consumer wants provider events ", wanted))
				}
			}
			deltas++
		}
		result, err := stream.ResultContext(ctx)
		add(fmt.Sprint("consumer result ", result.StopReason, err))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	<-produced
	// Once both bodies returned, whether the last drain to end reports idle depends on which goroutine returns last.
	for len(log) > 0 && log[len(log)-1] == "idle" {
		log = log[:len(log)-1]
	}
	return log, int(paused.Load())
}

// A body's deferred awaits are made where its next observable step is, in a row: each costs the trip through the queue that awaiting would have cost, and every other reaction, microtask, I/O completion and idle point lands where it would have. A check the body leaves for after them (awaitResolvedUnless) reads the state at the body's place. Settling a pause on the pausing coroutine's own stack runs the drain its driver would have run, in its order. Random streams interleave the decoder awaits with consumer yields, nextTick waits, an extension handler that awaits, a timer, an abort mid-record, a provider-event subscription that comes and goes, and I/O completions; every mode must produce the schedule of real awaits that each switch to the driver.
func TestDeferredAwaitsKeepTheSchedule(t *testing.T) {
	var basePauses, settledPauses, deferredPauses, failures int
	for seed := range uint64(300) {
		want, pauses := deferredSchedule(t, seed, false, true)
		if again, _ := deferredSchedule(t, seed, false, true); !reflect.DeepEqual(again, want) {
			t.Fatalf("seed %d: the reference schedule is not deterministic, %s", seed, scheduleDifference(again, want))
		}
		basePauses += pauses
		for _, mode := range []struct {
			name                       string
			deferred, switchEveryPause bool
		}{{"settled", false, false}, {"deferred", true, true}, {"deferred and settled", true, false}} {
			got, pauses := deferredSchedule(t, seed, mode.deferred, mode.switchEveryPause)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("seed %d, %s: schedules differ %s", seed, mode.name, scheduleDifference(got, want))
				if failures++; failures == 8 {
					t.FailNow()
				}
			}
			switch mode.name {
			case "settled":
				settledPauses += pauses
			case "deferred and settled":
				deferredPauses += pauses
			}
		}
	}
	t.Logf("coroutine pauses over 300 scripts: %d switching at every await, %d settled, %d deferred and settled", basePauses, settledPauses, deferredPauses)
	if settledPauses >= basePauses || deferredPauses >= settledPauses {
		t.Fatalf("pauses: %d switching, %d settled, %d deferred and settled; want each fewer than the one before", basePauses, settledPauses, deferredPauses)
	}
}

// scheduleDifference shows where got first departs from want, with the steps around it.
func scheduleDifference(got, want []string) string {
	index := 0
	for index < len(got) && index < len(want) && got[index] == want[index] {
		index++
	}
	from := max(0, index-30)
	window := func(log []string) string { return strings.Join(log[from:min(len(log), index+12)], " ") }
	return fmt.Sprintf("at step %d of %d (want %d):\ngot  ...%s\nwant ...%s", index, len(got), len(want), window(got), window(want))
}

// An await whose turn would be the next one resumed anyway does not switch: the promise reactions ahead of its grant run on the pausing body's stack, and owed awaits with nothing queued go on at once. The order is the one the driver makes.
func TestSettleResumesWithoutASwitch(t *testing.T) {
	want := []string{"·", "·", "first 1", "·", "awaited 2", "deferred", "·", "·", "·", "·", "settled"}
	for _, switchEveryPause := range []bool{true, false} {
		_, executor := withContinuationExecutor(t.Context())
		var log []string
		pauses := 0
		executor.trace = &executorTrace{microtask: func() { log = append(log, "·") }, pause: func() { pauses++ }}
		executor.switchEveryPause = switchEveryPause
		executor.run(func(turn *continuationTurn) {
			first := newContinuationPromise[int](executor)
			second := newContinuationPromise[int](executor)
			first.onResolved(func(value int) {
				log = append(log, "first "+strconv.Itoa(value))
				second.resolve(value + 1)
			})
			first.resolve(1)
			log = append(log, fmt.Sprint("awaited ", awaitContinuation(turn, second)))
			for range 3 {
				deferContinuation(turn)
			}
			log = append(log, "deferred")
			suspendContinuation(turn)
			log = append(log, "settled")
		})
		if !reflect.DeepEqual(log, want) {
			t.Fatalf("switchEveryPause %v: log %q, want %q", switchEveryPause, log, want)
		}
		if wantPauses := map[bool]int{true: 1, false: 0}[switchEveryPause]; pauses != wantPauses {
			t.Fatalf("switchEveryPause %v: %d coroutine pauses, want %d", switchEveryPause, pauses, wantPauses)
		}
	}
}
