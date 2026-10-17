//go:build !pig_strip_mistral_conversations && !pig_strip_openai_codex_responses && !pig_strip_google_vertex

package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// Every API module's `stream` (packages/ai/src/api/<api>.ts) runs its request inside its own `(async () => { try { ... } catch (error) { ... } })()`, so a failure there,
// the missing API key included, ends the module's stream with the module's output message: its `api` is model.api for anthropic-messages, mistral-conversations,
// openai-completions, openai-responses and pi-messages and the module's own API for azure-openai-responses, google-generative-ai, google-vertex and
// openai-codex-responses (google-vertex.ts:80, openai-codex-responses.ts:249), and its stop reason is "aborted" when the request's signal was aborted
// (`output.stopReason = options?.signal?.aborted ? "aborted" : "error"`). Only the synchronous authentication throw of a module's `streamSimple` (for example
// openai-codex-responses.ts:505-508) rejects lazyApi's setup, and lazyStream's createSetupErrorMessage reports it with model.api and "error" (lazy.ts:4-23, :47-60).
func TestLazyAPIEntryPointsEndModuleFailuresLikeTheModuleUpstream(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	cases := []struct {
		name string
		api  API
		// streams returns the entry point under test.
		streams func() *ProviderStreams
		// moduleAPI is the api of the module's own output message.
		moduleAPI API
		// simpleThrows says the module's streamSimple throws synchronously on a missing key.
		simpleThrows bool
	}{
		{"anthropic-messages", APIAnthropicMessages, AnthropicMessagesAPI, "", true},
		{"azure-openai-responses", APIAzureOpenAIResponses, AzureOpenAIResponsesAPI, APIAzureOpenAIResponses, true},
		{"google-generative-ai", APIGoogleGenerativeAI, GoogleGenerativeAIAPI, APIGoogleGenerativeAI, true},
		{"google-vertex", APIGoogleVertex, GoogleVertexAPI, APIGoogleVertex, false},
		{"mistral-conversations", APIMistralConversations, MistralConversationsAPI, "", true},
		{"openai-codex-responses", APIOpenAICodexResponses, OpenAICodexResponsesAPI, APIOpenAICodexResponses, true},
		{"openai-completions", APIOpenAICompletions, OpenAICompletionsAPI, "", true},
		{"openai-responses", APIOpenAIResponses, OpenAIResponsesAPI, "", true},
		{"pi-messages", APIPiMessages, PiMessagesAPI, "", false},
	}
	for _, tc := range cases {
		// model.api names another API, so the two api sources differ.
		modelAPI := APIAnthropicMessages
		if tc.api == APIAnthropicMessages {
			modelAPI = APIOpenAICompletions
		}
		moduleAPI := tc.moduleAPI
		if moduleAPI == "" {
			moduleAPI = modelAPI
		}
		transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})
		for _, simple := range []bool{false, true} {
			call := func(ctx context.Context, model *Model, options StreamOptions) *AssistantMessage {
				t.Helper()
				streams := tc.streams()
				entry := streams.Stream
				if simple {
					entry = streams.StreamSimple
				}
				stream, err := entry(ctx, model, transcript, options)
				if err != nil {
					t.Fatalf("the call failed: %v", err)
				}
				return stream.Result()
			}
			name := tc.name + "/stream"
			if simple {
				name = tc.name + "/streamSimple"
			}
			t.Run(name+"/missing key", func(t *testing.T) {
				srv, seen := recordingEndpoint(t)
				model := &Model{ID: "model-x", DisplayName: "model-x", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: modelAPI, ProviderID: "p", BaseURL: srv.URL}}
				message := call(t.Context(), model, StreamOptions{})
				want := moduleAPI
				if simple && tc.simpleThrows {
					want = modelAPI
				}
				if message.StopReason != StopReasonError || message.API != want {
					t.Errorf("result = api %q %v %q, want api %q and an error", message.API, message.StopReason, message.ErrorMessage, want)
				}
				if got := seen(); got.path != "" {
					t.Errorf("a missing key still sent a request to %q", got.path)
				}
			})
			t.Run(name+"/aborted before the request", func(t *testing.T) {
				srv, _ := recordingEndpoint(t)
				model := &Model{ID: "model-x", DisplayName: "model-x", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: modelAPI, ProviderID: "p", BaseURL: srv.URL}}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				key := "k"
				if tc.api == APIOpenAICodexResponses {
					key = codexToken()
				}
				if message := call(ctx, model, StreamOptions{APIKey: key}); message.StopReason != StopReasonAborted {
					t.Errorf("result = %v %q, want aborted", message.StopReason, message.ErrorMessage)
				}
			})
		}
	}
}

