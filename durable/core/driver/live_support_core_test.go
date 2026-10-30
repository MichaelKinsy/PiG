// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports packages/durable/test/live-support.ts over the core (the harness package's live_support_test.go is its harness-side
// form): what tests of renderers' state read, every pi.live value of a conversation and the results of nested calls. The core
// delivers on its own goroutine, so a test waits (waitFor) until what it reads was delivered.

// liveValue is the pi.live value of a view; an absent document is the empty state.
func liveValue(t testing.TB, view harness.ConversationView) harness.LiveState {
	t.Helper()
	document, present := view.Docs.Get(harness.LiveDoc.AnyDefinition().Kind)
	if !present {
		return harness.LiveState{}
	}
	text, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var live harness.LiveState
	if err := json.Unmarshal(text, &live); err != nil {
		t.Fatal(err)
	}
	return live
}

// viewRecording is every view of a conversation from the attachment on, one per commit that changes it, unfolded (live-support.ts
// viewHistory).
type viewRecording struct {
	t           testing.TB
	mu          sync.Mutex
	views       []harness.ConversationView
	observation durable.Observation[harness.ConversationView]
}

func (recording *viewRecording) Advance(_ context.Context, change chord.WatchChange[harness.ConversationView]) {
	recording.mu.Lock()
	defer recording.mu.Unlock()
	recording.views = append(recording.views, change.Value)
}

func (recording *viewRecording) End(error) {}

// viewHistory records the views of conversationId from now on.
func viewHistory(t testing.TB, h harness.Harness, conversationId durable.ConversationId) *viewRecording {
	t.Helper()
	recording := &viewRecording{t: t}
	observation, err := h.ObserveConversation(t.Context(), conversationId, recording, nil)
	if err != nil {
		t.Fatal(err)
	}
	recording.observation = observation
	recording.mu.Lock()
	recording.views = append(recording.views, observation.Value())
	recording.mu.Unlock()
	return recording
}

// All returns the views recorded so far, oldest first.
func (recording *viewRecording) All() []harness.ConversationView {
	recording.mu.Lock()
	defer recording.mu.Unlock()
	return slices.Clone(recording.views)
}

// Live returns the pi.live value of each view.
func (recording *viewRecording) Live() []harness.LiveState {
	views := recording.All()
	states := make([]harness.LiveState, len(views))
	for index, view := range views {
		states[index] = liveValue(recording.t, view)
	}
	return states
}

// Detach stops the recording.
func (recording *viewRecording) Detach() { recording.observation.Detach() }

// slotView is a model-issued or nested slot, read through the fields of both.
type slotView struct {
	CallId string
	Name   string
	// TaskId is nil for a model-issued call not started yet.
	TaskId *durable.TaskId
	Status harness.ToolSlotStatus
	// Output is the retained running output; nil when the slot has none.
	Output *string
	// Entry is the result entry of a done model-issued slot.
	Entry *durable.EntryId
	// Nested is the nested slot, nil for a model-issued one.
	Nested *harness.NestedToolSlot
}

func slotsIn(live harness.LiveState) []slotView {
	var slots []slotView
	for _, slot := range live.Tools {
		slots = append(slots, slotView{CallId: slot.CallId, Name: slot.Name, TaskId: slot.TaskId, Status: slot.Status, Output: slot.Output, Entry: slot.Entry})
	}
	for index := range live.NestedTools {
		nested := &live.NestedTools[index]
		slots = append(slots, slotView{CallId: nested.CallId, Name: nested.Name, TaskId: &nested.TaskId, Status: nested.Status, Output: nested.Output, Nested: nested})
	}
	return slots
}

// slotsOf returns the slot of tool call callId in each pi.live value; nil where it is absent.
func slotsOf(history []harness.LiveState, callId string) []*slotView {
	found := make([]*slotView, len(history))
	for index, live := range history {
		for _, slot := range slotsIn(live) {
			if slot.CallId == callId {
				found[index] = &slot
				break
			}
		}
	}
	return found
}

// endedAt returns the index of the first value where call callId ended: its slot is done, or gone after it was listed; -1
// when it was never listed.
func endedAt(history []harness.LiveState, callId string) int {
	slots := slotsOf(history, callId)
	listed := slices.IndexFunc(slots, func(slot *slotView) bool { return slot != nil })
	if listed < 0 {
		return -1
	}
	for index := listed; index < len(slots); index++ {
		if slots[index] == nil || slots[index].Status == harness.ToolSlotDone {
			return index
		}
	}
	return -1
}

// startOrder returns the calls whose slot went running, in the order they started.
func startOrder(history []harness.LiveState) []string {
	var started []string
	for _, live := range history {
		for _, slot := range slotsIn(live) {
			if slot.Status == harness.ToolSlotRunning && !slices.Contains(started, slot.CallId) {
				started = append(started, slot.CallId)
			}
		}
	}
	return started
}

// firstSlot is the first slot of slots that is present and satisfies match; nil when none is.
func firstSlot(slots []*slotView, match func(*slotView) bool) *slotView {
	for _, slot := range slots {
		if slot != nil && match(slot) {
			return slot
		}
	}
	return nil
}

// liveIndexAfter is the index of the first pi.live value after index from that satisfies match; -1 when none does.
func liveIndexAfter(states []harness.LiveState, from int, match func(harness.LiveState) bool) int {
	for index := from + 1; index < len(states); index++ {
		if match(states[index]) {
			return index
		}
	}
	return -1
}

// nestedResultsRecording is the result of every nested call from now on, by its tool task, read from the NestedCallDoc its
// terminal commit writes (spec §7.3), the place a client reads a live caller's nested results from.
type nestedResultsRecording struct {
	mu      sync.Mutex
	results map[durable.TaskId]durable.NestedToolExecutionResult
}

// nestedResults records the results of nested calls from now on.
func nestedResults(t testing.TB, h harness.Harness) *nestedResultsRecording {
	t.Helper()
	recording := &nestedResultsRecording{results: map[durable.TaskId]durable.NestedToolExecutionResult{}}
	off := h.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
		for _, change := range publication.Changes {
			document, ok := change.(durable.DocumentChange)
			if !ok || document.Record.Kind != harness.NestedCallDoc.AnyDefinition().Kind || document.Value == nil {
				continue
			}
			text, err := json.Marshal(document.Value)
			if err != nil {
				panic(err)
			}
			var state harness.NestedCallState
			if err := json.Unmarshal(text, &state); err != nil {
				panic(err)
			}
			if state.TaskId != nil && state.Result != nil {
				recording.mu.Lock()
				recording.results[*state.TaskId] = *state.Result
				recording.mu.Unlock()
			}
		}
	})
	t.Cleanup(off)
	return recording
}

// nestedResultOf returns the result of nested call callId, found by the task its slot names.
func (recording *nestedResultsRecording) nestedResultOf(history []harness.LiveState, callId string) (durable.NestedToolExecutionResult, bool) {
	for _, slot := range slotsOf(history, callId) {
		if slot != nil && slot.TaskId != nil {
			recording.mu.Lock()
			defer recording.mu.Unlock()
			result, present := recording.results[*slot.TaskId]
			return result, present
		}
	}
	return durable.NestedToolExecutionResult{}, false
}
