package ai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

// openai-responses.ts buildParams (:330-381) and openai-codex-responses.ts buildRequestBody (:553-596) create their keys in different orders,
// and buildParams ends with Object.assign(params, samplingParams) (:383-386), which keeps a key's position and appends a new key. Pi's own
// builders write the oracle bytes through onPayload; PiG's bytes are the HTTP request body.
//
// A tool's parameters are the tool's own object, written in declaration order, and a strict tool's schema is rebuilt member by member in
// that order (constrained-sampling.ts makeJsonSchemaNodeStrict: required, then additionalProperties, an optional property wrapped in anyOf).
func TestOpenAIResponsesRequestKeysFollowPiAssignmentOrder(t *testing.T) {
	type keyOrderCase struct {
		Name    string          `json:"name"`
		API     string          `json:"api"`
		Model   json.RawMessage `json:"model"`
		Options json.RawMessage `json:"options"`
		Tools   bool            `json:"tools"`
	}
	cases := []keyOrderCase{
		{Name: "responses plain", API: "openai-responses", Model: json.RawMessage(`{"reasoning":false}`), Options: json.RawMessage(`{"maxTokens":50,"temperature":0.7,"sessionId":"session-1"}`)},
		{Name: "responses tools and reasoning", API: "openai-responses", Model: json.RawMessage(`{"reasoning":true}`), Options: json.RawMessage(`{"maxTokens":50,"toolChoice":"auto","reasoningEffort":"medium","serviceTier":"flex"}`), Tools: true},
		{Name: "responses sampling params override in place and append", API: "openai-responses", Model: json.RawMessage(`{"reasoning":false,"samplingParams":{"temperature":0.3,"model":"override","top_p":0.9}}`),
			Options: json.RawMessage(`{"maxTokens":50,"temperature":0.7}`)},
		{Name: "codex", API: "openai-codex-responses", Model: json.RawMessage(`{"reasoning":true}`), Options: json.RawMessage(`{"temperature":0.7,"sessionId":"session-1","reasoningEffort":"high","serviceTier":"flex","toolChoice":"auto"}`), Tools: true},
	}
	apiKey := "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"account"}}`)) + ".signature"
	const script = `(async () => {
  const [root, casesJSON, apiKey, toolsJSON] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(require('node:path').join(root, rel)).href);
  const apis = {
    'openai-responses': (await load('node_modules/@earendil-works/pi-ai/dist/api/openai-responses.lazy.js')).openAIResponsesApi(),
    'openai-codex-responses': (await load('node_modules/@earendil-works/pi-ai/dist/api/openai-codex-responses.lazy.js')).openAICodexResponsesApi(),
  };
  const transcript = await load('node_modules/@earendil-works/pi-ai/dist/utils/transcript.js');
  const out = {};
  for (const c of JSON.parse(casesJSON)) {
    const model = { id: 'm1', provider: 'custom', api: c.api, baseUrl: 'http://127.0.0.1:9/v1', input: ['text'], maxTokens: 32768, contextWindow: 131072, ...c.model };
    const tools = c.tools ? JSON.parse(toolsJSON) : undefined;
    const context = transcript.normalizeContext({ messages: [{ role: 'user', content: 'hi <b> & c\u2028', timestamp: 1 }], tools });
    const stream = apis[c.api].stream(model, context, { apiKey, ...c.options,
      onPayload: (params) => { out[c.name] = JSON.stringify(params); throw new Error('captured'); } });
    await stream.result();
  }
  console.log(JSON.stringify(out));
})()`
	encoded, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), string(encoded), apiKey, responsesKeyOrderTools)
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
			var piModel struct {
				Reasoning      bool           `json:"reasoning"`
				SamplingParams map[string]any `json:"samplingParams"`
			}
			if err := json.Unmarshal(c.Model, &piModel); err != nil {
				t.Fatal(err)
			}
			var piOptions struct {
				MaxTokens       int      `json:"maxTokens"`
				Temperature     *float64 `json:"temperature"`
				ToolChoice      any      `json:"toolChoice"`
				SessionID       string   `json:"sessionId"`
				ReasoningEffort string   `json:"reasoningEffort"`
				ServiceTier     string   `json:"serviceTier"`
			}
			if err := json.Unmarshal(c.Options, &piOptions); err != nil {
				t.Fatal(err)
			}
			var body []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				if r.Header.Get("Content-Encoding") == "zstd" {
					var err error
					if body, err = decodeZstdFrameForTest(body); err != nil {
						t.Error(err)
					}
				}
				w.WriteHeader(http.StatusBadRequest)
			}))
			defer server.Close()
			model := &Model{ID: "m1", Input: []string{"text"}, SamplingParams: piModel.SamplingParams,
				ProviderMeta: ProviderMetadata{ProviderID: "custom", API: API(c.API), BaseURL: server.URL + "/v1", Reasoning: piModel.Reasoning},
				Capabilities: ModelCapabilities{MaxOutputTokens: 32768, ContextWindow: 131072}}
			if piModel.Reasoning {
				model.Capabilities.MaxThinking = ThinkingLevelHigh
			}
			var provider Provider
			if c.API == string(APIOpenAICodexResponses) {
				provider = NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: apiKey, Model: model.ID, ModelMetadata: model, ProviderID: "custom", BaseURL: server.URL + "/v1"})
			} else {
				provider = NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: apiKey, Model: model.ID, ModelMetadata: model, ProviderID: "custom", BaseURL: server.URL + "/v1", SamplingParams: piModel.SamplingParams, IsReasoning: piModel.Reasoning})
			}
			options := StreamOptions{MaxTokens: piOptions.MaxTokens, ToolChoice: piOptions.ToolChoice, SessionID: piOptions.SessionID, ReasoningEffort: piOptions.ReasoningEffort,
				ServiceTier: piOptions.ServiceTier, IsReasoning: piModel.Reasoning}
			if piOptions.Temperature != nil {
				options.Temperature, options.TemperatureSet = *piOptions.Temperature, true
			}
			var tools []ToolSchema
			if c.Tools {
				tools = mustTools(t, responsesKeyOrderTools)
			}
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi <b> & c\u2028"), Timestamp: 1}}, Tools: tools}), options)
			if err == nil {
				for range stream.Events(t.Context()) {
				}
			}
			if got := strings.TrimSpace(string(body)); got != want[c.Name] {
				t.Fatalf("request body\n got: %s\nwant: %s", got, want[c.Name])
			}
		})
	}
}

const responsesKeyOrderTools = `[{"name":"read","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string","description":"File"},"offset":{"type":"number"}},"required":["path"]}},` +
	`{"name":"emit","description":"Emit","parameters":{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"number","description":"Line"},"opts":{"type":"object","properties":{"x":{"type":"string"}}}},"required":["path"]},"constrainedSampling":{"type":"json_schema"}}]`
