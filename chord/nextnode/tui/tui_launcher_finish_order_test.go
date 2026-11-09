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
// given back, RESET_MODES written, the sockets closed) and its first await calls Sandbox.terminate(), which sets #ended at once
// (sandbox.ts:200-203). From then on the sandbox delivers nothing (:216, :229, `if (!this.#ended) this.#onMessage(message)`), so of the
// messages queued around the sandbox's exit, the ones before it are written, then RESET_MODES, and the ones after it are never handled.
// The launcher ran the prefix on a goroutine that posted it to the loop, behind the events already queued: a frame sent after the exit
// went out before RESET_MODES, and a message queued after it was handled before the terminal was given back.
func TestFramesAroundTheSandboxsExitFollowPisOrder(t *testing.T) {
	terminal := &orderTerminal{}
	base, err := url.Parse("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	l := newLauncher(context.Background(), base, "token", TerminalOptions{}, terminal)
	// The sandbox sent a frame, its exit, and another frame: all queued before the loop runs the first.
	l.post(func() { l.message(0, SandboxMessage{Kind: "write", Data: "before"}) })
	l.post(func() { l.message(0, SandboxMessage{Kind: "exit", Code: 0}) })
	l.post(func() { l.message(0, SandboxMessage{Kind: "write", Data: "after"}) })
	go l.loop()
	select {
	case code := <-l.exited:
		if code != 0 {
			t.Fatalf("exit code %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the launcher did not finish")
	}
	if got, want := terminal.written(), "before"+resetModes; got != want {
		t.Fatalf("the terminal was written %q, want the frame sent before the exit, then RESET_MODES, and nothing after: %q", got, want)
	}
}
