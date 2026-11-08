package codingagent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// upstream: agent-session.ts appendMessage stores the message object message_end carried, so Paseo's `entry.message === message` finds an entry in the interactive TUI as in print mode.
//
// A Node extension learns which entry a message_end message persisted through the host action getMessageEntries (SessionStatePayload.messageEntries). The interactive mode binds it to the live session, as BindSessionReadActions does for the other modes: an interactive session that named no entry would leave the extension without its entry.message === message.
func TestInteractiveModeNamesTheEntriesMessageEndPersisted(t *testing.T) {
	bridge := &captureUIBridge{}
	inner := NewSession("interactive", t.TempDir())
	m := &InteractiveMode{opts: InteractiveModeOptions{SubprocessUIBridge: bridge, SessionHandle: &recordingCompactHandle{inner: inner}}}
	detach := m.wireSubprocessHostCallbacks()
	defer detach()
	get, ok := bridge.actions["getMessageEntries"].(func(int) ([]sessionentry.MessageSeq, int))
	if !ok {
		t.Fatalf("getMessageEntries action = %T", bridge.actions["getMessageEntries"])
	}
	if pairs, next := get(0); len(pairs) != 0 || next != 0 {
		t.Fatalf("a session that persisted nothing named %v (cursor %d)", pairs, next)
	}
	inner.NoteMessageEntry("a1b2c3d4", 7)
	inner.NoteMessageEntry("e5f6a7b8", 9)
	want := []sessionentry.MessageSeq{{ID: "a1b2c3d4", Seq: 7}, {ID: "e5f6a7b8", Seq: 9}}
	if pairs, next := get(0); !reflect.DeepEqual(pairs, want) || next != 2 {
		t.Errorf("the interactive session named %v (cursor %d), want %v (cursor 2)", pairs, next, want)
	}
	if pairs, next := get(1); !reflect.DeepEqual(pairs, want[1:]) || next != 2 {
		t.Errorf("from cursor 1 the session named %v (cursor %d), want %v", pairs, next, want[1:])
	}
}
