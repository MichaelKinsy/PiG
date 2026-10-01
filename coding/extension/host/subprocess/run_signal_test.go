package subprocess

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"
)

func newRunSignalHost(t *testing.T, current func() context.Context) *Host {
	t.Helper()
	bridge := NewUIBridge(func() {})
	bridge.SetHostAction("getSignal", current)
	h := NewHost(t.TempDir())
	h.SetUIBridge(bridge)
	return h
}

func readRunSignal(t *testing.T, peer net.Conn) runSignalFrame {
	t.Helper()
	env := readLivenessEnvelope(t, peer)
	if env.Type != MsgNotify || env.Notify == nil || env.Notify.Method != runSignalNotify {
		t.Fatalf("frame = %+v, want a run_signal notify", env)
	}
	var frame runSignalFrame
	if err := json.Unmarshal(env.Notify.Args, &frame); err != nil {
		t.Fatal(err)
	}
	return frame
}

// Pi's ctx.signal is `agent.signal` (agent-session.ts:3368, agent.ts:336-338): a run's signal is one object from the start of the run to its end, aborted with the run, and a new run has a new one. The Host numbers the runs and sends a runtime a frame only when its state changes.
func TestHostSendsTheRunSignalStateOncePerChange(t *testing.T) {
	var current context.Context
	h := newRunSignalHost(t, func() context.Context { return current })
	host, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	conn := NewConn("run-signal", host)
	conn.Start(t.Context())
	t.Cleanup(func() { _ = conn.Close("test done") })

	// No run: nothing to send. A frame here would be unread and the writer would block the next one.
	if err := h.syncRunSignal(conn); err != nil {
		t.Fatal(err)
	}
	first, abortFirst := context.WithCancel(context.Background())
	current = first
	for range 2 {
		if err := h.syncRunSignal(conn); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := readRunSignal(t, peer), (runSignalFrame{Run: 1, Active: true}); got != want {
		t.Fatalf("frame = %+v, want %+v", got, want)
	}
	// The abort reaches the runtime without a request: the watcher forwards it.
	h.mu.Lock()
	h.exts = map[string]*managedExt{"run-signal": {}}
	h.exts["run-signal"].setConnLocked(conn)
	h.mu.Unlock()
	abortFirst()
	if got, want := readRunSignal(t, peer), (runSignalFrame{Run: 1, Active: true, Aborted: true}); got != want {
		t.Fatalf("frame after the abort = %+v, want %+v", got, want)
	}
	// The run ends, then another begins.
	current = nil
	if err := h.syncRunSignal(conn); err != nil {
		t.Fatal(err)
	}
	if got, want := readRunSignal(t, peer), (runSignalFrame{Run: 1}); got != want {
		t.Fatalf("frame after the run ended = %+v, want %+v", got, want)
	}
	second := t.Context()
	current = second
	if err := h.syncRunSignal(conn); err != nil {
		t.Fatal(err)
	}
	if got, want := readRunSignal(t, peer), (runSignalFrame{Run: 2, Active: true}); got != want {
		t.Fatalf("frame of the next run = %+v, want %+v", got, want)
	}
}

// A request the run's abort cancels must return at once, as it did before the abort is forwarded: queueing the run signal ahead of the cancel frame must not wait for a peer that has stopped reading.
func TestRunSignalSyncDoesNotWaitForAPeerThatStoppedReading(t *testing.T) {
	run := t.Context()
	h := newRunSignalHost(t, func() context.Context { return run })
	host, peer := net.Pipe() // nobody reads peer
	t.Cleanup(func() { _ = peer.Close() })
	conn := NewConn("stuck", host)
	conn.Start(t.Context())
	t.Cleanup(func() { _ = conn.Close("test done") })
	done := make(chan error, 1)
	go func() { done <- h.syncRunSignal(conn) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("syncRunSignal waited for the peer to read the frame")
	}
}

// Pi aborts the run's AbortSignal synchronously (agent.ts:336-338, `activeRun.abortController.abort()`), so a handler that kept ctx.signal sees the abort even when the run ends at once. The Host forwards the abort from a watcher goroutine; if the run ends and another sync reaches the runtime first, that sync must still deliver the abort before it reports the run ended, or the runtime drops a signal that never aborts.
func TestRunSignalAbortSurvivesTheRunEndingBeforeTheWatcher(t *testing.T) {
	var mu sync.Mutex
	var current context.Context
	h := newRunSignalHost(t, func() context.Context {
		mu.Lock()
		defer mu.Unlock()
		return current
	})
	host, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	conn := NewConn("run-signal", host)
	conn.Start(t.Context())
	t.Cleanup(func() { _ = conn.Close("test done") })
	h.mu.Lock()
	h.exts = map[string]*managedExt{"run-signal": {}}
	h.exts["run-signal"].setConnLocked(conn)
	h.mu.Unlock()

	run, abort := context.WithCancel(context.Background())
	mu.Lock()
	current = run
	mu.Unlock()
	if err := h.syncRunSignal(conn); err != nil {
		t.Fatal(err)
	}
	if got, want := readRunSignal(t, peer), (runSignalFrame{Run: 1, Active: true}); got != want {
		t.Fatalf("frame = %+v, want %+v", got, want)
	}

	// Hold the watcher back (it takes h.mu first), then abort and end the run, and let a request's sync reach the runtime first.
	h.mu.Lock()
	abort()
	mu.Lock()
	current = nil
	mu.Unlock()
	synced := make(chan error, 1)
	go func() { synced <- h.syncRunSignal(conn) }()
	for _, want := range []runSignalFrame{{Run: 1, Active: true, Aborted: true}, {Run: 1}} {
		if got := readRunSignal(t, peer); got != want {
			h.mu.Unlock()
			t.Fatalf("frame = %+v, want %+v", got, want)
		}
	}
	if err := <-synced; err != nil {
		t.Fatal(err)
	}
	h.mu.Unlock()
	// The watcher then finds the runtime up to date and sends nothing more.
	if err := h.syncRunSignal(conn); err != nil {
		t.Fatal(err)
	}
	_ = peer.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	var buf [1]byte
	if n, err := peer.Read(buf[:]); n != 0 || err == nil {
		t.Fatalf("unexpected frame after the run ended: n=%d err=%v", n, err)
	}
}

// Pi's ctx.signal getter reads the run at the moment of the call (runner.ts:917-920, agent.ts:336-338), so a timer that fires after a run ended sees undefined, and one that fires after a run began sees its signal, though neither runtime was sent a request.
// The owner reports each change with UIBridge.RunSignalChanged and the Host brings every runtime up to date at once.
func TestRunSignalChangedReachesEveryRuntimeWithoutARequest(t *testing.T) {
	var mu sync.Mutex
	var current context.Context
	setRun := func(run context.Context) {
		mu.Lock()
		defer mu.Unlock()
		current = run
	}
	h := newRunSignalHost(t, func() context.Context {
		mu.Lock()
		defer mu.Unlock()
		return current
	})
	peers := map[string]net.Conn{}
	h.mu.Lock()
	h.exts = map[string]*managedExt{}
	for _, name := range []string{"first", "second"} {
		host, peer := net.Pipe()
		t.Cleanup(func() { _ = peer.Close() })
		conn := NewConn(name, host)
		conn.Start(t.Context())
		t.Cleanup(func() { _ = conn.Close("test done") })
		h.exts[name] = &managedExt{}
		h.exts[name].setConnLocked(conn)
		peers[name] = peer
	}
	h.mu.Unlock()
	bridge := h.uiBridge

	// Both runtimes read the frames together, as two processes do.
	expect := func(want runSignalFrame) {
		t.Helper()
		for name, peer := range peers {
			// A runtime the Host never told leaves the read waiting; fail rather than hang.
			_ = peer.SetReadDeadline(time.Now().Add(5 * time.Second))
			if got := readRunSignal(t, peer); got != want {
				t.Fatalf("%s: frame = %+v, want %+v", name, got, want)
			}
		}
	}
	first, abortFirst := context.WithCancel(t.Context())
	defer abortFirst()
	setRun(first)
	bridge.RunSignalChanged()
	expect(runSignalFrame{Run: 1, Active: true})

	setRun(nil)
	bridge.RunSignalChanged()
	expect(runSignalFrame{Run: 1})

	setRun(t.Context())
	bridge.RunSignalChanged()
	expect(runSignalFrame{Run: 2, Active: true})
}
