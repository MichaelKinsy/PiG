package tui

import (
	"context"
	"os"
	"testing"
	"time"
)

// Pi consumes only the DA1 replies a keyboard protocol query owes (terminal.ts:298,313) and forwards the rest. A second DA1 reply on the same input, such as the one a pseudo console writes to the query of another process terminal, therefore reaches the application as the first key. These cases drive the production reader over a pipe with a DA1 reply ahead of typed keys.
func TestForwardTerminalForwardsADeviceAttributesReplyItDoesNotOwe(t *testing.T) {
	const reply = "\x1b[?62;4;52c"
	if got := forwardedAfterConsoleReply(t, reply, false, 2); got[0] != reply || got[1] != "a" {
		t.Fatalf("forwarded %q, want the unowed reply first and then a", got)
	}
}

func TestForwardTerminalConsumesTheDeviceAttributesReplyItsQueryOwes(t *testing.T) {
	const reply = "\x1b[?62;4;52c"
	got := forwardedAfterConsoleReply(t, reply, true, 2)
	if got[0] != "a" || got[1] != "b" {
		t.Fatalf("forwarded %q, want the typed keys only", got)
	}
}

// forwardedAfterConsoleReply types reply and then "ab" into a pipe read by a fresh terminal, with owed marking the reply as the answer to a query that terminal wrote, and returns the first count events it forwards.
func forwardedAfterConsoleReply(t *testing.T, reply string, owed bool, count int) []string {
	t.Helper()
	preserveKeyboardProtocolState(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	terminal := NewProcessTerminalWithOutput(r, nil, ioDiscard{})
	if owed {
		expectKeyboardProtocolReply(terminal)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	inputs := make(chan string, 8)
	go func() {
		defer close(done)
		terminal.forwardInput(ctx, func(data []byte) { inputs <- string(data) }, nil)
	}()
	defer func() { cancel(); _ = w.Close(); <-done }()
	// The reply arrives ahead of the typed keys.
	if _, err := w.WriteString(reply + "ab"); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, count)
	for len(got) < count {
		select {
		case input := <-inputs:
			got = append(got, input)
		case <-time.After(5 * time.Second):
			t.Fatalf("forwarded %q, want %d events", got, count)
		}
	}
	return got
}
