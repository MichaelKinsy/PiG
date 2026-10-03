package subprocess

import (
	"slices"
	"testing"
)

// After stdin ends, the RPC flush joins each in-flight command until it responds or is suspended (rpc-mode.ts shutdown exits within microtasks and one tick, output-guard.ts:105-108). A Node runtime reports its own command suspended, apart from another runtime's. A runtime with no microtask continuation counts as suspended at the checkpoint. A suspended command's response is held once input ended, until a suspended session_shutdown handler keeps Pi's process alive.
func TestCommandFlightsSuspendPerCommand(t *testing.T) {
	h := NewHost(t.TempDir())
	var counts []int
	h.SetCommandSuspendHandler(func(n int) { counts = append(counts, n) })
	first := h.beginCommandFlight(&managedExt{nodeRuntime: true})
	second := h.beginCommandFlight(&managedExt{nodeRuntime: true})
	h.beginCommandFlight(&managedExt{})

	h.setCommandSuspended(first, true)
	h.setCommandSuspended(first, true)
	if want := []int{1}; !slices.Equal(counts, want) {
		t.Fatalf("suspended counts after the first Node command was reported = %v, want %v", counts, want)
	}
	if h.heldAfterInputEnd(first) {
		t.Fatal("a response was held before input ended")
	}
	h.EndInput()
	if !h.heldAfterInputEnd(first) || h.heldAfterInputEnd(second) {
		t.Fatal("after input ended only the suspended command's response must be held")
	}
	alive := h.quitSuspended()
	select {
	case <-alive:
		t.Fatal("no session_shutdown handler was suspended yet")
	default:
	}
	h.setQuitHandlerSuspended()
	select {
	case <-alive:
	default:
		t.Fatal("a suspended session_shutdown handler did not release the held responses")
	}
	h.FlushCommands()
	if want := []int{1, 2}; !slices.Equal(counts, want) {
		t.Fatalf("suspended counts after the checkpoint = %v, want %v: the native command runs at the checkpoint and the second Node command has not been reported", counts, want)
	}
	h.endCommandFlight(second)
	h.endCommandFlight(first)
	if want := []int{1, 2, 1}; !slices.Equal(counts, want) {
		t.Fatalf("suspended counts after commands returned = %v, want %v", counts, want)
	}
}

// A command whose runtime reported it suspended (a pending newSession closes the window) can still answer after stdin ended when a suspended quit handler keeps Pi's process alive. Its response frame precedes the runtime's drain notification, so from the frame's arrival the join must not count the command as unable to respond until the delivery goroutine holds it.
func TestCommandFlightAnsweredIsNotSuspendedUntilHeld(t *testing.T) {
	h := NewHost(t.TempDir())
	var counts []int
	h.SetCommandSuspendHandler(func(n int) { counts = append(counts, n) })
	flight := h.beginCommandFlight(&managedExt{nodeRuntime: true})
	h.setCommandSuspended(flight, true)
	h.setCommandAnswered(flight)
	h.setCommandHeld(flight, true)
	h.setCommandHeld(flight, false)
	h.endCommandFlight(flight)
	if want := []int{1, 0, 1, 0}; !slices.Equal(counts, want) {
		t.Fatalf("suspended counts = %v, want %v: suspended, answered, held, released", counts, want)
	}
}

// At the loop-drain checkpoint Pi's single loop stays alive for a child process, a session change or any other host call, in whichever runtime process the command runs, and a dialog does not keep it alive. The host counts those calls from the call frames it applies, not from the last request_state report. A command that waits only on dialogs counts suspended; one that also waits on another call does not, and counts again once that call returns. An answered command counts only when its response is held. A Node command counts suspended once its own process reported its loop drained, and not before.
func TestDrainCheckpointCountsDialogWaitsFromHostCalls(t *testing.T) {
	h := NewHost(t.TempDir())
	var counts []int
	h.SetCommandSuspendHandler(func(n int) { counts = append(counts, n) })
	dialog := h.beginCommandFlight(&managedExt{})
	concurrent := h.beginCommandFlight(&managedExt{})
	answered := h.beginCommandFlight(&managedExt{})
	late := h.beginCommandFlight(&managedExt{})
	nodeExt := &managedExt{nodeRuntime: true}
	node := h.beginCommandFlight(nodeExt)
	h.setHostCall(dialog, true, 1)
	h.setHostCall(concurrent, true, 1)
	h.setHostCall(concurrent, false, 1)
	h.setHostCall(answered, true, 1)
	h.setCommandAnswered(answered)
	h.setHostCall(node, true, 1)
	if len(counts) != 0 {
		t.Fatalf("suspended counts before the checkpoint = %v, want none: a dialog alone suspends nothing", counts)
	}

	h.markLoopDrained()
	if want := []int{1}; !slices.Equal(counts, want) {
		t.Fatalf("suspended counts at the loop drain of another process = %v, want %v: only the command that waits on a dialog alone", counts, want)
	}
	h.setHostCall(late, true, 1)
	if want := []int{1, 2}; !slices.Equal(counts, want) {
		t.Fatalf("suspended counts after a later dialog = %v, want %v", counts, want)
	}
	h.setHostCall(concurrent, false, -1)
	if want := []int{1, 2, 3}; !slices.Equal(counts, want) {
		t.Fatalf("suspended counts after the other call returned = %v, want %v: the dialog is all that is left", counts, want)
	}
	h.setHostCall(dialog, false, 1)
	if want := []int{1, 2, 3, 2}; !slices.Equal(counts, want) {
		t.Fatalf("suspended counts after an exec started = %v, want %v: a host call keeps Pi's loop alive", counts, want)
	}
	h.setCommandHeld(answered, true)
	if want := []int{1, 2, 3, 2, 3}; !slices.Equal(counts, want) {
		t.Fatalf("suspended counts with a held response = %v, want %v", counts, want)
	}
	h.setCommandDrained(node)
	if want := []int{1, 2, 3, 2, 3, 4}; !slices.Equal(counts, want) {
		t.Fatalf("suspended counts after the Node command's own process drained = %v, want %v", counts, want)
	}
}

