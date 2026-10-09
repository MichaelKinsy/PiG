package coding

import (
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// RST-001: agent-session.ts:870-892 guards both extension and public settlement callbacks; prompt at 1607-1609 queues their requests before taking any run lock. The outer prompt awaits those requests after dispatch.
func TestPublicSettlementSubscriberPromptDefersUntilDispatchCompletes(t *testing.T) {
	for _, withHandler := range []bool{false, true} {
		t.Run(fmt.Sprintf("extension_handler=%v", withHandler), func(t *testing.T) {
			handlers := map[string]func(*Session){}
			if withHandler {
				handlers["agent_settled"] = func(*Session) {}
			}
			h := runStateHarness(t, handlers,
				fauxReply("first", ai.StopReasonStop, 0),
				fauxReply("second", ai.StopReasonStop, 0))
			if err := h.session.services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
				t.Fatal(err)
			}
			var started atomic.Bool
			var mu sync.Mutex
			var order []string
			record := func(value string) {
				mu.Lock()
				defer mu.Unlock()
				order = append(order, value)
			}
			h.session.Subscribe(func(event agent.AgentEvent) {
				if _, ok := event.(agent.AgentSettledEvent); !ok {
					return
				}
				if started.Swap(true) {
					record("second settled")
					return
				}
				record("first settled")
				if h.session.IsStreaming() {
					t.Error("session remains streaming during settlement")
				}
				// A missing guard makes Prompt lock the original run's mutex and deadlock the event funnel. Fail before that lock cycle so the regression also has bounded cleanup on the broken implementation.
				if !h.session.runState.settling.Load() {
					t.Error("public settlement callback lacks the prompt deferral guard")
					return
				}
				if err := h.session.Prompt(t.Context(), "follow-on"); err != nil {
					t.Errorf("deferred Prompt = %v; want no immediate error", err)
				}
				if got := h.provider.callCount(); got != 1 {
					t.Errorf("provider calls during settlement dispatch = %d, want only the first request", got)
				}
				record("subscriber returned")
			})
			if err := h.session.Prompt(t.Context(), "start"); err != nil {
				t.Fatal(err)
			}
			record("outer returned")
			if got := h.provider.callCount(); got != 2 {
				t.Errorf("provider calls at outer Prompt return = %d, want both requests", got)
			}
			if got := lastAssistantText(h.session.Messages()); got != "second" {
				t.Errorf("last session answer = %q, want second", got)
			}
			mu.Lock()
			defer mu.Unlock()
			if want := []string{"first settled", "subscriber returned", "second settled", "outer returned"}; !slices.Equal(order, want) {
				t.Errorf("settlement order = %v, want %v", order, want)
			}
		})
	}
}
