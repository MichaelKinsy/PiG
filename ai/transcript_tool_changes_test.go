package ai

// Ports the native cases of
// .upstream/current/packages/ai/test/transcript-tool-changes.test.ts as
// serialized-request table tests: each case drives a provider Stream and
// inspects the HTTP request it sends. The fold cases live in
// system_message_replay_test.go.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// toolChangeReply is one scripted HTTP answer.
type toolChangeReply struct {
	status int
	body   string
}

// toolChangeRequest is one serialized request: the decoded JSON body and the
// headers it was sent with.
type toolChangeRequest struct {
	body   map[string]any
	header http.Header
}

// captureToolChangeRequests installs a transport that records every request
// and answers with the scripted replies in order, then drains the stream.
func captureToolChangeRequests(t *testing.T, install func(*http.Client), stream func() (*AssistantMessageEventStream, error), replies ...toolChangeReply) []toolChangeRequest {
	t.Helper()
	var requests []toolChangeRequest
	install(&http.Client{Transport: responsesTestRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, err
		}
		requests = append(requests, toolChangeRequest{body: body, header: request.Header.Clone()})
		reply := replies[min(len(requests), len(replies))-1]
		return &http.Response{StatusCode: reply.status, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(reply.body))}, nil
	})})
	result, err := stream()
	if err != nil {
		t.Fatal(err)
	}
	for range result.Events(context.Background()) {
	}
	return requests
}

func jsonItems(value any) []map[string]any {
	list, _ := value.([]any)
	out := make([]map[string]any, len(list))
	for i, item := range list {
		out[i], _ = item.(map[string]any)
	}
	return out
}

func jsonStrings(items []map[string]any, key string) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i], _ = item[key].(string)
	}
	return out
}

func jsonHas(items []map[string]any, key string) []bool {
	out := make([]bool, len(items))
	for i, item := range items {
		_, out[i] = item[key]
	}
	return out
}

// additionContext is the transcript-tool-changes.test.ts additionContext: a
// later system message only adds a tool.
func additionContext() TranscriptContext {
	return NormalizeContext(Context{Messages: []Message{
		SystemMessage{Content: SystemText("base prompt"), ToolsAdded: []ToolSchema{replayTool("base_tool")}},
		UserMessage{Content: UserText("before"), Timestamp: 1},
		SystemMessage{Content: SystemText("updated guidance"), ToolsAdded: []ToolSchema{replayTool("late_tool")}, Timestamp: 2},
	}})
}

// ─── Anthropic ───────────────────────────────────────────────────────────────

const (
	anthropicToolChangeStop = "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	deferredPlaceholderName = "__pi_deferred_placeholder__"
)

var nativeAnthropicCompat = &AnthropicMessagesCompat{SupportsMidConvoSystemMessages: new(true), SupportsMidConvoToolChanges: new(true)}

func captureAnthropicToolChanges(t *testing.T, model string, compat *AnthropicMessagesCompat, transcript TranscriptContext, replies ...toolChangeReply) []toolChangeRequest {
	t.Helper()
	t.Setenv("PI_CACHE_RETENTION", "short")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	provider := NewAnthropicProvider(AnthropicConfig{APIKey: "test-key", Model: model, ProviderID: "anthropic", Compat: compat}).(*anthropicProvider)
	if len(replies) == 0 {
		replies = []toolChangeReply{{200, anthropicToolChangeStop}}
	}
	return captureToolChangeRequests(t, func(client *http.Client) { provider.client = client }, func() (*AssistantMessageEventStream, error) {
		return provider.Stream(context.Background(), transcript, StreamOptions{})
	}, replies...)
}

func anthropicBetas(request toolChangeRequest) []string {
	var betas []string
	for beta := range strings.SplitSeq(request.header.Get("anthropic-beta"), ",") {
		if beta != "" {
			betas = append(betas, beta)
		}
	}
	return betas
}

// anthropicBlockTools names the tool referenced by each content block, or the
// block type for blocks without one.
func anthropicBlockTools(blocks []map[string]any) []string {
	out := make([]string, len(blocks))
	for i, block := range blocks {
		out[i], _ = block["type"].(string)
		if tool, ok := block["tool"].(map[string]any); ok {
			name, _ := tool["name"].(string)
			if definition, ok := tool["definition"].(map[string]any); ok {
				name, _ = definition["name"].(string)
			}
			out[i] += ":" + name + ":" + tool["type"].(string)
		}
	}
	return out
}

