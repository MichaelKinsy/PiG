package ai

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// Observing the retained content reference must preserve argument values, including null, rather than normalizing the caller's JSON representation. Upstream agent-loop.ts:416-438 retains the provider's content values.
func TestAssistantMessageObservationPreservesToolArgumentValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		args JsonObject
	}{
		{"null", nil},
		{"empty", JsonObject{}},
		{"numeric", JsonObject{"integer": 42, "unsigned": uint64(42), "pointerInteger": uintptr(7), "number": float64(42), "float32": float32(0.5), "exact": json.Number("9007199254740993"), "offset": nil, "nested": map[string]any{"items": []any{float64(7), true}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := &AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: "call", Name: "read", Arguments: tc.args}}}
			cell := newAssistantMessageCell(message)
			for _, view := range []*AssistantMessage{message, cell.view(), cell.view().ShallowCopy()} {
				observed := view.Observe().Content[0].(ToolCall).Arguments
				if !reflect.DeepEqual(observed, tc.args) {
					t.Fatalf("observed arguments = %#v, want %#v", observed, tc.args)
				}
				if nested, ok := observed["nested"].(map[string]any); ok {
					nested["items"].([]any)[0] = "changed"
					if got := tc.args["nested"].(map[string]any)["items"].([]any)[0]; got != float64(7) {
						t.Fatalf("observation aliases argument values: %#v", got)
					}
				}
			}
		})
	}
}

type observationJSONMarshaler struct {
	calls  *int
	mutate func()
}

func (value observationJSONMarshaler) MarshalJSON() ([]byte, error) {
	*value.calls++
	if value.mutate != nil {
		value.mutate()
	}
	return json.Marshal(*value.calls)
}

func TestJSONCloneInvokesMarshalerOnce(t *testing.T) {
	t.Parallel()
	calls := 0
	copy := cloneJSONValue(map[string]any{"custom": observationJSONMarshaler{calls: &calls}}).(map[string]any)
	if calls != 1 || copy["custom"] != json.Number("1") {
		t.Fatalf("copy = %#v, marshal calls = %d; want number 1 from one call", copy, calls)
	}
}

func TestJSONCloneRetainsSnapshotWhenMarshalerMutatesInput(t *testing.T) {
	t.Parallel()
	calls := 0
	value := map[string]any{"aNumber": 1, "bObject": map[string]any{"n": 1}, "cDeleted": true}
	value["zCustom"] = observationJSONMarshaler{calls: &calls, mutate: func() {
		value["aNumber"] = 2
		value["bObject"] = 2
		delete(value, "cDeleted")
		value["added"] = true
	}}
	want := map[string]any{"aNumber": json.Number("1"), "bObject": map[string]any{"n": json.Number("1")}, "cDeleted": true, "zCustom": json.Number("1")}
	if got := cloneJSONValue(value); !reflect.DeepEqual(got, want) || calls != 1 {
		t.Fatalf("copy = %#v, marshal calls = %d; want captured %#v from one call", got, calls, want)
	}
}

func TestJSONClonePreservesNormalizedKeyCollisions(t *testing.T) {
	t.Parallel()
	value := map[string]any{"\xff": float64(1), "\ufffd": float64(2)}
	want := map[string]any{"\ufffd": json.Number("1")}
	if got := cloneJSONValue(value); !reflect.DeepEqual(got, want) {
		t.Fatalf("copy = %#v, want JSON key replacement and last value %#v", got, want)
	}
}

func TestMessageClonePreservesOptionalJSONValues(t *testing.T) {
	t.Parallel()
	for _, value := range []any{nil, map[string]any(nil), []any(nil), map[string]any{"number": float64(42), "null": nil}} {
		tool := ToolResultMessage{Details: value}
		toolCopy := tool.cloneMessage().(ToolResultMessage)
		assistant := AssistantMessage{Deferred: &DeferredHandle{Data: value}}
		assistantCopy := assistant.Observe()
		for _, copied := range []any{toolCopy.Details, assistantCopy.Deferred.Data} {
			if !reflect.DeepEqual(copied, value) {
				t.Errorf("copied optional JSON value = %#v, want %#v", copied, value)
			}
		}
		if got, want := observationJSON(t, toolCopy), observationJSON(t, tool); got != want {
			t.Errorf("copied details changed presence or value: %s, want %s", got, want)
		}
	}
}

