package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/ai/providers/bedrock"
)

// probeRequest is one provider request: the transcript context and the
// per-request stream options.
type probeRequest struct {
	context ai.Context
	stream  ai.StreamOptions
}

type capture struct {
	path   string
	header http.Header
	body   string
}

func main() {
	_ = os.Setenv("AWS_BEDROCK_SKIP_AUTH", "1")
	_ = os.Setenv("AWS_REGION", "us-east-1")
	_ = os.Setenv("AWS_BEDROCK_FORCE_HTTP1", "1")

	out := &object{}
	out.set("openaiCompletions", probeOpenAICompletions())
	out.set("openaiCompletionsGrammar", probeOpenAICompletionsGrammar())
	out.set("openaiCompletions084", probeOpenAICompletions084())
	out.set("openaiCompletionsResponse", probeOpenAICompletionsResponse())
	out.set("openaiCompletionsUnknownFinish", probeOpenAICompletionsUnknownFinish())
	out.set("openaiResponses", probeOpenAIResponses())
	out.set("openaiCodexResponses", probeOpenAICodexResponses())
	out.set("azureOpenAIResponses", probeAzureOpenAIResponses())
	out.set("openaiResponsesGrammar", probeOpenAIResponsesGrammar())
	out.set("anthropic", probeAnthropic())
	out.set("google", probeGoogle())
	out.set("googleToolIds", probeGoogleToolIds())
	out.set("googleThinkingSig", probeGoogleThinkingSig())
	out.set("googleVertex", probeGoogleVertex())
	out.set("mistral", probeMistral())
	out.set("bedrock", probeBedrock())

	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(out); err != nil {
		panic(err)
	}
}

// probeOpenAICompletionsGrammar drives a grammar-constrained-sampling tool
// through the openai-completions path (supportsOpenAIGrammarTools) to exercise
// the custom (grammar) tool wire shape. Mirrors the pi grammar probe.
func probeOpenAICompletionsGrammar() any {
	server, cap := captureServer()
	defer server.Close()
	strictTrue := true
	opts := sampleOptions(nil, false)
	opts.context.Tools = []ai.ToolSchema{toolFromJSON(`{"name":"calc","description":"calculator","parameters":{"type":"object","properties":{"expr":{"type":"string"}},"required":["expr"]},"constrainedSampling":{"type":"grammar","variants":{"openai_lark":"start: NUMBER"}}}`)}
	return runProbeOptions(ai.NewOpenAIProvider(ai.OpenAIConfig{
		BaseURL:    server.URL,
		APIKey:     "sk-test",
		Model:      "grok-2",
		ProviderID: "xai",
		Compat: &ai.OpenAICompat{
			SupportsStore:              new(false),
			SupportsReasoningEffort:    new(true),
			SupportsUsageInStreaming:   new(true),
			MaxTokensField:             "max_tokens",
			SupportsOpenAIGrammarTools: &strictTrue,
		},
	}), cap, opts)
}

func probeOpenAICompletions() any {
	server, cap := captureServer()
	defer server.Close()
	return runProbe(ai.NewOpenAIProvider(ai.OpenAIConfig{
		BaseURL:    server.URL,
		APIKey:     "sk-test",
		Model:      "grok-2",
		ProviderID: "xai",
		Compat: &ai.OpenAICompat{
			SupportsStore:            new(false),
			SupportsReasoningEffort:  new(true),
			SupportsUsageInStreaming: new(true),
			MaxTokensField:           "max_tokens",
		},
	}), cap, false)
}

func probeOpenAICompletions084() any {
	server, cap := captureServer()
	defer server.Close()
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{
		BaseURL:        server.URL,
		APIKey:         "sk-test",
		Model:          "reasoning-model",
		ProviderID:     "baseten",
		SamplingParams: map[string]any{"top_p": 0.7, "min_p": 0.1},
		// Models arrive from models.json with their members in file order; Pi's model literal writes top_p, then min_p.
		SamplingParamsOrder: []string{"top_p", "min_p"},
		Compat: &ai.OpenAICompat{
			SupportsDeveloperRole:       new(false),
			SupportsStore:               new(false),
			SupportsReasoningEffort:     new(true),
			SupportsThinkingTokenBudget: new(true),
			MaxTokensField:              "max_tokens",
			ThinkingFormat:              "baseten",
			ChatTemplateArgs: map[string]any{
				"enable_thinking": map[string]any{"$var": "thinking.enabled"},
			},
		},
	})
	opts := sampleOptions(nil, true)
	opts.stream.MaxTokens = 4096
	opts.stream.SamplingParams = map[string]any{"top_p": 0.9}
	return runProbeOptions(provider, cap, opts)
}

