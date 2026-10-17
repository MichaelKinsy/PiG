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

// JSON.stringify writes a request's keys in the order Pi's buildParams creates them, and Object.assign(params, samplingParams) keeps the
// position of a key params already has and appends a new one (packages/ai/src/api/openai-completions.ts:802-1009: the literal at :822-831,
// then stream_options :834, store :838, max tokens :842-849, temperature :851, tools :855-863, tool_choice :869, priority :873, the
// thinking-format members :879-976, the budget field :982-984, provider :988, providerOptions :992-1000, samplingParams :1004-1007).
// Pi's own buildParams writes the oracle bytes through onPayload; PiG's bytes are the HTTP request body.
func TestOpenAICompletionsRequestKeysFollowPiAssignmentOrder(t *testing.T) {
	type keyOrderCase struct {
		Name     string          `json:"name"`
		Model    json.RawMessage `json:"model"`
		Options  json.RawMessage `json:"options"`
		Tools    bool            `json:"tools"`
		Schema   json.RawMessage `json:"schema,omitempty"`
		thinking ModelThinkingLevel
	}
	cases := []keyOrderCase{
		{Name: "plain", Model: json.RawMessage(`{"reasoning":false}`), Options: json.RawMessage(`{"maxTokens":50,"temperature":0.7}`)},
		{Name: "tools", Model: json.RawMessage(`{"reasoning":false}`), Options: json.RawMessage(`{"maxTokens":50,"temperature":0.7,"toolChoice":"auto"}`), Tools: true},
		{Name: "long cache retention", Model: json.RawMessage(`{"reasoning":false,"compat":{"supportsLongCacheRetention":true,"maxTokensField":"max_tokens"}}`),
			Options: json.RawMessage(`{"maxTokens":50,"sessionId":"session-1","cacheRetention":"long"}`)},
		{Name: "sampling params override in place and append", Model: json.RawMessage(`{"reasoning":false,"samplingParams":{"temperature":0.3,"model":"override","top_p":0.9}}`),
			Options: json.RawMessage(`{"maxTokens":50,"temperature":0.7}`)},
		// prompt_cache_key is in buildParams' literal even when it is undefined, so a sampling parameter that sets it lands after stream.
		{Name: "sampling param fills an undefined literal key in place", Model: json.RawMessage(`{"reasoning":false,"samplingParams":{"prompt_cache_key":"from-sampling"}}`),
			Options: json.RawMessage(`{"maxTokens":50}`)},
		// The spread {...model.samplingParams, ...levelParams, ...requestParams} enumerates the model's keys before the request's new ones.
		{Name: "model sampling keys before request sampling keys", Model: json.RawMessage(`{"reasoning":false,"samplingParams":{"top_p":0.9}}`),
			Options: json.RawMessage(`{"maxTokens":50,"samplingParams":{"min_p":0.1,"top_p":0.5}}`)},
		{Name: "qwen budget field before routing, sampling last", Model: json.RawMessage(`{"reasoning":true,"samplingParams":{"min_p":0.1},"compat":{"thinkingFormat":"qwen","supportsReasoningEffort":true,"thinkingTokenBudgetField":"thinking_budget","openRouterRouting":{"only":["a"]}}}`),
			Options: json.RawMessage(`{"maxTokens":16384,"reasoningEffort":"medium"}`), thinking: ThinkingMedium},
		{Name: "deepseek thinking then effort then default budget field", Model: json.RawMessage(`{"reasoning":true,"compat":{"thinkingFormat":"deepseek","supportsReasoningEffort":true,"supportsThinkingTokenBudget":true}}`),
			Options: json.RawMessage(`{"maxTokens":16384,"reasoningEffort":"high"}`), thinking: ThinkingHigh},
		// A tool's parameters are the tool's own object: JSON.stringify writes its members in declaration order (convertTools :1506-1514), and a
		// strict schema is rebuilt member by member in the same order (constrained-sampling.ts makeStrictJsonSchema).
		{Name: "tool schema in declaration order", Model: json.RawMessage(`{"reasoning":false}`), Options: json.RawMessage(`{"maxTokens":50}`), Tools: true,
			Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"File"},"offset":{"type":"number"}},"required":["path"]}`)},
		{Name: "strict tool schema in declaration order", Model: json.RawMessage(`{"reasoning":false,"compat":{"supportsStrictMode":true}}`), Options: json.RawMessage(`{"maxTokens":50}`), Tools: true,
			Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"number"}},"required":["path"]}`)},
		// applyAnthropicCacheControl assigns cache_control to the last tool after convertTools built it (:1125-1135).
		{Name: "cache control after the tool's members", Model: json.RawMessage(`{"reasoning":false,"compat":{"cacheControlFormat":"anthropic"}}`), Options: json.RawMessage(`{"maxTokens":50}`), Tools: true},
	}
	const script = `(async () => {
  const [root, casesJSON] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(require('node:path').join(root, rel)).href);
  const lazy = await load('node_modules/@earendil-works/pi-ai/dist/api/openai-completions.lazy.js');
  const transcript = await load('node_modules/@earendil-works/pi-ai/dist/utils/transcript.js');
  const out = {};
  for (const c of JSON.parse(casesJSON)) {
    const model = { id: 'm1', provider: 'custom', api: 'openai-completions', baseUrl: 'http://127.0.0.1:9/v1', input: ['text'], maxTokens: 32768, contextWindow: 131072, ...c.model };
    const tools = c.tools ? [{ name: 'read', description: 'Read a file', parameters: c.schema ?? { properties: { path: { type: 'string' } }, required: ['path'], type: 'object' } }] : undefined;
    const context = transcript.normalizeContext({ messages: [{ role: 'user', content: 'hi <b> & c\u2028', timestamp: 1 }], tools });
    const stream = lazy.openAICompletionsApi().stream(model, context, { apiKey: 'k', ...c.options,
      onPayload: (params) => { out[c.name] = JSON.stringify(params); throw new Error('captured'); } });
    await stream.result();
  }
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
			var piModel struct {
				Reasoning      bool           `json:"reasoning"`
				SamplingParams map[string]any `json:"samplingParams"`
				Compat         *OpenAICompat  `json:"compat"`
			}
			if err := json.Unmarshal(c.Model, &piModel); err != nil {
				t.Fatal(err)
			}
			var piOptions struct {
				MaxTokens      int            `json:"maxTokens"`
				Temperature    *float64       `json:"temperature"`
				ToolChoice     any            `json:"toolChoice"`
				SessionID      string         `json:"sessionId"`
				CacheRetention CacheRetention `json:"cacheRetention"`
				SamplingParams SamplingParams `json:"samplingParams"`
			}
			if err := json.Unmarshal(c.Options, &piOptions); err != nil {
				t.Fatal(err)
			}
			var body []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusBadRequest)
			}))
			defer server.Close()
			model := &Model{ID: "m1", Input: []string{"text"}, SamplingParams: piModel.SamplingParams,
				ProviderMeta: ProviderMetadata{ProviderID: "custom", API: APIOpenAICompletions, BaseURL: server.URL + "/v1", Reasoning: piModel.Reasoning},
				Capabilities: ModelCapabilities{MaxOutputTokens: 32768, ContextWindow: 131072}}
			if piModel.Reasoning {
				model.Capabilities.MaxThinking = ThinkingLevelHigh
			}
			provider := NewOpenAIProvider(OpenAIConfig{Model: model.ID, ModelMetadata: model, ProviderID: "custom", BaseURL: server.URL + "/v1", APIKey: "k", Compat: piModel.Compat, SamplingParams: piModel.SamplingParams})
			options := StreamOptions{MaxTokens: piOptions.MaxTokens, ToolChoice: piOptions.ToolChoice, SessionID: piOptions.SessionID, CacheRetention: piOptions.CacheRetention, SamplingParams: piOptions.SamplingParams,
				IsReasoning: piModel.Reasoning, Thinking: ThinkingLevel(c.thinking)}
			if piOptions.Temperature != nil {
				options.Temperature, options.TemperatureSet = *piOptions.Temperature, true
			}
			var tools []ToolSchema
			if c.Tools {
				schema := c.Schema
				if schema == nil {
					schema = json.RawMessage(`{"properties":{"path":{"type":"string"}},"required":["path"],"type":"object"}`)
				}
				tools = mustTools(t, `[{"name":"read","description":"Read a file","parameters":`+string(schema)+`}]`)
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
