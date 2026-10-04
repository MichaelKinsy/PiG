package codingagent

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Exercise status replacement and countdown disposal on the production renderer
// with retained transcript history. Every iteration joins the countdown worker.
func BenchmarkStatusLifecycle(b *testing.B) {
	for _, mode := range []string{"regular", "fullscreen"} {
		for _, editor := range []string{"default", "standalone", "embedded"} {
			b.Run(fmt.Sprintf("%s/editor=%s", mode, editor), func(b *testing.B) {
				m, terminal := newTickRenderProbe(b, mode)
				custom := &fakeRemoteEditor{embedWorkingStatus: editor == "embedded"}
				if editor != "default" {
					m.setRemoteEditor(custom)
					m.editor.SetRemoteFrame([]string{"custom editor"}, m.tuiInst.Width(), false)
				}
				for range 1000 {
					m.chatContainer.Add(tui.NewText("retained transcript line"))
				}
				m.tuiInst.Render()
				terminal.take()
				b.ReportAllocs()
				for b.Loop() {
					m.handleAgentEvent(agent.CompactionStartEvent{Reason: "manual"})
					m.handleAgentEvent(agent.AutoRetryStartEvent{Attempt: 1, MaxAttempts: 3, DelayMs: 30000})
					m.handleAgentEvent(agent.SummarizationRetryAttemptStartEvent{Source: "branchSummary"})
					m.handleAgentEvent(agent.SummarizationRetryFinishedEvent{})
					m.clearStatusIndicator("branchSummary")
					m.drainMainLoopOnce()
					terminal.take()
					custom.configs = custom.configs[:0]
				}
			})
		}
	}
}
