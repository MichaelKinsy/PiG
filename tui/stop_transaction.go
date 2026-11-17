package tui

// Ports packages/tui/src/tui.ts stop() (:1046-1056) and packages/tui/src/terminal.ts stop() as one unit of terminal output.
//
// Pi's stop() is one synchronous call on its one event loop: no timer, extension handler or clipboard callback can write between its bytes, and after
// a signal's stop() `process.exit(0)` (interactive-mode.ts shutdown, :4491-4494) ends the process before any callback runs again. A quit by keys
// writes more after stop(): `await this.runtimeHost.dispose()` and the resume hint (:4505-4513), and the loop's callbacks (a timer's setTitle, an
// OSC 52 copy: terminal.write has no stopped guard, terminal.ts:550) run during those awaits.
//
// Here the writers that are not the stop (an extension's title and clipboard copy, the progress indicator) run on goroutines of their own. The
// terminal owns the transaction that orders them against a stop, so no goroutine's bytes land inside the stop's:
//
//   - A stop takes the gate with BeginStop and ends it with End. It waits for the foreign writes already in their bytes; a foreign write is one
//     write of a title, a copy or a progress update, so the wait is as long as the terminal takes for it.
//   - A foreign write (WriteOutsideStop, which SetTitle and SetProgress use) never waits for a stop: it holds the read side of the gate for its own
//     bytes only. Made while a stop holds the gate or waits for it, it is queued and runs, in the order it was made, on the goroutine that ends the
//     stop, as the callback that Pi's loop runs after stop() does; End(true), a signal exit's stop, drops it, as process.exit(0) does.
//   - End(true) seals the terminal: a later foreign write is dropped and the progress indicator is not started again. The process-exit restore
//     (RestoreOnExit) seals it before it writes. Entering raw mode opens it again.
//
// Nothing that runs inside a transaction calls a foreign writer of the terminal: the stop's own writes go straight to the terminal
// (writeUnlogged, WriteStopModes, StopTransaction.SetProgress), so a stop never waits for itself. A foreign write waits for nothing, so the gate is
// no part of any lock cycle: the order is the mode's shutdownMu, then the gate, then the renderer's locks (rendererMu, mu, outMu), then the
// terminal's outMu; a foreign writer holds the read side and takes the terminal's outMu only.

import (
	"sync"
	"sync/atomic"
)

// StopGate is a terminal that writes its stop as one transaction.
type StopGate interface {
	// BeginStop takes the terminal for a stop and returns the transaction that its caller ends with End.
	BeginStop() *StopTransaction
	// WriteOutsideStop runs write, a write of a goroutine other than the one that stops the terminal, where it is ordered against a stop: now, or after
	// the stop that holds the terminal.
	WriteOutsideStop(write func())
}

// stopGate is the state of a terminal's stop transaction.
type stopGate struct {
	// mu is held for reading across the bytes of one foreign write and for writing across a stop.
	mu sync.RWMutex
	// queueMu guards stops, deferred and the drain that End runs. It is never held across a write.
	queueMu sync.Mutex
	// stops counts the stops that hold the gate or wait for it.
	stops int
	// deferred holds the foreign writes made while a stop held or awaited the gate, in the order they were made.
	deferred []func()
	// sealed is whether the terminal was handed back for good: foreign writes are dropped.
	sealed atomic.Bool
}

// StopTransaction is a stop that holds a terminal.
type StopTransaction struct {
	t    *ProcessTerminal
	done atomic.Bool
}

// BeginStop takes the terminal for a stop: it waits for the foreign writes in flight and for an earlier stop. Every later foreign write is queued until End.
func (t *ProcessTerminal) BeginStop() *StopTransaction {
	g := &t.stopGate
	g.queueMu.Lock()
	g.stops++
	g.queueMu.Unlock()
	g.mu.Lock()
	return &StopTransaction{t: t}
}

// End ends the stop. The foreign writes made during it run now in the order they were made, or are dropped when seal is set, which is a stop that the
// process ends after (a signal's). It is safe to call twice.
func (s *StopTransaction) End(seal bool) {
	if !s.done.CompareAndSwap(false, true) {
		return
	}
	g := &s.t.stopGate
	g.queueMu.Lock()
	if seal {
		g.sealed.Store(true)
	}
	for {
		batch := g.deferred
		g.deferred = nil
		if len(batch) == 0 {
			break
		}
		g.queueMu.Unlock()
		if !g.sealed.Load() {
			for _, write := range batch {
				write()
			}
		}
		g.queueMu.Lock()
	}
	g.stops--
	g.queueMu.Unlock()
	g.mu.Unlock()
}

// SetProgress is Terminal.SetProgress for the stop that holds the terminal: it writes at once, where the terminal's own would queue the write until the stop ends.
func (s *StopTransaction) SetProgress(active bool) { s.t.setProgress(active) }

// WriteOutsideStop is StopGate.WriteOutsideStop.
func (t *ProcessTerminal) WriteOutsideStop(write func()) {
	g := &t.stopGate
	for {
		if g.sealed.Load() {
			return
		}
		g.queueMu.Lock()
		if g.stops > 0 {
			g.deferred = append(g.deferred, write)
			g.queueMu.Unlock()
			return
		}
		g.queueMu.Unlock()
		if g.mu.TryRLock() {
			defer g.mu.RUnlock()
			if !g.sealed.Load() {
				write()
			}
			return
		}
		// A stop took the gate between the two checks; the next pass queues the write behind it.
	}
}

// sealForeignWrites drops every later foreign write: the exit restore's last act on the terminal.
func (t *ProcessTerminal) sealForeignWrites() { t.stopGate.sealed.Store(true) }

// openForeignWrites undoes the seal when the terminal is started again.
func (t *ProcessTerminal) openForeignWrites() { t.stopGate.sealed.Store(false) }
