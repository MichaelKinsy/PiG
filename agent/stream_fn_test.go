package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// StreamFn receives every provider request with the loop's exact model,
// transcript, and options, and its stream drives the turn.
func TestStreamFnSendsEachProviderRequest(t *testing.T) {
	provider := &fakeProvider{responses: []fakeResponse{
		{toolCalls: []fakeToolCall{{id: "call", name: "echo", args: `{"v":1}`}}},
		{text: "done"},
	}}
	type request struct {
		model      *ai.Model
		transcript ai.TranscriptContext
		options    ai.StreamOptions
	}
	var requests []request
	model := &ai.Model{ID: "m", Provider: provider}
	agent := NewAgent(AgentOptions{
		Model:     model,
		Tools:     []AgentTool{echoTool{}},
		SessionID: "session",
		StreamFn: func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			requests = append(requests, request{model, transcript, options})
			return model.Provider.Stream(ctx, transcript, options)
		},
	})
	if _, err := agent.Send(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("StreamFn saw %d requests, want 2", len(requests))
	}
	for i, got := range requests {
		if got.model != model || got.options.SessionID != "session" {
			t.Fatalf("request %d = %+v", i, got)
		}
	}
	if len(requests[1].transcript.Messages()) <= len(requests[0].transcript.Messages()) {
		t.Fatalf("second request did not extend the first: %d then %d messages", len(requests[0].transcript.Messages()), len(requests[1].transcript.Messages()))
	}
	if final := agent.Messages(); len(final) == 0 || final[len(final)-1].Assistant == nil {
		t.Fatalf("turn did not complete through StreamFn: %+v", final)
	}
}

// A nil StreamFn uses the model's provider directly. Pi installs the same
// provider stream through its package-global default registry; PiG models carry
// that provider runtime explicitly instead.
func TestStreamFnDefaultsToModelProvider(t *testing.T) {
	provider := &fakeProvider{responses: []fakeResponse{{text: "default"}}}
	agent := NewAgent(AgentOptions{Model: &ai.Model{ID: "m", Provider: provider}})

	if _, err := agent.Send(t.Context(), "hi"); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	calls := provider.callIndex
	provider.mu.Unlock()
	if calls != 1 {
		t.Fatalf("provider stream calls = %d, want 1", calls)
	}
}

// MessagesSnapshot can be read from another goroutine while a run appends.
func TestMessagesSnapshotIsSafeDuringARun(t *testing.T) {
	provider := &fakeProvider{responses: []fakeResponse{
		{toolCalls: []fakeToolCall{{id: "a", name: "echo", args: `{}`}}},
		{toolCalls: []fakeToolCall{{id: "b", name: "echo", args: `{}`}}},
		{text: "done"},
	}}
	agent := NewAgent(AgentOptions{Model: &ai.Model{Provider: provider}, Tools: []AgentTool{echoTool{}}})
	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				_ = agent.MessagesSnapshot()
			}
		}
	})
	if _, err := agent.Send(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	close(stop)
	readers.Wait()
	if got, want := len(agent.MessagesSnapshot()), len(agent.Messages()); got != want {
		t.Fatalf("snapshot has %d messages, want %d", got, want)
	}
}