// fauxUsageSSE reproduces a provider (e.g. gemini-3.5-flash via github-copilot)
// that attaches a usage object to content-bearing chunks rather than a trailing
// standalone chunk. The parser must record usage and keep processing; aborting
// on the first usage chunk drops the whole stream. Kept byte-identical with the
// pi harness's FAUX_USAGE_SSE so both parse the same wire bytes.
const fauxUsageSSE = `data: {"choices":[{"delta":{"content":"VER"},"finish_reason":null}],"usage":{"prompt_tokens":5,"completion_tokens":1}}

data: {"choices":[{"delta":{"content":"DICT"},"finish_reason":null}],"usage":{"prompt_tokens":5,"completion_tokens":2}}

data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3}}

data: [DONE]
`

func sseResponseServer(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
}

func probeOpenAICompletionsResponse() any {
	server := sseResponseServer(fauxUsageSSE)
	defer server.Close()
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{
		BaseURL:    server.URL,
		APIKey:     "sk-test",
		Model:      "grok-2",
		ProviderID: "xai",
		Compat: &ai.OpenAICompat{
			SupportsStore:            new(false),
			SupportsReasoningEffort:  new(true),
			SupportsUsageInStreaming: new(true),
			MaxTokensField:           "max_tokens",
		},
	})
	return collectResponse(provider)
}

const unknownFinishSSE = `data: {"choices":[{"delta":{},"finish_reason":"vendor_custom"}]}

data: [DONE]
`

func probeOpenAICompletionsUnknownFinish() any {
	server := sseResponseServer(unknownFinishSSE)
	defer server.Close()
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{
		BaseURL:    server.URL,
		APIKey:     "sk-test",
		Model:      "grok-2",
		ProviderID: "xai",
		Compat: &ai.OpenAICompat{
			SupportsStore:            new(false),
			SupportsReasoningEffort:  new(true),
			SupportsUsageInStreaming: new(true),
			MaxTokensField:           "max_tokens",
		},
	})
	return collectResponse(provider)
}

// collectResponse drives a provider against the faux SSE and aggregates the
// observed stream into a normalized shape the pi harness produces identically:
// full text, full reasoning, terminal stop reason, and input/output usage.
func collectResponse(provider ai.Provider) *object {
	opts := sampleOptions(func(any, *ai.Model) (any, error) { return nil, nil }, false)
	res := obj("text", "", "reasoning", "", "stop", "", "usageIn", 0, "usageOut", 0)
	stream, err := provider.Stream(context.Background(), ai.NormalizeContext(opts.context), opts.stream)
	if err != nil {
		res.set("error", err.Error())
		return res
	}
	var text, reasoning strings.Builder
	for event := range stream.Events(context.Background()) {
		switch ev := event.(type) {
		case ai.TextDeltaEvent:
			text.WriteString(ev.Delta)
		case ai.ThinkingDeltaEvent:
			reasoning.WriteString(ev.Delta)
		case ai.DoneEvent:
			res.set("stop", ev.Reason)
			if ev.Message != nil {
				res.set("usageIn", ev.Message.Usage.Input)
				res.set("usageOut", ev.Message.Usage.Output)
			}
		case ai.ErrorEvent:
			if ev.Error != nil {
				res.set("error", ev.Error.ErrorMessage)
			}
		}
	}
	res.set("text", text.String())
	res.set("reasoning", reasoning.String())
	return res
}

