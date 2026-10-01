package ai_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

const overflowLorem = "Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum. "

// All case names/lines come from the independent pinned inventory. The exact oversized inputs run through real provider converters and deterministic HTTP outcomes; vendor-side enforcement and local model installation remain live-only.
func TestContextOverflowMatrixUpstream(t *testing.T) {
	cases := upstreamCaseSites(t, "packages/ai/test/context-overflow.test.ts")
	specs := []struct{ provider, model, kind, pattern string }{
		{"anthropic", "claude-haiku-4-5", "anthropic", "prompt is too long"}, {"anthropic", "claude-sonnet-4-6", "anthropic", "prompt is too long"},
		{"github-copilot", "", "copilot", `exceeds the limit of \d+`}, {"github-copilot", "claude-sonnet-4.6", "copilot", `exceeds the limit of \d+|input is too long`},
		{"openai", "gpt-4o-mini", "completions", "maximum context length"}, {"openai", "gpt-4o", "responses", "exceeds the context window"},
		{"azure-openai-responses", "gpt-4o-mini", "responses", "context|maximum"}, {"google", "gemini-2.5-flash", "google", "input token count.*exceeds the maximum"},
		{"openai-codex", "gpt-5.5", "responses", ""}, {"amazon-bedrock", "global.anthropic.claude-sonnet-4-5-20250929-v1:0", "bedrock", ""},
		{"xai", "grok-4.3", "xai", `maximum prompt length is \d+`}, {"groq", "llama-3.3-70b-versatile", "groq", "reduce the length of the messages"},
		{"cerebras", "gpt-oss-120b", "cerebras", `4(00|13|29).*\(no body\)`}, {"huggingface", "moonshotai/Kimi-K2.5", "generic", ""},
		{"together", "moonshotai/Kimi-K3", "together", ""}, {"zai", "glm-5.2", "zai", ""},
		{"mistral", "devstral-medium-latest", "mistral", `too large for model with \d+ maximum context length`}, {"minimax", "MiniMax-M2.7", "minimax", ""},
		{"xiaomi", "mimo-v2.5-pro", "length", ""}, {"xiaomi-token-plan-cn", "mimo-v2.5-pro", "length", ""},
		{"xiaomi-token-plan-ams", "mimo-v2.5-pro", "length", ""}, {"xiaomi-token-plan-sgp", "mimo-v2.5-pro", "length", ""},
		{"qwen-token-plan", "qwen3.7-max", "qwen", "input length"}, {"qwen-token-plan-individual", "qwen3.8-max", "qwen", "input length"},
		{"qwen-token-plan-cn", "qwen3.7-max", "qwen", "input length"}, {"kimi-coding", "kimi-for-coding", "kimi", ""},
		{"vercel-ai-gateway", "google/gemini-2.5-flash", "generic", ""}, {"openrouter", "anthropic/claude-sonnet-4", "completions", `maximum context length is \d+ tokens`},
		{"openrouter", "deepseek/deepseek-v3.2", "completions", `maximum context length is \d+ tokens`}, {"openrouter", "mistralai/mistral-large", "completions", `maximum context length is \d+ tokens`},
		{"openrouter", "google/gemini-2.5-flash", "completions", `maximum context length is \d+ tokens`}, {"openrouter", "meta-llama/llama-4-scout", "completions", `maximum context length is \d+ tokens`},
		{"ollama", "gpt-oss:20b", "ollama", ""}, {"lm-studio", "local-model", "lmstudio", ""}, {"llama.cpp", "local-model", "llamacpp", ""},
	}
	if len(cases) != len(specs) {
		t.Fatalf("unported matrix change: %d cases, %d specs", len(cases), len(specs))
	}
	for index, tc := range cases {
		t.Run(tc.ID, func(t *testing.T) {
			t.Logf(".upstream/current/packages/ai/test/context-overflow.test.ts:%d", tc.Line)
			spec := specs[index]
			var model ai.GeneratedModel
			if spec.provider == "ollama" || spec.provider == "lm-studio" || spec.provider == "llama.cpp" {
				window := 128000
				if spec.provider == "lm-studio" {
					window = 8192
				}
				if spec.provider == "llama.cpp" {
					window = 4096
				}
				model = ai.GeneratedModel{Provider: spec.provider, ID: spec.model, API: ai.APIOpenAICompletions, ContextWindow: window}
			} else {
				id := spec.model
				if id == "" {
					for _, candidate := range ai.ListModels(spec.provider) {
						if strings.HasPrefix(candidate.ID, "gemini-") {
							id = candidate.ID
							break
						}
					}
				}
				found, ok := ai.LookupModelExact(spec.provider + "/" + id)
				if !ok {
					t.Fatalf("upstream model missing: %s/%s", spec.provider, id)
				}
				model = *found
				if spec.provider == "openai" && spec.kind == "completions" {
					model.API = ai.APIOpenAICompletions
				}
			}
			// .upstream/v0.99.1/packages/ai/test/context-overflow.test.ts:31-36.
			targetChars := (model.ContextWindow + 10000) * 6
			content := strings.Repeat(overflowLorem, (targetChars+len(overflowLorem)-1)/len(overflowLorem))
			kinds := []string{spec.kind}
			if spec.kind == "zai" {
				kinds = []string{"zai", "silent"}
			}
			if spec.kind == "ollama" {
				kinds = []string{"ollama", "truncated"}
			}
			for _, kind := range kinds {
				t.Run(kind, func(t *testing.T) {
					var received, payloadIntact atomic.Bool
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if _, err := io.Copy(io.Discard, r.Body); err != nil {
							t.Error(err)
						}
						received.Store(true)
						if kind == "length" || kind == "silent" || kind == "truncated" {
							reason, input := "stop", model.ContextWindow+1
							if kind == "length" {
								reason = "length"
								input = model.ContextWindow
							}
							if kind == "truncated" {
								input = model.ContextWindow - 1
							}
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":%q}],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":0}}\n\ndata: [DONE]\n\n", reason, input)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("X-Amzn-Errortype", "ValidationException")
						w.WriteHeader(http.StatusBadRequest)
						if kind != "cerebras" {
							message := overflowFixtureMessage(kind, model.ContextWindow)
							_, _ = fmt.Fprintf(w, `{"error":{"type":"invalid_request_error","message":%q},"message":%q}`, message, message)
						}
					}))
					t.Cleanup(server.Close)
					services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
					if err != nil {
						t.Fatal(err)
					}
					provider := matrixProvider(t, &model, server.URL)
					response := services.ModelRuntime().Complete(t.Context(), &ai.Model{ID: model.ID, Provider: provider}, ai.Context{SystemPrompt: "You are a helpful assistant.", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText(content)}}}, ai.StreamOptions{Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}, Transport: ai.TransportSSE, OnPayload: func(payload any, _ *ai.Model) (any, error) {
						raw, err := json.Marshal(payload)
						payloadIntact.Store(err == nil && strings.Contains(string(raw), content))
						return nil, err
					}})
					// Inspect before transport encoding: Codex legitimately sends a zstd body.
					if !received.Load() || !payloadIntact.Load() {
						t.Fatal("provider dropped or changed the oversized input")
					}
					switch kind {
					case "length":
						if response.StopReason != ai.StopReasonLength || response.Usage.Output != 0 {
							t.Fatalf("length result=%#v", response)
						}
					case "silent":
						if response.StopReason != ai.StopReasonStop || response.Usage.Input <= model.ContextWindow {
							t.Fatalf("silent usage=%#v", response)
						}
					case "truncated":
						if response.StopReason != ai.StopReasonStop || response.Usage.Input == 0 {
							t.Fatalf("truncated=%#v", response)
						}
						return
					default:
						if response.StopReason != ai.StopReasonError {
							t.Fatalf("stopReason=%s", response.StopReason)
						}
					}
					if spec.pattern != "" && !regexp.MustCompile("(?i)"+spec.pattern).MatchString(response.ErrorMessage) {
						t.Fatalf("error %q does not match %q", response.ErrorMessage, spec.pattern)
					}
					if !ai.IsContextOverflow(*response, model.ContextWindow) {
						t.Fatalf("overflow not detected: %#v", response)
					}
				})
			}
		})
	}
}

