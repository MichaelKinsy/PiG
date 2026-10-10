package agent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Upstream convertToLlm (coding-agent core/messages.ts) turns every custom
// message into a user message whose content is a block array: bash executions,
// branch and compaction summaries as one text block, a custom message's string
// content as one text block (empty included) and its block-array content as
// those blocks. Only a bash execution excluded from context sends nothing. The
// block form reaches the provider converters, which serialize a string and a
// block array differently (Anthropic and Completions send a string as a plain
// string), so the content shape is part of the request bytes.
func TestConvertToLLMCustomMessagesMatchUpstreamConvertToLlm(t *testing.T) {
	image := map[string]any{"type": "image", "data": "aGk=", "mimeType": "image/png"}
	for _, tc := range []struct {
		name   string
		custom map[string]any
		want   []ai.Message
	}{
		{
			name:   "branch summary",
			custom: map[string]any{"role": RoleBranchSummary, "summary": "s", "fromId": "x", "timestamp": int64(5)},
			want:   []ai.Message{ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: BranchSummaryContextText("s")}}, Timestamp: 5}},
		},
		{
			name:   "compaction summary",
			custom: map[string]any{"role": RoleCompactionSummary, "summary": "s", "tokensBefore": 1, "timestamp": int64(5)},
			want:   []ai.Message{ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: CompactionSummaryContextText("s")}}, Timestamp: 5}},
		},
		{
			name:   "bash execution",
			custom: map[string]any{"role": RoleBashExecution, "command": "ls", "output": "x", "exitCode": float64(0), "timestamp": int64(5)},
			want:   []ai.Message{ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: "Ran `ls`\n```\nx\n```"}}, Timestamp: 5}},
		},
		{
			name:   "excluded bash execution",
			custom: map[string]any{"role": RoleBashExecution, "command": "ls", "output": "x", "excludeFromContext": true, "timestamp": int64(5)},
			want:   []ai.Message{},
		},
		{
			name:   "custom string",
			custom: map[string]any{"role": RoleCustom, "customType": "note", "content": "hello", "timestamp": int64(5)},
			want:   []ai.Message{ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: "hello"}}, Timestamp: 5}},
		},
		{
			name:   "custom empty string",
			custom: map[string]any{"role": RoleCustom, "customType": "note", "content": "", "timestamp": int64(5)},
			want:   []ai.Message{ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: ""}}, Timestamp: 5}},
		},
		{
			name:   "custom decoded block array",
			custom: map[string]any{"role": RoleCustom, "customType": "note", "content": []any{map[string]any{"type": "text", "text": "look"}, image}, "timestamp": float64(5)},
			want: []ai.Message{ai.UserMessage{Content: ai.UserContentBlocks{
				ai.TextContent{Text: "look"}, ai.ImageContent{Data: "aGk=", MimeType: "image/png"},
			}, Timestamp: 5}},
		},
		{
			name:   "custom typed block array",
			custom: map[string]any{"role": RoleCustom, "customType": "note", "content": []ai.UserContentBlock{ai.TextContent{Text: "typed"}}, "timestamp": int64(5)},
			want:   []ai.Message{ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: "typed"}}, Timestamp: 5}},
		},
		{
			name:   "custom empty block array",
			custom: map[string]any{"role": RoleCustom, "customType": "note", "content": []any{}, "timestamp": int64(5)},
			want:   []ai.Message{ai.UserMessage{Content: ai.UserContentBlocks{}, Timestamp: 5}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := convertToLLM([]AgentMessage{{Custom: tc.custom}}, nil)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("convertToLLM = %#v\nwant %#v", got, tc.want)
			}
		})
	}
}

