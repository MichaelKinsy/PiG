package codingagent

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// A keystroke goes from the terminal reader goroutine to the input pump, which frames it, routes it to the owner loop
// and waits for its ticket, and then to the owner, which waits for the pump's read batch to close before it paints: three
// to four goroutine hand-offs and two new channels per key. Framing on the reader and handing one batch per read to the
// owner leaves one hand-off per key (reader to owner) besides the test's own wake when the frame lands (TU-T12).
// GOMAXPROCS=1 makes every hand-off find its receiver parked, so the count is the number of hops. traceHops counts only
// the goroutines blocked in this module's code, not the test's own waits or the runtime's background goroutines, so a
// loaded machine moves the count only when a keystroke takes another hop. The test runs in its own process so that no
// other test's goroutines are alive to hand off (testenv.InOwnProcess).
func TestKeystrokeReachesTheOwnerInOneHandOff(t *testing.T) {
	if !testenv.InOwnProcess(t) {
		return
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	m := statusBorderMode(t, false)
	m.isIdle = true
	m.tuiInst.Add(m.editor)
	m.tuiInst.SetFocus(m.editor)
	probe := &paintProbe{frames: make(chan struct{}, 1)}
	m.tuiInst.Add(probe)
	m.installRenderDispatcher()
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readEnd.Close() }()
	defer func() { _ = writeEnd.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	m.backgroundCtx = ctx
	until, done := make(chan struct{}), make(chan error, 1)
	go func() { done <- m.inputLoopUntil(ctx, readEnd, until) }()
	defer func() {
		close(until)
		if err := <-done; err != nil {
			t.Errorf("input loop: %v", err)
		}
		cancel()
		// A Windows pipe is not a terminal: its handle reads as ready once a read has completed, so the reader that cancel woke
		// is parked in ReadFile and only the end of input frees it (tui/terminal_windows.go; interactive mode itself reads a
		// console). The context is already cancelled, so the end of input is not reported as a failure.
		_ = writeEnd.Close()
		if err := m.stopTerminalInput(); err != nil {
			t.Errorf("stop input: %v", err)
		}
	}()

	key := func() {
		if _, err := writeEnd.WriteString("a"); err != nil {
			t.Fatal(err)
		}
		select {
		case <-probe.frames:
		case <-time.After(5 * time.Second):
			t.Fatal("the keystroke never painted")
		}
		time.Sleep(20 * time.Millisecond) // past the frame throttle, so each key paints on its own
	}
	key()
	const keys = 40
	handoffs, started := traceHops(t, func() {
		for range keys {
			key()
		}
	})
	if got := m.editor.Text(); got != strings.Repeat("a", keys+1) {
		t.Fatalf("editor holds %q, want %d keys", got, keys+1)
	}
	perKey := float64(handoffs) / keys
	t.Logf("%.2f goroutine hand-offs per key, %.2f goroutines started per key", perKey, float64(started)/keys)
	if perKey > 1.5 {
		t.Fatalf("a keystroke takes %.2f goroutine hand-offs to reach the owner and paint, want 1 (reader to owner)", perKey)
	}
}
