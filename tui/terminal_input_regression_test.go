package tui

import (
	"bytes"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestTerminalNegotiationFragmentTransitions(t *testing.T) {
	// terminal.ts:readKeyboardProtocolNegotiationSequence retains only negotiation prefixes and replays rejected prefixes before normal input.
	for _, tc := range []struct {
		name, prefix, continuation string
		want                       []string
		kitty                      bool
	}{
		{"late confirmation", "\x1b[?7", "u", nil, true},
		{"extend prefix", "\x1b[", "?7u", nil, true},
		{"invalid continuation ordered replay", "\x1b[", "a", []string{"\x1b[", "a"}, false},
		{"zero flags", "\x1b[?0", "u", nil, false},
		{"DA suffix", "\x1b[?62;4;52", "c", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newTerminalNegotiationHarness(t)
				h.send(tc.prefix)
				time.Sleep(50 * time.Millisecond)
				synctest.Wait()
				h.noInput(t)
				h.send(tc.continuation)
				time.Sleep(150 * time.Millisecond)
				synctest.Wait()
				if !slices.Equal(h.input(), tc.want) || h.terminal.KittyProtocolActive() != tc.kitty {
					t.Fatalf("input=%q, kitty=%v; want %q,%v", h.input(), h.terminal.KittyProtocolActive(), tc.want, tc.kitty)
				}
			})
		})
	}
}

func TestTerminalNegotiationPrefixDeadlineBoundaries(t *testing.T) {
	// terminal.ts sets a fresh150ms fragment timeout after each prefix extension.
	for _, extend := range []bool{false, true} {
		t.Run(strconv.FormatBool(extend), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newTerminalNegotiationHarness(t)
				h.send("\x1b[")
				time.Sleep(50 * time.Millisecond)
				synctest.Wait()
				h.noInput(t)
				want := "\x1b["
				if extend {
					time.Sleep(100 * time.Millisecond)
					h.send("?7")
					want += "?7"
				}
				time.Sleep(149 * time.Millisecond)
				synctest.Wait()
				h.noInput(t)
				time.Sleep(time.Millisecond)
				synctest.Wait()
				h.lastInput(t, want)
			})
		})
	}
}

func TestTerminalNegotiationPasteBypassesPrefixAndCloseCancelsTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newTerminalNegotiationHarness(t)
		h.send("\x1b[")
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
		h.send("\x1b[200~\x1b[?7u\x1b[201~")
		if !slices.Equal(h.input(), []string{"\x1b[200~\x1b[?7u\x1b[201~"}) {
			t.Fatalf("paste flushed or consumed negotiation: %q", h.input())
		}
		h.cleanup()
		time.Sleep(150 * time.Millisecond)
		synctest.Wait()
		if len(h.input()) != 1 {
			t.Fatalf("closed decoder emitted prefix: %q", h.input())
		}
	})
}

func TestTerminalNegotiationAcceptsLargeNumericFlags(t *testing.T) {
	// Pi parses decimal flags as Number; flags are not limited to a machine-sized integer.
	for _, flags := range []string{"18446744073709551616", strings.Repeat("9", 400)} {
		t.Run(flags, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newTerminalNegotiationHarness(t)
				h.send("\x1b[?" + flags + "u")
				h.noInput(t)
				if !h.terminal.KittyProtocolActive() {
					t.Fatal("nonzero numeric flags did not activate Kitty")
				}
			})
		})
	}
}

type stalledProgressWriter struct {
	bytes.Buffer
	active  int
	entered chan struct{}
	release chan struct{}
}

func (w *stalledProgressWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func (w *stalledProgressWriter) Write(p []byte) (int, error) {
	if string(p) == "\x1b]9;4;3\x07" {
		w.active++
		if w.active == 2 {
			close(w.entered)
			<-w.release
		}
	}
	return w.Buffer.Write(p)
}

func TestTerminalProgressCancellationJoinsKeepalive(t *testing.T) {
	// Pi's interval callback and clearInterval share its event loop. Go must join an in-flight callback before returning timer ownership.
	synctest.Test(t, func(t *testing.T) {
		w := &stalledProgressWriter{entered: make(chan struct{}), release: make(chan struct{})}
		terminal := NewProcessTerminalWithOutput(nil, nil, w)
		terminal.SetProgress(true)
		<-w.entered
		done := make(chan struct{})
		go func() { terminal.clearProgressInterval(); close(done) }()
		synctest.Wait()
		select {
		case <-done:
			t.Error("clearProgressInterval returned with a live keepalive write")
		default:
		}
		close(w.release)
		<-done
		terminal.SetProgress(false)
		if !strings.HasSuffix(w.String(), "\x1b]9;4;0\x07") {
			t.Fatalf("progress ends with active state: %q", w.String())
		}
	})
}