func TestTranscriptToolChangesAnthropicRequests(t *testing.T) {
	redefined := replayTool("base_tool", "changed")
	nativePrompt := "base prompt\n\n<rules>\nold rules\n</rules>\n\n<docs>\nread docs\n</docs>"
	for _, tc := range []struct {
		name       string
		compat     *AnthropicMessagesCompat
		transcript TranscriptContext
		native     bool
		system     []string
		tools      []string
		deferred   []bool
		cached     []bool
		roles      []string
		// lastBlocks lists the final message's blocks as type[:tool:tool type].
		lastBlocks []string
		// description is the first tool's expected description.
		description string
		// addedDescription is the description of the final block's inline tool definition.
		addedDescription string
	}{
		{
			name: "native updates and tool changes", compat: nativeAnthropicCompat, transcript: foldContext(), native: true,
			system: []string{nativePrompt},
			// The top-level list holds only the initial tools (with the cache breakpoint)
			// and the deferred placeholder; later tools never appear there.
			tools: []string{"base_tool", deferredPlaceholderName}, deferred: []bool{false, true}, cached: []bool{true, false},
			roles:            []string{"user", "system"},
			lastBlocks:       []string{"text", "tool_removal:base_tool:tool_reference", "tool_addition:late_tool:tool_definition"},
			addedDescription: "late_tool tool",
		},
		{
			// The placeholder is declared before any change so its scaffolding is cached from request one.
			name: "native first request declares the placeholder", compat: nativeAnthropicCompat,
			transcript: NormalizeContext(Context{Messages: foldContext().Messages()[:2]}), native: true,
			system: []string{nativePrompt},
			tools:  []string{"base_tool", deferredPlaceholderName}, deferred: []bool{false, true}, cached: []bool{true, false},
			roles: []string{"user"}, lastBlocks: []string{"text"},
		},
		{
			// redefines an Anthropic tool by value under the same name: the top-level
			// declaration keeps the original definition, so the cached prefix survives,
			// and the new definition replaces the old one without a removal block.
			name: "redefinition is an inline tool_addition", compat: nativeAnthropicCompat,
			transcript: NormalizeContext(Context{Messages: []Message{
				SystemMessage{Content: SystemText("base prompt"), ToolsAdded: []ToolSchema{replayTool("base_tool")}},
				UserMessage{Content: UserText("before"), Timestamp: 1},
				SystemMessage{ToolsRemoved: []ToolReference{{Name: "base_tool"}}, ToolsAdded: []ToolSchema{redefined}, Timestamp: 2},
			}}), native: true,
			system: []string{"base prompt"},
			tools:  []string{"base_tool", deferredPlaceholderName}, deferred: []bool{false, true}, cached: []bool{true, false},
			roles: []string{"user", "system"}, lastBlocks: []string{"tool_addition:base_tool:tool_definition"},
			description: "base_tool tool", addedDescription: "changed",
		},
		{
			// No initial tool: Anthropic rejects a tool list where every tool (here: the placeholder) is deferred.
			name: "no initial tool sends the current tool list", compat: nativeAnthropicCompat,
			transcript: NormalizeContext(Context{Messages: []Message{
				SystemMessage{Content: SystemText("base prompt")},
				UserMessage{Content: UserText("before"), Timestamp: 1},
				SystemMessage{Content: SystemText("updated guidance"), ToolsAdded: []ToolSchema{replayTool("late_tool")}, Timestamp: 2},
			}}),
			system: []string{"base prompt"},
			tools:  []string{"late_tool"}, deferred: []bool{false}, cached: []bool{true},
			roles: []string{"user", "system"}, lastBlocks: []string{"text"},
		},
		{
			name: "folds without native support", transcript: foldContext(),
			system: []string{foldedPrompt},
			tools:  []string{"late_tool"}, deferred: []bool{false}, cached: []bool{true},
			roles: []string{"user"}, lastBlocks: []string{"text"},
		},
		{
			name: "requires both capabilities", compat: &AnthropicMessagesCompat{SupportsMidConvoToolChanges: new(true)}, transcript: foldContext(),
			system: []string{foldedPrompt},
			tools:  []string{"late_tool"}, deferred: []bool{false}, cached: []bool{true},
			roles: []string{"user"}, lastBlocks: []string{"text"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := captureAnthropicToolChanges(t, "custom-claude", tc.compat, tc.transcript)
			if len(requests) != 1 {
				t.Fatalf("requests = %d", len(requests))
			}
			request := requests[0]
			if got := slices.Contains(anthropicBetas(request), inlineToolsBeta); got != tc.native {
				t.Errorf("beta header %q, want tool-changes beta %v", request.header.Get("anthropic-beta"), tc.native)
			}
			if got := jsonStrings(jsonItems(request.body["system"]), "text"); !slices.Equal(got, tc.system) {
				t.Errorf("system = %q, want %q", got, tc.system)
			}
			tools := jsonItems(request.body["tools"])
			if got := jsonStrings(tools, "name"); !slices.Equal(got, tc.tools) {
				t.Fatalf("tools = %v, want %v", got, tc.tools)
			}
			if got := jsonHas(tools, "defer_loading"); !slices.Equal(got, tc.deferred) {
				t.Errorf("defer_loading = %v, want %v", got, tc.deferred)
			}
			if got := jsonHas(tools, "cache_control"); !slices.Equal(got, tc.cached) {
				t.Errorf("cache_control = %v, want %v", got, tc.cached)
			}
			if tc.description != "" && tools[0]["description"] != tc.description {
				t.Errorf("first tool description = %v, want %q", tools[0]["description"], tc.description)
			}
			messages := jsonItems(request.body["messages"])
			if got := jsonStrings(messages, "role"); !slices.Equal(got, tc.roles) {
				t.Fatalf("roles = %v, want %v", got, tc.roles)
			}
			lastBlocks := jsonItems(messages[len(messages)-1]["content"])
			if got := anthropicBlockTools(lastBlocks); !slices.Equal(got, tc.lastBlocks) {
				t.Errorf("last blocks = %v, want %v", got, tc.lastBlocks)
			}
			if tc.addedDescription != "" {
				tool, _ := lastBlocks[len(lastBlocks)-1]["tool"].(map[string]any)
				definition, _ := tool["definition"].(map[string]any)
				if definition["description"] != tc.addedDescription {
					t.Errorf("inline definition = %#v, want description %q", definition, tc.addedDescription)
				}
				// Cache control goes on the block, never on the inline definition; nothing is deferred.
				if _, ok := definition["cache_control"]; ok {
					t.Errorf("inline definition carries cache_control: %#v", definition)
				}
				if _, ok := definition["defer_loading"]; ok {
					t.Errorf("inline definition carries defer_loading: %#v", definition)
				}
			}
		})
	}
}

func TestTranscriptToolChangesAnthropicNativeUpdateText(t *testing.T) {
	request := captureAnthropicToolChanges(t, "custom-claude", nativeAnthropicCompat, foldContext())[0]
	messages := jsonItems(request.body["messages"])
	blocks := jsonItems(messages[len(messages)-1]["content"])
	text, _ := blocks[0]["text"].(string)
	for _, want := range []string{"updated guidance", "<rules>\nnew rules\n</rules>", `Removed system prompt section "docs"`} {
		if !strings.Contains(text, want) {
			t.Errorf("update text %q lacks %q", text, want)
		}
	}
	// The conversation cache breakpoint lands on the final tool change block.
	if !slices.Equal(jsonHas(blocks, "cache_control"), []bool{false, false, true}) {
		t.Errorf("update cache_control = %v", jsonHas(blocks, "cache_control"))
	}
	// Inline tool definitions keep their schema and the eager-streaming flag.
	late, _ := blocks[2]["tool"].(map[string]any)["definition"].(map[string]any)
	if late["eager_input_streaming"] != true || late["input_schema"] == nil {
		t.Errorf("late tool = %#v", late)
	}
	placeholder := jsonItems(request.body["tools"])[1]
	schema, _ := placeholder["input_schema"].(map[string]any)
	if placeholder["description"] != "Reserved placeholder. Never available. Never call this." || schema["type"] != "object" || len(jsonItems(schema["required"])) != 0 || schema["required"] == nil {
		t.Errorf("placeholder = %#v", placeholder)
	}
}

func TestTranscriptToolChangesAnthropicCatalogModelUsesNativeChanges(t *testing.T) {
	generated, ok := LookupModel("anthropic/claude-opus-5")
	if !ok || generated.Compat == nil || generated.Compat.SupportsMidConvoToolChanges == nil || !*generated.Compat.SupportsMidConvoToolChanges {
		t.Fatalf("catalog claude-opus-5 lacks supportsMidConvoToolChanges: %#v", generated.Compat)
	}
	request := captureAnthropicToolChanges(t, "claude-opus-5", nil, foldContext())[0]
	if !slices.Contains(anthropicBetas(request), inlineToolsBeta) {
		t.Errorf("beta header %q", request.header.Get("anthropic-beta"))
	}
	if got := jsonStrings(jsonItems(request.body["tools"]), "name"); !slices.Equal(got, []string{"base_tool", deferredPlaceholderName}) {
		t.Errorf("tools = %v", got)
	}
}

func TestTranscriptToolChangesAnthropicOAuthReferencesClaudeCodeNames(t *testing.T) {
	t.Setenv("PI_CACHE_RETENTION", "short")
	transcript := NormalizeContext(Context{Messages: []Message{
		SystemMessage{Content: SystemText("base prompt"), ToolsAdded: []ToolSchema{replayTool("read")}},
		UserMessage{Content: UserText("before"), Timestamp: 1},
		SystemMessage{ToolsRemoved: []ToolReference{{Name: "read"}}, ToolsAdded: []ToolSchema{replayTool("bash")}, Timestamp: 2},
	}})
	provider := NewAnthropicProvider(AnthropicConfig{APIKey: "sk-ant-oat01-test", Model: "custom-claude", ProviderID: "anthropic", Compat: nativeAnthropicCompat}).(*anthropicProvider)
	request := captureToolChangeRequests(t, func(client *http.Client) { provider.client = client }, func() (*AssistantMessageEventStream, error) {
		return provider.Stream(context.Background(), transcript, StreamOptions{})
	}, toolChangeReply{200, anthropicToolChangeStop})[0]
	if got := jsonStrings(jsonItems(request.body["tools"]), "name"); !slices.Equal(got, []string{"Read", deferredPlaceholderName}) {
		t.Errorf("tools = %v", got)
	}
	messages := jsonItems(request.body["messages"])
	if got := anthropicBlockTools(jsonItems(messages[len(messages)-1]["content"])); !slices.Equal(got, []string{"tool_removal:Read:tool_reference", "tool_addition:Bash:tool_definition"}) {
		t.Errorf("last blocks = %v", got)
	}
	if got := anthropicBetas(request); !slices.Equal(got, []string{claudeCodeBeta, oauthBeta, inlineToolsBeta}) {
		t.Errorf("betas = %v", got)
	}
}

