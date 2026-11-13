// SPDX-License-Identifier: MIT

package ai

import (
	"slices"
	"testing"
)

// A flushed event owns its Raw lines: later events reuse no storage, appending to one event's lines never reaches another's, and the parse-error message names exactly the lines of the failing event. The lines of a record that spans two chunks survive the arena a later chunk reserves.
func TestAnthropicSSEEventsOwnTheirRawLines(t *testing.T) {
	lines := []string{": keepalive", "event: a", "data: 1", "", "event: b", "data: 2", "data: 3", "", "event: c", "data: 4", ""}
	want := [][]string{{": keepalive", "event: a", "data: 1"}, {"event: b", "data: 2", "data: 3"}, {"event: c", "data: 4"}}
	for _, reserve := range []int{0, 2, len(lines)} {
		var state anthropicSSEState
		var events []serverSentEvent
		for start := 0; start < len(lines); start += 3 {
			state.reserveLines(reserve)
			for _, line := range lines[start:min(start+3, len(lines))] {
				if event, ok := state.decode(line); ok {
					events = append(events, event)
				}
			}
		}
		if len(events) != len(want) {
			t.Fatalf("reserve %d: events = %d, want %d", reserve, len(events), len(want))
		}
		for i, event := range events {
			if !slices.Equal(event.Raw, want[i]) {
				t.Errorf("reserve %d: event %d Raw = %q, want %q", reserve, i, event.Raw, want[i])
			}
			if cap(event.Raw) != len(event.Raw) {
				t.Errorf("reserve %d: event %d Raw has room for %d lines beyond its %d", reserve, i, cap(event.Raw)-len(event.Raw), len(event.Raw))
			}
		}
		_ = append(events[0].Raw, "overwritten")
		if !slices.Equal(events[1].Raw, want[1]) {
			t.Errorf("reserve %d: appending to the first event's lines changed the second's: %q", reserve, events[1].Raw)
		}
		if state.rawStart != len(state.arena) {
			t.Errorf("reserve %d: flush left the state holding %q", reserve, state.arena[state.rawStart:])
		}
	}
}
