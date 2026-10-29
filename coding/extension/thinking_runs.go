package extension

// thinkingRunsState is an extension's ownership of thinking runs, shared by every copy of the Extension.
type thinkingRunsState struct{}

// InitializeThinkingRuns creates the state every copy of the Extension shares. Call it before the Extension is copied.
func (e *Extension) InitializeThinkingRuns() {
	if e.thinkingRuns == nil {
		e.thinkingRuns = &thinkingRunsState{}
	}
}

// OwnsThinkingRuns reports whether the extension owns thinking runs.
// pig additive (D214)
func (e *Extension) OwnsThinkingRuns() bool { return false }

// SetOwnsThinkingRuns declares or withdraws the extension's ownership of thinking runs.
// pig additive (D214)
func (e *Extension) SetOwnsThinkingRuns(owned bool) {}

// ObserveThinkingRuns calls fn after the extension's ownership changes, until stop is called.
// pig additive (D214)
func (e *Extension) ObserveThinkingRuns(fn func()) (stop func()) { return func() {} }
