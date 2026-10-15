package ai

import (
	"reflect"
	"regexp"
	"testing"
	"time"
)

// Ports the registration contract of packages/ai/src/providers/faux.ts: createFauxCore draws a fresh `faux:<ms>:<id>`
// API when options.api is absent, copies each definition's cost and inputLimits onto its model, and fauxAssistantMessage
// defaults the stop reason to "stop".
// Pi: packages/ai/src/providers/faux.ts:47 (cost).
// Pi: packages/ai/src/providers/faux.ts:46 (inputLimits).
func TestFauxRegistrationContractUpstream(t *testing.T) {
	randomID := regexp.MustCompile(`^faux:\d+:[0-9a-z]+$`)
	t.Run("a registration without an api draws a fresh random one", func(t *testing.T) {
		first, second := NewFauxProvider(FauxConfig{}), NewFauxProvider(FauxConfig{})
		a, b := first.GetModel().ProviderMeta.API, second.GetModel().ProviderMeta.API
		if !randomID.MatchString(string(a)) || !randomID.MatchString(string(b)) || a == b {
			t.Fatalf("default apis = %q, %q", a, b)
		}
	})
	t.Run("an explicit api is kept", func(t *testing.T) {
		if got := NewFauxProvider(FauxConfig{API: "faux:test"}).GetModel().ProviderMeta.API; got != "faux:test" {
			t.Fatalf("api = %q", got)
		}
	})
	t.Run("model definitions carry cost and input limits", func(t *testing.T) {
		limits := &ModelInputLimits{}
		provider := NewFauxProvider(FauxConfig{Models: []FauxModelDefinition{
			{ID: "priced", Cost: &ModelCost{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4}, InputLimits: limits},
			{ID: "free"},
		}})
		priced, free := provider.GetModel("priced"), provider.GetModel("free")
		capabilities := priced.Capabilities
		if capabilities.InputCostPer1M != 1 || capabilities.OutputCostPer1M != 2 || capabilities.CacheReadCostPer1M != 3 || capabilities.CacheWriteCostPer1M != 4 {
			t.Fatalf("priced capabilities = %+v", capabilities)
		}
		if priced.InputLimits == nil || !reflect.DeepEqual(priced.InputLimits, limits) || free.InputLimits != nil || free.Capabilities.InputCostPer1M != 0 {
			t.Fatalf("limits/free = %v %v %+v", priced.InputLimits, free.InputLimits, free.Capabilities)
		}
	})
	t.Run("fauxAssistantMessage and fauxToolCall defaults", func(t *testing.T) {
		before := time.Now().UnixMilli()
		response := FauxAssistantMessage(FauxContentBlocks{FauxText("hi")}, FauxAssistantMessageOptions{})
		if response.StopReason != "stop" || len(response.Content) != 1 {
			t.Fatalf("response = %+v", response)
		}
		// timestamp: options.timestamp ?? Date.now() is taken when the message is built, not when it is streamed.
		if response.Timestamp < before || response.Timestamp > time.Now().UnixMilli() {
			t.Fatalf("timestamp = %v, want the creation time", response.Timestamp)
		}
		provider := NewFauxProvider(FauxConfig{})
		provider.SetResponses([]FauxResponseStep{FauxAssistantMessage(nil, FauxAssistantMessageOptions{Timestamp: new(int64(42))})})
		if streamed := fauxUpstreamComplete(t, provider, fauxUpstreamRequest(), StreamOptions{}); streamed.Timestamp != 42 {
			t.Fatalf("streamed timestamp = %d, want the message's 42", streamed.Timestamp)
		}
		stamp := new(int64(7))
		response = FauxAssistantMessage(nil, FauxAssistantMessageOptions{StopReason: "error", ErrorMessage: "boom", ResponseID: "r1", Timestamp: stamp})
		if response.StopReason != "error" || response.ErrorMessage != "boom" || response.ResponseID != "r1" || response.Timestamp != 7 {
			t.Fatalf("response = %+v", response)
		}
		if id := FauxToolCall("t", nil, &FauxToolCallOptions{ID: ""}).ID; !regexp.MustCompile(`^tool:\d+:[0-9a-z]+$`).MatchString(id) {
			t.Fatalf("tool id = %q", id)
		}
		// faux.ts:62-65 fauxToolCall(name, arguments, { id }): options.id names the call.
		if id := FauxToolCall("t", nil, &FauxToolCallOptions{ID: "call-7"}).ID; id != "call-7" {
			t.Fatalf("options id = %q, want call-7", id)
		}
	})
}

