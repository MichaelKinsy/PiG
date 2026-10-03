package main

import (
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Pi binds ctx.signal to `() => this.agent.signal` (agent-session.ts:3368) in every mode, and the getter is live at every read (runner.ts:917-920). Print and JSON mode report each run their Session's agent begins or ends to the extension host's UI bridge, so every runtime sees it at once, and the signal the host reads then is the run's: present when the run began, gone when it ended.
func TestPrintModeReportsRunSignalChangesToTheExtensionHost(t *testing.T) {
	for _, mode := range []string{"text", "json"} {
		t.Run(mode, func(t *testing.T) {
			provider := ai.NewFauxProvider(ai.FauxConfig{})
			provider.SetResponses(fauxSteps(fauxTextResponse("done")))
			host := printModeTestHost(t, provider)
			host.Bridge = subprocess.NewUIBridge(func() {})
			var mu sync.Mutex
			var states []bool
			host.Bridge.OnRunSignalChanged = func() {
				mu.Lock()
				defer mu.Unlock()
				states = append(states, host.Bridge.RunSignal() != nil)
			}
			result := runPrintModeForTest(t, host, printModeOptions{Mode: mode, InitialMessage: "go"})
			if result.err != nil || result.stderr != "" {
				t.Fatalf("run = %v, stderr %q", result.err, result.stderr)
			}
			mu.Lock()
			defer mu.Unlock()
			if want := []bool{true, false}; !slices.Equal(states, want) {
				t.Fatalf("run signal read at each reported change = %v, want %v", states, want)
			}
		})
	}
}
