package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/3982-message-end-cost-override.test.ts:14
func TestMessageEndAllowsExtensionsToReplaceFinalizedAssistantUsageCost(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Handlers: map[string][]extension.HandlerFn{
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
			var out extension.AgentMessage = agent.AgentMessage{Assistant: &replacement}
			return &extension.MessageEndEventResult{Message: &out}, nil
		}},
	}}}, fauxReply("hello", ai.StopReasonStop, 0))
	messages, err := h.session.Send(t.Context(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	assistant := lastAssistantMessage(messages)
	if assistant == nil || assistant.Usage == nil || assistant.Usage.Cost.Total != 0.123 {
		t.Fatalf("assistant message = %+v", assistant)
	}
	found := false
	for _, event := range h.settle(t) {
		if end, ok := event.(agent.MessageEndEvent); ok && end.Message.Assistant != nil {
			found = true
			if usage := end.Message.Assistant.Usage; usage == nil || usage.Cost.Total != 0.123 {
				t.Fatalf("message_end usage = %+v", usage)
			}
		}
	}
	if !found {
		t.Fatal("missing assistant message_end event")
	}
}
