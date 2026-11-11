package subprocess

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

// A packed cell holds several runtimes and the Host numbers the run signal once for all of them (runSignals), while each runtime's sync reads the run for itself. Pi's ctx.signal is `agent.signal`, one object from the start of a run to its end (agent.ts:336-338), and ctx.abort() aborts it before it returns (agent.ts:340-342), so a handler that holds ctx.signal and then aborts must see the signal it holds aborted: its runtime must still hold the run it was sent when the abort frame arrives.
// A sync that reads the run before the other runtime's sync reads and numbers the next one, and applies what it read afterwards, takes the numbering back to the run that ended. The next sync of the live run then numbers it again, the runtime replaces the signal its handler holds with a new one, and the abort frame aborts that one.
// The test stops the first runtime's sync after its read, begins the next run, and syncs the second runtime. A Host whose read and numbering are one step cannot start the second sync inside that gap, so the second runtime's sync follows.
func TestRunSignalKeepsOneNumberForARunWhenAnotherRuntimesReadIsStale(t *testing.T) {
	var mu sync.Mutex
	var current context.Context
	var stallNext bool
	entered, gate := make(chan struct{}), make(chan struct{})
	h := newRunSignalHost(t, func() context.Context {
		mu.Lock()
		run, stall := current, stallNext
		stallNext = false
		mu.Unlock()
		if stall {
			close(entered)
			<-gate
		}
		return run
	})
	setRun := func(run context.Context) {
		mu.Lock()
		defer mu.Unlock()
		current = run
	}
	newConn := func(name string) (*Conn, net.Conn) {
		host, peer := net.Pipe()
		t.Cleanup(func() { _ = peer.Close() })
		conn := NewConn(name, host)
		conn.Start(t.Context())
		t.Cleanup(func() { _ = conn.Close("test done") })
		return conn, peer
	}
	reader, readerPeer := newConn("reader")
	holder, holderPeer := newConn("holder")
	expect := func(peer net.Conn, want runSignalFrame) {
		t.Helper()
		_ = peer.SetReadDeadline(time.Now().Add(5 * time.Second))
		if got := readRunSignal(t, peer); got != want {
			t.Fatalf("frame = %+v, want %+v", got, want)
		}
	}

	// Both runtimes hold the first run.
	first, endFirst := context.WithCancel(t.Context())
	defer endFirst()
	setRun(first)
	for _, conn := range []*Conn{reader, holder} {
		if err := h.syncRunSignal(conn); err != nil {
			t.Fatal(err)
		}
	}
	expect(readerPeer, runSignalFrame{Run: 1, Active: true})
	expect(holderPeer, runSignalFrame{Run: 1, Active: true})

	// The first run ends and the reader's sync reads that, then stops before it numbers what it read.
	setRun(nil)
	mu.Lock()
	stallNext = true
	mu.Unlock()
	readerDone := make(chan error, 1)
	go func() { readerDone <- h.syncRunSignal(reader) }()
	<-entered

	// The next run begins and the holder's runtime, which a request is about to serve, is synced. It runs inside the reader's gap unless the read and the numbering are one step.
	second, endSecond := context.WithCancel(t.Context())
	defer endSecond()
	setRun(second)
	readUnderTheNumberingLock := !h.runSignals.mu.TryLock()
	if !readUnderTheNumberingLock {
		h.runSignals.mu.Unlock()
	}
	holderDone := make(chan error, 1)
	go func() { holderDone <- h.syncRunSignal(holder) }()
	var holderErr error
	holderSynced := !readUnderTheNumberingLock
	if holderSynced {
		holderErr = <-holderDone
	}
	close(gate)
	if err := <-readerDone; err != nil {
		t.Fatal(err)
	}
	if !holderSynced {
		holderErr = <-holderDone
	}
	if holderErr != nil {
		t.Fatal(holderErr)
	}
	expect(holderPeer, runSignalFrame{Run: 2, Active: true})

	// The handler holds the second run's signal and calls ctx.abort(): the host cancels the run and answers the call, and the frame before the reply must abort the run the runtime holds.
	endSecond()
	h.replyToCall(nil, holder, "abort-call", &CallPayload{Method: "abort"}, t.Context(), &CallResultPayload{}, nil)
	expect(holderPeer, runSignalFrame{Run: 2, Active: true, Aborted: true})
	if reply := readLivenessEnvelope(t, holderPeer); reply.Type != MsgCallResult || reply.ID != "abort-call" {
		t.Fatalf("after the run_signal frame the peer read %+v, want the reply to the abort call", reply)
	}
}
