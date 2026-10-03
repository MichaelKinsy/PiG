package main

import (
	"os"
	"slices"
	"testing"
)

// Pi starts shutdown() for stdin's end in an event-loop iteration after the line, so the microtasks of earlier commands and of a published agent_end finish first (rpc-mode.ts:355-360,726-742,802-805). Stdin end therefore settles before it removes the signal handlers, and a settle that reports the mode ended abandons the shutdown.
func TestRPCShutdownInputEndSettlesBeforeShutdownStarts(t *testing.T) {
	build := func(order *[]string, settle func() bool) *rpcShutdown {
		step := func(name string) func() { return func() { *order = append(*order, name) } }
		s := newRPCShutdown(step("remove"), step("detach"), step("dispose"), step("flush"), func(int) { *order = append(*order, "exit") }, func(os.Signal) {})
		s.settle = settle
		return s
	}
	var order []string
	build(&order, func() bool { order = append(order, "settle"); return true }).inputEnd()
	if want := []string{"settle", "remove", "detach", "dispose", "flush", "exit"}; !slices.Equal(order, want) {
		t.Fatalf("stdin end ran %v, want %v", order, want)
	}
	order = nil
	build(&order, func() bool { order = append(order, "settle"); return false }).inputEnd()
	if want := []string{"settle"}; !slices.Equal(order, want) {
		t.Fatalf("an abandoned settle ran %v, want %v", order, want)
	}
}
