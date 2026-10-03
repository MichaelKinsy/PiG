package main

import (
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
)

// rpcShutdown is rpc-mode.ts shutdown(). Stdin end, an extension's
// ctx.shutdown(), or a termination signal starts it once: it removes the
// signal handlers, writes the Session events already published and stops
// writing later ones, disposes the runtime (session_shutdown, then
// AgentSession.dispose), waits for stdout, and exits. Only SIGTERM skips the
// stdout flush, during which the aborted run's in-process tail finishes.
//
// Once shutdown has started, a termination signal takes Node's default
// action, and stdin end or a shutdown request re-enters shutdown(), which
// exits 0 at once. Stdin end after the dispose finished does nothing, because
// shutdown() detached the input then.
type rpcShutdown struct {
	// removeSignalHandlers makes a later termination signal take its
	// default action.
	removeSignalHandlers func()
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
	// die ends the process with a termination signal's default action.
	die func(sig os.Signal)

	mu        sync.Mutex
	started   bool
	startedCh chan struct{}
	disposed  chan struct{}
}

func newRPCShutdown(removeSignalHandlers, detach, dispose, flush func(), exit func(code int), die func(os.Signal)) *rpcShutdown {
	return &rpcShutdown{
		removeSignalHandlers: removeSignalHandlers, detach: detach, dispose: dispose, flush: flush, exit: exit, die: die,
		startedCh: make(chan struct{}), disposed: make(chan struct{}),
	}
}

// Started returns a channel closed when a trigger has started shutdown.
func (s *rpcShutdown) Started() <-chan struct{} { return s.startedCh }

// inputEnd handles stdin end.
func (s *rpcShutdown) inputEnd() {
	if s.settle != nil && !s.settle() {
		return
	}
	if s.begin() {
		s.flush()
		s.exit(0)
		return
	}
	select {
	case <-s.disposed:
	default:
		s.exit(0)
	}
}

// requested handles an extension's ctx.shutdown() once upstream checks for
// it: after a command's response and at agent_settled.
func (s *rpcShutdown) requested() {
	if s.begin() {
		s.flush()
	}
	s.exit(0)
}

// signal handles a termination signal, as the handler rpc-mode.ts registers
// does: shutdown(128+signum, signal).
func (s *rpcShutdown) signal(sig syscall.Signal) {
	if !s.begin() {
		s.die(sig)
		return
	}
	if sig != syscall.SIGTERM {
		s.flush()
	}
	s.exit(128 + int(sig))
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
	s.removeSignalHandlers()
	s.detach()
	s.dispose()
	close(s.disposed)
	return true
}

// terminationSignals are the signals rpc-mode.ts handles.
func terminationSignals() []os.Signal {
	if runtime.GOOS == "windows" {
		return []os.Signal{syscall.SIGTERM}
	}
	return []os.Signal{syscall.SIGTERM, syscall.SIGHUP}
}

// forcedTermination stands in for the default action upstream restores when
// shutdown() removes its signal handlers: a termination signal after that ends
// the process at once, without further cleanup.
type forcedTermination struct {
	received chan os.Signal
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
}

// watchForcedTermination starts watching for termination signals; die ends
// the process.
func watchForcedTermination(die func(os.Signal)) *forcedTermination {
	w := &forcedTermination{received: make(chan os.Signal, 1), stop: make(chan struct{}), done: make(chan struct{})}
	signal.Notify(w.received, terminationSignals()...)
	go func() {
		defer close(w.done)
		select {
		case sig := <-w.received:
			die(sig)
		case <-w.stop:
		}
	}()
	return w
}

// Stop stops watching and joins the watcher.
func (w *forcedTermination) Stop() {
	if w == nil {
		return
	}
	w.once.Do(func() {
		signal.Stop(w.received)
		close(w.stop)
		<-w.done
	})
}
