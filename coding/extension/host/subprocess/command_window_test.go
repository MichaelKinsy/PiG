package subprocess

import (
	"slices"
	"testing"
)

// The window count is Pi's view when it reads stdin's end: a command a runtime reported suspended, or a command of a runtime with no microtask continuation that is still running, never answers; a Node command that has not reported, and any answered command, may still answer before the shutdown (rpc-mode.ts:802-805, output-guard.ts:105-108). The window handler receives it without applying the flush checkpoint to the flights.
func TestCommandWindowCountsOnlyCommandsPiLeavesUnanswered(t *testing.T) {
	h := NewHost(t.TempDir())
	var window, flush []int
	h.SetCommandWindowHandler(func(n int) { window = append(window, n) })
	h.SetCommandSuspendHandler(func(n int) { flush = append(flush, n) })
	node := h.beginCommandFlight(&managedExt{nodeRuntime: true})
	suspendedNode := h.beginCommandFlight(&managedExt{nodeRuntime: true})
	running := h.beginCommandFlight(&managedExt{})
	answered := h.beginCommandFlight(&managedExt{})

	h.notifyCommandSuspended()
	if want := []int{2}; !slices.Equal(window[len(window)-1:], want) {
		t.Fatalf("window count = %v, want %v: the native commands count as unanswered at stdin's end, a Node command does not until it reports", window, want)
	}
	h.setCommandAnswered(answered)
	if got := window[len(window)-1]; got != 1 {
		t.Fatalf("window count after a native command answered = %d, want 1", got)
	}
	h.setCommandSuspended(suspendedNode, true)
	if got := window[len(window)-1]; got != 2 {
		t.Fatalf("window count after a Node command reported suspended = %d, want 2", got)
	}
	if got := flush[len(flush)-1]; got != 1 {
		t.Fatalf("flush count = %d, want 1: the window's native command is not applied to the flush", got)
	}
	h.setCommandAnswered(node)
	h.endCommandFlight(running)
	if got := window[len(window)-1]; got != 1 {
		t.Fatalf("window count after the native command ended = %d, want 1", got)
	}
}

// A Node runtime reports a suspension after every earlier call of its connection applied, so one command waiting on a child process reports it only when the child exits. The window count follows the call frame instead: a Node command with a host call Pi's loop waits on is unanswered at stdin's end at once, and counts again as answerable when the call returns.
func TestCommandWindowCountsNodeCommandWaitingOnAHostCall(t *testing.T) {
	h := NewHost(t.TempDir())
	var window []int
	h.SetCommandWindowHandler(func(n int) { window = append(window, n) })
	node := h.beginCommandFlight(&managedExt{nodeRuntime: true})
	if len(window) != 0 {
		t.Fatalf("a Node command with no call changed the window count: %v", window)
	}
	h.setHostCall(node, false, 1)
	h.setHostCall(node, false, -1)
	h.setHostCall(node, true, 1)
	if want := []int{1, 0, 1}; !slices.Equal(window, want) {
		t.Fatalf("window counts = %v, want %v: exec starts, exec returns, dialog starts", window, want)
	}
}

// Pi reads stdin while an agent_before_settle or agent_settled handler waits on a timer or I/O. A Node handler counts as waiting once its runtime reports its window closed; a handler of a runtime with no microtask continuation counts once input ended, as FlushCommands counts such a runtime's commands, so stdin's end cannot wait forever for it. A report that arrives after the handler answered changes nothing.
func TestSettleTailCountsHandlersPiServesStdinDuring(t *testing.T) {
	h := NewHost(t.TempDir())
	var counts []int
	h.SetSettleTailHandler(func(n int) { counts = append(counts, n) })
	node := h.beginTailFlight(&managedExt{nodeRuntime: true})
	native := h.beginTailFlight(&managedExt{})
	if len(counts) != 0 {
		t.Fatalf("handlers that reported nothing before input end changed the count: %v", counts)
	}
	h.setTailSuspended(node)
	h.setTailSuspended(node)
	h.EndInput()
	h.endTailFlight(node)
	h.endTailFlight(native)
	h.setTailSuspended(node)
	if want := []int{1, 2, 1, 0}; !slices.Equal(counts, want) {
		t.Fatalf("settle-tail counts = %v, want %v: Node report, input end, Node answer, native answer", counts, want)
	}
	late := h.beginTailFlight(&managedExt{})
	h.endTailFlight(late)
	if want := []int{1, 2, 1, 0, 1, 0}; !slices.Equal(counts, want) {
		t.Fatalf("settle-tail counts = %v, want %v: a native handler after input end waits at once", counts, want)
	}
}
