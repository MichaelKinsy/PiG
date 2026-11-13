package ai_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// pi: packages/ai/src/api/anthropic-messages.ts

// anthropicWireRow is one request of the wire-body table: the model, the stream options and the Context, as the JSON both sides read.
type anthropicWireRow struct {
	Name      string          `json:"name"`
	Provider  string          `json:"provider"`
	BaseURL   string          `json:"baseUrl"`
	APIKey    string          `json:"apiKey"`
	Reasoning bool            `json:"reasoning"`
	Compat    json.RawMessage `json:"compat,omitempty"`
	Simple    bool            `json:"simple"`
	Options   json.RawMessage `json:"options"`
	Context   json.RawMessage `json:"context"`
}

// anthropicWireRows are the request shapes buildParams, convertMessages and convertTools have: each block kind as the last block of the
// conversation (the one that takes the cache breakpoint), the system blocks of both kinds of credential under each cache retention, the
// optional members of the request, a poisoned history, and each provider that sends its requests through this converter.
func anthropicWireRows() []anthropicWireRow {
	const (
		official = "https://api.anthropic.com"
		key      = "sk-ant-api03-x"
		oauth    = "sk-ant-oat01-x"
		system   = `"systemPrompt":"You are helpful."`
		readTool = `{"name":"read","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"number"}},"required":["path"]}}`
		findTool = `{"name":"find","description":"Find files","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}`
		tools    = `"tools":[` + readTool + `,` + findTool + `]`
		usage    = `"usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
		call     = `{"type":"toolCall","id":"call_1","name":"read","arguments":{"path":"a.txt","offset":2}}`
		text     = `{"type":"text","text":"ok"}`
		userGo   = `{"role":"user","content":"go","timestamp":1}`
		userNext = `{"role":"user","content":[{"type":"text","text":"next"}],"timestamp":5}`
		everyone = `{"toolChoice":"auto","metadata":{"user_id":"u1"}`
	)
	image := `{"type":"image","data":"` + payloadOraclePNG + `","mimeType":"image/png"}`
	assistant := func(provider, stop, blocks string) string {
		return `{"role":"assistant","content":[` + blocks + `],"api":"anthropic-messages","provider":"` + provider + `","model":"claude-test",` + usage + `,"stopReason":"` + stop + `","timestamp":2}`
	}
	result := func(id, content string, isError bool) string {
		return fmt.Sprintf(`{"role":"toolResult","toolCallId":"%s","toolName":"read","content":[%s],"isError":%v,"timestamp":3}`, id, content, isError)
	}
	context := func(members string, messages ...string) json.RawMessage {
		if members != "" {
			members += ","
		}
		return json.RawMessage("{" + members + `"messages":[` + strings.Join(messages, ",") + "]}")
	}
	// called is a turn that calls a tool, with every block kind an assistant message replays.
	called := func(provider string) string {
		return assistant(provider, "toolUse", `{"type":"text","text":"I will read."},{"type":"thinking","thinking":"plan","thinkingSignature":"EqQB"},{"type":"thinking","thinking":"","thinkingSignature":"opaque","redacted":true},`+call)
	}
	full := func(provider string) json.RawMessage {
		return context(system+","+tools, userGo, called(provider), result("call_1", text, false), userNext)
	}
	// after is a conversation that ends with the tool result holding content.
	after := func(content string, isError bool) json.RawMessage {
		return context(tools, userGo, called("anthropic"), result("call_1", content, isError))
	}

	var rows []anthropicWireRow
	add := func(name string, row anthropicWireRow) {
		row.Name = name
		if row.Provider == "" {
			row.Provider, row.BaseURL = "anthropic", official
		}
		if row.APIKey == "" {
			row.APIKey = key
		}
		if row.Options == nil {
			row.Options = json.RawMessage(`{}`)
		}
		rows = append(rows, row)
	}
	raw := func(text string) json.RawMessage { return json.RawMessage(text) }

	for _, credential := range []struct{ name, key string }{{"api key", key}, {"oauth", oauth}} {
		for _, retention := range []string{"none", "short", "long"} {
			add(credential.name+", retention "+retention, anthropicWireRow{APIKey: credential.key, Options: raw(`{"cacheRetention":"` + retention + `"}`), Context: full("anthropic")})
		}
		add(credential.name+", no system prompt", anthropicWireRow{APIKey: credential.key, Context: context("", userGo)})
	}
	add("default retention", anthropicWireRow{Context: full("anthropic")})
	add("long retention, model without it", anthropicWireRow{Compat: raw(`{"supportsLongCacheRetention":false}`), Options: raw(`{"cacheRetention":"long"}`), Context: full("anthropic")})
	add("model without breakpoints on tools", anthropicWireRow{Compat: raw(`{"supportsCacheControlOnTools":false}`), Context: full("anthropic")})
	add("no message", anthropicWireRow{Context: context("")})
	add("system prompt, no tools", anthropicWireRow{Context: context(system, userGo)})

	add("last block: user string", anthropicWireRow{Context: context(tools, userGo)})
	add("last block: user text", anthropicWireRow{Context: context(tools, userNext)})
	add("last block: user image after text", anthropicWireRow{Context: context("", `{"role":"user","content":[{"type":"text","text":"look"},`+image+`],"timestamp":1}`)})
	add("last block: user text after image", anthropicWireRow{Context: context("", `{"role":"user","content":[`+image+`,{"type":"text","text":"look"}],"timestamp":1}`)})
	add("last block: user image alone", anthropicWireRow{Context: context("", `{"role":"user","content":[`+image+`],"timestamp":1}`)})
	add("last block: tool result text", anthropicWireRow{Context: after(text, false)})
	add("last block: tool result with an image", anthropicWireRow{Context: after(text+","+image, false)})
	add("last block: tool result image alone", anthropicWireRow{Context: after(image, false)})
	add("last block: tool result without content", anthropicWireRow{Context: after("", false)})
	add("last block: error tool result", anthropicWireRow{Context: after(text, true)})
	add("last block: image of an error tool result", anthropicWireRow{Context: after(text+","+image, true)})
	add("last block: two tool results", anthropicWireRow{Context: context(tools, userGo,
		assistant("anthropic", "toolUse", call+`,{"type":"toolCall","id":"call_2","name":"find","arguments":{"query":"x"}}`), result("call_1", text, false), result("call_2", text, true))})
	add("last message: assistant", anthropicWireRow{Context: context(tools, userGo, assistant("anthropic", "stop", text))})

	add("unsigned thinking", anthropicWireRow{Context: context("", userGo, assistant("anthropic", "stop", `{"type":"thinking","thinking":"plan"},`+text), userNext)})
	add("unsigned thinking, model with empty signatures", anthropicWireRow{Compat: raw(`{"allowEmptySignature":true}`), Context: context("", userGo, assistant("anthropic", "stop", `{"type":"thinking","thinking":"plan"},`+text), userNext)})
	add("turn of another provider", anthropicWireRow{Context: context(system+","+tools, userGo, called("other"), result("call_1", text, false), userNext)})

	// A lone surrogate keeps its own escape where Pi does not sanitize the text (a tool description), and < > & U+2028 stay raw.
	add("lone surrogates and the characters JSON.stringify leaves raw", anthropicWireRow{Context: context(`"systemPrompt":"a\ud83db <&> \u2028","tools":[{"name":"read","description":"a\ude00b <&> \u2028","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}]`,
		`{"role":"user","content":"x\ud83d <&> \u2028","timestamp":1}`)})

	add("poisoned: errored turn", anthropicWireRow{Context: context(tools, userGo, assistant("anthropic", "error", `{"type":"text","text":"partial"},`+call), userNext)})
	add("poisoned: aborted turn", anthropicWireRow{Context: context(tools, userGo, assistant("anthropic", "aborted", `{"type":"thinking","thinking":"half"},`+call), userNext)})
	add("poisoned: empty text blocks", anthropicWireRow{Context: context(tools, userGo, assistant("anthropic", "stop", `{"type":"text","text":""},{"type":"text","text":"kept"}`),
		`{"role":"user","content":[{"type":"text","text":""},{"type":"text","text":"real"}],"timestamp":5}`)})
	add("poisoned: turn of empty text only", anthropicWireRow{Context: context(tools, userGo, assistant("anthropic", "stop", `{"type":"text","text":" "}`), userNext)})
	add("poisoned: orphaned tool call", anthropicWireRow{Context: context(tools, userGo, assistant("anthropic", "toolUse", `{"type":"text","text":"calling"},`+call), userNext)})
	add("poisoned: orphaned tool call at the end", anthropicWireRow{Context: context(tools, userGo, assistant("anthropic", "toolUse", call))})

	add("request: temperature, tools, tool choice, metadata", anthropicWireRow{Options: raw(everyone + `,"temperature":0.2,"maxTokens":4096}`), Context: full("anthropic")})
	add("request: budget thinking", anthropicWireRow{Reasoning: true, Options: raw(everyone + `,"thinkingEnabled":true,"thinkingBudgetTokens":2048,"temperature":0.2}`), Context: full("anthropic")})
	add("request: adaptive thinking with effort", anthropicWireRow{Reasoning: true, Compat: raw(`{"forceAdaptiveThinking":true}`), Options: raw(everyone + `,"thinkingEnabled":true,"effort":"high","thinkingDisplay":"omitted"}`), Context: full("anthropic")})
	add("request: thinking disabled", anthropicWireRow{Reasoning: true, Options: raw(everyone + `,"thinkingEnabled":false,"temperature":0.2}`), Context: full("anthropic")})
	add("request: simple stream with a level", anthropicWireRow{Reasoning: true, Simple: true, Options: raw(`{"reasoning":"high","temperature":0.2}`), Context: full("anthropic")})
	add("request: simple stream without a level", anthropicWireRow{Reasoning: true, Simple: true, Options: raw(`{"temperature":1}`), Context: full("anthropic")})
	// A tool choice object is the caller's own object: its members stay in the order the caller wrote them.
	add("request: tool choice object, type first", anthropicWireRow{Options: raw(`{"toolChoice":{"type":"tool","name":"read"}}`), Context: full("anthropic")})
	add("request: tool choice object, name first", anthropicWireRow{Options: raw(`{"toolChoice":{"name":"read","type":"tool"}}`), Context: full("anthropic")})
	add("request: managed effort", anthropicWireRow{Reasoning: true, Compat: raw(`{"supportsMidConvoEffort":true}`), Options: raw(everyone + `,"thinkingEnabled":true,"effort":"medium"}`), Context: full("anthropic")})
	add("request: fallback models", anthropicWireRow{Compat: raw(`{"allowedFallbackModels":[{"provider":"anthropic","model":"claude-other"}]}`), Options: raw(everyone + `}`), Context: full("anthropic")})
	add("request: native tool changes", anthropicWireRow{Compat: raw(`{"supportsMidConvoSystemMessages":true,"supportsMidConvoToolChanges":true}`), Options: raw(everyone + `}`), Context: context("",
		`{"role":"system","content":"base","timestamp":1,"toolsAdded":[`+readTool+`]}`, userGo, `{"role":"system","content":"more","timestamp":3,"toolsAdded":[`+findTool+`],"toolsRemoved":[`+readTool+`]}`)})

	// rich has an image in each place one can be: a user message, a tool result, and the last block of the conversation.
	rich := func(provider string) json.RawMessage {
		return context(system+","+tools, `{"role":"user","content":[`+image+`,{"type":"text","text":"look"}],"timestamp":1}`, called(provider), result("call_1", text+","+image, false),
			`{"role":"user","content":[{"type":"text","text":"next"},`+image+`],"timestamp":5}`)
	}
	// Every provider that sends its requests through this converter (packages/ai/src/providers), as a model under that provider's name.
	for _, route := range []struct{ name, provider, baseURL, key string }{
		{"anthropic with an api key", "anthropic", official, key},
		{"anthropic with oauth", "anthropic", official, oauth},
		{"anthropic at another address", "anthropic", "http://127.0.0.1:9", key},
		{"custom", "custom", "http://127.0.0.1:9/proxy", "k"},
		{"github-copilot", "github-copilot", "https://api.individual.githubcopilot.com", "k"},
		{"openrouter", "openrouter", "https://openrouter.ai/api", "k"},
		{"opencode", "opencode", "https://opencode.ai/zen", "k"},
		{"opencode-go", "opencode-go", "https://opencode.ai/zen/go", "k"},
		{"vercel-ai-gateway", "vercel-ai-gateway", "https://ai-gateway.vercel.sh", "k"},
		{"minimax", "minimax", "https://api.minimax.io/anthropic", "k"},
		{"minimax-cn", "minimax-cn", "https://api.minimaxi.com/anthropic", "k"},
		{"fireworks", "fireworks", "https://api.fireworks.ai/inference", "k"},
		{"cloudflare-ai-gateway", "cloudflare-ai-gateway", "https://gateway.ai.cloudflare.com/v1/account/gateway/anthropic", "k"},
	} {
		row := anthropicWireRow{Provider: route.provider, BaseURL: route.baseURL, APIKey: route.key, Context: full(route.provider)}
		add("route "+route.name+": replay", row)
		row.Context = rich(route.provider)
		row.Options = raw(everyone + `,"temperature":0.2}`)
		add("route "+route.name+": images, temperature, tool choice, metadata", row)
		row.Reasoning, row.Options = true, raw(everyone+`,"thinkingEnabled":true,"thinkingBudgetTokens":2048}`)
		add("route "+route.name+": images, thinking, tool choice, metadata", row)
	}
	return rows
}

// wireCapture keeps the body of the request it is given and answers 400, so the stream ends with the first request.
type wireCapture struct{ body []byte }

func (c *wireCapture) RoundTrip(request *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	c.body = body
	reply := `{"type":"error","error":{"type":"invalid_request_error","message":"captured"}}`
	return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(reply)), Request: request}, nil
}

