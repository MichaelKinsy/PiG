package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

const thinkingDisablePrompt = "Before replying, carefully solve 36863 * 5279 internally. Then reply with the word pong repeated exactly 40 times, separated by single spaces. Do not add any other text."
const thinkingDisableSystem = "You are a precise assistant. Follow the requested output format exactly."

type thinkingDisableCase struct {
	name, provider, model, keyEnv, field, want string
	maxTokens, minPongs, maxOutput             int
	noTemperature                              bool
	env                                        ai.ProviderEnv
}

func thinkingDisableCases() []thinkingDisableCase {
	return []thinkingDisableCase{
		// .upstream/v0.87.1/packages/ai/test/google-thinking-disable.test.ts:93
		{name: "disables thinking for budget-based reasoning models", provider: "anthropic", model: "claude-sonnet-4-5", keyEnv: "PIG_LIVE_ANTHROPIC_API_KEY", field: "thinking", want: `{"type":"disabled"}`, maxTokens: 320},
		// .upstream/v0.87.1/packages/ai/test/google-thinking-disable.test.ts:99
		{name: "disables thinking for adaptive reasoning models", provider: "anthropic", model: "claude-sonnet-4-6", keyEnv: "PIG_LIVE_ANTHROPIC_API_KEY", field: "thinking", want: `{"type":"disabled"}`, maxTokens: 320},
		// .upstream/v0.87.1/packages/ai/test/google-thinking-disable.test.ts:107
		{name: "disables thinking for Gemini 2.5", provider: "google", model: "gemini-2.5-flash", keyEnv: "PIG_LIVE_GEMINI_API_KEY", field: "thinkingConfig", want: `{"thinkingBudget":0}`, maxTokens: 160},
		// .upstream/v0.87.1/packages/ai/test/google-thinking-disable.test.ts:111
		{name: "disables thinking for Gemini 3.x", provider: "google", model: "gemini-3-flash-preview", keyEnv: "PIG_LIVE_GEMINI_API_KEY", field: "thinkingConfig", want: `{"thinkingLevel":"MINIMAL"}`, maxTokens: 160},
		// .upstream/v0.87.1/packages/ai/test/google-thinking-disable.test.ts:115
		{name: "does not error when thinking is off for Gemini 3.1 Pro", provider: "google", model: "gemini-3.1-pro-preview", keyEnv: "PIG_LIVE_GEMINI_API_KEY", field: "thinkingConfig", want: `{"thinkingLevel":"LOW"}`, maxTokens: 512, minPongs: 20},
		// .upstream/v0.87.1/packages/ai/test/google-thinking-disable.test.ts:133
		{name: "disables thinking for Gemini 2.5", provider: "google-vertex", model: "gemini-2.5-flash", keyEnv: "PIG_LIVE_VERTEX_API_KEY", field: "thinkingConfig", want: `{"thinkingBudget":0}`, maxTokens: 160},
		// .upstream/v0.87.1/packages/ai/test/google-thinking-disable.test.ts:139
		{name: "disables thinking for Gemini 3.x", provider: "google-vertex", model: "gemini-3-flash-preview", keyEnv: "PIG_LIVE_VERTEX_API_KEY", field: "thinkingConfig", want: `{"thinkingLevel":"MINIMAL"}`, maxTokens: 160},
		// .upstream/v0.87.1/packages/ai/test/google-thinking-disable.test.ts:147
		{name: "disables thinking for Responses reasoning models", provider: "openai", model: "gpt-5.4-mini", keyEnv: "PIG_LIVE_OPENAI_API_KEY", field: "reasoning", want: `{"effort":"none"}`, maxTokens: 160, noTemperature: true},
		// .upstream/v0.87.1/packages/ai/test/google-thinking-disable.test.ts:155
		{name: "disables thinking for Qwen 3.5 reasoning models", provider: "openrouter", model: "qwen/qwen3.5-plus-02-15", keyEnv: "PIG_LIVE_OPENROUTER_API_KEY", field: "reasoning", want: `{"effort":"none"}`, maxTokens: 160, maxOutput: 100},
	}
}

