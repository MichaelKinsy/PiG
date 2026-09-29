package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

type synchronousPrefixProvider struct {
	ai.Provider
	called *bool
	stream *ai.AssistantMessageEventStream
}

func (provider synchronousPrefixProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	*provider.called = true
	return provider.stream, nil
}

// provider-composer.ts:492-508 wraps streamWith's setup in lazyStream, and that setup has no await. The provider call is therefore part of the caller's synchronous prefix and has run when the stream returns (lazy.ts:48).
func TestComposedStreamProviderCallsProviderBeforeReturning(t *testing.T) {
	called := false
	source := ai.NewAssistantMessageEventStream()
	message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{}, StopReason: ai.StopReasonStop}
	provider := &composedStreamProvider{Provider: synchronousPrefixProvider{called: &called, stream: source}, model: &ai.Model{ID: "probe"}}
	stream, err := provider.Stream(t.Context(), ai.TranscriptContext{}, ai.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("provider stream call ran after lazyStream returned; Pi runs it in the synchronous prefix")
	}
	if err := source.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}); err != nil {
		t.Fatal(err)
	}
	source.End()
	if got := stream.Result(); got != message {
		t.Fatalf("forwarded result = %p, want the source terminal message %p", got, message)
	}
}