// faux.ts cloneMessage keeps every member of a scripted AssistantMessage but api, provider, model, timestamp and usage, so its durationMs reaches the final message, and
// event-stream.ts:127 `message.durationMs !== undefined` leaves a message that has one unmeasured.
func TestFauxScriptedDurationMsSurvivesTheEventStreamUpstream(t *testing.T) {
	provider := NewFauxProvider(FauxConfig{})
	scripted := FauxAssistantMessage(FauxContentBlocks{FauxText("hi")}, FauxAssistantMessageOptions{})
	scripted.DurationMs = new(int64(7000))
	provider.SetResponses([]FauxResponseStep{scripted})
	if got := fauxUpstreamComplete(t, provider, fauxUpstreamRequest(), StreamOptions{}); got.DurationMs == nil || *got.DurationMs != 7000 {
		t.Fatalf("streamed durationMs = %v, want the scripted 7000", got.DurationMs)
	}
	provider.SetResponses([]FauxResponseStep{FauxAssistantMessage(FauxContentBlocks{FauxText("hi")}, FauxAssistantMessageOptions{})})
	if got := fauxUpstreamComplete(t, provider, fauxUpstreamRequest(), StreamOptions{}); got.DurationMs == nil || *got.DurationMs >= 7000 {
		t.Fatalf("unscripted durationMs = %v, want the stream's own measurement", got.DurationMs)
	}
	fromMessage := fauxResponseFromMessage(AssistantMessage{DurationMs: new(int64(9))})
	if fromMessage.DurationMs == nil || *fromMessage.DurationMs != 9 {
		t.Fatalf("an AssistantMessage step lost durationMs: %v", fromMessage.DurationMs)
	}
}

// faux.ts fauxAssistantMessage and a FauxResponseFactory return an AssistantMessage whose content blocks are the standard text, thinking and toolCall blocks
// (types.ts AssistantMessage); the faux stream reads that message back, so every member the scripted response sets reaches the streamed final message.
func TestFauxAssistantMessageIsAnAssistantMessageThatStreamsBackUnchanged(t *testing.T) {
	message := FauxAssistantMessage(FauxContentBlocks{
		{Type: FauxContentThinking, Thinking: "hmm", ThinkingSignature: "sig", Redacted: true},
		{Type: FauxContentText, Text: "hi", TextSignature: "tsig"},
		FauxToolCall("t", map[string]any{"a": float64(1)}, &FauxToolCallOptions{ID: "id1"}),
	}, FauxAssistantMessageOptions{StopReason: "toolUse", ResponseID: "r9", Timestamp: new(int64(5))})
	want := []AssistantContentBlock{
		ThinkingContent{Thinking: "hmm", ThinkingSignature: "sig", Redacted: true},
		TextContent{Text: "hi", TextSignature: "tsig"},
		ToolCall{ID: "id1", Name: "t", Arguments: JsonObject{"a": float64(1)}},
	}
	if message.StopReason != StopReasonToolUse || message.ResponseID != "r9" || message.Timestamp != 5 || !reflect.DeepEqual(message.Content, want) {
		t.Fatalf("FauxAssistantMessage = %+v", message)
	}
	provider := NewFauxProvider(FauxConfig{})
	provider.SetResponses([]FauxResponseStep{FauxFactoryStep(func(TranscriptContext, StreamOptions, *FauxProviderState, *Model) (AssistantMessage, error) {
		return message, nil
	})})
	got := fauxUpstreamComplete(t, provider, fauxUpstreamRequest(), StreamOptions{})
	if got.ResponseID != "r9" || got.Timestamp != 5 || got.StopReason != StopReasonToolUse || !reflect.DeepEqual(got.Content, want) {
		t.Fatalf("streamed factory message = %+v", got)
	}
}

// faux.ts:78 fauxAssistantMessage(content: string | FauxContentBlock | FauxContentBlock[]): a string is one text block, a block is itself, an array is as given.
func TestFauxAssistantMessageAcceptsStringBlockAndArrayContent(t *testing.T) {
	block := FauxToolCall("t", nil, &FauxToolCallOptions{ID: "id"})
	for name, c := range map[string]struct {
		content FauxAssistantContent
		want    []AssistantContentBlock
	}{
		"string": {FauxContentString("hi"), []AssistantContentBlock{TextContent{Text: "hi"}}},
		"block":  {block, []AssistantContentBlock{ToolCall{ID: "id", Name: "t"}}},
		"array":  {FauxContentBlocks{FauxText("a"), FauxThinking("b")}, []AssistantContentBlock{TextContent{Text: "a"}, ThinkingContent{Thinking: "b"}}},
		"none":   {nil, []AssistantContentBlock{}},
	} {
		if got := FauxAssistantMessage(c.content, FauxAssistantMessageOptions{}).Content; !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s content = %#v, want %#v", name, got, c.want)
		}
	}
}
