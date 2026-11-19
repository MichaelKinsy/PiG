package subprocess

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// Pi's AgentSessionRuntime.dispose awaits the session_shutdown handlers and then disposes the session, which invalidates the extension runner
// (agent-session-runtime.ts:415-424, agent-session.ts:1477-1488, extensions/runner.ts:729-735): nothing an extension does afterwards reaches the
// session. An extension's connection has a goroutine of the Host that acts on what the extension sends and on the end of the connection, its crash
// notice among it. When Shutdown returns, none of those may still run and no notice may follow. These tests hold the situations that were found,
// under testing/synctest so that a loop is held exactly where the test says and Shutdown's wait for it is read from the bubble, not from a clock.

// loopRig is a host with one extension, a connection over a pipe whose far end the host's writes drain, and that far end, which the test closes to end the extension.
func loopRig(t *testing.T, name string) (*Host, *managedExt, net.Conn) {
	t.Helper()
	conn, peer := loopConn(t, name)
	h := NewHost(t.TempDir())
	me := withConn(&managedExt{
		config:     ExtConfig{Name: name},
		host:       h,
		supervisor: NewSupervisor(SupervisorConfig{MaxCrashes: 3, InitialDelay: time.Hour}),
	}, conn)
	h.exts[name] = me
	return h, me, peer
}

// loopConn is a started connection over a pipe whose far end is read and answers nothing. A silent peer fails its connection by the heartbeat; the
// intervals are far longer than a test, so only the peer's close or the host's ends the connection.
func loopConn(t *testing.T, name string) (*Conn, net.Conn) {
	t.Helper()
	hostEnd, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	go func() { _, _ = io.Copy(io.Discard, peer) }()
	conn := newConnWithOptions(name, hostEnd, connOptions{HeartbeatInterval: 24 * time.Hour, HeartbeatTimeout: 48 * time.Hour})
	conn.Start(t.Context())
	return conn, peer
}

// crashReportHeld sets a crash handler that holds the notice until unblock, and signals entered when a notice is in the handler. delivered is set when the handler returns.
// A test defers unblock so that a failed check leaves nothing held.
func crashReportHeld(h *Host) (entered <-chan struct{}, delivered *atomic.Bool, unblock func()) {
	in, release := make(chan struct{}), make(chan struct{})
	delivered = new(atomic.Bool)
	var once sync.Once
	h.SetCrashHandler(func(string, time.Duration, bool, string) {
		close(in)
		<-release
		delivered.Store(true)
	})
	return in, delivered, func() { once.Do(func() { close(release) }) }
}

// A connection's read loop signals that it has seen its connection end before it has done with it: the crash notice, the restart and the goroutine's own
// exit come after the signal. A crash that coincides with the quit has the loop in its notice when Shutdown starts, so Shutdown has to wait for the
// goroutine, not for the signal, or the notice reaches a session that is gone.
func TestShutdownWaitsForALoopThatIsReportingACrash(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, me, peer := loopRig(t, "crashy")
		entered, delivered, unblock := crashReportHeld(h)
		defer unblock()
		h.goHandleIncoming(me, me.connection())
		// The extension's side ends: its connection fails and the loop reports the crash.
		_ = peer.Close()
		<-entered
		done := make(chan struct{})
		go func() {
			defer close(done)
			h.Shutdown("quit")
		}()
		synctest.Wait()
		select {
		case <-done:
			t.Fatalf("Shutdown returned while the read loop of a connection was still reporting a crash (%d loops, notice delivered %v)", h.loopCount(), delivered.Load())
		default:
		}
		unblock()
		<-done
		if !delivered.Load() {
			t.Fatal("the crash notice was not delivered before Shutdown returned")
		}
		if loops := h.loopCount(); loops != 0 {
			t.Fatalf("after Shutdown %d connection loops of the host still run, want 0", loops)
		}
	})
}

// A crash that is found after Shutdown began is not reported: the loop's connection has failed and it waits for the process to be reaped, which is
// past its check of Shutdown; Shutdown begins and waits for the same process, and the process ends. The crash handler waits for the goroutine that
// called Shutdown, so a notice that began would hold Shutdown for the bound; none begins, and Shutdown returns at once.
func TestShutdownDoesNotReportACrashItFindsAfterItBegan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, me, peer := loopRig(t, "late")
		exited := make(chan struct{})
		me.exitedCh = exited
		me.waitErr = errors.New("signal: killed")
		ownerTurn := make(chan struct{}) // what the goroutine that called Shutdown gives once Shutdown has returned
		defer close(ownerTurn)
		var entered atomic.Bool
		h.SetCrashHandler(func(string, time.Duration, bool, string) {
			entered.Store(true)
			<-ownerTurn
		})
		h.goHandleIncoming(me, me.connection())
		_ = peer.Close()
		synctest.Wait() // the loop waits for the process
		start := time.Now()
		done := make(chan struct{})
		go func() {
			defer close(done)
			h.Shutdown("quit")
		}()
		synctest.Wait() // Shutdown has begun and waits for the process too
		close(exited)
		<-done
		if took := time.Since(start); took != 0 {
			t.Fatalf("Shutdown took %s, want it to return at once: a crash found after it began must not be reported", took)
		}
		if entered.Load() {
			t.Fatal("a crash notice began after Shutdown did")
		}
		if loops := h.loopCount(); loops != 0 {
			t.Fatalf("after Shutdown %d connection loops of the host still run, want 0", loops)
		}
	})
}

// A connection that no extension holds as its current one still has a loop of the Host: a second connection of an extension, which a reconnect or a
// generation a reload replaced leaves behind. Shutdown closes it and waits for its loop, as for the connections it stops.
func TestShutdownEndsTheLoopOfAConnectionNoExtensionHolds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, me, _ := loopRig(t, "twin")
		extra, _ := loopConn(t, "twin")
		defer func() { _ = extra.Close("test done") }() // a failed check must not leave its goroutines
		h.goHandleIncoming(me, me.connection())
		h.goHandleIncoming(me, extra)
		if me.connection() == extra || h.loopCount() != 2 {
			t.Fatalf("the host runs %d loops and the extension holds the extra connection %v, want 2 loops and the other connection", h.loopCount(), me.connection() == extra)
		}
		h.Shutdown("quit")
		if loops := h.loopCount(); loops != 0 {
			t.Fatalf("after Shutdown %d connection loops of the host still run, want 0", loops)
		}
	})
}

// A loop that does not end holds Shutdown for the bound the host puts on the stop of a process, and no longer.
func TestShutdownBoundsTheWaitForALoopThatDoesNotEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, me, peer := loopRig(t, "stuck")
		entered, _, unblock := crashReportHeld(h)
		defer unblock()
		h.goHandleIncoming(me, me.connection())
		_ = peer.Close()
		<-entered
		start := time.Now()
		h.Shutdown("quit")
		if took, bound := time.Since(start), h.processWaitDelay(); took != bound {
			t.Fatalf("Shutdown took %s with a loop that does not end, want the bound %s", took, bound)
		}
		if loops := h.loopCount(); loops != 1 {
			t.Fatalf("%d loops run after the bound, want the one that is held", loops)
		}
		unblock()
	})
}
