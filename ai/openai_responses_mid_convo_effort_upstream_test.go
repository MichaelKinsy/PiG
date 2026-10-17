package ai

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

// Ports packages/ai/test/openai-responses-mid-convo-effort.test.ts (Pi 60ac3bcc, #10853): GPT-6 on the openai provider
// keeps reasoning.effort fixed and sends the thinking level as configuration_update input items.

func midConvoEffortConfig(compat *OpenAIResponsesCompat) OpenAIResponsesConfig {
	return OpenAIResponsesConfig{
		Model: "gpt-6-luna", ProviderID: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "sk-test", IsReasoning: true, Compat: compat,
		ThinkingLevelMap: ThinkingLevelMap{ThinkingOff: new("none"), ThinkingMinimal: nil, ThinkingLow: new("low"), ThinkingMedium: new("medium"), ThinkingHigh: new("high"), ThinkingXHigh: new("xhigh"), ThinkingMax: new("max")},
	}
}

func midConvoEffortReply(withToolCall bool) string {
	item := `{"type":"message","id":"msg_resp_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"answer","annotations":[]}]}`
	if withToolCall {
		item = `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{}","status":"completed"}`
	}
	return fmt.Sprintf("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"+
		"data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":%s}\n\n"+
		"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":%s}\n\n"+
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\"}}\n\n", item, item)
}

type midConvoEffortPayload struct {
	Input     []map[string]any `json:"input"`
	Reasoning *struct {
		Effort string `json:"effort"`
	} `json:"reasoning"`
}

func runMidConvoEffort(t *testing.T, cfg OpenAIResponsesConfig, messages []Message, effort string, withToolCall bool) (midConvoEffortPayload, AssistantMessage) {
	t.Helper()
	raw, _, message := captureResponsesCompat(t, cfg, Context{Messages: messages}, StreamOptions{ReasoningEffort: effort}, midConvoEffortReply(withToolCall))
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var payload midConvoEffortPayload
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	if message.StopReason == StopReasonError {
		t.Fatalf("stream error: %s", message.ErrorMessage)
	}
	return payload, *message
}

func midConvoUpdate(effort string) map[string]any {
	return map[string]any{"type": "configuration_update", "reasoning": map[string]any{"effort": effort}}
}

func midConvoUpdates(input []map[string]any) []map[string]any {
	var out []map[string]any
	for _, item := range input {
		if item["type"] == "configuration_update" {
			out = append(out, item)
		}
	}
	return out
}

func midConvoUser(text string) Message { return UserMessage{Content: UserText(text)} }