func overflowFixtureMessage(kind string, window int) string {
	switch kind {
	case "anthropic":
		return fmt.Sprintf("prompt is too long: %d tokens > %d maximum", window+10000, window)
	case "copilot":
		return fmt.Sprintf("prompt token count of %d exceeds the limit of %d", window+10000, window)
	case "completions":
		return fmt.Sprintf("This model's maximum context length is %d tokens", window)
	case "responses":
		return "Your input exceeds the context window of this model"
	case "google":
		return fmt.Sprintf("The input token count (%d) exceeds the maximum number of tokens allowed (%d)", window+10000, window)
	case "bedrock":
		return "Input is too long for requested model"
	case "xai":
		return fmt.Sprintf("This model's maximum prompt length is %d but the request contains %d tokens", window, window+10000)
	case "groq":
		return "Please reduce the length of the messages or completion"
	case "together":
		return fmt.Sprintf("The input (%d tokens) is longer than the model's context length (%d tokens).", window+10000, window)
	case "zai":
		return "model_context_window_exceeded"
	case "mistral":
		return fmt.Sprintf("Prompt contains %d tokens, too large for model with %d maximum context length", window+10000, window)
	case "minimax":
		return "invalid params, context window exceeds limit"
	case "qwen":
		return fmt.Sprintf("Range of input length should be [1, %d]", window)
	case "kimi":
		return fmt.Sprintf("Your request exceeded model token limit: %d (requested: %d)", window, window+10000)
	case "ollama":
		return "prompt too long; exceeded max context length by 10000 tokens"
	case "lmstudio":
		return "tokens to keep from the initial prompt is greater than the context length"
	case "llamacpp":
		return "the request exceeds the available context size, try increasing it"
	default:
		return "context_length_exceeded"
	}
}
