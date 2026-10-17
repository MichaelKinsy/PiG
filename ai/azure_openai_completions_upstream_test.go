// Ports packages/ai/test/azure-openai-completions.test.ts. The upstream test mocks the OpenAI client; this port intercepts the request at the HTTP boundary of the production provider path (directAPIProvider, the one StreamSimple and the model runtime build), so it asserts the wire request.

package ai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type azureCompletionsRig struct {
	requestURL string
	body       map[string]any
	calls      int
}

type azureCompletionsTransport struct{ rig *azureCompletionsRig }

func (tr azureCompletionsTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	tr.rig.calls++
	tr.rig.requestURL = request.URL.String()
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	tr.rig.body = nil
	if err := json.Unmarshal(raw, &tr.rig.body); err != nil {
		return nil, err
	}
	sse := "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"prompt_tokens_details\":{\"cached_tokens\":0}}}\n\ndata: [DONE]\n\n"
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(bytes.NewReader([]byte(sse))), Request: request}, nil
}

func azureDeepSeekModel(t *testing.T) *Model {
	t.Helper()
	return upstreamCatalogModel(t, "azure", "deepseek-v4-pro")
}

// azureCompletionsProvider builds the provider the way StreamSimple does and routes its HTTP client to the rig.
func azureCompletionsProvider(t *testing.T, model *Model, rig *azureCompletionsRig) Provider {
	t.Helper()
	provider, err := directAPIProvider(model, "test-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	openAI, ok := provider.(*openAIProvider)
	if !ok {
		t.Fatalf("azure openai-completions model built %T, want the Chat Completions provider", provider)
	}
	openAI.client = &http.Client{Transport: azureCompletionsTransport{rig}}
	return provider
}

func azureCompletionsContext() Context {
	return Context{SystemPrompt: "sys", Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}}
}

func azureCompletionsMessages(t *testing.T, rig *azureCompletionsRig) []map[string]any {
	t.Helper()
	raw, _ := rig.body["messages"].([]any)
	messages := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		message, _ := item.(map[string]any)
		messages = append(messages, message)
	}
	return messages
}