// Owned snapshots must not alias optional numbers in Pi's Usage object (packages/ai/src/types.ts:455-478).
func TestAssistantMessageCloneOwnsNestedValues(t *testing.T) {
	t.Parallel()
	message := observationTestMessage()
	before := observationJSON(t, message)
	copy := message.cloneMessage().(AssistantMessage)
	mutateObservationSnapshot(&copy)
	if got := observationJSON(t, message); got != before {
		t.Fatalf("mutating clone changed original\ngot  %s\nwant %s", got, before)
	}
}

func TestToolResultMessageCloneOwnsUsage(t *testing.T) {
	t.Parallel()
	message := ToolResultMessage{Usage: &Usage{CacheWrite1h: new(3), Reasoning: new(4)}}
	copy := message.cloneMessage().(ToolResultMessage)
	*copy.Usage.CacheWrite1h, *copy.Usage.Reasoning = 30, 40
	if *message.Usage.CacheWrite1h != 3 || *message.Usage.Reasoning != 4 {
		t.Fatalf("cloned tool result aliases usage: %#v", message.Usage)
	}
}

// upstream: packages/ai/src/utils/event-stream.ts:44-91 retains the partial through completion independently of iteration.
func TestAssistantMessageFullViewAdvances(t *testing.T) {
	t.Parallel()
	for _, initial := range [][]AssistantContentBlock{nil, {}, {TextContent{Text: "first"}}} {
		t.Run(fmt.Sprintf("blocks-%d-nil-%t", len(initial), initial == nil), func(t *testing.T) {
			message := &AssistantMessage{Content: initial, StopReason: StopReasonPending}
			cell := newAssistantMessageCell(message)
			view := cell.view()
			before := view.Observe()
			message.Content = []AssistantContentBlock{TextContent{Text: "final"}, ThinkingContent{Thinking: "reason"}, ToolCall{Arguments: JsonObject{"path": "file"}}}
			message.StopReason = StopReasonToolUse
			cell.publish(message, assistantMessageReplacements{})
			for range 2 {
				if got := observationJSON(t, view); got != observationJSON(t, message) {
					t.Fatalf("held full view did not advance: %s", got)
				}
			}
			if before.StopReason != StopReasonPending || !reflect.DeepEqual(before.Content, initial) {
				t.Fatalf("observation changed after publication: %#v", before)
			}
			if view.StopReason != StopReasonPending || !reflect.DeepEqual(view.Content, initial) {
				t.Fatalf("publication mutated public fields: %#v", view)
			}
			owned := view.cloneMessage().(AssistantMessage)
			message.Content[0] = TextContent{Text: "later"}
			cell.publish(message, assistantMessageReplacements{})
			if owned.Content[0].(TextContent).Text != "final" {
				t.Fatal("clone retained the live view")
			}
		})
	}
}

func TestAssistantMessageCellReusesFullView(t *testing.T) {
	t.Parallel()
	message := &AssistantMessage{Content: []AssistantContentBlock{}, StopReason: StopReasonPending}
	cell := newAssistantMessageCell(message)
	view := cell.view()
	for i := range 200 {
		message.Content = []AssistantContentBlock{TextContent{Text: fmt.Sprint(i)}}
		cell.publish(message, assistantMessageReplacements{})
		if got := cell.view(); got != view {
			t.Fatal("a queued view would retain a new field snapshot for each publication")
		}
	}
	if got := view.Observe().Content[0].(TextContent).Text; got != "199" {
		t.Fatalf("stable handle did not advance: %s", got)
	}
}

// upstream: packages/agent/src/agent-loop.ts:416-438 spreads scalars but retains content and usage identities.
func TestAssistantMessageShallowViewPinsScalars(t *testing.T) {
	t.Parallel()
	message := &AssistantMessage{Content: []AssistantContentBlock{}, StopReason: StopReasonPending, ResponseID: "first", EndTurn: new(false)}
	cell := newAssistantMessageCell(message)
	view := cell.view()
	shallow := view.ShallowCopy()
	message.Content = append(message.Content, TextContent{Text: "final"}, ThinkingContent{Thinking: "reason"})
	message.StopReason, message.ResponseID = StopReasonStop, "last"
	*message.EndTurn = true
	message.Usage.Input = 10
	cell.publish(message, assistantMessageReplacements{})
	want := *message
	want.StopReason, want.ResponseID, want.EndTurn = StopReasonPending, "first", new(false)
	for _, held := range []*AssistantMessage{shallow, shallow.ShallowCopy()} {
		if got := observationJSON(t, held); got != observationJSON(t, &want) {
			t.Fatalf("shallow view lost copy boundary\ngot  %s\nwant %s", got, observationJSON(t, &want))
		}
	}
	if got := view.ShallowCopy().Observe(); got.StopReason != StopReasonStop || got.ResponseID != "last" {
		t.Fatalf("new shallow copy used stale scalars: %#v", got)
	}
}

