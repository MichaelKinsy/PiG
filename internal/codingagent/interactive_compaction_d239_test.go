package codingagent

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// R6 (D239): replacing the session cancels a running compaction before it settles the run.
func TestSettleReplacedSessionCancelsCompaction(t *testing.T) {
	handle := &recordingCompactHandle{}
	m := &InteractiveMode{opts: InteractiveModeOptions{SessionHandle: handle}}
	if err := m.settleReplacedSession(); err != nil {
		t.Fatal(err)
	}
	if handle.abortCompactionCount != 1 {
		t.Fatalf("AbortCompaction calls = %d, want 1", handle.abortCompactionCount)
	}
	// A plain settle (abort/tree) never cancels it.
	if err := m.settleActiveRun(); err != nil || handle.abortCompactionCount != 1 {
		t.Fatalf("settleActiveRun: err = %v, aborts = %d", err, handle.abortCompactionCount)
	}
}

// R7 (D239): /tree during a compaction shows the waiting line and does not cancel it.
func TestNavigateTreeDuringCompactionShowsWaitingLine(t *testing.T) {
	handle := &recordingCompactHandle{}
	chat := tui.NewContainer()
	m := &InteractiveMode{
		chatContainer: chat,
		tuiInst:       tui.NewWithOutput(io.Discard, 120, 40),
		isCompacting:  true,
		opts:          InteractiveModeOptions{SessionHandle: handle},
	}
	_, _ = m.navigateTree(context.Background(), "x", false, "")
	if got := strings.Join(chat.Render(120), "\n"); !strings.Contains(got, "Waiting for the compaction to finish. Type /compact-cancel to stop it.") {
		t.Fatalf("waiting line missing: %q", got)
	}
	if handle.abortCompactionCount != 0 {
		t.Fatalf("AbortCompaction calls = %d, want 0", handle.abortCompactionCount)
	}
}

// R8 (D239): a refused second /compact shows "Already compacting" as an error.
func TestSecondCompactShowsAlreadyCompacting(t *testing.T) {
	handle := &recordingCompactHandle{compactErr: errors.New("Already compacting")}
	chat := tui.NewContainer()
	m := &InteractiveMode{
		chatContainer: chat,
		tuiInst:       tui.NewWithOutput(io.Discard, 120, 40),
		keybindings:   DefaultKeybindingsManager(),
		opts:          InteractiveModeOptions{SessionHandle: handle},
	}
	m.uiTaskCh = make(chan func(), 4)
	sc := m.buildSlashContext(t.Context())
	if err := sc.CompactSession(""); err != nil {
		t.Fatal(err)
	}
	select {
	case fn := <-m.uiTaskCh:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("no UI task posted")
	}
	if got := strings.Join(chat.Render(120), "\n"); !strings.Contains(got, "Already compacting") {
		t.Fatalf("error missing: %q", got)
	}
}