// anthropicWireBody is the body PiG posts for a row.
func anthropicWireBody(t *testing.T, row anthropicWireRow) string {
	t.Helper()
	model := &ai.Model{ID: "claude-test", DisplayName: "claude-test", Input: []string{"text", "image"}, Capabilities: ai.ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 16384},
		ProviderMeta: ai.ProviderMetadata{API: ai.APIAnthropicMessages, ProviderID: row.Provider, BaseURL: row.BaseURL, Reasoning: row.Reasoning}}
	if row.Compat != nil {
		model.ProviderMeta.Compat = &ai.ModelCompat{}
		if err := json.Unmarshal(row.Compat, model.ProviderMeta.Compat); err != nil {
			t.Fatal(err)
		}
	}
	var given struct {
		MaxTokens            int                         `json:"maxTokens"`
		Temperature          *float64                    `json:"temperature"`
		CacheRetention       ai.CacheRetention           `json:"cacheRetention"`
		Reasoning            ai.ThinkingLevel            `json:"reasoning"`
		ThinkingEnabled      *bool                       `json:"thinkingEnabled"`
		ThinkingBudgetTokens *int                        `json:"thinkingBudgetTokens"`
		Effort               ai.AnthropicEffort          `json:"effort"`
		ThinkingDisplay      ai.AnthropicThinkingDisplay `json:"thinkingDisplay"`
		ToolChoice           json.RawMessage             `json:"toolChoice"`
		Metadata             map[string]any              `json:"metadata"`
	}
	if err := json.Unmarshal(row.Options, &given); err != nil {
		t.Fatal(err)
	}
	capture := &wireCapture{}
	options := ai.StreamOptions{APIKey: row.APIKey, MaxRetries: new(int), Fetch: &http.Client{Transport: capture}, MaxTokens: given.MaxTokens, CacheRetention: given.CacheRetention,
		Thinking: given.Reasoning, ThinkingEnabled: given.ThinkingEnabled, ThinkingBudgetTokens: given.ThinkingBudgetTokens, Effort: given.Effort, ThinkingDisplay: given.ThinkingDisplay,
		Metadata: given.Metadata}
	if given.Temperature != nil {
		options.Temperature, options.TemperatureSet = *given.Temperature, true
	}
	// A tool choice is a name, or the caller's object as the caller wrote it.
	if name := ""; json.Unmarshal(given.ToolChoice, &name) == nil {
		options.ToolChoice = name
	} else if given.ToolChoice != nil {
		options.ToolChoice = given.ToolChoice
	}
	run := ai.StreamAnthropic
	if row.Simple {
		run = ai.StreamSimpleAnthropic
	}
	stream, err := run(payloadContext(t), model, payloadTranscript(t, row.Context), options)
	if err == nil {
		stream.Result()
	}
	return string(capture.body)
}

