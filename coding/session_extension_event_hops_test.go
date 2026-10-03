package coding

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi 0.99.1 AgentSession._handleAgentEvent awaits _emitExtensionEvent (agent-session.ts:1098), which awaits runner.emit (:1265 for message_start), so public listeners see
// message_start two microtask rounds after the Agent emits it. The oracle is real 0.99.1: createAgentSession with a stream function whose producer
// pushes `start` synchronously and then awaits `null` once per round logs
//
//	push start | tick1 | tick2 | tick3 | tick4 | tick5 | LISTENER message_start | tick6 ...
//
// A subscriber that runs one round early sees a provider one step behind; the RPC records of Bedrock and pi-messages then differ from Pi's.
func TestSessionListenerSeesMessageStartAfterTwoExtensionEventRounds(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	h := newRecoveryHarness(t, harnessOptions{})
	var mu sync.Mutex
	var log []string
	record := func(entry string) {
		mu.Lock()
		defer mu.Unlock()
		log = append(log, entry)
	}
	h.session.Agent().SetStreamFunction(func(runContext context.Context, model *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		stream := ai.NewAssistantMessageEventStream()
		partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{}, API: "pi-messages", Provider: "p", Model: model.ID, StopReason: ai.StopReasonPending, Timestamp: 1}
		if err := stream.Push(ai.StartEvent{Partial: partial}); err != nil {
			return nil, err
		}
		record("push start")
		// The reserved continuation is the first reaction after this synchronous prefix, as the first `await null` of the producer.
		producer := ai.StreamObservationFromContext(runContext).PrepareContinuation()
		go func() {
			_ = producer.Run(func(observation *ai.StreamObservation) error {
				for i := 1; i <= 12; i++ {
					record("tick" + string(rune('0'+i/10)) + string(rune('0'+i%10)))
					observation.Yield()
				}
				done := *partial
				done.StopReason = ai.StopReasonStop
				return stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: &done})
			})
		}()
		return stream, nil
	})
	unsubscribe := h.session.Subscribe(func(event agent.AgentEvent) {
		if start, ok := event.(agent.MessageStartEvent); ok && start.Message.Assistant != nil {
			record("LISTENER message_start")
		}
	})
	defer unsubscribe()
	if _, err := h.session.Send(ctx, "probe"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	listener := slices.Index(log, "LISTENER message_start")
	if listener < 0 {
		t.Fatalf("no assistant message_start reached the listener: %v", log)
	}
	if want := slices.Index(log, "tick06") - 1; listener != want {
		t.Fatalf("listener ran at log position %d, want %d (after tick05, before tick06): %v", listener, want, log)
	}
}

// The same two Session reactions follow message_update and message_end, on top of the Agent's own awaits (including 0.99.1's `await result()` wrapper, agent-loop.ts:409 and :445). The oracle is a real 0.99.1 createAgentSession whose producer runs `await null; log(tick)` per round, pushes text_start after tick08 and done after tick12, then keeps ticking:
//
//	push start | tick01 .. tick05 | LISTENER message_start | tick06 | tick07 | tick08 | push text_start | tick09 .. tick12 | push done | LISTENER message_update | tick13 .. tick20 | LISTENER message_end | tick21 .. tick24
func TestSessionListenerSeesEveryAssistantLifecycleEventAtPiReactions(t *testing.T) {
	want := []string{"push start", "tick01", "tick02", "tick03", "tick04", "tick05", "LISTENER message_start", "tick06", "tick07", "tick08", "push text_start", "tick09", "tick10", "tick11", "tick12", "push done", "LISTENER message_update", "tick13", "tick14", "tick15", "tick16", "tick17", "tick18", "tick19", "tick20", "LISTENER message_end", "tick21", "tick22", "tick23", "tick24"}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	h := newRecoveryHarness(t, harnessOptions{})
	var mu sync.Mutex
	var log []string
	record := func(entry string) {
		mu.Lock()
		defer mu.Unlock()
		log = append(log, entry)
	}
	produced := make(chan struct{})
	h.session.Agent().SetStreamFunction(func(runContext context.Context, model *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		stream := ai.NewAssistantMessageEventStream()
		partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{}, API: "pi-messages", Provider: "p", Model: model.ID, StopReason: ai.StopReasonPending, Timestamp: 1}
		if err := stream.Push(ai.StartEvent{Partial: partial}); err != nil {
			return nil, err
		}
		record("push start")
		producer := ai.StreamObservationFromContext(runContext).PrepareContinuation()
		go func() {
			defer close(produced)
			_ = producer.Run(func(observation *ai.StreamObservation) error {
				for i := 1; i <= 12; i++ {
					if i > 1 {
						observation.Yield()
					}
					record(fmt.Sprintf("tick%02d", i))
					if i == 8 {
						partial.Content = append(partial.Content, ai.TextContent{Text: "a"})
						if err := stream.Push(ai.TextStartEvent{ContentIndex: 0, Partial: partial}); err != nil {
							return err
						}
						record("push text_start")
					}
				}
				done := *partial
				done.StopReason = ai.StopReasonStop
				err := stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: &done})
				record("push done")
				for i := 13; i <= 24; i++ {
					observation.Yield()
					record(fmt.Sprintf("tick%d", i))
				}
				return err
			})
		}()
		return stream, nil
	})
	unsubscribe := h.session.Subscribe(func(event agent.AgentEvent) {
		switch event := event.(type) {
		case agent.MessageStartEvent:
			if event.Message.Assistant != nil {
				record("LISTENER message_start")
			}
		case agent.MessageUpdateEvent:
			record("LISTENER message_update")
		case agent.MessageEndEvent:
			if event.Message.Assistant != nil {
				record("LISTENER message_end")
			}
		}
	})
	defer unsubscribe()
	if _, err := h.session.Send(ctx, "probe"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-produced:
	case <-ctx.Done():
		t.Fatal("producer did not finish")
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(log, want) {
		t.Fatalf("Session listener order differs from Pi 0.99.2\n got: %s\nwant: %s", strings.Join(log, " | "), strings.Join(want, " | "))
	}
}
