package cli

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// rpcShutdown is rpc-mode.ts shutdown(). Stdin end, an extension's
// ctx.shutdown(), or a termination signal starts it once: it writes the
// Session events already published and stops writing later ones, disposes the
// runtime (session_shutdown, then AgentSession.dispose), waits for stdout, and
// exits. Only SIGTERM skips the stdout flush, during which the aborted run's
// in-process tail finishes.
//
// Once shutdown has started, a later stdin end, shutdown request or single signal
// does nothing, so the dispose finishes; a repeated signal exits at once with
// 128+signum (rpc-mode.ts signalCount). Pi 1.5.0 removed its signal handlers when
// shutdown() began, so the next signal killed the process mid-dispose, and a
// second shutdown() call exited 0 at once.
//
// An exit stops the extension processes first, and their exit completes a
// pending dispose or reports a drained event loop, which would exit 0 before
// the exit that started. The first exit that starts decides the status.
type rpcShutdown struct {
	// detach writes the Session events already published to stdout and
	// stops writing later ones.
	detach func()
	// dispose emits session_shutdown and disposes the Session.
	dispose func()
	// flush is flushRawStdout: the aborted run's in-process work finishes
	// before the process exits.
	flush func()
	exit  func(code int)
	// settle runs before stdin end starts shutdown: Pi reads stdin's end in an event-loop iteration of its own, after the microtasks of every earlier line and of a published agent_end (rpc-mode.ts:355-360,802-805). It returns false when the mode ended meanwhile, which abandons the shutdown. It may be nil.
	settle func() bool

	// abort ends the process for a repeated signal: it does not wait for stdout. It is nil in tests that need no separate path, and the repeated signal then uses exit.
	abort func(code int)

	// disposing is set by the caller that runs the dispose; disposed closes when that dispose has ended.
	disposing atomic.Bool
	disposed  chan struct{}

	// exiting is set when an exit starts; a later exit does nothing, except the repeated signal's.
	exiting atomic.Bool

	mu        sync.Mutex
	started   bool
	startedCh chan struct{}
}

func newRPCShutdown(detach, dispose, flush func(), exit func(code int)) *rpcShutdown {
	return &rpcShutdown{
		detach: detach, dispose: dispose, flush: flush, exit: exit,
		startedCh: make(chan struct{}), disposed: make(chan struct{}),
	}
}

// flushAndExit ends the process with code after an optional stdout flush. The first exit that starts decides the status, as the first process.exit does (rpc-mode.ts:728-744): stopping the extension processes inside an exit completes a pending dispose, whose own exit would otherwise race the first one's os.Exit.
func (s *rpcShutdown) flushAndExit(flush bool, code int) {
	if s.exiting.Load() {
		return
	}
	if flush {
		s.flush()
		if s.exiting.Load() {
			return
		}
	}
	if !s.exiting.CompareAndSwap(false, true) {
		return
	}
	s.exit(code)
}

// drained handles a Node event-loop drain after stdin ended: Pi's process exits 0 when its loop empties.
func (s *rpcShutdown) drained() { s.flushAndExit(false, 0) }

// Started returns a channel closed when a trigger has started shutdown.
func (s *rpcShutdown) Started() <-chan struct{} { return s.startedCh }

// inputEnd handles stdin end. After shutdown started it does nothing: the started dispose finishes and exits. It reports false in that case, so the caller leaves the process to that shutdown's exit.
func (s *rpcShutdown) inputEnd() bool {
	if s.settle != nil && !s.settle() {
		return true
	}
	if s.begin() {
		s.flushAndExit(true, 0)
		return true
	}
	return false
}

// requested handles an extension's ctx.shutdown() once the gate's check passes. After shutdown started it does nothing.
func (s *rpcShutdown) requested() {
	if s.begin() {
		s.flushAndExit(true, 0)
	}
}

// signal handles the first termination signal, as the handler rpc-mode.ts registers does: shutdown(128+signum, signal). It reports whether it started the shutdown: when one was already running, the signal does not interrupt it and the running shutdown's exit ends the process.
func (s *rpcShutdown) signal(sig syscall.Signal) bool {
	tools.KillTrackedDetachedChildren()
	if !s.claim() {
		return false
	}
	s.runSignal(sig)
	return true
}

// runSignal runs the shutdown a signal that claimed it started, and exits.
func (s *rpcShutdown) runSignal(sig syscall.Signal) {
	s.runDispose()
	s.flushAndExit(sig != syscall.SIGTERM, 128+int(sig))
}

