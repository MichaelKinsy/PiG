//go:build !pig_strip_mistral_conversations && !pig_strip_openai_codex_responses

package ai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// Ports packages/ai/src/legacy-api-aliases.ts: streamAnthropic is anthropicMessagesApi().stream and streamSimpleAnthropic is
// anthropicMessagesApi().streamSimple, and likewise for the Azure, Google, Vertex, Mistral, Codex, Completions and Responses APIs. The alias must
// send exactly the request of its API's own stream or streamSimple: stream sends the options as given, streamSimple fills the output-token limit from the model.
// A missing key tells the two methods apart for every API: the API module's stream asserts request authentication inside its own try block, so a
// cancelled call ends with the abort-aware stop reason "aborted" (openai-responses.ts:155-157), while streamSimple asserts it as a setup failure that ends "error".
// mutation-checked: an alias that calls the other method (stream for streamSimple) or another API changes the request path or body.
func TestLegacyAPIAliasesSendTheRequestOfTheirOwnAPIStreamUpstream(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("AWS_REGION", "us-east-1")
	type alias = func(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error)
	cases := []struct {
		name           string
		api            func() *ProviderStreams
		stream, simple alias
		model, apiKey  string
	}{
		{"anthropic-messages", AnthropicMessagesAPI, StreamAnthropic, StreamSimpleAnthropic, "claude-sonnet-4-5", "k"},
		{"azure-openai-responses", AzureOpenAIResponsesAPI, StreamAzureOpenAIResponses, StreamSimpleAzureOpenAIResponses, "gpt-4o", "k"},
		{"google-generative-ai", GoogleGenerativeAIAPI, StreamGoogle, StreamSimpleGoogle, "gemini-2.5-flash", "k"},
		{"google-vertex", GoogleVertexAPI, StreamGoogleVertex, StreamSimpleGoogleVertex, "gemini-2.5-flash", "k"},
		{"mistral-conversations", MistralConversationsAPI, StreamMistral, StreamSimpleMistral, "mistral-large-latest", "k"},
		{"openai-codex-responses", OpenAICodexResponsesAPI, StreamOpenAICodexResponses, StreamSimpleOpenAICodexResponses, "gpt-5.1-codex", codexToken()},
		{"openai-completions", OpenAICompletionsAPI, StreamOpenAICompletions, StreamSimpleOpenAICompletions, "gpt-4o-mini", "k"},
		{"openai-responses", OpenAIResponsesAPI, StreamOpenAIResponses, StreamSimpleOpenAIResponses, "gpt-5.1", "k"},
	}
	observe := func(t *testing.T, tc int, call alias) rawObservedRequest {
		t.Helper()
		srv, seen := rawRecordingEndpoint(t)
		c := cases[tc]
		model := &Model{ID: c.model, DisplayName: c.model, Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIAnthropicMessages, ProviderID: "p", BaseURL: srv.URL, Reasoning: true},
			Capabilities: ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 777, MaxThinking: ThinkingLevel(ThinkingHigh)}}
		transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})
		stream, err := call(t.Context(), model, transcript, StreamOptions{APIKey: c.apiKey, Thinking: ThinkingLevel(ThinkingHigh), ReasoningEffort: "low"})
		if err != nil {
			t.Fatalf("the call failed: %v", err)
		}
		stream.Result()
		return seen()
	}
	for i, tc := range cases {
		t.Run(tc.name+"/request", func(t *testing.T) {
			wantStream := observe(t, i, tc.api().Stream)
			wantSimple := observe(t, i, tc.api().StreamSimple)
			if wantStream.path == "" {
				t.Fatalf("the API's own stream sent no request")
			}
			if got := observe(t, i, tc.stream); !reflect.DeepEqual(got, wantStream) {
				t.Errorf("the stream alias sent %v, want its API's stream request %v", got, wantStream)
			}
			if got := observe(t, i, tc.simple); !reflect.DeepEqual(got, wantSimple) {
				t.Errorf("the streamSimple alias sent %v, want its API's streamSimple request %v", got, wantSimple)
			}
		})
	}
}

// rawObservedRequest is the last non-empty request a fake endpoint saw: its path, query, account header and exact body bytes (the codex request is zstd-compressed).
type rawObservedRequest struct {
	path, query, accountID, body string
}

