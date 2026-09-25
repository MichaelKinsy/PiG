package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// Expected usage values are Pi 0.87.1's serialized AssistantMessage.usage for
// the same response bodies, captured by running each upstream stream function
// against these exact bytes with the same catalog model.
func TestProviderUsageCostMatchesUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, kind, spec, body, want, responseModel string
	}{
		{
			name: "anthropic 1h cache write at twice input", kind: "anthropic", spec: "anthropic/claude-opus-4-8",
			body: `event: message_start
data: {"type":"message_start","message":{"id":"msg_test","usage":{"input_tokens":100,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":1000000,"cache_creation":{"ephemeral_5m_input_tokens":600000,"ephemeral_1h_input_tokens":400000}}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":100,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":1000000}}

event: message_stop
data: {"type":"message_stop"}

`,
			want: `{"input":100,"output":5,"cacheRead":0,"cacheWrite":1000000,"totalTokens":1000105,"cost":{"input":0.0005,"output":0.000125,"cacheRead":0,"cacheWrite":7.75,"total":7.750625},"cacheWrite1h":400000}`,
		},
		{
			name: "anthropic cache write without breakdown", kind: "anthropic", spec: "anthropic/claude-opus-4-8",
			body: `event: message_start
data: {"type":"message_start","message":{"id":"msg_test","usage":{"input_tokens":100,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":1000000}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":100,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":1000000}}

event: message_stop
data: {"type":"message_stop"}

`,
			want: `{"input":100,"output":5,"cacheRead":0,"cacheWrite":1000000,"totalTokens":1000105,"cost":{"input":0.0005,"output":0.000125,"cacheRead":0,"cacheWrite":6.25,"total":6.250625},"cacheWrite1h":0}`,
		},
		{
			name: "anthropic fallback model rates", kind: "anthropic", spec: "anthropic/claude-fable-5",
			body: `event: message_start
data: {"type":"message_start","message":{"id":"msg_test","model":"claude-opus-4-8","usage":{"input_tokens":1234,"output_tokens":0,"cache_read_input_tokens":5000,"cache_creation_input_tokens":700}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":321}}

event: message_stop
data: {"type":"message_stop"}

`,
			want:          `{"input":1234,"output":321,"cacheRead":5000,"cacheWrite":700,"totalTokens":7255,"cost":{"input":0.00617,"output":0.008025000000000001,"cacheRead":0.0025,"cacheWrite":0.004375,"total":0.021070000000000002},"cacheWrite1h":0}`,
			responseModel: "claude-opus-4-8",
		},
		{
			name: "completions prompt_cache_hit_tokens", kind: "completions", spec: "deepseek/deepseek-v4-pro",
			body: `data: {"id":"c1","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}]}

data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":12345,"completion_tokens":678,"prompt_cache_hit_tokens":10000}}

data: [DONE]

`,
			want: `{"input":2345,"output":678,"cacheRead":10000,"cacheWrite":0,"reasoning":0,"totalTokens":13023,"cost":{"input":0.0030954000000000003,"output":0.00268488,"cacheRead":0.00043999999999999996,"cacheWrite":0,"total":0.006220280000000001}}`,
		},
		{
			name: "completions present zero cached_tokens wins", kind: "completions", spec: "deepseek/deepseek-v4-pro",
			body: `data: {"id":"c1","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}]}

data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000,"completion_tokens":10,"prompt_cache_hit_tokens":500,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":100},"completion_tokens_details":{"reasoning_tokens":7}}}

data: [DONE]

`,
			want: `{"input":900,"output":10,"cacheRead":0,"cacheWrite":100,"reasoning":7,"totalTokens":1010,"cost":{"input":0.001188,"output":3.96e-05,"cacheRead":0,"cacheWrite":0,"total":0.0012276000000000001}}`,
		},
		{
			name: "completions choice usage fallback", kind: "completions", spec: "moonshotai/kimi-k3",
			body: `data: {"id":"c1","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}]}

data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop","usage":{"prompt_tokens":5000,"completion_tokens":200,"cached_tokens":3000}}]}

data: [DONE]

`,
			want: `{"input":2000,"output":200,"cacheRead":3000,"cacheWrite":0,"reasoning":0,"totalTokens":5200,"cost":{"input":0.006,"output":0.003,"cacheRead":0.0009,"cacheWrite":0,"total":0.0099}}`,
		},
		{
			name: "responses gpt-5.5 priority", kind: "responses", spec: "openai/gpt-5.5",
			body: `data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","service_tier":"priority","usage":{"input_tokens":100000,"output_tokens":100000,"total_tokens":200000,"input_tokens_details":{"cached_tokens":0}}}}

`,
			want: `{"input":100000,"output":100000,"cacheRead":0,"cacheWrite":0,"reasoning":0,"totalTokens":200000,"cost":{"input":1.25,"output":7.5,"cacheRead":0,"cacheWrite":0,"total":8.75}}`,
		},
		{
			name: "responses gpt-5.4 priority", kind: "responses", spec: "openai/gpt-5.4",
			body: `data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","service_tier":"priority","usage":{"input_tokens":100000,"output_tokens":100000,"total_tokens":200000,"input_tokens_details":{"cached_tokens":0}}}}

`,
			want: `{"input":100000,"output":100000,"cacheRead":0,"cacheWrite":0,"reasoning":0,"totalTokens":200000,"cost":{"input":0.5,"output":3,"cacheRead":0,"cacheWrite":0,"total":3.5}}`,
		},
		{
			name: "responses gpt-5.5 flex", kind: "responses", spec: "openai/gpt-5.5",
			body: `data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","service_tier":"flex","usage":{"input_tokens":100000,"output_tokens":100000,"total_tokens":200000,"input_tokens_details":{"cached_tokens":0}}}}

`,
			want: `{"input":100000,"output":100000,"cacheRead":0,"cacheWrite":0,"reasoning":0,"totalTokens":200000,"cost":{"input":0.25,"output":1.5,"cacheRead":0,"cacheWrite":0,"total":1.75}}`,
		},
		{
			name: "responses tier above threshold", kind: "responses", spec: "openai/gpt-5.5",
			body: `data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","service_tier":"default","usage":{"input_tokens":300000,"output_tokens":5000,"total_tokens":305000,"input_tokens_details":{"cached_tokens":100000},"output_tokens_details":{"reasoning_tokens":1200}}}}

`,
			want: `{"input":200000,"output":5000,"cacheRead":100000,"cacheWrite":0,"reasoning":1200,"totalTokens":305000,"cost":{"input":2,"output":0.225,"cacheRead":0.09999999999999999,"cacheWrite":0,"total":2.325}}`,
		},
		{
			name: "codex gpt-5.5 priority", kind: "codex", spec: "openai-codex/gpt-5.5",
			body: `data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","service_tier":"priority","usage":{"input_tokens":100000,"output_tokens":100000,"total_tokens":200000,"input_tokens_details":{"cached_tokens":0}}}}

`,
			want: `{"input":100000,"output":100000,"cacheRead":0,"cacheWrite":0,"reasoning":0,"totalTokens":200000,"cost":{"input":1.25,"output":7.5,"cacheRead":0,"cacheWrite":0,"total":8.75}}`,
		},
		{
			name: "azure ignores service tier", kind: "azure", spec: "azure-openai-responses/gpt-5.5",
			body: `data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","service_tier":"priority","usage":{"input_tokens":100000,"output_tokens":100000,"total_tokens":200000,"input_tokens_details":{"cached_tokens":0}}}}

`,
			want: `{"input":100000,"output":100000,"cacheRead":0,"cacheWrite":0,"reasoning":0,"totalTokens":200000,"cost":{"input":0.5,"output":3,"cacheRead":0,"cacheWrite":0,"total":3.5}}`,
		},
		{
			name: "google generative ai", kind: "google", spec: "google/gemini-2.5-pro",
			body: `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":250000,"candidatesTokenCount":1000,"thoughtsTokenCount":500,"cachedContentTokenCount":50000,"totalTokenCount":251500}}

`,
			want: `{"input":200000,"output":1500,"cacheRead":50000,"cacheWrite":0,"reasoning":500,"totalTokens":251500,"cost":{"input":0.25,"output":0.015000000000000001,"cacheRead":0.0062499999999999995,"cacheWrite":0,"total":0.27125}}`,
		},
		{
			name: "mistral conversations", kind: "mistral", spec: "mistral/mistral-large-2411",
			body: `data: {"id":"m1","model":"mistral-large-2411","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}

data: {"id":"m1","model":"mistral-large-2411","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":4000,"completion_tokens":300,"total_tokens":4300,"prompt_tokens_details":{"cached_tokens":1000}}}

data: [DONE]

`,
			want: `{"input":3000,"output":300,"cacheRead":1000,"cacheWrite":0,"totalTokens":4300,"cost":{"input":0.006,"output":0.0018,"cacheRead":0.0002,"cacheWrite":0,"total":0.008}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			generated, ok := LookupModelExact(tc.spec)
			if !ok {
				t.Fatalf("catalog model %s missing", tc.spec)
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(writer, tc.body)
			}))
			t.Cleanup(server.Close)
			provider := usageCostProvider(tc.kind, strings.SplitN(tc.spec, "/", 2), server.URL)
			transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})
			stream, err := provider.Stream(context.Background(), transcript, StreamOptions{ModelCost: (&Model{Capabilities: generated.ToCapabilities()}).CostRates()})
			if err != nil {
				t.Fatal(err)
			}
			result := stream.Result()
			if result.StopReason == StopReasonError {
				t.Fatalf("stream error: %s", result.ErrorMessage)
			}
			assertUsageJSON(t, result.Usage, tc.want)
			if result.ResponseModel != tc.responseModel {
				t.Fatalf("responseModel = %q, want %q", result.ResponseModel, tc.responseModel)
			}
		})
	}
}

func usageCostProvider(kind string, spec []string, baseURL string) Provider {
	providerID, modelID := spec[0], spec[1]
	switch kind {
	case "anthropic":
		return NewAnthropicProvider(AnthropicConfig{APIKey: "k", Model: modelID, BaseURL: baseURL, ProviderID: providerID})
	case "completions":
		return NewOpenAIProvider(OpenAIConfig{APIKey: "k", Model: modelID, BaseURL: baseURL, ProviderID: providerID})
	case "responses":
		return NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "k", Model: modelID, BaseURL: baseURL, ProviderID: providerID})
	case "codex":
		return NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: codexTestTokenForAccount("usage-cost"), Model: modelID, BaseURL: baseURL, ProviderID: providerID})
	case "azure":
		return NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{APIKey: "k", Model: modelID, BaseURL: baseURL, ProviderID: providerID})
	case "google":
		return NewGoogleProvider(GoogleConfig{APIKey: "k", Model: modelID, BaseURL: baseURL, ProviderID: providerID})
	case "mistral":
		return NewMistralProvider(MistralConfig{APIKey: "k", Model: modelID, BaseURL: baseURL, ProviderID: providerID})
	}
	panic("unknown provider kind " + kind)
}

func assertUsageJSON(t *testing.T, usage Usage, want string) {
	t.Helper()
	encoded, err := json.Marshal(usage)
	if err != nil {
		t.Fatal(err)
	}
	var got, expected map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("usage = %s\nwant  %s", encoded, want)
	}
}

// TestBedrockUsageCostMatchesUpstream ports bedrock-cache-write-1h-cost.test.ts:
// 1h cacheDetails bill at twice the input rate while cacheWrite keeps the total.
func TestBedrockUsageCostMatchesUpstream(t *testing.T) {
	generated, ok := LookupModelExact("amazon-bedrock/us.anthropic.claude-opus-4-8")
	if !ok {
		t.Fatal("catalog model missing")
	}
	input := make(chan btypes.ConverseStreamOutput, 3)
	input <- &btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}}
	input <- &btypes.ConverseStreamOutputMemberMetadata{Value: btypes.ConverseStreamMetadataEvent{Usage: &btypes.TokenUsage{
		InputTokens: aws.Int32(100), OutputTokens: aws.Int32(5), TotalTokens: aws.Int32(1_000_105), CacheWriteInputTokens: aws.Int32(1_000_000),
		CacheDetails: []btypes.CacheDetail{
			{Ttl: btypes.CacheTTLOneHour, InputTokens: aws.Int32(150_000)},
			{Ttl: btypes.CacheTTLFiveMinutes, InputTokens: aws.Int32(600_000)},
			{Ttl: btypes.CacheTTLOneHour, InputTokens: aws.Int32(250_000)},
		},
	}}}
	input <- &btypes.ConverseStreamOutputMemberMessageStop{Value: btypes.MessageStopEvent{StopReason: btypes.StopReasonEndTurn}}
	close(input)
	builder := newAssistantStreamBuilder(context.Background(), APIBedrockConverseStream, "amazon-bedrock", "us.anthropic.claude-opus-4-8")
	builder.modelCost = (&Model{Capabilities: generated.ToCapabilities()}).CostRates()
	go (&BedrockProvider{}).parseBedrockEvents(context.Background(), &fakeBedrockEventStream{events: input}, builder)
	assertUsageJSON(t, builder.stream.Result().Usage, `{"input":100,"output":5,"cacheRead":0,"cacheWrite":1000000,"cacheWrite1h":400000,"totalTokens":1000105,"cost":{"input":0.00055,"output":0.0001375,"cacheRead":0,"cacheWrite":8.525,"total":8.5256875}}`)
}
