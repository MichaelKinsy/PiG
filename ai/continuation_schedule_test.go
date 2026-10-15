package ai

import (
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"testing"
)

// executorSchedule runs one fixed program through every kind of turn and await the executor has, and returns the order its steps ran in: "·" marks each microtask the executor ran and "ext" each I/O completion. The provider is a response turn granted by an I/O completion that pushes events, awaits resolved promises, an I/O completion and a JavaScript promise; the consumer is a stream continuation on another goroutine that awaits a resolved operation, a nextTick callback, a callback a third goroutine runs, and the stream's result. providerAhead holds the consumer's goroutine back until the provider has run to its first await, an order a scheduler may also pick.
func executorSchedule(t *testing.T, providerAhead bool) []string {
	ctx, executor := withContinuationExecutor(t.Context())
	var (
		logMu sync.Mutex
		log   []string
	)
	add := func(step string) {
		logMu.Lock()
		log = append(log, step)
		logMu.Unlock()
	}
	// The first microtask runs once the provider's body has paused for the first time.
	suspended := make(chan struct{})
	var suspendedOnce sync.Once
	executor.trace = &executorTrace{microtask: func() {
		add("·")
		suspendedOnce.Do(func() { close(suspended) })
	}, external: func() { add("ext") }}
	stream := NewAssistantMessageEventStream()
	stream.executor = executor

	provider := executor.newDeferredTurn()
	executor.postExternal(provider.grant)
	// The consumer's place in the queue is reserved before either goroutine starts, as a caller reserves it before it hands work to another goroutine, so the program, not the scheduler, orders the provider's first await and the consumer's start.
	consumer := &StreamContinuation{turn: executor.newTurn()}
	produced := make(chan struct{})
	go func() {
		defer close(produced)
		provider.run(func(turn *continuationTurn) {
			add("provider start")
			partial := pushTestStart(t, stream)
			for index := range 4 {
				add("provider push " + strconv.Itoa(index))
				if err := stream.Push(TextDeltaEvent{Delta: strconv.Itoa(index), Partial: partial}); err != nil {
					t.Error(err)
				}
				suspendContinuation(turn)
				switch index {
				case 1:
					settled := newContinuationPromise[int](executor)
					executor.postExternal(func() {
						add("io")
						settled.resolve(7)
					})
					add(fmt.Sprint("provider io ", awaitContinuation(turn, settled)))
				case 2:
					value, err := jsAwait(turn, jsPromiseOperand(jsPromiseResolve(executor, jsValue(3))))
					add(fmt.Sprint("provider js ", value, err))
				}
			}
			add("provider done")
			pushTestDone(t, stream)
		})
	}()

	if providerAhead {
		// The scheduler may run the provider's goroutine ahead; the schedule must not move.
		<-suspended
	}
	err := consumer.Run(func(observation *StreamObservation) error {
		add("consumer begin")
		deltas := 0
		for event := range stream.Events(observation.Context(ctx)) {
			add("consumer " + string(event.EventType()))
			if event.EventType() != EventTextDelta {
				continue
			}
			switch deltas {
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
							add("callback")
							inner.Yield()
							add("callback yielded")
							return nil
						})
					}()
					<-returned
					return nil
				})
				add("consumer awaited callback")
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
	logMu.Lock()
	defer logMu.Unlock()
	return append([]string(nil), log...)
}

// executorScheduleAtBaton is executorSchedule's order under the channel-baton executor (4cfaf24153), where every turn ran on its own goroutine and a grant woke it. Coroutine hand-offs must not move any step: the order is what a Pi program observes.
var executorScheduleAtBaton = []string{
	"ext", "provider start", "provider push 0", "·", "consumer begin", "·", "provider push 1", "·", "·", "·", "consumer start", "·", "·",
	"consumer text_delta", "·", "consumer yielded", "·", "·", "consumer text_delta", "consumer tick", "ext", "io", "·", "provider io 7",
	"provider push 2", "·", "·", "·", "·", "provider js 3 <nil>", "provider push 3", "·", "consumer text_delta", "·", "provider done", "·",
	"callback", "·", "callback yielded", "·", "consumer awaited callback", "·", "·", "consumer text_delta", "·", "·", "consumer done", "·", "·",
	"consumer result stop<nil>",
}

func TestExecutorScheduleMatchesBaton(t *testing.T) {
	for _, providerAhead := range []bool{false, true} {
		for range 20 {
			if got := executorSchedule(t, providerAhead); !reflect.DeepEqual(got, executorScheduleAtBaton) {
				t.Fatalf("providerAhead=%v schedule:\n%q\nwant (baton):\n%q", providerAhead, got, executorScheduleAtBaton)
			}
		}
	}
}
