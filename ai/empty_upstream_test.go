package ai_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// TestEmptyMessagesUpstream ports the four helpers at .upstream/v0.99.1/packages/ai/test/empty.test.ts:19-144 for every case site in that file.
// The compiler-derived inventory supplies each exact case name and source line. Real provider conversion/error handling and faux success both run; remote model acceptance remains live-only because the upstream assertions allow either success or a provider error.
func TestEmptyMessagesUpstream(t *testing.T) {
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"empty input rejected"},"message":"empty input rejected"}`))
	}))
	t.Cleanup(server.Close)
	type modelCase struct{ provider, model string }
	models := []modelCase{
		{"google", "gemini-2.5-flash"}, {"openai", "gpt-4o-mini"}, {"openai", "gpt-5-mini"}, {"azure-openai-responses", "gpt-4o-mini"},
		{"anthropic", "claude-haiku-4-5"}, {"xai", "grok-4.3"}, {"groq", "openai/gpt-oss-20b"}, {"cerebras", "gpt-oss-120b"},
		{"cloudflare-workers-ai", "@cf/moonshotai/kimi-k2.6"}, {"cloudflare-ai-gateway", "workers-ai/@cf/moonshotai/kimi-k2.6"},
		{"huggingface", "moonshotai/Kimi-K2.5"}, {"together", "moonshotai/Kimi-K3"}, {"baseten", "zai-org/GLM-5.2"}, {"zai", "glm-5.2"},
		{"mistral", "devstral-medium-latest"}, {"minimax", "MiniMax-M2.7"}, {"xiaomi", "mimo-v2.5-pro"}, {"xiaomi-token-plan-cn", "mimo-v2.5-pro"},
		{"xiaomi-token-plan-ams", "mimo-v2.5-pro"}, {"xiaomi-token-plan-sgp", "mimo-v2.5-pro"}, {"qwen-token-plan", "qwen3.7-max"},
		{"qwen-token-plan-individual", "qwen3.8-max"}, {"qwen-token-plan-cn", "qwen3.7-max"}, {"kimi-coding", "kimi-for-coding"},
		{"vercel-ai-gateway", "google/gemini-2.5-flash"}, {"amazon-bedrock", "global.anthropic.claude-sonnet-4-5-20250929-v1:0"},
		{"anthropic", "claude-haiku-4-5"}, {"github-copilot", "claude-haiku-4.5"}, {"github-copilot", "claude-sonnet-4.6"}, {"openai-codex", "gpt-5.5"},
	}
	cases := upstreamCaseSites(t, "packages/ai/test/empty.test.ts")
	if len(cases) != len(models)*4 {
		t.Fatalf("upstream denominator has %d cases for %d four-case model groups", len(cases), len(models))
	}
	for index, tc := range cases {
		t.Run(tc.ID, func(t *testing.T) {
			t.Logf(".upstream/current/packages/ai/test/empty.test.ts:%d", tc.Line)
			spec := models[index/4]
			metadata, ok := ai.LookupModelExact(spec.provider + "/" + spec.model)
			if !ok {
				t.Fatalf("upstream test model missing from pinned catalog: %s/%s", spec.provider, spec.model)
			}
			request := emptyUpstreamContext(t, tc.ID, metadata)
			options := ai.StreamOptions{Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}, Transport: ai.TransportSSE}
			if spec.provider == "baseten" {
				options.Thinking = ai.ThinkingHigh
				options.IsReasoning = true
			}
			real := matrixProvider(t, metadata, server.URL)
			response := services.ModelRuntime().Complete(t.Context(), &ai.Model{ID: metadata.ID, Provider: real}, request, options)
			assertEmptyUpstreamResult(t, response, strings.Contains(tc.ID, "empty assistant"))
			faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: spec.provider, Model: spec.model})
			faux.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Please respond this time.")}, StopReason: "stop"})})
			response = services.ModelRuntime().Complete(t.Context(), &ai.Model{ID: metadata.ID, Provider: faux}, request, options)
			assertEmptyUpstreamResult(t, response, strings.Contains(tc.ID, "empty assistant"))
			if response.StopReason != ai.StopReasonStop || !reflect.DeepEqual(response.Content, []ai.AssistantContentBlock{ai.TextContent{Text: "Please respond this time."}}) {
				t.Fatalf("faux success result = %#v", response)
			}
			if faux.CallCount() != 1 {
				t.Fatal("faux success path was not invoked")
			}
		})
	}
}

type upstreamCaseSite struct {
	ID   string `json:"id"`
	Line int    `json:"line"`
}

func upstreamCaseSites(t *testing.T, path string) []upstreamCaseSite {
	t.Helper()
	raw, err := os.ReadFile("../test/parity/interfaces/upstream-tests-v" + coding.UpstreamVersion + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Files []struct {
			Path  string             `json:"path"`
			Cases []upstreamCaseSite `json:"cases"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &inventory); err != nil {
		t.Fatal(err)
	}
	for _, file := range inventory.Files {
		if file.Path == path {
			return file.Cases
		}
	}
	t.Fatal("missing upstream case inventory", path)
	return nil
}

