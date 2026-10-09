//go:build !pig_strip_mistral_conversations && !pig_strip_openai_codex_responses

package ai

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// observedRequest is what a fake provider endpoint saw.
type observedRequest struct {
	path, query, accountID string
	body                   map[string]any
}

// recordingEndpoint answers every request with a 400 so the stream ends, and records the request it received.
func recordingEndpoint(t *testing.T) (*httptest.Server, func() observedRequest) {
	t.Helper()
	var seen observedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		seen = observedRequest{path: r.URL.Path, query: r.URL.RawQuery, accountID: r.Header.Get("chatgpt-account-id")}
		_ = json.Unmarshal(raw, &seen.body)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	return srv, func() observedRequest { return seen }
}

// codexToken is an API key the codex API accepts: openai-codex-responses.ts extractAccountId reads the ChatGPT account id from the JWT claim.
func codexToken() string {
	payload, _ := json.Marshal(map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct_test"}})
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(payload) + "." + enc([]byte("sig"))
}

// Ports the entry points of packages/ai/src/api/{anthropic-messages,azure-openai-responses,bedrock-converse-stream,google-generative-ai,google-vertex,mistral-conversations,openai-codex-responses,openai-completions,openai-responses}.lazy.ts:
// each `xxxApi()` is lazyApi(() => import("./xxx.ts")) (api/lazy.ts lazyApi, :72-80), so its stream and streamSimple build the request of
// that API from the model's fields, whatever model.api says, and a setup failure ends the stream with an error event (lazy.ts lazyStream, :43-60)
// instead of failing the call.
func TestLazyAPIEntryPointsRouteToTheirOwnAPIUpstream(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("AWS_REGION", "us-east-1")
	cases := []struct {
		name   string
		api    func() *ProviderStreams
		model  string
		apiKey string
		// path and body key that only this API's request has.
		path, bodyKey, query, account string
	}{
		// openai-completions.ts: the OpenAI SDK posts to {baseURL}/chat/completions with a `messages` array.
		{name: "openai-completions", api: OpenAICompletionsAPI, model: "gpt-4o-mini", apiKey: "k", path: "/chat/completions", bodyKey: "messages"},
		// openai-codex-responses.ts:644-646 resolveCodexUrl appends /codex/responses; :270 extractAccountId sends the token's account as chatgpt-account-id.
		{name: "openai-codex-responses", api: OpenAICodexResponsesAPI, model: "gpt-5.1-codex", apiKey: codexToken(), path: "/codex/responses", account: "acct_test"},
		// mistral-conversations.ts: the Mistral SDK posts to {server}/v1/chat/completions with `messages`.
		{name: "mistral-conversations", api: MistralConversationsAPI, model: "mistral-large-latest", apiKey: "k", path: "/v1/chat/completions", bodyKey: "messages"},
		// google-vertex.ts: streamGenerateContent with alt=sse and `contents`.
		{name: "google-vertex", api: GoogleVertexAPI, model: "gemini-2.5-flash", apiKey: "k", path: "/v1/publishers/google/models/gemini-2.5-flash:streamGenerateContent", bodyKey: "contents", query: "alt=sse"},
		// anthropic-messages.ts: the Anthropic SDK posts to {baseURL}/v1/messages?beta=true.
		{name: "anthropic-messages", api: AnthropicMessagesAPI, model: "claude-sonnet-4-5", apiKey: "k", path: "/v1/messages", bodyKey: "messages", query: "beta=true"},
		// azure-openai-responses.ts: the Responses endpoint with api-version=v1.
		{name: "azure-openai-responses", api: AzureOpenAIResponsesAPI, model: "gpt-4o", apiKey: "k", path: "/responses", bodyKey: "input", query: "api-version=v1"},
		// bedrock-converse-stream.ts: ConverseStream at /model/{id}/converse-stream (credentials come from the AWS environment).
		{name: "bedrock-converse-stream", api: BedrockConverseStreamAPI, model: "anthropic.claude-3-haiku", apiKey: "k", path: "/model/anthropic.claude-3-haiku/converse-stream", bodyKey: "inferenceConfig"},
		// google-generative-ai.ts: streamGenerateContent with alt=sse.
		{name: "google-generative-ai", api: GoogleGenerativeAIAPI, model: "gemini-2.5-flash", apiKey: "k", path: "/models/gemini-2.5-flash:streamGenerateContent", bodyKey: "contents", query: "alt=sse"},
		// openai-responses.ts: {baseURL}/responses with `input`.
		{name: "openai-responses", api: OpenAIResponsesAPI, model: "gpt-5.1", apiKey: "k", path: "/responses", bodyKey: "input"},
	}
	for _, tc := range cases {
		for _, simple := range []bool{false, true} {
			name := tc.name + "/stream"
			if simple {
				name = tc.name + "/streamSimple"
			}
			t.Run(name, func(t *testing.T) {
				srv, seen := recordingEndpoint(t)
				// model.api names another API: the module does not compare it with its own.
				model := &Model{ID: tc.model, DisplayName: tc.model, Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIAnthropicMessages, ProviderID: "p", BaseURL: srv.URL}}
				transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})
				streams := tc.api()
				call := streams.Stream
				if simple {
					call = streams.StreamSimple
				}
				stream, err := call(t.Context(), model, transcript, StreamOptions{APIKey: tc.apiKey})
				if err != nil {
					t.Fatalf("the call failed: %v", err)
				}
				message := stream.Result()
				got := seen()
				if got.path != tc.path || got.query != tc.query {
					t.Errorf("request went to %q?%q, want %q?%q", got.path, got.query, tc.path, tc.query)
				}
				if _, ok := got.body[tc.bodyKey]; tc.bodyKey != "" && !ok {
					t.Errorf("request body %v lacks %q", got.body, tc.bodyKey)
				}
				if got.accountID != tc.account {
					t.Errorf("chatgpt-account-id = %q, want %q", got.accountID, tc.account)
				}
				if message.StopReason != StopReasonError {
					t.Errorf("a 400 from the endpoint ended with %v, want an error message", message.StopReason)
				}
			})
		}
	}
}

