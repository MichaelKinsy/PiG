//go:build unix

package codingagent

import (
	"slices"
	"testing"
)

// quitDisposeRuntime records which dispose a shutdown ran: the plain one, or the one of a mode whose TUI has stopped.
type quitDisposeRuntime struct {
	InteractiveRuntime
	order *[]string
}

func (h quitDisposeRuntime) EmitQuitShutdown() { *h.order = append(*h.order, "dispose") }

func (h quitDisposeRuntime) EmitQuitShutdownAfterStop() {
	*h.order = append(*h.order, "dispose after stop")
}

// Pi's quit by keys stops the TUI and then awaits runtimeHost.dispose() (interactive-mode.ts shutdown: drainInput, this.stop(), `await this.runtimeHost.dispose()`): stdin no
// longer keeps its event loop alive, so a dispose that waits for an aborted run whose author never reacts ends with the process (exit 0) when nothing else keeps the loop
// alive. The mode therefore runs that dispose as one whose input has ended, which is what lets the extension runtimes report a drained loop.
func TestQuitByKeysDisposesAsAModeWhoseInputEnded(t *testing.T) {
	var order []string
	m := upstreamShutdownMode(t, &order, false)
	m.runtimeHost = quitDisposeRuntime{order: &order}
	if err := m.finishInteractiveShutdown(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"drainInput", "stop", "dispose after stop"}; !slices.Equal(order, want) {
		t.Fatalf("the quit by keys ran %v, want %v: the dispose that follows the TUI's stop is the dispose of a mode whose input ended", order, want)
	}
}

// Pi's signal shutdown disposes BEFORE it stops the TUI (interactive-mode.ts shutdown({ fromSignal })): stdin still keeps its loop alive, so that dispose never ends with a
// drained loop, and the mode runs the plain dispose.
func TestSignalShutdownDisposesWhileItsInputIsAttached(t *testing.T) {
	var order []string
	m := upstreamShutdownMode(t, &order, false)
	m.runtimeHost = quitDisposeRuntime{order: &order}
	m.ShutdownFromSignal()
	if err := m.finishInteractiveShutdown(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"dispose", "drainInput", "stop"}; !slices.Equal(order, want) {
		t.Fatalf("the signal shutdown ran %v, want %v", order, want)
	}
}
