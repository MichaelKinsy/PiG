package coding

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// packages/coding-agent/test/suite/agent-session-runtime.test.ts:125.
// Pi agent-session.ts:_handleAgentEvent applies message_end replacements before persisting them.
func TestAgentSessionRuntimeOriginalAssistantReplacement(t *testing.T) {
	recordRuntimeOriginal(t, 125)
	h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension {
		return extension.Extension{
			Path: "/runtime-cost", Handlers: map[string][]extension.HandlerFn{
				"message_end": {func(args ...any) (any, error) {
					message := args[0].(extension.MessageEndEvent).Message.(agent.AgentMessage)
					if message.Assistant == nil {
						return nil, nil
					}
					replacement := *message.Assistant
					usage := ai.Usage{}
					if replacement.Usage != nil {
						usage = *replacement.Usage
					}
					usage.Cost.Total = 0.123
					replacement.Usage = &usage
					var result extension.AgentMessage = agent.AgentMessage{Assistant: &replacement}
					return &extension.MessageEndEventResult{Message: &result}, nil
				}},
			},
		}
	}})
	session := h.runtime.Session()
	if _, err := session.Prompt(t.Context(), "hello"); err != nil {
		t.Fatal(err)
	}
	var live, persisted *agent.AssistantMessage
	for _, message := range session.Messages() {
		if message.Assistant != nil {
			live = message.Assistant
			break
		}
	}
	for _, entry := range session.Entries() {
		if message, ok := entry.AsMessage(); ok && message.Message.Assistant != nil {
			persisted = message.Message.Assistant
			break
		}
	}
	for name, message := range map[string]*agent.AssistantMessage{"session": live, "persisted": persisted} {
		if message == nil || message.Role != agent.RoleAssistant || message.Usage == nil || message.Usage.Cost.Total != 0.123 {
			t.Fatalf("%s assistant = %+v, want cost.total 0.123", name, message)
		}
	}
	if os.Getenv("PIG_RUNTIME_ORIGINAL_PROBE") != "" {
		data, err := json.Marshal([]any{"runtime", 125, []any{live.Role, live.Usage.Cost.Total, persisted.Role, persisted.Usage.Cost.Total}})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("RUNTIME_OBSERVATION " + string(data))
	}
}