// lazy.ts createSetupErrorMessage: a setup failure (here a missing API key, `No API key for provider: ${model.provider}`) comes back as the
// terminal error message of a stream the call already returned, carrying the model's api, provider and id with zero usage.
func TestLazyAPIEntryPointsReportSetupFailuresOnTheStreamUpstream(t *testing.T) {
	for _, key := range []string{"OPENAI_API_KEY", "MISTRAL_API_KEY", "ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN", "AZURE_OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"} {
		t.Setenv(key, "")
	}
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	cases := []struct {
		name string
		api  func() *ProviderStreams
		want string
	}{
		{"openai-completions", OpenAICompletionsAPI, "No API key for provider: p"},
		{"openai-codex-responses", OpenAICodexResponsesAPI, "No API key for provider: p"},
		{"mistral-conversations", MistralConversationsAPI, "No API key for provider: p"},
		{"anthropic-messages", AnthropicMessagesAPI, "No API key for provider: p"},
		{"azure-openai-responses", AzureOpenAIResponsesAPI, "No API key for provider: p"},
		{"google-generative-ai", GoogleGenerativeAIAPI, "No API key for provider: p"},
		{"openai-responses", OpenAIResponsesAPI, "No API key for provider: p"},
		// google-vertex.ts:448
		{"google-vertex", GoogleVertexAPI, "Vertex AI requires a project ID. Set GOOGLE_CLOUD_PROJECT/GCLOUD_PROJECT or pass project in options."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, seen := recordingEndpoint(t)
			model := &Model{ID: "model-x", DisplayName: "model-x", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIAnthropicMessages, ProviderID: "p", BaseURL: srv.URL}}
			stream, err := tc.api().Stream(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{})
			if err != nil {
				t.Fatalf("a setup failure failed the call: %v", err)
			}
			message := stream.Result()
			if message.StopReason != StopReasonError || message.ErrorMessage != tc.want {
				t.Errorf("result = %v %q, want error %q", message.StopReason, message.ErrorMessage, tc.want)
			}
			if message.Provider != "p" || message.Model != "model-x" || message.Usage.TotalTokens != 0 {
				t.Errorf("message = provider %q model %q usage %+v", message.Provider, message.Model, message.Usage)
			}
			if got := seen(); got.path != "" {
				t.Errorf("a setup failure still sent a request to %q", got.path)
			}
		})
	}
}

// A nil model fails the call. Pi's model parameter cannot be undefined; an undefined model there returns a stream that never ends while
// lazy.ts createSetupErrorMessage's read of model.api rejects unhandled, so Go's synchronous error is the type-contract guard, not a Pi output.
func TestLazyAPIEntryPointsRejectANilModelUpstream(t *testing.T) {
	for name, api := range map[string]func() *ProviderStreams{"openai-completions": OpenAICompletionsAPI, "google-vertex": GoogleVertexAPI, "mistral-conversations": MistralConversationsAPI, "openai-codex-responses": OpenAICodexResponsesAPI, "anthropic-messages": AnthropicMessagesAPI, "azure-openai-responses": AzureOpenAIResponsesAPI, "bedrock-converse-stream": BedrockConverseStreamAPI, "google-generative-ai": GoogleGenerativeAIAPI, "openai-responses": OpenAIResponsesAPI} {
		if _, err := api().Stream(t.Context(), nil, TranscriptContext{}, StreamOptions{}); err == nil || !strings.Contains(err.Error(), "nil") {
			t.Errorf("%s: Stream(nil model) error = %v", name, err)
		}
	}
}