func TestTranscriptToolChangesAnthropicConfiguredBetaHeaderWins(t *testing.T) {
	t.Setenv("PI_CACHE_RETENTION", "short")
	provider := NewAnthropicProvider(AnthropicConfig{APIKey: "test-key", Model: "custom-claude", ProviderID: "anthropic", Compat: nativeAnthropicCompat, ExtraHeaders: map[string]string{"anthropic-beta": "custom-beta"}}).(*anthropicProvider)
	request := captureToolChangeRequests(t, func(client *http.Client) { provider.client = client }, func() (*AssistantMessageEventStream, error) {
		return provider.Stream(context.Background(), foldContext(), StreamOptions{})
	}, toolChangeReply{200, anthropicToolChangeStop})[0]
	if got := request.header.Get("anthropic-beta"); got != "custom-beta" {
		t.Errorf("anthropic-beta = %q", got)
	}
}

// poisonedToolChangeContext interleaves additive tool changes with poisoned
// history: an errored turn holding only an empty text block, a tool call
// replayed from another provider whose result arrives after a later update, and
// an aborted turn with whitespace text and an orphaned tool call.
func poisonedToolChangeContext() TranscriptContext {
	return NormalizeContext(Context{Messages: []Message{
		SystemMessage{Content: SystemText("base prompt"), ToolsAdded: []ToolSchema{replayTool("base_tool")}},
		UserMessage{Content: UserText("before"), Timestamp: 1},
		AssistantMessage{API: APIAnthropicMessages, Provider: "anthropic", Model: "custom", Content: []AssistantContentBlock{TextContent{Text: ""}}, StopReason: StopReasonError, ErrorMessage: "overloaded", Timestamp: 2},
		SystemMessage{Content: SystemText("load late tool"), ToolsAdded: []ToolSchema{replayTool("late_tool")}, Timestamp: 3},
		UserMessage{Content: UserText("retry"), Timestamp: 4},
		AssistantMessage{API: APIOpenAIResponses, Provider: "openai", Model: "gpt-5", Content: []AssistantContentBlock{ToolCall{ID: "call_1|fc_1", Name: "late_tool", Arguments: JsonObject{}}}, StopReason: StopReasonToolUse, Timestamp: 5},
		SystemMessage{Content: SystemText("between call and result"), Timestamp: 6},
		ToolResultMessage{ToolCallID: "call_1|fc_1", ToolName: "late_tool", Content: []ToolResultMessageContent{TextContent{Text: "done"}}, Timestamp: 7},
		AssistantMessage{API: APIAnthropicMessages, Provider: "anthropic", Model: "custom", Content: []AssistantContentBlock{TextContent{Text: "  "}, ToolCall{ID: "call_2", Name: "base_tool", Arguments: JsonObject{}}}, StopReason: StopReasonAborted, Timestamp: 8},
		UserMessage{Content: UserText("go on"), Timestamp: 9},
	}})
}

// anthropicMessageShape renders each message as role(block,...) with tool
// references and tool_use/tool_result ids.
func anthropicMessageShape(messages []map[string]any) []string {
	out := make([]string, len(messages))
	for i, message := range messages {
		blocks, ok := message["content"].([]any)
		if !ok {
			out[i] = message["role"].(string) + "(string)"
			continue
		}
		parts := anthropicBlockTools(jsonItems(blocks))
		for j, block := range jsonItems(blocks) {
			if id, ok := block["id"].(string); ok {
				parts[j] += ":" + id
			}
			if id, ok := block["tool_use_id"].(string); ok {
				parts[j] += ":" + id
			}
		}
		out[i] = message["role"].(string) + "(" + strings.Join(parts, ",") + ")"
	}
	return out
}

func TestTranscriptToolChangesAnthropicPoisonedHistory(t *testing.T) {
	// Updates are held back until the next assistant message, so the update
	// between the tool call and its result lands after the tool_result and the
	// tool_use stays directly followed by it.
	want := []string{
		"user(string)",
		"user(string)",
		"system(text,tool_addition:late_tool:tool_definition)",
		"assistant(tool_use:call_1_fc_1)",
		"user(tool_result:call_1_fc_1)",
		"user(string)",
		"system(text)",
	}
	signatureError := `{"type":"error","error":{"type":"invalid_request_error","message":"Invalid signature in thinking block"}}`
	requests := captureAnthropicToolChanges(t, "custom-claude", nativeAnthropicCompat, poisonedToolChangeContext(),
		toolChangeReply{400, signatureError}, toolChangeReply{200, anthropicToolChangeStop})
	// pig divergence (D37): the retry without thinking signatures keeps the native blocks.
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want the D37 retry", len(requests))
	}
	for i, request := range requests {
		if got := anthropicMessageShape(jsonItems(request.body["messages"])); !slices.Equal(got, want) {
			t.Errorf("request %d messages = %v, want %v", i, got, want)
		}
		if got := jsonStrings(jsonItems(request.body["tools"]), "name"); !slices.Equal(got, []string{"base_tool", deferredPlaceholderName}) {
			t.Errorf("request %d tools = %v", i, got)
		}
		if !slices.Contains(anthropicBetas(request), inlineToolsBeta) {
			t.Errorf("request %d beta header %q", i, request.header.Get("anthropic-beta"))
		}
	}
}

// ─── OpenAI Responses ────────────────────────────────────────────────────────

const responsesToolChangeDone = "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"

func captureResponsesToolChanges(t *testing.T, compat *OpenAIResponsesCompat, transcript TranscriptContext) toolChangeRequest {
	t.Helper()
	provider := NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "test-key", Model: "custom-model", ProviderID: "openai", IsReasoning: true, Compat: compat}).(*openAIResponsesProvider)
	requests := captureToolChangeRequests(t, func(client *http.Client) { provider.client = client }, func() (*AssistantMessageEventStream, error) {
		return provider.Stream(context.Background(), transcript, StreamOptions{})
	}, toolChangeReply{200, responsesToolChangeDone})
	if len(requests) != 1 {
		t.Fatalf("requests = %d", len(requests))
	}
	return requests[0]
}

// responsesInputShape renders each input item as its type, or its role for a
// plain message item, with the call_id or tool names it carries.
func responsesInputShape(input []map[string]any) []string {
	out := make([]string, len(input))
	for i, item := range input {
		kind, _ := item["type"].(string)
		if kind == "" {
			kind, _ = item["role"].(string)
		}
		if callID, ok := item["call_id"].(string); ok && !strings.HasPrefix(callID, "pi_tool_load_") {
			kind += ":" + callID
		}
		if tools, ok := item["tools"]; ok {
			kind += "[" + strings.Join(jsonStrings(jsonItems(tools), "name"), ",") + "]"
		}
		out[i] = kind
	}
	return out
}

// responsesInstructionTexts lists the content of plain developer messages.
func responsesInstructionTexts(input []map[string]any) []string {
	var out []string
	for _, item := range input {
		if item["role"] == "developer" && item["type"] == nil {
			text, _ := item["content"].(string)
			out = append(out, text)
		}
	}
	return out
}

