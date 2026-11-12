package tui

import (
	"runtime"
	"sync/atomic"
	"time"
)

// Pi's drainInput(1000, 50) at process exit (terminal.ts:391; interactive-mode.ts shutdown): wait until 50 ms pass with no input, at most 1000 ms.
const (
	// upstream: packages/tui/src/terminal.ts:drainInput (maxMs = 1000)
	exitDrainMaxWait = time.Second
	// upstream: packages/tui/src/terminal.ts:drainInput (idleMs = 50)
	exitDrainIdleWait = 50 * time.Millisecond
	// pig divergence (D622): the 5 ms of silence after the reply to the exit's DA1 query replaces Pi's 50 ms idle wait.
	// exitSentinelSettle is the silence that ends the exit after the reply to its query: a terminal or multiplexer that answers DA1 twice, or a reply to an earlier query still in flight, is discarded rather than left for the shell.
	exitSentinelSettle = 5 * time.Millisecond
)

// exitDrainClock is the time source and the readiness poll of the exit drain; its zero value is the real clock and pollReadable. Tests replace both so a drain that waits advances a fake clock.
type exitDrainClock struct {
	now  func() time.Time
	poll func(fd uintptr, ms int) (int, error)
}

func (c exitDrainClock) time() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c exitDrainClock) pollReadable(fd uintptr, ms int) (int, error) {
	if c.poll != nil {
		return c.poll(fd, ms)
	}
	return pollReadable(fd, ms)
}

// ExitDrainFacts is the state of the process teardown that a ProcessTerminal cannot see: the renderer and the input decoder own it.
type ExitDrainFacts struct {
	// DisableReports is what the renderer writes to stop the reports it enabled (fullscreen mouse and focus).
	DisableReports string
	// DeviceAttributesOwed is the number of DA1 replies the renderer's own queries (terminal colors) still wait for.
	DeviceAttributesOwed int
	// Unaccounted: the caller cannot account for the renderer.
	Unaccounted bool
}

// ExitDrain is the process-exit drain: late terminal input must not reach the shell. It writes everything that stops the terminal producing input, then a DA1 query, and discards input until the reply to that query arrives and 5 ms pass without input.
//
// pig divergence (D622): Pi's drainInput(1000, 50) (terminal.ts:391, called by interactive-mode.ts shutdown) waits until 50 ms pass with no input, at most 1000 ms, and writes the mouse, focus and light/dark disables only after it (tui.ts stop). Terminals process their input in order, so the reply to a query written after the disables proves the terminal handled them and that every byte it sent before is already read. The wait is one terminal round trip, bounded by Pi's 1000 ms maximum, and does not end on a timer shorter than that: a link slower than 50 ms would leak the reply. A terminal that did not answer DA1 at startup, or a renderer the caller cannot account for, keeps Pi's drain exactly. What the user types after the settle stays for the shell; Pi discards keys typed within its 50 ms window.
func (t *ProcessTerminal) ExitDrain(facts ExitDrainFacts) error {
	if !t.exitSentinelUsable(facts) {
		return t.DrainInput(exitDrainMaxWait, exitDrainIdleWait)
	}
	// The terminal produces no keyboard input once these are handled, and the sentinel follows them in the same stream.
	t.disableKeyboardProtocol()
	stops := "\x1b[?2004l"
	if t.colorSchemeReports.Load() {
		stops += "\x1b[?2031l"
	}
	t.writeUnlogged(stops + facts.DisableReports + deviceAttributesQuery)
	// Replies owed to earlier queries arrive first, in order, ahead of the one this exit asked for.
	return t.discardThroughDeviceAttributesReply(int(t.pendingKeyboardProtocolDeviceAttributes.Load()) + facts.DeviceAttributesOwed + 1)
}

