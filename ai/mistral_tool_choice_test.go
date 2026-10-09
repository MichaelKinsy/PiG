package ai

import (
	"reflect"
	"testing"
)

// openai-completions.ts:869 `if (options?.toolChoice) params.tool_choice = options.toolChoice`: an empty string is falsy, so the request carries no tool_choice; a non-empty choice is sent as given.
func TestOpenAICompletionsOmitsAnEmptyToolChoiceLikePi(t *testing.T) {
	tests := []struct {
		name   string
		choice any
		want   any
		sent   bool
	}{
		{"absent", nil, nil, false},
		{"empty string", "", nil, false},
		{"required", "required", "required", true},
		{"function object", map[string]any{"type": "function", "function": map[string]any{"name": "f"}}, map[string]any{"type": "function", "function": map[string]any{"name": "f"}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := captureOpenAIRequestMap(t, "openai", "gpt-4o", nil, StreamOptions{ToolChoice: tc.choice})
			got, sent := request["tool_choice"]
			if sent != tc.sent || !reflect.DeepEqual(got, tc.want) && tc.sent {
				t.Errorf("tool_choice = %#v (sent %v), want %#v (sent %v)", got, sent, tc.want, tc.sent)
			}
		})
	}
}

// anthropic-messages.ts:1273-1278 `if (options?.toolChoice)`: a string choice becomes {type: choice}, an object is sent as given, and an empty string is falsy so no tool_choice is sent.
func TestAnthropicToolChoiceMatchesPi(t *testing.T) {
	model := upstreamCatalogModel(t, "anthropic", "claude-sonnet-4-5")
	tests := []struct {
		name   string
		choice any
		want   string
	}{
		{"absent", nil, ""},
		{"empty string", "", ""},
		{"auto", "auto", `{"type":"auto"}`},
		{"any", "any", `{"type":"any"}`},
		{"tool object", map[string]any{"type": "tool", "name": "f"}, `{"name":"f","type":"tool"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := upstreamAnthropicParams(t, model, Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}, StreamOptions{ToolChoice: tc.choice})
			got, sent := payload["tool_choice"]
			if tc.want == "" {
				if sent {
					t.Fatalf("tool_choice = %s, want none", got)
				}
				return
			}
			if !sent || string(got) != tc.want {
				t.Errorf("tool_choice = %s (sent %v), want %s", got, sent, tc.want)
			}
		})
	}
}
