package tui

import (
	"context"
	"io"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// orderTerminal is a terminal whose output is recorded.
type orderTerminal struct {
	nullTerminal
	mu  sync.Mutex
	out strings.Builder
}

func (o *orderTerminal) Output() io.Writer { return o }
func (o *orderTerminal) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.out.Write(p)
}
func (o *orderTerminal) written() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.out.String()
}

// Pi's `void this.finish(code)` runs its synchronous prefix in the caller's turn (launcher.ts:207-218: `#finished = true`, the terminal
// given back, RESET_MODES written, the sockets closed), so a frame the sandbox sent after its `exit` is handled after it. The launcher
// ran that prefix on a goroutine that posted it to the loop, behind the events already queued: the frame went out before RESET_MODES.
func TestAFrameQueuedBehindTheSandboxsExitIsWrittenAfterTheTerminalWasGivenBack(t *testing.T) {
	terminal := &orderTerminal{}
	base, err := url.Parse("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	l := newLauncher(context.Background(), base, "token", TerminalOptions{}, terminal)
	// The sandbox sent its exit and then a frame: both are queued before the loop runs the first.
	l.post(func() { l.message(0, SandboxMessage{Kind: "exit", Code: 0}) })
	l.post(func() { l.message(0, SandboxMessage{Kind: "write", Data: "frame"}) })
	go l.loop()
	select {
	case code := <-l.exited:
		if code != 0 {
			t.Fatalf("exit code %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the launcher did not finish")
	}
	if got := terminal.written(); !strings.HasPrefix(got, resetModes) {
		t.Fatalf("the terminal was written %q: RESET_MODES must come first", got)
	}
}
