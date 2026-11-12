//go:build unix

package tui

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// Pi's drainInput(1000, 50) at exit exists so that terminal output produced after the TUI stops reading does not reach the
// shell. ExitDrain ends on the reply to a DA1 query written after everything that stops the terminal's input has been
// written, where Pi waits for 50 ms of silence. These tests run it against a fake terminal and a fake clock: the terminal
// answers in order, the clock advances only when the drain waits, and no test sleeps.
//
// What a terminal sends unprompted before it handles the disables (all in flight when the reply is written):
//   - Kitty keyboard protocol: release and repeat events for keys held at exit.
//   - modifyOtherKeys: a modified key pressed before the terminal handles the disable arrives as CSI 27;m;k~.
//   - reports of enabled modes: mouse and focus (1000/1002/1003/1004/1006, fullscreen), light/dark (2031).
//   - a reply to an earlier query: keyboard flags and DA1, OSC 10/11/4 and DA1 (colors), CSI 16 t (cell size).
const (
	exitDA1Reply      = "\x1b[?62;22c"
	exitMouseMove     = "\x1b[<35;10;5M"
	exitFocusIn       = "\x1b[I"
	exitKittyRelease  = "\x1b[99;5:3u"
	exitModifiedKey   = "\x1b[27;5;99~"
	exitColorReply    = "\x1b]11;rgb:1e1e/1e1e/2e2e\x07"
	exitCellSizeReply = "\x1b[6;20;10t"
)

type scheduledInput struct {
	at   time.Duration
	data string
}

// fakeDrainClock is the clock and poll of a drain: poll serves what is readable now, else delivers the next scheduled input if it falls inside the wait, else advances the clock by the whole wait.
type fakeDrainClock struct {
	start, now time.Time
	w          *os.File
	pending    []scheduledInput
	polls      int
}

func newFakeDrainClock(w *os.File) *fakeDrainClock {
	start := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	return &fakeDrainClock{start: start, now: start, w: w}
}

func (c *fakeDrainClock) elapsed() time.Duration { return c.now.Sub(c.start) }

// deliver makes data readable at elapsed time at; at or before the current time it is readable at once.
func (c *fakeDrainClock) deliver(at time.Duration, data string) {
	if at <= c.elapsed() {
		_, _ = c.w.WriteString(data)
		return
	}
	c.pending = append(c.pending, scheduledInput{at, data})
}

func (c *fakeDrainClock) poll(fd uintptr, ms int) (int, error) {
	c.polls++
	if n, err := pollReadable(fd, 0); err != nil || n > 0 {
		return n, err
	}
	limit := c.now.Add(time.Duration(ms) * time.Millisecond)
	if len(c.pending) > 0 {
		if next := c.start.Add(c.pending[0].at); !next.After(limit) {
			c.now = next
			_, _ = c.w.WriteString(c.pending[0].data)
			c.pending = c.pending[1:]
			return 1, nil
		}
	}
	c.now = limit
	return 0, nil
}

// fakeTerminal is the terminal at the other end: it records what is written to it and, in order, answers each DA1 query.
type fakeTerminal struct {
	written bytes.Buffer
	queries int
	onQuery func(n int)
}

func (f *fakeTerminal) Write(p []byte) (int, error) {
	f.written.Write(p)
	for range bytes.Count(p, []byte("\x1b[c")) {
		f.queries++
		if f.onQuery != nil {
			f.onQuery(f.queries)
		}
	}
	return len(p), nil
}

type exitDrainRig struct {
	term  *ProcessTerminal
	r, w  *os.File
	tty   *fakeTerminal
	clock *fakeDrainClock
}

