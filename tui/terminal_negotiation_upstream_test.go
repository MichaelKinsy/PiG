package tui

import (
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type terminalNegotiationHarness struct {
	terminal *ProcessTerminal
	writes   []string
	inputsMu sync.Mutex
	inputs   []string
	command  chan func()
	done     chan struct{}
	cleaned  bool
	send     func(string)
}

func (h *terminalNegotiationHarness) Write(data []byte) (int, error) {
	h.writes = append(h.writes, string(data))
	return len(data), nil
}

func newTerminalNegotiationHarness(t *testing.T) *terminalNegotiationHarness {
	t.Helper()
	preserveKeyboardProtocolState(t)
	h := &terminalNegotiationHarness{command: make(chan func()), done: make(chan struct{})}
	h.terminal = NewProcessTerminalWithOutput(nil, nil, h)
	h.terminal.queryAndEnableKittyProtocol()
	input := h.terminal.NewTerminalInput(func(sequence string) { h.addInputs([]string{sequence}) })
	go func() {
		defer close(h.done)
		defer input.Close()
		for {
			select {
			case f, ok := <-h.command:
				if !ok {
					return
				}
				f()
			case <-input.C:
				input.Flush()
			}
		}
	}()
	h.send = func(data string) {
		h.command <- func() { input.Process([]byte(data)) }
		synctest.Wait()
	}
	t.Cleanup(h.cleanup)
	return h
}

func (h *terminalNegotiationHarness) cleanup() {
	if h.cleaned {
		return
	}
	h.cleaned = true
	close(h.command)
	<-h.done
	h.terminal.Stop()
}

func (h *terminalNegotiationHarness) addInputs(inputs []string) {
	h.inputsMu.Lock()
	defer h.inputsMu.Unlock()
	h.inputs = append(h.inputs, inputs...)
}

func (h *terminalNegotiationHarness) input() []string {
	h.inputsMu.Lock()
	defer h.inputsMu.Unlock()
	return slices.Clone(h.inputs)
}

func (h *terminalNegotiationHarness) noInput(t *testing.T) {
	t.Helper()
	if input := h.input(); len(input) != 0 {
		t.Fatalf("input=%q, want undefined", input)
	}
}

func (h *terminalNegotiationHarness) lastInput(t *testing.T, want string) {
	t.Helper()
	input := h.input()
	if len(input) == 0 || input[len(input)-1] != want {
		t.Fatalf("input=%q, want last=%q", input, want)
	}
}

func (h *terminalNegotiationHarness) writeCount(t *testing.T, sequence string, want int) {
	t.Helper()
	got := 0
	for _, write := range h.writes {
		if write == sequence {
			got++
		}
	}
	if got != want {
		t.Fatalf("write %q count=%d, want %d: writes=%q", sequence, got, want, h.writes)
	}
}

func TestUpstreamTerminalNegotiation(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*testing.T, *terminalNegotiationHarness)
	}{
		// .upstream/v0.87.1/packages/tui/test/terminal.test.ts:132
		{"queries Kitty mode before enabling modifyOtherKeys fallback", func(t *testing.T, h *terminalNegotiationHarness) {
			if h.writes[0] != "\x1b[>7u\x1b[?u\x1b[c" {
				t.Fatalf("first write=%q", h.writes[0])
			}
			if slices.Contains(h.writes, "\x1b[>4;2m") || h.terminal.KittyProtocolActive() {
				t.Fatalf("premature fallback/activation: writes=%q", h.writes)
			}
		}},
		// .upstream/v0.87.1/packages/tui/test/terminal.test.ts:143
		{"activates Kitty mode for non-zero negotiated flags", func(t *testing.T, h *terminalNegotiationHarness) {
			h.send("\x1b[?7u")
			h.noInput(t)
			if !h.terminal.KittyProtocolActive() {
				t.Fatal("Kitty inactive")
			}
			h.writeCount(t, "\x1b[>4;2m", 0)
			h.writeCount(t, "\x1b[>4;0m", 0)
			h.cleanup()
			h.writeCount(t, "\x1b[<u", 1)
			h.writeCount(t, "\x1b[>4;0m", 0)
		}},
		// .upstream/v0.87.1/packages/tui/test/terminal.test.ts:161
		{"falls back to modifyOtherKeys for zero Kitty flags", func(t *testing.T, h *terminalNegotiationHarness) {
			h.send("\x1b[?0u")
			h.noInput(t)
			if h.terminal.KittyProtocolActive() {
				t.Fatal("Kitty active for zero flags")
			}
			h.writeCount(t, "\x1b[>4;2m", 1)
			h.cleanup()
			h.writeCount(t, "\x1b[>4;0m", 1)
		}},
		// .upstream/v0.87.1/packages/tui/test/terminal.test.ts:177
		{"falls back to modifyOtherKeys for device attributes without Kitty flags", func(t *testing.T, h *terminalNegotiationHarness) {
			h.send("\x1b[?62;4;52c")
			h.noInput(t)
			if h.terminal.KittyProtocolActive() {
				t.Fatal("Kitty active for DA")
			}
			h.writeCount(t, "\x1b[>4;2m", 1)
		}},
		// .upstream/v0.99.2/packages/tui/test/terminal.test.ts:190
		{"forwards device attributes replies that answer other queries", func(t *testing.T, h *terminalNegotiationHarness) {
			h.send("\x1b[?7u")
			h.send("\x1b[?62;4;52c")
			h.noInput(t)
			// The TUI's color query uses DA1 as its own sentinel.
			h.send("\x1b[?62;4;52c")
			h.lastInput(t, "\x1b[?62;4;52c")
		}},
		// .upstream/v0.99.2/packages/tui/test/terminal.test.ts:205
		{"forwards normal input while waiting for Kitty response", func(t *testing.T, h *terminalNegotiationHarness) {
			h.send("a")
			h.lastInput(t, "a")
			if h.terminal.KittyProtocolActive() {
				t.Fatal("Kitty active")
			}
		}},
		// .upstream/v0.99.2/packages/tui/test/terminal.test.ts:217
		{"tracks split Kitty confirmation", func(t *testing.T, h *terminalNegotiationHarness) {
			h.send("\x1b[?7")
			time.Sleep(10 * time.Millisecond)
			synctest.Wait()
			h.noInput(t)
			h.send("u")
			if !h.terminal.KittyProtocolActive() {
				t.Fatal("split Kitty response inactive")
			}
			h.writeCount(t, "\x1b[>4;2m", 0)
		}},
		// .upstream/v0.99.2/packages/tui/test/terminal.test.ts:236
		{"replays buffered CSI-prefix input when it is not a Kitty response", func(t *testing.T, h *terminalNegotiationHarness) {
			h.send("\x1b[")
			time.Sleep(50 * time.Millisecond)
			synctest.Wait()
			h.noInput(t)
			time.Sleep(150 * time.Millisecond)
			synctest.Wait()
			h.lastInput(t, "\x1b[")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) { tc.run(t, newTerminalNegotiationHarness(t)) })
		})
	}
}
