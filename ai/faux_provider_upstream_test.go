package ai

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func fauxUpstreamRequest() TranscriptContext {
	return NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})
}
func fauxUpstreamComplete(t *testing.T, p Provider, request TranscriptContext, options StreamOptions) *AssistantMessage {
	t.Helper()
	stream, err := p.Stream(t.Context(), request, options)
	if err != nil {
		t.Fatal(err)
	}
	return stream.Result()
}
func fauxUpstreamText(text string) FauxResponseStep {
	return FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText(text)}, StopReason: "stop"})
}
func fauxUpstreamEvents(t *testing.T, p Provider, ctx context.Context) (*AssistantMessage, []AssistantMessageEvent) {
	t.Helper()
	stream, err := p.Stream(ctx, fauxUpstreamRequest(), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	events := []AssistantMessageEvent{}
	for event := range stream.Events(t.Context()) {
		events = append(events, event)
	}
	return stream.Result(), events
}
func fauxUpstreamEventTypes(events []AssistantMessageEvent) []AssistantEventType {
	out := make([]AssistantEventType, len(events))
	for i, event := range events {
		out[i] = event.EventType()
	}
	return out
}

// Pi source: packages/ai/src/providers/faux.ts
// mutation-checked: dropping the reads and writes of FauxModelDefinition.Reasoning fails it
// mutation-checked: zeroing the results of FauxProviderHandle.AppendResponses fails it
// Pi: packages/ai/src/providers/faux.ts:141 (appendResponses)
// packages/ai/src/providers/faux.ts:141-154,668 (FauxProviderHandle.appendResponses, unregister).
func TestFauxProviderUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/faux-provider.test.ts:31
	t.Run("registers a custom provider and estimates usage", func(t *testing.T) {
		p := NewFauxProvider(FauxConfig{})
		p.SetResponses([]FauxResponseStep{fauxUpstreamText("hello world")})
		result := fauxUpstreamComplete(t, p, NormalizeContext(Context{SystemPrompt: "Be concise.", Messages: []Message{UserMessage{Content: UserText("hi there")}}}), StreamOptions{})
		if !reflect.DeepEqual(result.Content, []AssistantContentBlock{TextContent{Text: "hello world"}}) || result.Usage.Input <= 0 || result.Usage.Output <= 0 || result.Usage.TotalTokens != result.Usage.Input+result.Usage.Output || p.CallCount() != 1 {
			t.Fatalf("result=%#v calls=%d", result, p.CallCount())
		}
	})
	// .upstream/v0.87.1/packages/ai/test/faux-provider.test.ts:49
	t.Run("supports helper blocks for text thinking and tool calls", func(t *testing.T) {
		p := NewFauxProvider(FauxConfig{})
		call := FauxToolCall("echo", map[string]any{"text": "hi"}, &FauxToolCallOptions{ID: ""})
		p.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxThinking("think"), call, FauxText("done")}, StopReason: "toolUse"})})
		result := fauxUpstreamComplete(t, p, fauxUpstreamRequest(), StreamOptions{})
		want := []AssistantContentBlock{ThinkingContent{Thinking: "think"}, ToolCall{ID: call.ID, Name: "echo", Arguments: JsonObject{"text": "hi"}}, TextContent{Text: "done"}}
		if call.ID == "" || result.StopReason != StopReasonToolUse || !reflect.DeepEqual(result.Content, want) {
			t.Fatalf("result=%#v", result)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/faux-provider.test.ts:70
	t.Run("supports multiple models with per-model reasoning and model-aware factories", func(t *testing.T) {
		p := NewFauxProvider(FauxConfig{Models: []FauxModelDefinition{{ID: "faux-fast", Name: "Faux Fast"}, {ID: "faux-thinker", Name: "Faux Thinker", Reasoning: true}}})
		factory := FauxFactoryStep(func(_ TranscriptContext, _ StreamOptions, _ *FauxProviderState, model *Model) (AssistantMessage, error) {
			return FauxResponse{Content: []FauxContentBlock{FauxText(fmt.Sprintf("%s:%t", model.ID, model.ProviderMeta.Reasoning))}, StopReason: "stop"}.AssistantMessage(), nil
		})
		p.SetResponses([]FauxResponseStep{factory, factory})
		models := p.Models()
		if len(models) != 2 || models[0].ID != "faux-fast" || models[1].ID != "faux-thinker" || p.GetModel() != models[0] || p.GetModel("faux-fast").ProviderMeta.Reasoning || !p.GetModel("faux-thinker").ProviderMeta.Reasoning {
			t.Fatal("wrong model definitions")
		}
		for _, id := range []string{"faux-fast", "faux-thinker"} {
			result := fauxUpstreamComplete(t, p.GetModel(id).Provider, fauxUpstreamRequest(), StreamOptions{})
			want := id + ":false"
			if id == "faux-thinker" {
				want = id + ":true"
			}
			if !reflect.DeepEqual(result.Content, []AssistantContentBlock{TextContent{Text: want}}) {
				t.Fatalf("result=%#v", result)
			}
		}
	})
	// .upstream/v0.87.1/packages/ai/test/faux-provider.test.ts:99
	t.Run("rewrites api provider and model on returned messages", func(t *testing.T) {
		p := NewFauxProvider(FauxConfig{API: "faux:test", ProviderID: "faux-provider", Models: []FauxModelDefinition{{ID: "faux-model"}}})
		p.SetResponses([]FauxResponseStep{fauxUpstreamText("hello")})
		result := fauxUpstreamComplete(t, p, fauxUpstreamRequest(), StreamOptions{})
		if result.API != "faux:test" || result.Provider != "faux-provider" || result.Model != "faux-model" {
			t.Fatalf("result=%#v", result)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/faux-provider.test.ts:117
	t.Run("consumes queued responses in order and errors when exhausted", func(t *testing.T) {
		p := NewFauxProvider(FauxConfig{})
		p.SetResponses([]FauxResponseStep{fauxUpstreamText("first"), fauxUpstreamText("second")})
		for _, want := range []string{"first", "second"} {
			result := fauxUpstreamComplete(t, p, fauxUpstreamRequest(), StreamOptions{})
			if !reflect.DeepEqual(result.Content, []AssistantContentBlock{TextContent{Text: want}}) {
				t.Fatal(result)
			}
		}
		result := fauxUpstreamComplete(t, p, fauxUpstreamRequest(), StreamOptions{})
		if result.StopReason != StopReasonError || result.ErrorMessage != "No more faux responses queued" || p.PendingResponseCount() != 0 || p.CallCount() != 3 {
			t.Fatalf("result=%#v", result)
		}
	})
	// packages/ai/test/faux-provider.test.ts:138 (appendResponses at :154); packages/ai/src/providers/faux.ts:668
	t.Run("can replace and append queued responses", func(t *testing.T) {
		p := NewFauxProvider(FauxConfig{})
		for _, text := range []string{"first", "second"} {
			p.SetResponses([]FauxResponseStep{fauxUpstreamText(text)})
			if p.PendingResponseCount() != 1 {
				t.Fatal("missing queued response")
			}
			result := fauxUpstreamComplete(t, p, fauxUpstreamRequest(), StreamOptions{})
			if !reflect.DeepEqual(result.Content, []AssistantContentBlock{TextContent{Text: text}}) || p.PendingResponseCount() != 0 {
				t.Fatal(result)
			}
		}
		p.AppendResponses([]FauxResponseStep{fauxUpstreamText("third"), fauxUpstreamText("fourth")})
		if p.PendingResponseCount() != 2 {
			t.Fatal("append count")
		}
		for _, text := range []string{"third", "fourth"} {
			if result := fauxUpstreamComplete(t, p, fauxUpstreamRequest(), StreamOptions{}); !reflect.DeepEqual(result.Content, []AssistantContentBlock{TextContent{Text: text}}) {
				t.Fatal(result)
			}
		}
		if p.PendingResponseCount() != 0 {
			t.Fatal("queue not drained")
		}
	})
	// .upstream/v0.87.1/packages/ai/test/faux-provider.test.ts:161
	t.Run("supports async response factories", func(t *testing.T) {
		p := NewFauxProvider(FauxConfig{})
		started, release := make(chan struct{}), make(chan struct{})
		p.SetResponses([]FauxResponseStep{FauxFactoryStep(func(context TranscriptContext, _ StreamOptions, state *FauxProviderState, _ *Model) (AssistantMessage, error) {
			close(started)
			<-release
			return FauxResponse{Content: []FauxContentBlock{FauxText(fmt.Sprintf("%d:%d", len(context.Messages()), state.CallCount()))}, StopReason: "stop"}.AssistantMessage(), nil
		})})
		stream, err := p.Stream(t.Context(), fauxUpstreamRequest(), StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		<-started
		close(release)
		if result := stream.Result(); !reflect.DeepEqual(result.Content, []AssistantContentBlock{TextContent{Text: "1:1"}}) {
			t.Fatal(result)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/faux-provider.test.ts:175
	t.Run("emits an error when a response factory throws", func(t *testing.T) {
		p := NewFauxProvider(FauxConfig{})
		p.SetResponses([]FauxResponseStep{FauxFactoryStep(func(TranscriptContext, StreamOptions, *FauxProviderState, *Model) (AssistantMessage, error) {
			return FauxResponse{}.AssistantMessage(), errors.New("boom")
		})})
		result, events := fauxUpstreamEvents(t, p, t.Context())
		if len(events) != 1 || events[0].EventType() != EventError || result.StopReason != StopReasonError || result.ErrorMessage != "boom" {
			t.Fatalf("result=%#v events=%v", result, fauxUpstreamEventTypes(events))
		}
	})
	// .upstream/v0.87.1/packages/ai/test/faux-provider.test.ts:196
	t.Run("rejects a queued response without a terminal stop reason", func(t *testing.T) {
		p := NewFauxProvider(FauxConfig{})
		p.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText("partial")}, StopReason: "pending"})})
		result, events := fauxUpstreamEvents(t, p, t.Context())
		if result.StopReason != StopReasonError || result.ErrorMessage != "Faux response ended without a stop reason" || slices.Contains(fauxUpstreamEventTypes(events), EventDone) {
			t.Fatalf("result=%#v events=%v", result, fauxUpstreamEventTypes(events))
		}
	})
	// .upstream/v0.87.1/packages/ai/test/faux-provider.test.ts:214
	t.Run("estimates prompt and output tokens from serialized context", func(t *testing.T) {
		p := NewFauxProvider(FauxConfig{})
		p.SetResponses([]FauxResponseStep{fauxUpstreamText("done")})
		tool := ToolSchema{Name: "echo", Description: "Echo back text", Parameters: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}}}
		request := NormalizeContext(Context{SystemPrompt: "sys", Messages: []Message{UserMessage{Content: UserContentBlocks{TextContent{Text: "hello"}, ImageContent{MimeType: "image/png", Data: "abcd"}}}, AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "prior"}}}, ToolResultMessage{ToolCallID: "tool-1", ToolName: "echo", Content: []ToolResultMessageContent{TextContent{Text: "tool out"}}}}, Tools: []ToolSchema{tool}})
		result := fauxUpstreamComplete(t, p, request, StreamOptions{})
		prompt := "system:sys\n\nuser:hello\n[image:image/png:4]\n\nassistant:prior\n\ntoolResult:echo\ntool out\n\ntools:" + SafeJsonStringify([]ToolSchema{tool})
		expected := (len(prompt) + 3) / 4
		if result.Usage.Input != expected || result.Usage.Output != 1 || result.Usage.CacheRead != 0 || result.Usage.CacheWrite != 0 || result.Usage.TotalTokens != expected+1 {
			t.Fatalf("usage=%#v want input=%d output=1", result.Usage, expected)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/faux-provider.test.ts:266,299,327
	for _, mode := range []string{"does not share cache across sessions or requests without sessionId", "simulates prompt caching per sessionId", "does not simulate caching when cacheRetention is none"} {
		t.Run(mode, func(t *testing.T) {
			p := NewFauxProvider(FauxConfig{})
			p.SetResponses([]FauxResponseStep{fauxUpstreamText("first"), fauxUpstreamText("second"), fauxUpstreamText("third")})
			request := Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}
			if mode == "simulates prompt caching per sessionId" {
				request.SystemPrompt = "Be concise."
			}
			retention := CacheRetentionShort
			if mode == "does not simulate caching when cacheRetention is none" {
				retention = CacheRetentionNone
			}
			first := fauxUpstreamComplete(t, p, NormalizeContext(request), StreamOptions{SessionID: "session-1", CacheRetention: retention})
			if retention != CacheRetentionNone && (first.Usage.CacheRead != 0 || first.Usage.CacheWrite <= 0) {
				t.Fatal(first.Usage)
			}
			request.Messages = append(request.Messages, *first, UserMessage{Content: UserText("follow up")})
			session := "session-1"
			if mode == "does not share cache across sessions or requests without sessionId" {
				session = "session-2"
			}
			second := fauxUpstreamComplete(t, p, NormalizeContext(request), StreamOptions{SessionID: session, CacheRetention: retention})
			switch mode {
			case "simulates prompt caching per sessionId":
				if second.Usage.CacheRead <= 0 || second.Usage.Input+second.Usage.CacheRead <= second.Usage.Input {
					t.Fatal(second.Usage)
				}
			case "does not simulate caching when cacheRetention is none":
				if second.Usage.CacheRead != 0 || second.Usage.CacheWrite != 0 {
					t.Fatal(second.Usage)
				}
			default:
				if second.Usage.CacheRead != 0 || second.Usage.CacheWrite <= 0 {
					t.Fatal(second.Usage)
				}
				third := fauxUpstreamComplete(t, p, NormalizeContext(request), StreamOptions{})
				if third.Usage.CacheRead != 0 || third.Usage.CacheWrite != 0 {
					t.Fatal(third.Usage)
				}
			}
		})
	}
	// .upstream/v1.1.0/packages/ai/test/faux-provider.test.ts:327
	t.Run("counts cached characters up to the first difference in the joined prompt", func(t *testing.T) {
		p := NewFauxProvider(FauxConfig{})
		p.SetResponses([]FauxResponseStep{fauxUpstreamText("a"), fauxUpstreamText("b"), fauxUpstreamText("c")})
		options := StreamOptions{SessionID: "session-1", CacheRetention: CacheRetentionShort}
		user := func(content string) Message { return UserMessage{Content: UserText(content), Timestamp: 1} }
		fauxUpstreamComplete(t, p, NormalizeContext(Context{Messages: []Message{user("hello world")}}), options)
		// Prompt texts: "user:hello world" (16 characters), then 16 + 2 + "user:next" (9) = 27.
		extended := fauxUpstreamComplete(t, p, NormalizeContext(Context{Messages: []Message{user("hello world"), user("next")}}), options)
		if u := extended.Usage; u.Input != 3 || u.CacheRead != 4 || u.CacheWrite != 3 {
			t.Fatalf("extended usage = %+v, want input 3, cacheRead 4, cacheWrite 3", u)
		}
		// The first message now differs after "user:hello w" (12 characters): "user:hello wide" + 2 + 9 = 26.
		edited := fauxUpstreamComplete(t, p, NormalizeContext(Context{Messages: []Message{user("hello wide"), user("next")}}), options)
		if u := edited.Usage; u.Input != 4 || u.CacheRead != 3 || u.CacheWrite != 4 {
			t.Fatalf("edited usage = %+v, want input 4, cacheRead 3, cacheWrite 4", u)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/faux-provider.test.ts:347
	t.Run("streams thinking text and partial tool call deltas", func(t *testing.T) {
		p := NewFauxProvider(FauxConfig{})
		p.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxThinking("thinking text"), FauxText("answer text"), FauxToolCall("echo", map[string]any{"text": "hi", "count": 12}, &FauxToolCallOptions{ID: "tool-1"})}, StopReason: "toolUse"})})
		_, events := fauxUpstreamEvents(t, p, t.Context())
		types := fauxUpstreamEventTypes(events)
		for _, typ := range []AssistantEventType{EventThinkingStart, EventThinkingDelta, EventTextStart, EventTextDelta, EventToolCallStart, EventToolCallDelta, EventToolCallEnd} {
			if !slices.Contains(types, typ) {
				t.Fatal(types)
			}
		}
		deltas := ""
		count := 0
		for _, event := range events {
			if event, ok := event.(ToolCallDeltaEvent); ok {
				deltas += event.Delta
				count++
			}
		}
		if count <= 1 || !reflect.DeepEqual(parseStreamingJsonObject(deltas), JsonObject{"text": "hi", "count": float64(12)}) {
			t.Fatalf("deltas=%q count=%d", deltas, count)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/faux-provider.test.ts:382
	t.Run("streams an exact event order for fixed-size chunks", func(t *testing.T) {
		p := NewFauxProvider(FauxConfig{TokenSize: &FauxTokenSize{Min: new(1), Max: new(1)}})
		p.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxThinking("go"), FauxText("ok"), FauxToolCall("echo", map[string]any{}, &FauxToolCallOptions{ID: "tool-1"})}, StopReason: "toolUse"})})
		_, events := fauxUpstreamEvents(t, p, t.Context())
		want := []AssistantEventType{EventStart, EventThinkingStart, EventThinkingDelta, EventThinkingEnd, EventTextStart, EventTextDelta, EventTextEnd, EventToolCallStart, EventToolCallDelta, EventToolCallEnd, EventDone}
		if !reflect.DeepEqual(fauxUpstreamEventTypes(events), want) || events[0].(StartEvent).Partial.Observe().StopReason != StopReasonPending {
			t.Fatal(fauxUpstreamEventTypes(events))
		}
	})
	// .upstream/v0.87.1/packages/ai/test/faux-provider.test.ts:411
	t.Run("streams multiple tool calls in one message", func(t *testing.T) {
		p := NewFauxProvider(FauxConfig{})
		p.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxToolCall("echo", map[string]any{"text": "one"}, &FauxToolCallOptions{ID: "tool-1"}), FauxToolCall("echo", map[string]any{"text": "two"}, &FauxToolCallOptions{ID: "tool-2"})}, StopReason: "toolUse"})})
		_, events := fauxUpstreamEvents(t, p, t.Context())
		starts, ends := 0, 0
		for _, event := range events {
			if event.EventType() == EventToolCallStart {
				starts++
			}
			if event.EventType() == EventToolCallEnd {
				ends++
			}
		}
		if starts != 2 || ends != 2 {
			t.Fatal(starts, ends)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/faux-provider.test.ts:432,457
	for _, reason := range []StopReason{StopReasonError, StopReasonAborted} {
		t.Run("streams an explicit assistant "+string(reason)+" message as a terminal error", func(t *testing.T) {
			message := "upstream failed"
			if reason == StopReasonAborted {
				message = "Request was aborted"
			}
			p := NewFauxProvider(FauxConfig{TokenSize: &FauxTokenSize{Min: new(2), Max: new(2)}})
			p.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText("partial")}, StopReason: string(reason), ErrorMessage: message})})
			result, events := fauxUpstreamEvents(t, p, t.Context())
			if !reflect.DeepEqual(fauxUpstreamEventTypes(events), []AssistantEventType{EventStart, EventTextStart, EventTextDelta, EventTextEnd, EventError}) || result.StopReason != reason || result.ErrorMessage != message || events[len(events)-1].(ErrorEvent).Reason != reason {
				t.Fatalf("result=%#v events=%v", result, fauxUpstreamEventTypes(events))
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/faux-provider.test.ts:482
	t.Run("supports aborting before the first chunk", func(t *testing.T) {
		p := NewFauxProvider(FauxConfig{TokensPerSecond: 50, TokenSize: &FauxTokenSize{Min: new(3), Max: new(3)}})
		p.SetResponses([]FauxResponseStep{fauxUpstreamText("abcdefghijklmnopqrstuvwxyz")})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		result, events := fauxUpstreamEvents(t, p, ctx)
		if len(events) != 1 || events[0].EventType() != EventError || events[0].(ErrorEvent).Reason != StopReasonAborted || result.StopReason != StopReasonAborted {
			t.Fatal(result, events)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/faux-provider.test.ts:505,533,566
	for _, kind := range []string{"text", "thinking", "toolcall"} {
		t.Run("supports aborting mid-"+kind+" stream when paced", func(t *testing.T) {
			p := NewFauxProvider(FauxConfig{TokensPerSecond: 100, TokenSize: &FauxTokenSize{Min: new(3), Max: new(3)}})
			block := FauxText("abcdefghijklmnopqrstuvwxyz")
			start, delta, end := EventTextStart, EventTextDelta, EventTextEnd
			reason := "stop"
			if kind == "thinking" {
				block = FauxThinking("abcdefghijklmnopqrstuvwxyz")
				start, delta, end = EventThinkingStart, EventThinkingDelta, EventThinkingEnd
			}
			if kind == "toolcall" {
				block = FauxToolCall("echo", map[string]any{"text": "abcdefghijklmnopqrstuvwxyz", "count": 123456789}, &FauxToolCallOptions{ID: "tool-1"})
				start, delta, end = EventToolCallStart, EventToolCallDelta, EventToolCallEnd
				reason = "toolUse"
			}
			p.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{block}, StopReason: reason})})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			stream, err := p.Stream(ctx, fauxUpstreamRequest(), StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			types := []AssistantEventType{}
			for event := range stream.Events(t.Context()) {
				types = append(types, event.EventType())
				if event.EventType() == delta {
					count++
					cancel()
				}
			}
			if count != 1 || !slices.Contains(types, start) || !slices.Contains(types, delta) || !slices.Contains(types, EventError) || slices.Contains(types, end) {
				t.Fatalf("count=%d types=%v", count, types)
			}
		})
	}
	// packages/ai/test/faux-provider.test.ts:607; packages/ai/src/providers/faux.ts:143 unregister
	t.Run("unregisters the provider", func(t *testing.T) {
		p := NewFauxProvider(FauxConfig{})
		p.SetResponses([]FauxResponseStep{fauxUpstreamText("hello")})
		p.Unregister()
		_, err := p.GetModel().Provider.Stream(t.Context(), fauxUpstreamRequest(), StreamOptions{})
		if err == nil || !strings.Contains(err.Error(), "No API provider registered for api: "+string(p.cfg.API)) {
			t.Fatal(err)
		}
	})
}

