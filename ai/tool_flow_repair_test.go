package ai

import (
	"fmt"
	"strings"
	"testing"
)

// shape renders messages as "u", "a(ids)", "r:id", "s" so a test reads the order of a request.
func shape(messages []Message) string {
	var parts []string
	for _, message := range messages {
		switch typed := message.(type) {
		case UserMessage:
			parts = append(parts, "u")
		case SystemMessage:
			parts = append(parts, "s")
		case AssistantMessage:
			var ids []string
			for _, block := range typed.Content {
				if call, ok := block.(ToolCall); ok {
					ids = append(ids, call.ID)
				}
			}
			label := "a"
			if typed.StopReason == StopReasonError || typed.StopReason == StopReasonAborted {
				label = "a!"
			}
			parts = append(parts, fmt.Sprintf("%s(%s)", label, strings.Join(ids, ",")))
		case ToolResultMessage:
			parts = append(parts, "r:"+typed.ToolCallID)
		}
	}
	return strings.Join(parts, " ")
}

func TestRepairToolFlow(t *testing.T) {
	user := UserMessage{Content: UserContentBlocks{TextContent{Text: "x"}}}
	system := SystemMessage{Content: SystemText("s")}
	call := func(stop StopReason, ids ...string) AssistantMessage {
		message := AssistantMessage{StopReason: stop}
		for _, id := range ids {
			message.Content = append(message.Content, ToolCall{ID: id, Name: "t"})
		}
		return message
	}
	result := func(id string) ToolResultMessage { return ToolResultMessage{ToolCallID: id, ToolName: "t"} }
	for _, tc := range []struct {
		name     string
		messages []Message
		want     string
	}{
		{"clean cycle is unchanged", []Message{user, call(StopReasonToolUse, "a", "b"), result("a"), result("b"), user}, "u a(a,b) r:a r:b u"},
		{"no tool result is unchanged", []Message{user, call(StopReasonStop), user}, "u a() u"},
		{"missing result is left for the placeholder", []Message{user, call(StopReasonToolUse, "a", "b"), result("a"), user}, "u a(a,b) r:a u"},
		{"orphan result", []Message{user, result("ghost"), user}, "u u"},
		{"result of an errored turn is an orphan", []Message{user, call(StopReasonError, "e"), result("e"), user}, "u a!(e) u"},
		{"result of an aborted turn is an orphan", []Message{user, call(StopReasonAborted, "g"), result("g"), user}, "u a!(g) u"},
		{"repeated result", []Message{user, call(StopReasonToolUse, "a"), result("a"), result("a"), user}, "u a(a) r:a u"},
		{"out of order result takes the slot of its call", []Message{user, call(StopReasonToolUse, "a", "b"), result("a"), user, result("b")}, "u a(a,b) r:a r:b u"},
		{"result after the next assistant turn", []Message{user, call(StopReasonToolUse, "a"), call(StopReasonToolUse, "c"), result("c"), result("a")}, "u a(a) r:a a(c) r:c"},
		{"id reused every turn", []Message{user, call(StopReasonToolUse, "a"), result("a"), call(StopReasonToolUse, "a"), result("a"), result("a")}, "u a(a) r:a a(a) r:a"},
		{"id reused after a failed turn", []Message{user, call(StopReasonError, "a"), call(StopReasonToolUse, "a"), result("a")}, "u a!(a) a(a) r:a"},
		{"answered id reused by a call answered late", []Message{user, call(StopReasonToolUse, "a"), result("a"), call(StopReasonToolUse, "a"), user, result("a")}, "u a(a) r:a a(a) r:a u"},
		{"late result moved while a later turn reuses its id", []Message{user, call(StopReasonToolUse, "a"), user, result("a"), call(StopReasonToolUse, "a"), user, result("a")}, "u a(a) r:a u a(a) r:a u"},
		{"result consumed in order is not recovered for a later call", []Message{user, call(StopReasonToolUse, "x"), user, call(StopReasonToolUse, "y"), result("y"), call(StopReasonToolUse, "y"), user}, "u a(x) u a(y) r:y a(y) u"},
		{"trailing call reusing an answered id", []Message{user, call(StopReasonToolUse, "a"), result("a"), call(StopReasonToolUse, "a")}, "u a(a) r:a a(a)"},
		{"reused id answered in its own turn stays there", []Message{user, call(StopReasonToolUse, "a"), user, call(StopReasonToolUse, "a"), result("a")}, "u a(a) u a(a) r:a"},
		{"reused id in the next turn keeps its result", []Message{user, call(StopReasonToolUse, "a"), call(StopReasonToolUse, "a"), result("a")}, "u a(a) a(a) r:a"},
		{"system update stays", []Message{user, call(StopReasonToolUse, "a", "b"), result("a"), system, user, result("b")}, "u a(a,b) r:a s r:b u"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := RepairToolFlow(tc.messages)
			if shape(got) != tc.want {
				t.Fatalf("got  %s\nwant %s", shape(got), tc.want)
			}
		})
	}
}

// A request with nothing to repair is the same slice, so a long transcript costs no copy.
func TestRepairToolFlowKeepsAWellFormedTranscript(t *testing.T) {
	messages := []Message{
		UserMessage{Content: UserContentBlocks{TextContent{Text: "x"}}},
		AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: "a", Name: "t"}}, StopReason: StopReasonToolUse},
		ToolResultMessage{ToolCallID: "a", ToolName: "t"},
	}
	if got := RepairToolFlow(messages); &got[0] != &messages[0] {
		t.Fatal("a well-formed transcript was copied")
	}
}
