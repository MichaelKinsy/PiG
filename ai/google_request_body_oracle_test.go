//go:build !pig_strip_google_vertex

package ai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

// The @google/genai SDK writes the body Pi's buildParams hands it through its converters, which build each object member by member
// (generateContentParametersToMldev, generateContentConfigToMldev, contentToMldev, partToMldev, functionCallToMldev, blobToMldev) and pass
// a functionResponse and the function declarations through as Pi built them. Pi's own stream posts the oracle body to a local server;
// PiG's provider posts to another.
func TestGoogleRequestBodyMatchesPi(t *testing.T) {
	type bodyCase struct {
		Name   string          `json:"name"`
		Model  string          `json:"model"`
		Schema json.RawMessage `json:"schema"`
		Strict bool            `json:"strict"`
		// History is the transcript after the first user message; System and Options go to both streams.
		History json.RawMessage `json:"history,omitempty"`
		System  string          `json:"system,omitempty"`
		Options json.RawMessage `json:"options,omitempty"`
		Images  bool            `json:"images,omitempty"`
		Vertex  bool            `json:"vertex,omitempty"`
		// Replacement is the payload onPayload returns, as an extension's raw JSON; the SDK lowers it in its own member order.
		Replacement json.RawMessage `json:"replacement,omitempty"`
	}
	replacement := func(model string, vertex bool) json.RawMessage {
		labels := ""
		if vertex {
			labels = `,"labels":{"z":"1","a":"2"}`
		}
		return json.RawMessage(`{"config":{"temperature":0.1,"systemInstruction":"s","tools":[{"functionDeclarations":[{"parametersJsonSchema":{"type":"object","properties":{"z":{"type":"string"},"a":{}}},"name":"n","description":"d"}]}],"maxOutputTokens":9` + labels +
			`,"toolConfig":{"functionCallingConfig":{"mode":"AUTO"}}},"contents":[{"parts":[{"text":"q"}],"role":"user"},{"parts":[{"text":"x"},{"functionCall":{"name":"n","args":{"z":"1","a":2},"id":"c1"}}],"role":"model"},{"role":"user","parts":[{"functionResponse":{"response":{"z":1,"a":2},"name":"n","id":"c1"}}]}],"model":"` + model + `"}`)
	}
	cases := []bodyCase{
		{Name: "replaced payload", Model: "gemini-2.5-flash", Schema: json.RawMessage(`{"type":"object"}`), Replacement: replacement("gemini-2.5-flash", false)},
		{Vertex: true, Name: "vertex replaced payload", Model: "gemini-2.5-flash", Schema: json.RawMessage(`{"type":"object"}`), Replacement: replacement("gemini-2.5-flash", true)},
		{Name: "strict tool", Model: "gemini-3-pro-preview", Strict: true,
			Schema: json.RawMessage(`{"type":"object","properties":{"z":{"type":"string"},"a":{"type":"number"}},"required":["z"]}`)},
		{Name: "history with a tool call and options", Model: "gemini-2.5-flash", System: "be brief", Options: json.RawMessage(`{"temperature":0.5,"maxTokens":100}`),
			Schema: json.RawMessage(`{"type":"object","properties":{"z":{"type":"string"}},"required":["z"]}`),
			History: json.RawMessage(`[{"role":"assistant","content":[{"type":"thinking","thinking":"hm","thinkingSignature":"c2ln"},{"type":"text","text":"ok"},{"type":"toolCall","id":"c1","name":"lookup","arguments":{"z":"q","a":1},"thoughtSignature":"c2ln"}],"api":"google-generative-ai","provider":"google","model":"gemini-2.5-flash","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":2},` +
				`{"role":"toolResult","toolCallId":"c1","toolName":"lookup","content":[{"type":"text","text":"r"}],"isError":false,"timestamp":3},{"role":"user","content":"next","timestamp":4}]`)},
		{Name: "tool result image inside the function response", Model: "gemini-3-pro-preview", Images: true, Schema: json.RawMessage(`{"type":"object","properties":{"z":{"type":"string"}}}`),
			History: json.RawMessage(`[{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"lookup","arguments":{"z":"q"}}],"api":"google-generative-ai","provider":"google","model":"gemini-3-pro-preview","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":2},{"role":"toolResult","toolCallId":"c1","toolName":"lookup","content":[{"type":"text","text":"r"},{"type":"image","data":"aGk=","mimeType":"image/png"}],"isError":false,"timestamp":3}]`)},
		{Name: "tool result image in its own turn", Model: "gemini-2.5-flash", Images: true, Schema: json.RawMessage(`{"type":"object","properties":{"z":{"type":"string"}}}`),
			History: json.RawMessage(`[{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"lookup","arguments":{"z":"q"}}],"api":"google-generative-ai","provider":"google","model":"gemini-2.5-flash","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":2},{"role":"toolResult","toolCallId":"c1","toolName":"lookup","content":[{"type":"text","text":"r"},{"type":"image","data":"aGk=","mimeType":"image/png"}],"isError":false,"timestamp":3}]`)},
		{Vertex: true, Name: "vertex history with a tool call and options", Model: "gemini-2.5-flash", System: "be brief", Options: json.RawMessage(`{"temperature":0.5,"maxTokens":100}`),
			Schema: json.RawMessage(`{"type":"object","properties":{"z":{"type":"string"}},"required":["z"]}`),
			History: json.RawMessage(`[{"role":"assistant","content":[{"type":"thinking","thinking":"hm","thinkingSignature":"c2ln"},{"type":"text","text":"ok"},{"type":"toolCall","id":"c1","name":"lookup","arguments":{"z":"q","a":1},"thoughtSignature":"c2ln"}],"api":"google-vertex","provider":"google","model":"gemini-2.5-flash","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":2},` +
				`{"role":"toolResult","toolCallId":"c1","toolName":"lookup","content":[{"type":"text","text":"r"}],"isError":false,"timestamp":3},{"role":"user","content":"next","timestamp":4}]`)},
		{Vertex: true, Name: "vertex tool result image inside the function response", Model: "gemini-3-pro-preview", Images: true, Schema: json.RawMessage(`{"type":"object","properties":{"z":{"type":"string"}}}`),
			History: json.RawMessage(`[{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"lookup","arguments":{"z":"q"}}],"api":"google-vertex","provider":"google","model":"gemini-3-pro-preview","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":2},{"role":"toolResult","toolCallId":"c1","toolName":"lookup","content":[{"type":"text","text":"r"},{"type":"image","data":"aGk=","mimeType":"image/png"}],"isError":false,"timestamp":3}]`)},
		{Vertex: true, Name: "vertex tool result image in its own turn", Model: "gemini-2.5-flash", Images: true, Schema: json.RawMessage(`{"type":"object","properties":{"z":{"type":"string"}}}`),
			History: json.RawMessage(`[{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"lookup","arguments":{"z":"q"}}],"api":"google-vertex","provider":"google","model":"gemini-2.5-flash","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":2},{"role":"toolResult","toolCallId":"c1","toolName":"lookup","content":[{"type":"text","text":"r"},{"type":"image","data":"aGk=","mimeType":"image/png"}],"isError":false,"timestamp":3}]`)},
	}
	const script = `(async () => {
  const http = require('node:http');
  const [root, casesJSON] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(require('node:path').join(root, rel)).href);
  const lazy = await load('node_modules/@earendil-works/pi-ai/dist/api/google-generative-ai.lazy.js');
  const vertex = await load('node_modules/@earendil-works/pi-ai/dist/api/google-vertex.lazy.js');
  const transcript = await load('node_modules/@earendil-works/pi-ai/dist/utils/transcript.js');
  let body;
  const server = http.createServer((req, res) => { let data = ''; req.on('data', (chunk) => data += chunk); req.on('end', () => { body = data; res.statusCode = 400; res.end('{}'); }); });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const out = {};
  for (const c of JSON.parse(casesJSON)) {
    const model = { id: c.model, provider: c.vertex ? 'google-vertex' : 'google', api: c.vertex ? 'google-vertex' : 'google-generative-ai', baseUrl: 'http://127.0.0.1:' + server.address().port, reasoning: false,
      input: c.images ? ['text', 'image'] : ['text'], maxTokens: 1000, contextWindow: 10000, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } };
    const tools = [{ name: 'lookup', description: 'Look up', parameters: c.schema, ...(c.strict ? { constrainedSampling: { type: 'json_schema' } } : {}) }];
    body = undefined;
    const messages = [{ role: 'user', content: 'hi', timestamp: 1 }, ...(c.history ?? [])];
    await (c.vertex ? vertex.googleVertexApi() : lazy.googleGenerativeAIApi()).stream(model, transcript.normalizeContext({ systemPrompt: c.system, messages, tools }), { apiKey: 'k', ...c.options, ...(c.replacement ? { onPayload: () => JSON.parse(JSON.stringify(c.replacement)) } : {}) }).result();
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

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var body []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusBadRequest)
			}))
			defer server.Close()
			constrained := ""
			if c.Strict {
				constrained = `,"constrainedSampling":{"type":"json_schema"}`
			}
			tools := mustTools(t, `[{"name":"lookup","description":"Look up","parameters":`+string(c.Schema)+constrained+`}]`)
			if c.Images && !strings.Contains(want[c.Name], `"inlineData"`) {
				t.Fatalf("oracle body has no image: %s", want[c.Name])
			}
			input := []string{"text"}
			if c.Images {
				input = append(input, "image")
			}
			provider := NewGoogleProvider(GoogleConfig{Model: c.Model, ProviderID: "google", BaseURL: server.URL, APIKey: "k",
				ModelMetadata: &Model{ID: c.Model, Input: input, ProviderMeta: ProviderMetadata{ProviderID: "google", API: APIGoogleGenerativeAI}}})
			if c.Vertex {
				provider = NewGoogleVertexProvider(GoogleVertexConfig{Model: c.Model, ProviderID: "google-vertex", BaseURL: server.URL, APIKey: "k",
					ModelMetadata: &Model{ID: c.Model, Input: input, ProviderMeta: ProviderMetadata{ProviderID: "google-vertex", API: APIGoogleVertex}}})
			}
			messages := []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}
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
			var options struct {
				Temperature *float64 `json:"temperature"`
				MaxTokens   int      `json:"maxTokens"`
			}
			if c.Options != nil {
				if err := json.Unmarshal(c.Options, &options); err != nil {
					t.Fatal(err)
				}
			}
			streamOptions := StreamOptions{MaxTokens: options.MaxTokens}
			if options.Temperature != nil {
				streamOptions.Temperature, streamOptions.TemperatureSet = *options.Temperature, true
			}
			// A coding session always sets onPayload (sdk.ts installs it for before_provider_request), which lowers the payload again.
			hooks := []func(any, *Model) (any, error){nil, func(payload any, _ *Model) (any, error) { return payload, nil }}
			if c.Replacement != nil {
				hooks = hooks[1:]
				hooks[0] = func(any, *Model) (any, error) { return c.Replacement, nil }
			}
			for _, onPayload := range hooks {
				body = nil
				streamOptions.OnPayload = onPayload
				stream, err := provider.Stream(t.Context(), NormalizeContext(Context{SystemPrompt: c.System, Messages: messages, Tools: tools}), streamOptions)
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
