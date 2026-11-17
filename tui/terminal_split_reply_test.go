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
// then absorbs the rest of the reply it owes and settles it (D143). `want` is the replay, if any, followed by the typed key.
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

// An anonymous pipe is not a Windows terminal: waiting on its handle signals no readiness, so the reader cannot wake for a framing deadline
// there (terminal_negotiation_input_windows_test.go). The decoder tests with a fake clock cover the deadlines on every platform.
func skipWhereAPipeSignalsNoReadiness(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("an anonymous pipe signals no readiness on Windows, so the reader cannot wake for the 50 ms and 150 ms framing deadlines")
	}
}

// startSplitReplyLoop runs the process terminal's input loop on a pipe that already holds prefill and owes one keyboard-protocol DA1 reply. It
// returns the pipe's write end, the inputs the loop forwards and the terminal.
func startSplitReplyLoop(t *testing.T, prefill ...string) (*os.File, <-chan string, *ProcessTerminal) {
	t.Helper()
	preserveKeyboardProtocolState(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	t.Cleanup(func() { _ = w.Close() })
	for _, chunk := range prefill {
		if _, err := w.WriteString(chunk); err != nil {
			t.Fatal(err)
		}
	}
	terminal := NewProcessTerminalWithOutput(r, nil, ioDiscard{})
	expectKeyboardProtocolReply(terminal)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	inputs := make(chan string, 64)
	go func() {
		defer close(done)
		terminal.forwardInput(ctx, func(data []byte) { inputs <- string(data) }, nil)
	}()
	t.Cleanup(func() { cancel(); _ = w.Close(); <-done })
	return w, inputs, terminal
}

// awaitInputs appends forwarded inputs to got until enough reports the ones the test needs. The loop's own deadlines decide when inputs arrive, so
// the test waits for them and does not outwait them; the bound only fails a loop that forwards nothing more.
func awaitInputs(t *testing.T, inputs <-chan string, got []string, enough func([]string) bool) []string {
	t.Helper()
	bound := time.After(5 * time.Second)
	for !enough(got) {
		select {
		case input := <-inputs:
			got = append(got, input)
		case <-bound:
			t.Fatalf("forwarded %q, then nothing", got)
		}
	}
	return got
}

func requireTypedKeyOnly(t *testing.T, got, want []string, terminal *ProcessTerminal) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("forwarded %q, want %q: part of the reply reached input", got, want)
	}
	if owed := terminal.pendingKeyboardProtocolDeviceAttributes.Load(); owed != 0 {
		t.Fatalf("%d replies still owed after the reply arrived", owed)
	}
}

// The real reader also frames these on a pipe. Whether a bare `ESC[` is replayed is the loop's decision, made when its 50 ms and 150 ms timers
// run, and a loop the scheduler held back for those 200 ms reads the next chunk first and consumes the `ESC[` with it
// (TestForwardTerminalConsumesAnOwedReplyThatReachesTheLoopLate). So a case that expects the replay waits for it on the loop's output, as the
// sibling real-reader tests do (terminal_input_reader_test.go), and does not outwait the timers with a sleep. A held `ESC[?` prefix forwards
// nothing and the typed key follows the reply however the loop is scheduled, so there the gap only gives a wrongly armed timeout the time to
// forward the prefix.
func TestForwardTerminalConsumesAnOwedReplySplitAcrossSlowReads(t *testing.T) {
	skipWhereAPipeSignalsNoReadiness(t)
	for _, test := range splitReplyCases {
		t.Run(test.name, func(t *testing.T) {
			w, inputs, terminal := startSplitReplyLoop(t)
			replayed := test.want[:len(test.want)-1]
			var got []string
			for i, chunk := range test.chunks {
				if _, err := w.WriteString(chunk); err != nil {
					t.Fatal(err)
				}
				switch {
				case i == 0 && len(replayed) > 0:
					got = awaitInputs(t, inputs, got, func(got []string) bool { return len(got) >= len(replayed) })
				case len(replayed) == 0:
					time.Sleep(test.gap) // the terminal's delivery gap: a stimulus for a wrongly armed timeout, not a wait for the code under test
				}
			}
			if _, err := w.WriteString("a"); err != nil {
				t.Fatal(err)
			}
			got = awaitInputs(t, inputs, got, func(got []string) bool { return slices.Contains(got, "a") })
			requireTypedKeyOnly(t, got, test.want, terminal)
		})
	}
}

