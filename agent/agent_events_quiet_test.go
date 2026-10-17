package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// quietFauxModel streams thinking, text and a tool call in small paced chunks, so each chunk is pushed from its own
// timer callback: an observer that starts observing between two updates does so between two of the provider's I/O-like
// turns, as any observer Pi can add does.
func quietFauxModel(paced bool) *ai.Model {
	// A fixed chunk size: a range would split two runs differently.
	config := ai.FauxConfig{TokenSize: &ai.FauxTokenSize{Min: new(1), Max: new(1)}}
	if paced {
		config.TokensPerSecond = 4000
	}
	provider := ai.NewFauxProvider(config)
	timestamp := int64(1_700_000_000_000)
	provider.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Timestamp: &timestamp, StopReason: string(ai.StopReasonToolUse), Content: []ai.FauxContentBlock{
			ai.FauxThinking("thinking it over in a few chunks"),
			ai.FauxText("an answer streamed in several chunks"),
			ai.FauxToolCall("probe", map[string]any{"path": "a.txt", "content": "some content to stream"}, &ai.FauxToolCallOptions{ID: "call_quiet"}),
		}}),
		ai.FauxStaticStep(ai.FauxResponse{Timestamp: &timestamp, Content: []ai.FauxContentBlock{ai.FauxText("done")}}),
	})
	return &ai.Model{ID: "faux-1", Provider: provider, ProviderMeta: ai.ProviderMetadata{API: "faux", ProviderID: "faux"}}
}

func quietProbeTool() *scriptTool {
	return &scriptTool{name: "probe", label: "Probe", description: "Probe", params: map[string]any{"type": "object", "properties": map[string]any{}},
		execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, Details: map[string]any{}}, nil
		}}
}

