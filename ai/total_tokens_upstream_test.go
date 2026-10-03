package ai

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTotalTokensUpstream(t *testing.T) {
	for _, tc := range []struct {
		provider, id string
		api          API
		oauth, cache bool
		effort       string
	}{
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:106
		{"anthropic", "claude-sonnet-4-5", "", false, true, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:129
		{"anthropic", "claude-sonnet-4-6", "", true, true, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:156
		{"openai", "gpt-4o-mini", APIOpenAICompletions, false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:180
		{"openai", "gpt-4o", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:199
		{"azure-openai-responses", "gpt-4o-mini", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:224
		{"google", "gemini-2.5-flash", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:247
		{"xai", "grok-4.3", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:266
		{"groq", "openai/gpt-oss-120b", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:289
		{"cerebras", "gpt-oss-120b", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:312
		{"cloudflare-workers-ai", "@cf/moonshotai/kimi-k2.6", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:337
		{"cloudflare-ai-gateway", "workers-ai/@cf/moonshotai/kimi-k2.6", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:362
		{"huggingface", "moonshotai/Kimi-K2.5", "", false, false, ""},
		// .upstream/v0.99.1/packages/ai/test/total-tokens.test.ts:381 (0.99.1 moved Together to Kimi-K3)
		{"together", "moonshotai/Kimi-K3", "", false, false, "high"},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:403
		{"baseten", "zai-org/GLM-5.2", "", false, false, "high"},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:425
		{"zai", "glm-5.2", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:444
		{"mistral", "devstral-medium-latest", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:467
		{"minimax", "MiniMax-M2.7", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:490
		{"xiaomi", "mimo-v2.5-pro", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:513
		{"xiaomi-token-plan-cn", "mimo-v2.5-pro", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:538
		{"xiaomi-token-plan-ams", "mimo-v2.5-pro", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:563
		{"xiaomi-token-plan-sgp", "mimo-v2.5-pro", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:588
		{"qwen-token-plan", "qwen3.7-max", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:613
		{"qwen-token-plan-individual", "qwen3.8-max", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:638
		{"qwen-token-plan-cn", "qwen3.7-max", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:663
		{"kimi-coding", "kimi-for-coding", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:686
		{"vercel-ai-gateway", "google/gemini-2.5-flash", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:709
		{"openrouter", "anthropic/claude-sonnet-4", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:726
		{"openrouter", "deepseek/deepseek-chat", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:743
		{"openrouter", "mistralai/mistral-small-3.2-24b-instruct", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:760
		{"openrouter", "google/gemini-2.5-flash", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:777 (duplicate upstream case retained)
		{"openrouter", "deepseek/deepseek-chat", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:800
		{"github-copilot", "claude-haiku-4.5", "", true, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:817
		{"github-copilot", "claude-sonnet-4.6", "", true, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:842
		{"amazon-bedrock", "global.anthropic.claude-sonnet-4-5-20250929-v1:0", "", false, false, ""},
		// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:865
		{"openai-codex", "gpt-5.5", "", true, false, ""},
	} {
		t.Run(tc.provider+"/"+tc.id+" should return totalTokens equal to sum of components", func(t *testing.T) {
			gm, ok := LookupModelExact(tc.provider + "/" + tc.id)
			if !ok {
				t.Fatal("missing upstream model")
			}
			model := *gm
			if tc.api != "" {
				model.API = tc.api
				model.Compat = nil
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := calls.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				question := "What is 2 + 2? Reply with just the number."
				answer := "4"
				if call == 2 {
					question = "What is 3 + 3? Reply with just the number."
					answer = "6"
				}
				if !strings.Contains(string(body), question) {
					t.Errorf("request %d lacks question: %s", call, body)
				}
				writeMatrixUsageResponse(t, w, model.API, answer, call == 2)
			}))
			defer server.Close()
			provider := newMatrixProvider(t, &model, server.URL, tc.oauth)
			defer func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			}()
			request := Context{SystemPrompt: totalTokensSystemPrompt(), Messages: []Message{UserMessage{Content: UserText("What is 2 + 2? Reply with just the number."), Timestamp: 1}}}
			options := StreamOptions{IsReasoning: model.Reasoning, ReasoningEffort: tc.effort, ModelCost: model.ToModel().CostRates(), Transport: TransportSSE, Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}}
			complete := func() *AssistantMessage {
				stream, err := provider.Stream(t.Context(), NormalizeContext(request), options)
				if err != nil {
					t.Fatal(err)
				}
				response := stream.Result()
				if response.StopReason != StopReasonStop {
					t.Fatalf("stopReason = %s, error = %s", response.StopReason, response.ErrorMessage)
				}
				usage := response.Usage
				if want := usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite; usage.TotalTokens != want {
					t.Fatalf("totalTokens = %d, components = %d: %+v", usage.TotalTokens, want, usage)
				}
				return response
			}
			first := complete()
			request.Messages = append(request.Messages, *first, UserMessage{Content: UserText("What is 3 + 3? Reply with just the number."), Timestamp: 2})
			second := complete()
			if tc.cache && second.Usage.CacheRead <= 0 && second.Usage.CacheWrite <= 0 && first.Usage.CacheWrite <= 0 {
				t.Fatal("Anthropic has no cache activity")
			}
			if got := calls.Load(); got != 2 {
				t.Fatalf("requests = %d, want 2", got)
			}
		})
	}
}

// .upstream/v0.87.1/packages/ai/test/total-tokens.test.ts:35-45
func totalTokensSystemPrompt() string {
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = "Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris."
	}
	return fmt.Sprintf("You are a helpful assistant. Be concise in your responses.\n\nHere is some additional context that makes this system prompt long enough to trigger caching:\n\n%s\n\nRemember: Always be helpful and concise.", strings.Join(lines, "\n\n"))
}
