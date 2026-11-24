//go:build unix

package tui

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

type continueHandlerRegistration struct {
	fn func(owned func() bool)
}

var continueSignal struct {
	mu        sync.Mutex
	current   *continueHandlerRegistration
	owners    map[*continueOwnership]struct{}
	listening chan os.Signal // non-nil while the listener exists
	done      chan struct{}
	exited    chan struct{} // closed when the listener goroutine returns
}

// SetContinueSignalHandler sets the handler that runs on SIGCONT while a renderer or raw terminal owns the process terminal, from its start until its stop
// has restored the terminal, or clears it with nil. The returned remove function clears this registration only if it is still the active one, matching
// OnTerminalWriteError so a second InteractiveMode stopping does not clear a newer mode's handler. owned reports whether the process terminal is still owned
// when it is called, because the handler usually runs later on another goroutine. The handler runs on the signal goroutine and must not block. A SIGCONT
// while nothing owns the terminal (before the first start, after a stop, while a child such as an external editor owns it) reaches nobody and only continues
// the process.
//
// The terminal modes pig armed survive an external stop (kill -TSTP) and the shell takes the tty settings for its prompt; the handler is where a run
// arms them again after the continue.
// pig divergence (D661): see continue_signal.go. Pi's listener is for SIGTSTP and is the Ctrl+Z path; the default stop is the kernel's here.
func SetContinueSignalHandler(handler func(owned func() bool)) (remove func()) {
	var reg *continueHandlerRegistration
	if handler != nil {
		reg = &continueHandlerRegistration{fn: handler}
	}
	continueSignal.mu.Lock()
	continueSignal.current = reg
	exited := updateContinueListener()
	continueSignal.mu.Unlock()
	if exited != nil {
		<-exited
	}
	if reg == nil {
		return func() {}
	}
	return func() {
		continueSignal.mu.Lock()
		if continueSignal.current != reg {
			continueSignal.mu.Unlock()
			return
		}
		continueSignal.current = nil
		exited := updateContinueListener()
		continueSignal.mu.Unlock()
		if exited != nil {
			<-exited
		}
	}
}

func continueOwnersChanged(c *continueOwnership, owns bool) {
	continueSignal.mu.Lock()
	if owns {
		if continueSignal.owners == nil {
			continueSignal.owners = map[*continueOwnership]struct{}{}
		}
		if _, exists := continueSignal.owners[c]; exists {
			continueSignal.mu.Unlock()
			return
		}
		continueSignal.owners[c] = struct{}{}
	} else {
		if _, exists := continueSignal.owners[c]; !exists {
			continueSignal.mu.Unlock()
			return
		}
		delete(continueSignal.owners, c)
	}
	exited := updateContinueListener()
	continueSignal.mu.Unlock()
	if exited != nil {
		<-exited
	}
}

// terminalOwned reports whether any process-terminal owner holds the terminal now.
func terminalOwned() bool {
	continueSignal.mu.Lock()
	defer continueSignal.mu.Unlock()
	return len(continueSignal.owners) > 0
}

// updateContinueListener adds or removes the listener only when the answer changes, as Pi's updateSuspendSignalListener does. continueSignal.mu is held.
// When it stops the listener, it returns the channel closed when the listener goroutine exits so the caller can wait after releasing mu.
func updateContinueListener() <-chan struct{} {
	listen := len(continueSignal.owners) > 0 && continueSignal.current != nil
	if listen == (continueSignal.listening != nil) {
		return nil
	}
	if !listen {
		signal.Stop(continueSignal.listening)
		close(continueSignal.done)
		exited := continueSignal.exited
		continueSignal.listening, continueSignal.done, continueSignal.exited = nil, nil, nil
		return exited
	}
	signals := make(chan os.Signal, 1)
	done := make(chan struct{})
	exited := make(chan struct{})
	continueSignal.listening, continueSignal.done, continueSignal.exited = signals, done, exited
	signal.Notify(signals, syscall.SIGCONT)
	go func() {
		defer close(exited)
		for {
			select {
			case <-done:
				return
			case <-signals:
				continueSignal.mu.Lock()
				reg := continueSignal.current
				continueSignal.mu.Unlock()
				if reg != nil {
					reg.fn(terminalOwned)
				}
			}
		}
	}()
	return nil
}
