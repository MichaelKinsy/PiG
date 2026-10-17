package ai

import "testing"

// transform-messages.ts:205-210 starts an empty result-id set for each assistant message with tool calls, so a result recorded before that message, while no call was pending, does not answer its calls: the call still gets a synthetic "No result provided" result.
func TestTransformMessagesResultBeforeCallDoesNotAnswerIt(t *testing.T) {
	model := replayTarget()
	input := []Message{
		UserMessage{Content: UserText("start")},
		ToolResultMessage{ToolCallID: "one", ToolName: "tool", Content: []ToolResultMessageContent{TextContent{Text: "early"}}},
		AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: "one", Name: "tool", Arguments: JsonObject{}}}, StopReason: StopReasonToolUse},
		UserMessage{Content: UserText("next")},
	}
	result := TransformMessages(input, model, nil)
	if len(result) != 5 {
		t.Fatalf("result=%#v, want a synthetic result after the call", result)
	}
	synthetic, ok := result[3].(ToolResultMessage)
	if !ok || synthetic.ToolCallID != "one" || !synthetic.IsError || len(synthetic.Content) != 1 || synthetic.Content[0] != (TextContent{Text: "No result provided"}) {
		t.Fatalf("result[3]=%#v, want the synthetic result of call one", result[3])
	}
}