func probeOpenAIResponses() any {
	server, cap := captureServer()
	defer server.Close()
	return runProbe(ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{
		BaseURL:     server.URL,
		APIKey:      "sk-test",
		Model:       "gpt-5",
		ProviderID:  "openai",
		IsReasoning: true,
	}), cap, true)
}

// probeOpenAIResponsesGrammar drives a grammar-constrained-sampling tool through
// the generic openai-responses path (supportsOpenAIGrammarTools) to exercise the
// custom (grammar) tool wire shape. Mirrors the pi grammar probe.
func probeOpenAIResponsesGrammar() any {
	server, cap := captureServer()
	defer server.Close()
	strictTrue := true
	opts := sampleOptions(nil, true)
	opts.context.Tools = []ai.ToolSchema{toolFromJSON(`{"name":"calc","description":"calculator","parameters":{"type":"object","properties":{"expr":{"type":"string"}},"required":["expr"]},"constrainedSampling":{"type":"grammar","variants":{"openai_lark":"start: NUMBER"}}}`)}
	return runProbeOptions(ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{
		BaseURL:     server.URL,
		APIKey:      "sk-test",
		Model:       "gpt-5",
		ProviderID:  "openai",
		IsReasoning: true,
		Compat:      &ai.OpenAIResponsesCompat{SupportsOpenAIGrammarTools: &strictTrue},
	}), cap, opts)
}

func probeOpenAICodexResponses() any {
	server, cap := captureServer()
	defer server.Close()
	return runProbe(ai.NewOpenAICodexResponsesProvider(ai.OpenAICodexResponsesConfig{
		BaseURL:    server.URL,
		APIKey:     makeCodexJWT("acct_probe"),
		Model:      "gpt-5.2",
		ProviderID: "openai-codex",
	}), cap, true)
}

func probeAzureOpenAIResponses() any {
	server, cap := captureServer()
	defer server.Close()
	return runProbe(ai.NewAzureOpenAIResponsesProvider(ai.AzureOpenAIResponsesConfig{
		APIKey:     "azure-test",
		Model:      "gpt-4",
		ProviderID: "azure",
		Env:        ai.ProviderEnv{"AZURE_OPENAI_BASE_URL": server.URL},
	}), cap, false)
}

func probeAnthropic() any {
	server, cap := captureServer()
	defer server.Close()
	// Match anthropic.stream in provider-wire-pi.mjs, not streamSimple: native thinking does not add a reasoning-level budget to maxTokens.
	opts := sampleOptions(nil, true)
	opts.stream.Thinking = ""
	opts.stream.ThinkingEnabled = new(true)
	return runProbeOptions(ai.NewAnthropicProvider(ai.AnthropicConfig{
		BaseURL:    server.URL,
		APIKey:     "anth-test",
		Model:      "claude-haiku-4-5",
		ProviderID: "anthropic",
	}), cap, opts)
}

func probeGoogle() any {
	server, cap := captureServer()
	defer server.Close()
	return runProbe(ai.NewGoogleProvider(ai.GoogleConfig{
		BaseURL:    server.URL + "/v1beta",
		APIVersion: "",
		APIKey:     "gemini-test",
		Model:      "gemini-1.5-flash",
		ProviderID: "google",
	}), cap, false)
}

func probeGoogleVertex() any {
	server, cap := captureServer()
	defer server.Close()
	return runProbe(ai.NewGoogleVertexProvider(ai.GoogleVertexConfig{
		BaseURL:    server.URL + "/v1/publishers/google",
		APIKey:     "vertex-test",
		Model:      "gemini-1.5-flash",
		ProviderID: "google-vertex",
	}), cap, false)
}

