package ai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// pi: packages/ai/src/api/anthropic-messages.ts (convertTools :1925-1936), openai-completions.ts, openai-responses-shared.ts (:386-393),
// google-shared.ts (:391-397), mistral-conversations.ts (toFunctionTools). None of them sanitizes a tool, a called tool's name or a call's
// arguments: JSON.stringify writes a lone UTF-16 surrogate of each as its escape. The Bedrock kind posts through the AWS SDK and has the same
// rows in ai/providers/bedrock (TestBedrockRequestBodyMatchesPi).

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
	// Hook gives the request an onPayload hook that hands the payload back: the body is then written from what the hook returned.
	Hook bool `json:"hook,omitempty"`
	// Compat is the model's compat object.
	Compat json.RawMessage `json:"compat,omitempty"`
	// holds is text Pi's body must hold, so a row cannot pass with a body that lost the tools.
	holds string
}

// surrogateTool declares a tool whose name, description, property name, property description, enum value and default each hold unit.
func surrogateTool(name, unit string) string {
	return strings.ReplaceAll(`{"name":"`+name+`[§]","description":"D[§]","parameters":{"type":"object","properties":{"K[§]":{"type":"string","description":"PD[§]","enum":["E[§]"],"default":"DF[§]"}},"required":["K[§]"]}}`, "§", unit)
}

// toolSurrogateShapes are the provider shapes of the table: both authentications of anthropic-messages, both OpenAI-compatible paths of
// openai-completions, openai-responses and its Azure twin, google-generative-ai and mistral-conversations.
func toolSurrogateShapes() []toolSurrogateRow {
	return []toolSurrogateRow{
		{Name: "anthropic API key", API: "anthropic-messages", ID: "claude-test", Provider: "anthropic", BaseURL: "https://api.anthropic.com", APIKey: "sk-ant-api03-x", ContextWindow: 200000},
		{Name: "anthropic OAuth", API: "anthropic-messages", ID: "claude-test", Provider: "anthropic", BaseURL: "https://api.anthropic.com", APIKey: "sk-ant-oat01-x", ContextWindow: 200000},
		{Name: "openai-completions custom base URL", API: "openai-completions", ID: "m", Provider: "custom", BaseURL: "http://127.0.0.1:9/v1", APIKey: "k", ContextWindow: 128000},
		{Name: "openai-completions openai", API: "openai-completions", ID: "m", Provider: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "k", ContextWindow: 128000},
		{Name: "openai-responses openai", API: "openai-responses", ID: "gpt-test", Provider: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "k", ContextWindow: 400000},
		{Name: "azure-openai-responses", API: "azure-openai-responses", ID: "gpt-test", Provider: "azure-openai-responses", BaseURL: "https://example.openai.azure.com/openai/v1", APIKey: "k", ContextWindow: 400000},
		{Name: "google-generative-ai", API: "google-generative-ai", ID: "gemini-2.5-flash", Provider: "google", BaseURL: "https://generativelanguage.googleapis.com/v1beta", APIKey: "k", ContextWindow: 1000000},
		{Name: "mistral-conversations", API: "mistral-conversations", ID: "mistral-large-latest", Provider: "mistral", BaseURL: "https://api.mistral.ai", APIKey: "k", ContextWindow: 128000},
	}
}