// repeatedSignal handles a second or later termination signal: it exits at once with 128+signum, without waiting for the dispose or for stdout. Pi calls process.exit directly here (rpc-mode.ts signalCount > 1), so an exit that is already flushing a stuck stdout does not hold it back: the exiting guard is set, not consulted, and the exit skips the stdout wait.
func (s *rpcShutdown) repeatedSignal(sig syscall.Signal) {
	tools.KillTrackedDetachedChildren()
	s.exiting.Store(true)
	exit := s.abort
	if exit == nil {
		exit = s.exit
	}
	exit(128 + int(sig))
}

// finish disposes the runtime when nobody has, for a mode exit that had no trigger, or whose trigger claimed the shutdown and did not get to run it: a termination signal can claim it when it is recorded and find no hook to run it, because the mode is still starting or already tearing down. It does not exit, and does not wait for a dispose that is running.
func (s *rpcShutdown) finish() {
	s.claim()
	s.startDispose()
}

// begin starts shutdown and disposes the runtime when no trigger has started
// it, and reports whether it did.
func (s *rpcShutdown) begin() bool {
	if !s.claim() {
		return false
	}
	s.runDispose()
	return true
}

// claim marks the shutdown as started by the caller, which must then run it (runDispose). It reports false when another trigger started it.
func (s *rpcShutdown) claim() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return false
	}
	s.started = true
	close(s.startedCh)
	return true
}

// runDispose is the shutdown a trigger that claimed it runs: the events already published reach stdout, then the runtime is disposed. It runs once. A trigger that loses the race to another caller, the mode's finish, waits for that dispose to end before it returns, because its exit would otherwise end the process under the running session_shutdown handlers.
func (s *rpcShutdown) runDispose() {
	if !s.startDispose() {
		<-s.disposed
	}
}

// startDispose runs the dispose unless another caller has, and reports whether it ran it. It does not wait for a dispose that is running.
func (s *rpcShutdown) startDispose() bool {
	if !s.disposing.CompareAndSwap(false, true) {
		return false
	}
	defer close(s.disposed)
	s.detach()
	s.dispose()
	return true
}

// terminationSignals are the signals rpc-mode.ts handles.
func terminationSignals() []os.Signal {
	if runtime.GOOS == "windows" {
		return []os.Signal{syscall.SIGTERM}
	}
	return []os.Signal{syscall.SIGTERM, syscall.SIGHUP}
}

// repeatedTermination counts the termination signals that arrive while RPC mode runs. Main's handler takes the first one and runs the shutdown hook; every later one is a repeated signal that exits at once (rpc-mode.ts signalCount > 1).
type repeatedTermination struct {
	received chan os.Signal
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
}

// watchRepeatedTermination starts counting termination signals; repeat runs for each one after the first.
func watchRepeatedTermination(repeat func(syscall.Signal)) *repeatedTermination {
	w := &repeatedTermination{received: make(chan os.Signal, 8), stop: make(chan struct{}), done: make(chan struct{})}
	signal.Notify(w.received, terminationSignals()...)
	go func() {
		defer close(w.done)
		first := true
		for {
			select {
			case sig := <-w.received:
				if first {
					first = false
					continue
				}
				if number, ok := sig.(syscall.Signal); ok {
					repeat(number)
				}
			case <-w.stop:
				return
			}
		}
	}()
	return w
}

// Stop stops counting and joins the watcher.
func (w *repeatedTermination) Stop() {
	if w == nil {
		return
	}
	w.once.Do(func() {
		signal.Stop(w.received)
		close(w.stop)
		<-w.done
	})
}

// rpcExit ends the process once. Several exits can be in flight: a shutdown's own (after its dispose) and a repeated signal's. Each stops writing stdout first; the one that reaches the commit first owns the end and runs the cleanup, the exit.log line and the process exit, once. A later normal exit never returns, because the owner ends the process; a later repeated signal returns and leaves the end to the owner, which is past its stdout wait and about to exit (Pi's process.exit runs once, synchronously). The mode's own end can own the end too (endedByMode): then the process ends with the mode's return, and a normal exit returns without running anything.
type rpcExit struct {
	// closeStdout stops later records from being written.
	closeStdout func()
	// waitStdout waits for the stdout tail to be written.
	waitStdout func()
	// cleanup stops the extension processes and the profiles.
	cleanup func()
	// pig additive (D102): record writes the exit.log line of the exit that owns the end.
	record func(code int, forced bool)
	// end flushes the exit.log line and ends the process; it does not return in the process.
	end func(code int)

	mu    sync.Mutex
	state rpcExitState
	ended chan struct{}
}