// Shapes recorded from pinned Pi 0.87.1: convertToLlm (coding-agent
// dist/core/messages.js) then transformMessages (pi-ai
// dist/api/transform-messages.js) over the same histories. Any custom message
// closes a pending tool flow, because upstream converts it to a user message
// before transformMessages runs; a system message is held until the flow
// closes.
func TestConvertToLLMToolFlowMatchesUpstreamTransformOrder(t *testing.T) {
	calls := func(ids ...string) AgentMessage {
		blocks := make([]ai.AssistantContentBlock, 0, len(ids))
		for _, id := range ids {
			blocks = append(blocks, ai.ToolCall{ID: id, Name: "read", Arguments: ai.JsonObject{}})
		}
		return AgentMessage{Assistant: &AssistantMessage{Role: "assistant", Content: blocks, StopReason: "toolUse"}}
	}
	user, result := boundaryUserPrompt, boundaryToolResult
	system := AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText("upd"), Timestamp: 9}}
	summary := AgentMessage{Custom: map[string]any{"role": RoleBranchSummary, "summary": "x", "fromId": "f", "timestamp": int64(5)}}
	custom := func(content any) AgentMessage {
		return AgentMessage{Custom: map[string]any{"role": RoleCustom, "customType": "n", "content": content, "timestamp": int64(5)}}
	}
	excluded := AgentMessage{Custom: map[string]any{"role": RoleBashExecution, "command": "ls", "output": "x", "exitCode": float64(0), "excludeFromContext": true, "timestamp": int64(5)}}
	for _, tc := range []struct {
		name    string
		history []AgentMessage
		want    string
	}{
		{"branchSummary", []AgentMessage{user("s"), calls("a", "b"), result("a"), summary, user("p")}, "user:s assistant toolResult:a toolResult:b! user:The followin user:p"},
		{"system", []AgentMessage{user("s"), calls("a", "b"), result("a"), system, user("p")}, "user:s assistant toolResult:a toolResult:b! system user:p"},
		{"excludedBash", []AgentMessage{user("s"), calls("a", "b"), result("a"), excluded, result("b"), user("p")}, "user:s assistant toolResult:a toolResult:b user:p"},
		{"customString", []AgentMessage{user("s"), calls("a", "b"), result("a"), custom("hi"), user("p")}, "user:s assistant toolResult:a toolResult:b! user:hi user:p"},
		{"customArray", []AgentMessage{user("s"), calls("a", "b"), result("a"), custom([]any{map[string]any{"type": "text", "text": "hi"}}), user("p")}, "user:s assistant toolResult:a toolResult:b! user:hi user:p"},
		{"customEmpty", []AgentMessage{user("s"), calls("a", "b"), result("a"), custom(""), user("p")}, "user:s assistant toolResult:a toolResult:b! user: user:p"},
		{"systemThenResult", []AgentMessage{user("s"), calls("a", "b"), result("a"), system, result("b"), user("p")}, "user:s assistant toolResult:a toolResult:b system user:p"},
		{"systemAfterAll", []AgentMessage{user("s"), calls("a"), result("a"), system, calls("c"), result("c"), user("p")}, "user:s assistant toolResult:a system assistant toolResult:c user:p"},
		{"systemThenSummary", []AgentMessage{user("s"), calls("a", "b"), result("a"), system, summary, user("p")}, "user:s assistant toolResult:a toolResult:b! system user:The followin user:p"},
		{"summaryThenSystem", []AgentMessage{user("s"), calls("a", "b"), result("a"), summary, system, user("p")}, "user:s assistant toolResult:a toolResult:b! user:The followin system user:p"},
		{"trailingSystem", []AgentMessage{user("s"), calls("a", "b"), result("a"), system}, "user:s assistant toolResult:a toolResult:b! system"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := convertToLLM(tc.history, nil)
			if got := upstreamOrderShape(wire); got != tc.want {
				t.Fatalf("order = %q\nwant  %q (pinned Pi)", got, tc.want)
			}
			if problem := wireToolFlowError(wire); problem != "" {
				t.Fatal(problem)
			}
		})
	}
}

