//go:build !pig_strip_mistral_conversations && !pig_strip_openai_codex_responses

package ai

import "testing"

// upstream: packages/ai/src/api/openai-completions.lazy.ts:4 `openAICompletionsApi = () => lazyApi(() => import("./openai-completions.ts"))` with api/lazy.ts lazyApi (:72-80). lazyApi is called without capabilities, so the ProviderStreams has stream and streamSimple only (no fetchDeferred, no cancelDeferred, lazy.ts:82-97). Both streams build the openai-completions request from the model's fields whatever model.api says (the module does not compare it) and a setup failure ends the returned stream with an error message (lazy.ts:43-60 createSetupErrorMessage), not a failed call.
// The model names the anthropic-messages API on purpose. The OpenAI SDK posts to {baseURL}/chat/completions with a `messages` array; the 400 reply ends each stream with an error; with no API key the stream ends with `No API key for provider: p` and sends nothing.
// mutation-checked: OpenAICompletionsAPI returning the openai-responses streams, returning streams with a FetchDeferred, and skipping the missing-API-key check each fail it.
func TestOpenAICompletionsAPIEntryPointUpstream(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	streams := OpenAICompletionsAPI()
	if streams.FetchDeferred != nil || streams.CancelDeferred != nil {
		t.Errorf("openAICompletionsApi has deferred capabilities: fetch %v cancel %v", streams.FetchDeferred != nil, streams.CancelDeferred != nil)
	}
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})
	newModel := func(baseURL string) *Model {
		return &Model{ID: "gpt-4o-mini", DisplayName: "gpt-4o-mini", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIAnthropicMessages, ProviderID: "p", BaseURL: baseURL}}
	}

	for _, simple := range []bool{false, true} {
		name := "stream"
		call := streams.Stream
		if simple {
			name, call = "streamSimple", streams.StreamSimple
		}
		t.Run(name+" posts to chat/completions", func(t *testing.T) {
			srv, seen := recordingEndpoint(t)
			stream, err := call(t.Context(), newModel(srv.URL), transcript, StreamOptions{APIKey: "k"})
			if err != nil {
				t.Fatalf("the call failed: %v", err)
			}
			message := stream.Result()
			got := seen()
			if got.path != "/chat/completions" {
				t.Errorf("request path = %q, want /chat/completions", got.path)
			}
			if _, ok := got.body["messages"]; !ok {
				t.Errorf("request body %v lacks messages", got.body)
			}
			if message.StopReason != StopReasonError {
				t.Errorf("a 400 reply ended with %v, want error", message.StopReason)
			}
		})
		t.Run(name+" reports a missing API key on the stream", func(t *testing.T) {
			srv, seen := recordingEndpoint(t)
			stream, err := call(t.Context(), newModel(srv.URL), transcript, StreamOptions{})
			if err != nil {
				t.Fatalf("a setup failure failed the call: %v", err)
			}
			message := stream.Result()
			if message.StopReason != StopReasonError || message.ErrorMessage != "No API key for provider: p" {
				t.Errorf("result = %v %q, want error %q", message.StopReason, message.ErrorMessage, "No API key for provider: p")
			}
			if message.Provider != "p" || message.Model != "gpt-4o-mini" || message.Usage.TotalTokens != 0 {
				t.Errorf("message = provider %q model %q usage %+v", message.Provider, message.Model, message.Usage)
			}
			if got := seen(); got.path != "" {
				t.Errorf("a missing key still sent a request to %q", got.path)
			}
		})
	}
}