// newExitDrainRig is a terminal that answered the DA1 sentinel of its startup queries and has the Kitty flags pushed, as a run that has been interactive for a while.
func newExitDrainRig(t *testing.T) *exitDrainRig {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	tty := &fakeTerminal{}
	term := NewProcessTerminalWithOutput(r, nil, tty)
	clock := newFakeDrainClock(w)
	term.drainClock = exitDrainClock{now: func() time.Time { return clock.now }, poll: clock.poll}
	term.deviceAttributesAnswered.Store(true)
	resetKeyboardProtocolState(true, false, false)
	t.Cleanup(func() { resetKeyboardProtocolState(false, false, false) })
	return &exitDrainRig{term: term, r: r, w: w, tty: tty, clock: clock}
}

// answerWith makes the terminal send inFlight, then the DA1 reply after delay, when the query arrives, and the user type typedAfter once the exit has settled.
func (g *exitDrainRig) answerWith(inFlight string, delay time.Duration, typedAfter string) {
	g.tty.onQuery = func(int) {
		g.clock.deliver(0, inFlight)
		g.clock.deliver(delay, exitDA1Reply)
		if typedAfter != "" {
			g.clock.deliver(delay+exitSentinelSettle+time.Millisecond, typedAfter)
		}
	}
}

// unread returns what is still queued on stdin once the input that was still to come (the user's type-ahead) has come.
func (g *exitDrainRig) unread(t *testing.T) string {
	t.Helper()
	for _, later := range g.clock.pending {
		_, _ = g.w.WriteString(later.data)
	}
	g.clock.pending = nil
	var got []byte
	buf := make([]byte, 256)
	for {
		n, err := pollReadable(g.r.Fd(), 0)
		if err != nil {
			t.Fatalf("poll: %v", err)
		}
		if n == 0 {
			return string(got)
		}
		m, err := g.r.Read(buf)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		got = append(got, buf[:m]...)
	}
}

func (g *exitDrainRig) exitDrain(t *testing.T, facts ExitDrainFacts) {
	t.Helper()
	if err := g.term.ExitDrain(facts); err != nil {
		t.Fatalf("ExitDrain: %v", err)
	}
}

func TestExitDrainEndsOnTheReplyWithoutWaiting(t *testing.T) {
	g := newExitDrainRig(t)
	g.answerWith("", 0, "")
	g.exitDrain(t, ExitDrainFacts{})
	if got := g.clock.elapsed(); got != exitSentinelSettle {
		t.Fatalf("ExitDrain waited %v for a terminal whose reply was already there, want the settle %v; Pi waits %v", got, exitSentinelSettle, exitDrainIdleWait)
	}
	if got := g.unread(t); got != "" {
		t.Fatalf("the reply to the exit query reached the shell: %q", got)
	}
	if g.tty.queries != 1 {
		t.Fatalf("the exit wrote %d DA1 queries, want 1", g.tty.queries)
	}
}

// A byte the terminal sent before it handled the disables is the terminal's, not the shell's.
func TestExitDrainDiscardsWhatWasInFlightBeforeTheReply(t *testing.T) {
	for name, inFlight := range map[string]string{
		"mouse motion":          exitMouseMove,
		"focus report":          exitFocusIn,
		"Kitty key release":     exitKittyRelease,
		"modifyOtherKeys key":   exitModifiedKey,
		"color reply":           exitColorReply,
		"cell size reply":       exitCellSizeReply,
		"all of them, repeated": strings.Repeat(exitMouseMove+exitFocusIn+exitKittyRelease+exitModifiedKey, 40),
		"typed before reply":    "ab",
	} {
		t.Run(name, func(t *testing.T) {
			g := newExitDrainRig(t)
			g.answerWith(inFlight, 0, "")
			g.exitDrain(t, ExitDrainFacts{})
			if got := g.unread(t); got != "" {
				t.Fatalf("%q reached the shell", got)
			}
		})
	}
}