// .upstream/v1.1.0/packages/ai/test/faux-provider.test.ts:327 "counts cached characters up to the first difference in the joined prompt":
// the cached prefix is measured in characters of the joined prompt texts, so a first message edited after "user:hello w" keeps
// only those 12 characters cached (4 and 3 tokens at the faux provider's 4-characters-per-token estimate).
func TestFauxProviderCountsCachedCharactersUpToTheFirstDifference(t *testing.T) {
	p := NewFauxProvider(FauxConfig{})
	p.SetResponses([]FauxResponseStep{fauxUpstreamText("a"), fauxUpstreamText("b"), fauxUpstreamText("c")})
	options := StreamOptions{SessionID: "session-1", CacheRetention: CacheRetentionShort}
	user := func(content string) Message { return UserMessage{Content: UserText(content), Timestamp: 1} }
	complete := func(messages ...Message) *AssistantMessage {
		return fauxUpstreamComplete(t, p, NormalizeContext(Context{Messages: messages}), options)
	}

	// Prompt texts: "user:hello world" (16 characters), then 16 + 2 + "user:next" (9) = 27.
	complete(user("hello world"))
	extended := complete(user("hello world"), user("next"))
	if extended.Usage.Input != 3 || extended.Usage.CacheRead != 4 || extended.Usage.CacheWrite != 3 {
		t.Fatalf("extended usage = %+v, want input 3, cacheRead 4, cacheWrite 3", extended.Usage)
	}
	// The first message now differs after "user:hello w" (12 characters): "user:hello wide" + 2 + 9 = 26.
	edited := complete(user("hello wide"), user("next"))
	if edited.Usage.Input != 4 || edited.Usage.CacheRead != 3 || edited.Usage.CacheWrite != 4 {
		t.Fatalf("edited usage = %+v, want input 4, cacheRead 3, cacheWrite 4", edited.Usage)
	}
}

// faux.ts commonPromptPrefixLength counts characters, and the cache read and write round up like Math.ceil(chars / 4): a
// 13-character shared prefix of "user:hello world" and "user:hello wolf" (15 characters) reads 4 tokens and writes 1.
func TestFauxProviderRoundsCachedCharactersUp(t *testing.T) {
	p := NewFauxProvider(FauxConfig{})
	p.SetResponses([]FauxResponseStep{fauxUpstreamText("a"), fauxUpstreamText("b")})
	options := StreamOptions{SessionID: "session-1", CacheRetention: CacheRetentionShort}
	user := func(content string) Message { return UserMessage{Content: UserText(content), Timestamp: 1} }
	complete := func(message Message) *AssistantMessage {
		return fauxUpstreamComplete(t, p, NormalizeContext(Context{Messages: []Message{message}}), options)
	}
	complete(user("hello world"))
	next := complete(user("hello wolf"))
	if next.Usage.CacheRead != 4 || next.Usage.CacheWrite != 1 || next.Usage.Input != 0 {
		t.Fatalf("usage = %+v, want cacheRead 4, cacheWrite 1, input 0", next.Usage)
	}
}