// wireDifference says how PiG's body differs from Pi's, or "" when the bytes are equal.
func wireDifference(got, want string) string {
	if got == want {
		return ""
	}
	kind := "more than member order differs"
	var x, y any
	if json.Unmarshal([]byte(got), &x) == nil && json.Unmarshal([]byte(want), &y) == nil && reflect.DeepEqual(x, y) {
		kind = "member order differs"
	}
	at := 0
	for at < len(got) && at < len(want) && got[at] == want[at] {
		at++
	}
	from := max(0, at-40)
	return fmt.Sprintf("%s, first at byte %d:\n PiG ..%.140s\n Pi  ..%.140s\n PiG body %s\n Pi  body %s", kind, at, got[from:], want[from:], got, want)
}

// The body anthropic-messages posts equals Pi's byte for byte. JSON.stringify writes the members of each object in the order Pi's code
// created them: a literal's members in the order written, then the members assigned later (a block's cache_control, the request's system,
// temperature, tools, thinking, output_config, metadata, tool_choice and fallbacks). Pi's side is the body the Anthropic SDK hands to fetch.
func TestAnthropicWireBodyMatchesPiByteForByte(t *testing.T) {
	rows := anthropicWireRows()
	want := piPayloads(t, "testdata/anthropic_wire_body.mjs", rows)
	for i, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			if want[i] == "" {
				t.Fatal("Pi sent no request")
			}
			if difference := wireDifference(anthropicWireBody(t, row), want[i]); difference != "" {
				t.Error(difference)
			}
		})
	}
}
