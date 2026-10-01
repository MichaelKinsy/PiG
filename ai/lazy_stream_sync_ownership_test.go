package ai

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"
)

// lazyStreamPushOrder creates a synchronous lazy stream over a producer whose whole response is already buffered (every step is a ready reaction, so it never waits on I/O) and returns the order in which the producer and the forwarding layer push events. owned runs the call inside the executor's running turn, as an agent turn does; otherwise a plain goroutine calls it, as ModelRuntime's setup goroutine does.
func lazyStreamPushOrder(t *testing.T, owned bool) []string {
	t.Helper()
	executor := &continuationExecutor{}
	ctx := context.WithValue(t.Context(), continuationExecutorKey{}, executor)
	var (
		mu    sync.Mutex
		order []string
		inner *AssistantMessageEventStream
		outer *AssistantMessageEventStream
	)
	executor.trace = &executorTrace{push: func(stream *AssistantMessageEventStream, event AssistantMessageEvent) {
		mu.Lock()
		defer mu.Unlock()
		layer := "forwarder"
		if stream == inner {
			layer = "producer"
		}
		order = append(order, layer+":"+string(event.EventType()))
	}}
	var joined sync.WaitGroup
	launch := func(body func()) { joined.Go(body) }
	model := &Model{ID: "probe-model", ProviderMeta: ProviderMetadata{API: "probe-api", ProviderID: "probe"}}
	create := func(ctx context.Context) {
		stream := startLazyStreamSync(ctx, model, func(context.Context) (*AssistantMessageEventStream, error) {
			source := NewAssistantMessageEventStream()
			source.executor = executor
			mu.Lock()
			inner = source
			mu.Unlock()
			partial := lazyProbeMessage(StopReasonPending)
			steps := 0
			var step func()
			step = func() {
				if steps == 4 {
					_ = source.Push(DoneEvent{Reason: StopReasonStop, Message: lazyProbeMessage(StopReasonStop)})
					source.End()
					return
				}
				steps++
				_ = source.Push(TextDeltaEvent{ContentIndex: 0, Delta: "d" + strconv.Itoa(steps), Partial: partial})
				executor.post(step)
			}
			executor.post(step)
			return source, nil
		}, launch)
		mu.Lock()
		outer = stream
		mu.Unlock()
	}
	if owned {
		executor.run(func(turn *continuationTurn) { create(context.WithValue(ctx, continuationTurnKey{}, turn)) })
	} else {
		create(ctx)
	}
	joined.Wait()
	mu.Lock()
	done := outer.done
	mu.Unlock()
	<-done
	mu.Lock()
	defer mu.Unlock()
	return slices.Clone(order)
}

// lazy.ts:48-50 queues `setup().then(forwardStream)` while the caller's synchronous prefix still runs, so a producer's already-queued reactions and the forwarding reaction keep one FIFO order. A caller that does not own the queue must reserve that reaction before its setup turn releases execution; posting it afterwards lets a buffered producer finish its whole response before the forwarder registers, and a consumer then sees the final message at its first event.
func TestLazyStreamSyncForwarderOrderDoesNotDependOnCallerOwnership(t *testing.T) {
	want := lazyStreamPushOrder(t, true)
	if len(want) == 0 || want[0] != "producer:text_delta" {
		t.Fatalf("owned reference order = %v", want)
	}
	for run := range 200 {
		if got := lazyStreamPushOrder(t, false); !slices.Equal(got, want) {
			t.Fatalf("run %d: caller without the queue pushes in a different order\n got: %v\nwant: %v", run, got, want)
		}
	}
}

// ModelRuntime creates its provider stream and forwards it in one stream continuation. ForwardStream must forward in the turn its caller owns: acquiring a second turn while the caller holds the queue never runs, and a caller that created the source before acquiring one lets a buffered source finish first.
func TestForwardStreamInOwnedTurnRegistersBeforeSourceReactionsRun(t *testing.T) {
	ctx := WithStreamContinuations(t.Context())
	_, executor := withContinuationExecutor(ctx)
	var (
		mu    sync.Mutex
		order []string
		inner *AssistantMessageEventStream
	)
	executor.trace = &executorTrace{push: func(stream *AssistantMessageEventStream, event AssistantMessageEvent) {
		mu.Lock()
		defer mu.Unlock()
		layer := "forwarder"
		if stream == inner {
			layer = "producer"
		}
		order = append(order, layer+":"+string(event.EventType()))
	}}
	outer := NewAssistantMessageEventStream()
	finished := make(chan error, 1)
	go func() {
		finished <- RunStreamContinuation(ctx, func(observation *StreamObservation) error {
			ctx := outer.ObservationContext(observation.Context(ctx))
			source := NewAssistantMessageEventStream()
			source.executor = executor
			mu.Lock()
			inner = source
			mu.Unlock()
			partial := lazyProbeMessage(StopReasonPending)
			steps := 0
			var step func()
			step = func() {
				if steps == 4 {
					_ = source.Push(DoneEvent{Reason: StopReasonStop, Message: lazyProbeMessage(StopReasonStop)})
					source.End()
					return
				}
				steps++
				_ = source.Push(TextDeltaEvent{ContentIndex: 0, Delta: "d" + strconv.Itoa(steps), Partial: partial})
				executor.post(step)
			}
			executor.post(step)
			return outer.ForwardStream(ctx, source)
		})
	}()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ForwardStream called from the caller's owned turn never returned: it waits for a second turn that the caller's own turn blocks")
	}
	mu.Lock()
	defer mu.Unlock()
	forwarded, finishedAt := slices.Index(order, "forwarder:text_delta"), slices.Index(order, "producer:done")
	if forwarded < 0 || finishedAt < 0 || forwarded > finishedAt {
		t.Fatalf("the forwarder read its first event after the producer finished its response: %v", order)
	}
}