// upstream: packages/ai/src/api/openai-completions.ts:563,572 and openai-responses-shared.ts:565 replace usage, not its fields.
func TestAssistantMessageShallowUsageReplacementAndMutation(t *testing.T) {
	t.Parallel()
	message := &AssistantMessage{Usage: Usage{Input: 1, Reasoning: new(2)}}
	cell := newAssistantMessageCell(message)
	full := cell.view()
	old := full.ShallowCopy()
	message.Usage.Input, *message.Usage.Reasoning = 3, 4
	cell.publish(message, assistantMessageReplacements{})
	if got := old.Observe().Usage; got.Input != 3 || *got.Reasoning != 4 {
		t.Fatalf("mutation did not reach shallow view: %#v", got)
	}
	message.Usage = Usage{Input: 5, Reasoning: new(6)}
	cell.publish(message, assistantMessageReplacements{Usage: true})
	newer := full.ShallowCopy()
	message.Usage.Input, *message.Usage.Reasoning = 7, 8
	cell.publish(message, assistantMessageReplacements{})
	for _, held := range []*AssistantMessage{old, old.ShallowCopy()} {
		if got := held.Observe().Usage; got.Input != 3 || *got.Reasoning != 4 {
			t.Fatalf("replacement revised an old usage object: %#v", got)
		}
	}
	for _, held := range []*AssistantMessage{full, newer} {
		if got := held.Observe().Usage; got.Input != 7 || *got.Reasoning != 8 {
			t.Fatalf("new usage object did not advance: %#v", got)
		}
	}
}

func TestAssistantMessageNestedReplacement(t *testing.T) {
	t.Parallel()
	message := observationTestMessage()
	cell := newAssistantMessageCell(message)
	full := cell.view()
	old := full.ShallowCopy()
	mutateObservationSnapshot(message)
	cell.publish(message, assistantMessageReplacements{})
	want := *message
	want.EndTurn = new(false)
	before := observationJSON(t, &want)
	if got := observationJSON(t, old); got != before {
		t.Fatalf("nested mutation did not reach shallow view\ngot  %s\nwant %s", got, before)
	}
	message.Content = []AssistantContentBlock{TextContent{Text: "replacement"}}
	message.Diagnostics = []AssistantMessageDiagnostic{{Type: "replacement"}}
	message.Deferred = &DeferredHandle{ID: "replacement"}
	message.Usage = Usage{Input: 100}
	cell.publish(message, assistantMessageReplacements{Content: true, Diagnostics: true, Deferred: true, Usage: true})
	if got := observationJSON(t, old); got != before {
		t.Fatalf("replacement changed retained nested objects\ngot  %s\nwant %s", got, before)
	}
	if got := observationJSON(t, full); got != observationJSON(t, message) {
		t.Fatalf("full view did not see replacement: %s", got)
	}
}

func TestAssistantMessageAbsentNestedValuesStayAbsent(t *testing.T) {
	t.Parallel()
	message := &AssistantMessage{Content: []AssistantContentBlock{}}
	cell := newAssistantMessageCell(message)
	old := cell.view().ShallowCopy()
	message.Diagnostics = []AssistantMessageDiagnostic{{Type: "added"}}
	message.Deferred = &DeferredHandle{ID: "added"}
	cell.publish(message, assistantMessageReplacements{})
	if got := old.Observe(); got.Diagnostics != nil || got.Deferred != nil {
		t.Fatalf("shallow copy acquired previously absent properties: %#v", got)
	}
	present := cell.view().ShallowCopy()
	message.Diagnostics, message.Deferred = nil, nil
	cell.publish(message, assistantMessageReplacements{})
	if got := present.Observe(); len(got.Diagnostics) != 1 || got.Deferred == nil || got.Deferred.ID != "added" {
		t.Fatalf("removal revised the old objects: %#v", got)
	}
}

