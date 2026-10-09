package ai

import (
	"context"
	"testing"
)

func recordingProviderStreams(label string, calls *[]string) *ProviderStreams {
	respond := func(_ context.Context, model *Model, _ TranscriptContext, _ StreamOptions) (*AssistantMessageEventStream, error) {
		if calls != nil {
			*calls = append(*calls, label+":"+model.ID)
		}
		message := &AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "ok"}}, StopReason: StopReasonStop}
		stream := NewAssistantMessageEventStream()
		if err := stream.Push(StartEvent{Partial: message}); err != nil {
			return nil, err
		}
		if err := stream.Push(DoneEvent{Reason: StopReasonStop, Message: message}); err != nil {
			return nil, err
		}
		return stream, nil
	}
	return &ProviderStreams{Stream: respond, StreamSimple: respond}
}

// .upstream/v0.87.1/packages/ai/test/providers.test.ts:517
// packages/ai/src/types.ts:295-300 (fetchDeferred, cancelDeferred).
func TestLazyAPIOnlyDeclaredDeferredCapabilitiesUpstream(t *testing.T) {
	loads := 0
	streams := recordingProviderStreams("deferred", nil)
	streams.FetchDeferred = func(ctx context.Context, model *Model, _ DeferredHandle, _ DeferredFetchOptions) (*AssistantMessageEventStream, error) {
		return streams.StreamSimple(ctx, model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{})
	}
	api := LazyAPI(func(context.Context) (*ProviderStreams, error) { loads++; return streams, nil }, LazyAPICapabilities{FetchDeferred: true})
	model := &Model{ID: "model-a", ProviderMeta: ProviderMetadata{API: "api-a", ProviderID: "mixed"}}
	if loads != 0 || api.CancelDeferred != nil || api.FetchDeferred == nil {
		t.Fatal("lazy capabilities/loading changed")
	}
	stream, err := api.FetchDeferred(t.Context(), model, DeferredHandle{Provider: "mixed", ModelID: "model-a", API: "api-a", ID: "response-1"}, DeferredFetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if stream.Result().StopReason != StopReasonStop || loads != 1 {
		t.Fatalf("result=%v loads=%d", stream.Result(), loads)
	}
}
