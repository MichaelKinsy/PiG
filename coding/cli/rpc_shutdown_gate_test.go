package cli

import "testing"

// rpc-mode.ts checkShutdownRequested: shut down only when a request is pending, input is attached, no command is in flight and the session is idle; a request schedules the check on the next tick, which re-reads the conditions.
func TestRPCShutdownGate(t *testing.T) {
	type harness struct {
		gate    *rpcShutdownGate
		ticks   []func()
		idle    bool
		started bool
		fired   int
	}
	newHarness := func() *harness {
		h := &harness{idle: true}
		h.gate = newRPCShutdownGate()
		h.gate.bind(func(fn func()) { fn() }, func(fn func()) { h.ticks = append(h.ticks, fn) }, func() bool { return h.idle }, func() bool { return h.started }, func() { h.fired++; h.started = true })
		return h
	}
	tick := func(h *harness) {
		ticks := h.ticks
		h.ticks = nil
		for _, fn := range ticks {
			fn()
		}
	}
	t.Run("idle request waits for the next tick", func(t *testing.T) {
		h := newHarness()
		h.gate.InputReady()
		tick(h)
		h.gate.Request()
		if h.fired != 0 {
			t.Fatal("shutdown started before the tick")
		}
		tick(h)
		if h.fired != 1 {
			t.Fatalf("fired = %d, want 1", h.fired)
		}
	})
	t.Run("command that starts before the tick is waited for", func(t *testing.T) {
		h := newHarness()
		h.gate.InputReady()
		tick(h)
		h.gate.Request()
		h.gate.Begin()
		tick(h)
		if h.fired != 0 {
			t.Fatal("shutdown started while a command was in flight")
		}
		h.gate.End()
		tick(h)
		if h.fired != 1 {
			t.Fatalf("fired = %d, want 1 after the command finished", h.fired)
		}
	})
	t.Run("request before input is attached waits for it", func(t *testing.T) {
		h := newHarness()
		h.gate.Request()
		tick(h)
		if h.fired != 0 {
			t.Fatal("shutdown started before input was attached")
		}
		h.gate.InputReady()
		tick(h)
		if h.fired != 1 {
			t.Fatalf("fired = %d, want 1", h.fired)
		}
	})
	t.Run("busy session waits", func(t *testing.T) {
		h := newHarness()
		h.gate.InputReady()
		h.idle = false
		h.gate.Request()
		tick(h)
		if h.fired != 0 {
			t.Fatal("shutdown started while the session was busy")
		}
		h.idle = true
		h.gate.Check()
		tick(h)
		if h.fired != 1 {
			t.Fatalf("fired = %d, want 1", h.fired)
		}
	})
	t.Run("repeated request does nothing once shutdown started", func(t *testing.T) {
		h := newHarness()
		h.gate.InputReady()
		h.gate.Request()
		tick(h)
		h.gate.Request()
		tick(h)
		if h.fired != 1 {
			t.Fatalf("fired = %d, want 1", h.fired)
		}
	})
}
