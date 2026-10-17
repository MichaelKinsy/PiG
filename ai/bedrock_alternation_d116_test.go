//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"testing"

	btypes "github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime/types"
)

// D116: Bedrock Converse requires the roles of a conversation to alternate (it rejects two consecutive user messages with a ValidationException). Pi 1.1.0 (bedrock-converse-stream.ts convertMessages) pushes every user message and every run of tool results as a message of its own, so a user prompt right after tool results (the usual shape after an abort) or after a dropped assistant turn gives two user messages. PiG merges adjacent messages of one role into one message with the blocks in order.
func TestBedrockMergesAdjacentSameRoleMessagesD116(t *testing.T) {
	call := AssistantMessage{API: APIBedrockConverseStream, Provider: "amazon-bedrock", Model: "us.anthropic.claude-sonnet-4-5-20250929-v1:0", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: "tool-1", Name: "read", Arguments: JsonObject{}}}}
	result := ToolResultMessage{ToolCallID: "tool-1", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "done"}}}
	for _, tc := range []struct {
		name     string
		messages []Message
		want     []btypes.ConversationRole
		blocks   []int
	}{
		{"user prompt after tool results", []Message{UserMessage{Content: UserText("go")}, call, result, UserMessage{Content: UserText("next")}}, []btypes.ConversationRole{"user", "assistant", "user"}, []int{1, 1, 2}},
		{"two user messages", []Message{UserMessage{Content: UserText("a")}, UserMessage{Content: UserText("b")}}, []btypes.ConversationRole{"user"}, []int{2}},
		{"user messages around a dropped assistant", []Message{UserMessage{Content: UserText("a")}, AssistantMessage{}, UserMessage{Content: UserText("b")}}, []btypes.ConversationRole{"user"}, []int{2}},
		{"already alternating", []Message{UserMessage{Content: UserText("a")}, AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "x"}}}, UserMessage{Content: UserText("b")}}, []btypes.ConversationRole{"user", "assistant", "user"}, []int{1, 1, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := convertBedrockMessages(tc.messages, "us.anthropic.claude-sonnet-4-5-20250929-v1:0", "Claude Sonnet 4.5 (US)", bedrockCacheNone, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("roles = %v, want %v", bedrockRoles(got), tc.want)
			}
			for i, message := range got {
				if message.Role != tc.want[i] || len(message.Content) != tc.blocks[i] {
					t.Fatalf("message %d = %s with %d blocks, want %s with %d (roles %v)", i, message.Role, len(message.Content), tc.want[i], tc.blocks[i], bedrockRoles(got))
				}
			}
		})
	}
	// The tool result stays first in the merged user message: Converse wants toolResult blocks right after the toolUse message.
	got, _ := convertBedrockMessages([]Message{UserMessage{Content: UserText("go")}, call, result, UserMessage{Content: UserText("next")}}, "us.anthropic.claude-sonnet-4-5-20250929-v1:0", "Claude Sonnet 4.5 (US)", bedrockCacheNone, nil)
	if len(got) == 3 {
		if _, ok := got[2].Content[0].(*btypes.ContentBlockMemberToolResult); !ok {
			t.Fatalf("merged user message starts with %T, want the tool result", got[2].Content[0])
		}
	}
}

func bedrockRoles(messages []btypes.Message) []btypes.ConversationRole {
	roles := make([]btypes.ConversationRole, 0, len(messages))
	for _, message := range messages {
		roles = append(roles, message.Role)
	}
	return roles
}
