package cli

import (
	"os"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// rpcShutdown is rpc-mode.ts shutdown(). Stdin end, an extension's
// ctx.shutdown(), or a termination signal starts it once: it writes the
// Session events already published and stops writing later ones, disposes the
// runtime (session_shutdown, then AgentSession.dispose), waits for stdout, and
// exits. Only SIGTERM skips the stdout flush, during which the aborted run's
// in-process tail finishes.
//
// pig divergence (D234): follows Pi main ace560c30 ahead of the v1.5.0 pin. Once
// shutdown has started, a later stdin end, shutdown request or single signal
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

	// exiting is set when an exit starts; a later exit does nothing, except the repeated signal's.
	exiting atomic.Bool

	mu        sync.Mutex
	started   bool
	startedCh chan struct{}
}

func newRPCShutdown(detach, dispose, flush func(), exit func(code int)) *rpcShutdown {
	return &rpcShutdown{
		detach: detach, dispose: dispose, flush: flush, exit: exit,
		startedCh: make(chan struct{}),
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

// inputEnd handles stdin end. After shutdown started it does nothing: the started dispose finishes and exits (D234). It reports false in that case, so the caller leaves the process to that shutdown's exit.
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

// requested handles an extension's ctx.shutdown() once the gate's check passes. After shutdown started it does nothing (D234).
func (s *rpcShutdown) requested() {
	if s.begin() {
		s.flushAndExit(true, 0)
	}
}

// signal handles the first termination signal, as the handler rpc-mode.ts registers does: shutdown(128+signum, signal). It reports whether it started the shutdown: when one was already running, the signal does not interrupt it and the running shutdown's exit ends the process (D234).
func (s *rpcShutdown) signal(sig syscall.Signal) bool {
	tools.KillTrackedDetachedChildren()
	if !s.begin() {
		return false
	}
	s.flushAndExit(sig != syscall.SIGTERM, 128+int(sig))
	return true
}

// repeatedSignal handles a second or later termination signal: it exits at once with 128+signum, without waiting for the dispose or for stdout. Pi calls process.exit directly here (rpc-mode.ts signalCount > 1), so an exit that is already flushing a stuck stdout does not hold it back: the exiting guard is set, not consulted, and the exit skips the stdout wait (D234).
func (s *rpcShutdown) repeatedSignal(sig syscall.Signal) {
	tools.KillTrackedDetachedChildren()
	s.exiting.Store(true)
	exit := s.abort
	if exit == nil {
		exit = s.exit
	}
	exit(128 + int(sig))
}

// finish disposes the runtime when no trigger did, for a mode exit that had
// none. It does not exit.
func (s *rpcShutdown) finish() {
	s.begin()
}

// begin starts shutdown and disposes the runtime when no trigger has started
// it, and reports whether it did.
func (s *rpcShutdown) begin() bool {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return false
	}
	s.started = true
	close(s.startedCh)
	s.mu.Unlock()
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
