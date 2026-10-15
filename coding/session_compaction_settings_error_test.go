// SPDX-License-Identifier: MIT

package coding

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestCompactionInvalidSettingsReachCaller(t *testing.T) {
	const failure = "Invalid compaction.reserveTokens setting: -1. Expected a non-negative safe integer."
	t.Run("manual compaction fails before provider work and releases admission", func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"reserveTokens":-1,"modelOverrides":{"faux/faux-1":{"reserveTokens":4096}}}}`})
		var events []agent.AgentEvent
		unsubscribe := h.session.Subscribe(func(event agent.AgentEvent) { events = append(events, event) })
		defer unsubscribe()
		_, err := h.session.Compact(t.Context(), "")
		if err == nil || err.Error() != failure {
			t.Fatalf("error=%v", err)
		}
		if h.provider.callCount() != 0 || h.session.IsCompacting() {
			t.Fatal("invalid settings reached provider or retained compaction admission")
		}
		var starts, ends int
		for _, event := range events {
			switch ev := event.(type) {
			case agent.CompactionStartEvent:
				starts++
			case agent.CompactionEndEvent:
				ends++
				if ev.ErrorMessage != "Compaction failed: "+failure {
					t.Fatal(ev)
				}
			}
		}
		if starts != 1 || ends != 1 {
			t.Fatalf("starts=%d ends=%d", starts, ends)
		}
		fmt.Println("COMPACTION_CALLER manual requests=0 start=1 end=1 idle=true")
	})
	t.Run("same-run settings change prevents next provider request", func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{}, fauxReply("first", ai.StopReasonStop, 0), fauxReply("must not run", ai.StopReasonStop, 0))
		unsubscribe := h.session.Subscribe(func(event agent.AgentEvent) {
			if turn, ok := event.(agent.TurnEndEvent); ok && turn.Message.Assistant != nil && turn.Message.Assistant.StopReason == ai.StopReasonStop {
				h.session.services.SettingsManager().ApplyOverrides(icodingagent.Settings{Compaction: &icodingagent.CompactionSettingsJSON{ReserveTokens: new(-1.)}})
			}
		})
		defer unsubscribe()
		h.session.agent.FollowUp(queueUser("next", 1))
		messages, err := h.session.Send(t.Context(), "start")
		if err == nil || !strings.Contains(err.Error(), failure) {
			t.Fatal(err)
		}
		if h.provider.callCount() != 1 {
			t.Fatalf("requests=%d", h.provider.callCount())
		}
		if len(messages) == 0 || messages[len(messages)-1].Assistant == nil || messages[len(messages)-1].Assistant.ErrorMessage != failure {
			t.Fatalf("messages=%+v error=%v", messages, err)
		}
		branch := h.session.inner.GetBranch()
		if len(branch) == 0 {
			t.Fatal("failure not persisted")
		}
		last, ok := branch[len(branch)-1].(icodingagent.MessageEntry)
		if !ok || last.Message.Assistant == nil || last.Message.Assistant.ErrorMessage != failure {
			t.Fatal("failed assistant missing from persisted branch")
		}
		fmt.Printf("COMPACTION_CALLER next requests=1 persisted=true rejected=%v\n", err != nil)
	})
}
