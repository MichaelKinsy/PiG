package ai

import (
	"reflect"
	"testing"
)

// deferredCall is a streamed tool call as a stream builder publishes it: its argument text waits for a reader to parse it.
func deferredCall(id, text string) ToolCall {
	call := ToolCall{ID: id, Name: "tool", Arguments: JsonObject{}}
	call.deferArguments(text)
	return call
}

func settledArguments(t *testing.T, label string, got AssistantContentBlock, id, text string) {
	t.Helper()
	call, ok := got.(ToolCall)
	if !ok || call.ID != id {
		t.Fatalf("%s: block %#v, want tool call %q", label, got, id)
	}
	want := eagerToolCall(text)
	want.ID = id
	sameArguments(t, label, call, want)
}

// A reader of a cell that publishes the same argument text again finds the parse it stored for that text: a call that streamed nothing since is neither parsed nor copied again, and the copy each reader owns is
// independent of it. A text that changed, a text that is no longer a prefix of the last, a call of another identity with the same text, and an argument value that is not an object each read as the eager parse, whatever the
// cache holds, for the live view and for a shallow copy taken between the publications.
func TestSettledToolCallsAreReusedForAnUnchangedText(t *testing.T) {
	const (
		textA   = `{"path":"a.go","edits":[{"oldText":"x","newText":"y"},{"oldText":"é","newText":"日本"}],"n":1.5}`
		textB   = `{"content":"line one\nline two`
		grownB  = textB + ` and more\n"`
		restart = `{"other":[1,2,{"k":"v"}]}`
	)
	partial := &AssistantMessage{API: APIOpenAICompletions, Provider: "p", Model: "m", StopReason: StopReasonPending, Content: []AssistantContentBlock{deferredCall("a", textA), deferredCall("b", textB)}}
	cell := newAssistantMessageCell(partial)
	view := cell.view()

	first := view.Observe()
	settledArguments(t, "first read, call a", first.Content[0], "a", textA)
	settledArguments(t, "first read, call b", first.Content[1], "b", textB)
	// The copy belongs to the reader: rewriting it, at every depth, reaches nothing the cell holds.
	argumentsOfA := first.Content[0].(ToolCall).Arguments
	argumentsOfA["path"] = "rewritten"
	argumentsOfA["edits"].([]any)[0].(map[string]any)["newText"] = "rewritten"
	argumentsOfA["added"] = true
	delete(argumentsOfA, "n")
	heldArguments := cell.arguments[0].settled

	// Call a streams nothing more while call b grows: the cell publishes call a as the settled call it holds.
	partial.Content[1] = deferredCall("b", grownB)
	cell.publish(partial, assistantMessageReplacements{})
	cell.mu.RLock()
	held, ok := cell.current.Content[0].(ToolCall)
	sameMap := reflect.ValueOf(held.Arguments).Pointer() == reflect.ValueOf(heldArguments).Pointer()
	cell.mu.RUnlock()
	if !ok || held.deferredArguments != "" || !sameMap {
		t.Fatalf("a call that streamed nothing since was published as %#v, want the settled call sharing the cell's arguments", held)
	}
	second := view.Observe()
	settledArguments(t, "after call b grew, call a", second.Content[0], "a", textA)
	settledArguments(t, "after call b grew, call b", second.Content[1], "b", grownB)
	shallow := view.ShallowCopy()

	// Another call ID with the text of call a: the parse is the text's, the call is the new one. The producer assigns a new content array, so the shallow copy keeps the one it was taken with.
	partial.Content[0] = deferredCall("a2", textA)
	cell.publish(partial, assistantMessageReplacements{Content: true})
	third := view.Observe()
	settledArguments(t, "call a under another ID", third.Content[0], "a2", textA)
	settledArguments(t, "call a under another ID, call b", third.Content[1], "b", grownB)
	if cell.arguments[0].call.(ToolCall).ID != "a2" {
		t.Fatalf("the cell keeps %#v for the new call", cell.arguments[0].call)
	}

	// A text that does not extend the last one starts the parse over.
	partial.Content[0] = deferredCall("a2", restart)
	cell.publish(partial, assistantMessageReplacements{})
	settledArguments(t, "restarted text", view.Observe().Content[0], "a2", restart)

	// An argument value that is not an object: read, then published again unchanged, it is the same value.
	partial.Content = append(partial.Content, deferredCall("c", `[1,2,{"x":null}]`))
	for round := range 3 {
		cell.publish(partial, assistantMessageReplacements{})
		observed := view.Observe()
		call := observed.Content[2].(ToolCall)
		if call.Arguments != nil || string(call.nonObjectArguments) != `[1,2,{"x":null}]` || call.deferredArguments != "" {
			t.Fatalf("round %d: non-object call %#v", round, call)
		}
	}

	// The shallow copy kept the content of its own publication: calls a and b as they were when it was taken.
	kept := shallow.Observe()
	settledArguments(t, "shallow copy, call a", kept.Content[0], "a", textA)
	settledArguments(t, "shallow copy, call b", kept.Content[1], "b", grownB)
	if len(kept.Content) != 2 {
		t.Fatalf("shallow copy holds %d blocks, want the 2 it was taken with", len(kept.Content))
	}
}
