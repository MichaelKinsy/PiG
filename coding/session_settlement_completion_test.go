package coding

import (
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/coding-agent/src/core/agent-session.ts:1468-1489,831-834 — prompt awaits _emitAgentSettled, including synchronous public subscribers, even when no extension subscribes.
func TestPromptWaitsForSettlementSubscriberWithoutExtensions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{}, fauxReply("done", ai.StopReasonStop, 0))
		if err := h.session.services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
			t.Fatal(err)
		}
		entered, release := make(chan struct{}), make(chan struct{})
		h.session.Subscribe(func(event agent.AgentEvent) {
			if _, ok := event.(agent.AgentSettledEvent); ok {
				close(entered)
				<-release
			}
		})
		done := make(chan error, 1)
		go func() { err := h.session.Prompt(t.Context(), "hello"); done <- err }()
		synctest.Wait()
		select {
		case <-entered:
		default:
			t.Error("settlement subscriber was not invoked")
		}
		select {
		case err := <-done:
			t.Errorf("Prompt returned before settlement subscriber completed: %v", err)
			close(release)
			return
		default:
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}
