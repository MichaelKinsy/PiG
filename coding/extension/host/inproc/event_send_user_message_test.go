package inproc_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Upstream puts sendUserMessage on ExtensionAPI, the object handed to every
// extension, so an upstream event handler can send a user message. Pig attached
// only the base Context to event dispatch and kept the action on the command
// context, so an in-process extension could observe a turn ending and had no way
// to tell the agent anything about it. The subprocess SDK could, from the same
// kind of handler, which made the capability depend on where an extension ran.
func TestAnEventHandlerCanSendAUserMessage(t *testing.T) {
	var got any
	var gotOpts *extension.SendUserMessageOptions

	ext := extension.Extension{
		Name:     "probe",
		Path:     "builtin:probe",
		Handlers: map[string][]extension.HandlerFn{},
	}
	ext.Handlers["turn_end"] = []extension.HandlerFn{func(args ...any) (any, error) {
		ctx, _ := args[1].(context.Context)
		extCtx := extension.FromContext(ctx)
		if extCtx == nil {
			t.Error("no extension context was attached to the event dispatch")
			return nil, nil
		}
		return nil, extCtx.SendUserMessage("from an event handler",
			&extension.SendUserMessageOptions{DeliverAs: extension.DeliverAsFollowUp})
	}}
	runner := inproc.NewRunner([]extension.Extension{ext}, "/cwd")
	runner.BindCore(
		extension.ExtensionActions{
			SendUserMessage: func(content any, opts *extension.SendUserMessageOptions) error {
				got = content
				gotOpts = opts
				return nil
			},
		},
		extension.ContextActions{},
		nil,
	)

	if _, err := runner.Emit(context.Background(), extension.TurnEndEvent{Type: "turn_end"}); err != nil {
		t.Fatalf("emit turn_end: %v", err)
	}

	if got != "from an event handler" {
		t.Errorf("host received %v, want the handler's message", got)
	}
	if gotOpts == nil || gotOpts.DeliverAs != extension.DeliverAsFollowUp {
		t.Errorf("delivery options did not reach the host: %+v", gotOpts)
	}
}