// Everything that stops the terminal's input precedes the query, so the reply proves the terminal handled it.
func TestExitDrainWritesEveryDisableBeforeTheQuery(t *testing.T) {
	g := newExitDrainRig(t)
	g.term.colorSchemeReports.Store(true)
	g.answerWith("", 0, "")
	g.exitDrain(t, ExitDrainFacts{DisableReports: altDisableMouse})
	want := keyboardProtocolPop + "\x1b[?2004l" + "\x1b[?2031l" + altDisableMouse + "\x1b[c"
	if got := g.tty.written.String(); got != want {
		t.Fatalf("exit wrote %q, want %q", got, want)
	}
}

func TestExitDrainDisablesModifyOtherKeysBeforeTheQuery(t *testing.T) {
	g := newExitDrainRig(t)
	resetKeyboardProtocolState(true, true, false)
	g.answerWith("", 0, "")
	g.exitDrain(t, ExitDrainFacts{})
	want := keyboardProtocolPop + modifyOtherKeysDisable + "\x1b[?2004l" + "\x1b[c"
	if got := g.tty.written.String(); got != want {
		t.Fatalf("exit wrote %q, want %q", got, want)
	}
}

// What the user types after the reply is the shell's. Pi discards keys typed within its 50 ms window; here the window ends at the reply.
func TestExitDrainLeavesWhatIsTypedAfterTheReply(t *testing.T) {
	g := newExitDrainRig(t)
	g.answerWith(exitMouseMove+"ab", 0, "ls\r")
	g.exitDrain(t, ExitDrainFacts{})
	if got := g.unread(t); got != "ls\r" {
		t.Fatalf("unread after the exit = %q, want the type-ahead %q only", got, "ls\r")
	}
}

// The reply may arrive in pieces and after the user's bytes, at any distance: the wait follows the reply, not a timer.
func TestExitDrainWaitsForALateReply(t *testing.T) {
	for _, delay := range []time.Duration{20 * time.Millisecond, 80 * time.Millisecond, 200 * time.Millisecond, 300 * time.Millisecond, exitDrainMaxWait - 10*time.Millisecond} {
		t.Run(delay.String(), func(t *testing.T) {
			g := newExitDrainRig(t)
			g.answerWith(exitKittyRelease, delay, "x")
			g.exitDrain(t, ExitDrainFacts{})
			if got := g.clock.elapsed(); got != delay+exitSentinelSettle {
				t.Fatalf("ExitDrain returned after %v, want %v: the time of the reply and the settle", got, delay+exitSentinelSettle)
			}
			if got := g.unread(t); got != "x" {
				t.Fatalf("unread after the exit = %q, want the type-ahead only", got)
			}
		})
	}
}

func TestExitDrainReadsAReplySplitAcrossReads(t *testing.T) {
	g := newExitDrainRig(t)
	g.tty.onQuery = func(int) {
		g.clock.deliver(10*time.Millisecond, "\x1b[?62;")
		g.clock.deliver(70*time.Millisecond, "22c")
		g.clock.deliver(70*time.Millisecond+exitSentinelSettle+time.Millisecond, "x")
	}
	g.exitDrain(t, ExitDrainFacts{})
	if got := g.clock.elapsed(); got != 70*time.Millisecond+exitSentinelSettle {
		t.Fatalf("ExitDrain returned after %v, want 75ms: the end of the reply and the settle", got)
	}
	if got := g.unread(t); got != "x" {
		t.Fatalf("unread after the exit = %q, want %q", got, "x")
	}
}

// A reply that is not DA1 ends nothing: the exit waits for DA1.
func TestExitDrainIgnoresRepliesThatAreNotDeviceAttributes(t *testing.T) {
	g := newExitDrainRig(t)
	g.tty.onQuery = func(int) {
		g.clock.deliver(0, exitKittyRelease+exitCellSizeReply+"\x1b[?0u\x1b\x1b[?1;2")
		g.clock.deliver(30*time.Millisecond, "c")
	}
	g.exitDrain(t, ExitDrainFacts{})
	if got := g.clock.elapsed(); got != 30*time.Millisecond+exitSentinelSettle {
		t.Fatalf("ExitDrain returned after %v, want 35ms: the DA1 reply that follows a stray ESC, and the settle", got)
	}
}

