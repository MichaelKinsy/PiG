package coding

import (
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Ports Pi 1.2.0 test/suite/agent-session-prompt.test.ts:690 (case by ReviewCodingAgent.CaCoreRows).
func TestUpstreamSessionPromptAbortWhileBeforeAgentStartRuns(t *testing.T) {

	// upstream: packages/coding-agent/test/suite/agent-session-prompt.test.ts:690.
	t.Run("aborts a run when abort is requested while before_agent_start runs", func(t *testing.T) {
		handlerStarted, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
			once.Do(func() { close(handlerStarted) })
			<-release
			return nil, nil
		}}}}
		h := newRecoveryHarness(t, harnessOptions{extension: ext, compatStream: true}, fauxReply("should not complete", ai.StopReasonStop, 0))

		run := make(chan error, 1)
		go func() {
			run <- h.session.SendCustomMessage(t.Context(), extension.CustomMessageRef{CustomType: "task-done", Content: "background task finished", Display: true}, &extension.SendMessageOptions{TriggerTurn: new(true)})
		}()
		<-handlerStarted
		// session.abort() records the abort synchronously and returns the wait for idle.
		h.session.RequestAbort()
		aborted := make(chan error, 1)
		go func() { aborted <- h.session.WaitForIdle(t.Context()) }()
		close(release)
		if err := <-run; err != nil {
			t.Fatal(err)
		}
		if err := <-aborted; err != nil {
			t.Fatal(err)
		}

		messages := h.session.Messages()
		if len(messages) == 0 {
			t.Fatal("no messages")
		}
		last := messages[len(messages)-1]
		if last.Assistant == nil {
			t.Fatalf("last message role = %q, want assistant", last.Role())
		}
		if last.Assistant.StopReason != ai.StopReasonAborted {
			t.Errorf("last stopReason = %q, want aborted", last.Assistant.StopReason)
		}
		if !h.session.IsIdle() {
			t.Error("the Session is not idle after the aborted run")
		}
	})
}
