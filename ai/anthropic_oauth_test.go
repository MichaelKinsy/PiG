package ai

// Ports .upstream/current/packages/ai/test/anthropic-auth-token.test.ts and
// anthropic-tool-name-normalization.test.ts as serialized-request tests. The
// upstream tool-name suite runs against the live API; here a local server
// echoes the Claude Code tool name the model would return.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const anthropicEndTurnSSE = `event: message_start
data: {"type":"message_start","message":{"id":"msg_test","usage":{"input_tokens":1,"output_tokens":0}}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}

event: message_stop
data: {"type":"message_stop"}

`

type anthropicWireCapture struct {
	header http.Header
	body   map[string]any
}

func anthropicToolUseSSE(name string) string {
	return fmt.Sprintf(`event: message_start
data: {"type":"message_start","message":{"id":"msg_tool","usage":{"input_tokens":1,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":%q,"input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"/tmp/test.txt\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`, name)
}

// clearAnthropicAuthEnv isolates a test from a developer's Anthropic env.
func clearAnthropicAuthEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "PI_CACHE_RETENTION"} {
		t.Setenv(name, "")
	}
}

// runAnthropicWire streams one request through a local server that answers
// with sse (or the SSE its responder returns for the request body) and returns
// the captured request and the emitted events.
func runAnthropicWire(t *testing.T, cfg AnthropicConfig, context Context, opts StreamOptions, respond func(body map[string]any) string) (anthropicWireCapture, []AssistantMessageEvent) {
	t.Helper()
	var captured anthropicWireCapture
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		captured.header = r.Header.Clone()
		if err := json.Unmarshal(raw, &captured.body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, respond(captured.body))
	}))
	t.Cleanup(server.Close)
	cfg.BaseURL = server.URL
	if cfg.Model == "" {
		cfg.Model = "claude-test"
	}
	stream, err := NewAnthropicProvider(cfg).Stream(t.Context(), NormalizeContext(context), opts)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var events []AssistantMessageEvent
	for event := range stream.Events(t.Context()) {
		events = append(events, event)
	}
	return captured, events
}

func endTurn(map[string]any) string { return anthropicEndTurnSSE }

var anthropicAuthContext = Context{
	SystemPrompt: "System prompt.",
	Messages:     []Message{UserMessage{Content: UserText("Hello")}},
	Tools: []ToolSchema{
		{Name: "read", Description: "Read a file", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}},
		{Name: "todowrite", Description: "Write a todo item", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
	},
}

