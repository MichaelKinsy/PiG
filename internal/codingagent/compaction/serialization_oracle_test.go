package compaction

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

func oracleCall(t *testing.T, name, arguments string) ai.ToolCall {
	t.Helper()
	call := ai.ToolCall{ID: "c", Name: name}
	if err := call.SetArgumentsJSON([]byte(arguments)); err != nil {
		t.Fatal(err)
	}
	return call
}

func oracleAssistant(blocks ...ai.AssistantContentBlock) ai.Message {
	return ai.AssistantMessage{Content: blocks, API: "anthropic-messages", Provider: "anthropic", Model: "m", StopReason: ai.StopReasonStop}
}

func oracleToolResult(text string) ai.Message {
	return ai.ToolResultMessage{ToolCallID: "c", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}}
}

// Pi's serializeConversation (core/compaction/utils.ts) runs against the vendored Pi module for the same messages.
// The truncation limit counts UTF-16 code units, tool calls print their arguments in the order JavaScript enumerates them with JSON.stringify values,
// and a text block that is empty still makes the `[Assistant]:` line.
func TestSerializeConversationMatchesPiOracle(t *testing.T) {
	cases := map[string][]ai.Message{
		"tool result 2000 chars":        {oracleToolResult(strings.Repeat("x", 2000))},
		"tool result 2001 chars":        {oracleToolResult(strings.Repeat("x", 2001))},
		"tool result latin1 2001 units": {oracleToolResult(strings.Repeat("é", 2001))},
		"tool result cjk 2500":          {oracleToolResult(strings.Repeat("小", 2500))},
		"tool result astral 2002 units": {oracleToolResult(strings.Repeat("😀", 1001))},
		"args member order":             {oracleAssistant(oracleCall(t, "f", `{"z":1,"a":2,"m":3}`))},
		"args integer keys first":       {oracleAssistant(oracleCall(t, "f", `{"b":1,"2":2,"1":3}`))},
		"args html and separators":      {oracleAssistant(oracleCall(t, "f", "{\"s\":\"<a href=\\\"x\\\">&</a>\\u2028\\u2029\"}"))},
		"args numbers":                  {oracleAssistant(oracleCall(t, "f", `{"a":1e21,"b":0.1,"c":-0,"d":12345678901234567890,"e":1.5e-7,"f":100,"g":[1,[2,{"h":null}]]}`))},
		"two calls":                     {oracleAssistant(oracleCall(t, "f", `{"x":1}`), oracleCall(t, "g", `{}`))},
		"thinking and text": {oracleAssistant(ai.ThinkingContent{Thinking: "a"}, ai.TextContent{Text: "t1"}, ai.ThinkingContent{Thinking: "b"},
			ai.TextContent{Text: "t2"})},
		"empty text block":     {oracleAssistant(ai.TextContent{Text: ""})},
		"empty then text":      {oracleAssistant(ai.TextContent{Text: ""}, ai.TextContent{Text: "x"})},
		"empty thinking":       {oracleAssistant(ai.ThinkingContent{Thinking: ""})},
		"user text":            {ai.UserMessage{Content: ai.UserText("hello")}},
		"user empty":           {ai.UserMessage{Content: ai.UserText("")}},
		"user blocks":          {ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: "a"}, ai.ImageContent{Data: "d", MimeType: "image/png"}, ai.TextContent{Text: "b"}}}},
		"tool result no text":  {ai.ToolResultMessage{ToolCallID: "c", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.ImageContent{Data: "d", MimeType: "image/png"}}}},
		"tool result 2 blocks": {ai.ToolResultMessage{ToolCallID: "c", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "a"}, ai.TextContent{Text: "b"}}}},
	}
	for name, messages := range cases {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(messages)
			if err != nil {
				t.Fatal(err)
			}
			var want string
			pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/compaction/utils.js");
emit(mod.serializeConversation(input));`, json.RawMessage(raw), &want)
			if got := SerializeConversation(messages); got != want {
				t.Errorf("SerializeConversation = %q\nPi                     = %q", got, want)
			}
		})
	}
}

// A tool result is cut at 2000 UTF-16 units, so a cut inside an emoji keeps the high surrogate as JavaScript's slice does.
func TestSerializeConversationKeepsASurrogateHalfAtTheCut(t *testing.T) {
	text := "x" + strings.Repeat("😀", 1000) // 2001 units
	units := jsstring.ToUTF16(SerializeConversation([]ai.Message{oracleToolResult(text)}))
	prefix := len(jsstring.ToUTF16("[Tool result]: "))
	if got := units[prefix+1999]; got != 0xD83D {
		t.Fatalf("last kept unit = %#x, want the high surrogate 0xd83d", got)
	}
	if tail := string(jsstring.FromUTF16(units[prefix+2000:])); tail != "\n\n[... 1 more characters truncated]" {
		t.Fatalf("marker = %q", tail)
	}
}
