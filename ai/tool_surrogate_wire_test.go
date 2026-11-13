package ai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// pi: packages/ai/src/api/anthropic-messages.ts (convertTools :1925-1936), openai-completions.ts, openai-responses-shared.ts (:386-393),
// google-shared.ts (:391-397). None of them sanitizes a tool: JSON.stringify writes a lone UTF-16 surrogate of a tool as its escape.

// The JSON escapes of a lone high surrogate, a lone low surrogate, a low surrogate before a high one, and U+FFFD, which a PiG before this
// test wrote into a session in the place of each.
const (
	highUnit     = `\ud83d`
	lowUnit      = `\ude00`
	reversedPair = `\ude00\ud83d`
	replacement  = `\ufffd`
)

// toolSurrogateRow is one request of the table: the API kind, the provider's shape and the Context, as the JSON both sides read.
type toolSurrogateRow struct {
	Name          string          `json:"name"`
	API           string          `json:"api"`
	ID            string          `json:"id"`
	Provider      string          `json:"provider"`
	BaseURL       string          `json:"baseUrl"`
	APIKey        string          `json:"apiKey"`
	ContextWindow int             `json:"contextWindow"`
	Context       json.RawMessage `json:"context"`
	// holds is text Pi's body must hold, so a row cannot pass with a body that lost the tools.
	holds string
}

// surrogateTool declares a tool whose name, description, property name, property description, enum value and default each hold unit.
func surrogateTool(name, unit string) string {
	return strings.ReplaceAll(`{"name":"`+name+`[§]","description":"D[§]","parameters":{"type":"object","properties":{"K[§]":{"type":"string","description":"PD[§]","enum":["E[§]"],"default":"DF[§]"}},"required":["K[§]"]}}`, "§", unit)
}

// toolSurrogateRows are, for each provider shape, a first request that declares the tools, a history in which the model called one of two tools
// whose names differ only in the lone unit (as a session Pi wrote holds it), and the same history as a PiG before this test stored it: U+FFFD
// in the place of the unit, which is sent as it is.
func toolSurrogateRows() []toolSurrogateRow {
	shapes := []toolSurrogateRow{
		{Name: "anthropic API key", API: "anthropic-messages", ID: "claude-test", Provider: "anthropic", BaseURL: "https://api.anthropic.com", APIKey: "sk-ant-api03-x", ContextWindow: 200000},
		{Name: "anthropic OAuth", API: "anthropic-messages", ID: "claude-test", Provider: "anthropic", BaseURL: "https://api.anthropic.com", APIKey: "sk-ant-oat01-x", ContextWindow: 200000},
		{Name: "openai-completions custom base URL", API: "openai-completions", ID: "m", Provider: "custom", BaseURL: "http://127.0.0.1:9/v1", APIKey: "k", ContextWindow: 128000},
		{Name: "openai-completions openai", API: "openai-completions", ID: "m", Provider: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "k", ContextWindow: 128000},
		{Name: "openai-responses openai", API: "openai-responses", ID: "gpt-test", Provider: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "k", ContextWindow: 400000},
		{Name: "google-generative-ai", API: "google-generative-ai", ID: "gemini-2.5-flash", Provider: "google", BaseURL: "https://generativelanguage.googleapis.com/v1beta", APIKey: "k", ContextWindow: 1000000},
	}
	user := `{"role":"user","content":"go","timestamp":1}`
	replay := func(shape toolSurrogateRow, unit string) string {
		return user + `,{"role":"assistant","content":[{"type":"toolCall","id":"toolu_1","name":"N[` + unit + `]","arguments":{}}],"api":"` + shape.API + `","provider":"` + shape.Provider + `","model":"` + shape.ID + `",` +
			`"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":2},` +
			`{"role":"toolResult","toolCallId":"toolu_1","toolName":"N[` + unit + `]","content":[{"type":"text","text":"ran"}],"isError":false,"timestamp":3}`
	}
	twins := surrogateTool("N", highUnit) + "," + surrogateTool("N", lowUnit)
	var rows []toolSurrogateRow
	for _, shape := range shapes {
		add := func(name, tools, messages, holds string) {
			row := shape
			row.Name, row.holds = shape.Name+": "+name, holds
			row.Context = json.RawMessage(`{"systemPrompt":"s","tools":[` + tools + `],"messages":[` + messages + `]}`)
			rows = append(rows, row)
		}
		add("declarations", surrogateTool("N_high", highUnit)+","+surrogateTool("N_low", lowUnit)+","+surrogateTool("N_reversed", reversedPair)+","+twins, user, `"N_reversed[\ude00\ud83d]"`)
		add("history Pi wrote", twins, replay(shape, lowUnit), `"N[\ude00]"`)
		add("history with U+FFFD", surrogateTool("N", replacement), replay(shape, replacement), "\"N[\uFFFD]\"")
	}
	return rows
}

