package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// providerEventRecorder is the observer the upstream tests push into. The upstream callbacks yield once
// (`await Promise.resolve()`); a blocking Go call has the same ordering effect.
type providerEventRecorder struct {
	mu     sync.Mutex
	events []any
	models []*Model
}

func (r *providerEventRecorder) observe(_ context.Context, data any, model *Model) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, data)
	r.models = append(r.models, model)
	return nil
}

func (r *providerEventRecorder) snapshot() ([]any, []*Model) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]any(nil), r.events...), append([]*Model(nil), r.models...)
}

// decodedJSON is the parsed form of one wire payload: the shape an adapter hands to OnProviderStreamEvent.
func decodedJSON(t *testing.T, raw string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return value
}

func decodedEvents(t *testing.T, raws ...string) []any {
	t.Helper()
	out := make([]any, len(raws))
	for i, raw := range raws {
		out[i] = decodedJSON(t, raw)
	}
	return out
}

func sseFrames(payloads ...string) string {
	var out strings.Builder
	for _, payload := range payloads {
		fmt.Fprintf(&out, "data: %s\n\n", payload)
	}
	return out.String()
}

func serveSSE(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

// assertEventModels checks the model argument of every observed event. Adapters that keep the selected model
// (ModelMetadata) hand it back unchanged, as upstream's toEqual([model, ...]) requires.
func assertEventModels(t *testing.T, got []*Model, want *Model, count int) {
	t.Helper()
	if len(got) != count {
		t.Fatalf("observed %d models, want %d", len(got), count)
	}
	for i, model := range got {
		if !reflect.DeepEqual(model, want) {
			t.Fatalf("event %d model = %#v, want %#v", i, model, want)
		}
	}
}

// assertEventModelIdentity is assertEventModels for adapters whose Go configuration does not retain a Model value.
func assertEventModelIdentity(t *testing.T, got []*Model, want *Model, count int) {
	t.Helper()
	if len(got) != count {
		t.Fatalf("observed %d models, want %d", len(got), count)
	}
	for i, model := range got {
		if model == nil || model.ID != want.ID || model.ProviderMeta.ProviderID != want.ProviderMeta.ProviderID || model.ProviderMeta.API != want.ProviderMeta.API {
			t.Fatalf("event %d model = %#v, want %s/%s", i, model, want.ProviderMeta.ProviderID, want.ID)
		}
	}
}

func helloTranscript() TranscriptContext {
	return userTranscript("hi")
}

func userTranscript(text string) TranscriptContext {
	return NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText(text), Timestamp: 1}}})
}

// responsesUpstreamContext is the context the upstream Responses tests build: an empty system prompt, one text block, and no tools.
func responsesUpstreamContext() TranscriptContext {
	return NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserContentBlocks{TextContent{Text: "hi"}}}}, Tools: []ToolSchema{}})
}

