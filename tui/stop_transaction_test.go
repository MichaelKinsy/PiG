package tui

import (
	"bytes"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Pi's tui.ts stop() (:1046-1056) is one synchronous call: no handler of Pi's event loop writes inside it, and after a signal's stop() process.exit(0)
// ends the process before any callback runs. The terminal's stop transaction (stop_transaction.go) gives the goroutines that are not the stop the same
// order: a title or a progress update made while a stop holds the terminal runs after it, or is dropped when the stop is a signal's.

func stopGateTerminal(t *testing.T) (*ProcessTerminal, *bytes.Buffer) {
	t.Helper()
	resetKeyboardProtocolState(false, false, false)
	t.Cleanup(func() { resetKeyboardProtocolState(false, false, false) })
	var out bytes.Buffer
	return NewProcessTerminalWithOutput(nil, nil, &out), &out
}

const stopTitleSeq = "\x1b]0;T\x07"

func TestForeignWriteMadeDuringAStopRunsAfterItInTheOrderItWasMade(t *testing.T) {
	term, out := stopGateTerminal(t)
	tx := term.BeginStop()
	term.writeUnlogged("STOP1;")
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		term.SetTitle("T")
		term.WriteOutsideStop(func() { term.writeUnlogged("SECOND;") })
	}()
	<-returned // a foreign write never waits for the stop
	term.writeUnlogged("STOP2;")
	tx.End(false)
	if got, want := out.String(), "STOP1;STOP2;"+stopTitleSeq+"SECOND;"; got != want {
		t.Fatalf("terminal received %q, want the stop's bytes whole, then the foreign writes in the order they were made: %q", got, want)
	}
	term.SetTitle("T")
	if got, want := out.String(), "STOP1;STOP2;"+stopTitleSeq+"SECOND;"+stopTitleSeq; got != want {
		t.Fatalf("a write after an ended stop is written where it is made: %q, want %q", got, want)
	}
}

func TestSealedStopDropsTheForeignWritesMadeDuringItAndAfterIt(t *testing.T) {
	term, out := stopGateTerminal(t)
	tx := term.BeginStop()
	term.writeUnlogged("STOP;")
	term.SetTitle("T")
	term.SetProgress(true)
	tx.End(true)
	term.SetTitle("T")
	term.SetProgress(true)
	term.WriteOutsideStop(func() { term.writeUnlogged("LATE;") })
	if got, want := out.String(), "STOP;"; got != want {
		t.Fatalf("terminal received %q after a signal's stop, want only %q: Pi's process.exit(0) runs no callback after stop()", got, want)
	}
	if term.progressActiveForExit() {
		t.Fatal("a progress update made after the sealed stop started the keepalive again")
	}
}

func TestStopTransactionEndsOnceAndTheNextStopTakesTheTerminalAgain(t *testing.T) {
	term, out := stopGateTerminal(t)
	for range 2 {
		tx := term.BeginStop()
		term.SetTitle("T")
		tx.End(false)
		tx.End(true) // a second End is a no-op: it neither drops nor seals
		term.SetTitle("T")
	}
	if got, want := out.String(), strings.Repeat(stopTitleSeq, 4); got != want {
		t.Fatalf("terminal received %q, want %q", got, want)
	}
}

func TestStopTransactionProgressClearIsWrittenInsideTheStop(t *testing.T) {
	term, out := stopGateTerminal(t)
	tx := term.BeginStop()
	tx.SetProgress(false)
	tx.End(false)
	if got := out.String(); got != terminalProgressClearSeq {
		t.Fatalf("the stop's own progress clear = %q, want %q written at once, not queued behind itself", got, terminalProgressClearSeq)
	}
}

func TestRestoreOnExitSealsTheTerminalAgainstForeignWrites(t *testing.T) {
	term, out := stopGateTerminal(t)
	term.RestoreOnExit("BEFORE;", "AFTER;")
	written := out.String()
	term.SetTitle("T")
	term.SetProgress(true)
	if got := out.String(); got != written {
		t.Fatalf("a write made after the process exit's restore reached the terminal: %q, want %q: process.exit() runs no callback", got, written)
	}
}

// A storm of foreign writers must not put a byte inside the stop's writes, or after a sealed stop.
func TestForeignWriteStormHoldsNoByteInsideAStop(t *testing.T) {
	term, out := stopGateTerminal(t)
	var running atomic.Bool
	running.Store(true)
	var started, ended sync.WaitGroup
	for range 4 {
		started.Add(1)
		ended.Go(func() {
			started.Done()
			for running.Load() {
				term.SetTitle("T")
				term.SetProgress(true)
			}
		})
	}
	started.Wait()
	tx := term.BeginStop()
	const parts = 50
	for i := range parts {
		term.writeUnlogged("S")
		if i%5 == 0 {
			runtime.Gosched()
		}
	}
	tx.End(true)
	running.Store(false)
	ended.Wait()
	written := out.String()
	first, last := strings.Index(written, "S"), strings.LastIndex(written, "S")
	if first < 0 || strings.Count(written[first:last+1], "S") != parts {
		t.Fatalf("the stop's %d writes are not whole in %q", parts, written)
	}
	if inside := written[first : last+1]; strings.ReplaceAll(inside, "S", "") != "" {
		t.Fatalf("a foreign write landed inside the stop: %q", inside)
	}
	if tail := written[last+1:]; tail != "" {
		t.Fatalf("a foreign write landed after a sealed stop: %q", tail)
	}
}
