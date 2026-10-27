// Ports packages/durable/test/live-support.ts: what tests of renderers' state read, every pi.live value of a conversation and
// the results of nested calls.

package harness

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// liveValue is the pi.live value of a view; an absent document is the empty state.
func liveValue(t testing.TB, view ConversationView) LiveState {
	t.Helper()
	document, present := view.Docs.Get(LiveDoc.AnyDefinition().Kind)
	if !present {
		return LiveState{}
	}
	text, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var live LiveState
	if err := json.Unmarshal(text, &live); err != nil {
		t.Fatal(err)
	}
	return live
}

// viewRecording is every view of a conversation from the attachment on, one per commit that changes it, unfolded: a synchronous
// observer, so no transition is skipped (live-support.ts viewHistory).
type viewRecording struct {
	t           testing.TB
	mu          sync.Mutex
	views       []ConversationView
	observation durable.Observation[ConversationView]
}

func (recording *viewRecording) Advance(_ context.Context, change chord.WatchChange[ConversationView]) {
	recording.mu.Lock()
	defer recording.mu.Unlock()
	recording.views = append(recording.views, change.Value)
}

func (recording *viewRecording) End(error) {}

// viewHistory records the views of conversationId from now on.
func viewHistory(t testing.TB, harness Harness, conversationId durable.ConversationId) *viewRecording {
	t.Helper()
	recording := &viewRecording{t: t}
	observation, err := harness.ObserveConversation(testContext, conversationId, recording, nil)
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
func (recording *viewRecording) All() []ConversationView {
	recording.mu.Lock()
	defer recording.mu.Unlock()
	return slices.Clone(recording.views)
}

// Live returns the pi.live value of each view.
func (recording *viewRecording) Live() []LiveState {
	views := recording.All()
	states := make([]LiveState, len(views))
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
	Status ToolSlotStatus
	// Entry is the result entry of a done model-issued slot.
	Entry *durable.EntryId
	// Nested is the nested slot, nil for a model-issued one.
	Nested *NestedToolSlot
}

func slotsIn(live LiveState) []slotView {
	var slots []slotView
	for _, slot := range live.Tools {
		slots = append(slots, slotView{CallId: slot.CallId, Name: slot.Name, TaskId: slot.TaskId, Status: slot.Status, Entry: slot.Entry})
	}
	for index := range live.NestedTools {
		nested := &live.NestedTools[index]
		slots = append(slots, slotView{CallId: nested.CallId, Name: nested.Name, TaskId: &nested.TaskId, Status: nested.Status, Nested: nested})
	}
	return slots
}

func slotsOf(history []LiveState, callId string) []*slotView {
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
func endedAt(history []LiveState, callId string) int {
	slots := slotsOf(history, callId)
	listed := slices.IndexFunc(slots, func(slot *slotView) bool { return slot != nil })
	if listed < 0 {
		return -1
	}
	for index := listed; index < len(slots); index++ {
		if slots[index] == nil || slots[index].Status == ToolSlotDone {
			return index
		}
	}
	return -1
}

// startOrder returns the calls whose slot went running, in the order they started.
func startOrder(history []LiveState) []string {
	var started []string
	for _, live := range history {
		for _, slot := range slotsIn(live) {
			if slot.Status == ToolSlotRunning && !slices.Contains(started, slot.CallId) {
				started = append(started, slot.CallId)
			}
		}
	}
	return started
}

// nestedResultsRecording is the result of every nested call from now on, by its tool task, read from the NestedCallDoc its
// terminal commit writes (spec §7.3), the place a client reads a live caller's nested results from.
type nestedResultsRecording struct {
	mu      sync.Mutex
	results map[durable.TaskId]durable.NestedToolExecutionResult
}

// nestedResults records the results of nested calls from now on.
func nestedResults(harness Harness) *nestedResultsRecording {
	recording := &nestedResultsRecording{results: map[durable.TaskId]durable.NestedToolExecutionResult{}}
	observeCommits(harness, func(_ context.Context, publication session.ObservedCommit) {
		for _, change := range documentChanges(publication.Changes) {
			if change.Record.Kind != nestedCallKind || change.Value == nil {
				continue
			}
			text, err := json.Marshal(change.Value)
			if err != nil {
				panic(err)
			}
			var state NestedCallState
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
	return recording
}

// nestedResultOf returns the result of nested call callId, found by the task its slot names.
func (recording *nestedResultsRecording) nestedResultOf(history []LiveState, callId string) (durable.NestedToolExecutionResult, bool) {
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

// graphRecording is every task graph from now on, one per change, unfolded.
type graphRecording struct {
	mu          sync.Mutex
	graphs      []TaskGraph
	observation durable.Observation[TaskGraph]
}

func (recording *graphRecording) Advance(_ context.Context, change chord.WatchChange[TaskGraph]) {
	recording.mu.Lock()
	defer recording.mu.Unlock()
	recording.graphs = append(recording.graphs, change.Value)
}

func (recording *graphRecording) End(error) {}

// graphHistory records the task graphs from now on.
func graphHistory(t testing.TB, harness Harness) *graphRecording {
	t.Helper()
	recording := &graphRecording{}
	observation, err := harness.ObserveTaskGraph(testContext, recording)
	if err != nil {
		t.Fatal(err)
	}
	recording.observation = observation
	recording.mu.Lock()
	recording.graphs = append(recording.graphs, observation.Value())
	recording.mu.Unlock()
	return recording
}

// All returns the graphs recorded so far, oldest first.
func (recording *graphRecording) All() []TaskGraph {
	recording.mu.Lock()
	defer recording.mu.Unlock()
	return slices.Clone(recording.graphs)
}

// Detach stops the recording.
func (recording *graphRecording) Detach() { recording.observation.Detach() }

// generations returns the live pi.generation nodes of graph, in task ID order.
func generations(graph TaskGraph) []TaskGraphNode {
	var nodes []TaskGraphNode
	for _, node := range graph.Tasks {
		if node.Kind == "pi.generation" {
			nodes = append(nodes, node)
		}
	}
	slices.SortFunc(nodes, func(a, b TaskGraphNode) int { return int(a.Id - b.Id) })
	return nodes
}

// liveIndexAfter is the index of the first pi.live value after index from that satisfies match; -1 when none does.
func liveIndexAfter(states []LiveState, from int, match func(LiveState) bool) int {
	for index := from + 1; index < len(states); index++ {
		if match(states[index]) {
			return index
		}
	}
	return -1
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
