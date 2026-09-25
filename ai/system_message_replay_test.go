package ai

// Ports .upstream/current/packages/ai/test/system-message-replay.test.ts, the
// pure contract of utils/transcript.ts, plus the fold-without-native-support
// payload cases of transcript-tool-changes.test.ts for each provider family.

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

func replayTool(name string, description ...string) ToolSchema {
	text := name + " tool"
	if len(description) > 0 {
		text = description[0]
	}
	return ToolSchema{Name: name, Description: text, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
}

func replayTranscript() TranscriptContext {
	return NormalizeContext(Context{Messages: []Message{
		SystemMessage{Content: SystemText("base"), Sections: OrderedSections{{Name: "a", Value: new("<a>1</a>")}, {Name: "b", Value: new("<b>1</b>")}}, ToolsAdded: []ToolSchema{replayTool("first")}, Timestamp: 10},
		UserMessage{Content: UserText("hello"), Timestamp: 11},
		SystemMessage{Content: SystemText("also do this"), Timestamp: 12},
		AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "ok"}}, StopReason: StopReasonStop, Timestamp: 13},
		SystemMessage{Content: SystemText(""), Sections: OrderedSections{{Name: "a", Value: new("<a>2</a>")}, {Name: "b", Value: nil}, {Name: "c", Value: new("<c>1</c>")}}, ToolsRemoved: []ToolReference{{Name: "first"}}, ToolsAdded: []ToolSchema{replayTool("second")}, Timestamp: 14},
	}})
}

func messageRoles(messages []Message) []string {
	roles := make([]string, len(messages))
	for i, message := range messages {
		roles[i] = message.messageRole()
	}
	return roles
}

func TestSystemMessageReplayReplaysContentSectionsAndToolsIntoOneLeadingMessage(t *testing.T) {
	transcript := replayTranscript()
	current := GetCurrentSystemMessage(transcript.Messages())
	want := &SystemMessage{
		Content:    SystemText("base\n\nalso do this"),
		Sections:   OrderedSections{{Name: "a", Value: new("<a>2</a>")}, {Name: "c", Value: new("<c>1</c>")}},
		ToolsAdded: []ToolSchema{replayTool("second")},
		Timestamp:  10,
	}
	if !reflect.DeepEqual(current, want) {
		t.Fatalf("current = %#v, want %#v", current, want)
	}
	if got := GetCurrentSystemPrompt(transcript.Messages()); got != "base\n\nalso do this\n\n<a>2</a>\n\n<c>1</c>" {
		t.Fatalf("prompt = %q", got)
	}
}

func TestSystemMessageReplayCollapseKeepsNonSystemMessagesAfterReplayedHead(t *testing.T) {
	collapsed := CollapseSystemMessages(replayTranscript())
	if roles := messageRoles(collapsed.Messages()); !slices.Equal(roles, []string{"system", "user", "assistant"}) {
		t.Fatalf("roles = %v", roles)
	}
	if again := CollapseSystemMessages(collapsed); !reflect.DeepEqual(again.Messages(), collapsed.Messages()) {
		t.Fatalf("collapse is not idempotent: %#v", again.Messages())
	}
}

func TestSystemMessageReplayWithoutSystemMessagesIsEmpty(t *testing.T) {
	context := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}})
	if current := GetCurrentSystemMessage(context.Messages()); current != nil {
		t.Fatalf("current = %#v", current)
	}
	if prompt := GetCurrentSystemPrompt(context.Messages()); prompt != "" {
		t.Fatalf("prompt = %q", prompt)
	}
	if collapsed := CollapseSystemMessages(context).Messages(); !reflect.DeepEqual(collapsed, context.Messages()) {
		t.Fatalf("collapsed = %#v", collapsed)
	}
}

func TestSystemMessageReplayLateFullPatchWithoutLeadingMessageBecomesPrompt(t *testing.T) {
	context := NormalizeContext(Context{Messages: []Message{
		UserMessage{Content: UserText("old session"), Timestamp: 1},
		SystemMessage{Content: SystemText(""), Sections: OrderedSections{{Name: "preamble", Value: new("You are pi.")}}, ToolsAdded: []ToolSchema{replayTool("x")}, Timestamp: 2},
	}})
	if prompt := GetCurrentSystemPrompt(context.Messages()); prompt != "You are pi." {
		t.Fatalf("prompt = %q", prompt)
	}
	head, ok := CollapseSystemMessages(context).Messages()[0].(SystemMessage)
	if !ok || !reflect.DeepEqual(head.ToolsAdded, []ToolSchema{replayTool("x")}) {
		t.Fatalf("collapsed head = %#v", head)
	}
}