func TestTranscriptToolChangesOpenAIResponsesRequests(t *testing.T) {
	for _, tc := range []struct {
		name         string
		compat       *OpenAIResponsesCompat
		transcript   TranscriptContext
		tools        []string
		shape        []string
		instructions []string
	}{
		{
			name:   "declares every OpenAI tool in additional_tools at its developer message",
			compat: &OpenAIResponsesCompat{SupportsMidConvoSystemMessages: new(true), SupportsAdditionalTools: new(true)}, transcript: additionContext(),
			// A later additional_tools item cannot replace a top-level tool, so no tool is declared there.
			tools:        nil,
			shape:        []string{"additional_tools[base_tool]", "developer", "user", "developer", "additional_tools[late_tool]", "developer"},
			instructions: []string{"base prompt", incrementalToolsNotice, "updated guidance"},
		},
		{
			name:   "maps system-message additions into synthetic tool search",
			compat: &OpenAIResponsesCompat{SupportsMidConvoSystemMessages: new(true), SupportsToolSearch: new(true)}, transcript: additionContext(),
			tools:        []string{"base_tool"},
			shape:        []string{"developer", "user", "tool_search_call", "tool_search_output[late_tool]", "developer"},
			instructions: []string{"base prompt", "updated guidance"},
		},
		{
			name:   "additional tools win over tool search",
			compat: &OpenAIResponsesCompat{SupportsMidConvoSystemMessages: new(true), SupportsAdditionalTools: new(true), SupportsToolSearch: new(true)}, transcript: additionContext(),
			tools:        nil,
			shape:        []string{"additional_tools[base_tool]", "developer", "user", "developer", "additional_tools[late_tool]", "developer"},
			instructions: []string{"base prompt", incrementalToolsNotice, "updated guidance"},
		},
		{
			name:   "folds updates into the leading developer message without native support",
			compat: &OpenAIResponsesCompat{SupportsAdditionalTools: new(true)}, transcript: foldContext(),
			tools:        []string{"late_tool"},
			shape:        []string{"developer", "user"},
			instructions: []string{foldedPrompt},
		},
		{
			name:   "announces OpenAI removals as text",
			compat: &OpenAIResponsesCompat{SupportsMidConvoSystemMessages: new(true), SupportsAdditionalTools: new(true)}, transcript: foldContext(),
			tools:        nil,
			shape:        []string{"additional_tools[base_tool]", "developer", "user", "developer", "additional_tools[late_tool]", "developer"},
			instructions: []string{"base prompt\n\n<rules>\nold rules\n</rules>\n\n<docs>\nread docs\n</docs>", incrementalToolsNotice, RenderSystemMessageUpdate(foldContext().Messages()[2].(SystemMessage)) + "\n\nThe following tools are no longer available. Do not call them:\n- base_tool"},
		},
		{
			name:   "sends every current tool when mid-conversation messages lack tool support",
			compat: &OpenAIResponsesCompat{SupportsMidConvoSystemMessages: new(true)}, transcript: additionContext(),
			tools:        []string{"base_tool", "late_tool"},
			shape:        []string{"developer", "user", "developer"},
			instructions: []string{"base prompt", "updated guidance"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := captureResponsesToolChanges(t, tc.compat, tc.transcript)
			if got := jsonStrings(jsonItems(request.body["tools"]), "name"); !slices.Equal(got, tc.tools) {
				t.Errorf("tools = %v, want %v", got, tc.tools)
			}
			input := jsonItems(request.body["input"])
			if got := responsesInputShape(input); !slices.Equal(got, tc.shape) {
				t.Errorf("input = %v, want %v", got, tc.shape)
			}
			if got := responsesInstructionTexts(input); !slices.Equal(got, tc.instructions) {
				t.Errorf("developer texts = %q, want %q", got, tc.instructions)
			}
		})
	}
}

func TestTranscriptToolChangesOpenAIResponsesToolSearchItems(t *testing.T) {
	request := captureResponsesToolChanges(t, &OpenAIResponsesCompat{SupportsMidConvoSystemMessages: new(true), SupportsToolSearch: new(true)}, additionContext())
	input := jsonItems(request.body["input"])
	call, output := input[2], input[3]
	// The seed is the message index after the leading system message.
	callID := "pi_tool_load_" + ShortHash("system:1:late_tool")
	arguments, _ := call["arguments"].(map[string]any)
	if call["type"] != "tool_search_call" || call["call_id"] != callID || call["execution"] != "client" || call["status"] != "completed" ||
		arguments["query"] != "late_tool" || arguments["limit"] != float64(1) {
		t.Errorf("tool_search_call = %#v", call)
	}
	if output["type"] != "tool_search_output" || output["call_id"] != callID || output["execution"] != "client" || output["status"] != "completed" {
		t.Errorf("tool_search_output = %#v", output)
	}
	loaded := jsonItems(output["tools"])
	if len(loaded) != 1 || loaded[0]["type"] != "function" || loaded[0]["defer_loading"] != true || loaded[0]["parameters"] == nil {
		t.Errorf("loaded tools = %#v", loaded)
	}
	if _, deferred := jsonItems(request.body["tools"])[0]["defer_loading"]; deferred {
		t.Error("request-level tool is deferred")
	}
}

func TestTranscriptToolChangesOpenAIResponsesAdditionalToolsItem(t *testing.T) {
	compat := &OpenAIResponsesCompat{SupportsMidConvoSystemMessages: new(true), SupportsAdditionalTools: new(true), SupportsStrictMode: new(true)}
	request := captureResponsesToolChanges(t, compat, additionContext())
	input := jsonItems(request.body["input"])
	// The initial and the later tools use the request tool conversion, including strict mode.
	for _, index := range []int{0, 4} {
		item := input[index]
		tools := jsonItems(item["tools"])
		if item["type"] != "additional_tools" || item["role"] != "developer" || len(tools) != 1 || tools[0]["strict"] != false || tools[0]["defer_loading"] != nil {
			t.Errorf("additional_tools at %d = %#v", index, item)
		}
	}
}

func TestTranscriptToolChangesOpenAIResponsesPoisonedHistory(t *testing.T) {
	compat := &OpenAIResponsesCompat{SupportsMidConvoSystemMessages: new(true), SupportsAdditionalTools: new(true)}
	request := captureResponsesToolChanges(t, compat, poisonedToolChangeContext())
	// Pi transformMessages drops failed turns and holds system updates behind tool results; every tool is declared in additional_tools items, the later one after the incremental notice.
	want := []string{
		"additional_tools[base_tool]", "developer", "user",
		"developer", "additional_tools[late_tool]", "developer", "user",
		"function_call:call_1", "function_call_output:call_1", "developer", "user",
	}
	input := jsonItems(request.body["input"])
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	assertShapeJSON(t, encoded, `[{"role":"developer","tools":[{"description":"base_tool tool","name":"base_tool","parameters":{"properties":{},"type":"object"},"type":"function"}],"type":"additional_tools"},{"content":"base prompt","role":"developer"},{"content":[{"text":"before","type":"input_text"}],"role":"user"},{"content":"`+incrementalToolsNotice+`","role":"developer"},{"role":"developer","tools":[{"description":"late_tool tool","name":"late_tool","parameters":{"properties":{},"type":"object"},"type":"function"}],"type":"additional_tools"},{"content":"load late tool","role":"developer"},{"content":[{"text":"retry","type":"input_text"}],"role":"user"},{"arguments":"{}","call_id":"call_1","name":"late_tool","type":"function_call"},{"call_id":"call_1","output":"done","type":"function_call_output"},{"content":"between call and result","role":"developer"},{"content":[{"text":"go on","type":"input_text"}],"role":"user"}]`)
	if got := responsesInputShape(input); !slices.Equal(got, want) {
		t.Errorf("input = %v, want %v", got, want)
	}
	if tools, ok := request.body["tools"]; ok {
		t.Errorf("tools = %v, want none", tools)
	}
}

