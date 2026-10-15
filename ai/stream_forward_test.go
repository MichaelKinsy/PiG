package ai

import (
	"context"
	"reflect"
	"strconv"
	"sync"
	"testing"
)

// forwardEventsByTurn is the forwarding loop forwardEvents replaces: the turn's goroutine resumes at every await, pushes, and parks again.
func forwardEventsByTurn(turn *continuationTurn, ctx context.Context, source *AssistantMessageEventStream, push func(AssistantMessageEvent) error) (*AssistantMessage, error) {
	for event := range source.events(ctx, false) {
		if err := push(event); err != nil {
			return nil, err
		}
	}
	return awaitContinuation(turn, source.resultContinuation(turn.executor)), nil
}

type forwardScenario struct {
	events int
	// hops[i] is the number of reactions the producer awaits after its i-th push.
	hops []int
	// sourceFirst starts the producer's turn before the forwarder's, so events are buffered when the forwarder starts.
	sourceFirst bool
	// terminalFirst lets the producer finish before the forwarder starts.
	terminalFirst bool
}

// forwardTrace runs a producer, a forwarder (forward) and a consumer of the forwarded stream as executor turns and returns every push and delivery with the microtask it ran in.
func forwardTrace(t *testing.T, scenario forwardScenario, forward func(*continuationTurn, context.Context, *AssistantMessageEventStream, func(AssistantMessageEvent) error) (*AssistantMessage, error)) []string {
	t.Helper()
	executor := &continuationExecutor{}
	ctx := context.WithValue(context.WithoutCancel(t.Context()), continuationExecutorKey{}, executor)
	source := NewAssistantMessageEventStream()
	source.executor = executor
	outer := NewAssistantMessageEventStream()
	outer.executor = executor
	var mu sync.Mutex
	var log []string
	microtask := 0
	record := func(entry string) {
		mu.Lock()
		defer mu.Unlock()
		log = append(log, strconv.Itoa(microtask)+" "+entry)
	}
	executor.trace = &executorTrace{
		microtask: func() {
			mu.Lock()
			microtask++
			mu.Unlock()
		},
		push: func(stream *AssistantMessageEventStream, event AssistantMessageEvent) {
			layer := "forwarded"
			if stream == source {
				layer = "produced"
			}
			record(layer + " " + string(event.EventType()))
		},
	}
	partial := lazyProbeMessage(StopReasonPending)
	produce := func(turn *continuationTurn) {
		for i := range scenario.events {
			_ = source.Push(TextDeltaEvent{ContentIndex: 0, Delta: "d" + strconv.Itoa(i), Partial: partial})
			hops := 1
			if len(scenario.hops) > 0 {
				hops = scenario.hops[i%len(scenario.hops)]
			}
			for range hops {
				awaitSettled(turn)
			}
		}
		_ = source.Push(DoneEvent{Reason: StopReasonStop, Message: lazyProbeMessage(StopReasonStop)})
		record("producer returned")
	}
	forwarder := func(turn *continuationTurn) {
		observation := context.WithValue(ctx, continuationTurnKey{}, turn)
		result, err := forward(turn, observation, source, outer.Push)
		if err != nil {
			t.Errorf("forward: %v", err)
			return
		}
		record("forwarder resumed with " + string(result.StopReason))
		outer.End(result)
	}
	consume := func(turn *continuationTurn) {
		observation := context.WithValue(ctx, continuationTurnKey{}, turn)
		for event := range outer.events(observation, false) {
			record("consumed " + string(event.EventType()))
		}
		result := awaitContinuation(turn, outer.resultContinuation(executor))
		record("consumer result " + string(result.StopReason))
	}
	var joined sync.WaitGroup
	start := func(bodies ...func(*continuationTurn)) {
		for _, body := range bodies {
			turn := executor.newTurn()
			joined.Go(func() { turn.run(body) })
		}
	}
	executor.run(func(*continuationTurn) {
		switch {
		case scenario.terminalFirst:
			start(func(turn *continuationTurn) {
				produce(turn)
				// The forwarder starts after the source is terminal.
				start(forwarder, consume)
			})
		case scenario.sourceFirst:
			start(produce, forwarder, consume)
		default:
			start(forwarder, produce, consume)
		}
	})
	joined.Wait()
	<-outer.done
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), log...)
}

// forwardEvents runs each loop step as the reaction that would resume the forwarding turn, so its pushes, deliveries, the turn's resumption with the source result and the consumer's view land in the same microtasks, in the same order, as the turn-per-await loop in every interleaving.
func TestForwardEventsMatchesTheTurnLoopReactionForReaction(t *testing.T) {
	scenarios := map[string]forwardScenario{
		"live one hop":         {events: 4},
		"live no hop":          {events: 4, hops: []int{0}},
		"live mixed hops":      {events: 7, hops: []int{0, 1, 3, 2}},
		"buffered":             {events: 5, hops: []int{0}, sourceFirst: true},
		"buffered mixed hops":  {events: 6, hops: []int{2, 0, 1}, sourceFirst: true},
		"no events":            {events: 0},
		"source done first":    {events: 3, hops: []int{0}, terminalFirst: true},
		"single slow producer": {events: 2, hops: []int{5}},
	}
	for name, scenario := range scenarios {
		t.Run(name, func(t *testing.T) {
			want := forwardTrace(t, scenario, forwardEventsByTurn)
			got := forwardTrace(t, scenario, forwardEvents)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("forwardEvents trace\n%v\nwant (turn loop)\n%v", got, want)
			}
			if len(want) < scenario.events*3+3 {
				t.Fatalf("trace too short to compare: %v", want)
			}
		})
	}
}
