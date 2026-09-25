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
			out[i] += ":" + tool["name"].(string) + ":" + tool["type"].(string)
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
		// lastBlocks lists the final message's blocks as type[:tool:reference type].
		lastBlocks []string
		// description is the first tool's expected description.
		description string
	}{
		{
			name: "native updates and tool changes", compat: nativeAnthropicCompat, transcript: foldContext(), native: true,
			system: []string{nativePrompt},
			// Initial tools stay active and carry the cache breakpoint; the placeholder
			// and every later declaration are deferred; the removed tool stays declared.
			tools: []string{"base_tool", deferredPlaceholderName, "late_tool"}, deferred: []bool{false, true, true}, cached: []bool{true, false, false},
			roles:      []string{"user", "system"},
			lastBlocks: []string{"text", "tool_removal:base_tool:tool_reference", "tool_addition:late_tool:tool_reference"},
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
			// Same-name redefinition: blocks reference tools by name only.
			name: "redefinition falls back to the current tool list", compat: nativeAnthropicCompat,
			transcript: NormalizeContext(Context{Messages: []Message{
				SystemMessage{Content: SystemText("base prompt"), ToolsAdded: []ToolSchema{replayTool("base_tool")}},
				SystemMessage{Content: SystemText("updated guidance"), ToolsRemoved: []ToolReference{{Name: "base_tool"}}, ToolsAdded: []ToolSchema{redefined}, Timestamp: 2},
			}}),
			system: []string{"base prompt"},
			tools:  []string{"base_tool"}, deferred: []bool{false}, cached: []bool{true},
			roles: []string{"system"}, lastBlocks: []string{"text"}, description: "changed",
		},
		{
			// No initial tool: Anthropic rejects an all-deferred tool list.
			name: "no initial tool falls back to the current tool list", compat: nativeAnthropicCompat,
			transcript: NormalizeContext(Context{Messages: []Message{
				SystemMessage{Content: SystemText("base prompt")},
				SystemMessage{Content: SystemText("updated guidance"), ToolsAdded: []ToolSchema{redefined}, Timestamp: 2},
			}}),
			system: []string{"base prompt"},
			tools:  []string{"base_tool"}, deferred: []bool{false}, cached: []bool{true},
			roles: []string{"system"}, lastBlocks: []string{"text"}, description: "changed",
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
			if got := slices.Contains(anthropicBetas(request), midConversationToolChangesBeta); got != tc.native {
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
			if got := anthropicBlockTools(jsonItems(messages[len(messages)-1]["content"])); !slices.Equal(got, tc.lastBlocks) {
				t.Errorf("last blocks = %v, want %v", got, tc.lastBlocks)
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
	// Native deferred tools keep their schema and the eager-streaming flag.
	late := jsonItems(request.body["tools"])[2]
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
	if !slices.Contains(anthropicBetas(request), midConversationToolChangesBeta) {
		t.Errorf("beta header %q", request.header.Get("anthropic-beta"))
	}
	if got := jsonStrings(jsonItems(request.body["tools"]), "name"); !slices.Equal(got, []string{"base_tool", deferredPlaceholderName, "late_tool"}) {
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
	if got := jsonStrings(jsonItems(request.body["tools"]), "name"); !slices.Equal(got, []string{"Read", deferredPlaceholderName, "Bash"}) {
		t.Errorf("tools = %v", got)
	}
	messages := jsonItems(request.body["messages"])
	if got := anthropicBlockTools(jsonItems(messages[len(messages)-1]["content"])); !slices.Equal(got, []string{"tool_removal:Read:tool_reference", "tool_addition:Bash:tool_reference"}) {
		t.Errorf("last blocks = %v", got)
	}
	if got := anthropicBetas(request); !slices.Equal(got, []string{claudeCodeBeta, oauthBeta, midConversationToolChangesBeta}) {
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
		"system(text,tool_addition:late_tool:tool_reference)",
		"assistant(tool_use:call_1_fc_1)",
		"user(tool_result:call_1_fc_1)",
		"system(text)",
		"assistant(tool_use:call_2)",
		"user(text)",
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
		if got := jsonStrings(jsonItems(request.body["tools"]), "name"); !slices.Equal(got, []string{"base_tool", deferredPlaceholderName, "late_tool"}) {
			t.Errorf("request %d tools = %v", i, got)
		}
		if !slices.Contains(anthropicBetas(request), midConversationToolChangesBeta) {
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
			name:   "anchors additions at their developer message",
			compat: &OpenAIResponsesCompat{SupportsMidConvoSystemMessages: new(true), SupportsAdditionalTools: new(true)}, transcript: additionContext(),
			tools:        []string{"base_tool"},
			shape:        []string{"developer", "user", "additional_tools[late_tool]", "developer"},
			instructions: []string{"base prompt", "updated guidance"},
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
			tools:        []string{"base_tool"},
			shape:        []string{"developer", "user", "additional_tools[late_tool]", "developer"},
			instructions: []string{"base prompt", "updated guidance"},
		},
		{
			name:   "folds updates into the leading developer message without native support",
			compat: &OpenAIResponsesCompat{SupportsAdditionalTools: new(true)}, transcript: foldContext(),
			tools:        []string{"late_tool"},
			shape:        []string{"developer", "user"},
			instructions: []string{foldedPrompt},
		},
		{
			name:   "falls back to the complete current tool state when removals are unsupported",
			compat: &OpenAIResponsesCompat{SupportsMidConvoSystemMessages: new(true), SupportsAdditionalTools: new(true)}, transcript: foldContext(),
			tools:        []string{"late_tool"},
			shape:        []string{"developer", "user", "developer"},
			instructions: []string{"base prompt\n\n<rules>\nold rules\n</rules>\n\n<docs>\nread docs\n</docs>", RenderSystemMessageUpdate(foldContext().Messages()[2].(SystemMessage))},
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
	callID := "pi_tool_load_" + shortHash32("system:1:late_tool")
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
	item := jsonItems(request.body["input"])[2]
	tools := jsonItems(item["tools"])
	// Added tools use the request tool conversion, including strict mode.
	if item["role"] != "developer" || len(tools) != 1 || tools[0]["strict"] != false || tools[0]["defer_loading"] != nil {
		t.Errorf("additional_tools = %#v", item)
	}
}

func TestTranscriptToolChangesOpenAIResponsesPoisonedHistory(t *testing.T) {
	compat := &OpenAIResponsesCompat{SupportsMidConvoSystemMessages: new(true), SupportsAdditionalTools: new(true)}
	request := captureResponsesToolChanges(t, compat, poisonedToolChangeContext())
	want := []string{
		"developer", "user", "message",
		"additional_tools[late_tool]", "developer", "user",
		"function_call:call_1", "developer", "function_call_output:call_1",
		"message", "function_call:call_2", "user",
	}
	input := jsonItems(request.body["input"])
	// This fixture calls the converter directly, before agent.NormalizeMessages filters failed turns. Responses retains the empty text block here.
	empty, err := json.Marshal(input[2])
	if err != nil {
		t.Fatal(err)
	}
	assertShapeJSON(t, empty, `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"","annotations":[]}],"status":"completed","id":"msg_pi_1"}`)
	if got := responsesInputShape(input); !slices.Equal(got, want) {
		t.Errorf("input = %v, want %v", got, want)
	}
	if got := jsonStrings(jsonItems(request.body["tools"]), "name"); !slices.Equal(got, []string{"base_tool"}) {
		t.Errorf("tools = %v", got)
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

// completionsSystemContents lists every system message's content; a
// tool-bearing message has none.
func completionsSystemContents(messages []map[string]any) []string {
	var out []string
	for _, message := range messages {
		if message["role"] != "system" {
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
			shape:  []string{"system", "user"},
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
		"assistant:call_1_fc_1", "system", "tool:call_1_fc_1",
		"assistant:call_2", "user",
	}
	if got := completionsMessageShape(jsonItems(request.body["messages"])); !slices.Equal(got, want) {
		t.Errorf("messages = %v, want %v", got, want)
	}
	if got := completionsToolNames(request.body["tools"]); !slices.Equal(got, []string{"base_tool"}) {
		t.Errorf("tools = %v", got)
	}
}