// String escapes the body: a codex body is zstd bytes that would corrupt a failure report.
func (r rawObservedRequest) String() string {
	return fmt.Sprintf("{%s?%s account=%q body=%.200q}", r.path, r.query, r.accountID, r.body)
}

func rawRecordingEndpoint(t *testing.T) (*httptest.Server, func() rawObservedRequest) {
	t.Helper()
	var seen rawObservedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			seen = rawObservedRequest{path: r.URL.Path, query: r.URL.RawQuery, accountID: r.Header.Get("chatgpt-account-id"), body: string(raw)}
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	return srv, func() rawObservedRequest { return seen }
}

func TestLegacyAPIAliasesKeepTheAuthenticationTimingOfTheirMethodUpstream(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN", "OPENAI_API_KEY", "AZURE_OPENAI_API_KEY", "MISTRAL_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"} {
		t.Setenv(key, "")
	}
	type alias = func(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error)
	cases := []struct {
		name           string
		stream, simple alias
		model          string
	}{
		{"anthropic-messages", StreamAnthropic, StreamSimpleAnthropic, "claude-sonnet-4-5"},
		{"azure-openai-responses", StreamAzureOpenAIResponses, StreamSimpleAzureOpenAIResponses, "gpt-4o"},
		{"google-generative-ai", StreamGoogle, StreamSimpleGoogle, "gemini-2.5-flash"},
		{"mistral-conversations", StreamMistral, StreamSimpleMistral, "mistral-large-latest"},
		{"openai-codex-responses", StreamOpenAICodexResponses, StreamSimpleOpenAICodexResponses, "gpt-5.1-codex"},
		{"openai-completions", StreamOpenAICompletions, StreamSimpleOpenAICompletions, "gpt-4o-mini"},
		{"openai-responses", StreamOpenAIResponses, StreamSimpleOpenAIResponses, "gpt-5.1"},
	}
	for _, tc := range cases {
		for _, simple := range []bool{false, true} {
			name, call, want := tc.name+"/stream", tc.stream, StopReasonAborted
			if simple {
				name, call, want = tc.name+"/streamSimple", tc.simple, StopReasonError
			}
			t.Run(name, func(t *testing.T) {
				srv, seen := rawRecordingEndpoint(t)
				model := &Model{ID: tc.model, DisplayName: tc.model, Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIAnthropicMessages, ProviderID: "p", BaseURL: srv.URL}}
				transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				stream, err := call(ctx, model, transcript, StreamOptions{})
				if err != nil {
					t.Fatalf("the call failed: %v", err)
				}
				message := stream.Result()
				if message.StopReason != want || message.ErrorMessage != "No API key for provider: p" {
					t.Errorf("a cancelled call without a key ended %v %q, want %v %q", message.StopReason, message.ErrorMessage, want, "No API key for provider: p")
				}
				if got := seen(); got.path != "" {
					t.Errorf("a call without a key sent a request to %q", got.path)
				}
			})
		}
	}
}