func TestThinkingDisableUpstream(t *testing.T) {
	for _, tc := range thinkingDisableCases() {
		t.Run(tc.provider+"/"+tc.name, func(t *testing.T) {
			t.Run("hermetic", func(t *testing.T) {
				server := thinkingDisableFauxEndpoint(t, tc)
				runThinkingDisable(t, t.Context(), tc, "test", server.URL)
			})
		})
	}
}

func runThinkingDisable(t *testing.T, ctx context.Context, tc thinkingDisableCase, key, baseURL string) {
	t.Helper()
	dir := t.TempDir()
	if baseURL != "" {
		config, err := json.Marshal(map[string]any{"providers": map[string]any{tc.provider: map[string]any{"baseUrl": baseURL}}})
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "models.json"), config, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	model, err := BuildModel(tc.provider+"/"+tc.model, services)
	if err != nil {
		t.Fatal(err)
	}
	stream := services.ModelRuntime().StreamSimple(ctx, model, ai.Context{SystemPrompt: thinkingDisableSystem, Messages: []ai.Message{ai.UserMessage{Content: ai.UserText(thinkingDisablePrompt)}}}, ai.StreamOptions{APIKey: key, Env: tc.env, MaxTokens: tc.maxTokens, TemperatureSet: !tc.noTemperature})
	thinkingEvents, thinkingChars := 0, 0
	for event := range stream.Events(ctx) {
		switch event := event.(type) {
		case ai.ThinkingStartEvent, ai.ThinkingEndEvent:
			thinkingEvents++
		case ai.ThinkingDeltaEvent:
			thinkingEvents++
			thinkingChars += len(event.Delta)
		}
	}
	response := stream.Result()
	if response.StopReason != ai.StopReasonStop {
		t.Fatalf("response=%#v", response)
	}
	if thinkingEvents != 0 || thinkingChars != 0 {
		t.Fatalf("thinking events=%d chars=%d", thinkingEvents, thinkingChars)
	}
	var text strings.Builder
	for _, block := range response.Content {
		switch block := block.(type) {
		case ai.ThinkingContent:
			t.Fatal("thinking block present")
		case ai.TextContent:
			text.WriteString(block.Text)
		}
	}
	pongs := len(regexp.MustCompile(`(?i)\bpong\b`).FindAllString(text.String(), -1))
	minimum := tc.minPongs
	if minimum == 0 {
		minimum = 35
	}
	if pongs < minimum {
		t.Fatalf("pongs=%d want>=%d; text=%q", pongs, minimum, text.String())
	}
	if tc.maxOutput != 0 && response.Usage.Output >= tc.maxOutput {
		t.Fatalf("output tokens=%d want<%d", response.Usage.Output, tc.maxOutput)
	}
}

func thinkingDisableFauxEndpoint(t *testing.T, tc thinkingDisableCase) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			http.Error(w, err.Error(), 400)
			return
		}
		var expected any
		if err := json.Unmarshal([]byte(tc.want), &expected); err != nil {
			t.Error(err)
			http.Error(w, err.Error(), 400)
			return
		}
		actual := payload[tc.field]
		if tc.field == "thinkingConfig" {
			config, _ := payload["generationConfig"].(map[string]any)
			actual = config[tc.field]
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("disabled request=%#v want=%#v", actual, expected)
			http.Error(w, "thinking is not disabled", 400)
			return
		}
		encoded, _ := json.Marshal(payload)
		if !strings.Contains(string(encoded), thinkingDisablePrompt) || !strings.Contains(string(encoded), thinkingDisableSystem) {
			t.Error("upstream prompt changed")
			http.Error(w, "prompt changed", 400)
			return
		}
		text, _ := json.Marshal(strings.TrimSpace(strings.Repeat("pong ", 40)))
		w.Header().Set("Content-Type", "text/event-stream")
		switch {
		case tc.provider == "anthropic":
			_, _ = fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"reply\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%s}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":80}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", text)
		case tc.field == "thinkingConfig":
			_, _ = fmt.Fprintf(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":%s}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":80,\"totalTokenCount\":81}}\n\n", text)
		case tc.provider == "openai":
			_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"reply\",\"content\":[]}}\n\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"reply\",\"content\":[{\"type\":\"output_text\",\"text\":%s}]}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":80,\"total_tokens\":81}}}\n\n", text)
		default:
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%s},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":80}}\n\n", text)
		}
	}))
	t.Cleanup(server.Close)
	return server
}
