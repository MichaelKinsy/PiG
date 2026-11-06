package extensionconformance

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// deepModelStream is a model stream whose partial message holds a tool call with arguments nested depth levels deep: the host sends each event to the extension as a
// model_stream_event notification, which carries the partial message and so the arguments.
func deepModelStream(provider, model string, depth int) (*ai.AssistantMessageEventStream, error) {
	var nested any = 1
	for range depth {
		nested = []any{nested}
	}
	call := ai.ToolCall{ID: "deep-1", Name: "deep", Arguments: ai.JsonObject{"zeta": 1, "meta": map[string]any{"d": nested}}}
	partial := &ai.AssistantMessage{API: ai.API("faux"), Provider: provider, Model: model, Content: []ai.AssistantContentBlock{call}, StopReason: ai.StopReasonPending}
	terminal := &ai.AssistantMessage{API: ai.API("faux"), Provider: provider, Model: model, Content: []ai.AssistantContentBlock{call}, StopReason: ai.StopReasonStop}
	stream := ai.NewAssistantMessageEventStream()
	for _, event := range []ai.AssistantMessageEvent{
		ai.StartEvent{Partial: partial},
		ai.ToolCallStartEvent{ContentIndex: 0, Partial: partial},
		ai.ToolCallEndEvent{ContentIndex: 0, ToolCall: call, Partial: partial},
		ai.DoneEvent{Reason: ai.StopReasonStop, Message: terminal},
	} {
		if err := stream.Push(event); err != nil {
			return nil, err
		}
	}
	return stream, nil
}

// A model_stream_event whose partial message holds tool-call arguments nested thousands of levels deep (up to what the host's own marshal sends) reaches every SDK: the
// stream completes and the same extension answers the next stream. A notification the host cannot send because it nests past the SDKs' readers is covered by the SDK suites
// (Rust surrogate_tests.rs, Python tests/test_deep_frames.py, Lisp json-tests.lisp, Go extensions/sdk).
func TestModelStreamEventWithDeepToolArgumentsReachesEverySDK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping model stream conformance in short mode (builds subprocess fixtures)")
	}
	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			h.bridge.SetHostAction("streamModel", func(callCtx context.Context, model map[string]any, _ map[string]any) (*ai.AssistantMessageEventStream, error) {
				provider, _ := model["provider"].(string)
				modelID, _ := model["modelId"].(string)
				// The probe's callbacks are the extension's own: the production host's request calls them, as in TestModelStreamCallbacksSDKsMatch.
				request := extension.ModelStreamRequestFromContext(callCtx)
				if request.OnPayload != nil {
					_, _ = request.OnPayload(map[string]any{"original": true}, nil)
				}
				if request.OnResponse != nil {
					_ = request.OnResponse(callCtx, ai.ProviderResponse{Status: 201, Headers: map[string]string{"x-upstream": "seen"}}, nil)
				}
				if request.TransformHeaders != nil {
					one := "1"
					_, _ = request.TransformHeaders(callCtx, ai.ProviderHeaders{"x-in": &one})
				}
				return deepModelStream(provider, modelID, 9000)
			})
			command, ok := findCommand(h.runner, "model-stream-callback-probe")
			if !ok {
				t.Fatal("model-stream-callback-probe command not registered")
			}
			for round := 1; round <= 2; round++ {
				h.ui.ClearRecorded()
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				err := command.Handler(ctx, "")
				cancel()
				if err != nil {
					t.Fatalf("round %d: %v", round, err)
				}
				waitFor(t, func() bool { return slices.Contains(h.ui.Recorded(), "model-callbacks=ok:info") })
			}
		})
	}
}