func TestSystemMessageReplayRendersCompletePromptsAndFramedUpdates(t *testing.T) {
	messages := replayTranscript().Messages()
	if got := GetCurrentSystemPrompt(messages[:1]); got != "base\n\n<a>1</a>\n\n<b>1</b>" {
		t.Fatalf("leading prompt = %q", got)
	}
	want := "Updated system prompt section \"a\":\n\n<a>2</a>\n\n" +
		"Removed system prompt section \"b\".\n\n" +
		"Updated system prompt section \"c\":\n\n<c>1</c>"
	if got := RenderSystemMessageUpdate(messages[4].(SystemMessage)); got != want {
		t.Fatalf("update = %q, want %q", got, want)
	}
}

func TestSystemMessageReplayNormalizesLegacyPromptAndToolFields(t *testing.T) {
	messages := []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}
	if got := NormalizeContext(Context{Messages: messages}).Messages(); !reflect.DeepEqual(got, messages) {
		t.Fatalf("plain = %#v", got)
	}
	if got := NormalizeContext(Context{SystemPrompt: "", Tools: []ToolSchema{}, Messages: messages}).Messages(); !reflect.DeepEqual(got, messages) {
		t.Fatalf("empty prompt and tools = %#v", got)
	}
	got := NormalizeContext(Context{SystemPrompt: "be brief", Tools: []ToolSchema{replayTool("a")}, Messages: messages}).Messages()
	want := []Message{SystemMessage{Content: SystemText("be brief"), ToolsAdded: []ToolSchema{replayTool("a")}, Timestamp: 0}, messages[0]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized = %#v, want %#v", got, want)
	}
}

func TestSystemMessageReplayComparesToolDeclarationsWithoutNonDeclarationFields(t *testing.T) {
	withGuidelines := replayTool("a")
	withGuidelines.PromptGuidelines = []string{"prompt-only"}
	if !DeclarationsEqual(withGuidelines, replayTool("a")) {
		t.Fatal("prompt-only fields changed the declaration")
	}
	if DeclarationsEqual(replayTool("a"), replayTool("a", "changed")) {
		t.Fatal("changed description compared equal")
	}
	// Pi distinguishes constrainedSampling false from undefined; Go's nil is
	// both, so a set config is the differing declaration here.
	constrained := replayTool("a")
	constrained.ConstrainedSampling = &ConstrainedSamplingConfig{Type: "json_schema"}
	if DeclarationsEqual(replayTool("a"), constrained) {
		t.Fatal("constrainedSampling did not change the declaration")
	}
}

func TestSystemMessageReplayToolStateChangesTreatChangedDefinitionsAsRemovalPlusAddition(t *testing.T) {
	changes := GetToolStateChanges([]ToolSchema{replayTool("a"), replayTool("b")}, []ToolSchema{replayTool("b", "changed"), replayTool("c")})
	want := ToolStateChanges{
		ToolsAdded:   []ToolSchema{replayTool("b", "changed"), replayTool("c")},
		ToolsRemoved: []ToolReference{{Name: "a"}, {Name: "b"}},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %#v, want %#v", changes, want)
	}
	if unchanged := GetToolStateChanges([]ToolSchema{replayTool("a")}, []ToolSchema{replayTool("a")}); len(unchanged.ToolsAdded) != 0 || len(unchanged.ToolsRemoved) != 0 {
		t.Fatalf("unchanged = %#v", unchanged)
	}
}

func TestSystemMessageReplayDetectsNonAdditiveToolHistoryAndRedefinitions(t *testing.T) {
	transcript := replayTranscript().Messages()
	if !HasNonAdditiveToolChanges(transcript) || HasToolRedefinitions(transcript) {
		t.Fatal("removal history misclassified")
	}
	additive := NormalizeContext(Context{Messages: []Message{
		SystemMessage{ToolsAdded: []ToolSchema{replayTool("a")}, Timestamp: 1},
		SystemMessage{ToolsAdded: []ToolSchema{replayTool("b")}, Timestamp: 2},
	}}).Messages()
	if HasNonAdditiveToolChanges(additive) {
		t.Fatal("additive history reported non-additive")
	}
	redeclared := NormalizeContext(Context{Messages: []Message{
		SystemMessage{ToolsAdded: []ToolSchema{replayTool("a")}, Timestamp: 1},
		SystemMessage{ToolsAdded: []ToolSchema{replayTool("a", "changed")}, Timestamp: 2},
	}}).Messages()
	if !HasNonAdditiveToolChanges(redeclared) || !HasToolRedefinitions(redeclared) {
		t.Fatal("redeclaration not detected")
	}
}

