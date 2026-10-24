package tui

import (
	"context"
	"os"
	"runtime"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// Pi #10657 (open): a DA1 reply that arrives in slow reads, with gaps longer than the 50 ms sequence timeout plus the 150 ms fragment timeout,
// leaked its fragments into the focused component as key input. An `ESC[?` prefix now waits for its final byte, as the issue's reference fix
// does, so no part of the reply reaches input. A bare `ESC[` is still replayed after the fragment timeout, as Pi's tests require; the terminal
// then absorbs the rest of the reply it owes and settles it (D143).
var splitReplyCases = []struct {
	name   string
	chunks []string
	gap    time.Duration
	want   []string
}{
	{"issue's 8-byte reads", []string{"\x1b[?61;6;", "7;22;23;", "24;28;32", ";42c"}, 200 * time.Millisecond, []string{"a"}},
	{"after ESC[?", []string{"\x1b[?", "61;6;7;22", ";42c"}, 300 * time.Millisecond, []string{"a"}},
	{"after ESC[", []string{"\x1b[", "?61;6;7;22", ";42c"}, 300 * time.Millisecond, []string{"\x1b[", "a"}},
}

// TestTerminalInputConsumesAnOwedReplySplitAcrossSlowReads is the same contract on the decoder with a fake clock, so it holds on every platform.
func TestTerminalInputConsumesAnOwedReplySplitAcrossSlowReads(t *testing.T) {
	t.Setenv("PI_PROGRAM_STATUS", "")
	for _, test := range splitReplyCases {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newTerminalNegotiationHarness(t)
				for _, chunk := range test.chunks {
					h.send(chunk)
					time.Sleep(test.gap)
					synctest.Wait()
				}
				h.send("a")
				if got := h.input(); !slices.Equal(got, test.want) {
					t.Fatalf("forwarded %q, want %q: part of the reply reached input", got, test.want)
				}
				if owed := h.terminal.pendingKeyboardProtocolDeviceAttributes.Load(); owed != 0 {
					t.Fatalf("%d replies still owed after the reply arrived", owed)
				}
			})
		})
	}
}

// The real reader also frames these on a pipe. An anonymous pipe is not a Windows terminal: waiting on its handle signals no
// readiness, so the reader cannot wake for a framing deadline there (terminal_negotiation_input_windows_test.go).
func TestForwardTerminalConsumesAnOwedReplySplitAcrossSlowReads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("an anonymous pipe signals no readiness on Windows, so the reader cannot wake for the 50 ms and 150 ms framing deadlines; TestTerminalInputConsumesAnOwedReplySplitAcrossSlowReads covers them")
	}
	for _, test := range splitReplyCases {
		t.Run(test.name, func(t *testing.T) {
			preserveKeyboardProtocolState(t)
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = r.Close() }()
			defer func() { _ = w.Close() }()
			terminal := NewProcessTerminalWithOutput(r, nil, ioDiscard{})
			expectKeyboardProtocolReply(terminal)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			inputs := make(chan string, 64)
			go func() {
				defer close(done)
				terminal.forwardInput(ctx, func(data []byte) { inputs <- string(data) }, nil)
			}()
			defer func() { cancel(); _ = w.Close(); <-done }()
			for _, chunk := range test.chunks {
				if _, err := w.WriteString(chunk); err != nil {
					t.Fatal(err)
				}
				time.Sleep(test.gap) // the terminal's delivery gap, not a wait for the code under test
			}
			if _, err := w.WriteString("a"); err != nil {
				t.Fatal(err)
			}
			var got []string
			deadline := time.After(2 * time.Second)
			for !slices.Contains(got, "a") {
				select {
				case input := <-inputs:
					got = append(got, input)
				case <-deadline:
					t.Fatalf("forwarded %q, want the typed key", got)
				}
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("forwarded %q, want %q: part of the reply reached input", got, test.want)
			}
			if owed := terminal.pendingKeyboardProtocolDeviceAttributes.Load(); owed != 0 {
				t.Fatalf("%d replies still owed after the reply arrived", owed)
			}
		})
	}
}

// Port of the test in Pi #10657's reference fix ("reassembles delayed DA1 replies for keyboard and color queries"): the keyboard query's DA1
// and the color query's DA1, each split across 200 ms reads, produce no input until complete; the first settles negotiation and the second is
// forwarded whole. Input typed afterwards is forwarded as usual (D143).
func TestReassemblesDelayedDA1RepliesForKeyboardAndColorQueries(t *testing.T) {
	t.Setenv("PI_PROGRAM_STATUS", "")
	synctest.Test(t, func(t *testing.T) {
		h := newTerminalNegotiationHarness(t)
		const reply = "\x1b[?61;6;7;22;23;24;28;32;42c"
		for _, chunk := range []string{"\x1b[?61;6;", "7;22;23;", "24;28;32", ";42c"} {
			h.send(chunk)
			time.Sleep(200 * time.Millisecond)
			synctest.Wait()
			h.noInput(t)
		}
		if !modifyOtherKeysActive.Load() {
			t.Fatal("the keyboard query's DA1 did not enable the modifyOtherKeys fallback")
		}
		for _, chunk := range []string{"\x1b[?61;6;", "7;22;23;", "24;28;32"} {
			h.send(chunk)
			time.Sleep(200 * time.Millisecond)
			synctest.Wait()
			h.noInput(t)
		}
		h.send(";42c")
		h.lastInput(t, reply)
		h.send("x")
		h.lastInput(t, "x")
		if input := h.input(); len(input) != 2 {
			t.Fatalf("input=%q, want the whole reply and the typed key", input)
		}
	})
}
