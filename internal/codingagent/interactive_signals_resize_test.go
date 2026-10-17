//go:build linux || darwin

package codingagent

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// TestResizeHandlerFrameSeesTheNewSize: with the SIGWINCH handler installed the process terminal keeps its size between
// SIGWINCHs (as Node keeps process.stdout.columns and rows), and the handler drops the kept size before it posts the resize
// render, so the frame drawn for a SIGWINCH has the new geometry.
func TestResizeHandlerFrameSeesTheNewSize(t *testing.T) {
	master, slave := openTestPTY(t)
	go func() { _, _ = io.Copy(io.Discard, master) }()
	resize := func(cols, rows uint16) {
		t.Helper()
		if err := unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols}); err != nil {
			t.Fatal(err)
		}
	}
	resize(97, 31)

	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), Model: model})
	m.editor = tui.NewEditor()
	m.tuiInst = tui.NewTuiMainScreen(tui.NewProcessTerminal(slave, slave), nil, t.TempDir())
	m.tuiInst.Add(m.editor)
	ctx, cancel := context.WithCancel(context.Background())
	stop := m.installResizeHandler(ctx)
	t.Cleanup(func() { stop(); cancel() })

	m.tuiInst.Render()
	if m.tuiInst.Width() != 97 || m.tuiInst.Height() != 31 {
		t.Fatalf("first frame is %dx%d, want 97x31", m.tuiInst.Width(), m.tuiInst.Height())
	}
	resize(120, 40)
	m.tuiInst.Render()
	if m.tuiInst.Width() != 97 {
		t.Fatalf("a frame before the SIGWINCH is %d wide, want the kept 97", m.tuiInst.Width())
	}
	// The pseudo-terminal is not this process's controlling terminal, so the kernel does not signal it; deliver what a terminal
	// window change sends.
	if err := unix.Kill(os.Getpid(), unix.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	select {
	case task := <-m.uiTaskCh:
		task() // the resize work the handler posted to the owner loop: the editor cap and the render request
	case <-time.After(10 * time.Second):
		t.Fatal("the SIGWINCH handler posted no resize work")
	}
	m.tuiInst.Render()
	if m.tuiInst.Width() != 120 || m.tuiInst.Height() != 40 {
		t.Fatalf("the frame drawn for the SIGWINCH is %dx%d, want 120x40", m.tuiInst.Width(), m.tuiInst.Height())
	}
}
