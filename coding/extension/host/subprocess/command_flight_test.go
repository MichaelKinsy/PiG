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