func TestOpenAIResponsesConfigurationUpdateUpstream(t *testing.T) {
	enabled := &OpenAIResponsesCompat{SupportsMidConvoEffort: new(true)}

	t.Run("keeps reasoning.effort constant and only appends input when the level changes", func(t *testing.T) {
		cfg := midConvoEffortConfig(enabled)
		history := []Message{midConvoUser("one")}
		first, firstMessage := runMidConvoEffort(t, cfg, history, "high", false)
		history = append(history, firstMessage, midConvoUser("two"))
		second, secondMessage := runMidConvoEffort(t, cfg, history, "high", false)
		history = append(history, secondMessage, midConvoUser("three"))
		third, thirdMessage := runMidConvoEffort(t, cfg, history, "low", false)

		want := []map[string]any{{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "one"}}}, midConvoUpdate("high")}
		if !reflect.DeepEqual(first.Input, want) {
			t.Fatalf("first input = %#v", first.Input)
		}
		for i, payload := range []midConvoEffortPayload{first, second, third} {
			if payload.Reasoning == nil || payload.Reasoning.Effort != "medium" {
				t.Fatalf("request %d reasoning = %#v, want effort medium", i, payload.Reasoning)
			}
		}
		if !reflect.DeepEqual(second.Input[:len(first.Input)], first.Input) || !reflect.DeepEqual(third.Input[:len(second.Input)], second.Input) {
			t.Fatal("earlier input changed")
		}
		if got := midConvoUpdates(third.Input); !reflect.DeepEqual(got, []map[string]any{midConvoUpdate("high"), midConvoUpdate("high"), midConvoUpdate("low")}) {
			t.Fatalf("updates = %#v", got)
		}
		if got := third.Input[len(third.Input)-1]; !reflect.DeepEqual(got, midConvoUpdate("low")) {
			t.Fatalf("last item = %#v", got)
		}
		if got := []string{firstMessage.ProviderThinkingLevel, secondMessage.ProviderThinkingLevel, thirdMessage.ProviderThinkingLevel}; !reflect.DeepEqual(got, []string{"high", "high", "low"}) {
			t.Fatalf("providerThinkingLevel = %v", got)
		}
	})

	t.Run("applies a level change made during a tool loop to the next reply", func(t *testing.T) {
		cfg := midConvoEffortConfig(enabled)
		history := []Message{midConvoUser("one")}
		toolCall, toolCallMessage := runMidConvoEffort(t, cfg, history, "high", true)
		history = append(history, toolCallMessage, ToolResultMessage{ToolCallID: "call_1|fc_1", ToolName: "lookup", Content: []ToolResultMessageContent{TextContent{Text: "result"}}})
		afterTool, afterToolMessage := runMidConvoEffort(t, cfg, history, "low", false)

		if !reflect.DeepEqual(afterTool.Input[:len(toolCall.Input)], toolCall.Input) {
			t.Fatal("earlier input changed")
		}
		tail := afterTool.Input[len(afterTool.Input)-2:]
		if tail[0]["type"] != "function_call_output" || tail[0]["call_id"] != "call_1" || !reflect.DeepEqual(tail[1], midConvoUpdate("low")) {
			t.Fatalf("tail = %#v", tail)
		}
		if afterToolMessage.ProviderThinkingLevel != "low" {
			t.Fatalf("providerThinkingLevel = %q", afterToolMessage.ProviderThinkingLevel)
		}
	})

	t.Run("adds items only before replies recorded by this model", func(t *testing.T) {
		_, unstamped := runMidConvoEffort(t, midConvoEffortConfig(&OpenAIResponsesCompat{}), []Message{midConvoUser("one")}, "high", false)
		otherProvider, otherModel, recorded := unstamped, unstamped, unstamped
		otherProvider.Provider, otherProvider.ProviderThinkingLevel = "opencode", "high"
		otherModel.Model, otherModel.ProviderThinkingLevel = "gpt-6-sol", "high"
		recorded.ProviderThinkingLevel = "high"

		payload, _ := runMidConvoEffort(t, midConvoEffortConfig(enabled), []Message{
			midConvoUser("one"), unstamped, midConvoUser("two"), otherProvider, midConvoUser("three"), otherModel, midConvoUser("four"), recorded, midConvoUser("five"),
		}, "low", false)

		var kinds []string
		for _, item := range payload.Input {
			kind, _ := item["type"].(string)
			if kind == "" {
				kind, _ = item["role"].(string)
			}
			kinds = append(kinds, kind)
		}
		if want := []string{"user", "message", "user", "message", "user", "message", "user", "configuration_update", "message", "user", "configuration_update"}; !reflect.DeepEqual(kinds, want) {
			t.Fatalf("items = %v, want %v", kinds, want)
		}
		if got := midConvoUpdates(payload.Input); !reflect.DeepEqual(got, []map[string]any{midConvoUpdate("high"), midConvoUpdate("low")}) {
			t.Fatalf("updates = %#v", got)
		}
	})

	for _, tc := range []struct {
		name, apiKey, level, effort string
		compat                      *OpenAIResponsesCompat
	}{
		{name: "Sign in with ChatGPT", apiKey: "chatgpt-access-token", compat: enabled, level: "low", effort: "low"},
		{name: "the compat flag turned off", apiKey: "sk-test", compat: &OpenAIResponsesCompat{SupportsMidConvoEffort: new(false)}, level: "low", effort: "low"},
		{name: "thinking off", apiKey: "sk-test", compat: enabled, effort: "none"},
	} {
		t.Run("sends the level as reasoning.effort with "+tc.name, func(t *testing.T) {
			cfg := midConvoEffortConfig(tc.compat)
			cfg.APIKey = tc.apiKey
			_, first := runMidConvoEffort(t, cfg, []Message{midConvoUser("one")}, "high", false)
			payload, message := runMidConvoEffort(t, cfg, []Message{midConvoUser("one"), first, midConvoUser("two")}, tc.level, false)
			if payload.Reasoning == nil || payload.Reasoning.Effort != tc.effort {
				t.Fatalf("reasoning = %#v, want effort %q", payload.Reasoning, tc.effort)
			}
			if len(midConvoUpdates(payload.Input)) != 0 {
				t.Fatalf("configuration_update items sent: %#v", payload.Input)
			}
			if message.ProviderThinkingLevel != "" {
				t.Fatalf("providerThinkingLevel = %q, want none", message.ProviderThinkingLevel)
			}
		})
	}
}
