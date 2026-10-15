//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// Pi's ConverseStream request body is the JS SDK's se_ConverseStreamCommand output: the members take() copies, in its order, with each
// document (a tool's inputSchema, a tool call's input, additionalModelRequestFields) passed through as Pi built it, so its members keep
// their order and an empty member name stays. Pi's stream posts the oracle body to a local server; PiG's provider posts to another.
func TestBedrockRequestBodyMatchesPi(t *testing.T) {
	type bodyCase struct {
		Name      string          `json:"name"`
		Model     string          `json:"model"`
		Reasoning string          `json:"reasoning,omitempty"`
		Schema    json.RawMessage `json:"schema"`
		// History is the transcript after the first user message. It ends with a tool result: a user message after it is merged into
		// the tool result's message (D116).
		History json.RawMessage `json:"history,omitempty"`
		System  string          `json:"system,omitempty"`
		// Image adds an image to the first user message.
		Image   bool            `json:"image,omitempty"`
		Options json.RawMessage `json:"options,omitempty"`
		// Direct calls stream, which passes requestMetadata on, instead of streamSimple.
		Direct bool `json:"direct,omitempty"`
		// Strict sets compat.supportsStrictMode and a json_schema constrainedSampling tool, so toolSpec carries strict after description
		// (ToolSpecification$ order) and the strict schema adds required and additionalProperties.
		Strict bool `json:"strict,omitempty"`
	}
	const claude = "anthropic.claude-sonnet-4-5-20250929-v1:0"
	usage := `"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
	cases := []bodyCase{
		{Name: "tool schema in authored order", Model: claude, System: "be brief",
			Schema: json.RawMessage(`{"type":"object","properties":{"z":{"type":"string","description":"zed"},"a":{"type":"number"}},"required":["z"]}`)},
		{Name: "tool schema with an empty property name", Model: claude,
			Schema: json.RawMessage(`{"properties":{"":{"type":"string"},"b":{"description":"<&>\u2028","type":"string"}},"type":"object"}`)},
		{Name: "history with a tool call", Model: claude,
			Schema: json.RawMessage(`{"type":"object","properties":{"z":{"type":"string"}}}`),
			History: json.RawMessage(`[{"role":"assistant","content":[{"type":"text","text":"ok"},{"type":"toolCall","id":"c1","name":"lookup","arguments":{"z":"q","a":{"y":1,"b":2},"":"e"}}],"api":"bedrock-converse-stream","provider":"amazon-bedrock","model":"` + claude + `",` + usage + `,"stopReason":"toolUse","timestamp":2},` +
				`{"role":"toolResult","toolCallId":"c1","toolName":"lookup","content":[{"type":"text","text":"r"}],"isError":false,"timestamp":3}]`)},
		{Name: "strict tool schema", Model: claude, Strict: true,
			Schema: json.RawMessage(`{"type":"object","properties":{"z":{"type":"string"},"a":{"type":"number"}},"required":["z"]}`)},
		{Name: "reasoning fields", Model: claude, Reasoning: "high",
			Schema: json.RawMessage(`{"type":"object","properties":{"z":{"type":"string"}}}`)},
		{Name: "adaptive thinking with block binding", Model: "anthropic.claude-opus-4-7-v1:0", Reasoning: "high",
			Schema: json.RawMessage(`{"type":"object","properties":{"z":{"type":"string"}}}`)},
		{Name: "gpt-oss reasoning effort", Model: "openai.gpt-oss-120b-1:0", Reasoning: "high",
			Schema: json.RawMessage(`{"type":"object","properties":{"z":{"type":"string"}}}`)},
		// requestMetadata keeps its insertion order (StreamOptions.RequestMetadataOrder), with array-index keys first as in any JS object.
		{Name: "image, signed thinking and options", Model: claude, Image: true, System: "s", Direct: true,
			Options: json.RawMessage(`{"temperature":0.2,"toolChoice":"any","cacheRetention":"long","requestMetadata":{"zeta":"1","alpha":"2","10":"3","mid":"4"}}`),
			Schema:  json.RawMessage(`{"type":"object","properties":{"z":{"type":"string"}}}`),
			History: json.RawMessage(`[{"role":"assistant","content":[{"type":"thinking","thinking":"hm","thinkingSignature":"sig"},{"type":"toolCall","id":"c1","name":"lookup","arguments":{}}],"api":"bedrock-converse-stream","provider":"amazon-bedrock","model":"` + claude + `",` + usage + `,"stopReason":"toolUse","timestamp":2},` +
				`{"role":"toolResult","toolCallId":"c1","toolName":"lookup","content":[{"type":"text","text":"<&>"},{"type":"image","data":"aGk=","mimeType":"image/png"}],"isError":true,"timestamp":3}]`)},
	}
	const script = `(async () => {
  const http = require('node:http');
  const [root, casesJSON] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(require('node:path').join(root, rel)).href);
  const lazy = await load('node_modules/@earendil-works/pi-ai/dist/api/bedrock-converse-stream.lazy.js');
  const transcript = await load('node_modules/@earendil-works/pi-ai/dist/utils/transcript.js');
  process.env.AWS_BEDROCK_SKIP_AUTH = '1';
  process.env.AWS_REGION = 'us-east-1';
  process.env.AWS_BEDROCK_FORCE_HTTP1 = '1';
  let body;
  const server = http.createServer((req, res) => { let data = ''; req.on('data', (chunk) => data += chunk); req.on('end', () => { body = data; res.statusCode = 400; res.setHeader('content-type', 'application/json'); res.end('{"message":"stop"}'); }); });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const out = {};
  for (const c of JSON.parse(casesJSON)) {
    const model = { id: c.model, name: 'Claude', provider: 'amazon-bedrock', api: 'bedrock-converse-stream', baseUrl: 'http://127.0.0.1:' + server.address().port, reasoning: !!c.reasoning,
      input: ['text', 'image'], maxTokens: 1000, contextWindow: 10000, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, ...(c.strict ? { compat: { supportsStrictMode: true } } : {}) };
    const tools = [{ name: 'lookup', description: 'Look up', parameters: c.schema, ...(c.strict ? { constrainedSampling: { type: 'json_schema' } } : {}) }];
    body = undefined;
    const first = c.image ? [{ type: 'text', text: 'hi' }, { type: 'image', data: 'aGk=', mimeType: 'image/jpeg' }] : 'hi';
    const messages = [{ role: 'user', content: first, timestamp: 1 }, ...(c.history ?? [])];
    const api = lazy.bedrockConverseStreamApi();
    await (c.direct ? api.stream : api.streamSimple)(model, transcript.normalizeContext({ systemPrompt: c.system, messages, tools }), { maxTokens: 100, ...(c.reasoning ? { reasoning: c.reasoning } : {}), ...c.options }).result();
    out[c.name] = body;
  }
  server.close();
  console.log(JSON.stringify(out));
})()`
	encoded, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), string(encoded))
	cmd.Stderr = new(bytes.Buffer)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, cmd.Stderr)
	}
	var want map[string]string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("oracle output %q: %v", out, err)
	}
	t.Setenv("AWS_BEDROCK_SKIP_AUTH", "1")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if want[c.Name] == "" {
				t.Fatal("oracle captured no body")
			}
			var body []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"stop"}`))
			}))
			defer server.Close()
			sampling := ""
			if c.Strict {
				sampling = `,"constrainedSampling":{"type":"json_schema"}`
			}
			tools := mustTools(t, `[{"name":"lookup","description":"Look up","parameters":`+string(c.Schema)+sampling+`}]`)
			model := &Model{ID: c.Model, DisplayName: "Claude", Input: []string{"text", "image"}, Capabilities: ModelCapabilities{MaxOutputTokens: 1000, ContextWindow: 10000},
				ProviderMeta: ProviderMetadata{ProviderID: "amazon-bedrock", API: APIBedrockConverseStream, BaseURL: server.URL, Reasoning: c.Reasoning != ""}}
			if c.Strict {
				model.ProviderMeta.Compat = &ModelCompat{SupportsStrictMode: new(true)}
			}
			messages := []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}
			if c.Image {
				messages[0] = UserMessage{Content: UserContentBlocks{TextContent{Text: "hi"}, ImageContent{Data: "aGk=", MimeType: "image/jpeg"}}, Timestamp: 1}
			}
			var extra struct {
				Temperature     *float64          `json:"temperature"`
				ToolChoice      string            `json:"toolChoice"`
				CacheRetention  CacheRetention    `json:"cacheRetention"`
				RequestMetadata map[string]string `json:"requestMetadata"`
			}
			var metadataOrder []string
			if c.Options != nil {
				if err := json.Unmarshal(c.Options, &extra); err != nil {
					t.Fatal(err)
				}
				var raw struct {
					RequestMetadata json.RawMessage `json:"requestMetadata"`
				}
				if err := json.Unmarshal(c.Options, &raw); err != nil {
					t.Fatal(err)
				}
				if raw.RequestMetadata != nil {
					object, err := orderedjson.Parse(raw.RequestMetadata)
					if err != nil {
						t.Fatal(err)
					}
					metadataOrder = object.Keys()
				}
			}
			var history []json.RawMessage
			if c.History != nil {
				if err := json.Unmarshal(c.History, &history); err != nil {
					t.Fatal(err)
				}
			}
			for _, raw := range history {
				var role struct {
					Role string `json:"role"`
				}
				if err := json.Unmarshal(raw, &role); err != nil {
					t.Fatal(err)
				}
				var message Message
				switch role.Role {
				case "assistant":
					var assistant AssistantMessage
					err = json.Unmarshal(raw, &assistant)
					message = assistant
				case "toolResult":
					var result ToolResultMessage
					err = json.Unmarshal(raw, &result)
					message = result
				default:
					var user struct {
						Content   string `json:"content"`
						Timestamp int64  `json:"timestamp"`
					}
					err = json.Unmarshal(raw, &user)
					message = UserMessage{Content: UserText(user.Content), Timestamp: user.Timestamp}
				}
				if err != nil {
					t.Fatal(err)
				}
				messages = append(messages, message)
			}
			// A coding session always sets onPayload (sdk.ts installs it for before_provider_request).
			for _, onPayload := range []func(any, *Model) (any, error){nil, func(payload any, _ *Model) (any, error) { return payload, nil }} {
				body = nil
				options := StreamOptions{MaxTokens: 100, OnPayload: onPayload, Thinking: ThinkingLevel(c.Reasoning), CacheRetention: extra.CacheRetention, RequestMetadata: extra.RequestMetadata, RequestMetadataOrder: metadataOrder}
				if extra.ToolChoice != "" {
					options.ToolChoice = extra.ToolChoice
				}
				if extra.Temperature != nil {
					options.Temperature, options.TemperatureSet = *extra.Temperature, true
				}
				run := BedrockConverseStreamAPI().StreamSimple
				if c.Direct {
					run = BedrockConverseStreamAPI().Stream
				}
				stream, err := run(t.Context(), model, NormalizeContext(Context{SystemPrompt: c.System, Messages: messages, Tools: tools}), options)
				if err == nil {
					for range stream.Events(t.Context()) {
					}
				}
				if got := string(body); got != want[c.Name] {
					t.Fatalf("request body (onPayload set: %t)\n got: %s\nwant: %s", onPayload != nil, got, want[c.Name])
				}
			}
		})
	}
}