func TestAssistantMessageObservationsAreIndependent(t *testing.T) {
	t.Parallel()
	message := observationTestMessage()
	cell := newAssistantMessageCell(message)
	for _, view := range []*AssistantMessage{message, cell.view(), cell.view().ShallowCopy()} {
		before := observationJSON(t, view)
		one, two := view.Observe(), view.Observe()
		mutateObservationSnapshot(one)
		for _, unchanged := range []*AssistantMessage{two, view} {
			if got := observationJSON(t, unchanged); got != before {
				t.Fatalf("observation aliases nested state\ngot  %s\nwant %s", got, before)
			}
		}
	}
	view := cell.view()
	before := observationJSON(t, view)
	mutateObservationSnapshot(message)
	if got := observationJSON(t, view); got != before {
		t.Fatalf("unpublished producer mutation escaped: %s", got)
	}
}

func TestAssistantMessageOrdinaryShallowCopy(t *testing.T) {
	t.Parallel()
	message := observationTestMessage()
	copy := message.ShallowCopy()
	copy.StopReason = StopReasonStop
	copy.Content[2].(ToolCall).Arguments["added"] = true
	if message.StopReason != StopReasonPending || message.Content[2].(ToolCall).Arguments["added"] != true {
		t.Fatal("ordinary shallow copy did not copy scalars and share nested data")
	}
	var absent *AssistantMessage
	if absent.Observe() != nil || absent.ShallowCopy() != nil {
		t.Fatal("nil message became non-nil")
	}
}

func TestAssistantMessageConcurrentPublicationObservation(t *testing.T) {
	t.Parallel()
	message := observationTestMessage()
	cell := newAssistantMessageCell(message)
	full := cell.view()
	shallow := full.ShallowCopy()
	var workers sync.WaitGroup
	start := make(chan struct{})
	workers.Go(func() {
		<-start
		for i := range 200 {
			message.Content[0] = TextContent{Text: fmt.Sprint(i)}
			message.Usage.Input, *message.Usage.Reasoning = i, i
			cell.publish(message, assistantMessageReplacements{})
		}
	})
	for _, held := range []*AssistantMessage{full, shallow} {
		workers.Go(func() {
			<-start
			for range 200 {
				for _, observed := range []*AssistantMessage{held.Observe(), held.ShallowCopy().Observe()} {
					if got := observed.Content[0].(TextContent).Text; got != "first" && got != fmt.Sprint(observed.Usage.Input) {
						t.Errorf("torn publication: content %s, input %d", got, observed.Usage.Input)
					}
					mutateObservationSnapshot(observed)
				}
				if got := cell.view(); got != full {
					t.Error("concurrent caller received a different full view")
				}
				if _, err := json.Marshal(held); err != nil {
					t.Error(err)
				}
				if held.Content[0].(TextContent).Text != "first" || held.Usage.Input != 2 {
					t.Error("producer wrote delivered public fields")
				}
			}
		})
	}
	close(start)
	workers.Wait()
}

func observationTestMessage() *AssistantMessage {
	return &AssistantMessage{
		API: APIOpenAICompletions, Provider: "test", Model: "test-model", StopReason: StopReasonPending,
		Content: []AssistantContentBlock{
			TextContent{Text: "first"},
			ThinkingContent{Thinking: "reason", ThinkingSignature: "signature"},
			ToolCall{ID: "call", Name: "read", Arguments: JsonObject{"nested": map[string]any{"path": "first"}}},
		},
		Usage: Usage{Input: 2, CacheWrite1h: new(3), Reasoning: new(4)},
		Diagnostics: []AssistantMessageDiagnostic{{
			Type: "test", Error: &DiagnosticErrorInfo{Message: "error", Code: map[string]any{"code": "first"}},
			Details: map[string]any{"detail": []any{"first"}},
		}},
		Deferred: &DeferredHandle{ID: "deferred", Data: map[string]any{"id": "first"}, ExpiresAt: new(int64(10)), PollAfterMS: new(int64(20))},
		EndTurn:  new(false), Timestamp: 1,
	}
}

func mutateObservationSnapshot(message *AssistantMessage) {
	message.Content[0] = TextContent{Text: "changed"}
	message.Content[2].(ToolCall).Arguments["nested"].(map[string]any)["path"] = "changed"
	*message.Usage.CacheWrite1h = 30
	*message.Usage.Reasoning = 40
	message.Diagnostics[0].Error.Code.(map[string]any)["code"] = "changed"
	message.Diagnostics[0].Details["detail"].([]any)[0] = "changed"
	message.Deferred.Data.(map[string]any)["id"] = "changed"
	*message.Deferred.ExpiresAt = 100
	*message.Deferred.PollAfterMS = 200
	*message.EndTurn = true
}

func observationJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