func TestTranscriptToolChangesOpenAIResponsesNonReasoningUpdatesUseSystemRole(t *testing.T) {
	provider := NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "test-key", Model: "custom-model", ProviderID: "openai", Compat: &OpenAIResponsesCompat{SupportsMidConvoSystemMessages: new(true)}}).(*openAIResponsesProvider)
	request := captureToolChangeRequests(t, func(client *http.Client) { provider.client = client }, func() (*AssistantMessageEventStream, error) {
		return provider.Stream(context.Background(), additionContext(), StreamOptions{})
	}, toolChangeReply{200, responsesToolChangeDone})[0]
	if got := responsesInputShape(jsonItems(request.body["input"])); !slices.Equal(got, []string{"system", "user", "system"}) {
		t.Errorf("input = %v", got)
	}
}

// ─── OpenAI Completions (Kimi) ───────────────────────────────────────────────

const completionsToolChangeDone = "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"

var kimiToolAdditionsCompat = &OpenAICompat{SupportsMidConvoSystemMessages: new(true), SupportsMidConvoToolAdditions: new(true)}

func captureCompletionsToolChanges(t *testing.T, providerID string, compat *OpenAICompat, transcript TranscriptContext) toolChangeRequest {
	t.Helper()
	provider := &openAIProvider{cfg: OpenAIConfig{BaseURL: "https://example.test/v1", APIKey: "test-key", Model: "custom-model", ProviderID: providerID, Compat: compat}}
	requests := captureToolChangeRequests(t, func(client *http.Client) { provider.client = client }, func() (*AssistantMessageEventStream, error) {
		return provider.Stream(context.Background(), transcript, StreamOptions{IsReasoning: true})
	}, toolChangeReply{200, completionsToolChangeDone})
	if len(requests) != 1 {
		t.Fatalf("requests = %d", len(requests))
	}
	return requests[0]
}

func completionsToolNames(value any) []string {
	var names []string
	for _, tool := range jsonItems(value) {
		function, _ := tool["function"].(map[string]any)
		name, _ := function["name"].(string)
		names = append(names, name)
	}
	return names
}

// completionsMessageShape renders each message as its role, the tools a
// tool-bearing message loads, and the tool call ids it carries.
func completionsMessageShape(messages []map[string]any) []string {
	out := make([]string, len(messages))
	for i, message := range messages {
		shape, _ := message["role"].(string)
		if tools, ok := message["tools"]; ok {
			shape += "[" + strings.Join(completionsToolNames(tools), ",") + "]"
		}
		for _, call := range jsonItems(message["tool_calls"]) {
			shape += ":" + call["id"].(string)
		}
		if id, ok := message["tool_call_id"].(string); ok {
			shape += ":" + id
		}
		out[i] = shape
	}
	return out
}

// completionsSystemContents lists instruction-message content; a tool-bearing message has none.
func completionsSystemContents(messages []map[string]any) []string {
	var out []string
	for _, message := range messages {
		if message["role"] != "system" && message["role"] != "developer" {
			continue
		}
		content, ok := message["content"].(string)
		if _, present := message["content"]; !present {
			content = "<absent>"
		} else if !ok {
			content = "<non-string>"
		}
		out = append(out, content)
	}
	return out
}

func TestTranscriptToolChangesOpenAICompletionsRequests(t *testing.T) {
	for _, tc := range []struct {
		name       string
		providerID string
		compat     *OpenAICompat
		transcript TranscriptContext
		tools      []string
		shape      []string
		system     []string
	}{
		{
			name: "anchors Kimi additions in tool-bearing system messages", providerID: "moonshotai", compat: kimiToolAdditionsCompat, transcript: additionContext(),
			tools:  []string{"base_tool"},
			shape:  []string{"system", "user", "system[late_tool]", "system"},
			system: []string{"base prompt", "<absent>", "updated guidance"},
		},
		{
			name: "keeps Kimi K2 system text inline without dynamic tool messages", providerID: "moonshotai", compat: &OpenAICompat{SupportsMidConvoSystemMessages: new(true)}, transcript: additionContext(),
			tools:  []string{"base_tool", "late_tool"},
			shape:  []string{"system", "user", "system"},
			system: []string{"base prompt", "updated guidance"},
		},
		{
			name: "falls back to the complete current tool state when removals are unsupported", providerID: "moonshotai", compat: kimiToolAdditionsCompat, transcript: foldContext(),
			tools:  []string{"late_tool"},
			shape:  []string{"system", "user", "system"},
			system: []string{"base prompt\n\n<rules>\nold rules\n</rules>\n\n<docs>\nread docs\n</docs>", RenderSystemMessageUpdate(foldContext().Messages()[2].(SystemMessage))},
		},
		{
			name: "requires mid-conversation system messages for tool additions", providerID: "moonshotai", compat: &OpenAICompat{SupportsMidConvoToolAdditions: new(true)}, transcript: additionContext(),
			tools:  []string{"base_tool", "late_tool"},
			shape:  []string{"system", "user"},
			system: []string{"base prompt\n\nupdated guidance"},
		},
		{
			name: "folds OpenAI-compatible updates into the system prompt without native support", providerID: "custom-provider", transcript: foldContext(),
			tools:  []string{"late_tool"},
			shape:  []string{"developer", "user"},
			system: []string{foldedPrompt},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := captureCompletionsToolChanges(t, tc.providerID, tc.compat, tc.transcript)
			if got := completionsToolNames(request.body["tools"]); !slices.Equal(got, tc.tools) {
				t.Errorf("tools = %v, want %v", got, tc.tools)
			}
			messages := jsonItems(request.body["messages"])
			if got := completionsMessageShape(messages); !slices.Equal(got, tc.shape) {
				t.Errorf("messages = %v, want %v", got, tc.shape)
			}
			if got := completionsSystemContents(messages); !slices.Equal(got, tc.system) {
				t.Errorf("system contents = %q, want %q", got, tc.system)
			}
		})
	}
}

func TestTranscriptToolChangesKimiCatalogCompatAnchorsAdditions(t *testing.T) {
	// Added tools use the request tool conversion, so the catalog strict-mode
	// flag decides whether they carry strict.
	for _, tc := range []struct {
		provider  string
		hasStrict bool
	}{{"moonshotai", false}, {"opencode", true}} {
		t.Run(tc.provider, func(t *testing.T) {
			generated, ok := LookupModel(tc.provider + "/kimi-k3")
			if !ok || generated.Compat == nil {
				t.Fatalf("catalog %s/kimi-k3 missing", tc.provider)
			}
			request := captureCompletionsToolChanges(t, tc.provider, generated.Compat, additionContext())
			messages := jsonItems(request.body["messages"])
			if got := completionsMessageShape(messages); !slices.Equal(got, []string{"system", "user", "system[late_tool]", "system"}) {
				t.Fatalf("messages = %v", got)
			}
			added := jsonItems(messages[2]["tools"])[0]
			function, _ := added["function"].(map[string]any)
			strict, hasStrict := function["strict"]
			if added["type"] != "function" || function["parameters"] == nil || hasStrict != tc.hasStrict || (hasStrict && strict != false) {
				t.Errorf("added tool = %#v", added)
			}
		})
	}
}

