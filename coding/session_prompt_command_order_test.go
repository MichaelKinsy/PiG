package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi prompt() checks extension commands before input transformation, not again on the transformed provider text.
func TestPromptInputTransformDoesNotRedispatchCommand(t *testing.T) {
	commands := 0
	ext := extension.Extension{
		Handlers: map[string][]extension.HandlerFn{"input": {func(...any) (any, error) {
			return extension.InputEventResultTransform{Text: "/literal-command"}, nil
		}}},
		Commands: map[string]extension.RegisteredCommand{"literal-command": {Name: "literal-command", Handler: func(context.Context, string) error {
			commands++
			return nil
		}}},
	}
	var observed string
	h := newModelExtensionHarness(t, []bool{false}, "", true, ext, nil, func(messages []ai.Message) *ai.AssistantMessage {
		observed = modelExtensionUserText(messages)
		return fauxReply("done", ai.StopReasonStop, 0)(messages)
	})
	if err := h.session.Prompt(t.Context(), "ordinary input"); err != nil {
		t.Fatal(err)
	}
	if commands != 0 || observed != "/literal-command" {
		t.Fatalf("transformed input redispatched: commands=%d provider=%q", commands, observed)
	}
}