func emptyUpstreamContext(t *testing.T, name string, model *ai.GeneratedModel) ai.Context {
	t.Helper()
	switch {
	case strings.Contains(name, "empty content array"):
		return ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserContentBlocks{}}}}
	case strings.Contains(name, "empty string content"):
		return ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("")}}}
	case strings.Contains(name, "whitespace-only content"):
		return ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("   \n\t  ")}}}
	case strings.Contains(name, "empty assistant message"):
		return ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello, how are you?")}, ai.AssistantMessage{Content: []ai.AssistantContentBlock{}, API: model.API, Provider: model.Provider, Model: model.ID, Usage: ai.Usage{Input: 10, TotalTokens: 10}, StopReason: ai.StopReasonStop}, ai.UserMessage{Content: ai.UserText("Please respond this time.")}}}
	default:
		t.Fatal("unported helper", name)
		return ai.Context{}
	}
}

func assertEmptyUpstreamResult(t *testing.T, response *ai.AssistantMessage, assistant bool) {
	t.Helper()
	if response == nil {
		t.Fatal("undefined response")
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["role"]) != `"assistant"` {
		t.Fatalf("role = %s", fields["role"])
	}
	if response.StopReason == ai.StopReasonError {
		if response.ErrorMessage == "" {
			t.Fatal("error response lacks errorMessage")
		}
	} else {
		if response.Content == nil {
			t.Fatal("successful response lacks content")
		}
		if assistant && len(response.Content) == 0 {
			t.Fatal("empty-assistant continuation has no content")
		}
	}
}

func matrixProvider(t *testing.T, model *ai.GeneratedModel, url string) ai.Provider {
	t.Helper()
	switch model.API {
	case ai.APIAnthropicMessages:
		return ai.NewAnthropicProvider(ai.AnthropicConfig{APIKey: "test-key", Model: model.ID, ProviderID: model.Provider, BaseURL: url, Compat: model.Compat})
	case ai.APIOpenAICompletions:
		return ai.NewOpenAIProvider(ai.OpenAIConfig{APIKey: "test-key", Model: model.ID, ProviderID: model.Provider, BaseURL: url, Compat: model.Compat})
	case ai.APIOpenAIResponses:
		return ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{APIKey: "test-key", Model: model.ID, ProviderID: model.Provider, BaseURL: url, Compat: model.Compat})
	case ai.APIAzureOpenAIResponses:
		return ai.NewAzureOpenAIResponsesProvider(ai.AzureOpenAIResponsesConfig{APIKey: "test-key", Model: model.ID, ProviderID: model.Provider, BaseURL: url, Compat: model.Compat})
	case ai.APIGoogleGenerativeAI:
		return ai.NewGoogleProvider(ai.GoogleConfig{APIKey: "test-key", Model: model.ID, ProviderID: model.Provider, BaseURL: url})
	case ai.APIGoogleVertex:
		return ai.NewGoogleVertexProvider(ai.GoogleVertexConfig{APIKey: "test-key", Model: model.ID, ProviderID: model.Provider, BaseURL: url})
	case ai.APIMistralConversations:
		return ai.NewMistralProvider(ai.MistralConfig{APIKey: "test-key", Model: model.ID, ProviderID: model.Provider, BaseURL: url})
	case ai.APIBedrockConverseStream:
		return ai.NewBedrockProvider(model.ID, url)
	case ai.APIOpenAICodexResponses:
		return ai.NewOpenAICodexResponsesProvider(ai.OpenAICodexResponsesConfig{APIKey: "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"account"}}`)) + ".signature", Model: model.ID, BaseURL: url})
	default:
		t.Fatal(fmt.Sprintf("unported API %s", model.API))
		return nil
	}
}