// Each API module's own stream builds its request from the options as given and the model's fields: openai-completions.ts sends
// `reasoning_effort` only when `options.reasoningEffort && model.reasoning`; bedrock-converse-stream.ts stream sends the thinking budget and the output cap it is given
// (only streamSimple lowers them); every module prices usage with calculateCost(model, usage); pi-messages.ts:366-367 asserts the key inside its stream.
// mutation-checked: dropping the reasoning flag, the raw-thinking flag, the model's prices or pi-messages' key check fails.
func TestAPIStreamsPassTheModelAndOptionsToTheirProviderUpstream(t *testing.T) {
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})
	t.Run("openai-completions sends reasoning_effort only for a reasoning model", func(t *testing.T) {
		for _, reasoning := range []bool{false, true} {
			srv, seen := rawRecordingEndpoint(t)
			model := &Model{ID: "gpt-4o-mini", DisplayName: "x", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIAnthropicMessages, ProviderID: "p", BaseURL: srv.URL, Reasoning: reasoning}}
			stream, err := OpenAICompletionsAPI().Stream(t.Context(), model, transcript, StreamOptions{APIKey: "k", ReasoningEffort: "low"})
			if err != nil {
				t.Fatal(err)
			}
			stream.Result()
			if got := strings.Contains(seen().body, `"reasoning_effort":"low"`); got != reasoning {
				t.Errorf("model.reasoning=%v: reasoning_effort sent = %v in %v", reasoning, got, seen())
			}
		}
	})
	t.Run("bedrock stream sends the output cap and thinking budget as given", func(t *testing.T) {
		t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
		t.Setenv("AWS_REGION", "us-east-1")
		want := map[bool][]string{
			false: {`"maxTokens":5000`, `"budget_tokens":16384`},
			true:  {`"maxTokens":8000`, `"budget_tokens":6976`},
		}
		for _, simple := range []bool{false, true} {
			srv, seen := rawRecordingEndpoint(t)
			model := &Model{ID: "anthropic.claude-3-haiku", DisplayName: "anthropic.claude-3-haiku", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIAnthropicMessages, ProviderID: "p", BaseURL: srv.URL, Reasoning: true},
				Capabilities: ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 8000, MaxThinking: ThinkingLevel(ThinkingHigh)}}
			call := BedrockConverseStreamAPI().Stream
			if simple {
				call = BedrockConverseStreamAPI().StreamSimple
			}
			stream, err := call(t.Context(), model, transcript, StreamOptions{APIKey: "k", MaxTokens: 5000, Thinking: ThinkingLevel(ThinkingHigh)})
			if err != nil {
				t.Fatal(err)
			}
			stream.Result()
			for _, fragment := range want[simple] {
				if !strings.Contains(seen().body, fragment) {
					t.Errorf("simple=%v: request %v lacks %s", simple, seen(), fragment)
				}
			}
		}
	})
	t.Run("usage is priced with the model's own rates", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1000000,\"completion_tokens\":1000000,\"total_tokens\":2000000}}\n\ndata: [DONE]\n\n")
		}))
		t.Cleanup(srv.Close)
		model := &Model{ID: "gpt-4o-mini", DisplayName: "x", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIAnthropicMessages, ProviderID: "p", BaseURL: srv.URL},
			Capabilities: ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 777, InputCostPer1M: 2, OutputCostPer1M: 3}}
		for _, simple := range []bool{false, true} {
			call := OpenAICompletionsAPI().Stream
			if simple {
				call = OpenAICompletionsAPI().StreamSimple
			}
			stream, err := call(t.Context(), model, transcript, StreamOptions{APIKey: "k"})
			if err != nil {
				t.Fatal(err)
			}
			message := stream.Result()
			if message.StopReason != StopReasonStop || message.Usage.Cost.Input != 2 || message.Usage.Cost.Output != 3 || message.Usage.Cost.Total != 5 {
				t.Errorf("simple=%v: stop %v cost %+v, want stop with input 2, output 3, total 5", simple, message.StopReason, message.Usage.Cost)
			}
		}
	})
	t.Run("pi-messages asserts its key inside its stream and sends nothing without one", func(t *testing.T) {
		for _, simple := range []bool{false, true} {
			srv, seen := rawRecordingEndpoint(t)
			model := &Model{ID: "m", DisplayName: "m", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIAnthropicMessages, ProviderID: "p", BaseURL: srv.URL}}
			call := PiMessagesAPI().Stream
			if simple {
				call = PiMessagesAPI().StreamSimple
			}
			stream, err := call(t.Context(), model, transcript, StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			message := stream.Result()
			if message.StopReason != StopReasonError || message.ErrorMessage != `No API key provided for provider "p"` || seen().path != "" {
				t.Errorf("simple=%v: %v %q after a request to %q, want the key error and no request", simple, message.StopReason, message.ErrorMessage, seen().path)
			}
			if !simple {
				// A cancelled call ends with the abort-aware stop reason: the assertion sits inside the stream's try block.
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				stream, err = call(ctx, model, transcript, StreamOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if message := stream.Result(); message.StopReason != StopReasonAborted {
					t.Errorf("a cancelled keyless pi-messages stream ended %v, want aborted", message.StopReason)
				}
			}
			// With a key the request goes to {baseURL}/messages.
			srv2, seen2 := rawRecordingEndpoint(t)
			model.ProviderMeta.BaseURL = srv2.URL
			stream, err = call(t.Context(), model, transcript, StreamOptions{APIKey: "k"})
			if err != nil {
				t.Fatal(err)
			}
			stream.Result()
			if seen2().path != "/messages" || !strings.Contains(seen2().body, `"model":"m"`) {
				t.Errorf("simple=%v: keyed request = %v, want POST /messages for model m", simple, seen2())
			}
		}
	})
}