type rpcExitState int

const (
	rpcExitOpen      rpcExitState = iota
	rpcExitCommitted              // an exit owns the end
	rpcExitModeEnded              // the mode's return ends the process
)

func newRPCExit(closeStdout, waitStdout, cleanup func(), record func(code int, forced bool), end func(code int)) *rpcExit {
	return &rpcExit{closeStdout: closeStdout, waitStdout: waitStdout, cleanup: cleanup, record: record, end: end, ended: make(chan struct{})}
}

// endedByMode makes the mode's own end, a return with the exit status of the signal that was recorded, the end of the process, and reports whether it did. It reports false when an exit already owns the end: that exit records itself.
func (e *rpcExit) endedByMode() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state != rpcExitOpen {
		return false
	}
	e.state = rpcExitModeEnded
	return true
}

// commit takes the end for a run and reports whether it owns it. A repeated signal takes it from the mode's end, whose return it forces; every other exit leaves it there.
func (e *rpcExit) commit(forced bool) (owner bool, wait bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case e.state == rpcExitOpen, e.state == rpcExitModeEnded && forced:
		e.state = rpcExitCommitted
		return true, false
	case e.state == rpcExitCommitted:
		return false, !forced
	}
	return false, false
}

// run ends the process with code. flushStdout waits for the stdout tail first, and forced is the exit of a repeated signal, which never waits for stdout.
func (e *rpcExit) run(code int, flushStdout, forced bool) {
	e.closeStdout()
	if flushStdout {
		e.waitStdout()
	}
	owner, wait := e.commit(forced)
	if !owner {
		if wait {
			<-e.ended
		}
		return
	}
	e.cleanup()
	e.record(code, forced)
	e.end(code)
	//portlint:allow doubleclose commit hands the end to one owner, the only run that gets here, so ended closes once
	close(e.ended)
}

// rpcTermination is the termination signal's part of RPC mode: the exit.log decision at the first signal, and the hook that runs the shutdown it started.
type rpcTermination struct {
	shutdown *rpcShutdown
	exit     *rpcExit
	// release ends the hold on the Session's agent events that the signal's prelude took.
	release func()
	// done is closed when the mode ends.
	done <-chan struct{}
	// received is the signal number main recorded.
	received func() syscall.Signal
	// pig additive (D102): recordEnding writes an exit.log line for the ending that ends the process.
	recordEnding func(reason string)

	decided, claimed atomic.Bool
	ignored          atomic.Int32
}

// record is the first signal's decision whether it starts the shutdown (setTerminationRecord): claiming it here makes the exit.log line and the shutdown agree.
func (t *rpcTermination) record(sig os.Signal) bool {
	t.decided.Store(true)
	if t.shutdown.claim() {
		t.claimed.Store(true)
		return true
	}
	if number, ok := sig.(syscall.Signal); ok {
		t.ignored.Store(int32(number))
	}
	return false
}

// hook runs the shutdown the first signal started, as the handler rpc-mode.ts registers does. It is registered before record: a signal between the two registrations finds no decision and claims the shutdown here, and one that claimed it at record time and finds no hook is run by finish.
func (t *rpcTermination) hook() {
	sig := t.received()
	started := t.claimed.Load()
	if !t.decided.Load() {
		started = t.shutdown.claim()
	}
	if started {
		t.shutdown.runSignal(sig)
		return
	}
	// A shutdown is already running and a single signal does not interrupt it. The prelude held the Session's agent events for this signal, and nothing else ends that hold: a run a session_shutdown handler starts would block on it while the dispose waits for the handler. Main cancels the root context, which owns the extension processes, once this hook returns, so it waits for the exit or for the mode's own end (a stdout write failure ends it during the dispose too).
	t.release()
	<-t.done
	// The mode ended while that shutdown ran, and no exit owns the end: the process ends with the mode's return, 128+signum. The ignored signal is the cause on record (D102).
	if number := syscall.Signal(t.ignored.Load()); number != 0 && t.exit.endedByMode() {
		t.recordEnding(fmt.Sprintf("received %s during shutdown, which the mode's end cut off (exit %d)", codingagent.SignalName(number), 128+int(number)))
	}
}