func requestToolNames(body map[string]any) []string {
	var names []string
	tools, _ := body["tools"].([]any)
	for _, tool := range tools {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	return names
}

func systemTexts(body map[string]any) []string {
	var texts []string
	blocks, _ := body["system"].([]any)
	for _, block := range blocks {
		block := block.(map[string]any)
		if cc, _ := block["cache_control"].(map[string]any); cc["type"] != "ephemeral" {
			texts = append(texts, "missing cache_control: "+block["text"].(string))
			continue
		}
		texts = append(texts, block["text"].(string))
	}
	return texts
}

// TestAnthropicRequestAuthShapes serializes one request per auth source and
// checks the auth headers, Claude Code identity headers, betas, system blocks,
// and tool names upstream createClient and buildParams produce.
func TestAnthropicRequestAuthShapes(t *testing.T) {
	oauthBetas := "claude-code-20250219,oauth-2025-04-20"
	for _, tc := range []struct {
		name          string
		cfg           AnthropicConfig
		env           map[string]string
		headers       ProviderHeaders
		apiKey        string
		authorization string
		userAgent     string
		xApp          string
		beta          string
		system        []string
		tools         []string
	}{
		{
			name:   "api key",
			cfg:    AnthropicConfig{APIKey: "sk-ant-api03-test"},
			apiKey: "sk-ant-api03-test", system: []string{"System prompt."}, tools: []string{"read", "todowrite"},
		},
		{
			name:          "oauth token",
			cfg:           AnthropicConfig{APIKey: "sk-ant-oat01-test"},
			authorization: "Bearer sk-ant-oat01-test", userAgent: "claude-cli/2.1.280", xApp: "cli", beta: oauthBetas,
			system: []string{claudeCodeSystemPrompt, "System prompt."}, tools: []string{"Read", "TodoWrite"},
		},
		{
			// anthropic-auth-token.test.ts: preserves OAuth request shaping for ANTHROPIC_OAUTH_TOKEN.
			name:          "oauth token wins over ANTHROPIC_AUTH_TOKEN once resolved as the key",
			cfg:           AnthropicConfig{APIKey: "sk-ant-oat-test"},
			env:           map[string]string{"ANTHROPIC_AUTH_TOKEN": "auth-token"},
			authorization: "Bearer sk-ant-oat-test", userAgent: "claude-cli/2.1.280", xApp: "cli", beta: oauthBetas,
			system: []string{claudeCodeSystemPrompt, "System prompt."}, tools: []string{"Read", "TodoWrite"},
		},
		{
			// anthropic-auth-token.test.ts: resolves ANTHROPIC_AUTH_TOKEN as a bearer Authorization header.
			name:          "ANTHROPIC_AUTH_TOKEN",
			env:           map[string]string{"ANTHROPIC_AUTH_TOKEN": "auth-token"},
			authorization: "Bearer auth-token", system: []string{"System prompt."}, tools: []string{"read", "todowrite"},
		},
		{
			// anthropic-auth-token.test.ts: threads authContext ANTHROPIC_AUTH_TOKEN through request headers.
			name:          "ANTHROPIC_AUTH_TOKEN from provider env",
			cfg:           AnthropicConfig{Env: ProviderEnv{"ANTHROPIC_AUTH_TOKEN": "ctx-token"}},
			authorization: "Bearer ctx-token", system: []string{"System prompt."}, tools: []string{"read", "todowrite"},
		},
		{
			// anthropic-auth-token.test.ts: lets explicit request headers override ANTHROPIC_AUTH_TOKEN.
			name:          "request Authorization overrides ANTHROPIC_AUTH_TOKEN",
			env:           map[string]string{"ANTHROPIC_AUTH_TOKEN": "ctx-token"},
			headers:       ProviderHeaders{"Authorization": new("Bearer explicit-token")},
			authorization: "Bearer explicit-token", system: []string{"System prompt."}, tools: []string{"read", "todowrite"},
		},
		{
			// anthropic-auth-token.test.ts: uses Authorization headers without OAuth-mode request shaping.
			name:          "request Authorization without a key",
			headers:       ProviderHeaders{"Authorization": new("Bearer gateway-token")},
			authorization: "Bearer gateway-token", system: []string{"System prompt."}, tools: []string{"read", "todowrite"},
		},
		{
			// A stored or configured key owns the provider; the ambient token is not consulted.
			name:   "configured key outranks ANTHROPIC_AUTH_TOKEN",
			cfg:    AnthropicConfig{APIKey: "sk-ant-api03-test"},
			env:    map[string]string{"ANTHROPIC_AUTH_TOKEN": "auth-token"},
			apiKey: "sk-ant-api03-test", system: []string{"System prompt."}, tools: []string{"read", "todowrite"},
		},
		{
			name:   "ANTHROPIC_AUTH_TOKEN applies only to the anthropic provider",
			cfg:    AnthropicConfig{ProviderID: "kimi-coding", APIKey: "kimi-key"},
			env:    map[string]string{"ANTHROPIC_AUTH_TOKEN": "auth-token"},
			apiKey: "kimi-key", system: []string{"System prompt."}, tools: []string{"read", "todowrite"},
		},
		{
			// The upstream github-copilot branch precedes the OAuth-token branch.
			name:          "copilot bearer client never uses the Claude Code identity",
			cfg:           AnthropicConfig{ProviderID: "github-copilot", APIKey: "sk-ant-oat-copilot", UseBearerAuth: true},
			authorization: "Bearer sk-ant-oat-copilot", system: []string{"System prompt."}, tools: []string{"read", "todowrite"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearAnthropicAuthEnv(t)
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			captured, events := runAnthropicWire(t, tc.cfg, anthropicAuthContext, StreamOptions{Headers: tc.headers}, endTurn)
			if message := anthropicTerminal(t, events); message.StopReason != StopReasonStop {
				t.Fatalf("stop reason = %q (%s)", message.StopReason, message.ErrorMessage)
			}
			header := captured.header
			for name, want := range map[string]string{
				"X-Api-Key":      tc.apiKey,
				"Authorization":  tc.authorization,
				"X-App":          tc.xApp,
				"Anthropic-Beta": tc.beta,
				"Accept":         "application/json",
				"Anthropic-Dangerous-Direct-Browser-Access": "true",
				"Anthropic-Version":                         "2023-06-01",
			} {
				if got := header.Get(name); got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
			if _, present := header["Anthropic-Beta"]; present != (tc.beta != "") {
				t.Errorf("anthropic-beta present = %v, want %v", present, tc.beta != "")
			}
			userAgent := header.Get("User-Agent")
			if tc.userAgent != "" && userAgent != tc.userAgent {
				t.Errorf("User-Agent = %q, want %q", userAgent, tc.userAgent)
			}
			if tc.userAgent == "" && strings.HasPrefix(userAgent, "claude-cli/") {
				t.Errorf("User-Agent = %q, want no Claude Code identity", userAgent)
			}
			if _, present := captured.body["betas"]; present {
				t.Errorf("request body carries betas: %v", captured.body["betas"])
			}
			if got := systemTexts(captured.body); !reflect.DeepEqual(got, tc.system) {
				t.Errorf("system = %q, want %q", got, tc.system)
			}
			if got := requestToolNames(captured.body); !reflect.DeepEqual(got, tc.tools) {
				t.Errorf("tool names = %q, want %q", got, tc.tools)
			}
		})
	}
}

// TestAnthropicBetaHeaderMergeRules covers upstream getBetaFeatures and how the
// SDK sends params.betas.
func TestAnthropicBetaHeaderMergeRules(t *testing.T) {
	falseValue := false
	for _, tc := range []struct {
		name    string
		cfg     AnthropicConfig
		headers ProviderHeaders
		opts    StreamOptions
		want    *string
	}{
		{
			// anthropic-auth-token.test.ts: preserves explicit Anthropic beta header replacement.
			name:    "request header replaces computed betas",
			cfg:     AnthropicConfig{APIKey: "anthropic-key"},
			headers: ProviderHeaders{"anthropic-beta": new("custom-beta")},
			want:    new("custom-beta"),
		},
		{
			name:    "replacement drops the OAuth betas too",
			cfg:     AnthropicConfig{APIKey: "sk-ant-oat-test"},
			headers: ProviderHeaders{"anthropic-beta": new("custom-beta")},
			want:    new("custom-beta"),
		},
		{
			// anthropic-auth-token.test.ts: preserves explicit Anthropic beta header suppression.
			name:    "null request header suppresses every beta",
			cfg:     AnthropicConfig{APIKey: "sk-ant-oat-test"},
			headers: ProviderHeaders{"anthropic-beta": nil},
		},
		{
			name: "model header is split, trimmed, and deduplicated",
			cfg:  AnthropicConfig{APIKey: "anthropic-key", ExtraHeaders: map[string]string{"Anthropic-Beta": " a, b ,a,,"}},
			want: new("a,b"),
		},
		{
			name:    "request header outranks the model header",
			cfg:     AnthropicConfig{APIKey: "anthropic-key", ExtraHeaders: map[string]string{"anthropic-beta": "model-beta"}},
			headers: ProviderHeaders{"ANTHROPIC-BETA": new("request-beta")},
			want:    new("request-beta"),
		},
		{
			name: "no betas without OAuth, eager-streaming gaps, or thinking",
			cfg:  AnthropicConfig{APIKey: "anthropic-key", Model: "claude-haiku-4-5"},
		},
		{
			name: "thinking on a budget-thinking model adds interleaved thinking",
			cfg:  AnthropicConfig{APIKey: "anthropic-key", Model: "claude-haiku-4-5"},
			opts: StreamOptions{Thinking: ThinkingHigh},
			want: new("interleaved-thinking-2025-05-14"),
		},
		{
			name: "OAuth betas lead the computed list",
			cfg:  AnthropicConfig{APIKey: "sk-ant-oat-test", Model: "claude-haiku-4-5"},
			opts: StreamOptions{Thinking: ThinkingHigh},
			want: new("claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14"),
		},
		{
			name: "thinking off omits interleaved thinking",
			cfg:  AnthropicConfig{APIKey: "anthropic-key", Model: "claude-haiku-4-5"},
			opts: StreamOptions{Thinking: ThinkingOff},
		},
		{
			name: "non-reasoning model omits interleaved thinking",
			cfg:  AnthropicConfig{APIKey: "anthropic-key"},
			opts: StreamOptions{Thinking: ThinkingHigh},
		},
		{
			name: "tools without eager input streaming add fine-grained tool streaming",
			cfg:  AnthropicConfig{APIKey: "sk-ant-oat-test", Compat: &AnthropicMessagesCompat{SupportsEagerToolInputStreaming: &falseValue}},
			want: new("claude-code-20250219,oauth-2025-04-20,fine-grained-tool-streaming-2025-05-14"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearAnthropicAuthEnv(t)
			opts := tc.opts
			opts.Headers = tc.headers
			var sawBetas []string
			opts.OnPayload = func(payload any, _ *Model) (any, error) {
				sawBetas = payload.(anthRequest).Betas
				return nil, nil
			}
			captured, _ := runAnthropicWire(t, tc.cfg, anthropicAuthContext, opts, endTurn)
			values, present := captured.header["Anthropic-Beta"]
			if (tc.want != nil) != present {
				t.Fatalf("anthropic-beta = %q, want %v", values, tc.want)
			}
			if tc.want != nil {
				if got := captured.header.Get("anthropic-beta"); got != *tc.want {
					t.Errorf("anthropic-beta = %q, want %q", got, *tc.want)
				}
				if got := strings.Join(sawBetas, ","); got != *tc.want {
					t.Errorf("OnPayload betas = %q, want %q", sawBetas, *tc.want)
				}
			}
			if _, inBody := captured.body["betas"]; inBody {
				t.Errorf("request body carries betas")
			}
		})
	}
}

// TestAnthropicOnPayloadReplacesBetas mirrors upstream onPayload: returned
// params replace the request, and their betas become the header.
func TestAnthropicOnPayloadReplacesBetas(t *testing.T) {
	clearAnthropicAuthEnv(t)
	opts := StreamOptions{OnPayload: func(payload any, _ *Model) (any, error) {
		data, _ := json.Marshal(payload)
		var params map[string]any
		_ = json.Unmarshal(data, &params)
		params["betas"] = []string{"hook-beta", "second-beta"}
		return params, nil
	}}
	captured, _ := runAnthropicWire(t, AnthropicConfig{APIKey: "sk-ant-oat-test"}, anthropicAuthContext, opts, endTurn)
	if got := captured.header.Get("anthropic-beta"); got != "hook-beta,second-beta" {
		t.Errorf("anthropic-beta = %q, want hook-beta,second-beta", got)
	}
	if _, inBody := captured.body["betas"]; inBody {
		t.Errorf("request body carries betas")
	}
	if got := systemTexts(captured.body); len(got) != 2 || got[0] != claudeCodeSystemPrompt {
		t.Errorf("system = %q, want the Claude Code identity first", got)
	}
}

// TestAnthropicClientUserAgent covers mergeClientHeaders key order: the
// seeded User-Agent key defaults to PiUserAgent() (D65) and takes an
// exact-case request value in place, so the OAuth identity's lowercase
// user-agent key, assigned later, wins over it (Q3: OAuth requests keep
// claude-cli/2.1.280 regardless of a configured header).
func TestAnthropicClientUserAgent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		apiKey  string
		headers ProviderHeaders
		want    string
	}{
		{name: "api key defaults to PiUserAgent", apiKey: "kimi-key", want: PiUserAgent()},
		{name: "oauth identity defaults to the Claude Code identity", apiKey: "sk-ant-oat-test", want: "claude-cli/2.1.280"},
		// anthropic-auth-token.test.ts: lets explicit headers override the default Anthropic Messages User-Agent.
		{name: "api key keeps a request User-Agent", apiKey: "kimi-key", headers: ProviderHeaders{"User-Agent": new("custom-client")}, want: "custom-client"},
		{name: "oauth identity outranks an exact-case User-Agent", apiKey: "sk-ant-oat-test", headers: ProviderHeaders{"User-Agent": new("custom-client")}, want: "claude-cli/2.1.280"},
		{name: "a lowercase user-agent overrides the oauth identity", apiKey: "sk-ant-oat-test", headers: ProviderHeaders{"user-agent": new("custom-client")}, want: "custom-client"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearAnthropicAuthEnv(t)
			captured, _ := runAnthropicWire(t, AnthropicConfig{APIKey: tc.apiKey}, anthropicAuthContext, StreamOptions{Headers: tc.headers}, endTurn)
			if got := captured.header.Get("User-Agent"); got != tc.want {
				t.Errorf("User-Agent = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClaudeCodeToolNames(t *testing.T) {
	tools := []ToolSchema{{Name: "todowrite"}, {Name: "read"}, {Name: "find"}, {Name: "my_custom_tool"}}
	for _, tc := range []struct{ original, outbound string }{
		{"todowrite", "TodoWrite"},
		{"read", "Read"},
		{"BASH", "Bash"},
		{"webfetch", "WebFetch"},
		// find is not a Claude Code tool: the old find -> Glob mapping broke the round trip.
		{"find", "find"},
		{"my_custom_tool", "my_custom_tool"},
	} {
		if got := toClaudeCodeName(tc.original); got != tc.outbound {
			t.Errorf("toClaudeCodeName(%q) = %q, want %q", tc.original, got, tc.outbound)
		}
	}
	for _, tc := range []struct {
		streamed string
		tools    []ToolSchema
		want     string
	}{
		{"TodoWrite", tools, "todowrite"},
		{"Read", tools, "read"},
		{"Glob", tools, "Glob"},
		{"my_custom_tool", tools, "my_custom_tool"},
		{"Read", nil, "Read"},
	} {
		if got := fromClaudeCodeName(tc.streamed, tc.tools); got != tc.want {
			t.Errorf("fromClaudeCodeName(%q) = %q, want %q", tc.streamed, got, tc.want)
		}
	}
}

// TestAnthropicOAuthToolNameRoundTrip ports the four
// anthropic-tool-name-normalization.test.ts cases: an OAuth request sends
// Claude Code casing, the model answers with that name, and every streamed
// tool-call event and the final message carry the original tool name.
func TestAnthropicOAuthToolNameRoundTrip(t *testing.T) {
	for _, tc := range []struct{ tool, outbound string }{
		{"todowrite", "TodoWrite"},
		{"read", "Read"},
		{"find", "find"},
		{"my_custom_tool", "my_custom_tool"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			clearAnthropicAuthEnv(t)
			context := Context{
				SystemPrompt: "You are a helpful assistant.",
				Messages:     []Message{UserMessage{Content: UserText("Use the " + tc.tool + " tool.")}},
				Tools:        []ToolSchema{{Name: tc.tool, Description: "tool", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}},
			}
			captured, events := runAnthropicWire(t, AnthropicConfig{APIKey: "sk-ant-oat-test"}, context, StreamOptions{}, func(body map[string]any) string {
				return anthropicToolUseSSE(requestToolNames(body)[0])
			})
			if got := requestToolNames(captured.body); !reflect.DeepEqual(got, []string{tc.outbound}) {
				t.Fatalf("outbound tool names = %q, want [%q]", got, tc.outbound)
			}
			var toolEvents int
			for _, event := range events {
				var partial *AssistantMessage
				switch event := event.(type) {
				case ToolCallStartEvent:
					partial = event.Partial
				case ToolCallDeltaEvent:
					partial = event.Partial
				case ToolCallEndEvent:
					if event.ToolCall.Name != tc.tool {
						t.Errorf("toolcall_end name = %q, want %q", event.ToolCall.Name, tc.tool)
					}
					partial = event.Partial
				default:
					continue
				}
				toolEvents++
				if call, ok := partial.Content[0].(ToolCall); !ok || call.Name != tc.tool {
					t.Errorf("%s partial tool call = %#v, want name %q", event.EventType(), partial.Content[0], tc.tool)
				}
			}
			if toolEvents != 3 {
				t.Errorf("tool-call events = %d, want start, delta, and end", toolEvents)
			}
			message := anthropicTerminal(t, events)
			if message.StopReason != StopReasonToolUse {
				t.Fatalf("stop reason = %q (%s), want toolUse", message.StopReason, message.ErrorMessage)
			}
			if call, ok := message.Content[0].(ToolCall); !ok || call.Name != tc.tool {
				t.Errorf("final tool call = %#v, want name %q", message.Content[0], tc.tool)
			}
		})
	}
}

// TestAnthropicAPIKeyKeepsStreamedToolNames checks that name mapping is
// OAuth-only: an API-key request sends and returns names unchanged.
func TestAnthropicAPIKeyKeepsStreamedToolNames(t *testing.T) {
	clearAnthropicAuthEnv(t)
	context := Context{
		Messages: []Message{UserMessage{Content: UserText("read it")}},
		Tools:    []ToolSchema{{Name: "read", Description: "Read a file", Parameters: map[string]any{"type": "object"}}},
	}
	captured, events := runAnthropicWire(t, AnthropicConfig{APIKey: "sk-ant-api03-test"}, context, StreamOptions{}, func(map[string]any) string {
		return anthropicToolUseSSE("Read")
	})
	if got := requestToolNames(captured.body); !reflect.DeepEqual(got, []string{"read"}) {
		t.Errorf("outbound tool names = %q, want [read]", got)
	}
	if call, ok := anthropicTerminal(t, events).Content[0].(ToolCall); !ok || call.Name != "Read" {
		t.Errorf("final tool call = %#v, want the streamed name Read", call)
	}
}

func assistantToolUseNames(body map[string]any) []string {
	var names []string
	messages, _ := body["messages"].([]any)
	for _, message := range messages {
		message := message.(map[string]any)
		blocks, _ := message["content"].([]any)
		for _, block := range blocks {
			if block := block.(map[string]any); block["type"] == "tool_use" {
				names = append(names, block["name"].(string)+"#"+block["id"].(string))
			}
		}
	}
	return names
}

// TestAnthropicHistoryToolNames covers the tool_use blocks convertMessages
// replays: OAuth requests send Claude Code casing, API-key requests do not,
// and the D37 signature retry keeps the OAuth mapping.
func TestAnthropicHistoryToolNames(t *testing.T) {
	history := Context{
		SystemPrompt: "System prompt.",
		Messages: []Message{
			UserMessage{Content: UserText("read then list")},
			AssistantMessage{
				Content: []AssistantContentBlock{
					ThinkingContent{Thinking: "plan", ThinkingSignature: "stale-signature"},
					ToolCall{ID: "call_1|fc_1", Name: "read", Arguments: JsonObject{"path": "a"}},
					ToolCall{ID: "call_2", Name: "ls", Arguments: JsonObject{}},
				},
				API: APIAnthropicMessages, Provider: "anthropic", Model: "claude-test", StopReason: StopReasonToolUse,
			},
			ToolResultMessage{ToolCallID: "call_1|fc_1", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "a"}}},
			ToolResultMessage{ToolCallID: "call_2", ToolName: "ls", Content: []ToolResultMessageContent{TextContent{Text: "b"}}},
		},
		Tools: []ToolSchema{{Name: "read", Parameters: map[string]any{"type": "object"}}, {Name: "ls", Parameters: map[string]any{"type": "object"}}},
	}
	for _, tc := range []struct {
		name   string
		apiKey string
		want   []string
	}{
		{"oauth", "sk-ant-oat-test", []string{"Read#call_1_fc_1", "ls#call_2"}},
		{"api key", "sk-ant-api03-test", []string{"read#call_1_fc_1", "ls#call_2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearAnthropicAuthEnv(t)
			var bodies []map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				bodies = append(bodies, body)
				if len(bodies) == 1 {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"Invalid signature in thinking block"}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, anthropicEndTurnSSE)
			}))
			defer server.Close()
			provider := NewAnthropicProvider(AnthropicConfig{APIKey: tc.apiKey, Model: "claude-test", BaseURL: server.URL})
			stream, err := provider.Stream(context.Background(), NormalizeContext(history), StreamOptions{})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			for range stream.Events(context.Background()) {
			}
			if len(bodies) != 2 {
				t.Fatalf("requests = %d, want the rejected request and its D37 retry", len(bodies))
			}
			for index, body := range bodies {
				if got := assistantToolUseNames(body); !reflect.DeepEqual(got, tc.want) {
					t.Errorf("request %d tool_use = %q, want %q", index, got, tc.want)
				}
			}
		})
	}
}

