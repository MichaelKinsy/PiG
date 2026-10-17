package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// observingTool records ToolUpdatesObserved for the callback it was given, before and after its first update, and
// sends two updates either way, so a test sees both what the tool was told and what reached the listeners.
func observingTool(before, after *atomic.Bool, between func()) *scriptTool {
	return &scriptTool{name: "probe", label: "Probe", description: "Probe", params: map[string]any{"type": "object", "properties": map[string]any{}},
		execute: func(ctx context.Context, _ string, _ json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error) {
			before.Store(ToolUpdatesObserved(ctx, onUpdate))
			onUpdate(AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "one"}}, Details: map[string]any{}})
			if between != nil {
				between()
			}
			after.Store(ToolUpdatesObserved(ctx, onUpdate))
			onUpdate(AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "two"}}, Details: map[string]any{}})
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}, Details: map[string]any{}}, nil
		}}
}

func updateTexts(events []AgentEvent) []string {
	var texts []string
	for _, event := range events {
		if update, ok := event.(ToolExecutionUpdateEvent); ok {
			texts = append(texts, update.PartialResult.Text())
		}
	}
	return texts
}

// The flag is false only when nothing the Agent delivers events to could see a tool_execution_update; every listener
// and option that can see one makes it true, and the events those observers receive are unchanged.
func TestToolUpdatesObservedFollowsTheAgentsObservers(t *testing.T) {
	for _, tc := range []struct {
		name string
		// subscribe registers the test's observers and returns where they recorded events (nil when none can see them).
		subscribe func(a *Agent) *[]AgentEvent
		options   func(seen *[]AgentEvent) AgentOptions
		want      bool
	}{
		{name: "no observer", want: false},
		{name: "update-blind listener", want: false, subscribe: func(a *Agent) *[]AgentEvent {
			a.SubscribeObserving(func(context.Context, AgentEvent) error { return nil }, func() bool { return false })
			return nil
		}},
		{name: "Subscribe listener", want: true, subscribe: func(a *Agent) *[]AgentEvent {
			var seen []AgentEvent
			a.Subscribe(func(_ context.Context, event AgentEvent) error { seen = append(seen, event); return nil })
			return &seen
		}},
		{name: "observing listener", want: true, subscribe: func(a *Agent) *[]AgentEvent {
			var seen []AgentEvent
			a.SubscribeObserving(func(_ context.Context, event AgentEvent) error { seen = append(seen, event); return nil }, func() bool { return true })
			return &seen
		}},
		{name: "OnEvent", want: true, options: func(seen *[]AgentEvent) AgentOptions {
			return AgentOptions{OnEvent: func(event AgentEvent) { *seen = append(*seen, event) }}
		}},
		{name: "AfterEvent", want: true, options: func(seen *[]AgentEvent) AgentOptions {
			return AgentOptions{AfterEvent: func(event AgentEvent) error { *seen = append(*seen, event); return nil }}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var before, after atomic.Bool
			provider := &scriptedProvider{respond: toolCallsThenText(toolCall("c", "probe", nil))}
			var optionSeen []AgentEvent
			options := AgentOptions{}
			if tc.options != nil {
				options = tc.options(&optionSeen)
			}
			options.Model, options.Tools = scriptedModel(provider), []AgentTool{observingTool(&before, &after, nil)}
			a := mustNewAgent(options)
			var seen *[]AgentEvent
			if tc.subscribe != nil {
				seen = tc.subscribe(a)
			}
			if tc.options != nil {
				seen = &optionSeen
			}
			mustSend(t, a, "go")
			if before.Load() != tc.want || after.Load() != tc.want {
				t.Fatalf("ToolUpdatesObserved = %v then %v, want %v", before.Load(), after.Load(), tc.want)
			}
			if seen != nil {
				if got := updateTexts(*seen); !reflect.DeepEqual(got, []string{"one", "two"}) {
					t.Fatalf("observer saw updates %v, want both", got)
				}
			}
		})
	}
}