func TestTranscriptToolChangesOpenAICompletionsPoisonedHistory(t *testing.T) {
	request := captureCompletionsToolChanges(t, "moonshotai", kimiToolAdditionsCompat, poisonedToolChangeContext())
	want := []string{
		"system", "user",
		"system[late_tool]", "system", "user",
		"assistant:call_1_fc_1", "tool:call_1_fc_1", "system", "user",
	}
	if got := completionsMessageShape(jsonItems(request.body["messages"])); !slices.Equal(got, want) {
		t.Errorf("messages = %v, want %v", got, want)
	}
	if got := completionsToolNames(request.body["tools"]); !slices.Equal(got, []string{"base_tool"}) {
		t.Errorf("tools = %v", got)
	}
}

// .upstream/v1.2.0+b06c1f2c/packages/ai/test/transcript-tool-changes.test.ts (#10720): Kimi Coding uses Chat Completions,
// so K3-style models keep added tools out of the top-level list, and Kimi Code Highspeed, which rejects tool-bearing
// system messages, keeps them there.
func TestTranscriptToolChangesKimiCodingAdditions(t *testing.T) {
	for _, tc := range []struct {
		model    string
		tools    []string
		anchored []string
	}{
		{"k3", []string{"base_tool"}, []string{"late_tool"}},
		{"k3-256k", []string{"base_tool"}, []string{"late_tool"}},
		{"kimi-for-coding", []string{"base_tool"}, []string{"late_tool"}},
		{"kimi-for-coding-highspeed", []string{"base_tool", "late_tool"}, nil},
	} {
		t.Run(tc.model, func(t *testing.T) {
			generated, ok := LookupModel("kimi-coding/" + tc.model)
			if !ok {
				t.Fatalf("catalog kimi-coding/%s missing", tc.model)
			}
			if generated.API != "openai-completions" {
				t.Fatalf("api = %s, want openai-completions", generated.API)
			}
			request := captureCompletionsToolChanges(t, "kimi-coding", generated.Compat, additionContext())
			if got := completionsToolNames(request.body["tools"]); !slices.Equal(got, tc.tools) {
				t.Errorf("tools = %v, want %v", got, tc.tools)
			}
			var anchored []string
			for _, message := range jsonItems(request.body["messages"]) {
				if tools, ok := message["tools"]; ok {
					anchored = completionsToolNames(tools)
					break
				}
			}
			if !slices.Equal(anchored, tc.anchored) {
				t.Errorf("tool-bearing message tools = %v, want %v", anchored, tc.anchored)
			}
		})
	}
}

// ─── Tool history across a removal and later loads (Pi #11142, #10940) ───────

// incrementalToolsNotice precedes every additional_tools item after the first (openai-responses-shared.ts renderIncrementalToolsNotice).
const incrementalToolsNotice = "This is an incremental tools update. Previously declared tools remain available unless explicitly marked unavailable. A tool declared again here is available again, and its latest definition replaces the earlier one."

// toolHistoryTranscripts is transcript-tool-changes.test.ts captureToolHistory's request sequence: the initial tools, the removal of
// drop_tool, then two loads, each transcript the previous one plus one step.
func toolHistoryTranscripts() []TranscriptContext {
	steps := [][]Message{
		{UserMessage{Content: UserText("first"), Timestamp: 1}},
		{
			SystemMessage{Content: SystemText(""), ToolsRemoved: []ToolReference{{Name: "drop_tool"}}, Timestamp: 2},
			UserMessage{Content: UserText("after removal"), Timestamp: 3},
		},
		{
			SystemMessage{Content: SystemText(""), ToolsAdded: []ToolSchema{replayTool("late_tool")}, Timestamp: 4},
			UserMessage{Content: UserText("after first load"), Timestamp: 5},
		},
		{
			SystemMessage{Content: SystemText(""), ToolsAdded: []ToolSchema{replayTool("later_tool")}, Timestamp: 6},
			UserMessage{Content: UserText("after second load"), Timestamp: 7},
		},
	}
	messages := []Message{SystemMessage{Content: SystemText("base prompt"), ToolsAdded: []ToolSchema{replayTool("base_tool"), replayTool("drop_tool")}}}
	var transcripts []TranscriptContext
	for _, step := range steps {
		messages = append(messages, step...)
		transcripts = append(transcripts, NormalizeContext(Context{Messages: slices.Clone(messages)}))
	}
	return transcripts
}

// expectGrowingPrefix requires every request's list to start with the previous request's whole list.
func expectGrowingPrefix(t *testing.T, lists [][]map[string]any) {
	t.Helper()
	for index := 1; index < len(lists); index++ {
		previous := lists[index-1]
		if len(lists[index]) < len(previous) || !reflect.DeepEqual(lists[index][:len(previous)], previous) {
			t.Errorf("request %d does not start with request %d:\n got %v\nwant prefix %v", index, index-1, lists[index], previous)
		}
	}
}

func toolHistoryResponses(t *testing.T, compat *OpenAIResponsesCompat) []toolChangeRequest {
	t.Helper()
	var requests []toolChangeRequest
	for _, transcript := range toolHistoryTranscripts() {
		requests = append(requests, captureResponsesToolChanges(t, compat, transcript))
	}
	return requests
}

// #10940: deferred loads after a removal must not rewrite the cached prefix.
func TestTranscriptToolChangesOpenAIResponsesKeepsPrefixAcrossRemovalAndLoads(t *testing.T) {
	requests := toolHistoryResponses(t, &OpenAIResponsesCompat{SupportsMidConvoSystemMessages: new(true), SupportsAdditionalTools: new(true)})
	var inputs [][]map[string]any
	for _, request := range requests {
		if tools, ok := request.body["tools"]; ok {
			t.Errorf("tools = %v, want none", tools)
		}
		inputs = append(inputs, jsonItems(request.body["input"]))
	}
	expectGrowingPrefix(t, inputs)
	last := inputs[len(inputs)-1]
	var declared []string
	var noticed, removed bool
	var notices []string
	for _, item := range last {
		if item["type"] == "additional_tools" {
			declared = append(declared, strings.Join(jsonStrings(jsonItems(item["tools"]), "name"), ","))
		}
		content, _ := item["content"].(string)
		removed = removed || strings.Contains(content, "no longer available. Do not call them:\n- drop_tool")
		if strings.Contains(content, "incremental tools update") {
			noticed = true
			notices = append(notices, content)
		}
	}
	if want := []string{"base_tool,drop_tool", "late_tool", "later_tool"}; !slices.Equal(declared, want) {
		t.Errorf("additional_tools = %v, want %v", declared, want)
	}
	if !removed || !noticed {
		t.Errorf("removal announced = %t, incremental notice = %t", removed, noticed)
	}
	// Each later load restates the tools that are still unavailable.
	if want := []string{incrementalToolsNotice + "\nStill unavailable: drop_tool.", incrementalToolsNotice + "\nStill unavailable: drop_tool."}; !slices.Equal(notices, want) {
		t.Errorf("notices = %q, want %q", notices, want)
	}
}

