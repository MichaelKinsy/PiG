// SPDX-License-Identifier: MIT

package ai

import (
	"slices"
	"testing"
)

// A flushed event owns its Raw lines: later events reuse no storage, and the parse-error message names exactly the lines of the failing event.
func TestAnthropicSSEEventsOwnTheirRawLines(t *testing.T) {
	var state anthropicSSEState
	var events []serverSentEvent
	for _, line := range []string{": keepalive", "event: a", "data: 1", "", "event: b", "data: 2", "data: 3", "", "event: c", "data: 4", ""} {
		if event, ok := state.decode(line); ok {
			events = append(events, event)
		}
	}
	want := [][]string{{": keepalive", "event: a", "data: 1"}, {"event: b", "data: 2", "data: 3"}, {"event: c", "data: 4"}}
	if len(events) != len(want) {
		t.Fatalf("events = %d, want %d", len(events), len(want))
	}
	for i, event := range events {
		if !slices.Equal(event.Raw, want[i]) {
			t.Errorf("event %d Raw = %q, want %q", i, event.Raw, want[i])
		}
	}
	if state.raw != nil {
		t.Errorf("flush left the state holding %q", state.raw)
	}
}
