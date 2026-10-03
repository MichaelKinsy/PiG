package tui

import (
	"context"
	"os"
	"testing"
	"time"
)

// A pseudo console answers the keyboard protocol query that the test process writes when it enters raw mode, and the terminal under test reads that DA1 reply from the same console input ahead of the typed keys. Pi consumes only the DA1 replies a query owes (terminal.ts:262,272) and forwards the rest, so a terminal that does not record the owed reply forwards it as the first key. These cases drive the production reader over a pipe with the reply the console writes.
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

// forwardedAfterConsoleReply types reply and then "ab" into a pipe read by a fresh terminal, with owed marking the reply as the answer to a query that terminal owes, and returns the first count events it forwards.
func forwardedAfterConsoleReply(t *testing.T, reply string, owed bool, count int) []string {
	t.Helper()
	preserveKeyboardProtocolState(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	in := interactiveTestInput{file: r, queryReplyOwed: owed}
	terminal := NewProcessTerminalWithOutput(in.file, nil, ioDiscard{})
	in.ownQueryReply(terminal)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	inputs := make(chan string, 8)
	go func() {
		defer close(done)
		terminal.forwardInput(ctx, func(data []byte) { inputs <- string(data) }, nil)
	}()
	defer func() { cancel(); _ = w.Close(); <-done }()
	// The console writes the reply ahead of the typed keys.
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