func TestTranscriptToolChangesOpenAIResponsesRedefinitionIsNotARemoval(t *testing.T) {
	base := replayTool("base_tool")
	request := captureResponsesToolChanges(t, &OpenAIResponsesCompat{SupportsMidConvoSystemMessages: new(true), SupportsAdditionalTools: new(true)}, NormalizeContext(Context{Messages: []Message{
		SystemMessage{Content: SystemText("base prompt"), ToolsAdded: []ToolSchema{base}},
		UserMessage{Content: UserText("before"), Timestamp: 1},
		SystemMessage{Content: SystemText(""), ToolsRemoved: []ToolReference{{Name: "base_tool"}}, ToolsAdded: []ToolSchema{replayTool("base_tool", "changed")}, Timestamp: 2},
	}}))
	input := jsonItems(request.body["input"])
	if got, want := responsesInputShape(input), []string{"additional_tools[base_tool]", "developer", "user", "developer", "additional_tools[base_tool]"}; !slices.Equal(got, want) {
		t.Fatalf("input = %v, want %v", got, want)
	}
	if got := jsonStrings(jsonItems(input[0]["tools"]), "description"); !slices.Equal(got, []string{"base_tool tool"}) {
		t.Errorf("first definition = %v", got)
	}
	if got := jsonStrings(jsonItems(input[4]["tools"]), "description"); !slices.Equal(got, []string{"changed"}) {
		t.Errorf("redefinition = %v", got)
	}
	if text, _ := input[3]["content"].(string); !strings.Contains(text, "latest definition replaces the earlier one") || strings.Contains(text, "Still unavailable") {
		t.Errorf("notice = %q", text)
	}
	if tools, ok := request.body["tools"]; ok {
		t.Errorf("tools = %v, want none", tools)
	}
}

func TestTranscriptToolChangesOpenAIResponsesStopsListingAToolAsUnavailableOnceDeclaredAgain(t *testing.T) {
	request := captureResponsesToolChanges(t, &OpenAIResponsesCompat{SupportsMidConvoSystemMessages: new(true), SupportsAdditionalTools: new(true)}, NormalizeContext(Context{Messages: []Message{
		SystemMessage{Content: SystemText("base prompt"), ToolsAdded: []ToolSchema{replayTool("base_tool"), replayTool("drop_tool")}},
		SystemMessage{Content: SystemText(""), ToolsRemoved: []ToolReference{{Name: "drop_tool"}}, Timestamp: 1},
		SystemMessage{Content: SystemText(""), ToolsAdded: []ToolSchema{replayTool("drop_tool")}, Timestamp: 2},
		UserMessage{Content: UserText("go"), Timestamp: 3},
	}}))
	input := jsonItems(request.body["input"])
	var notice string
	for _, item := range input {
		if text, _ := item["content"].(string); strings.Contains(text, "incremental tools update") {
			notice = text
			break
		}
	}
	if notice == "" || strings.Contains(notice, "Still unavailable") {
		t.Errorf("notice = %q", notice)
	}
	if item := input[len(input)-2]; item["type"] != "additional_tools" || !slices.Equal(jsonStrings(jsonItems(item["tools"]), "name"), []string{"drop_tool"}) {
		t.Errorf("input before the user message = %v", item)
	}
}

func TestTranscriptToolChangesOpenAIResponsesDoesNotListAToolAsUnavailableBeforeItsRemovalIsAnnounced(t *testing.T) {
	request := captureResponsesToolChanges(t, &OpenAIResponsesCompat{SupportsMidConvoSystemMessages: new(true), SupportsAdditionalTools: new(true)}, NormalizeContext(Context{Messages: []Message{
		SystemMessage{Content: SystemText("base prompt"), ToolsAdded: []ToolSchema{replayTool("base_tool"), replayTool("drop_tool")}},
		SystemMessage{Content: SystemText(""), ToolsAdded: []ToolSchema{replayTool("late_tool")}, ToolsRemoved: []ToolReference{{Name: "drop_tool"}}, Timestamp: 1},
		UserMessage{Content: UserText("go"), Timestamp: 2},
	}}))
	input := jsonItems(request.body["input"])
	// Like Codex: notice, then the tools, then the removal notice.
	tail := input[len(input)-4:]
	if text, _ := tail[0]["content"].(string); text != incrementalToolsNotice {
		t.Errorf("notice = %q", text)
	}
	if got, want := responsesInputShape(tail[1:]), []string{"additional_tools[late_tool]", "developer", "user"}; !slices.Equal(got, want) {
		t.Errorf("tail = %v, want %v", got, want)
	}
	if text, _ := tail[2]["content"].(string); !strings.Contains(text, "no longer available. Do not call them:\n- drop_tool") {
		t.Errorf("removal notice = %q", text)
	}
}

// #10940: synthetic tool search cannot remove tools, so a removal rewrites the prefix once.
func TestTranscriptToolChangesOpenAIResponsesToolSearchPrefixAcrossLoadsAfterARemoval(t *testing.T) {
	requests := toolHistoryResponses(t, &OpenAIResponsesCompat{SupportsMidConvoSystemMessages: new(true), SupportsToolSearch: new(true)})
	if got := jsonStrings(jsonItems(requests[0].body["tools"]), "name"); !slices.Equal(got, []string{"base_tool", "drop_tool"}) {
		t.Errorf("tools before the removal = %v", got)
	}
	var inputs [][]map[string]any
	for _, request := range requests[1:] {
		if got := jsonStrings(jsonItems(request.body["tools"]), "name"); !slices.Equal(got, []string{"base_tool"}) {
			t.Errorf("tools after the removal = %v", got)
		}
		inputs = append(inputs, jsonItems(request.body["input"]))
	}
	expectGrowingPrefix(t, inputs)
	var loaded []string
	for _, item := range inputs[len(inputs)-1] {
		if item["type"] == "tool_search_output" {
			loaded = append(loaded, jsonStrings(jsonItems(item["tools"]), "name")[0])
		}
	}
	if !slices.Equal(loaded, []string{"late_tool", "later_tool"}) {
		t.Errorf("tool_search_output tools = %v", loaded)
	}
}

// #10940: removals and redeclarations move the top-level boundary once; plain additions stay anchored.
func TestResolveTranscriptToolsBoundaryPerTransport(t *testing.T) {
	base, drop := replayTool("base_tool"), replayTool("drop_tool")
	lateX, lateY := replayTool("late_tool", "x"), replayTool("late_tool", "y")
	messages := []SystemMessage{
		{Content: SystemText("base prompt"), ToolsAdded: []ToolSchema{base, drop}},
		{Content: SystemText(""), ToolsRemoved: []ToolReference{{Name: "drop_tool"}}, Timestamp: 1},
		{Content: SystemText(""), ToolsAdded: []ToolSchema{drop}, Timestamp: 2},
		{Content: SystemText(""), ToolsAdded: []ToolSchema{lateX}, Timestamp: 3},
		{Content: SystemText(""), ToolsAdded: []ToolSchema{lateY}, Timestamp: 4},
	}
	check := func(name string, got TranscriptTools, tools []ToolSchema, folded int) {
		t.Helper()
		if got.FoldedSystemMessages != folded || len(got.RequestTools) != len(tools) {
			t.Errorf("%s = %+v, want %d tools folded %d", name, got, len(tools), folded)
			return
		}
		for i := range tools {
			if got.RequestTools[i].Name != tools[i].Name || got.RequestTools[i].Description != tools[i].Description {
				t.Errorf("%s tool %d = %+v, want %+v", name, i, got.RequestTools[i], tools[i])
			}
		}
	}
	check("removal", ResolveTranscriptTools(messages[:2], TranscriptToolSupportAdditions), []ToolSchema{base}, 2)
	// Kimi K3 ignores a removed tool that a later tool message declares again, so the re-added drop_tool moves the boundary; the new
	// late_tool after it stays anchored.
	check("re-added", ResolveTranscriptTools(messages[:4], TranscriptToolSupportAdditions), []ToolSchema{base, drop}, 3)
	// The redefinition of late_tool moves the boundary to its message.
	check("redefined", ResolveTranscriptTools(messages, TranscriptToolSupportAdditions), []ToolSchema{base, drop, lateY}, 5)
	check("changes", ResolveTranscriptTools(messages, TranscriptToolSupportChanges), nil, 0)
	check("none", ResolveTranscriptTools(messages, TranscriptToolSupportNone), []ToolSchema{base, drop, lateY}, 5)
}

