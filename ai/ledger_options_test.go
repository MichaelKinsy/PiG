package ai

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// packages/ai/src/api/openai-completions.ts:162,510,547 (grammarToolInputProperties): a tool call whose name the map
// holds is replayed as a custom tool call carrying the named input property; without the map the call stays a function
// call. The supplied map replaces the one derived from the transcript's declared tools.
func TestConvertCompletionsMessagesGrammarToolInputPropertiesReplaysACustomToolCall(t *testing.T) {
	transcript := NormalizeContext(Context{Messages: []Message{
		UserMessage{Content: UserText("run"), Timestamp: 1},
		AssistantMessage{API: APIOpenAICompletions, Provider: "custom", Model: "model", StopReason: StopReasonToolUse, Timestamp: 2, Content: []AssistantContentBlock{
			ToolCall{ID: "call_1", Name: "patch", Arguments: JsonObject{"patch": "*** Begin"}},
		}},
	}})
	model := &Model{ID: "model", ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, ProviderID: "custom", BaseURL: "https://example.test/v1"}, Input: []string{"text"}}
	assistantCall := func(options ConvertCompletionsMessagesOptions) oaiRequestToolCall {
		t.Helper()
		converted, err := ConvertCompletionsMessages(model, transcript, nil, options)
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range converted {
			if message.Role == "assistant" && len(message.ToolCalls) == 1 {
				return message.ToolCalls[0]
			}
		}
		t.Fatalf("no assistant tool call in %+v", converted)
		return oaiRequestToolCall{}
	}
	if call := assistantCall(ConvertCompletionsMessagesOptions{}); call.Type != "function" || call.Function == nil || call.Custom != nil {
		t.Errorf("derived map: call = %+v, want a function call", call)
	}
	call := assistantCall(ConvertCompletionsMessagesOptions{GrammarToolInputProperties: map[string]string{"patch": "patch"}})
	if call.Type != "custom" || call.Custom == nil || call.Custom.Name != "patch" || call.Custom.Input != "*** Begin" || call.Function != nil {
		t.Errorf("supplied map: call = %+v, want a custom call with input *** Begin", call)
	}
}

// packages/ai/src/utils/abort-signals.ts:1-12 (CombinedAbortSignal): no signal gives a result with no signal and a
// callable cleanup; one signal is returned as it is.
func TestCombinedAbortSignalHoldsTheSignalAndACallableCleanup(t *testing.T) {
	none := CombineAbortSignals()
	if none.Signal != nil || none.Cleanup == nil {
		t.Fatalf("no signals: %+v, want a nil Signal and a Cleanup", none)
	}
	none.Cleanup()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	one := CombineAbortSignals(nil, ctx)
	if one.Signal != ctx || one.Cleanup == nil {
		t.Fatalf("one signal: Signal is not the supplied context")
	}
	one.Cleanup()
	cause := errors.New("stop")
	many := CombineAbortSignals(ctx, context.Background())
	defer many.Cleanup()
	cancel(cause)
	<-many.Signal.Done()
	if !errors.Is(context.Cause(many.Signal), cause) {
		t.Fatalf("combined cause = %v, want the aborting signal's cause", context.Cause(many.Signal))
	}
}

// packages/ai/src/types.ts:458-459,515 (JsonValue, JsonObject, DeferredHandle.data?: JsonValue): a deferred handle carries any JSON value
// as its provider conversion data, and the value survives the handle's JSON form.
func TestDeferredHandleDataHoldsEveryJsonValueKind(t *testing.T) {
	for name, data := range map[string]JsonValue{
		"boolean": true,
		"number":  1.5,
		"string":  "row-7",
		"array":   []JsonValue{"a", 2.0, nil, []JsonValue{false}},
		"object":  map[string]any{"batch": "b1", "rows": []JsonValue{1.0, JsonObject{"id": "r"}}},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(DeferredHandle{Provider: "p", ModelID: "m", API: APIOpenAIResponses, ID: "id", Data: data})
			if err != nil {
				t.Fatal(err)
			}
			var decoded DeferredHandle
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(data)
			got, _ := json.Marshal(decoded.Data)
			if string(got) != string(want) {
				t.Errorf("data after the round trip = %s, want %s (handle %s)", got, want, encoded)
			}
		})
	}
}
