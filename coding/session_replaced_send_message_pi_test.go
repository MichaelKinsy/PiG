package coding

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestReplacedSessionContextSendMessageTargetsTheReplacementSession ports agent-session.ts:4375-4382
// createReplacedSessionContext: the context's sendMessage is the replacement session's sendCustomMessage, so a custom
// message sent from withSession is appended to the replacement session (agent-session.ts:2326) and not to the old one.
func TestReplacedSessionContextSendMessageTargetsTheReplacementSession(t *testing.T) {
	var oldSession *Session
	h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension {
		return extension.Extension{Commands: map[string]extension.RegisteredCommand{"repro": {Name: "repro", Description: "repro", Handler: func(ctx context.Context, _ string) error {
			oldContext := extension.CommandContextFromContext(ctx)
			_, err := oldContext.NewSession(&extension.NewSessionOptions{WithSession: func(replaced *extension.ReplacedSessionContext) error {
				return replaced.SendMessage(extension.CustomMessageRef{CustomType: "note", Content: "from withSession", Display: true, Details: map[string]any{"k": 1}}, nil)
			}})
			return err
		}}}}
	}})
	bindReplacementCommands(t, h.runtime)
	oldSession = h.runtime.Session()
	runtimePrompt(t, h.runtime, "/repro")
	if h.runtime.Session() == oldSession {
		t.Fatal("the runtime did not replace the session")
	}
	customTypes := func(session *Session) []string {
		var types []string
		for _, message := range session.Messages() {
			if message.Custom != nil {
				types = append(types, message.Custom["customType"].(string))
			}
		}
		return types
	}
	if got, want := customTypes(h.runtime.Session()), []string{"note"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("replacement custom messages = %v, want %v", got, want)
	}
	if got := customTypes(oldSession); len(got) != 0 {
		t.Fatalf("old session received %v, want nothing", got)
	}
}
