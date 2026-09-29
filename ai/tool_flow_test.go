package ai

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

// transform-messages.ts:162-253 closes pending calls at assistant/user/end boundaries and holds system messages until those calls have results.
func TestProviderToolFlowBoundaries(t *testing.T) {
	call := AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: "call", Name: "calculate", Arguments: JsonObject{}}}, StopReason: StopReasonToolUse}
	real := ToolResultMessage{ToolCallID: "call", ToolName: "calculate", Content: []ToolResultMessageContent{TextContent{Text: "42"}}, Timestamp: 1}
	user := UserMessage{Content: UserText("next")}
	system := SystemMessage{Content: SystemText("update")}
	done := AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "done"}}, StopReason: StopReasonStop}
	for _, tc := range []struct {
		name     string
		messages []Message
		want     []string
	}{
		{"empty", nil, nil},
		{"trailing call", []Message{call}, []string{"assistant", "tool:call:calculate:No result provided:true"}},
		{"new user", []Message{call, user}, []string{"assistant", "tool:call:calculate:No result provided:true", "user"}},
		{"new assistant", []Message{call, done}, []string{"assistant", "tool:call:calculate:No result provided:true", "assistant"}},
		{"existing result", []Message{call, real, user}, []string{"assistant", "tool:call:calculate:42:false", "user"}},
		{"system behind real result", []Message{call, system, real, user}, []string{"assistant", "tool:call:calculate:42:false", "system", "user"}},
		{"system behind missing result", []Message{call, system, user}, []string{"assistant", "tool:call:calculate:No result provided:true", "system", "user"}},
		{"late result is not moved", []Message{call, user, real}, []string{"assistant", "tool:call:calculate:No result provided:true", "user", "tool:call:calculate:42:false"}},
		{"reused call ID", []Message{call, real, call}, []string{"assistant", "tool:call:calculate:42:false", "assistant", "tool:call:calculate:No result provided:true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := cloneMessages(tc.messages)
			before := time.Now().UnixMilli()
			result := prepareProviderToolFlow(NormalizeContext(Context{Messages: tc.messages})).Messages()
			after := time.Now().UnixMilli()
			var got []string
			for _, message := range result {
				if tool, ok := message.(ToolResultMessage); ok {
					got = append(got, fmt.Sprintf("tool:%s:%s:%s:%t", tool.ToolCallID, tool.ToolName, ContentText(tool.Content), tool.IsError))
					if tool.IsError && (tool.Timestamp < before || tool.Timestamp > after) {
						t.Errorf("synthetic timestamp = %d", tool.Timestamp)
					}
				} else {
					got = append(got, message.messageRole())
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("messages = %q, want %q", got, tc.want)
			}
			if !reflect.DeepEqual(tc.messages, original) {
				t.Fatal("repair mutated original history")
			}
		})
	}
	for _, reason := range []StopReason{StopReasonError, StopReasonAborted} {
		t.Run(string(reason), func(t *testing.T) {
			failed := call
			failed.StopReason = reason
			result := prepareProviderToolFlow(NormalizeContext(Context{Messages: []Message{failed, real}})).Messages()
			if len(result) != 1 {
				t.Fatalf("result = %#v", result)
			}
			if _, ok := result[0].(ToolResultMessage); !ok {
				t.Fatalf("raw API must retain the orphaned result as Pi does: %#v", result)
			}
		})
	}
}
