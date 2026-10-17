package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi 1.1.0 agent-session.ts:1080-1085 (_emitAgentSettled) reads _agentRunAbortRequested and emits agent_settled { aborted } to the
// extension runner and the session event stream (types.ts AgentSettledEvent, CHANGELOG 1.1.0, #10607). A run that finishes
// normally settles with aborted=false; a run stopped by abort() settles with aborted=true; the next run clears it.
func TestAgentSettledReportsWhetherTheRunWasAborted(t *testing.T) {
	t.Run("a finished run settles with aborted false", func(t *testing.T) {
		h := newBoundaryHarness(t, harnessOptions{}, boundaryReply("done", ai.StopReasonStop, 0))
		boundaryPrompt(t, h, "start")
		settled := boundaryEvents[agent.AgentSettledEvent](h)
		if len(settled) != 1 || settled[0].Aborted {
			t.Fatalf("settled events = %+v, want one with Aborted=false", settled)
		}
	})
	t.Run("an aborted run settles with aborted true, the next run with false", func(t *testing.T) {
		started, release := make(chan struct{}), make(chan struct{})
		var extSettled []extension.AgentSettledEvent
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"agent_before_settle": {func(...any) (any, error) {
				select {
				case <-started:
				default:
					close(started)
					<-release
				}
				return nil, nil
			}},
			"agent_settled": {func(args ...any) (any, error) {
				for _, arg := range args {
					if evt, ok := arg.(extension.AgentSettledEvent); ok {
						extSettled = append(extSettled, evt)
					}
				}
				return nil, nil
			}},
		}}
		h := newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("first", ai.StopReasonStop, 0), boundaryReply("second", ai.StopReasonStop, 0))
		prompt := make(chan error, 1)
		go func() { prompt <- h.session.Prompt(t.Context(), "start", nil) }()
		<-started
		h.session.RequestAbort()
		if !h.session.AgentRunAbortRequested() {
			t.Error("AgentRunAbortRequested = false after abort during an active run")
		}
		close(release)
		if err := <-prompt; err != nil {
			t.Fatal(err)
		}
		boundaryPrompt(t, h, "again")
		settled := boundaryEvents[agent.AgentSettledEvent](h)
		if len(settled) != 2 || !settled[0].Aborted || settled[1].Aborted {
			t.Fatalf("settled events = %+v, want Aborted true then false", settled)
		}
		if len(extSettled) != 2 || !extSettled[0].Aborted || extSettled[1].Aborted {
			t.Fatalf("extension settled events = %+v, want aborted true then false", extSettled)
		}
	})
}