// #10940: Kimi tool messages cannot remove tools, so a removal rewrites the prefix once.
func TestTranscriptToolChangesKimiPrefixAcrossLoadsAfterARemoval(t *testing.T) {
	var requests []toolChangeRequest
	for _, transcript := range toolHistoryTranscripts() {
		requests = append(requests, captureCompletionsToolChanges(t, "moonshotai", kimiToolAdditionsCompat, transcript))
	}
	if got := completionsToolNames(requests[0].body["tools"]); !slices.Equal(got, []string{"base_tool", "drop_tool"}) {
		t.Errorf("tools before the removal = %v", got)
	}
	var messages [][]map[string]any
	for _, request := range requests[1:] {
		if got := completionsToolNames(request.body["tools"]); !slices.Equal(got, []string{"base_tool"}) {
			t.Errorf("tools after the removal = %v", got)
		}
		messages = append(messages, jsonItems(request.body["messages"]))
	}
	expectGrowingPrefix(t, messages)
	var loaded []string
	for _, message := range messages[len(messages)-1] {
		if tools, ok := message["tools"]; ok {
			loaded = append(loaded, completionsToolNames(tools)[0])
		}
	}
	if !slices.Equal(loaded, []string{"late_tool", "later_tool"}) {
		t.Errorf("tool-bearing messages = %v", loaded)
	}
}

// ─── Request tools resolve over the transcript as given (openai-responses.ts buildParams) ─

// captureToolChangesForResponsesProviders sends one request through each Responses provider that shares the converter and returns
// the request bodies by provider name.
func captureToolChangesForResponsesProviders(t *testing.T, compat *OpenAIResponsesCompat, transcript TranscriptContext) map[string]toolChangeRequest {
	t.Helper()
	capture := func(name string, install func(*http.Client), stream func() (*AssistantMessageEventStream, error)) toolChangeRequest {
		var started *AssistantMessageEventStream
		requests := captureToolChangeRequests(t, install, func() (*AssistantMessageEventStream, error) {
			result, err := stream()
			started = result
			return result, err
		}, toolChangeReply{200, responsesToolChangeDone})
		if len(requests) != 1 {
			message := ""
			if started != nil {
				message = started.Result().ErrorMessage
			}
			t.Fatalf("%s requests = %d (stream error %q)", name, len(requests), message)
		}
		return requests[0]
	}
	out := map[string]toolChangeRequest{}
	openai := NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "test-key", Model: "custom-model", ProviderID: "openai", IsReasoning: true, Compat: compat}).(*openAIResponsesProvider)
	out["openai-responses"] = capture("openai-responses", func(client *http.Client) { openai.client = client }, func() (*AssistantMessageEventStream, error) {
		return openai.Stream(context.Background(), transcript, StreamOptions{})
	})
	azure := NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{APIKey: "test-key", Model: "custom-deployment", IsReasoning: true, Compat: compat}).(*openAIResponsesProvider)
	out["azure-openai-responses"] = capture("azure-openai-responses", func(client *http.Client) { azure.client = client }, func() (*AssistantMessageEventStream, error) {
		return azure.Stream(context.Background(), transcript, StreamOptions{AzureBaseURL: "https://example.test/openai/v1", AzureResourceName: "acme"})
	})
	codex := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: codexTestToken(t, "acc_test"), Model: "custom-model", ProviderID: "openai-codex", Compat: compat, ModelMetadata: &Model{ID: "custom-model", DisplayName: "custom-model", ProviderMeta: ProviderMetadata{API: APIOpenAICodexResponses, ProviderID: "openai-codex", Reasoning: true}, Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 400000, MaxOutputTokens: 128000, MaxThinking: ThinkingLevelHigh}}}).(*openAIResponsesProvider)
	// The Codex transport sends its body in its own wire form, which the Codex tests decode with codexUpstreamBody.
	var codexBody map[string]json.RawMessage
	codex.client = &http.Client{Transport: codexRoundTripper(func(r *http.Request) (*http.Response, error) {
		codexBody = codexUpstreamBody(t, r)
		return codexUpstreamHTTP(codexUpstreamSSE("completed", nil)), nil
	})}
	stream, err := codex.Stream(context.Background(), transcript, StreamOptions{APIKey: codexTestToken(t, "acc_test"), Transport: TransportSSE})
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Result()
	decoded := map[string]any{}
	for key, raw := range codexBody {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		decoded[key] = value
	}
	out["openai-codex-responses"] = toolChangeRequest{body: decoded}
	return out
}

// Pi's buildParams and convertResponsesMessages resolve the tools over the transcript after resolveTranscript and before
// transformMessages, so these two histories send what Pi sends on every Responses provider.
func TestTranscriptToolChangesResponsesRequestToolsResolveBeforeTransformMessages(t *testing.T) {
	aborted := AssistantMessage{API: APIAnthropicMessages, Provider: "anthropic", Model: "custom", Content: []AssistantContentBlock{TextContent{Text: ""}}, StopReason: StopReasonAborted, Timestamp: 1}
	for _, tc := range []struct {
		name       string
		compat     *OpenAIResponsesCompat
		transcript TranscriptContext
		tools      []string
		// loaded is the tools each tool_search_output item declares.
		loaded [][]string
	}{
		{
			// The assistant is dropped by transformMessages, but the transcript after resolveTranscript (what Pi's buildParams and
			// converter resolve over) has no initial system message: no top-level tools, and the leading system message declares its
			// tools in place.
			name:   "an aborted assistant before the first system message with tool search",
			compat: &OpenAIResponsesCompat{SupportsMidConvoSystemMessages: new(true), SupportsToolSearch: new(true)},
			transcript: NormalizeContext(Context{Messages: []Message{
				aborted,
				SystemMessage{Content: SystemText("base prompt"), ToolsAdded: []ToolSchema{replayTool("a")}, Timestamp: 2},
				UserMessage{Content: UserText("go"), Timestamp: 3},
			}}),
			tools:  nil,
			loaded: [][]string{{"a"}},
		},
		{
			// Without mid-conversation system messages the transcript collapses into one leading system message holding every current
			// tool, which the request's tool list carries; nothing is declared in place.
			name:   "tool search without mid-conversation system messages",
			compat: &OpenAIResponsesCompat{SupportsToolSearch: new(true)},
			transcript: NormalizeContext(Context{Messages: []Message{
				SystemMessage{Content: SystemText("base prompt"), ToolsAdded: []ToolSchema{replayTool("a")}},
				UserMessage{Content: UserText("go"), Timestamp: 1},
				SystemMessage{Content: SystemText("update"), ToolsAdded: []ToolSchema{replayTool("b")}, Timestamp: 2},
			}}),
			tools:  []string{"a", "b"},
			loaded: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for provider, request := range captureToolChangesForResponsesProviders(t, tc.compat, tc.transcript) {
				if got := jsonStrings(jsonItems(request.body["tools"]), "name"); !slices.Equal(got, tc.tools) {
					t.Errorf("%s tools = %v, want %v", provider, got, tc.tools)
				}
				var loaded [][]string
				for _, item := range jsonItems(request.body["input"]) {
					if item["type"] == "tool_search_output" {
						loaded = append(loaded, jsonStrings(jsonItems(item["tools"]), "name"))
					}
				}
				if !reflect.DeepEqual(loaded, tc.loaded) {
					t.Errorf("%s tool_search_output tools = %v, want %v", provider, loaded, tc.loaded)
				}
			}
		})
	}
}
