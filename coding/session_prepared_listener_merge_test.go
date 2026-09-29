package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi awaits synchronous listeners without a Session mutex blocking their reads. Prepared-prompt admission must not reintroduce that lock around the provider run.
func TestPreparedPromptListenersCanReadSessionState(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{}, fauxReply("answer", ai.StopReasonStop, 0))
	observed, unlocked := false, false
	detach := h.session.Subscribe(func(event agent.AgentEvent) {
		if end, ok := event.(agent.MessageEndEvent); ok && end.Message.Assistant != nil {
			observed = true
			unlocked = h.session.mu.TryLock()
			if unlocked {
				h.session.mu.Unlock()
			}
		}
	})
	defer detach()
	if _, err := h.session.Send(t.Context(), "readable callback"); err != nil {
		t.Fatal(err)
	}
	if !observed || !unlocked {
		t.Fatalf("listener observed=%v state lock available=%v", observed, unlocked)
	}
}
