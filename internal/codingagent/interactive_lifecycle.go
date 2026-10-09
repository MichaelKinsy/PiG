package codingagent

import (
	"errors"
	"slices"
)

// onStop registers a teardown for Stop. Stop runs the teardowns last in, first out, as the defers of the former single-function Run did.
func (m *InteractiveMode) onStop(teardown func() error) {
	m.stopMu.Lock()
	defer m.stopMu.Unlock()
	m.teardowns = append(m.teardowns, teardown)
}

// Stop is upstream's stop(): it releases everything Init and Run registered, in reverse order, and marks the mode uninitialized. It is safe to
// call more than once, and after a failed Init it releases what that Init built. The optional fullscreenExitOutput replaces the setting for this
// stop, as upstream's parameter does. The returned error joins the teardown failures.
func (m *InteractiveMode) Stop(fullscreenExitOutput ...FullscreenExitOutput) error {
	m.stopMu.Lock()
	teardowns := m.teardowns
	m.teardowns = nil
	m.exitOutputOverride = nil
	if len(fullscreenExitOutput) > 0 {
		m.exitOutputOverride = &fullscreenExitOutput[0]
	}
	m.stopMu.Unlock()
	var err error
	for _, teardown := range slices.Backward(teardowns) {
		err = errors.Join(err, teardown())
	}
	m.isInitialized = false
	// The owner loop no longer runs once Stop finished, also when Init ran without Run (PI_STARTUP_BENCHMARK), so a runtime closed later resets the UI in place instead of waiting for the loop.
	m.runEnded.Store(true)
	return err
}

// fullscreenExitOutput is the output a fullscreen exit prints: the argument of the running Stop, else the setting.
func (m *InteractiveMode) fullscreenExitOutput() FullscreenExitOutput {
	m.stopMu.Lock()
	override := m.exitOutputOverride
	m.stopMu.Unlock()
	if override != nil {
		return *override
	}
	return (&SettingsManager{merged: m.opts.Settings}).GetFullscreenExitOutput()
}

// stopRun is the Stop that ends Run. After a fatal runtime error it passes "transcript", as upstream's handleFatalRuntimeError calls
// stop("transcript") so a fullscreen exit prints the transcript with the error whatever the setting (interactive-mode.ts:2118-2131).
func (m *InteractiveMode) stopRun() error {
	if m.fatalRuntime.Load() {
		return m.Stop(FullscreenExitOutputTranscript)
	}
	return m.Stop()
}
