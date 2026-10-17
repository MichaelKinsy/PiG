package agent

import (
	"math"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// assertMessageAppendStringifyMatches checks that value's AppendStringify appends what the JSON.stringify mode writes for its MarshalJSON
// output, alone and spliced into an enclosing value.
func assertMessageAppendStringifyMatches(t *testing.T, name string, value json.StringifyAppender) {
	t.Helper()
	marshaled, err := value.MarshalJSON()
	if err != nil {
		t.Fatalf("%s: MarshalJSON: %v", name, err)
	}
	want, err := json.MarshalStringify(json.RawMessage(marshaled))
	if err != nil {
		t.Fatal(err)
	}
	appended, err := value.AppendStringify([]byte("prefix:"))
	if err != nil || string(appended) != "prefix:"+string(want) {
		t.Fatalf("%s: AppendStringify = %s, %v\nwant prefix:%s", name, appended, err, want)
	}
	spliced, err := json.MarshalStringify(map[string]any{"message": value})
	if err != nil || string(spliced) != `{"message":`+string(want)+`}` {
		t.Fatalf("%s: spliced %s, %v\nwant {\"message\":%s}", name, spliced, err, want)
	}
}

// Every AgentMessage variant, built in memory or decoded from text it keeps, appends what its MarshalJSON writes in the JSON.stringify mode.
func TestAgentMessageAppendStringifyMatchesMarshalJSON(t *testing.T) {
	text := "<b> & \u2028 \xed\xa0\x80 \xff \x01 \"q\""
	endTurn, duration := true, int64(-0)
	content := []ai.AssistantContentBlock{
		ai.TextContent{Text: text}, ai.ThinkingContent{Thinking: text, ThinkingSignature: "sig"},
		ai.ToolCall{ID: "c1", Name: "write", Arguments: ai.JsonObject{"content": text, "n": math.Copysign(0, -1)}},
	}
	assistant := &AssistantMessage{Role: RoleAssistant, Content: content, Provider: "p", ModelID: "m", API: "openai-completions",
		Usage: &ai.Usage{Input: 1, Output: 2, TotalTokens: 3}, StopReason: ai.StopReasonToolUse, Timestamp: 5, ResponseID: text,
		EndTurn: &endTurn, DurationMs: &duration}
	errorFirst := *assistant
	errorFirst.ErrorMessage, errorFirst.ErrorBeforeTimestamp = text, true
	toolResult := &ToolResultMessage{ToolCallID: "c1", ToolName: "write", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text},
		ai.ImageContent{Data: "AAAA", MimeType: "image/png"}}, Details: map[string]any{"z": text, "a": math.Copysign(0, -1)}, Timestamp: 6}
	nullDetails := *toolResult
	nullDetails.Details, nullDetails.DetailsNull = nil, true
	messages := map[string]AgentMessage{
		"user text":          {User: &UserMessage{Role: RoleUser, Content: ai.UserText(text), Timestamp: 1}},
		"user blocks":        {User: &UserMessage{Role: RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: 1}},
		"user nil content":   {User: &UserMessage{Role: RoleUser, Timestamp: 1}},
		"assistant":          {Assistant: assistant},
		"assistant no usage": {Assistant: &AssistantMessage{Role: RoleAssistant, Content: content[:1], Timestamp: 2}},
		"assistant error":    {Assistant: &errorFirst},
		"tool result":        {ToolResult: toolResult},
		"tool result null":   {ToolResult: &nullDetails},
		"custom":             {Custom: map[string]any{"role": RoleCustom, "customType": "x", "content": text, "display": true, "timestamp": 3}},
	}
	for _, line := range []string{
		`{"timestamp":1,"content":[{"text":"hi <b> & \u00e9 \ud800","type":"text"}],"role":"user"}`,
		`{"role":"assistant","content":[{"type":"text","text":"a\u2028b"}],"api":"a","provider":"p","model":"m","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":2,"unread":-0}`,
		`{"role":"toolResult","toolCallId":"c1","toolName":"read","isError":false,"content":[{"type":"text","text":"ok"}],"details":{"b":1,"a":2},"timestamp":3}`,
		`{"timestamp":4,"role":"bashExecution","command":"<ls>","output":"x","exitCode":0,"cancelled":false,"truncated":false}`,
	} {
		var message AgentMessage
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatal(err)
		}
		messages["decoded "+line] = message
	}
	for name, message := range messages {
		assertMessageAppendStringifyMatches(t, name, message)
	}
	assertMessageAppendStringifyMatches(t, "AssistantMessage", *assistant)
	assertMessageAppendStringifyMatches(t, "ToolResultMessage", *toolResult)
	if _, err := (AgentMessage{}).AppendStringify(nil); err == nil {
		t.Fatal("AppendStringify accepted a message with no variant")
	}
}

// TextBytes counts the strings that make up a message's JSON, so a buffer sized from it holds the encoded message's bulk.
func TestAgentMessageTextBytes(t *testing.T) {
	message := AgentMessage{Assistant: &AssistantMessage{Role: RoleAssistant, Content: []ai.AssistantContentBlock{
		ai.TextContent{Text: "12345"}, ai.ToolCall{ID: "c", Arguments: ai.JsonObject{"ab": "123", "c": []any{"1", 2.0}}}}}}
	if got := message.TextBytes(); got != 5+2+3+1+1 {
		t.Fatalf("TextBytes = %d, want 12", got)
	}
	cyclic := map[string]any{}
	cyclic["self"], cyclic["again"] = cyclic, cyclic
	if got := (AgentMessage{Custom: cyclic}).TextBytes(); got <= 0 {
		t.Fatalf("TextBytes of a cyclic custom message = %d, want a bounded positive count", got)
	}
}
