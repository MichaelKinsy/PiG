package coding

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/src/core/agent-session.ts:_emitAgentSettled — extension handlers complete before the public agent_settled event. Their custom messages precede settlement on listener and mode-output paths.
func TestSettledCustomMessagePrecedesSettlementPublication(t *testing.T) {
	for _, count := range []int{1, 128} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var session *Session
			h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Handlers: map[string][]extension.HandlerFn{
				"agent_settled": {func(...any) (any, error) {
					for range count {
						if err := session.SendMessage(extension.CustomMessageRef{CustomType: "after", Content: "after turn", Display: true}, nil); err != nil {
							return nil, err
						}
					}
					return nil, nil
				}},
			}}}, fauxReply("done", ai.StopReasonStop, 0))
			session = h.session
			key := func(event agent.AgentEvent) string {
				switch value := event.(type) {
				case agent.MessageStartEvent:
					if value.Message.Custom != nil {
						return "custom:start"
					}
				case agent.MessageEndEvent:
					if value.Message.Custom != nil {
						return "custom:end"
					}
				case agent.AgentSettledEvent:
					return "settled"
				}
				return ""
			}
			var mu sync.Mutex
			var listeners []string
			session.Subscribe(func(event agent.AgentEvent) {
				if value := key(event); value != "" {
					mu.Lock()
					listeners = append(listeners, value)
					mu.Unlock()
				}
			})
			if _, err := session.Prompt(t.Context(), "hello"); err != nil {
				t.Fatal(err)
			}
			if err := session.FlushEvents(t.Context()); err != nil {
				t.Fatal(err)
			}
			h.mu.Lock()
			var output []string
			for _, event := range h.events {
				if value := key(event); value != "" {
					output = append(output, value)
				}
			}
			h.mu.Unlock()
			var want []string
			for range count {
				want = append(want, "custom:start", "custom:end")
			}
			want = append(want, "settled")
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(listeners, want) {
				t.Errorf("listener order=%v want=%v", listeners, want)
			}
			if !reflect.DeepEqual(output, want) {
				t.Errorf("mode output order=%v want=%v", output, want)
			}
			if os.Getenv("PIG_CUSTOM_SETTLED_PROBE") == "1" && !t.Failed() {
				data, err := json.Marshal([]any{count, listeners, output})
				if err != nil {
					t.Fatal(err)
				}
				fmt.Println("CUSTOM_SETTLEMENT " + string(data))
			}
		})
	}
}