// The predicate is asked again while the tool runs: a listener that starts observing during the execution turns the
// flag true and receives the updates from then on.
func TestToolUpdatesObservedTurnsTrueWhenAnObserverAppearsMidExecution(t *testing.T) {
	var before, after, observing atomic.Bool
	var seen []string
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("c", "probe", nil))}
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{observingTool(&before, &after, func() { observing.Store(true) })}})
	a.SubscribeObserving(func(_ context.Context, event AgentEvent) error {
		if update, ok := event.(ToolExecutionUpdateEvent); ok && observing.Load() {
			seen = append(seen, update.PartialResult.Text())
		}
		return nil
	}, observing.Load)
	mustSend(t, a, "go")
	if before.Load() || !after.Load() {
		t.Fatalf("ToolUpdatesObserved = %v then %v, want false then true", before.Load(), after.Load())
	}
	if !reflect.DeepEqual(seen, []string{"two"}) {
		t.Fatalf("late observer saw %v, want the update sent after it appeared", seen)
	}
}

// A standalone loop's sink is the caller's, so it always observes; RunToolCall without OnUpdate has no receiver; and a
// callback the tool did not get from the loop (a wrapping tool's own) is always observed, whatever the loop's gate says.
func TestToolUpdatesObservedOutsideTheAgent(t *testing.T) {
	var before, after atomic.Bool
	if _, err := RunToolCall(context.Background(), AgentToolCall{ID: "c", Name: "probe", Arguments: ai.JsonObject{}}, RunToolCallOptions{Tools: []AgentTool{observingTool(&before, &after, nil)}}); err != nil {
		t.Fatal(err)
	}
	if before.Load() || after.Load() {
		t.Fatalf("RunToolCall without OnUpdate: ToolUpdatesObserved = %v, %v, want false", before.Load(), after.Load())
	}
	if _, err := RunToolCall(context.Background(), AgentToolCall{ID: "c", Name: "probe", Arguments: ai.JsonObject{}}, RunToolCallOptions{Tools: []AgentTool{observingTool(&before, &after, nil)}, OnUpdate: func(AgentToolResult) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	if !before.Load() || !after.Load() {
		t.Fatal("RunToolCall with OnUpdate: ToolUpdatesObserved false, want true")
	}

	var wrapped atomic.Bool
	inner := &scriptTool{name: "inner", label: "Inner", description: "Inner", params: map[string]any{"type": "object", "properties": map[string]any{}},
		execute: func(ctx context.Context, _ string, _ json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error) {
			wrapped.Store(ToolUpdatesObserved(ctx, onUpdate))
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}, Details: map[string]any{}}, nil
		}}
	outer := &scriptTool{name: "probe", label: "Probe", description: "Probe", params: map[string]any{"type": "object", "properties": map[string]any{}},
		execute: func(ctx context.Context, id string, args json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error) {
			before.Store(ToolUpdatesObserved(ctx, onUpdate))
			return inner.Execute(ctx, id, args, func(AgentToolResult) {})
		}}
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("c", "probe", nil))}
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{outer}})
	mustSend(t, a, "go")
	if before.Load() || !wrapped.Load() {
		t.Fatalf("loop callback observed = %v, wrapping callback observed = %v; want false, true", before.Load(), wrapped.Load())
	}

	var sinkEvents []AgentEvent
	provider = &scriptedProvider{respond: toolCallsThenText(toolCall("c", "probe", nil))}
	if _, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("go")}, AgentContext{Tools: []AgentTool{observingTool(&before, &after, nil)}}, AgentLoopConfig{Model: scriptedModel(provider)}, func(event AgentEvent) error {
		sinkEvents = append(sinkEvents, event)
		return nil
	}, providerStream); err != nil {
		t.Fatal(err)
	}
	if !before.Load() || !after.Load() || !reflect.DeepEqual(updateTexts(sinkEvents), []string{"one", "two"}) {
		t.Fatalf("standalone loop: observed %v, %v; updates %v", before.Load(), after.Load(), updateTexts(sinkEvents))
	}
}
