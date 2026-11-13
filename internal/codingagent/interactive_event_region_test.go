package codingagent

import (
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// collectedEventNames names the events a task's region holds, a message_update by the provider event it carries.
func collectedEventNames(events []agent.AgentEvent) []string {
	names := make([]string, 0, len(events))
	for _, ev := range events {
		switch e := ev.(type) {
		case agent.MessageUpdateEvent:
			switch event := e.AssistantMessageEvent.(type) {
			case ai.TextDeltaEvent:
				names = append(names, "text_delta:"+event.Delta)
			case ai.ToolCallDeltaEvent:
				names = append(names, fmt.Sprintf("toolcall_delta[%d]:%s", event.ContentIndex, event.Delta))
			default:
				names = append(names, string(event.EventType()))
			}
		default:
			names = append(names, fmt.Sprintf("%T", ev))
		}
	}
	return names
}

// A task holds one message_update for each run of updates of one part of the message, the last of the run: the response that arrives in
// one piece is hundreds of them, and each is applied from what it carries alone. Updates of different tool calls and the events between
// updates stay, in order.
func TestCollectEventKeepsTheLastUpdateOfEachRunForOnePartOfTheMessage(t *testing.T) {
	update := func(event ai.AssistantMessageEvent) agent.AgentEvent {
		return agent.MessageUpdateEvent{AssistantMessageEvent: event}
	}
	mode := &InteractiveMode{}
	for _, ev := range []agent.AgentEvent{
		// A tool call at content index 0 is the first event of a mode that collected nothing yet.
		update(ai.ToolCallStartEvent{ContentIndex: 0}),
		update(ai.ToolCallDeltaEvent{ContentIndex: 0, Delta: "a"}),
		agent.MessageStartEvent{},
		update(ai.TextStartEvent{}),
		update(ai.TextDeltaEvent{Delta: "one"}),
		update(ai.TextDeltaEvent{Delta: "two"}),
		update(ai.ThinkingDeltaEvent{}),
		update(ai.TextEndEvent{}),
		update(ai.ToolCallStartEvent{ContentIndex: 1}),
		update(ai.ToolCallDeltaEvent{ContentIndex: 1, Delta: "b"}),
		update(ai.ToolCallEndEvent{ContentIndex: 1}),
		update(ai.ToolCallStartEvent{ContentIndex: 2}),
		update(ai.ToolCallDeltaEvent{ContentIndex: 2, Delta: "c"}),
		// The update of another tool call between two of one keeps all three.
		update(ai.ToolCallDeltaEvent{ContentIndex: 1, Delta: "d"}),
		update(ai.ToolCallDeltaEvent{ContentIndex: 2, Delta: "e"}),
		update(ai.TextDeltaEvent{Delta: "three"}),
		agent.MessageEndEvent{},
		update(ai.TextDeltaEvent{Delta: "four"}),
		// No block has a negative index: such updates name no part and are all kept.
		update(ai.ToolCallDeltaEvent{ContentIndex: -1, Delta: "f"}),
		update(ai.ToolCallDeltaEvent{ContentIndex: -1, Delta: "g"}),
		update(ai.ToolCallDeltaEvent{ContentIndex: -2, Delta: "h"}),
		update(ai.ToolCallDeltaEvent{ContentIndex: -2, Delta: "i"}),
	} {
		mode.collectEvent(ev)
	}
	want := []string{
		"toolcall_delta[0]:a",
		"agent.MessageStartEvent",
		"text_end",
		"toolcall_end",
		"toolcall_delta[2]:c",
		"toolcall_delta[1]:d",
		"toolcall_delta[2]:e",
		"text_delta:three",
		"agent.MessageEndEvent",
		"text_delta:four",
		"toolcall_delta[-1]:f",
		"toolcall_delta[-1]:g",
		"toolcall_delta[-2]:h",
		"toolcall_delta[-2]:i",
	}
	if got := collectedEventNames(mode.eventRegion); !slices.Equal(got, want) {
		t.Fatalf("collected\n got %q\nwant %q", got, want)
	}

	// The region that was applied starts the next one with nothing to replace.
	mode.eventRegion, mode.eventRegionUpdate = mode.eventRegion[:0], eventRegionNoUpdate
	mode.collectEvent(update(ai.ToolCallDeltaEvent{ContentIndex: -2, Delta: "j"}))
	mode.collectEvent(update(ai.TextDeltaEvent{Delta: "five"}))
	if got, want := collectedEventNames(mode.eventRegion), []string{"toolcall_delta[-2]:j", "text_delta:five"}; !slices.Equal(got, want) {
		t.Fatalf("collected after an applied region\n got %q\nwant %q", got, want)
	}
}