// toolSurrogateRows are, for each provider shape, a first request that declares the tools, a history in which the model called one of two tools
// whose names differ only in the lone unit, with an argument that holds one (as a session Pi wrote holds them), the same history as a PiG
// before this test stored it (U+FFFD in the place of the unit, which is sent as it is), and the history Pi wrote again with a hook that
// hands the payload back.
func toolSurrogateRows() []toolSurrogateRow {
	user := `{"role":"user","content":"go","timestamp":1}`
	replay := func(shape toolSurrogateRow, unit string) string {
		return user + `,{"role":"assistant","content":[{"type":"toolCall","id":"toolu_1","name":"N[` + unit + `]","arguments":{"p":"A[` + unit + `]"}}],"api":"` + shape.API + `","provider":"` + shape.Provider + `","model":"` + shape.ID + `",` +
			`"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":2},` +
			`{"role":"toolResult","toolCallId":"toolu_1","toolName":"N[` + unit + `]","content":[{"type":"text","text":"ran"}],"isError":false,"timestamp":3}`
	}
	twins := surrogateTool("N", highUnit) + "," + surrogateTool("N", lowUnit)
	var rows []toolSurrogateRow
	for _, shape := range toolSurrogateShapes() {
		add := func(name, tools, messages, holds string, hook bool) {
			row := shape
			row.Name, row.holds, row.Hook = shape.Name+": "+name, holds, hook
			row.Context = json.RawMessage(`{"systemPrompt":"s","tools":[` + tools + `],"messages":[` + messages + `]}`)
			rows = append(rows, row)
		}
		add("declarations", surrogateTool("N_high", highUnit)+","+surrogateTool("N_low", lowUnit)+","+surrogateTool("N_reversed", reversedPair)+","+twins, user, `"N_reversed[\ude00\ud83d]"`, false)
		add("history Pi wrote", twins, replay(shape, lowUnit), `"N[\ude00]"`, false)
		add("history with U+FFFD", surrogateTool("N", replacement), replay(shape, replacement), "\"N[\uFFFD]\"", false)
		add("a hook hands the payload back", twins, replay(shape, lowUnit), `"N[\ude00]"`, true)
	}
	return rows
}

// toolSurrogateResult runs a row's request through transport and returns the message the stream ends with.
func toolSurrogateResult(t *testing.T, row toolSurrogateRow, transport http.RoundTripper) *ai.AssistantMessage {
	return toolSurrogateStream(t, row, transport).Result()
}

// toolSurrogateStream runs a row's request through transport and returns its stream.
func toolSurrogateStream(t *testing.T, row toolSurrogateRow, transport http.RoundTripper) *ai.AssistantMessageEventStream {
	t.Helper()
	model := &ai.Model{ID: row.ID, DisplayName: row.ID, Input: []string{"text"}, Capabilities: ai.ModelCapabilities{ContextWindow: row.ContextWindow, MaxOutputTokens: 16384},
		ProviderMeta: ai.ProviderMetadata{API: ai.API(row.API), ProviderID: row.Provider, BaseURL: row.BaseURL}}
	run := map[string]func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error){
		"anthropic-messages": ai.StreamAnthropic, "openai-completions": ai.StreamOpenAICompletions, "openai-responses": ai.StreamOpenAIResponses,
		"azure-openai-responses": ai.StreamAzureOpenAIResponses, "google-generative-ai": ai.StreamGoogle, "mistral-conversations": ai.StreamMistral, "openai-codex-responses": ai.StreamOpenAICodexResponses,
	}[row.API]
	if row.API == "pi-messages" {
		provider := ai.NewPiMessagesProvider(ai.PiMessagesConfig{BaseURL: row.BaseURL, APIKey: row.APIKey, Model: row.ID, ProviderID: row.Provider})
		t.Cleanup(func() { _ = provider.Close() })
		run = func(ctx context.Context, _ *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			return provider.Stream(ctx, transcript, options)
		}
	}
	if row.Compat != nil {
		model.ProviderMeta.Compat = &ai.OpenAICompat{}
		if err := json.Unmarshal(row.Compat, model.ProviderMeta.Compat); err != nil {
			t.Fatal(err)
		}
	}
	options := ai.StreamOptions{APIKey: row.APIKey, MaxRetries: new(int), Fetch: &http.Client{Transport: transport}}
	if row.Hook {
		options.OnPayload = func(payload any, _ *ai.Model) (any, error) { return payload, nil }
	}
	stream, err := run(payloadContext(t), model, payloadTranscript(t, row.Context), options)
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

