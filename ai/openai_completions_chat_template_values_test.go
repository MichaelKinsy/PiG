package ai

import "testing"

// openai-completions.ts buildChatTemplateValues / resolveChatTemplateKwargValue (1034-1075) resolve a compat chat-template value the same way for
// compat.chatTemplateKwargs (the "chat-template" thinking format, line 906) and compat.chatTemplateArgs (the "baseten" format, line 916): a scalar passes through,
// {omitWhenOff} drops the key when reasoning is off, "thinking.enabled" is whether reasoning is on, "thinking.budget" is the clamped thinking budget, and
// "thinking.effort" is the mapped effort (thinkingLevelMap[level] ?? the level), or the mapped "off" value when reasoning is off, omitted when that is not a
// string.
func TestOpenAICompletionsChatTemplateValuesResolveTheSameForKwargsAndArgs(t *testing.T) {
	registerTestModel(t, GeneratedModel{
		ID: "template-model", Provider: "baseten", Reasoning: true, MaxOutputTokens: 4096,
		ThinkingLevelMap: map[ModelThinkingLevel]*string{ThinkingHigh: new("deep")},
	})
	values := map[string]any{
		"plain":    "kept",
		"enabled":  map[string]any{"$var": "thinking.enabled"},
		"budget":   map[string]any{"$var": "thinking.budget"},
		"effort":   map[string]any{"$var": "thinking.effort"},
		"dropped":  map[string]any{"$var": "thinking.enabled", "omitWhenOff": true},
		"droppedB": map[string]any{"$var": "thinking.budget", "omitWhenOff": true},
	}
	for _, tc := range []struct {
		format, field string
		compat        func() *OpenAICompat
	}{
		{"baseten", "chat_template_args", func() *OpenAICompat {
			return &OpenAICompat{ThinkingFormat: "baseten", SupportsReasoningEffort: new(true), MaxTokensField: "max_tokens", ChatTemplateArgs: values}
		}},
		{"chat-template", "chat_template_kwargs", func() *OpenAICompat {
			return &OpenAICompat{ThinkingFormat: "chat-template", SupportsReasoningEffort: new(true), MaxTokensField: "max_tokens", ChatTemplateKwargs: values}
		}},
	} {
		t.Run(tc.format+" on", func(t *testing.T) {
			request := captureOpenAIRequestMap(t, "baseten", "template-model", tc.compat(), StreamOptions{IsReasoning: true, Thinking: ThinkingLevelHigh, MaxTokens: 4096})
			got, _ := request[tc.field].(map[string]any)
			want := map[string]any{"plain": "kept", "enabled": true, "budget": float64(3072), "effort": "deep", "dropped": true, "droppedB": float64(3072)}
			if len(got) != len(want) {
				t.Fatalf("%s = %#v, want %#v", tc.field, got, want)
			}
			for key, value := range want {
				if got[key] != value {
					t.Errorf("%s[%q] = %#v, want %#v (all: %#v)", tc.field, key, got[key], value, got)
				}
			}
		})
		t.Run(tc.format+" off", func(t *testing.T) {
			request := captureOpenAIRequestMap(t, "baseten", "template-model", tc.compat(), StreamOptions{IsReasoning: true, MaxTokens: 4096})
			got, _ := request[tc.field].(map[string]any)
			want := map[string]any{"plain": "kept", "enabled": false}
			if len(got) != len(want) {
				t.Fatalf("%s = %#v, want %#v", tc.field, got, want)
			}
			for key, value := range want {
				if got[key] != value {
					t.Errorf("%s[%q] = %#v, want %#v (all: %#v)", tc.field, key, got[key], value, got)
				}
			}
		})
	}
}

// resolveChatTemplateKwargValue's effort (openai-completions.ts:1073-1074): thinkingLevelMap[level] when the map has the level, else the level itself; a null
// entry is omitted; reasoning off reads thinkingLevelMap.off and sends it only when it is a string.
//
// mutation-checked: reading the off entry for an on request, treating a null entry as the raw level, and sending "none" for an absent off entry each fail a row.
func TestOpenAICompletionsChatTemplateEffortFollowsTheThinkingLevelMap(t *testing.T) {
	registerTestModel(t, GeneratedModel{
		ID: "template-mapped", Provider: "baseten", Reasoning: true, MaxOutputTokens: 4096,
		ThinkingLevelMap: map[ModelThinkingLevel]*string{ThinkingOff: new("disabled"), ThinkingLow: nil},
	})
	values := map[string]any{"effort": map[string]any{"$var": "thinking.effort"}}
	for _, tc := range []struct {
		name    string
		options StreamOptions
		want    any
	}{
		{"off sends the mapped off value", StreamOptions{}, "disabled"},
		{"a null entry sends nothing (the native effort is not clamped)", StreamOptions{ReasoningEffort: "low"}, nil},
		{"an absent entry sends the level", StreamOptions{Thinking: ThinkingLevelMedium}, "medium"},
	} {
		for _, format := range []string{"baseten", "chat-template"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				compat := &OpenAICompat{ThinkingFormat: format, MaxTokensField: "max_tokens", SupportsReasoningEffort: new(false)}
				field := "chat_template_args"
				if format == "baseten" {
					compat.ChatTemplateArgs = values
				} else {
					compat.ChatTemplateKwargs = values
					field = "chat_template_kwargs"
				}
				request := captureOpenAIRequestMap(t, "baseten", "template-mapped", compat, withReasoning(tc.options))
				got, _ := request[field].(map[string]any)
				if got["effort"] != tc.want {
					t.Fatalf("%s = %#v, want effort %#v", field, request[field], tc.want)
				}
			})
		}
	}
}

func withReasoning(options StreamOptions) StreamOptions {
	options.IsReasoning, options.MaxTokens = true, 4096
	return options
}