// upstreamOrderShape renders wire in the oracle notation: role, tool call id,
// "!" for an error result and up to 12 bytes of user text.
func upstreamOrderShape(wire []ai.Message) string {
	shape := make([]string, 0, len(wire))
	for _, message := range wire {
		switch message := message.(type) {
		case ai.ToolResultMessage:
			entry := "toolResult:" + message.ToolCallID
			if message.IsError {
				entry += "!"
			}
			shape = append(shape, entry)
		case ai.AssistantMessage:
			shape = append(shape, "assistant")
		case ai.SystemMessage:
			shape = append(shape, "system")
		case ai.UserMessage:
			text := ""
			switch content := message.Content.(type) {
			case ai.UserText:
				text = string(content)
			case ai.UserContentBlocks:
				for _, block := range content {
					if block, ok := block.(ai.TextContent); ok {
						text += block.Text
					}
				}
			}
			shape = append(shape, "user:"+text[:min(len(text), 12)])
		}
	}
	return strings.Join(shape, " ")
}

// The user-message content each custom message puts on the wire, recorded from
// pinned Pi 0.87.1 (convertToLlm, then the pi-ai openai-completions and
// anthropic-messages stream functions against a capturing server). Upstream
// sends every custom message as a text-block array, never as a plain string.
// The contents are compared as decoded JSON: Pig's encoder escapes "<" as
// \u003c where JSON.stringify does not, which is the same JSON value.
func TestCustomMessagesReachProvidersAsUpstreamContentBlocks(t *testing.T) {
	history := []AgentMessage{
		boundaryUserPrompt("start"),
		{Assistant: &AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "ok"}}, StopReason: "stop"}},
		{Custom: map[string]any{"role": RoleBranchSummary, "summary": "went elsewhere", "fromId": "f", "timestamp": int64(3)}},
		{Custom: map[string]any{"role": RoleCustom, "customType": "n", "content": "note", "display": true, "timestamp": int64(4)}},
		{Custom: map[string]any{"role": RoleCustom, "customType": "n", "content": []any{map[string]any{"type": "text", "text": "blocks"}}, "display": true, "timestamp": int64(4)}},
		{Custom: map[string]any{"role": RoleBashExecution, "command": "ls", "output": "x", "exitCode": float64(0), "timestamp": int64(5)}},
		boundaryUserPrompt("next"),
	}
	want := map[string]string{
		"openai-completions": `[[{"type":"text","text":"start"}],[{"type":"text","text":"The following is a summary of a branch that this conversation came back from:\n\n<summary>\nwent elsewhere</summary>"}],[{"type":"text","text":"note"}],[{"type":"text","text":"blocks"}],[{"type":"text","text":"Ran ` + "`ls`" + `\n` + "```" + `\nx\n` + "```" + `"}],[{"type":"text","text":"next"}]]`,
		"anthropic-messages": `[[{"type":"text","text":"start"}],[{"type":"text","text":"The following is a summary of a branch that this conversation came back from:\n\n<summary>\nwent elsewhere</summary>"}],[{"type":"text","text":"note"}],[{"type":"text","text":"blocks"}],[{"type":"text","text":"Ran ` + "`ls`" + `\n` + "```" + `\nx\n` + "```" + `"}],[{"type":"text","text":"next","cache_control":{"type":"ephemeral"}}]]`,
	}
	for _, builder := range providerBuilders() {
		expected, ok := want[builder.name]
		if !ok {
			continue
		}
		t.Run(builder.name, func(t *testing.T) {
			body := captureStreamWire(t, builder.sse, builder.make, convertToLLM(history, nil))
			var request struct {
				Messages []struct {
					Role    string          `json:"role"`
					Content json.RawMessage `json:"content"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(body, &request); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			var contents []json.RawMessage
			for _, message := range request.Messages {
				if message.Role == "user" {
					contents = append(contents, message.Content)
				}
			}
			got, err := json.Marshal(contents)
			if err != nil {
				t.Fatal(err)
			}
			var gotValue, wantValue any
			if err := json.Unmarshal(got, &gotValue); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(expected), &wantValue); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotValue, wantValue) {
				t.Fatalf("user contents =\n%s\nwant (pinned Pi)\n%s", got, expected)
			}
		})
	}
}
