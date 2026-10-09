package ai

import (
	"encoding/json"
	"slices"
	"testing"
)

// The pinned 1.0.4 content blocks and messages are discriminated unions: `type` for content blocks, `role` for messages.
// Go carries the discriminator as the variant type and emits it through MarshalJSON. A fully populated value must emit
// the upstream literal and exactly the upstream property names (packages/ai/src/types.ts).
func TestWireDiscriminatorsMatchUpstreamLiterals(t *testing.T) {
	handle := &DeferredHandle{Provider: "p", ModelID: "m", API: "a", ID: "h"}
	endTurn := true
	tool := ToolSchema{Name: "read", Description: "d", Parameters: map[string]any{"type": "object"}}
	sectionValue := "v"
	for _, tc := range []struct {
		name         string
		value        any
		discriminant string
		literal      string
		keys         []string
	}{
		{"TextContent", TextContent{Text: "t", TextSignature: "s"}, "type", "text", []string{"text", "textSignature", "type"}},
		{"ThinkingContent", ThinkingContent{Thinking: "t", ThinkingSignature: "s", Redacted: true}, "type", "thinking", []string{"redacted", "thinking", "thinkingSignature", "type"}},
		{"ImageContent", ImageContent{Data: "d", MimeType: "image/png"}, "type", "image", []string{"data", "mimeType", "type"}},
		{"ToolCall", ToolCall{ID: "i", Name: "n", Arguments: JsonObject{}, Namespace: "ns", ThoughtSignature: "s"}, "type", "toolCall", []string{"arguments", "id", "name", "namespace", "thoughtSignature", "type"}},
		{"UserMessage", UserMessage{Content: UserText("hi"), Timestamp: 1}, "role", "user", []string{"content", "role", "timestamp"}},
		{"SystemMessage", SystemMessage{Content: SystemText("s"), Sections: OrderedSections{{Name: "a", Value: &sectionValue}}, Timestamp: 1, ToolsAdded: []ToolSchema{tool}, ToolsRemoved: []ToolReference{{Name: "x"}}}, "role", "system", []string{"content", "role", "sections", "timestamp", "toolsAdded", "toolsRemoved"}},
		{"ToolResultMessage", ToolResultMessage{ToolCallID: "c", ToolName: "n", Content: []ToolResultMessageContent{TextContent{Text: "x"}}, Details: map[string]any{"k": 1}, Usage: &Usage{}, NestedCalls: &NestedToolCalls{}, IsError: true, Timestamp: 1}, "role", "toolResult", []string{"content", "details", "isError", "nestedCalls", "role", "timestamp", "toolCallId", "toolName", "usage"}},
		{"AssistantMessage", AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "x"}}, API: "a", Provider: "p", Model: "m", ResponseModel: "rm", ResponseID: "r", ProviderThinkingLevel: "high", Diagnostics: []AssistantMessageDiagnostic{{Type: "d", Timestamp: 1}}, StopReason: StopReasonDeferred, Deferred: handle, ErrorMessage: "e", RawStopReason: "raw", EndTurn: &endTurn, Timestamp: 1, ThinkingLevel: ThinkingHigh}, "role", "assistant",
			[]string{"api", "content", "deferred", "diagnostics", "endTurn", "errorMessage", "model", "provider", "providerThinkingLevel", "rawStopReason", "responseId", "responseModel", "role", "stopReason", "thinkingLevel", "timestamp", "usage"}},
	} {
		encoded, err := json.Marshal(tc.value)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if decoded[tc.discriminant] != tc.literal {
			t.Errorf("%s: %s = %v, want %q", tc.name, tc.discriminant, decoded[tc.discriminant], tc.literal)
		}
		var keys []string
		for key := range decoded {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		if !slices.Equal(keys, tc.keys) {
			t.Errorf("%s: emitted keys %v, upstream properties %v", tc.name, keys, tc.keys)
		}
	}
}