func sseResponse(body string) *http.Client {
	return &http.Client{Transport: FetchFunction(func(r *http.Request) (*http.Response, error) {
		_, _ = io.Copy(io.Discard, r.Body)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
}

func TestProviderStreamEventsUpstream(t *testing.T) {
	// .upstream/v0.99.1/packages/ai/test/openai-completions-provider-stream-event.test.ts:60 (regression for #9784)
	t.Run("openai-completions exposes provider chunks including OpenRouter metadata", func(t *testing.T) {
		first := `{"id":"chatcmpl-1","model":"anthropic/claude-sonnet-4.6","choices":[{"index":0,"delta":{"content":"hello"}}]}`
		final := `{"id":"chatcmpl-1","model":"anthropic/claude-sonnet-4.6","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"cost":0.0012,"is_byok":false},"openrouter_metadata":{"strategy":"direct","region":"iad"}}`
		server := serveSSE(t, sseFrames(first, final, "[DONE]"))
		model := &Model{ID: "openrouter/auto", DisplayName: "OpenRouter Auto", Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 8192}, ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, ProviderID: "openrouter", BaseURL: server.URL}}
		recorder := &providerEventRecorder{}
		stream, err := StreamSimple(t.Context(), model, helloTranscript(), StreamOptions{APIKey: "test", OnProviderStreamEvent: recorder.observe})
		if err != nil {
			t.Fatal(err)
		}
		message := stream.Result()
		events, models := recorder.snapshot()
		if len(message.Content) != 1 || message.Content[0] != (TextContent{Text: "hello"}) {
			t.Fatalf("content = %#v (error %q)", message.Content, message.ErrorMessage)
		}
		if !reflect.DeepEqual(events, decodedEvents(t, first, final)) {
			t.Fatalf("events = %#v", events)
		}
		assertEventModels(t, models, model, 2)
	})

	// .upstream/v0.99.1/packages/ai/test/openai-responses-terminal-event.test.ts:293
	// The mocked SDK stream is the one at openai-responses-terminal-event.test.ts:10-30; upstream asserts the three event types.
	t.Run("openai-responses forwards parsed provider stream events in order", func(t *testing.T) {
		created := `{"type":"response.created","sequence_number":0,"response":{"id":"resp_wrapper_early_eof"}}`
		added := `{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"type":"reasoning","id":"rs_wrapper_early_eof","summary":[]}}`
		delta := `{"type":"response.reasoning_text.delta","sequence_number":2,"output_index":0,"content_index":0,"item_id":"rs_wrapper_early_eof","delta":"partial reasoning before the wrapper stream ends"}`
		model := chatGPTTestModel("https://api.openai.com/v1", nil)
		provider, err := directAPIProvider(model, "test", nil)
		if err != nil {
			t.Fatal(err)
		}
		recorder := &providerEventRecorder{}
		stream, err := provider.Stream(t.Context(), responsesUpstreamContext(), StreamOptions{OnProviderStreamEvent: recorder.observe, Fetch: sseResponse(sseFrames(created, added, delta))})
		if err != nil {
			t.Fatal(err)
		}
		stream.Result()
		events, models := recorder.snapshot()
		var types []string
		for _, event := range events {
			types = append(types, event.(map[string]any)["type"].(string))
		}
		if want := []string{"response.created", "response.output_item.added", "response.reasoning_text.delta"}; !reflect.DeepEqual(types, want) {
			t.Fatalf("types = %v", types)
		}
		assertEventModels(t, models, model, 3)
	})

	// .upstream/v0.99.1/packages/ai/test/azure-openai-base-url.test.ts:233
	t.Run("azure-openai-responses forwards parsed events in order before normalizing the response", func(t *testing.T) {
		created := `{"type":"response.created","sequence_number":0,"response":{"id":"resp_azure"}}`
		completed := `{"type":"response.completed","sequence_number":1,"response":{"id":"resp_azure","status":"completed"}}`
		server := serveSSE(t, sseFrames(created, completed))
		// Upstream passes the catalog model with azureBaseUrl; the Go model carries the local server as its base URL.
		model := mustGeneratedModel(t, "azure-openai-responses", "gpt-4o-mini").ToModel()
		model.ProviderMeta.BaseURL = server.URL
		recorder := &providerEventRecorder{}
		stream, err := StreamSimple(t.Context(), model, userTranscript("hello"), StreamOptions{APIKey: "test-api-key", OnProviderStreamEvent: recorder.observe})
		if err != nil {
			t.Fatal(err)
		}
		result := stream.Result()
		events, models := recorder.snapshot()
		if !reflect.DeepEqual(events, decodedEvents(t, created, completed)) {
			t.Fatalf("events = %#v", events)
		}
		assertEventModels(t, models, model, 2)
		if result.StopReason != StopReasonStop || result.ResponseID != "resp_azure" {
			t.Fatalf("result = %#v", result)
		}
	})

	// .upstream/v0.99.1/packages/ai/test/anthropic-sse-parsing.test.ts:113
	t.Run("anthropic forwards parsed provider stream events in order", func(t *testing.T) {
		model := mustGeneratedModel(t, "anthropic", "claude-haiku-4-5").ToModel()
		recorder := &providerEventRecorder{}
		result, _, _ := streamAnthropicFixture(t, AnthropicConfig{ModelMetadata: model, Model: model.ID, ProviderID: "anthropic"}, anthropicMinimalFixture(), StreamOptions{OnProviderStreamEvent: recorder.observe})
		events, models := recorder.snapshot()
		if result.StopReason != StopReasonStop {
			t.Fatalf("result = %#v", result)
		}
		var types []string
		for _, event := range events {
			types = append(types, event.(map[string]any)["type"].(string))
		}
		if want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}; !reflect.DeepEqual(types, want) {
			t.Fatalf("types = %v", types)
		}
		assertEventModels(t, models, model, 6)
	})

	// .upstream/v0.99.1/packages/ai/test/google-raw-stop-reason.test.ts:195
	for _, adapter := range []string{"Google Generative AI", "Google Vertex"} {
		t.Run("google forwards each SDK chunk in order before normalizing it for "+adapter, func(t *testing.T) {
			first := `{"responseId":"resp_google","candidates":[{"content":{"parts":[{"text":"hello"}]}}]}`
			second := `{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`
			server := serveSSE(t, sseFrames(first, second))
			recorder := &providerEventRecorder{}
			options := StreamOptions{OnProviderStreamEvent: recorder.observe}
			var provider Provider
			want := mustGeneratedModel(t, "google", "gemini-2.5-flash").ToModel()
			if adapter == "Google Vertex" {
				want = mustGeneratedModel(t, "google-vertex", "gemini-3-flash-preview").ToModel()
				vertex := NewGoogleVertexProvider(GoogleVertexConfig{ModelMetadata: want, Model: want.ID, ProviderID: want.ProviderMeta.ProviderID, Project: "test-project", Location: "us-central1", BaseURL: server.URL})
				vertex.(*googleVertexProvider).accessToken = func(context.Context, ProviderEnv) (string, error) { return "test-adc", nil }
				provider = vertex
			} else {
				provider = NewGoogleProvider(GoogleConfig{ModelMetadata: want, Model: want.ID, ProviderID: want.ProviderMeta.ProviderID, APIKey: "test-api-key", BaseURL: server.URL})
			}
			stream, err := provider.Stream(t.Context(), userTranscript("hello"), options)
			if err != nil {
				t.Fatal(err)
			}
			result := stream.Result()
			events, models := recorder.snapshot()
			if !reflect.DeepEqual(events, decodedEvents(t, first, second)) {
				t.Fatalf("events = %#v", events)
			}
			assertEventModels(t, models, want, 2)
			if result.StopReason != StopReasonStop || result.ResponseID != "resp_google" || len(result.Content) != 1 || result.Content[0] != (TextContent{Text: "hello"}) {
				t.Fatalf("result = %#v", result)
			}
		})
	}

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

	// .upstream/v0.99.1/packages/ai/test/pi-messages.test.ts:168
	t.Run("pi-messages forwards parsed wire events in order before converting them", func(t *testing.T) {
		wire := []string{
			`{"type":"start"}`,
			`{"type":"text_start","contentIndex":0}`,
			`{"type":"text_delta","contentIndex":0,"delta":"Hello","gatewayField":"upstream-value"}`,
			`{"type":"text_end","contentIndex":0,"content":"Hello"}`,
			`{"type":"done","reason":"stop","usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"totalTokens":15,"cost":{"input":0.1,"output":0.2,"cacheRead":0,"cacheWrite":0,"total":0.3}},"responseId":"resp_1"}`,
		}
		wireEvents := make([]any, len(wire))
		for i, raw := range wire {
			wireEvents[i] = decodedJSON(t, raw)
		}
		baseURL, _ := startPiMessagesServer(t, piMessagesResponder{events: wireEvents})
		// createModel(baseUrl) at pi-messages.test.ts:69.
		model := &Model{ID: "auto", DisplayName: "Radius Auto", Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16384, InputCostPer1M: 1, OutputCostPer1M: 2, CacheReadCostPer1M: 0.1, CacheWriteCostPer1M: 0.2}, ProviderMeta: ProviderMetadata{API: APIPiMessages, ProviderID: "radius", BaseURL: baseURL}}
		provider := newTestPiMessagesProvider(baseURL, func(cfg *PiMessagesConfig) { cfg.ModelMetadata = model })
		recorder := &providerEventRecorder{}
		stream, err := provider.Stream(t.Context(), piMessagesTestContext(), StreamOptions{APIKey: "test-key", OnProviderStreamEvent: recorder.observe})
		if err != nil {
			t.Fatal(err)
		}
		message := stream.Result()
		events, models := recorder.snapshot()
		if !reflect.DeepEqual(events, decodedEvents(t, wire...)) {
			t.Fatalf("events = %#v", events)
		}
		assertEventModels(t, models, model, len(wire))
		if message.StopReason != StopReasonStop || message.ResponseID != "resp_1" || len(message.Content) != 1 || message.Content[0] != (TextContent{Text: "Hello"}) {
			t.Fatalf("message = %#v", message)
		}
	})
}