// exitSentinelUsable reports whether the exit may end on the reply to its own DA1 query: the terminal answered DA1 at startup (so it answers this one) and the caller accounts for the renderer.
func (t *ProcessTerminal) exitSentinelUsable(facts ExitDrainFacts) bool {
	return t.stdin != nil && runtime.GOOS != "windows" && t.deviceAttributesAnswered.Load() && !facts.Unaccounted
}

// discardThroughDeviceAttributesReply reads and discards input until the want-th DA1 reply has been read and then exitSentinelSettle has passed without input, so a second reply to the query that follows the first is discarded too. It waits for the reply up to Pi's maximum however long the link: a timer shorter than the round trip would end before the reply and leak it. Input is read one byte at a time, so nothing past the settle is taken: what the user types after it is the shell's. A failed read ends it with that error, as DrainInput's does.
func (t *ProcessTerminal) discardThroughDeviceAttributesReply(want int) error {
	fd := t.stdin.Fd()
	clock := t.drainClock
	end := clock.time().Add(exitDrainMaxWait)
	// A reply the input reader read half of is finished here.
	scan := deviceAttributesScanner{state: uint8(inputTail.Load())}
	// One byte per read: a longer read cannot be pushed back, and would take the user's type-ahead along with the reply.
	var buf [1]byte
	settling := false
	for {
		now := clock.time()
		if !now.Before(end) {
			return nil
		}
		wait := end.Sub(now)
		if settling {
			wait = min(wait, exitSentinelSettle)
		}
		ms := max(1, int((wait+time.Millisecond-1)/time.Millisecond))
		n, err := clock.pollReadable(fd, ms)
		if err != nil {
			return err
		}
		if n == 0 {
			if settling {
				return nil
			}
			continue
		}
		if _, err := t.stdin.Read(buf[:]); err != nil {
			if isEINTR(err) {
				continue
			}
			return err
		}
		if !settling && scan.add(buf[0]) {
			want--
			settling = want == 0
		}
	}
}

// deviceAttributesScanner finds DA1 replies (CSI ? params c) in a byte stream that arrives in any pieces.
type deviceAttributesScanner struct{ state uint8 }

// add consumes one byte and reports whether it ended a DA1 reply.
func (s *deviceAttributesScanner) add(b byte) bool {
	switch {
	case b == 0x1b:
		s.state = 1
	case s.state == 1 && b == '[':
		s.state = 2
	case s.state == 2 && b == '?':
		s.state = 3
	case s.state == 3 && (b >= '0' && b <= '9' || b == ';'):
	case s.state == 3 && b == 'c':
		s.state = 0
		return true
	default:
		s.state = 0
	}
	return false
}

// ExitDrainFacts reports what this renderer left on the terminal: the DA1 replies its color queries still wait for.
func (t *tuiBase) ExitDrainFacts() ExitDrainFacts {
	var facts ExitDrainFacts
	if queries := t.terminalBackground; queries != nil {
		queries.mu.Lock()
		facts.DeviceAttributesOwed = len(queries.pendingTerminalColorQueries)
		queries.mu.Unlock()
	}
	return facts
}

// ExitDrainFacts adds the mouse and focus reports the fullscreen renderer enabled with the alternate screen.
func (t *TuiAltScreen) ExitDrainFacts() ExitDrainFacts {
	facts := t.tuiBase.ExitDrainFacts()
	t.mu.Lock()
	if t.altScreenActive && t.mouseEnabled {
		facts.DisableReports = altDisableMouse
	}
	t.mu.Unlock()
	return facts
}

// inputTail is the scanner state at the end of the last terminal read: a DA1 reply that the input reader read the start of continues in the exit drain.
var inputTail atomic.Uint32

// noteInputTail advances inputTail over a chunk the input reader read.
func noteInputTail(chunk []byte) {
	scan := deviceAttributesScanner{state: uint8(inputTail.Load())}
	for _, b := range chunk {
		scan.add(b)
	}
	inputTail.Store(uint32(scan.state))
}