// probeGoogleToolIds drives a requiresToolCallId model (gemini-3-pro) through a
// tool-call + tool-result turn so the payload exercises google-shared.ts:176/215
// id emission. The "|" in the id is normalized to "_" (google-shared.ts:101).
func probeGoogleToolIds() any {
	server, cap := captureServer()
	defer server.Close()
	opts := sampleOptions(nil, false)
	opts.context.Messages = []ai.Message{
		ai.AssistantMessage{Provider: "google", Model: "gemini-3-pro", Content: []ai.AssistantContentBlock{
			ai.ToolCall{ID: "call_abc|item_def", Name: "lookup", Arguments: ai.JsonObject{"q": "x"}},
		}},
		ai.ToolResultMessage{ToolCallID: "call_abc|item_def", ToolName: "lookup", Content: []ai.ToolResultMessageContent{
			ai.TextContent{Text: "found"},
		}},
	}
	return runProbeOptions(ai.NewGoogleProvider(ai.GoogleConfig{
		BaseURL:    server.URL + "/v1beta",
		APIKey:     "gemini-test",
		Model:      "gemini-3-pro",
		ProviderID: "google",
	}), cap, opts)
}

// probeGoogleThinkingSig drives a same-model (gemini-3-pro) assistant thinking
// block carrying a valid base64 signature so the payload exercises the outbound
// thought-signature resend (google-shared.ts:154-161).
func probeGoogleThinkingSig() any {
	server, cap := captureServer()
	defer server.Close()
	opts := sampleOptions(nil, false)
	opts.context.Tools = nil
	opts.context.Messages = []ai.Message{
		ai.AssistantMessage{Provider: "google", API: ai.APIGoogleGenerativeAI, Model: "gemini-3-pro", Content: []ai.AssistantContentBlock{
			ai.ThinkingContent{Thinking: "reasoning", ThinkingSignature: "c2lnbmF0dXJl"},
			ai.TextContent{Text: "answer"},
		}},
	}
	return runProbeOptions(ai.NewGoogleProvider(ai.GoogleConfig{
		BaseURL:    server.URL + "/v1beta",
		APIKey:     "gemini-test",
		Model:      "gemini-3-pro",
		ProviderID: "google",
	}), cap, opts)
}

func probeMistral() any {
	server, cap := captureServer()
	defer server.Close()
	return runProbe(ai.NewMistralProvider(ai.MistralConfig{
		BaseURL:    server.URL,
		APIKey:     "mistral-test",
		Model:      "codestral-latest",
		ProviderID: "mistral",
		Reasoning:  true,
	}), cap, true)
}

func probeBedrock() any {
	server, cap := captureServer()
	defer server.Close()
	provider := bedrock.NewBedrockProviderWithName("anthropic.claude-3-5-sonnet-20241022-v2:0", "Claude 3.5 Sonnet", server.URL)
	return runProbe(provider, cap, true)
}

func runProbe(provider ai.Provider, cap *capture, isReasoning bool) *object {
	return runProbeOptions(provider, cap, sampleOptions(nil, isReasoning))
}

func runProbeOptions(provider ai.Provider, cap *capture, opts probeRequest) *object {
	var payload any
	opts.stream.OnPayload = func(p any, _ *ai.Model) (any, error) {
		payload = p
		return nil, nil
	}
	stream, err := provider.Stream(context.Background(), ai.NormalizeContext(opts.context), opts.stream)
	if err == nil {
		for range stream.Events(context.Background()) {
		}
	}
	body := []byte(cap.body)
	if cap.header.Get("Content-Encoding") == "zstd" {
		command := exec.Command("node", "-e", `process.stdout.write(require('node:zlib').zstdDecompressSync(require('node:fs').readFileSync(0)))`)
		command.Stdin = strings.NewReader(cap.body)
		var err error
		body, err = command.Output()
		if err != nil {
			panic(err)
		}
	}
	if !json.Valid(body) {
		panic("provider probe did not capture a complete JSON request: " + provider.ID())
	}
	// Pi writes provider, path, auth, the codex headers, the wire body, then the payload summary.
	result := obj("provider", provider.ID(), "path", cap.path, "auth", authKind(cap.header))
	if provider.ID() == "openai-codex" {
		result.set("accountHeader", cap.header.Get("chatgpt-account-id"))
		result.set("sessionHeader", cap.header.Get("session-id"))
	}
	result.set("wireBody", json.RawMessage(body))
	result.set("payload", summarizePayload(provider.ID(), payload))
	return result
}

