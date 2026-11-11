package extension

import (
	"maps"
	"slices"
	"sync"
)

// thinkingRunsState is an extension's ownership of thinking runs, shared by every copy of the Extension: the host that loads the
// extension, the runner that holds a copy and the API a factory kept all read and write the same flag.
type thinkingRunsState struct {
	mu        sync.Mutex
	owned     bool
	observers map[int]func()
	next      int
}

// InitializeThinkingRuns creates the state every copy of the Extension shares. Call it before the Extension is copied; a copy made
// earlier keeps a flag of its own that nothing else reads.
// pig additive (D214)
func (e *Extension) InitializeThinkingRuns() {
	if e.thinkingRuns == nil {
		e.thinkingRuns = &thinkingRunsState{}
	}
}

// OwnsThinkingRuns reports whether the extension owns the thinking runs of assistant messages (pi.ownThinkingRuns). An Extension
// whose state was never initialized owns none.
// pig additive (D214): Pi has no such declaration; its in-process extensions patch AssistantMessageComponent instead.
func (e *Extension) OwnsThinkingRuns() bool {
	state := e.thinkingRuns
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.owned
}

// SetOwnsThinkingRuns declares (true) or withdraws (false) the extension's ownership of thinking runs and tells the observers when
// that changes it. The Extension's state must be initialized ([Extension.InitializeThinkingRuns]).
// pig additive (D214)
func (e *Extension) SetOwnsThinkingRuns(owned bool) {
	state := e.thinkingRuns
	if state == nil {
		panic("extension: SetOwnsThinkingRuns on an Extension without InitializeThinkingRuns")
	}
	state.mu.Lock()
	if state.owned == owned {
		state.mu.Unlock()
		return
	}
	state.owned = owned
	observers := make([]func(), 0, len(state.observers))
	for _, id := range slices.Sorted(maps.Keys(state.observers)) {
		observers = append(observers, state.observers[id])
	}
	state.mu.Unlock()
	for _, observe := range observers {
		observe()
	}
}

// ObserveThinkingRuns calls fn, on the goroutine that changed the flag, after the extension's ownership of thinking runs changes,
// until stop is called. Calling stop again does nothing. An Extension without state has nothing to observe.
// pig additive (D214)
func (e *Extension) ObserveThinkingRuns(fn func()) (stop func()) {
	state := e.thinkingRuns
	if state == nil {
		return func() {}
	}
	state.mu.Lock()
	if state.observers == nil {
		state.observers = make(map[int]func())
	}
	id := state.next
	state.next++
	state.observers[id] = fn
	state.mu.Unlock()
	return func() {
		state.mu.Lock()
		delete(state.observers, id)
		state.mu.Unlock()
	}
}