// A loop that stalls (a starved CI runner, a suspended process) reads the reply's chunks together, or the continuation before the bare `ESC[`
// has been replayed: the whole reply is the owed reply and nothing of it reaches input, the replay included. The chunks are already in the
// pipe when the loop first reads, which forces that order.
func TestForwardTerminalConsumesAnOwedReplyThatReachesTheLoopLate(t *testing.T) {
	skipWhereAPipeSignalsNoReadiness(t)
	for _, test := range splitReplyCases {
		t.Run(test.name, func(t *testing.T) {
			_, inputs, terminal := startSplitReplyLoop(t, append(slices.Clone(test.chunks), "a")...)
			got := awaitInputs(t, inputs, nil, func(got []string) bool { return slices.Contains(got, "a") })
			requireTypedKeyOnly(t, got, []string{"a"}, terminal)
		})
	}
}

// A reply's continuation `?61;6;7;22` and the rest of the reply arrive after the bare introducer `ESC[`. Whether the introducer is replayed as input
// is decided by the two chained timers and by nothing else: a continuation that gets there first joins the held prefix and the owed reply is consumed
// whole; one that gets there after both is the tail of a reply whose introducer was already replayed. A loop the scheduler holds back reads the
// continuation in the first state however late the writer was (TestForwardTerminalConsumesAnOwedReplyThatReachesTheLoopLate). The rows put the
// continuation on each side of each deadline with a fake clock; each says what is forwarded as input and what is consumed.
//
// Pi's rules, at the pinned Pi: stdin-buffer.ts:23,316,417-418 flush a held partial sequence after 50 ms (DEFAULT_SEQUENCE_TIMEOUT_MS; the 10 ms
// escape timeout is for a lone ESC); terminal.ts:20,261-264,390-395 hold the flushed prefix for 150 ms (KEYBOARD_PROTOCOL_RESPONSE_FRAGMENT_TIMEOUT_MS)
// and then forward it as input; terminal.ts:347-364 (readKeyboardProtocolNegotiationSequence) joins the next sequence to the held prefix while the
// result is still a prefix, and terminal.ts:373-376 cancels the flush timer when it does. Pi has no rule for the tail of an introducer it has already
// forwarded: it forwards the tail as text (Pi #10657), and D143 absorbs it instead.
func TestTerminalInputConsumesAnOwedReplyOnEachSideOfTheBareIntroducersReplayDeadline(t *testing.T) {
	t.Setenv("PI_PROGRAM_STATUS", "")
	for _, test := range []struct {
		name      string
		wait      time.Duration
		forwarded []string
	}{
		// The 50 ms sequence timeout has not run: stdin-buffer still holds `ESC[` and the continuation joins it there. Consumed whole.
		{"30 ms: held by the sequence buffer", 30 * time.Millisecond, []string{"a"}},
		// 50 ms ran, so `ESC[` is held by the negotiation buffer, 150 ms from forwarding. The continuation joins it there. Consumed whole.
		{"120 ms: held by the negotiation buffer", 120 * time.Millisecond, []string{"a"}},
		{"199 ms: 1 ms before the fragment deadline", 199 * time.Millisecond, []string{"a"}},
		// 50 + 150 ms ran: `ESC[` was forwarded as input, and the continuation is the tail of the reply it opened. Absorbed, and the reply settles.
		{"201 ms: after the fragment deadline", 201 * time.Millisecond, []string{"\x1b[", "a"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newTerminalNegotiationHarness(t)
				h.send("\x1b[")
				time.Sleep(test.wait)
				synctest.Wait()
				h.send("?61;6;7;22")
				h.send(";42c")
				h.send("a")
				requireTypedKeyOnly(t, h.input(), test.forwarded, h.terminal)
				if !modifyOtherKeysActive.Load() {
					t.Fatal("the reply was not consumed as the owed keyboard query's DA1 (it did not enable the modifyOtherKeys fallback)")
				}
			})
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
