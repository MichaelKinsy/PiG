package inproc

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// runner.ts:1144-1170 emitMessageEnd(event): every handler receives `{ ...event, message: currentMessage }`, the caller's event with the latest
// message, so a later handler sees an earlier replacement and the event's other members unchanged.
func TestEmitMessageEndHandsHandlersTheCallersEventWithTheLatestMessage(t *testing.T) {
	var seen []extension.MessageEndEvent
	replace := func(text string) extension.HandlerFn {
		return func(args ...any) (any, error) {
			event := args[0].(extension.MessageEndEvent)
			seen = append(seen, event)
			message := assistantText(text)
			return &extension.MessageEndEventResult{Message: &message}, nil
		}
	}
	runner := NewRunner([]extension.Extension{{Path: "first", Handlers: map[string][]extension.HandlerFn{"message_end": {replace("one")}}}, {Path: "second", Handlers: map[string][]extension.HandlerFn{"message_end": {replace("two")}}}}, t.TempDir())
	original := assistantText("original")
	ended, err := runner.EmitMessageEnd(context.Background(), extension.MessageEndEvent{Type: "message_end-from-caller", Message: original})
	if err != nil {
		t.Fatal(err)
	}
	want := []extension.MessageEndEvent{
		{Type: "message_end-from-caller", Message: original},
		{Type: "message_end-from-caller", Message: assistantText("one")},
	}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("handlers saw %v, want %v", seen, want)
	}
	if ended == nil || !reflect.DeepEqual(*ended, assistantText("two")) {
		t.Fatalf("final message = %v", ended)
	}
}
