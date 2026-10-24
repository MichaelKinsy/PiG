package tui

import (
	"context"
	"io"
	"os"
	"sync"
	"testing"
	"time"
)

type negotiationWriter struct {
	seen chan struct{}
	once sync.Once
}

func (w *negotiationWriter) Write(p []byte) (int, error) {
	if string(p) == modifyOtherKeysEnable {
		w.once.Do(func() { close(w.seen) })
	}
	return len(p), nil
}

func preserveKeyboardProtocolState(t *testing.T) {
	t.Helper()
	kitty, modify, pushed := kittyProtocolActive.Load(), modifyOtherKeysActive.Load(), keyboardProtocolPushed.Load()
	kittyProtocolActive.Store(false)
	modifyOtherKeysActive.Store(false)
	keyboardProtocolPushed.Store(false)
	t.Cleanup(func() {
		kittyProtocolActive.Store(kitty)
		modifyOtherKeysActive.Store(modify)
		keyboardProtocolPushed.Store(pushed)
	})
}

// expectKeyboardProtocolReply marks the DA1 reply a keyboard protocol query would have written to this terminal as owed. Only that reply is negotiation; later DA1 replies answer other queries and are forwarded (terminal.ts:298,313).
func expectKeyboardProtocolReply(terminal *ProcessTerminal) {
	terminal.pendingKeyboardProtocolDeviceAttributes.Add(1)
}

// interactiveTestInput is the input a test reads through the production
// reader: the file the reader owns, send to type into it, and release to end
// a read that is still blocked when the test finishes. The input carries only
// what a test sends: no reply of its own precedes the first send.
type interactiveTestInput struct {
	file    *os.File
	send    func(t *testing.T, keys string)
	release func()
}

// The input holds only what a test sends. On Windows the child reads a pseudo console, which answers every query written to it, so a child that wrote the keyboard protocol query would find the console's DA1 reply ahead of the first key. A terminal that owes no reply forwards that reply (terminal.ts:262,313), and a test that types a DA1 reply of its own, such as TestStopAfterNegotiationOnlyInput, gets two for the one it owes.
func TestTerminalInputCarriesOnlyWhatIsSent(t *testing.T) {
	withTerminalInput(t, "TestTerminalInputCarriesOnlyWhatIsSent", 0, func(t *testing.T, in interactiveTestInput) {
		preserveKeyboardProtocolState(t)
		terminal := NewProcessTerminalWithOutput(in.file, nil, ioDiscard{})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		inputs := make(chan string, 4)
		go func() {
			defer close(done)
			terminal.forwardInput(ctx, func(data []byte) { inputs <- string(data) }, nil)
		}()
		defer func() { cancel(); in.release(); <-done }()
		in.send(t, "x")
		select {
		case got := <-inputs:
			if got != "x" {
				t.Fatalf("first input %q, want the x the test sent", got)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the key the test sent did not arrive")
		}
		// The console writes its reply within a millisecond of a query, so a quiet input here means the child wrote none.
		select {
		case got := <-inputs:
			t.Fatalf("input %q arrived that the test did not send", got)
		case <-time.After(100 * time.Millisecond):
		}
	})
}

// Pi terminal.ts:setupStdinBuffer consumes negotiation in its data callback and
// returns to the event loop. stop() does not wait for an unrelated key.
func TestStopAfterNegotiationOnlyInput(t *testing.T) {
	for i, reply := range []string{"\x1b[?62;22c", "\x1b[?0u", "\x1b[?0u\x1b[?62;22c"} {
		t.Run(reply, func(t *testing.T) {
			withTerminalInput(t, "TestStopAfterNegotiationOnlyInput", i, func(t *testing.T, in interactiveTestInput) {
				stopAfterNegotiationOnlyInput(t, reply, in)
			})
		})
	}
}

func stopAfterNegotiationOnlyInput(t *testing.T, reply string, in interactiveTestInput) {
	preserveKeyboardProtocolState(t)
	output := &negotiationWriter{seen: make(chan struct{})}
	terminal := NewProcessTerminalWithOutput(in.file, nil, output)
	expectKeyboardProtocolReply(terminal)
	ctx, cancel := context.WithCancel(t.Context())
	terminal.stopReader = cancel
	terminal.readerDone = make(chan struct{})
	done := terminal.readerDone
	inputs := make(chan string, 1)
	readErrors := make(chan error, 1)
	go func() {
		defer close(done)
		terminal.forwardInput(ctx, func(data []byte) { inputs <- string(data) }, func(err error) { readErrors <- err })
	}()
	defer func() {
		cancel()
		in.release()
		<-done
	}()
	in.send(t, reply)
	select {
	case <-output.seen:
	case <-time.After(5 * time.Second):
		t.Fatal("negotiation did not reach the production reader")
	}
	stopped := make(chan struct{})
	go func() { terminal.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		// A key ends the blocked read, so the failure is reported.
		in.send(t, "x")
		<-stopped
		t.Fatal("Stop blocked after negotiation-only input; the next read bypassed cancellable readiness")
	}
	select {
	case data := <-inputs:
		t.Fatalf("negotiation delivered as input: %q", data)
	case err := <-readErrors:
		t.Fatalf("cancellation reported as a read error: %v", err)
	default:
	}
	in.send(t, "x")
	data, err := readTestInput(in.file)
	if err != nil || string(data) != "x" {
		t.Fatalf("next terminal owner read %q, %v; want x", data, err)
	}
}

func TestReadInputWaitsPastNegotiationOnlyRead(t *testing.T) {
	preserveKeyboardProtocolState(t)
	terminal := NewProcessTerminalWithOutput(nil, nil, &negotiationWriter{seen: make(chan struct{})})
	expectKeyboardProtocolReply(terminal)
	r := &separateInputReads{chunks: []string{"\x1b[?0u", "\x1b[?62;22c", "x"}}
	got, err := readTestTerminalInput(terminal, r)
	if err != nil || string(got) != "x" || len(r.chunks) != 0 {
		t.Fatalf("blocking ReadInput = %q, %v; %d unread chunks", got, err, len(r.chunks))
	}
}

type separateInputReads struct{ chunks []string }

func (r *separateInputReads) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[0])
	r.chunks = r.chunks[1:]
	return n, nil
}
