package codingagent

import (
	"context"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

func TestInteractiveInputPumpHoldsNegotiationPrefixAfterFraming(t *testing.T) {
	// terminal.ts:setupStdinBuffer and readKeyboardProtocolNegotiationSequence hold ESC[ for50ms then150ms before delivering it to the editor.
	synctest.Test(t, func(t *testing.T) {
		m := &InteractiveMode{}
		r, w := io.Pipe()
		ctx, cancel := context.WithCancel(t.Context())
		readCh := make(chan inputChunk)
		errCh := make(chan error, 1)
		done := make(chan struct{})
		go func() { defer close(done); m.pumpTerminalInput(ctx, r, readCh, errCh) }()
		defer func() { cancel(); _ = w.Close(); _ = r.Close(); <-done }()
		if _, err := w.Write([]byte("a\x1b[")); err != nil {
			t.Fatal(err)
		}
		first := <-readCh
		if string(first.data) != "a" {
			t.Fatalf("first input=%q", first.data)
		}
		first.ticket.settle()
		synctest.Wait()
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
		select {
		case chunk := <-readCh:
			chunk.ticket.settle()
			t.Fatalf("prefix escaped at stdin deadline: %q", chunk.data)
		default:
		}
		time.Sleep(150 * time.Millisecond)
		synctest.Wait()
		select {
		case chunk := <-readCh:
			chunk.ticket.settle()
			if string(chunk.data) != "\x1b[" {
				t.Fatalf("replay=%q", chunk.data)
			}
		default:
			t.Fatal("prefix not replayed at negotiation deadline")
		}
	})
}

func TestInteractiveInputPumpSettlesListenersBeforeLaterNegotiation(t *testing.T) {
	// terminal.ts:setupStdinBuffer forwards each data event synchronously before processing later events in the same read.
	synctest.Test(t, func(t *testing.T) {
		previous := tui.IsKittyProtocolActive()
		t.Cleanup(func() { tui.SetKittyProtocolActive(previous) })
		tui.SetKittyProtocolActive(false)
		m := &InteractiveMode{}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		readCh := make(chan inputChunk)
		errCh := make(chan error, 1)
		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := m.pumpTerminalInput(ctx, strings.NewReader("a\x1b[?7ub"), readCh, errCh); err != nil {
				t.Error(err)
			}
		}()
		defer func() { cancel(); <-done }()
		first := <-readCh
		if string(first.data) != "a" || tui.IsKittyProtocolActive() {
			t.Fatal("later negotiation overtook the first listener")
		}
		synctest.Wait()
		if tui.IsKittyProtocolActive() {
			t.Fatal("negotiation advanced while the first listener was pending")
		}
		first.ticket.settle()
		second := <-readCh
		if string(second.data) != "b" || !tui.IsKittyProtocolActive() {
			t.Fatal("negotiation did not precede the second listener")
		}
		if first.readDone == nil || first.readDone != second.readDone {
			t.Fatal("one read lost its shared paint boundary")
		}
		select {
		case <-second.readDone:
			t.Fatal("read completed before its last listener settled")
		default:
		}
		second.ticket.settle()
		<-second.readDone
		if err := <-errCh; err != io.EOF {
			t.Fatalf("terminal error=%v, want EOF after the last event", err)
		}
	})
}

func TestInteractiveInputPumpRetainsFinalBytesBeforeEOF(t *testing.T) {
	m := &InteractiveMode{}
	readCh := make(chan inputChunk)
	errCh := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := m.pumpTerminalInput(t.Context(), iotest.DataErrReader(strings.NewReader("ab")), readCh, errCh); err != nil {
			t.Error(err)
		}
	}()
	var got strings.Builder
	for chunk := range readCh {
		got.Write(chunk.data)
		chunk.ticket.settle()
	}
	<-done
	if err := <-errCh; got.String() != "ab" || err != io.EOF {
		t.Fatalf("input=(%q,%v), want (ab,EOF)", got.String(), err)
	}
}

func BenchmarkInteractiveTerminalInputBurst(b *testing.B) {
	// Ordinary keys, arrows and a framed multi-line paste traverse the real pump and input-ticket handoff. No Session history is involved.
	payload := strings.Repeat("hello\x1b[A", 64) + "\x1b[200~pasted\ntext\x1b[201~"
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for b.Loop() {
		m := &InteractiveMode{}
		readCh := make(chan inputChunk)
		errCh := make(chan error, 1)
		go m.pumpTerminalInput(b.Context(), strings.NewReader(payload), readCh, errCh)
		for chunk := range readCh {
			chunk.ticket.settle()
		}
		if err := <-errCh; err != io.EOF {
			b.Fatal(err)
		}
	}
}
