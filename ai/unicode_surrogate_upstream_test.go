package ai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type unicodeUpstreamCase struct {
	name, provider, model string
	kind                  int
	oauth                 bool
	effort                string
}

func TestUnicodeSurrogateUpstream(t *testing.T) {
	for _, tc := range unicodeUpstreamCases() {
		t.Run(tc.provider+"/"+tc.model+"/"+tc.name, func(t *testing.T) {
			model, ok := LookupModelExact(tc.provider + "/" + tc.model)
			if !ok {
				t.Fatal("missing upstream model")
			}
			request, text := unicodeToolRequest(model, tc.kind)
			expected := text
			if tc.kind == 2 {
				expected = strings.ReplaceAll(text, "\xed\xa0\xbd", "")
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := decodeMatrixRequest(r)
				if err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				// The faux endpoint validates the original text and Pi's sanitized unpaired-code-unit result before responding.
				if !jsonContainsText(body, expected) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error":{"message":"tool text was lost or changed"}}`)
					return
				}
				writeMatrixUsageResponse(t, w, model.API, "The tool returned text.", false)
			}))
			defer server.Close()
			provider := newMatrixProvider(t, model, server.URL, tc.oauth)
			defer func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			}()
			options := StreamOptions{IsReasoning: model.Reasoning, ReasoningEffort: tc.effort, ModelCost: model.ToModel().CostRates(), Transport: TransportSSE, Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}}
			stream, err := provider.Stream(t.Context(), NormalizeContext(request), options)
			if err != nil {
				t.Fatal(err)
			}
			response := stream.Result()
			if response.StopReason == StopReasonError || response.ErrorMessage != "" {
				t.Fatalf("stopReason=%s error=%s", response.StopReason, response.ErrorMessage)
			}
			if tc.kind == 1 {
				hasText := false
				for _, block := range response.Content {
					if _, ok := block.(TextContent); ok {
						hasText = true
					}
				}
				if !hasText {
					t.Fatal("missing text response")
				}
			} else if len(response.Content) == 0 {
				t.Fatal("empty response")
			}
		})
	}
}

func jsonContainsText(value any, text string) bool {
	switch value := value.(type) {
	case string:
		return strings.Contains(value, text)
	case []any:
		for _, item := range value {
			if jsonContainsText(item, text) {
				return true
			}
		}
	case map[string]any:
		for _, item := range value {
			if jsonContainsText(item, text) {
				return true
			}
		}
	}
	return false
}

// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:35,121,209
func unicodeToolRequest(model *GeneratedModel, kind int) (Context, string) {
	toolID, toolName, description := "test_1", "test_tool", "A test tool"
	user, followup := "Use the test tool", "Summarize the tool result briefly."
	text := `Test with emoji 🙈 and other characters:
- Monkey emoji: 🙈
- Thumbs up: 👍
- Heart: ❤️
- Thinking face: 🤔
- Rocket: 🚀
- Mixed text: Mario Zechner wann? Wo? Bin grad äußersr eventuninformiert 🙈
- Japanese: こんにちは
- Chinese: 你好
- Mathematical symbols: ∑∫∂√
- Special quotes: "curly" 'quotes'`
	if kind == 1 {
		toolID, toolName, description = "linkedin_1", "linkedin_skill", "Get LinkedIn comments"
		user, followup = "Use the linkedin tool to get comments", "How many comments are there?"
		text = `Post: Hab einen "Generative KI für Nicht-Techniker" Workshop gebaut.
Unanswered Comments: 2

=> {
  "comments": [
    {
      "author": "Matthias Neumayer's  graphic link",
      "text": "Leider nehmen das viel zu wenige Leute ernst"
    },
    {
      "author": "Matthias Neumayer's  graphic link",
      "text": "Mario Zechner wann? Wo? Bin grad äußersr eventuninformiert 🙈"
    }
  ]
}`
	} else if kind == 2 {
		toolID = "test_2"
		followup = "What did the tool return?"
		// WTF-8 encodes the same lone UTF-16 code unit as String.fromCharCode(0xd83d); a Go rune conversion would replace it before the provider is exercised.
		text = "Text with unpaired surrogate: \xed\xa0\xbd <- should be sanitized"
	}
	if model.Provider == "mistral" {
		toolID = []string{"testtool1", "linkedin1", "testtool2"}[kind]
	}
	return Context{SystemPrompt: "You are a helpful assistant.", Tools: []ToolSchema{{Name: toolName, Description: description, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}}, Messages: []Message{
		UserMessage{Content: UserText(user), Timestamp: 1},
		AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: toolID, Name: toolName, Arguments: JsonObject{}}}, API: model.API, Provider: model.Provider, Model: model.ID, StopReason: StopReasonToolUse, Timestamp: 2},
		ToolResultMessage{ToolCallID: toolID, ToolName: toolName, Content: []ToolResultMessageContent{TextContent{Text: text}}, IsError: false, Timestamp: 3},
		UserMessage{Content: UserText(followup), Timestamp: 4},
	}}, text
}
