package codingagent

import (
	"io"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// interactive-mode.ts:2755 showExtensionConfirm: a confirmation shown in the editor slot (a resume into a missing cwd) reports a permission
// request named by its title alone, and clears it when the choice is made.
func TestEditorSlotConfirmationReportsPermissionStatus(t *testing.T) {
	input := make(chan []byte)
	m := &InteractiveMode{editor: tui.NewEditor(), editorContainer: tui.NewContainer(), layout: tui.NewContainer(), modalInputCh: input, runCtx: t.Context()}
	m.tuiInst = tui.NewWithOutput(io.Discard, 120, 40)
	m.tuiInst.SetFocus(m.editor)
	recorder := recordProgramStatus(m, "Run")

	confirmed := make(chan bool, 1)
	go func() {
		confirmed <- m.confirmSelection("Session cwd not found", "The stored cwd is gone. Continue in the current directory?")
	}()
	input <- []byte("x")
	input <- []byte("\x1b[1;1:3A") // a key release is dropped: the previous chunk is fully handled and rendered when this one is received
	want := tui.ProgramStatus{State: tui.ProgramStateBlocked, App: "pig", Kind: tui.ProgramStatusKindPermission, Message: "Session cwd not found"}
	if got := recorder.reports[len(recorder.reports)-1]; got != want {
		t.Fatalf("status while open = %+v, want %+v", got, want)
	}
	input <- []byte("\r")
	if !<-confirmed {
		t.Fatal("Enter on the first option (Yes) did not confirm")
	}
	if got := recorder.reports[len(recorder.reports)-1]; got != (tui.ProgramStatus{State: tui.ProgramStateIdle, App: "pig"}) {
		t.Fatalf("status after the choice = %+v, want idle", got)
	}
}