func sampleOptions(onPayload func(any, *ai.Model) (any, error), isReasoning bool) probeRequest {
	return probeRequest{
		context: ai.Context{
			SystemPrompt: "system prompt",
			Messages: []ai.Message{
				ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: "hello provider"}}},
				ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "prior answer"}}},
			},
			Tools: []ai.ToolSchema{toolFromJSON(`{"name":"lookup","description":"look things up","parameters":{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}}`)},
		},
		stream: ai.StreamOptions{
			MaxTokens:   123,
			Temperature: 0.2,
			Thinking:    ai.ThinkingLevelHigh,
			IsReasoning: isReasoning,
			SessionID:   "sess-provider-wire",
			OnPayload:   onPayload,
		},
	}
}

func captureServer() (*httptest.Server, *capture) {
	cap := &capture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cap.path = r.URL.RequestURI()
		cap.header = r.Header.Clone()
		cap.body = string(body)
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"error":{"message":"probe stop"}}`))
	}))
	return server, cap
}

func summarizePayload(provider string, payload any) *object {
	if payload == nil {
		return obj("kind", "nil")
	}
	if bedrock, ok := payload.(*bedrockruntime.ConverseStreamInput); ok {
		return obj(
			"kind", "bedrock-converse",
			"model", stringValue(bedrock.ModelId),
			"messages", len(bedrock.Messages),
			"system", len(bedrock.System),
			"tools", bedrock.ToolConfig != nil,
			"inference", bedrock.InferenceConfig != nil,
			"additional", bedrock.AdditionalModelRequestFields != nil,
		)
	}
	var v any
	b, _ := json.Marshal(payload)
	_ = json.Unmarshal(b, &v)
	m, _ := v.(map[string]any)
	// chat_template_args is a caller's object: it is summarized as the payload wrote it, member order and all, as Pi's JSON round trip keeps it.
	var members map[string]json.RawMessage
	_ = json.Unmarshal(b, &members)
	modelValue := m["model"]
	if provider == "google" || provider == "google-vertex" {
		modelValue = nil
	}
	out := obj(
		"kind", "json",
		"keys", normalizedKeys(provider, m),
		"model", modelValue,
		"tools", normalizedToolCount(provider, m),
		"maxFields", normalizedMaxFields(provider, m),
		"reasoning", m["reasoning"] != nil || m["reasoning_effort"] != nil || m["reasoningEffort"] != nil || m["thinking"] != nil || m["prompt_mode"] != nil,
		"stream", m["stream"],
		"store", m["store"],
		"sessionKey", m["prompt_cache_key"] != nil || m["prompt_cache_retention"] != nil,
	)
	if value, ok := m["top_p"]; ok {
		out.set("topP", value)
	}
	if value, ok := m["min_p"]; ok {
		out.set("minP", value)
	}
	if value, ok := m["thinking_token_budget"]; ok {
		out.set("thinkingTokenBudget", value)
	}
	if value, ok := members["chat_template_args"]; ok {
		out.set("chatTemplateArgs", value)
	}
	if roles := messageRoles(m["messages"]); len(roles) > 0 {
		out.set("messageRoles", roles)
	}
	if roles := inputRoles(m["input"]); len(roles) > 0 {
		out.set("inputRoles", roles)
	}
	if contents := geminiRoles(m["contents"]); len(contents) > 0 {
		out.set("contentRoles", contents)
	}
	if ids := geminiToolCallIDs(m["contents"]); len(ids) > 0 {
		out.set("toolCallIds", ids)
	}
	if sigs := geminiThoughtSignatures(m["contents"]); len(sigs) > 0 {
		out.set("thoughtSignatures", sigs)
	}
	if system := m["system"]; system != nil {
		out.set("system", true)
	}
	if system := m["systemInstruction"]; system != nil {
		out.set("systemInstruction", true)
	}
	if config, ok := m["config"].(map[string]any); ok {
		if config["systemInstruction"] != nil {
			out.set("systemInstruction", true)
		}
	}
	if shape := responsesToolShape(m); shape != nil {
		out.set("responsesToolShape", shape)
	}
	if shape := completionsToolShape(m); shape != nil {
		out.set("completionsToolShape", shape)
	}
	return out
}

// completionsToolShape surfaces the constrained-sampling-relevant shape of an
// openai-completions tools array. Unlike responses, completions nests strict
// under function and the grammar format under custom.format.grammar. It returns
// nil for responses payloads (identified by the "input" field) and for payloads
// without a tools array.
func completionsToolShape(m map[string]any) []*object {
	if _, ok := m["input"]; ok {
		return nil
	}
	tools, ok := m["tools"].([]any)
	if !ok || len(tools) == 0 {
		return nil
	}
	out := make([]*object, 0, len(tools))
	for _, t := range tools {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		fn, hasFn := tm["function"].(map[string]any)
		cu, hasCustom := tm["custom"].(map[string]any)
		if !hasFn && !hasCustom {
			// Not an openai-completions-shaped tool (e.g. anthropic/google).
			continue
		}
		shape := obj("type", tm["type"])
		if hasFn {
			if v, present := fn["strict"]; present {
				shape.set("strict", v)
			} else {
				shape.set("strict", "absent")
			}
		}
		if hasCustom {
			if f, ok := cu["format"].(map[string]any); ok {
				format := obj("type", f["type"])
				if g, ok := f["grammar"].(map[string]any); ok {
					format.set("syntax", g["syntax"])
				}
				shape.set("format", format)
			}
		}
		out = append(out, shape)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// responsesToolShape surfaces the constrained-sampling-relevant shape of an
// openai-responses tools array: each tool's type, whether strict is present (and
// its value), and any grammar format block. It returns nil for non-responses
// payloads (identified by the "input" field) so completions payloads are
// unaffected.
func responsesToolShape(m map[string]any) []*object {
	if _, ok := m["input"]; !ok {
		return nil
	}
	tools, ok := m["tools"].([]any)
	if !ok || len(tools) == 0 {
		return nil
	}
	out := make([]*object, 0, len(tools))
	for _, t := range tools {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		shape := obj("type", tm["type"])
		if v, present := tm["strict"]; present {
			shape.set("strict", v)
		} else {
			shape.set("strict", "absent")
		}
		if f, ok := tm["format"].(map[string]any); ok {
			shape.set("format", obj("type", f["type"], "syntax", f["syntax"]))
		}
		out = append(out, shape)
	}
	return out
}

func normalizedKeys(provider string, m map[string]any) []string {
	switch provider {
	case "google", "google-vertex":
		return []string{"contents", "generation", "system", "tools"}
	case "mistral":
		return []string{"max_tokens", "messages", "model", "reasoning", "stream", "temperature", "tools"}
	default:
		return sortedKeys(m)
	}
}

func normalizedToolCount(provider string, m map[string]any) int {
	if provider == "google" || provider == "google-vertex" {
		if config, ok := m["config"].(map[string]any); ok {
			return arrayLen(config["tools"])
		}
	}
	return arrayLen(m["tools"])
}

func normalizedMaxFields(_ string, m map[string]any) []string {
	fields := make([]string, 0, 3)
	fields = append(fields, presentKeys(m, "max_tokens", "max_completion_tokens", "max_output_tokens")...)
	if _, ok := m["maxTokens"]; ok {
		fields = append(fields, "max_tokens")
	}
	if _, ok := m["maxOutputTokens"]; ok {
		fields = append(fields, "max_output_tokens")
	}
	if config, ok := m["config"].(map[string]any); ok {
		if _, ok := config["maxOutputTokens"]; ok {
			fields = append(fields, "max_output_tokens")
		}
	}
	if config, ok := m["generationConfig"].(map[string]any); ok {
		if _, ok := config["maxOutputTokens"]; ok {
			fields = append(fields, "max_output_tokens")
		}
	}
	sort.Strings(fields)
	return slices.Compact(fields)
}

func messageRoles(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	roles := make([]string, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			roles = append(roles, stringAny(m["role"]))
		}
	}
	return roles
}

func inputRoles(v any) []string {
	switch raw := v.(type) {
	case []any:
		roles := make([]string, 0, len(raw))
		for _, item := range raw {
			if m, ok := item.(map[string]any); ok {
				roles = append(roles, firstNonEmptyString(stringAny(m["type"]), stringAny(m["role"])))
			}
		}
		return roles
	case string:
		var arr []any
		if json.Unmarshal([]byte(raw), &arr) == nil {
			return inputRoles(arr)
		}
	}
	return nil
}

func geminiRoles(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	roles := make([]string, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			roles = append(roles, stringAny(m["role"]))
		}
	}
	return roles
}

// geminiThoughtSignatures collects every part's thoughtSignature from the gemini
// contents so the wire comparison asserts the resent thinking/text signatures
// (google-shared.ts:148/161/178).
func geminiThoughtSignatures(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var sigs []string
	for _, item := range arr {
		content, ok := item.(map[string]any)
		if !ok {
			continue
		}
		parts, ok := content["parts"].([]any)
		if !ok {
			continue
		}
		for _, p := range parts {
			part, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if s := stringAny(part["thoughtSignature"]); s != "" {
				sigs = append(sigs, s)
			}
		}
	}
	return sigs
}

// geminiToolCallIDs collects functionCall.id / functionResponse.id from the
// gemini contents so the wire comparison asserts the emitted tool-call ids
// (google-shared.ts:176/215), not just role structure.
func geminiToolCallIDs(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var ids []string
	for _, item := range arr {
		content, ok := item.(map[string]any)
		if !ok {
			continue
		}
		parts, ok := content["parts"].([]any)
		if !ok {
			continue
		}
		for _, p := range parts {
			part, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if fc, ok := part["functionCall"].(map[string]any); ok {
				ids = append(ids, "call:"+stringAny(fc["id"]))
			}
			if fr, ok := part["functionResponse"].(map[string]any); ok {
				ids = append(ids, "resp:"+stringAny(fr["id"]))
			}
		}
	}
	return ids
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if v != nil {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func presentKeys(m map[string]any, names ...string) []string {
	var out []string
	for _, name := range names {
		if _, ok := m[name]; ok {
			out = append(out, name)
		}
	}
	return out
}

func arrayLen(v any) int {
	arr, ok := v.([]any)
	if !ok {
		return 0
	}
	return len(arr)
}

func authKind(h http.Header) string {
	switch {
	case h.Get("Authorization") != "":
		return "authorization"
	case h.Get("X-Api-Key") != "":
		return "x-api-key"
	case h.Get("api-key") != "":
		return "api-key"
	case h.Get("x-goog-api-key") != "":
		return "x-goog-api-key"
	case h.Get("cf-aig-authorization") != "":
		return "cf-aig-authorization"
	default:
		return "none"
	}
}

func stringValue(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

func stringAny(v any) string {
	s, _ := v.(string)
	return s
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func makeCodexJWT(accountID string) string {
	claims, _ := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": accountID},
	})
	return "header." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
}

//go:fix inline

// toolFromJSON declares a tool as JSON text, the way an extension or an MCP server hands pig its schema: ai.ToolSchema keeps that member order, where a Go map carries none and is sent sorted. Pi's probe holds the same members in a literal.
func toolFromJSON(text string) ai.ToolSchema {
	var tool ai.ToolSchema
	if err := json.Unmarshal([]byte(text), &tool); err != nil {
		panic(err)
	}
	return tool
}

// object is a JSON object that keeps its members in the order they were set, the order Pi's probe writes them: a Go map would write them sorted.
type object struct{ members []member }

type member struct {
	key   string
	value any
}

// obj builds an object from alternating keys and values.
func obj(pairs ...any) *object {
	o := &object{}
	for i := 0; i < len(pairs); i += 2 {
		o.set(pairs[i].(string), pairs[i+1])
	}
	return o
}

// set replaces the member key keeps, or appends one, as an assignment to a JavaScript property does.
func (o *object) set(key string, value any) {
	for i := range o.members {
		if o.members[i].key == key {
			o.members[i].value = value
			return
		}
	}
	o.members = append(o.members, member{key, value})
}

func (o *object) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, m := range o.members {
		if i > 0 {
			out.WriteByte(',')
		}
		key, err := json.Marshal(m.key)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(m.value)
		if err != nil {
			return nil, err
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}