func TestAzureOpenaiCompletionsUpstream(t *testing.T) {
	setup := func(t *testing.T) *azureCompletionsRig {
		t.Helper()
		t.Setenv("PI_CACHE_RETENTION", "")
		t.Setenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", "")
		t.Setenv("AZURE_OPENAI_RESOURCE_NAME", "")
		t.Setenv("AZURE_OPENAI_BASE_URL", "https://my-resource.services.ai.azure.com")
		return &azureCompletionsRig{}
	}
	stream := func(t *testing.T, rig *azureCompletionsRig, model *Model, transcript Context, options StreamOptions) *AssistantMessage {
		t.Helper()
		provider := azureCompletionsProvider(t, model, rig)
		options.APIKey = "test-key"
		stream, err := provider.Stream(t.Context(), NormalizeContext(transcript), options)
		if err != nil {
			t.Fatal(err)
		}
		return stream.Result()
	}
	streamSimple := func(t *testing.T, rig *azureCompletionsRig, model *Model, transcript Context, options StreamOptions) *AssistantMessage {
		t.Helper()
		provider := azureCompletionsProvider(t, model, rig)
		options.APIKey = "test-key"
		options.IsReasoning = model.ProviderMeta.Reasoning
		stream, err := provider.Stream(t.Context(), NormalizeContext(transcript), options)
		if err != nil {
			t.Fatal(err)
		}
		return stream.Result()
	}

	// Regression for #9645: Azure Foundry rejects DeepSeek's thinking field and every prompt cache parameter here.
	t.Run("azure deepseek-v4-pro over Chat Completions › turns thinking on with reasoning_effort instead of DeepSeek's thinking field", func(t *testing.T) {
		// upstream: packages/ai/test/azure-openai-completions.test.ts:112
		rig := setup(t)
		streamSimple(t, rig, azureDeepSeekModel(t), azureCompletionsContext(), StreamOptions{Thinking: ThinkingLevelHigh})
		if rig.body["reasoning_effort"] != "high" {
			t.Errorf("reasoning_effort = %v, want high", rig.body["reasoning_effort"])
		}
		if _, ok := rig.body["thinking"]; ok {
			t.Errorf("thinking = %v, want absent", rig.body["thinking"])
		}
	})
	t.Run("azure deepseek-v4-pro over Chat Completions › clamps thinking levels the deployment does not accept", func(t *testing.T) {
		// upstream: packages/ai/test/azure-openai-completions.test.ts:119
		rig := setup(t)
		streamSimple(t, rig, azureDeepSeekModel(t), azureCompletionsContext(), StreamOptions{Thinking: ThinkingLevelMax})
		if rig.body["reasoning_effort"] != "high" {
			t.Errorf("reasoning_effort = %v, want high", rig.body["reasoning_effort"])
		}
	})
	t.Run("azure deepseek-v4-pro over Chat Completions › sends no reasoning_effort when no thinking level is requested", func(t *testing.T) {
		// upstream: packages/ai/test/azure-openai-completions.test.ts:125
		rig := setup(t)
		streamSimple(t, rig, azureDeepSeekModel(t), azureCompletionsContext(), StreamOptions{})
		if _, ok := rig.body["reasoning_effort"]; ok {
			t.Errorf("reasoning_effort = %v, want absent", rig.body["reasoning_effort"])
		}
		if _, ok := rig.body["thinking"]; ok {
			t.Errorf("thinking = %v, want absent", rig.body["thinking"])
		}
	})
	t.Run("azure deepseek-v4-pro over Chat Completions › omits prompt cache parameters when long retention comes from PI_CACHE_RETENTION", func(t *testing.T) {
		// upstream: packages/ai/test/azure-openai-completions.test.ts:132
		rig := setup(t)
		t.Setenv("PI_CACHE_RETENTION", "long")
		stream(t, rig, azureDeepSeekModel(t), azureCompletionsContext(), StreamOptions{SessionID: "session-env"})
		for _, key := range []string{"prompt_cache_key", "prompt_cache_retention"} {
			if _, ok := rig.body[key]; ok {
				t.Errorf("%s = %v, want absent", key, rig.body[key])
			}
		}
	})
	// The deployment discards a `developer` system message once reasoning_effort is set, without billing it, so the system prompt has to go out under the system role.
	t.Run("azure deepseek-v4-pro over Chat Completions › sends the system prompt under the system role", func(t *testing.T) {
		// upstream: packages/ai/test/azure-openai-completions.test.ts:143
		rig := setup(t)
		stream(t, rig, azureDeepSeekModel(t), azureCompletionsContext(), StreamOptions{ReasoningEffort: "low", IsReasoning: true})
		messages := azureCompletionsMessages(t, rig)
		if len(messages) == 0 || messages[0]["role"] != "system" || messages[0]["content"] != "sys" {
			t.Errorf("first message = %v, want the system prompt under the system role", messages)
		}
	})
	// The deployment honours system messages sent mid-conversation, so pi must not collapse them.
	t.Run("azure deepseek-v4-pro over Chat Completions › keeps mid-conversation system messages in place", func(t *testing.T) {
		// upstream: packages/ai/test/azure-openai-completions.test.ts:150
		rig := setup(t)
		resumed := Context{SystemPrompt: "first", Messages: []Message{
			UserMessage{Content: UserText("hi"), Timestamp: 1},
			SystemMessage{Content: SystemText("second"), Timestamp: 2},
			UserMessage{Content: UserText("again"), Timestamp: 3},
		}}
		stream(t, rig, azureDeepSeekModel(t), resumed, StreamOptions{})
		var roles []string
		for _, message := range azureCompletionsMessages(t, rig) {
			roles = append(roles, message["role"].(string))
		}
		if got := strings.Join(roles, ","); got != "system,user,system,user" {
			t.Errorf("roles = %s, want system,user,system,user", got)
		}
	})
	t.Run("azure deepseek-v4-pro over Chat Completions › omits prompt cache parameters even when long retention is requested", func(t *testing.T) {
		// upstream: packages/ai/test/azure-openai-completions.test.ts:170
		rig := setup(t)
		stream(t, rig, azureDeepSeekModel(t), azureCompletionsContext(), StreamOptions{CacheRetention: CacheRetentionLong, SessionID: "session-1"})
		for _, key := range []string{"prompt_cache_key", "prompt_cache_retention"} {
			if _, ok := rig.body[key]; ok {
				t.Errorf("%s = %v, want absent", key, rig.body[key])
			}
		}
	})
	t.Run("azure deepseek-v4-pro over Chat Completions › replays reasoning_content on assistant turns so the cached prefix is unchanged", func(t *testing.T) {
		// upstream: packages/ai/test/azure-openai-completions.test.ts:179
		rig := setup(t)
		assistant := AssistantMessage{
			Content:    []AssistantContentBlock{ThinkingContent{Thinking: "internal reasoning", ThinkingSignature: "reasoning_content"}, TextContent{Text: "answer"}},
			Provider:   "azure",
			API:        APIOpenAICompletions,
			Model:      "deepseek-v4-pro",
			Timestamp:  2,
			StopReason: StopReasonStop,
		}
		resumed := Context{SystemPrompt: "sys", Messages: []Message{
			UserMessage{Content: UserText("first"), Timestamp: 1},
			assistant,
			UserMessage{Content: UserText("second"), Timestamp: 3},
		}}
		stream(t, rig, azureDeepSeekModel(t), resumed, StreamOptions{})
		var replayed any
		for _, message := range azureCompletionsMessages(t, rig) {
			if message["role"] == "assistant" {
				replayed = message["reasoning_content"]
			}
		}
		if replayed != "internal reasoning" {
			t.Errorf("reasoning_content = %v, want internal reasoning", replayed)
		}
	})

	t.Run("azure Chat Completions endpoint resolution › normalizes the Azure endpoint the completions client is built with", func(t *testing.T) {
		// upstream: packages/ai/test/azure-openai-completions.test.ts:217
		rig := setup(t)
		stream(t, rig, azureDeepSeekModel(t), azureCompletionsContext(), StreamOptions{})
		if want := "https://my-resource.services.ai.azure.com/openai/v1/chat/completions"; rig.requestURL != want {
			t.Errorf("request URL = %s, want %s", rig.requestURL, want)
		}
	})
	t.Run("azure Chat Completions endpoint resolution › surfaces an unconfigured endpoint as an error event rather than throwing out of stream()", func(t *testing.T) {
		// upstream: packages/ai/test/azure-openai-completions.test.ts:223
		rig := setup(t)
		t.Setenv("AZURE_OPENAI_BASE_URL", "")
		provider := azureCompletionsProvider(t, azureDeepSeekModel(t), rig)
		// Go providers report a request failure as Stream's error, as the Responses side does for an invalid endpoint (azure-openai-base-url.test.ts:155).
		_, err := provider.Stream(t.Context(), NormalizeContext(azureCompletionsContext()), StreamOptions{APIKey: "test-key"})
		if err == nil || !strings.Contains(err.Error(), "Azure OpenAI base URL is required") {
			t.Errorf("error = %v, want the unconfigured endpoint", err)
		}
		if rig.calls != 0 {
			t.Errorf("requests = %d, want none", rig.calls)
		}
	})
	// The id is persisted on the assistant message and read back by name, so it has to stay a catalog id.
	t.Run("azure Chat Completions endpoint resolution › sends the model id as the request model", func(t *testing.T) {
		// upstream: packages/ai/test/azure-openai-completions.test.ts:233
		rig := setup(t)
		result := stream(t, rig, azureDeepSeekModel(t), azureCompletionsContext(), StreamOptions{})
		if rig.body["model"] != "deepseek-v4-pro" || result.Model != "deepseek-v4-pro" {
			t.Errorf("request model = %v, message model = %s", rig.body["model"], result.Model)
		}
	})
	t.Run("azure Chat Completions endpoint resolution › sends the mapped deployment name while keeping the catalog id on the message", func(t *testing.T) {
		// upstream: packages/ai/test/azure-openai-completions.test.ts:240
		rig := setup(t)
		t.Setenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", "deepseek-v4-pro=my-deepseek")
		result := streamSimple(t, rig, azureDeepSeekModel(t), azureCompletionsContext(), StreamOptions{})
		if rig.body["model"] != "my-deepseek" || result.Model != "deepseek-v4-pro" {
			t.Errorf("request model = %v, message model = %s", rig.body["model"], result.Model)
		}
	})
	t.Run("azure Chat Completions endpoint resolution › passes the deployment name through a caller's onPayload", func(t *testing.T) {
		// upstream: packages/ai/test/azure-openai-completions.test.ts:249
		rig := setup(t)
		t.Setenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", "deepseek-v4-pro=my-deepseek")
		var seenModel any
		stream(t, rig, azureDeepSeekModel(t), azureCompletionsContext(), StreamOptions{OnPayload: func(payload any, _ *Model) (any, error) {
			encoded, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			var params map[string]any
			if err := json.Unmarshal(encoded, &params); err != nil {
				return nil, err
			}
			seenModel = params["model"]
			params["temperature"] = 0.1
			return params, nil
		}})
		if seenModel != "my-deepseek" || rig.body["model"] != "my-deepseek" || rig.body["temperature"] != 0.1 {
			t.Errorf("onPayload saw model %v; request model %v, temperature %v", seenModel, rig.body["model"], rig.body["temperature"])
		}
	})

	t.Run("azure api map › still routes Responses models to the Responses api", func(t *testing.T) {
		// upstream: packages/ai/test/azure-openai-completions.test.ts:269
		model := upstreamCatalogModel(t, "azure", "gpt-4o-mini")
		provider, err := directAPIProvider(model, "test-key", nil)
		if err != nil {
			t.Fatal(err)
		}
		responses, ok := provider.(*openAIResponsesProvider)
		if !ok || responses.api() != APIAzureOpenAIResponses {
			t.Errorf("gpt-4o-mini built %T, want the Azure Responses provider", provider)
		}
	})
	t.Run("azure api map › routes openai-completions models to chat completions", func(t *testing.T) {
		// upstream: packages/ai/test/azure-openai-completions.test.ts:275
		rig := setup(t)
		stream(t, rig, azureDeepSeekModel(t), azureCompletionsContext(), StreamOptions{})
		if rig.calls != 1 || !strings.HasSuffix(rig.requestURL, "/chat/completions") {
			t.Errorf("requests = %d to %s, want one Chat Completions request", rig.calls, rig.requestURL)
		}
	})
}
