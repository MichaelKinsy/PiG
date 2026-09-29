package codingagent

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's native input callback can open a synchronous modal. Its input listener pass must release the pump before entering that callback; the remote-editor acknowledgement is not a native modal barrier.
func TestTerminalInputPassReleasesNativeKeyBeforeModalHandler(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	ticket := newInputTicket()
	called := false
	err := m.passTerminalInput(t.Context(), "\r", ticket, func(context.Context, string) error {
		called = true
		select {
		case <-ticket.done:
		default:
			t.Error("native modal would wait on an input pump held by its opening key")
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("called=%v err=%v", called, err)
	}
}

// Pi custom-editor.ts:137-141 and interactive-mode.ts:4116-4123,4458-4461 complete clear before the next input. A host echo of the local clear would erase that next input.
func TestRemoteEditorLocalClearDoesNotReplayOverNextKey(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	m.uiTaskCh = make(chan func(), 64)
	fake := &fakeRemoteEditor{}
	m.setRemoteEditor(fake)
	fake.calls = nil
	ticket := newInputTicket()
	if err := m.dispatchInputChunk(t.Context(), "\x03", ticket); err != nil {
		t.Fatal(err)
	}
	ticket.settle()
	select {
	case <-ticket.done:
		t.Fatal("input settled before the editor answered")
	default:
	}
	fake.host.EditorChanged("", "")
	fake.host.EditorAction(extension.RemoteEditorAction{Action: "app.clear", Text: "seed", Expanded: "seed", Local: true})
	fake.host.EditorInputDone()
	runPostedTasks(t, m)
	select {
	case <-ticket.done:
	default:
		t.Fatal("input barrier did not settle")
	}
	next := newInputTicket()
	if err := m.dispatchInputChunk(t.Context(), "x", next); err != nil {
		t.Fatal(err)
	}
	fake.host.EditorChanged("x", "x")
	fake.host.EditorInputDone()
	runPostedTasks(t, m)
	if !slices.Equal(fake.calls, []string{"input:\x03", "input:x"}) {
		t.Fatalf("host replayed editor mutation: %q", fake.calls)
	}
	if m.editor.Text() != "x" {
		t.Fatalf("next key lost: %q", m.editor.Text())
	}
	select {
	case <-next.done:
	default:
		t.Fatal("next input did not settle")
	}
}

func TestRemoteEditorDisconnectReleasesInput(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	m.uiTaskCh = make(chan func(), 64)
	fake := &fakeRemoteEditor{}
	m.setRemoteEditor(fake)
	ticket := newInputTicket()
	if err := m.dispatchInputChunk(t.Context(), "x", ticket); err != nil {
		t.Fatal(err)
	}
	fake.host.EditorClosed()
	runPostedTasks(t, m)
	select {
	case <-ticket.done:
	default:
		t.Fatal("disconnect retained input barrier")
	}
}