// A tool reaches the wire of every API kind as Pi sends it when its strings hold a lone UTF-16 surrogate: in the tools a request declares, in a
// history that names a called tool and holds its arguments, for a history Pi wrote and for one that already holds U+FFFD, and in the body
// written from a payload a hook handed back. The comparison is of bytes, so a body with U+FFFD in the place of a unit differs from Pi's.
func TestToolLoneSurrogatesReachTheWireAsPi(t *testing.T) {
	rows := toolSurrogateRows()
	want := piPayloads(t, "testdata/tool_surrogate_wire_body.mjs", rows)
	for i, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			skipStrippedAPI(t, row.API)
			if want[i] == "" {
				t.Fatal("Pi posted no body for this row")
			}
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

// pi: packages/ai/src/api/openai-completions.ts:979-980 hands model.compat.openRouterRouting on as the request's provider member, as it is.
// PiG writes that member through a marshaler of its own when the compat lists the object's members in an order (ai.compatObject), so a lone
// UTF-16 surrogate in a value, in an array, in a nested object and in a member name reaches the wire as the bytes Pi posts. The compat is
// decoded from JSON, as models.json and an extension's registration give it: the decode keeps the unit, as JSON.parse does.
func TestCompatObjectLoneSurrogatesReachTheWireAsPi(t *testing.T) {
	row := toolSurrogateRow{Name: "openRouterRouting", API: "openai-completions", ID: "m", Provider: "custom", BaseURL: "http://127.0.0.1:9/v1", APIKey: "k", ContextWindow: 128000,
		Context: json.RawMessage(`{"systemPrompt":"s","tools":[],"messages":[{"role":"user","content":"go","timestamp":1}]}`),
		Compat:  json.RawMessage(`{"openRouterRouting":{"sort":"S[` + highUnit + `]","order":["O[` + highUnit + `]"],"max_price":{"prompt":"P[` + highUnit + `]","completion":1},"K[` + highUnit + `]":true}}`)}
	want := piPayloads(t, "testdata/tool_surrogate_wire_body.mjs", []toolSurrogateRow{row})[0]
	const holds = `"provider":{"sort":"S[\ud83d]","order":["O[\ud83d]"],"max_price":{"prompt":"P[\ud83d]","completion":1},"K[\ud83d]":true}`
	if !strings.Contains(want, holds) {
		t.Fatalf("Pi's body does not hold %s: %s", holds, want)
	}
	capture := &wireCapture{}
	toolSurrogateResult(t, row, capture)
	if difference := wireDifference(string(capture.body), want); difference != "" {
		t.Error(difference)
	}
}

// sseReply answers a request with one event stream.
type sseReply string

func (reply sseReply) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(string(reply))), Request: request}, nil
}

// toolSurrogateStreamRow is a request and the event stream that answers it.
type toolSurrogateStreamRow struct {
	toolSurrogateRow
	Reply string `json:"reply"`
}

// surrogateEvents writes records as a stream of server-sent events; named gives each its `type` member as the event name.
func surrogateEvents(named bool, records ...string) string {
	var out strings.Builder
	for _, record := range records {
		if named {
			var kind struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal([]byte(record), &kind)
			out.WriteString("event: " + kind.Type + "\n")
		}
		out.WriteString("data: " + record + "\n\n")
	}
	return out.String()
}

// calledToolName finds the name of the first tool call in a message written as JSON.stringify writes it.
var calledToolName = regexp.MustCompile(`"type":"toolCall".*?"name":"((?:[^"\\]|\\.)*)"`)