// toolSurrogateResult runs a row's request through transport and returns the message the stream ends with.
func toolSurrogateResult(t *testing.T, row toolSurrogateRow, transport http.RoundTripper) *ai.AssistantMessage {
	t.Helper()
	model := &ai.Model{ID: row.ID, DisplayName: row.ID, Input: []string{"text"}, Capabilities: ai.ModelCapabilities{ContextWindow: row.ContextWindow, MaxOutputTokens: 16384},
		ProviderMeta: ai.ProviderMetadata{API: ai.API(row.API), ProviderID: row.Provider, BaseURL: row.BaseURL}}
	run := map[string]func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error){
		"anthropic-messages": ai.StreamAnthropic, "openai-completions": ai.StreamOpenAICompletions, "openai-responses": ai.StreamOpenAIResponses, "google-generative-ai": ai.StreamGoogle,
	}[row.API]
	stream, err := run(payloadContext(t), model, payloadTranscript(t, row.Context), ai.StreamOptions{APIKey: row.APIKey, MaxRetries: new(int), Fetch: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	return stream.Result()
}

// A tool reaches the wire of every API kind as Pi sends it when its strings hold a lone UTF-16 surrogate: in the tools a request declares and
// in a history that names a called tool, for a history Pi wrote and for one that already holds U+FFFD.
func TestToolLoneSurrogatesReachTheWireAsPi(t *testing.T) {
	rows := toolSurrogateRows()
	want := piPayloads(t, "testdata/tool_surrogate_wire_body.mjs", rows)
	for i, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			if !strings.Contains(want[i], row.holds) {
				t.Fatalf("Pi's body does not hold %s: %s", row.holds, want[i])
			}
			capture := &wireCapture{}
			toolSurrogateResult(t, row, capture)
			if difference := wireDifference(string(capture.body), want[i]); difference != "" {
				t.Error(difference)
			}
		})
	}
}

// sseReply answers a request with one event stream.
type sseReply string

func (reply sseReply) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(string(reply))), Request: request}, nil
}

// The name of a called tool is the one the provider's stream gives, a lone UTF-16 surrogate included, as JSON.parse reads it: the agent loop
// finds the tool by that name, and two tools can differ in that unit alone. The message is written as Pi's session line holds it.
func TestCalledToolNameKeepsALoneSurrogateOfTheStream(t *testing.T) {
	context := json.RawMessage(`{"systemPrompt":"s","tools":[` + surrogateTool("N", highUnit) + "," + surrogateTool("N", lowUnit) + `],"messages":[{"role":"user","content":"go","timestamp":1}]}`)
	var anthropic strings.Builder
	for _, event := range []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m1","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"N[\ude00]","input":{}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":1}}`,
		`{"type":"message_stop"}`,
	} {
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(event), &kind); err != nil {
			t.Fatal(err)
		}
		anthropic.WriteString("event: " + kind.Type + "\ndata: " + event + "\n\n")
	}
	completions := "data: " + `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m1","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"N[\ude00]","arguments":"{}"}}]},"finish_reason":null}]}` +
		"\n\ndata: " + `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"
	for _, row := range []struct {
		shape toolSurrogateRow
		reply string
	}{
		{toolSurrogateRow{Name: "anthropic-messages", API: "anthropic-messages", ID: "claude-test", Provider: "anthropic", BaseURL: "https://api.anthropic.com", APIKey: "sk-ant-api03-x", ContextWindow: 200000, Context: context}, anthropic.String()},
		{toolSurrogateRow{Name: "openai-completions", API: "openai-completions", ID: "m", Provider: "custom", BaseURL: "http://127.0.0.1:9/v1", APIKey: "k", ContextWindow: 128000, Context: context}, completions},
	} {
		t.Run(row.shape.Name, func(t *testing.T) {
			message := toolSurrogateResult(t, row.shape, sseReply(row.reply))
			encoded, err := jsstring.MarshalJSON(message)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(encoded), `"name":"N[\ude00]"`) {
				t.Errorf("the message does not name the called tool as the stream did: %s", encoded)
			}
		})
	}
}