// The pinned pi-ai entry points decide the same cases: a missing key gives the same api, stop reason and error message, and a request aborted before it starts
// ends with the same stop reason and error message (testdata/lazy_api_failures.mjs drives api/*.lazy.js with a model whose api names another API).
func TestLazyAPIEntryPointFailuresMatchPi(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	type probe struct {
		Module   string `json:"module"`
		Factory  string `json:"factory"`
		ModelAPI API    `json:"modelApi"`
		APIKey   string `json:"apiKey,omitempty"`
		Aborted  bool   `json:"aborted,omitempty"`
		Simple   bool   `json:"simple,omitempty"`
	}
	entries := []struct {
		api     API
		factory string
		streams func() *ProviderStreams
	}{
		{APIAnthropicMessages, "anthropicMessagesApi", AnthropicMessagesAPI},
		{APIAzureOpenAIResponses, "azureOpenAIResponsesApi", AzureOpenAIResponsesAPI},
		{APIGoogleGenerativeAI, "googleGenerativeAIApi", GoogleGenerativeAIAPI},
		{APIGoogleVertex, "googleVertexApi", GoogleVertexAPI},
		{APIMistralConversations, "mistralConversationsApi", MistralConversationsAPI},
		{APIOpenAICodexResponses, "openAICodexResponsesApi", OpenAICodexResponsesAPI},
		{APIOpenAICompletions, "openAICompletionsApi", OpenAICompletionsAPI},
		{APIOpenAIResponses, "openAIResponsesApi", OpenAIResponsesAPI},
		{APIPiMessages, "piMessagesApi", PiMessagesAPI},
	}
	var probes []probe
	var streams []func() *ProviderStreams
	for _, entry := range entries {
		modelAPI := APIAnthropicMessages
		if entry.api == APIAnthropicMessages {
			modelAPI = APIOpenAICompletions
		}
		key := "k"
		if entry.api == APIOpenAICodexResponses {
			key = codexToken()
		}
		for _, simple := range []bool{false, true} {
			probes = append(probes, probe{Module: string(entry.api), Factory: entry.factory, ModelAPI: modelAPI, Simple: simple})
			probes = append(probes, probe{Module: string(entry.api), Factory: entry.factory, ModelAPI: modelAPI, APIKey: key, Aborted: true, Simple: simple})
			streams = append(streams, entry.streams, entry.streams)
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/lazy_api_failures.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, stderr.String())
	}
	var want []struct {
		API          API        `json:"api"`
		StopReason   StopReason `json:"stopReason"`
		ErrorMessage string     `json:"errorMessage"`
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(probes) {
		t.Fatalf("Pi oracle answered %d cases, want %d", len(want), len(probes))
	}
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})
	for i, probe := range probes {
		srv, _ := recordingEndpoint(t)
		model := &Model{ID: "model-x", DisplayName: "model-x", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: probe.ModelAPI, ProviderID: "p", BaseURL: srv.URL}}
		ctx := t.Context()
		if probe.Aborted {
			aborted, cancel := context.WithCancel(ctx)
			cancel()
			ctx = aborted
		}
		entry := streams[i]().Stream
		if probe.Simple {
			entry = streams[i]().StreamSimple
		}
		stream, err := entry(ctx, model, transcript, StreamOptions{APIKey: probe.APIKey})
		if err != nil {
			t.Fatalf("%+v: the call failed: %v", probe, err)
		}
		got := stream.Result()
		if probe.Aborted {
			// The abort text is the module's own: retryProviderRequest's "Request aborted" (provider-retry.ts:71-75), or the
			// signal's reason where the module calls signal.throwIfAborted() first (anthropic-messages.ts:647: "This
			// operation was aborted" for a plain abort()).
			if got.StopReason != want[i].StopReason || got.ErrorMessage != want[i].ErrorMessage {
				t.Errorf("%s simple=%v aborted: %v %q, Pi %v %q", probe.Module, probe.Simple, got.StopReason, got.ErrorMessage, want[i].StopReason, want[i].ErrorMessage)
			}
			continue
		}
		if got.API != want[i].API || got.StopReason != want[i].StopReason || got.ErrorMessage != want[i].ErrorMessage {
			t.Errorf("%s simple=%v missing key: %q %v %q, Pi %q %v %q", probe.Module, probe.Simple, got.API, got.StopReason, got.ErrorMessage, want[i].API, want[i].StopReason, want[i].ErrorMessage)
		}
	}
}