// The name of a called tool is the one the provider's stream gives, a lone UTF-16 surrogate included, as JSON.parse reads it: the agent loop
// finds the tool by that name, and two tools can differ in that unit alone. For the stream of every API kind the message PiG ends with names
// the tool with the bytes of the message Pi ends with, as each is written into a session line.
func TestCalledToolNameKeepsALoneSurrogateOfTheStream(t *testing.T) {
	context := json.RawMessage(`{"systemPrompt":"s","tools":[` + surrogateTool("N", highUnit) + "," + surrogateTool("N", lowUnit) + `],"messages":[{"role":"user","content":"go","timestamp":1}]}`)
	anthropic := surrogateEvents(true,
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m1","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"N[\ude00]","input":{}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":1}}`,
		`{"type":"message_stop"}`)
	completions := surrogateEvents(false,
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m1","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"N[\ude00]","arguments":"{}"}}]},"finish_reason":null}]}`,
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`) + "data: [DONE]\n\n"
	responses := surrogateEvents(true,
		`{"type":"response.created","sequence_number":0,"response":{"id":"resp_1","object":"response","status":"in_progress","output":[]}}`,
		`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"N[\ude00]","arguments":"","status":"in_progress"}}`,
		`{"type":"response.function_call_arguments.delta","sequence_number":2,"item_id":"fc_1","output_index":0,"delta":"{}"}`,
		`{"type":"response.function_call_arguments.done","sequence_number":3,"item_id":"fc_1","output_index":0,"arguments":"{}"}`,
		`{"type":"response.output_item.done","sequence_number":4,"output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"N[\ude00]","arguments":"{}","status":"completed"}}`,
		`{"type":"response.completed","sequence_number":5,"response":{"id":"resp_1","object":"response","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}}`)
	google := surrogateEvents(false,
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"N[\ude00]","args":{}}}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`)
	mistral := surrogateEvents(false,
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m1","choices":[{"index":0,"delta":{"role":"assistant","content":"","tool_calls":[{"id":"call00001","type":"function","function":{"name":"N[\ude00]","arguments":"{}"},"index":0}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`) + "data: [DONE]\n\n"
	replies := map[string]string{"anthropic-messages": anthropic, "openai-completions": completions, "openai-responses": responses, "azure-openai-responses": responses,
		"google-generative-ai": google, "mistral-conversations": mistral}
	var rows []toolSurrogateStreamRow
	for _, shape := range toolSurrogateShapes() {
		shape.Context = context
		rows = append(rows, toolSurrogateStreamRow{toolSurrogateRow: shape, Reply: replies[shape.API]})
	}
	want := piPayloads(t, "testdata/tool_surrogate_stream.mjs", rows)
	for i, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			skipStrippedAPI(t, row.API)
			piName := calledToolName.FindStringSubmatch(want[i])
			if piName == nil || piName[1] != `N[\ude00]` {
				t.Fatalf("Pi's message does not name the called tool as the stream did: %s", want[i])
			}
			message := toolSurrogateResult(t, row.toolSurrogateRow, sseReply(row.Reply))
			encoded, err := jsstring.MarshalJSON(message)
			if err != nil {
				t.Fatal(err)
			}
			if name := calledToolName.FindStringSubmatch(string(encoded)); name == nil || name[1] != piName[1] {
				t.Errorf("the message does not name the called tool as Pi's does (%s): %s", piName[1], encoded)
			}
		})
	}
}

// pi: packages/ai/src/utils/constrained-sampling.ts makeStrictJsonSchema copies the schema of a tool with structuredClone, which keeps every
// string as it is. PiG copies it through its JSON form, so a lone UTF-16 surrogate in a description or an enum value of a tool that asks
// for strict sampling reaches the wire of every API kind as the bytes Pi posts.
func TestStrictToolSchemaLoneSurrogatesReachTheWireAsPi(t *testing.T) {
	var rows []toolSurrogateRow
	for _, shape := range toolSurrogateShapes() {
		shape.Name += ": a tool that prefers strict sampling"
		shape.Context = json.RawMessage(`{"systemPrompt":"s","tools":[{"name":"N","description":"d","parameters":{"type":"object","properties":{"p":{"type":"string","description":"P[` + highUnit + `]","enum":["E[` + highUnit + `]"]}},"required":["p"],"additionalProperties":false},"constrainedSampling":{"type":"json_schema","strict":"prefer"}}],"messages":[{"role":"user","content":"go","timestamp":1}]}`)
		if shape.API == "mistral-conversations" {
			// Pi 2c2a77f9f3ab: Mistral sends the tool strict only when the model's compat sets supportsStrictMode, so the row that asks for strict
			// sampling carries the flag, and the same tool without it is a row of its own (sent non-strict, with the schema as authored).
			plain := shape
			plain.Name += " (no supportsStrictMode)"
			rows = append(rows, plain)
			shape.Compat = json.RawMessage(`{"supportsStrictMode":true}`)
		}
		rows = append(rows, shape)
	}
	want := piPayloads(t, "testdata/tool_surrogate_wire_body.mjs", rows)
	strict := 0
	for i, row := range rows {
		if strings.Contains(want[i], `"strict":true`) {
			strict++
		}
		t.Run(row.Name, func(t *testing.T) {
			skipStrippedAPI(t, row.API)
			if !strings.Contains(want[i], "E[") {
				t.Fatalf("the body of Pi does not hold the enum value: %s", want[i])
			}
			capture := &wireCapture{}
			toolSurrogateResult(t, row, capture)
			if difference := wireDifference(string(capture.body), want[i]); difference != "" {
				t.Error(difference)
			}
		})
	}
	if strict < 2 {
		t.Fatalf("Pi asked for strict sampling in %d bodies, want at least 2", strict)
	}
}