// A Node process that reported its own loop drained cannot answer its unanswered commands, before the loop-drain checkpoint too: the report does not end the host, and the flush checkpoint reads the same count. An answered command counts only while its response is held.
func TestCommandsDrainedProcessCountsItsUnansweredCommands(t *testing.T) {
	h := NewHost(t.TempDir())
	var counts []int
	h.SetCommandSuspendHandler(func(n int) { counts = append(counts, n) })
	ext := &managedExt{nodeRuntime: true}
	other := h.beginCommandFlight(&managedExt{nodeRuntime: true})
	first := h.beginCommandFlight(ext)
	second := h.beginCommandFlight(ext)
	h.setCommandAnswered(second)
	h.setCommandDrained(first)
	h.setCommandDrained(second)
	if want := []int{1}; !slices.Equal(counts, want) {
		t.Fatalf("suspended counts = %v, want %v: the unanswered command of the drained process only", counts, want)
	}
	if h.draining {
		t.Fatal("a commands report took the loop-drain checkpoint")
	}
	h.setCommandHeld(second, true)
	h.endCommandFlight(first)
	h.endCommandFlight(other)
	if want := []int{1, 2, 1}; !slices.Equal(counts, want) {
		t.Fatalf("suspended counts = %v, want %v", counts, want)
	}
}

// runtime_quit_yield is Pi's exit at the stdout flush, so it applies the flush checkpoint and does not take the loop-drain checkpoint: a native command that is still running counts suspended, whichever of the flush and the yield comes first.
func TestQuitYieldKeepsTheFlushCheckpoint(t *testing.T) {
	h := NewHost(t.TempDir())
	var counts []int
	h.SetCommandSuspendHandler(func(n int) { counts = append(counts, n) })
	h.beginCommandFlight(&managedExt{})
	handled := 0
	h.SetRuntimeDrainHandler(func() { handled++ })
	h.applyRuntimeDrain(&managedExt{nodeRuntime: true}, notifyRuntimeQuitYield)
	if handled != 1 || !slices.Equal(counts, []int{1}) {
		t.Fatalf("handler ran %d times, suspended counts = %v, want once and [1]", handled, counts)
	}
	if h.draining {
		t.Fatal("a quit yield took the loop-drain checkpoint")
	}
	h.applyRuntimeDrain(&managedExt{nodeRuntime: true}, notifyRuntimeCommandsDrained)
	if handled != 1 {
		t.Fatal("a commands report ran the host's drain handler")
	}
	h.applyRuntimeDrain(&managedExt{nodeRuntime: true}, notifyRuntimeDrained)
	if handled != 2 || !h.draining {
		t.Fatalf("handler ran %d times, draining = %v, want the loop-drain checkpoint", handled, h.draining)
	}
}

// The flush checkpoint marks a still-running native command suspended. A command whose response already arrived settled before the checkpoint, as Pi's command that settled before its exit did, so its response is delivered and not held.
func TestFlushCommandsDoesNotHoldAnAnsweredNativeCommand(t *testing.T) {
	h := NewHost(t.TempDir())
	running := h.beginCommandFlight(&managedExt{})
	answered := h.beginCommandFlight(&managedExt{})
	h.setCommandAnswered(answered)
	h.EndInput()
	h.FlushCommands()
	if !h.heldAfterInputEnd(running) {
		t.Fatal("a native command still running at the checkpoint was not held")
	}
	if h.heldAfterInputEnd(answered) {
		t.Fatal("the checkpoint held the response of a command that answered before it")
	}
}
