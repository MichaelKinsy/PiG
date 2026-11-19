package inproc

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// pig divergence (D657): the in-process Go runner is the one realization that does not copy an event for a handler. Emit passes the one event to every handler
// of every extension, as Pi's emit does (runner.ts emit: handler(event, ctx) in the loop over snapshotEventHandlers), so a handler's write into nested event data
// (here the first tool result of a turn_end event) is read by the next handler, of the same extension and of the next one.
func TestRunnerEmitHandsEveryHandlerTheSameEvent(t *testing.T) {
	var read []string
	write := func(args ...any) (any, error) {
		args[0].(extension.TurnEndEvent).ToolResults[0].ToolName = "WRITTEN-BY-THE-FIRST-HANDLER"
		return nil, nil
	}
	reader := func(args ...any) (any, error) {
		read = append(read, args[0].(extension.TurnEndEvent).ToolResults[0].ToolName)
		return nil, nil
	}
	runner := NewRunner([]extension.Extension{
		{Path: "/ext/first.ts", Handlers: map[string][]extension.HandlerFn{"turn_end": {write, reader}}},
		{Path: "/ext/second.ts", Handlers: map[string][]extension.HandlerFn{"turn_end": {reader}}},
	}, t.TempDir())
	runner.AddErrorListener(func(err *extension.ExtensionError) { t.Errorf("error listener: %+v", err) })
	event := extension.TurnEndEvent{Type: "turn_end", ToolResults: []extension.ToolResultMessage{{Role: "toolResult", ToolName: "original"}}}
	if _, err := runner.Emit(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	want := []string{"WRITTEN-BY-THE-FIRST-HANDLER", "WRITTEN-BY-THE-FIRST-HANDLER"}
	if len(read) != 2 || read[0] != want[0] || read[1] != want[1] {
		t.Errorf("the handlers after the first read %q, want %q: one event for every handler", read, want)
	}
	if got := event.ToolResults[0].ToolName; got != "WRITTEN-BY-THE-FIRST-HANDLER" {
		t.Errorf("the caller's event reads %q after the dispatch, want the handler's write: the event is not a copy", got)
	}
}