// foldContext is the transcript-tool-changes.test.ts history: a later system
// message rewrites sections, removes the base tool, and adds another.
func foldContext() TranscriptContext {
	return NormalizeContext(Context{Messages: []Message{
		SystemMessage{
			Content:    SystemText("base prompt"),
			Sections:   OrderedSections{{Name: "rules", Value: new("<rules>\nold rules\n</rules>")}, {Name: "docs", Value: new("<docs>\nread docs\n</docs>")}},
			ToolsAdded: []ToolSchema{replayTool("base_tool")},
		},
		UserMessage{Content: UserText("before"), Timestamp: 1},
		SystemMessage{
			Content:      SystemText("updated guidance"),
			Sections:     OrderedSections{{Name: "rules", Value: new("<rules>\nnew rules\n</rules>")}, {Name: "docs", Value: nil}},
			ToolsRemoved: []ToolReference{{Name: "base_tool"}},
			ToolsAdded:   []ToolSchema{replayTool("late_tool")},
			Timestamp:    2,
		},
	}})
}

const foldedPrompt = "base prompt\n\nupdated guidance\n\n<rules>\nnew rules\n</rules>"

// capturePayloadVia installs a transport that records the JSON request body
// and answers with an empty stream, then drains the provider stream.
func capturePayloadVia(t *testing.T, install func(*http.Client), stream func() (*AssistantMessageEventStream, error), body string) map[string]any {
	t.Helper()
	var payload map[string]any
	install(&http.Client{Transport: responsesTestRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})})
	result, err := stream()
	if err != nil {
		t.Fatal(err)
	}
	for range result.Events(context.Background()) {
	}
	return payload
}

func payloadNames(t *testing.T, values any, path ...string) []string {
	t.Helper()
	items, _ := values.([]any)
	names := make([]string, 0, len(items))
	for _, item := range items {
		value := item
		for _, key := range path {
			value = value.(map[string]any)[key]
		}
		name, _ := value.(string)
		names = append(names, name)
	}
	return names
}

func TestTranscriptFoldsAnthropicUpdatesIntoSystemPromptWithoutNativeSupport(t *testing.T) {
	for _, compat := range []*AnthropicMessagesCompat{nil, {SupportsMidConvoToolChanges: new(true)}} {
		provider := NewAnthropicProvider(AnthropicConfig{APIKey: "test", Model: "custom-claude", ProviderID: "anthropic", Compat: compat}).(*anthropicProvider)
		payload := capturePayloadVia(t, func(client *http.Client) { provider.client = client }, func() (*AssistantMessageEventStream, error) {
			return provider.Stream(context.Background(), foldContext(), StreamOptions{})
		}, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")

		if got := payloadNames(t, payload["system"], "text"); !slices.Equal(got, []string{foldedPrompt}) {
			t.Errorf("compat %+v system = %q", compat, got)
		}
		if got := payloadNames(t, payload["tools"], "name"); !slices.Equal(got, []string{"late_tool"}) {
			t.Errorf("compat %+v tools = %v", compat, got)
		}
		if got := payloadNames(t, payload["messages"], "role"); !slices.Equal(got, []string{"user"}) {
			t.Errorf("compat %+v message roles = %v", compat, got)
		}
	}
}

func TestTranscriptFoldsOpenAIResponsesUpdatesIntoLeadingDeveloperMessage(t *testing.T) {
	provider := NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "test", Model: "custom-model", ProviderID: "openai", IsReasoning: true}).(*openAIResponsesProvider)
	payload := capturePayloadVia(t, func(client *http.Client) { provider.client = client }, func() (*AssistantMessageEventStream, error) {
		return provider.Stream(context.Background(), foldContext(), StreamOptions{})
	}, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")

	if got := payloadNames(t, payload["tools"], "name"); !slices.Equal(got, []string{"late_tool"}) {
		t.Errorf("tools = %v", got)
	}
	input, _ := payload["input"].([]any)
	if got := payloadNames(t, input, "role"); !slices.Equal(got, []string{"developer", "user"}) {
		t.Errorf("input roles = %v", got)
	}
	if first, _ := input[0].(map[string]any); first["content"] != foldedPrompt {
		t.Errorf("developer content = %#v", first["content"])
	}
}

func TestTranscriptFoldsOpenAICompletionsUpdatesIntoSystemPrompt(t *testing.T) {
	provider := &openAIProvider{cfg: OpenAIConfig{BaseURL: "https://example.test/v1", APIKey: "test", Model: "custom-model", ProviderID: "custom-provider"}}
	payload := capturePayloadVia(t, func(client *http.Client) { provider.client = client }, func() (*AssistantMessageEventStream, error) {
		return provider.Stream(context.Background(), foldContext(), StreamOptions{})
	}, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")

	if got := payloadNames(t, payload["tools"], "function", "name"); !slices.Equal(got, []string{"late_tool"}) {
		t.Errorf("tools = %v", got)
	}
	messages, _ := payload["messages"].([]any)
	if got := payloadNames(t, messages, "role"); !slices.Equal(got, []string{"system", "user"}) {
		t.Errorf("roles = %v", got)
	}
	if first, _ := messages[0].(map[string]any); first["content"] != foldedPrompt {
		t.Errorf("system content = %#v", first["content"])
	}
}
