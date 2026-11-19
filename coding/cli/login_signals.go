package cli

import (
	"context"
	"os"
	"os/signal"
	"sync"
)

// commandSignals is the one cancellation source of a terminal authentication command. The first SIGINT, SIGTERM or SIGHUP cancels ctx, which every step of
// the command runs under, and is recorded for the command's end to reproduce with exitProcessBySignal once its cleanup is done. A further signal ends the process
// at once, without that cleanup, so a cleanup that stops answering never holds the process. A signal the process inherited as ignored (nohup's SIGHUP) stays ignored.
type commandSignals struct {
	ctx      context.Context
	cancel   context.CancelFunc
	incoming chan os.Signal
	stopped  chan struct{}
	stopOnce sync.Once

	mu    sync.Mutex
	first os.Signal
}

// commandTerminationSignals are the signals that end a terminal authentication command.
func commandTerminationSignals() []os.Signal { return append(terminationSignals(), os.Interrupt) }

// notifyCommandSignals starts the source under parent. Stop it when the command ends without a signal.
func notifyCommandSignals(parent context.Context, signals ...os.Signal) *commandSignals {
	ctx, cancel := context.WithCancel(parent)
	s := &commandSignals{ctx: ctx, cancel: cancel, incoming: make(chan os.Signal, 1), stopped: make(chan struct{})}
	listened := make([]os.Signal, 0, len(signals))
	for _, sig := range signals {
		if !signal.Ignored(sig) {
			listened = append(listened, sig)
		}
	}
	signal.Notify(s.incoming, listened...)
	//portlint:allow golifetime watch ends on the first signal pair or when Stop closes stopped, which every command path calls
	go s.watch()
	return s
}

func (s *commandSignals) watch() {
	select {
	case sig := <-s.incoming:
		s.mu.Lock()
		s.first = sig
		s.mu.Unlock()
		s.cancel()
	case <-s.stopped:
		return
	}
	select {
	case sig := <-s.incoming:
		dieBySignal(sig)
	case <-s.stopped:
	}
}

// Fired reports the first signal received, if any.
func (s *commandSignals) Fired() (os.Signal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.first, s.first != nil
}

// Stop ends the source: later signals take the process's default action again.
func (s *commandSignals) Stop() {
	s.stopOnce.Do(func() {
		signal.Stop(s.incoming)
		close(s.stopped)
		s.cancel()
	})
}