// A DA1 reply that answers an earlier query arrives before the reply to the exit's and must not end the wait. The earlier queries the exit can account for are counted: the keyboard protocol queries and the renderer's color queries. Counting, where a different query would do, keeps the proof the startup gave: this terminal answers exactly \x1b[c.
func TestExitDrainCountsDeviceAttributesRepliesOwedToEarlierQueries(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(g *exitDrainRig) ExitDrainFacts
		owed  int
	}{
		"one color query":   {func(*exitDrainRig) ExitDrainFacts { return ExitDrainFacts{DeviceAttributesOwed: 1} }, 1},
		"two color queries": {func(*exitDrainRig) ExitDrainFacts { return ExitDrainFacts{DeviceAttributesOwed: 2} }, 2},
		"a keyboard query owed": {func(g *exitDrainRig) ExitDrainFacts {
			g.term.pendingKeyboardProtocolDeviceAttributes.Store(1)
			return ExitDrainFacts{}
		}, 1},
		"both": {func(g *exitDrainRig) ExitDrainFacts {
			g.term.pendingKeyboardProtocolDeviceAttributes.Store(1)
			return ExitDrainFacts{DeviceAttributesOwed: 1}
		}, 2},
	} {
		t.Run(name, func(t *testing.T) {
			g := newExitDrainRig(t)
			facts := tc.setup(g)
			g.tty.onQuery = func(int) {
				for i := range tc.owed {
					g.clock.deliver(time.Duration(10*(i+1))*time.Millisecond, exitColorReply+exitDA1Reply)
				}
				g.clock.deliver(time.Duration(10*(tc.owed+1))*time.Millisecond, exitDA1Reply)
				g.clock.deliver(time.Duration(10*(tc.owed+1))*time.Millisecond+exitSentinelSettle+time.Millisecond, "x")
			}
			g.exitDrain(t, facts)
			if got, want := g.clock.elapsed(), time.Duration(10*(tc.owed+1))*time.Millisecond+exitSentinelSettle; got != want {
				t.Fatalf("ExitDrain returned after %v, want %v: the reply to its own query", got, want)
			}
			if got := g.unread(t); got != "x" {
				t.Fatalf("unread after the exit = %q, want the type-ahead only", got)
			}
		})
	}
}

// A DA1 reply the input reader read the start of ends in the drain, and counts as the reply it is.
func TestExitDrainFinishesAReplyTheInputReaderBegan(t *testing.T) {
	g := newExitDrainRig(t)
	t.Cleanup(func() { inputTail.Store(0) })
	if _, err := g.w.WriteString("\x1b[?6"); err != nil {
		t.Fatal(err)
	}
	if chunk, err := ReadInputChunk(g.r); err != nil || string(chunk) != "\x1b[?6" {
		t.Fatalf("ReadInputChunk = %q, %v", chunk, err)
	}
	g.tty.onQuery = func(int) {
		g.clock.deliver(10*time.Millisecond, "4;1c")
		g.clock.deliver(20*time.Millisecond, exitDA1Reply)
	}
	g.exitDrain(t, ExitDrainFacts{DeviceAttributesOwed: 1})
	if got := g.clock.elapsed(); got != 20*time.Millisecond+exitSentinelSettle {
		t.Fatalf("ExitDrain returned after %v, want 25ms: the reply to its own query, after the half-read reply, and the settle", got)
	}
}

