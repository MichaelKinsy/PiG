package agent

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsonopts"
)

// viaMarshalJSON hides MarshalJSONTo, so encoding/json marshals the message through MarshalJSON as it did before MarshalJSONTo existed.
type viaMarshalJSON struct{ message json.Marshaler }

func (v viaMarshalJSON) MarshalJSON() ([]byte, error) { return v.message.MarshalJSON() }

func marshalJSONToMessages() map[string]AgentMessage {
	lone := "lone \xed\xa0\x80 unit"
	assistant := func(text string, call ai.ToolCall) *AssistantMessage {
		return &AssistantMessage{
			Role: RoleAssistant, API: "openai-responses", Provider: "openai", ModelID: "gpt-5",
			Content: []ai.AssistantContentBlock{
				ai.ThinkingContent{Thinking: text, ThinkingSignature: "sig<&>"},
				ai.TextContent{Text: text, TextSignature: "t"},
				call,
			},
			Usage:      &ai.Usage{Input: 1000, Output: 50, CacheRead: 900, TotalTokens: 1950},
			StopReason: "toolUse", Timestamp: 1_700_000_000_000,
		}
	}
	call := ai.ToolCall{ID: "call-1", Name: "read", Arguments: ai.JsonObject{"path": "a<b>&c\u2028", "n": 1.5, "nested": []any{"x", nil, true}}}
	return map[string]AgentMessage{
		"assistant":        {Assistant: assistant("considering <b>&amp; é \u2029", call)},
		"assistant lone":   {Assistant: assistant(lone, call)},
		"assistant empty":  {Assistant: &AssistantMessage{Role: RoleAssistant, StopReason: "stop"}},
		"user text":        {User: &UserMessage{Role: RoleUser, Content: ai.UserText("hi <&>"), Timestamp: 1}},
		"user blocks":      {User: &UserMessage{Role: RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: lone}, ai.ImageContent{Data: "QUJD", MimeType: "image/png"}}, Timestamp: 2}},
		"tool result":      {ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "c", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "<done>"}}, Details: map[string]any{"lines": 3.0}, Timestamp: 3}},
		"tool result null": {ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "c", ToolName: "read", DetailsNull: true, IsError: true, Timestamp: 4}},
		"custom":           {Custom: map[string]any{"role": "custom", "customType": "x", "content": "<&>", "timestamp": 5.0}},
	}
}

// encoding/json writes the same bytes through MarshalJSONTo as through MarshalJSON, with its own options and with the JSON.stringify ones.
func TestMarshalJSONToWritesWhatMarshalJSONWrites(t *testing.T) {
	for name, message := range marshalJSONToMessages() {
		t.Run(name, func(t *testing.T) {
			want, err := json.Marshal(viaMarshalJSON{message})
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatalf("json.Marshal\n got: %s\nwant: %s", got, want)
			}
			want, err = jsonv2.Marshal(viaMarshalJSON{message}, jsonopts.Stringify)
			if err != nil {
				t.Fatal(err)
			}
			got, err = jsonv2.Marshal(message, jsonopts.Stringify)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatalf("jsonv2.Marshal(Stringify)\n got: %s\nwant: %s", got, want)
			}
			// As a member and as an element, where the encoder reaches the message without a copy.
			type holder struct {
				Messages []AgentMessage `json:"messages"`
				Last     *AgentMessage  `json:"last"`
			}
			type holderOld struct {
				Messages []viaMarshalJSON `json:"messages"`
				Last     viaMarshalJSON   `json:"last"`
			}
			want, err = json.Marshal(holderOld{[]viaMarshalJSON{{message}, {message}}, viaMarshalJSON{message}})
			if err != nil {
				t.Fatal(err)
			}
			got, err = json.Marshal(holder{[]AgentMessage{message, message}, &message})
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatalf("nested\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

func TestMarshalJSONToReportsTheErrorMarshalJSONReports(t *testing.T) {
	_, wantErr := json.Marshal(viaMarshalJSON{AgentMessage{}})
	_, err := json.Marshal(AgentMessage{})
	if wantErr == nil || err == nil {
		t.Fatalf("an empty message marshaled: %v, %v", err, wantErr)
	}
}

func TestMarshalJSONToOfAssistantAndToolResult(t *testing.T) {
	messages := marshalJSONToMessages()
	assistant, toolResult := *messages["assistant"].Assistant, *messages["tool result"].ToolResult
	for name, pair := range map[string][2]any{
		"assistant":   {assistant, viaMarshalJSON{assistant}},
		"tool result": {toolResult, viaMarshalJSON{toolResult}},
	} {
		got, err := json.Marshal(pair[0])
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(pair[1])
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s\n got: %s\nwant: %s", name, got, want)
		}
	}
}