// quietRecord is the JSON of an event as a listener reads it, without the wall-clock fields two runs cannot share.
func quietRecord(t *testing.T, event AgentEvent) string {
	t.Helper()
	var value any
	switch event := event.(type) {
	case MessageUpdateEvent:
		value = map[string]any{"type": "message_update", "message": event.Message, "event": event.AssistantMessageEvent}
	case MessageStartEvent:
		value = map[string]any{"type": "message_start", "message": event.Message}
	case MessageEndEvent:
		value = map[string]any{"type": "message_end", "message": event.Message}
	default:
		value = map[string]any{"type": reflect.TypeOf(event).Name()}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var tree any
	if err := json.Unmarshal(encoded, &tree); err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(withoutRunFields(tree))
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// withoutRunFields drops the members two runs cannot share: user prompts' and tool results' timestamps and responses'
// durationMs, which read the wall clock, and api, which names each faux provider instance apart.
func withoutRunFields(value any) any {
	switch value := value.(type) {
	case map[string]any:
		delete(value, "durationMs")
		delete(value, "api")
		if value["role"] == "user" || value["role"] == "toolResult" {
			delete(value, "timestamp")
		}
		for key, member := range value {
			value[key] = withoutRunFields(member)
		}
	case []any:
		for i, member := range value {
			value[i] = withoutRunFields(member)
		}
	}
	return value
}

// quietRun prompts a fresh Agent with subscribe's listeners and returns the transcript's records.
func quietRun(t *testing.T, paced bool, subscribe func(a *Agent)) []string {
	t.Helper()
	a := mustNewAgent(AgentOptions{Model: quietFauxModel(paced), Tools: []AgentTool{quietProbeTool()}})
	subscribe(a)
	if err := a.Prompt(t.Context(), "go"); err != nil {
		t.Fatal(err)
	}
	var transcript []string
	for _, message := range a.Messages() {
		transcript = append(transcript, quietRecord(t, MessageEndEvent{Message: message}))
	}
	return transcript
}

// A listener that does nothing with message_update receives none while nothing else could observe one, every other
// event reaches it as it reaches a listener that sees everything, and the run's transcript is the same.
func TestUnobservedMessageUpdatesAreNotDelivered(t *testing.T) {
	var all []string
	allTranscript := quietRun(t, false, func(a *Agent) {
		a.Subscribe(func(_ context.Context, event AgentEvent) error { all = append(all, quietRecord(t, event)); return nil })
	})
	var quiet []string
	var asked atomic.Int64
	quietTranscript := quietRun(t, false, func(a *Agent) {
		a.SubscribeObservingUpdates(func(_ context.Context, event AgentEvent) error {
			if _, ok := event.(MessageUpdateEvent); ok {
				t.Error("an unobserved message_update was delivered")
			}
			quiet = append(quiet, quietRecord(t, event))
			return nil
		}, nil, func() bool { asked.Add(1); return false })
	})
	var want []string
	updates := 0
	for _, record := range all {
		var event struct{ Type string }
		_ = json.Unmarshal([]byte(record), &event)
		if event.Type == "message_update" {
			updates++
			continue
		}
		want = append(want, record)
	}
	if updates < 10 || asked.Load() != int64(updates) {
		t.Fatalf("observing run saw %d updates; the quiet listener was asked %d times, want once per update", updates, asked.Load())
	}
	if !reflect.DeepEqual(quiet, want) {
		t.Fatalf("events other than message_update differ:\n quiet %q\n  want %q", quiet, want)
	}
	if !reflect.DeepEqual(quietTranscript, allTranscript) {
		t.Fatalf("transcripts differ:\n quiet %q\n  want %q", quietTranscript, allTranscript)
	}
}

// An observer that starts observing after n updates went undelivered receives every later update exactly as a listener
// that observed all along does, for every n.
func TestMessageUpdateObserverAppearingMidStreamSeesEveryLaterUpdate(t *testing.T) {
	updatesOf := func(records []string) []string {
		var updates []string
		for _, record := range records {
			var event struct{ Type string }
			_ = json.Unmarshal([]byte(record), &event)
			if event.Type == "message_update" {
				updates = append(updates, record)
			}
		}
		return updates
	}
	var all []string
	quietRun(t, true, func(a *Agent) {
		a.Subscribe(func(_ context.Context, event AgentEvent) error { all = append(all, quietRecord(t, event)); return nil })
	})
	want := updatesOf(all)
	if len(want) < 10 {
		t.Fatalf("%d updates, want a streamed response", len(want))
	}
	for _, n := range []int{1, 2, 5, len(want) / 2, len(want) - 1} {
		var seen []string
		var asked atomic.Int64
		quietRun(t, true, func(a *Agent) {
			a.SubscribeObservingUpdates(func(_ context.Context, event AgentEvent) error {
				seen = append(seen, quietRecord(t, event))
				return nil
			}, nil, func() bool { return asked.Add(1) > int64(n) })
		})
		if got := updatesOf(seen); !reflect.DeepEqual(got, want[n:]) {
			t.Fatalf("observing from update %d: updates differ:\n got %q\nwant %q", n, got, want[n:])
		}
	}
}

// StreamingMessage after an undelivered update returns what the update would have set, however far the provider has
// published since.
func TestStreamingMessageAfterAnUnobservedUpdate(t *testing.T) {
	model := quietFauxModel(false)
	stream, err := model.Provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("go")}}}), ai.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := mustNewAgent(AgentOptions{})
	checked := 0
	for observation, event := range stream.ObservePublishedEvents(t.Context()) {
		partial, update := assistantUpdatePartial(event)
		if !update {
			continue
		}
		var capture ai.ShallowCapture
		capture.Capture(partial)
		want := shallowAssistantMessage(partial)
		a.reduceUnobservedUpdate(&capture)
		// The provider runs ahead before the state is read.
		observation.Yield()
		got := a.StreamingMessage()
		if got == nil || !reflect.DeepEqual(*got.Assistant, *want) {
			t.Fatalf("update %d: StreamingMessage = %+v, want %+v", checked, got, want)
		}
		if again := a.StreamingMessage(); !reflect.DeepEqual(again, got) {
			t.Fatalf("update %d: a second read differs: %+v, want %+v", checked, again, got)
		}
		checked++
	}
	if checked < 10 {
		t.Fatalf("%d updates checked, want a streamed response", checked)
	}
}
