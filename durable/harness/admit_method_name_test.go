package harness

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// Pi 32c71a6d4 renames Conversation.submit(tx, draft) and ConversationHandle.submit(tx, draft) to admit(tx, draft).
func TestConversationAndHandleExposeAdmitNotSubmitIn(t *testing.T) {
	conv := reflect.TypeFor[Conversation]()
	if _, ok := conv.MethodByName("Admit"); !ok {
		t.Fatalf("harness.Conversation has no Admit method; Pi 32c71a6d4 renamed submit(tx, draft) to admit(tx, draft)")
	}
	if _, ok := conv.MethodByName("SubmitIn"); ok {
		t.Fatalf("harness.Conversation still has SubmitIn method; want renamed to Admit")
	}
	handle := reflect.TypeFor[durable.ConversationHandle]()
	if _, ok := handle.MethodByName("Admit"); !ok {
		t.Fatalf("durable.ConversationHandle has no Admit method; Pi 32c71a6d4 renamed submit(tx, draft) to admit(tx, draft)")
	}
	if _, ok := handle.MethodByName("SubmitIn"); ok {
		t.Fatalf("durable.ConversationHandle still has SubmitIn method; want renamed to Admit")
	}
}