// TestAnthropicEnvAuthStatus ports the env-api-keys.test.ts discovery order:
// ANTHROPIC_AUTH_TOKEN, then ANTHROPIC_OAUTH_TOKEN, then ANTHROPIC_API_KEY.
func TestAnthropicEnvAuthStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"all three", map[string]string{"ANTHROPIC_AUTH_TOKEN": "auth-token", "ANTHROPIC_OAUTH_TOKEN": "oauth-token", "ANTHROPIC_API_KEY": "api-key"}, "ANTHROPIC_AUTH_TOKEN"},
		{"auth token only", map[string]string{"ANTHROPIC_AUTH_TOKEN": "auth-token"}, "ANTHROPIC_AUTH_TOKEN"},
		{"oauth token", map[string]string{"ANTHROPIC_OAUTH_TOKEN": "oauth-token", "ANTHROPIC_API_KEY": "api-key"}, "ANTHROPIC_OAUTH_TOKEN"},
		{"api key", map[string]string{"ANTHROPIC_API_KEY": "api-key"}, "ANTHROPIC_API_KEY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearAnthropicAuthEnv(t)
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			auth, err := NewAuthStorage(t.TempDir() + "/auth.json")
			if err != nil {
				t.Fatalf("NewAuthStorage: %v", err)
			}
			status := auth.GetAuthStatus("anthropic")
			if status.Source != AuthSourceEnvironment || status.Label != tc.want {
				t.Errorf("status = %+v, want environment %s", status, tc.want)
			}
		})
	}
}