// Without a DA1 answer at startup the terminal is not known to answer \x1b[c, and a query would leak its own reply: Pi's drain, exactly.
func TestExitDrainKeepsPisTimerWhereTheTerminalNeverAnsweredDeviceAttributes(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(g *exitDrainRig) ExitDrainFacts
	}{
		"DA1 never answered": {func(g *exitDrainRig) ExitDrainFacts {
			g.term.deviceAttributesAnswered.Store(false)
			return ExitDrainFacts{}
		}},
		"renderer unknown": {func(*exitDrainRig) ExitDrainFacts { return ExitDrainFacts{Unaccounted: true} }},
	} {
		t.Run(name, func(t *testing.T) {
			g := newExitDrainRig(t)
			facts := tc.setup(g)
			g.clock.deliver(30*time.Millisecond, exitKittyRelease)
			g.clock.deliver(60*time.Millisecond, "x")
			g.exitDrain(t, facts)
			// 50 ms of silence after the last byte, as Pi's rule.
			if got := g.clock.elapsed(); got != 110*time.Millisecond {
				t.Fatalf("ExitDrain returned after %v, want 110ms: 50ms after the byte at 60ms", got)
			}
			if got := g.unread(t); got != "" {
				t.Fatalf("%q reached the shell", got)
			}
			if g.tty.queries != 0 || strings.Contains(g.tty.written.String(), "\x1b[c") {
				t.Fatalf("the exit wrote a query: %q", g.tty.written.String())
			}
			if got := strings.Count(g.tty.written.String(), keyboardProtocolPop); got != 1 {
				t.Fatalf("the Kitty flags were popped %d times in %q, want once", got, g.tty.written.String())
			}
		})
	}
}

// Pi's drain is bounded by 1000 ms however long the terminal keeps sending.
func TestExitDrainTimerPathIsBoundedByPisMaximum(t *testing.T) {
	g := newExitDrainRig(t)
	g.term.deviceAttributesAnswered.Store(false)
	for at := 40 * time.Millisecond; at < 3*time.Second; at += 40 * time.Millisecond {
		g.clock.deliver(at, "x")
	}
	g.exitDrain(t, ExitDrainFacts{})
	if got := g.clock.elapsed(); got != exitDrainMaxWait {
		t.Fatalf("ExitDrain returned after %v, want Pi's maximum %v", got, exitDrainMaxWait)
	}
}

// The wait for the reply is bounded by Pi's maximum, and by nothing shorter: a timer under the round trip would end before the reply and leave it for the shell.
func TestExitDrainWaitsForTheReplyUpToPisMaximum(t *testing.T) {
	t.Run("the reply never comes", func(t *testing.T) {
		g := newExitDrainRig(t)
		g.exitDrain(t, ExitDrainFacts{})
		if got := g.clock.elapsed(); got != exitDrainMaxWait {
			t.Fatalf("ExitDrain returned after %v, want Pi's maximum %v", got, exitDrainMaxWait)
		}
		if g.tty.queries != 1 {
			t.Fatalf("the exit wrote %d queries, want 1", g.tty.queries)
		}
	})
	t.Run("input keeps arriving and the reply never comes", func(t *testing.T) {
		g := newExitDrainRig(t)
		for at := 40 * time.Millisecond; at < 3*time.Second; at += 40 * time.Millisecond {
			g.clock.deliver(at, "x")
		}
		g.exitDrain(t, ExitDrainFacts{})
		if got := g.clock.elapsed(); got != exitDrainMaxWait {
			t.Fatalf("ExitDrain returned after %v, want Pi's maximum %v", got, exitDrainMaxWait)
		}
	})
}

// A terminal or multiplexer that answers DA1 twice, or an earlier reply in flight, leaves nothing for the shell if it follows the first within the settle.
func TestExitDrainDiscardsASecondReplyThatFollowsTheFirst(t *testing.T) {
	g := newExitDrainRig(t)
	g.tty.onQuery = func(int) {
		g.clock.deliver(0, exitDA1Reply)
		g.clock.deliver(3*time.Millisecond, exitDA1Reply)
	}
	g.exitDrain(t, ExitDrainFacts{})
	if got := g.clock.elapsed(); got != 3*time.Millisecond+exitSentinelSettle {
		t.Fatalf("ExitDrain returned after %v, want 8ms: the settle after the second reply", got)
	}
	if got := g.unread(t); got != "" {
		t.Fatalf("a second reply reached the shell: %q", got)
	}
}

