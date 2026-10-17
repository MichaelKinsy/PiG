//go:build !pig_strip_mistral_conversations

package ai

import (
	"net/http"
	"reflect"
	"testing"
)

func TestMistralProviderStreamEventsUpstream(t *testing.T) {
	// .upstream/v0.99.1/packages/ai/test/mistral-http-transport.test.ts:377
	t.Run("mistral forwards each parsed SSE payload before normalizing it", func(t *testing.T) {
		first := `{"id":"response-1","provider_metadata":{"request":"test"},"choices":[{"index":0,"finish_reason":null,"delta":{"content":"hello"}}]}`
		second := `{"id":"response-1","choices":[{"index":0,"finish_reason":"stop","delta":{}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
		model := mustGeneratedModel(t, "mistral", "mistral-large-latest").ToModel()
		recorder := &providerEventRecorder{}
		stream, err := StreamSimple(t.Context(), model, mistralUpstreamContext(), StreamOptions{APIKey: "test", OnProviderStreamEvent: recorder.observe, Fetch: &http.Client{Transport: FetchFunction(func(*http.Request) (*http.Response, error) {
			return mistralUpstreamSSE(mistralCreateSSEBody(first, second)), nil
		})}})
		if err != nil {
			t.Fatal(err)
		}
		result := stream.Result()
		events, models := recorder.snapshot()
		if !reflect.DeepEqual(events, decodedEvents(t, first, second)) {
			t.Fatalf("events = %#v", events)
		}
		assertEventModels(t, models, model, 2)
		if result.StopReason != StopReasonStop || len(result.Content) != 1 || result.Content[0] != (TextContent{Text: "hello"}) {
			t.Fatalf("result = %#v", result)
		}
	})
}
