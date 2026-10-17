package ai

import (
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Oracle for packages/ai/src/api/openai-responses-shared.ts: Pi 1.0.4's own convertResponsesMessages and convertResponsesTools run
// in Node (the pi-ai under extensions/sdk-ts/node_modules) over the same inputs, and the Go functions must produce the same input
// items and tools. One Node process serves every case.

type responsesSharedCase struct {
	name string
	// model is Pi's Model JSON; the Go Model is built from the same fields.
	model        *Model
	systemPrompt string
	tools        string // Pi Tool[] JSON
	messages     []Message
	allowed      map[string]bool
	options      ConvertResponsesMessagesOptions
	piOptions    string // Pi options JSON, without grammarToolInputProperties
	grammarProps map[string]string
}

var (
	responsesSharedOnce    sync.Once
	responsesSharedResults map[string]json.RawMessage
	responsesSharedErr     error
)

const responsesSharedScript = `(async () => {
  const path = require('node:path');
  const root = process.argv[1];
  const load = (rel) => import(require('node:url').pathToFileURL(path.join(root, 'node_modules/@earendil-works/pi-ai/dist', rel)).href);
  const shared = await load('api/openai-responses-shared.js');
  const transcript = await load('utils/transcript.js');
  const cases = JSON.parse(require('node:fs').readFileSync(0, 'utf8'));
  const out = {};
  for (const c of cases) {
    try {
      if (c.kind === 'messages') {
        const options = { ...c.options };
        if (c.grammar) options.grammarToolInputProperties = new Map(Object.entries(c.grammar));
        const context = transcript.normalizeContext({ systemPrompt: c.systemPrompt || undefined, tools: c.tools, messages: c.messages });
        out[c.name] = { ok: shared.convertResponsesMessages(c.model, context, new Set(c.allowed), options) };
      } else {
        out[c.name] = { ok: shared.convertResponsesTools(c.tools, c.options) };
      }
    } catch (e) { out[c.name] = { error: e.message }; }
  }
  console.log(JSON.stringify(out));
})()`

func piResponsesShared(t *testing.T, wire []map[string]any) map[string]json.RawMessage {
	t.Helper()
	responsesSharedOnce.Do(func() {
		input, err := json.Marshal(wire)
		if err != nil {
			responsesSharedErr = err
			return
		}
		cmd := exec.Command("node", "-e", responsesSharedScript, piCodingAgentPackage(t))
		cmd.Stdin = strings.NewReader(string(input))
		cmd.Stderr = os.Stderr
		raw, err := cmd.Output()
		if err != nil {
			responsesSharedErr = err
			return
		}
		responsesSharedErr = json.Unmarshal(raw, &responsesSharedResults)
	})
	if responsesSharedErr != nil {
		t.Fatalf("Pi oracle: %v", responsesSharedErr)
	}
	return responsesSharedResults
}

func sharedCanonicalJSON(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func mustMessagesJSON(t *testing.T, messages []Message) []json.RawMessage {
	t.Helper()
	out := make([]json.RawMessage, len(messages))
	for i, message := range messages {
		raw, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = raw
	}
	return out
}

func mustTools(t *testing.T, raw string) []ToolSchema {
	t.Helper()
	var tools []ToolSchema
	if err := json.Unmarshal([]byte(raw), &tools); err != nil {
		t.Fatal(err)
	}
	return tools
}

func sharedModel(id, provider string, api API, reasoning bool, input []string, compat *ModelCompat) *Model {
	return &Model{ID: id, Input: input, ProviderMeta: ProviderMetadata{ProviderID: provider, API: api, Reasoning: reasoning, Compat: compat}}
}

func modelWire(m *Model) map[string]any {
	wire := map[string]any{"id": m.ID, "provider": m.ProviderMeta.ProviderID, "api": string(m.ProviderMeta.API), "reasoning": m.ProviderMeta.Reasoning, "input": m.Input}
	if m.ProviderMeta.Compat != nil {
		wire["compat"] = m.ProviderMeta.Compat
	}
	return wire
}

// openai-responses-shared.ts:143-337 convertResponsesMessages.
func TestConvertResponsesMessagesMatchesPi(t *testing.T) {
	openai := sharedModel("gpt-5", "openai", APIOpenAIResponses, true, []string{"text", "image"}, nil)
	plain := sharedModel("gpt-4.1", "openai", APIOpenAIResponses, false, []string{"text"}, nil)
	noDev := sharedModel("o-mini", "openai", APIOpenAIResponses, true, []string{"text"}, &ModelCompat{SupportsDeveloperRole: new(false)})
	allowed := map[string]bool{"openai": true, "openai-codex": true, "opencode": true}
	longSignature := strings.Repeat("s", 70)
	textSignature := func(id, phase string) string {
		raw, _ := json.Marshal(map[string]any{"v": 1, "id": id, "phase": phase})
		return string(raw)
	}
	assistant := func(provider string, api API, model string, content ...AssistantContentBlock) AssistantMessage {
		return AssistantMessage{Provider: provider, API: api, Model: model, Content: content, StopReason: StopReasonStop, Timestamp: 1}
	}
	toolArgs := JsonObject{}
	if err := json.Unmarshal([]byte(`{"city":"Oslo","days":2}`), &toolArgs); err != nil {
		t.Fatal(err)
	}
	conversation := []Message{
		UserMessage{Content: UserText("hello"), Timestamp: 1},
		UserMessage{Content: UserContentBlocks{TextContent{Text: "look"}, ImageContent{Data: "AAAA", MimeType: "image/png"}}, Timestamp: 2},
		assistant("openai", APIOpenAIResponses, "gpt-5",
			ThinkingContent{Thinking: "t", ThinkingSignature: `{"type":"reasoning","id":"rs_1","summary":[]}`},
			TextContent{Text: "answer", TextSignature: textSignature("msg_abc", "final_answer")},
			TextContent{Text: "second"},
			TextContent{Text: "long id", TextSignature: textSignature(longSignature, "")},
			ToolCall{ID: "call_1|fc_1", Name: "weather", Arguments: toolArgs, Namespace: "ns"},
		),
		ToolResultMessage{ToolCallID: "call_1|fc_1", ToolName: "weather", Content: []ToolResultMessageContent{TextContent{Text: "sunny"}, ImageContent{Data: "BBBB", MimeType: "image/png"}}, Timestamp: 3},
		ToolResultMessage{ToolCallID: "call_2|fc_2", ToolName: "weather", Content: []ToolResultMessageContent{}, Timestamp: 4},
		assistant("openai", APIOpenAIResponses, "gpt-4o", ToolCall{ID: "call_3|fc_3", Name: "weather", Arguments: toolArgs, Namespace: "ns"}),
		assistant("anthropic", APIAnthropicMessages, "claude", ToolCall{ID: "toolu_01 bad!|item/9", Name: "weather", Arguments: toolArgs}),
		assistant("openai", APIOpenAIResponses, "gpt-5"),
	}
	grammarTools := `[{"name":"weather","description":"w","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}]`
	cases := []responsesSharedCase{
		{name: "reasoning model, system prompt, full conversation", model: openai, systemPrompt: "be brief", messages: conversation, allowed: allowed},
		{name: "includeSystemPrompt false drops the leading system message", model: openai, systemPrompt: "be brief", messages: conversation[:1], allowed: allowed, options: ConvertResponsesMessagesOptions{IncludeSystemPrompt: new(false)}, piOptions: `{"includeSystemPrompt":false}`},
		{name: "non-reasoning model uses the system role and drops images", model: plain, systemPrompt: "be brief", messages: conversation, allowed: allowed},
		{name: "supportsDeveloperRole false keeps the system role for a reasoning model", model: noDev, systemPrompt: "be brief", messages: conversation[:1], allowed: allowed},
		{name: "provider outside the allowed set only sanitizes ids", model: plain, messages: conversation, allowed: map[string]bool{}},
		{name: "grammar tool calls and results are custom tool items", model: openai, tools: grammarTools, messages: conversation[2:6], allowed: allowed, grammarProps: map[string]string{"weather": "city"}, options: ConvertResponsesMessagesOptions{GrammarToolInputProperties: map[string]string{"weather": "city"}}},
	}
	// Later system messages: in place when mid-convo system messages are supported, tools added as additional_tools or tool search.
	later := []Message{
		UserMessage{Content: UserText("go"), Timestamp: 1},
		SystemMessage{Content: SystemText("rule update"), ToolsAdded: mustTools(t, `[{"name":"extra","description":"e","parameters":{"type":"object","properties":{}}}]`), Timestamp: 2},
		UserMessage{Content: UserText("again"), Timestamp: 3},
	}
	cases = append(cases,
		responsesSharedCase{name: "mid-convo system message with additional tools", model: openai, systemPrompt: "base", messages: later, allowed: allowed,
			options: ConvertResponsesMessagesOptions{SupportsMidConvoSystemMessages: true, SupportsAdditionalTools: true}, piOptions: `{"supportsMidConvoSystemMessages":true,"supportsAdditionalTools":true}`},
		responsesSharedCase{name: "mid-convo system message with tool search", model: openai, systemPrompt: "base", messages: later, allowed: allowed,
			options: ConvertResponsesMessagesOptions{SupportsMidConvoSystemMessages: true, SupportsToolSearch: true}, piOptions: `{"supportsMidConvoSystemMessages":true,"supportsToolSearch":true}`},
		// openai-responses-shared.ts:131,191,210: toolOptions reaches convertResponsesTools for the additional_tools item and for the
		// tool_search_output (which also adds defer_loading); strict true requests strict function tools, null sends strict: null.
		responsesSharedCase{name: "additional tools take toolOptions strict true", model: openai, systemPrompt: "base", messages: later, allowed: allowed,
			options: ConvertResponsesMessagesOptions{SupportsMidConvoSystemMessages: true, SupportsAdditionalTools: true, ToolOptions: ConvertResponsesToolsOptions{Strict: ResponsesStrictTrue}}, piOptions: `{"supportsMidConvoSystemMessages":true,"supportsAdditionalTools":true,"toolOptions":{"strict":true}}`},
		responsesSharedCase{name: "tool search output takes toolOptions strict null", model: openai, systemPrompt: "base", messages: later, allowed: allowed,
			options: ConvertResponsesMessagesOptions{SupportsMidConvoSystemMessages: true, SupportsToolSearch: true, ToolOptions: ConvertResponsesToolsOptions{Strict: ResponsesStrictNull}}, piOptions: `{"supportsMidConvoSystemMessages":true,"supportsToolSearch":true,"toolOptions":{"strict":null}}`},
		responsesSharedCase{name: "mid-convo system message folded into the leading prompt", model: openai, systemPrompt: "base", messages: later, allowed: allowed},
	)
	wire := make([]map[string]any, 0, len(cases))
	for _, c := range cases {
		tools := json.RawMessage(`[]`)
		if c.tools != "" {
			tools = json.RawMessage(c.tools)
		}
		entry := map[string]any{"kind": "messages", "name": c.name, "model": modelWire(c.model), "systemPrompt": c.systemPrompt, "tools": tools,
			"messages": mustMessagesJSON(t, c.messages), "allowed": setKeys(c.allowed), "options": json.RawMessage(cmpOr(c.piOptions, "{}")), "grammar": c.grammarProps}
		wire = append(wire, entry)
	}
	oracle := toolsOracleWire(t)
	wire = append(wire, oracle...)
	results := piResponsesShared(t, wire)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var want struct {
				OK    any    `json:"ok"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal(results[c.name], &want); err != nil || want.Error != "" {
				t.Fatalf("Pi: %v %s", err, want.Error)
			}
			var tools []ToolSchema
			if c.tools != "" {
				tools = mustTools(t, c.tools)
			}
			context := NormalizeContext(Context{SystemPrompt: c.systemPrompt, Tools: tools, Messages: c.messages})
			got, err := ConvertResponsesMessages(c.model, context, c.allowed, c.options)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(sharedCanonicalJSON(t, got), want.OK) {
				gotJSON, _ := json.MarshalIndent(got, "", " ")
				wantJSON, _ := json.MarshalIndent(want.OK, "", " ")
				t.Fatalf("ConvertResponsesMessages differs from Pi\ngot:\n%s\nwant:\n%s", gotJSON, wantJSON)
			}
		})
	}
	runToolsOracle(t, results)
}

func setKeys(m map[string]bool) []string {
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func cmpOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

type responsesToolsCase struct {
	name    string
	tools   string
	options ConvertResponsesToolsOptions
	piOpts  string
}

func responsesToolsCases() []responsesToolsCase {
	plain := `[{"name":"weather","description":"w","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}]`
	prefer := `[{"name":"weather","description":"w","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]},"constrainedSampling":{"type":"json_schema","strict":"prefer"}}]`
	require := `[{"name":"weather","description":"w","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]},"constrainedSampling":{"type":"json_schema","strict":"require"}}]`
	grammar := `[{"name":"patch","description":"p","parameters":{"type":"object","properties":{"input":{"type":"string"}}},"constrainedSampling":{"type":"grammar","variants":{"openai_lark":"start: /[a-z]+/"}}}]`
	return []responsesToolsCase{
		{"tools: defaults are strict false and strict mode on", plain, ConvertResponsesToolsOptions{}, `{}`},
		{"tools: strict default true makes the schema strict", plain, ConvertResponsesToolsOptions{Strict: ResponsesStrictTrue}, `{"strict":true}`},
		{"tools: strict default null (Codex)", plain, ConvertResponsesToolsOptions{Strict: ResponsesStrictNull}, `{"strict":null}`},
		{"tools: strict mode unsupported omits strict", plain, ConvertResponsesToolsOptions{SupportsStrictMode: new(false)}, `{"supportsStrictMode":false}`},
		{"tools: json_schema prefer requests strict", prefer, ConvertResponsesToolsOptions{Strict: ResponsesStrictNull}, `{"strict":null}`},
		{"tools: json_schema prefer falls back when strict mode is unsupported", prefer, ConvertResponsesToolsOptions{SupportsStrictMode: new(false)}, `{"supportsStrictMode":false}`},
		{"tools: json_schema require fails when strict mode is unsupported", require, ConvertResponsesToolsOptions{SupportsStrictMode: new(false)}, `{"supportsStrictMode":false}`},
		{"tools: grammar tool becomes a custom tool", grammar, ConvertResponsesToolsOptions{SupportsOpenAIGrammarTools: true}, `{"supportsOpenAIGrammarTools":true}`},
		{"tools: grammar tool falls back to a function tool", grammar, ConvertResponsesToolsOptions{}, `{}`},
		{"tools: toolSearchResult defers loading", grammar, ConvertResponsesToolsOptions{SupportsOpenAIGrammarTools: true, ToolSearchResult: true}, `{"supportsOpenAIGrammarTools":true,"toolSearchResult":true}`},
		{"tools: toolSearchResult defers a function tool", plain, ConvertResponsesToolsOptions{ToolSearchResult: true}, `{"toolSearchResult":true}`},
	}
}

func toolsOracleWire(t *testing.T) []map[string]any {
	var wire []map[string]any
	for _, c := range responsesToolsCases() {
		wire = append(wire, map[string]any{"kind": "tools", "name": c.name, "tools": json.RawMessage(c.tools), "options": json.RawMessage(c.piOpts)})
	}
	return wire
}

// openai-responses-shared.ts:339-376 convertResponsesTools.
func runToolsOracle(t *testing.T, results map[string]json.RawMessage) {
	for _, c := range responsesToolsCases() {
		t.Run(c.name, func(t *testing.T) {
			var want struct {
				OK    any    `json:"ok"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal(results[c.name], &want); err != nil {
				t.Fatal(err)
			}
			got, err := ConvertResponsesTools(mustTools(t, c.tools), c.options)
			if want.Error != "" {
				if err == nil || err.Error() != want.Error {
					t.Fatalf("error = %v, want Pi's %q", err, want.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(sharedCanonicalJSON(t, got), want.OK) {
				gotJSON, _ := json.MarshalIndent(got, "", " ")
				wantJSON, _ := json.MarshalIndent(want.OK, "", " ")
				t.Fatalf("ConvertResponsesTools differs from Pi\ngot:\n%s\nwant:\n%s", gotJSON, wantJSON)
			}
		})
	}
}
