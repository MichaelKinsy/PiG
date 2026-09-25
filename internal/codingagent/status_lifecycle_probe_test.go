package codingagent

import (
	"encoding/json"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
)

// This probe is paired with the pinned Pi handleEvent path by the canonical
// status-lifecycle scenario. It emits semantic status state, not normalized ANSI.
func TestStatusLifecycleProbe(t *testing.T) {
	type phase struct {
		Kind           string `json:"kind"`
		Message        string `json:"message"`
		Embedded       bool   `json:"embedded"`
		StandaloneRows int    `json:"standaloneRows"`
	}
	type trace struct {
		Mode          string  `json:"mode"`
		Embedded      bool    `json:"embedded"`
		ClearOnShrink bool    `json:"clearOnShrink"`
		Phases        []phase `json:"phases"`
	}
	var traces []trace
	for _, mode := range []string{"regular", "fullscreen"} {
		for _, embedded := range []bool{false, true} {
			for _, clearOnShrink := range []bool{false, true} {
				synctest.Test(t, func(t *testing.T) {
					m := statusBorderMode(t, embedded)
					m.opts.Settings.TuiMode = mode
					m.tuiInst.SetClearOnShrink(clearOnShrink)
					t.Cleanup(func() { m.clearStatusIndicator("") })
					result := trace{Mode: mode, Embedded: embedded, ClearOnShrink: clearOnShrink}
					sample := func() {
						p := phase{Embedded: m.activeWorkingIndicatorEmbedded, StandaloneRows: len(m.statusContainer.Render(120))}
						if indicator := m.activeStatusIndicator; indicator != nil {
							p.Kind, p.Message = indicator.Kind, indicator.Message
						}
						result.Phases = append(result.Phases, p)
					}
					m.handleAgentEvent(agent.CompactionStartEvent{Reason: "manual"})
					sample()
					m.handleAgentEvent(agent.SummarizationRetryScheduledEvent{Attempt: 1, MaxAttempts: 3, DelayMs: 2100, ErrorMessage: "overloaded"})
					sample()
					time.Sleep(time.Second)
					synctest.Wait()
					select {
					case apply := <-m.uiTaskCh:
						apply()
					default:
						t.Fatal("missing countdown update")
					}
					sample()
					m.handleAgentEvent(agent.SummarizationRetryAttemptStartEvent{Source: "branchSummary"})
					sample()
					m.handleAgentEvent(agent.SummarizationRetryFinishedEvent{})
					sample()
					m.clearStatusIndicator("branchSummary")
					sample()
					m.handleAgentEvent(agent.SummarizationRetryScheduledEvent{Attempt: 2, MaxAttempts: 3, DelayMs: 4000, ErrorMessage: "overloaded again"})
					sample()
					m.handleAgentEvent(agent.SummarizationRetryFinishedEvent{})
					sample()
					if m.retryCountdownStop != nil {
						t.Fatal("disposed status retained a timer")
					}
					traces = append(traces, result)
				})
			}
		}
	}
	encoded, err := json.Marshal(traces)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("STATUS_LIFECYCLE %s\n", encoded)
}
