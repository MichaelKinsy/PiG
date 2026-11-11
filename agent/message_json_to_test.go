package agent

import (
	"bytes"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"math"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

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
	negZero := math.Copysign(0, -1)
	zeroCall := ai.ToolCall{ID: "call-z", Name: "read", Arguments: ai.JsonObject{"neg": negZero, "nested": []any{negZero, map[string]any{"z": negZero}}}}
	zeroAssistant := assistant("inv \xff <b> \xc0\xaf", zeroCall)
	zeroAssistant.Usage = &ai.Usage{Input: 1, TotalTokens: 1, Cost: ai.UsageCost{Input: negZero, Output: -0.5, Total: negZero}}
	zeroAssistant.ErrorMessage = "bad \xff"
	return map[string]AgentMessage{
		// Negative zero and invalid UTF-8 are written as JSON.stringify writes them: 0, and the replacement character itself.
		"assistant negative zero and invalid UTF-8": {Assistant: zeroAssistant},
		"user invalid UTF-8":                        {User: &UserMessage{Role: RoleUser, Content: ai.UserText("bad \xff byte"), Timestamp: 6}},
		"user escape before fffd":                   {User: &UserMessage{Role: RoleUser, Content: ai.UserText("say \"fffd\"\nFFFD \\fffd"), Timestamp: 8}},
		"tool result negative zero":                 {ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "c\xff", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "x\xfe"}}, Details: map[string]any{"z": negZero, "s": "\xff"}, Timestamp: 7}},
		"tool result underflow":                     {ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "c", ToolName: "read", Details: json.RawMessage(`{"x":-1e-400}`), Timestamp: 9}},
		"custom negative zero":                      {Custom: map[string]any{"role": "custom", "customType": "x", "content": "\xff", "timestamp": negZero}},
		"assistant":                                 {Assistant: assistant("considering <b>&amp; é \u2029", call)},
		"assistant lone":                            {Assistant: assistant(lone, call)},
		"assistant empty":                           {Assistant: &AssistantMessage{Role: RoleAssistant, StopReason: "stop"}},
		"user text":                                 {User: &UserMessage{Role: RoleUser, Content: ai.UserText("hi <&>"), Timestamp: 1}},
		"user blocks":                               {User: &UserMessage{Role: RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: lone}, ai.ImageContent{Data: "QUJD", MimeType: "image/png"}}, Timestamp: 2}},
		"tool result":                               {ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "c", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "<done>"}}, Details: map[string]any{"lines": 3.0}, Timestamp: 3}},
		"tool result null":                          {ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "c", ToolName: "read", DetailsNull: true, IsError: true, Timestamp: 4}},
		"custom":                                    {Custom: map[string]any{"role": "custom", "customType": "x", "content": "<&>", "timestamp": 5.0}},
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
			if !bytes.Equal(got, want) {
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
			if !bytes.Equal(got, want) {
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
			if !bytes.Equal(got, want) {
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
		if !bytes.Equal(got, want) {
			t.Fatalf("%s\n got: %s\nwant: %s", name, got, want)
		}
	}
}

// The bytes are the ones Pi's JSON.stringify writes: negative zero is 0 and a byte that is not UTF-8 is the replacement character, raw, from
// every encoder that reaches a message.
func TestMessageMarshalJSONWritesWhatJSONStringifyWrites(t *testing.T) {
	for name, message := range marshalJSONToMessages() {
		if !strings.Contains(name, "negative zero") && !strings.Contains(name, "invalid UTF-8") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			direct, err := message.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			viaJSON, err := json.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			viaStringify, err := jsonv2.Marshal(message, jsonopts.Stringify)
			if err != nil {
				t.Fatal(err)
			}
			forms := map[string]string{"MarshalJSON": string(direct), "json.Marshal": string(viaJSON), "Stringify": string(viaStringify)}
			if message.Assistant != nil {
				assistant, err := message.Assistant.MarshalJSON()
				if err != nil {
					t.Fatal(err)
				}
				forms["AssistantMessage.MarshalJSON"] = string(assistant)
			}
			for form, text := range forms {
				if negativeZero.MatchString(text) {
					t.Errorf("%s writes a negative zero: %s", form, text)
				}
				if strings.Contains(text, `\ufffd`) || !utf8.ValidString(text) {
					t.Errorf("%s writes the replacement character as an escape or keeps an ill-formed byte: %s", form, text)
				}
			}
		})
	}
}

// negativeZero matches a JSON number that is a negative zero without a fraction or an exponent.
var negativeZero = regexp.MustCompile(`(^|[^0-9eE.])-0([^.0-9eE]|$)`)