// .upstream/v0.99.1/packages/ai/test/bedrock-raw-stop-reason.test.ts:73,91
func TestBedrockProviderStreamEventsUpstream(t *testing.T) {
	generated := mustGeneratedModel(t, "amazon-bedrock", "us.anthropic.claude-opus-4-8").ToModel()
	newProvider := func(t *testing.T, frames [][2]string) (Provider, StreamOptions, *Model) {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
			writeBedrockFrames(t, w, frames)
		}))
		t.Cleanup(server.Close)
		t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
		t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
		t.Setenv("AWS_PROFILE", "")
		t.Setenv("AWS_DEFAULT_PROFILE", "")
		t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "config"))
		t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials"))
		model := *generated
		model.ProviderMeta.BaseURL = server.URL
		provider := NewBedrockProviderWithModel(model)
		t.Cleanup(func() { _ = provider.Close() })
		return provider, StreamOptions{CacheRetention: CacheRetentionNone, Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}}, &model
	}

	t.Run("forwards SDK stream items in order before normalizing them", func(t *testing.T) {
		provider, options, model := newProvider(t, [][2]string{
			{"messageStart", `{"role":"assistant"}`},
			{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"hello"}}`},
			{"messageStop", `{"stopReason":"end_turn","additionalModelResponseFields":{"source":"test"}}`},
			{"metadata", `{"usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2}}`},
		})
		recorder := &providerEventRecorder{}
		options.OnProviderStreamEvent = recorder.observe
		stream, err := provider.Stream(t.Context(), userTranscript("hello"), options)
		if err != nil {
			t.Fatal(err)
		}
		result := stream.Result()
		events, models := recorder.snapshot()
		var kinds []string
		for _, event := range events {
			kinds = append(kinds, fmt.Sprintf("%T", event))
		}
		want := []string{
			"*types.ConverseStreamOutputMemberMessageStart", "*types.ConverseStreamOutputMemberContentBlockDelta",
			"*types.ConverseStreamOutputMemberMessageStop", "*types.ConverseStreamOutputMemberMetadata",
		}
		if !reflect.DeepEqual(kinds, want) {
			t.Fatalf("items = %v", kinds)
		}
		if delta := events[1].(*btypes.ConverseStreamOutputMemberContentBlockDelta); delta.Value.Delta.(*btypes.ContentBlockDeltaMemberText).Value != "hello" {
			t.Fatalf("delta = %#v", delta)
		}
		if stop := events[2].(*btypes.ConverseStreamOutputMemberMessageStop); stop.Value.StopReason != btypes.StopReasonEndTurn || stop.Value.AdditionalModelResponseFields == nil {
			t.Fatalf("stop = %#v", stop)
		}
		assertEventModels(t, models, model, 4)
		if result.StopReason != StopReasonStop || len(result.Content) != 1 || result.Content[0] != (TextContent{Text: "hello"}) {
			t.Fatalf("result = %#v", result)
		}
	})

	// A frame whose :message-type is exception never becomes a stream item: getMessageUnmarshaller (@smithy/core event-streams
	// getUnmarshalledStream.js) throws the deserialized exception before the for-await body runs, so the observer sees only the items before it.
	t.Run("forwards SDK stream items before an exception frame ends the request", func(t *testing.T) {
		provider, options, _ := newProvider(t, [][2]string{
			{"messageStart", `{"role":"assistant"}`},
			{"!internalServerException", `{"message":"bedrock stream failed"}`},
		})
		recorder := &providerEventRecorder{}
		options.OnProviderStreamEvent = recorder.observe
		stream, err := provider.Stream(t.Context(), userTranscript("hello"), options)
		if err != nil {
			t.Fatal(err)
		}
		result := stream.Result()
		events, _ := recorder.snapshot()
		if len(events) != 1 {
			t.Fatalf("items = %#v", events)
		}
		if _, ok := events[0].(*btypes.ConverseStreamOutputMemberMessageStart); !ok {
			t.Fatalf("item = %#v", events[0])
		}
		// formatBedrockError names the modeled exception; the upstream mock throws a plain Error, so its message has no prefix.
		if result.StopReason != StopReasonError || result.ErrorMessage != "Internal server error: bedrock stream failed" {
			t.Fatalf("result = %#v", result)
		}
	})

	// .upstream/v0.99.2/packages/ai/test/bedrock-raw-stop-reason.test.ts:91. An event frame whose :event-type is a modeled exception member is
	// deserialized as that union member and yielded ({ internalServerException } and its siblings), so Pi forwards it to onProviderStreamEvent and
	// then throws it (bedrock-converse-stream.ts:297,318-327). The upstream mock throws a plain Error; the real SDK throws the modeled exception,
	// whose message formatBedrockError prefixes. Probe of the pinned Pi package's streamBedrock against these frames: each kind below delivered
	// [messageStart, {<kind>}] and the listed error; an event type the union does not model was dropped and the stream ended without a stop reason.
	for _, tc := range []struct{ kind, message string }{
		{"internalServerException", "Internal server error: bedrock stream failed"},
		{"modelStreamErrorException", "Model stream error: bedrock stream failed"},
		{"validationException", "Validation error: bedrock stream failed"},
		{"throttlingException", "Throttling error: bedrock stream failed"},
		{"serviceUnavailableException", "Service unavailable: bedrock stream failed"},
	} {
		t.Run("forwards SDK error items before reporting them/"+tc.kind, func(t *testing.T) {
			body := `{"message":"bedrock stream failed"}`
			provider, options, _ := newProvider(t, [][2]string{{"messageStart", `{"role":"assistant"}`}, {tc.kind, body}})
			recorder := &providerEventRecorder{}
			options.OnProviderStreamEvent = recorder.observe
			stream, err := provider.Stream(t.Context(), userTranscript("hello"), options)
			if err != nil {
				t.Fatal(err)
			}
			result := stream.Result()
			events, _ := recorder.snapshot()
			if len(events) != 2 {
				t.Fatalf("items = %#v", events)
			}
			if _, ok := events[0].(*btypes.ConverseStreamOutputMemberMessageStart); !ok {
				t.Fatalf("item 0 = %#v", events[0])
			}
			if item, ok := events[1].(*btypes.UnknownUnionMember); !ok || item.Tag != tc.kind || string(item.Value) != body {
				t.Fatalf("item 1 = %#v", events[1])
			}
			if result.StopReason != StopReasonError || result.ErrorMessage != tc.message {
				t.Fatalf("result = %#v", result)
			}
		})
	}

	t.Run("drops event types the stream union does not model", func(t *testing.T) {
		provider, options, _ := newProvider(t, [][2]string{{"messageStart", `{"role":"assistant"}`}, {"somethingUnknown", `{"message":"bedrock stream failed"}`}})
		recorder := &providerEventRecorder{}
		options.OnProviderStreamEvent = recorder.observe
		stream, err := provider.Stream(t.Context(), userTranscript("hello"), options)
		if err != nil {
			t.Fatal(err)
		}
		result := stream.Result()
		if events, _ := recorder.snapshot(); len(events) != 1 {
			t.Fatalf("items = %#v", events)
		}
		if result.StopReason != StopReasonError || result.ErrorMessage != "Bedrock stream ended without a stop reason" {
			t.Fatalf("result = %#v", result)
		}
	})
}

// writeBedrockFrames encodes AWS event-stream frames. A kind that starts with "!" is a modeled exception.
func writeBedrockFrames(t *testing.T, w io.Writer, frames [][2]string) {
	t.Helper()
	encoder := eventstream.NewEncoder()
	for _, frame := range frames {
		headers := eventstream.Headers{}
		if exception, ok := strings.CutPrefix(frame[0], "!"); ok {
			headers.Set(":message-type", eventstream.StringValue("exception"))
			headers.Set(":exception-type", eventstream.StringValue(exception))
		} else {
			headers.Set(":message-type", eventstream.StringValue("event"))
			headers.Set(":event-type", eventstream.StringValue(frame[0]))
		}
		headers.Set(":content-type", eventstream.StringValue("application/json"))
		if err := encoder.Encode(w, eventstream.Message{Headers: headers, Payload: []byte(frame[1])}); err != nil {
			t.Fatal(err)
		}
	}
}