// A terminal that hung up ends the exit with the read error, as Pi's drain does.
func TestExitDrainReportsAFailedRead(t *testing.T) {
	g := newExitDrainRig(t)
	if err := g.w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := g.term.ExitDrain(ExitDrainFacts{}); err != io.EOF {
		t.Fatalf("ExitDrain of a hung-up terminal returned %v, want io.EOF", err)
	}
}

// The terminal answered \x1b[c at startup when the last owed DA1 reply was consumed; a new query starts over.
func TestDeviceAttributesAnsweredFollowsTheStartupReply(t *testing.T) {
	t.Setenv("PI_PROGRAM_STATUS", "0")
	term := NewProcessTerminalWithOutput(nil, nil, &bytes.Buffer{})
	term.queryAndEnableKittyProtocol()
	if term.deviceAttributesAnswered.Load() {
		t.Fatal("answered before any reply")
	}
	if !term.handleKeyboardProtocolNegotiationSequence("\x1b[?62;22c") {
		t.Fatal("the owed DA1 reply was not consumed")
	}
	if !term.deviceAttributesAnswered.Load() {
		t.Fatal("not answered after the DA1 reply")
	}
	term.queryAndEnableKittyProtocol()
	if term.deviceAttributesAnswered.Load() {
		t.Fatal("a new query left the previous answer standing")
	}
}

func TestRendererExitDrainFacts(t *testing.T) {
	t.Run("color queries wait for their DA1 replies", func(t *testing.T) {
		ui := NewWithOutput(&bytes.Buffer{}, 80, 24)
		if got := ui.ExitDrainFacts().DeviceAttributesOwed; got != 0 {
			t.Fatalf("a renderer that asked nothing is owed %d replies", got)
		}
		ui.QueryTerminalColors(TerminalColorQueryOptions{TimeoutMs: 1000})
		ui.QueryTerminalColors(TerminalColorQueryOptions{TimeoutMs: 1000})
		if got := ui.ExitDrainFacts().DeviceAttributesOwed; got != 2 {
			t.Fatalf("two unanswered color queries are owed %d replies", got)
		}
		if !ui.ConsumeTerminalColorResponse("\x1b[?62;22c") {
			t.Fatal("the DA1 reply was not consumed")
		}
		if got := ui.ExitDrainFacts().DeviceAttributesOwed; got != 1 {
			t.Fatalf("one answered of two color queries leaves %d owed", got)
		}
	})
	t.Run("fullscreen mouse and focus reports are disabled before the query", func(t *testing.T) {
		on, off := true, false
		for _, tc := range []struct {
			name  string
			mouse *bool
			start bool
			want  string
		}{
			{"mouse on, started", &on, true, altDisableMouse},
			{"mouse default, started", nil, true, altDisableMouse},
			{"mouse off, started", &off, true, ""},
			{"mouse on, not started", &on, false, ""},
		} {
			ui := NewTuiAltScreenWithOutput(&bytes.Buffer{}, 80, 24, TuiAltScreenOptions{Mouse: tc.mouse})
			if tc.start {
				ui.Start()
			}
			if got := ui.ExitDrainFacts().DisableReports; got != tc.want {
				t.Errorf("%s: DisableReports = %q, want %q", tc.name, got, tc.want)
			}
		}
	})
	t.Run("the main-screen renderer has no mouse reports to stop", func(t *testing.T) {
		ui := NewWithOutput(&bytes.Buffer{}, 80, 24)
		if got := ui.ExitDrainFacts().DisableReports; got != "" {
			t.Fatalf("DisableReports = %q, want none", got)
		}
	})
}
