package inproc_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// jsonContextExtension is an extension of count subprocess-style handlers (AddJSONEventHandler: they read their event only as JSON) for event, each answering reply.
func jsonContextExtension(event string, count int, reply string) extension.Extension {
	ext := newFakeExtension("json-context")
	for i := range count {
		ext.AddJSONEventHandler(event, i+1, func(...any) (any, error) { return json.RawMessage(reply), nil })
	}
	return ext
}

func contextUser(text string) extension.AgentMessage {
	return extension.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText(text)}}
}

func contextText(message extension.AgentMessage) string {
	return string(message.User.Content.(ai.UserText))
}

// Source: runner.ts emitContext structuredClones the conversation for the handlers, so no handler's edit reaches the caller's messages, and a handler that returns nothing or the list it was given leaves the conversation as it was. A subprocess handler reads its event only as JSON: when every handler is one and none replied with a list, the runner has nothing to copy and hands the caller's own list back.
func TestContextDispatchOfUnchangedSubprocessHandlersCopiesNothing(t *testing.T) {
	messages := []extension.AgentMessage{contextUser("one"), contextUser("two")}
	runner := inproc.NewRunner([]extension.Extension{jsonContextExtension("context", 2, `{"_pigContextUnchanged":true}`)}, "")
	got, replaced, err := runner.EmitContextTracked(t.Context(), messages)
	if err != nil {
		t.Fatal(err)
	}
	if replaced || len(got) != 2 || &got[0] != &messages[0] {
		t.Fatalf("replaced = %t, got %d messages (the caller's list: %t)", replaced, len(got), len(got) > 0 && &got[0] == &messages[0])
	}
}

// A handler that reads Go values after subprocess handlers still gets a copy: its in-place edit never reaches the caller (structuredClone), and a subprocess handler's decoded list replaces the working list for the handlers after it.
func TestContextGoHandlerAfterSubprocessHandlersEditsACopy(t *testing.T) {
	messages := []extension.AgentMessage{contextUser("one"), contextUser("two")}
	exts := []extension.Extension{jsonContextExtension("context", 1, `{"_pigContextUnchanged":true}`)}
	goExt := newFakeExtension("go")
	goExt.Handlers["context"] = []extension.HandlerFn{func(args ...any) (any, error) {
		event := args[0].(extension.ContextEvent)
		event.Messages[0].User.Content = ai.UserText("edited")
		return nil, nil
	}}
	exts = append(exts, goExt)
	runner := inproc.NewRunner(exts, "")
	got, replaced, err := runner.EmitContextTracked(t.Context(), messages)
	if err != nil {
		t.Fatal(err)
	}
	if replaced {
		t.Fatal("an in-place edit is not a replacement")
	}
	if contextText(got[0]) != "edited" || contextText(messages[0]) != "one" {
		t.Fatalf("result %q, caller's message %q", contextText(got[0]), contextText(messages[0]))
	}
}

// A reply that carries a list (an in-place edit, or a replacement) is decoded and is what the next handler sees; an unchanged reply after it leaves that list in force.
func TestContextSubprocessReplyListIsUsedAndAnUnchangedReplyKeepsIt(t *testing.T) {
	messages := []extension.AgentMessage{contextUser("one"), contextUser("two")}
	ext := newFakeExtension("json-context")
	var seen []string
	ext.AddJSONEventHandler("context", 1, func(...any) (any, error) {
		return json.RawMessage(`{"messages":[{"role":"user","content":"kept","timestamp":0}],"_pigContextUnchanged":false}`), nil
	})
	ext.AddJSONEventHandler("context", 2, func(args ...any) (any, error) {
		for _, message := range args[0].(extension.ContextEvent).Messages {
			seen = append(seen, contextText(message))
		}
		return json.RawMessage(`{"_pigContextUnchanged":true}`), nil
	})
	runner := inproc.NewRunner([]extension.Extension{ext}, "")
	got, replaced, err := runner.EmitContextTracked(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}
	if !replaced || len(got) != 1 || contextText(got[0]) != "kept" || !reflect.DeepEqual(seen, []string{"kept"}) {
		t.Fatalf("replaced = %t, result %d messages, second handler saw %q", replaced, len(got), seen)
	}
	if len(messages) != 2 || contextText(messages[0]) != "one" {
		t.Fatal("the caller's messages changed")
	}
}