// packages/ai/src/types.ts ClassifierQuestion and ClassifierAnswer variants: the `type` literal and the property names,
// with Record-typed members (criteria, probabilities) as JSON objects in insertion order.
func TestClassifierQuestionAndAnswerWireShapesMatchUpstream(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"ClassifierBoolQuestion", ClassifierBoolQuestion{Instructions: "i", Criteria: ClassifierBoolCriteria{True: "y", False: "n"}}, `{"type":"bool","instructions":"i","criteria":{"true":"y","false":"n"}}`},
		{"ClassifierChoiceQuestion", ClassifierChoiceQuestion{Instructions: "i", Criteria: []ClassifierChoiceCriterion{{Key: "b", Description: "B"}, {Key: "a", Description: "A"}}}, `{"type":"choice","instructions":"i","criteria":{"b":"B","a":"A"}}`},
		{"ClassifierScoreQuestion", ClassifierScoreQuestion{Instructions: "i", Criteria: []string{"low", "high"}}, `{"type":"score","instructions":"i","criteria":["low","high"]}`},
		{"ClassifierBoolAnswer", ClassifierBoolAnswer{Probability: 0.75}, `{"type":"bool","probability":0.75}`},
		{"ClassifierChoiceAnswer", ClassifierChoiceAnswer{Choice: "b", Probabilities: []ClassifierProbability{{Key: "b", Probability: 0.6}, {Key: "a", Probability: 0.4}}, Confidence: 0.6}, `{"type":"choice","choice":"b","probabilities":{"b":0.6,"a":0.4},"confidence":0.6}`},
		{"ClassifierScoreAnswer", ClassifierScoreAnswer{Score: 2, Confidence: 0.5}, `{"type":"score","score":2,"confidence":0.5}`},
	} {
		encoded, err := json.Marshal(tc.value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("%s: %s, %v; want %s", tc.name, encoded, err, tc.want)
		}
	}
}

// packages/ai/src/types.ts ImageModel.type is "image", ClassifierModel.type is "classifier" and Model.type is "chat" or
// absent. Go carries the discriminator as the ModelType accessor.
func TestModelTypeDiscriminatorsMatchUpstreamLiterals(t *testing.T) {
	if got := (&ImageModel{}).ModelType(); got != "image" {
		t.Errorf("ImageModel.ModelType() = %q, want image", got)
	}
	if got := (&ClassifierModel{}).ModelType(); got != "classifier" {
		t.Errorf("ClassifierModel.ModelType() = %q, want classifier", got)
	}
	if got := (&Model{}).ModelType(); got != "chat" {
		t.Errorf("Model.ModelType() = %q, want chat for an absent type", got)
	}
	models := []AnyModel{&Model{}, &ImageModel{}, &ClassifierModel{}}
	seen := map[ModelType]bool{}
	for _, model := range models {
		seen[model.ModelType()] = true
	}
	if len(seen) != 3 {
		t.Errorf("the three model kinds must report distinct types, got %v", seen)
	}
}

// packages/ai/src/types.ts AssistantMessageEvent: every variant emits its `type` literal and exactly its upstream
// property names.
func TestAssistantMessageEventVariantsMatchUpstreamShapes(t *testing.T) {
	partial := testAssistant(StopReasonPending)
	final := testAssistant(StopReasonStop)
	for _, tc := range []struct {
		event AssistantMessageEvent
		keys  []string
	}{
		{StartEvent{Partial: partial}, []string{"partial", "type"}},
		{TextStartEvent{ContentIndex: 1, Partial: partial}, []string{"contentIndex", "partial", "type"}},
		{TextDeltaEvent{ContentIndex: 1, Delta: "d", Partial: partial}, []string{"contentIndex", "delta", "partial", "type"}},
		{TextEndEvent{ContentIndex: 1, Content: "c", Partial: partial}, []string{"content", "contentIndex", "partial", "type"}},
		{ThinkingStartEvent{ContentIndex: 1, Partial: partial}, []string{"contentIndex", "partial", "type"}},
		{ThinkingDeltaEvent{ContentIndex: 1, Delta: "d", Partial: partial}, []string{"contentIndex", "delta", "partial", "type"}},
		{ThinkingEndEvent{ContentIndex: 1, Content: "c", Partial: partial}, []string{"content", "contentIndex", "partial", "type"}},
		{ToolCallStartEvent{ContentIndex: 1, Partial: partial}, []string{"contentIndex", "partial", "type"}},
		{ToolCallDeltaEvent{ContentIndex: 1, Delta: "d", Partial: partial}, []string{"contentIndex", "delta", "partial", "type"}},
		{ToolCallEndEvent{ContentIndex: 1, ToolCall: ToolCall{ID: "i", Name: "n", Arguments: JsonObject{}}, Partial: partial}, []string{"contentIndex", "partial", "toolCall", "type"}},
		{DoneEvent{Reason: StopReasonStop, Message: final}, []string{"message", "reason", "type"}},
		{ErrorEvent{Reason: StopReasonError, Error: final}, []string{"error", "reason", "type"}},
	} {
		encoded, err := json.Marshal(tc.event)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		var keys []string
		for key := range decoded {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		if decoded["type"] != string(tc.event.EventType()) || !slices.Equal(keys, tc.keys) {
			t.Errorf("%T: type %v keys %v, want type %s keys %v", tc.event, decoded["type"], keys, tc.event.EventType(), tc.keys)
		}
	}
	for _, literal := range []AssistantEventType{EventStart, EventTextStart, EventTextDelta, EventTextEnd, EventThinkingStart, EventThinkingDelta, EventThinkingEnd, EventToolCallStart, EventToolCallDelta, EventToolCallEnd, EventDone, EventError} {
		want := map[AssistantEventType]string{EventStart: "start", EventTextStart: "text_start", EventTextDelta: "text_delta", EventTextEnd: "text_end", EventThinkingStart: "thinking_start", EventThinkingDelta: "thinking_delta", EventThinkingEnd: "thinking_end", EventToolCallStart: "toolcall_start", EventToolCallDelta: "toolcall_delta", EventToolCallEnd: "toolcall_end", EventDone: "done", EventError: "error"}[literal]
		if string(literal) != want {
			t.Errorf("event type %q, want %q", literal, want)
		}
	}
}

// packages/ai/src/auth/types.ts AuthPrompt and AuthEvent: every variant emits its `type` literal first and exactly its
// upstream property names.
func TestAuthPromptAndEventVariantsMatchUpstreamShapes(t *testing.T) {
	interval, expires := 5.0, 900.0
	for _, tc := range []struct {
		value any
		want  string
	}{
		{AuthTextPrompt{Message: "m", Placeholder: "p"}, `{"type":"text","message":"m","placeholder":"p"}`},
		{AuthSecretPrompt{Message: "m", Placeholder: "p"}, `{"type":"secret","message":"m","placeholder":"p"}`},
		{AuthManualCodePrompt{Message: "m", Placeholder: "p"}, `{"type":"manual_code","message":"m","placeholder":"p"}`},
		{AuthSelectPrompt{Message: "m", Options: []AuthSelectOption{{ID: "i", Label: "l", Description: "d"}}}, `{"type":"select","message":"m","options":[{"id":"i","label":"l","description":"d"}]}`},
		{AuthInfoEvent{Message: "m", Links: []AuthInfoLink{{URL: "u", Label: "l"}}}, `{"type":"info","message":"m","links":[{"url":"u","label":"l"}]}`},
		{AuthURLEvent{URL: "u", Instructions: "i"}, `{"type":"auth_url","url":"u","instructions":"i"}`},
		{AuthDeviceCodeEvent{UserCode: "c", VerificationURI: "v", IntervalSeconds: &interval, ExpiresInSeconds: &expires}, `{"type":"device_code","userCode":"c","verificationUri":"v","intervalSeconds":5,"expiresInSeconds":900}`},
		{AuthProgressEvent{Message: "m"}, `{"type":"progress","message":"m"}`},
	} {
		encoded, err := json.Marshal(tc.value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("%T: %s, %v; want %s", tc.value, encoded, err, tc.want)
		}
	}
}
