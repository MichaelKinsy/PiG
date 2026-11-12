// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package tui

import (
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// pacingScreen is a fullscreen renderer whose clock, timer, terminal and output the test owns: a frame takes renderCost in its component and writeCost in its write, on that clock, and every timer the scheduler arms is recorded.
type pacingScreen struct {
	t          *testing.T
	ui         *TuiAltScreen
	at         time.Time
	delays     []time.Duration
	timer      *manualRenderTimer
	renders    int
	renderCost time.Duration
	writeCost  time.Duration
	gate       atomic.Bool
	terminal   *pacingTerminal
	pacing     *FramePacing
}

// pacingTerminal is the renderer's terminal with a screen writer that is on or off.
type pacingTerminal struct {
	Terminal
	writerActive bool
}

func (p *pacingTerminal) ScreenWriterActive() bool { return p.writerActive }

// Render shows a row that differs in every frame, so every frame writes.
func (s *pacingScreen) Render(int) []string {
	s.renders++
	s.at = s.at.Add(s.renderCost)
	return []string{"frame " + strconv.Itoa(s.renders)}
}

func (s *pacingScreen) Invalidate() {}

func (s *pacingScreen) Write(data []byte) (int, error) {
	s.at = s.at.Add(s.writeCost)
	return len(data), nil
}

// newPacingScreen starts a renderer with the adaptive policy installed and every condition of it met.
func newPacingScreen(t *testing.T) *pacingScreen {
	t.Helper()
	s := &pacingScreen{t: t, at: time.Unix(1_000_000, 0)}
	s.ui = NewTuiAltScreenWithOutput(s, 40, 10, TuiAltScreenOptions{})
	s.ui.now = func() time.Time { return s.at }
	s.ui.afterFunc = func(d time.Duration, fn func()) stoppableTimer {
		if s.timer != nil && !s.timer.stopped {
			t.Fatal("a second frame timer was armed while one was pending")
		}
		s.delays = append(s.delays, d)
		s.timer = &manualRenderTimer{fn: fn}
		return s.timer
	}
	s.terminal = &pacingTerminal{Terminal: s.ui.Terminal(), writerActive: true}
	s.ui.terminal = s.terminal
	s.gate.Store(true)
	s.pacing = &FramePacing{terminalTrusted: true, allowed: &s.gate}
	s.ui.SetFramePacing(s.pacing)
	s.ui.Add(s)
	s.ui.Start()
	t.Cleanup(s.ui.Stop)
	return s
}

// fire runs the pending frame timer, as the runtime does when it comes due.
func (s *pacingScreen) fire() {
	s.t.Helper()
	if s.timer == nil || s.timer.stopped {
		s.t.Fatal("no frame timer is pending")
	}
	timer := s.timer
	s.timer = nil
	timer.fn()
}

// frame draws one throttled frame that costs renderCost in layout and writeCost in its write, long enough after the last one that nothing delays it.
func (s *pacingScreen) frame(renderCost, writeCost time.Duration) {
	s.t.Helper()
	s.at = s.at.Add(time.Second)
	s.renderCost, s.writeCost = renderCost, writeCost
	before := s.renders
	s.ui.requestRender(false)
	s.fire()
	s.renderCost, s.writeCost = 0, 0
	if s.renders != before+1 {
		s.t.Fatalf("a requested frame rendered %d times, want 1", s.renders-before)
	}
}

// interval is the scheduler's answer now.
func (s *pacingScreen) interval() time.Duration {
	s.ui.mu.Lock()
	defer s.ui.mu.Unlock()
	return s.ui.frameIntervalLocked()
}

// lastDelay is the delay of the timer armed last.
func (s *pacingScreen) lastDelay() time.Duration {
	s.t.Helper()
	if len(s.delays) == 0 {
		s.t.Fatal("no frame timer was armed")
	}
	return s.delays[len(s.delays)-1]
}

// I = min(16 ms, max(F, 4c)): the floor up to a cost of F/4, four times the cost from there, Pi's constant from a cost of 4 ms on.
func TestAdaptiveFrameIntervalIsTheCostTimesFourBetweenTheFloorAndPisConstant(t *testing.T) {
	for _, tc := range []struct{ cost, want time.Duration }{
		{0, 10 * time.Millisecond},
		{time.Millisecond, 10 * time.Millisecond},
		{2500 * time.Microsecond, 10 * time.Millisecond},
		{3 * time.Millisecond, 12 * time.Millisecond},
		{3900 * time.Microsecond, 15600 * time.Microsecond},
		{4 * time.Millisecond, 16 * time.Millisecond},
		{20 * time.Millisecond, 16 * time.Millisecond},
	} {
		s := newPacingScreen(t)
		s.frame(tc.cost, 0)
		if got := s.interval(); got != tc.want {
			t.Errorf("after a frame that cost %v the interval is %v, want %v", tc.cost, got, tc.want)
		}
	}
}

// Pi's rule with the adaptive interval: a frame asked for 1 ms after the last one began waits the rest of 10 ms, where Pi's waits the rest of 16.
func TestAdaptiveFrameWaitsTheRestOfTheAdaptiveInterval(t *testing.T) {
	s := newPacingScreen(t)
	s.frame(time.Millisecond, 0)
	s.ui.requestRender(false)
	if got, want := s.lastDelay(), 9*time.Millisecond; got != want {
		t.Fatalf("adaptive: a frame asked for 1 ms after the last one began waits %v, want %v", got, want)
	}

	pi := newPacingScreen(t)
	pi.ui.SetFramePacing(nil)
	pi.frame(time.Millisecond, 0)
	pi.ui.requestRender(false)
	if got, want := pi.lastDelay(), minRenderInterval-time.Millisecond; got != want {
		t.Fatalf("no policy: a frame asked for 1 ms after the last one began waits %v, want Pi's %v", got, want)
	}
}

// Each condition alone returns the interval to Pi's constant, at the next question the scheduler asks: no policy, a terminal not trusted with synchronized updates, the owner's gate closed (no run streams, or an extension draws), a terminal with no screen writer, and a screen writer that is off.
func TestFrameIntervalIsPisConstantWhenAnyConditionFails(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail func(s *pacingScreen)
	}{
		{"no policy", func(s *pacingScreen) { s.ui.SetFramePacing(nil) }},
		{"terminal not trusted with synchronized updates", func(s *pacingScreen) { s.pacing.terminalTrusted = false }},
		{"owner's gate closed", func(s *pacingScreen) { s.gate.Store(false) }},
		{"terminal without a screen writer", func(s *pacingScreen) { s.ui.terminal = s.terminal.Terminal }},
		{"screen writer off", func(s *pacingScreen) { s.terminal.writerActive = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newPacingScreen(t)
			s.frame(time.Millisecond, 0)
			if got := s.interval(); got != frameIntervalFloor {
				t.Fatalf("every condition met: interval %v, want the floor %v", got, frameIntervalFloor)
			}
			tc.fail(s)
			if got := s.interval(); got != minRenderInterval {
				t.Fatalf("interval %v, want Pi's %v", got, minRenderInterval)
			}
			// The frame asked for next is Pi's: no frame passes between the change and the answer.
			s.ui.requestRender(false)
			if got, want := s.lastDelay(), minRenderInterval-time.Millisecond; got != want {
				t.Fatalf("the next frame waits %v, want Pi's %v", got, want)
			}
		})
	}
}

// A forced request and a key press draw at once under either policy, however short a time ago the last frame began.
func TestForcedAndImmediateFramesAreNeverPaced(t *testing.T) {
	for _, adaptive := range []bool{false, true} {
		for _, tc := range []struct {
			name    string
			request func(ui *TuiAltScreen)
		}{
			{"forced", func(ui *TuiAltScreen) { ui.requestRender(true) }},
			{"key press", func(ui *TuiAltScreen) { ui.RequestImmediateRender() }},
		} {
			s := newPacingScreen(t)
			if !adaptive {
				s.ui.SetFramePacing(nil)
			}
			s.frame(time.Millisecond, 0)
			armed := len(s.delays)
			tc.request(s.ui)
			if len(s.delays) != armed+1 || s.lastDelay() != 0 {
				t.Errorf("%s request (adaptive=%v): timers armed %v, want one more with no delay", tc.name, adaptive, s.delays[armed:])
			}
			before := s.renders
			s.fire()
			if s.renders != before+1 {
				t.Errorf("%s request (adaptive=%v) rendered %d frames, want 1", tc.name, adaptive, s.renders-before)
			}
		}
	}
}

// A terminal that does not take the bytes blocks the frame's write. The blocked time is in the frame's cost, so the interval is Pi's again; once writes return at once it is the floor again.
func TestBlockedWriteReturnsTheIntervalToPisConstant(t *testing.T) {
	s := newPacingScreen(t)
	s.frame(500*time.Microsecond, 20*time.Millisecond)
	if got, want := s.ui.lastFrameCost, 20500*time.Microsecond; got != want {
		t.Fatalf("frame cost %v, want %v: layout and the blocked write", got, want)
	}
	if got := s.interval(); got != minRenderInterval {
		t.Fatalf("after a write blocked for 20 ms the interval is %v, want Pi's %v", got, minRenderInterval)
	}
	s.frame(500*time.Microsecond, 0)
	if got := s.interval(); got != frameIntervalFloor {
		t.Fatalf("after a write that returned at once the interval is %v, want the floor %v", got, frameIntervalFloor)
	}
}

// The policy adds no timer: a stream of requests arms one timer for each frame drawn, as without it, and never a second one while one is pending (newPacingScreen fails the test on that).
func TestAdaptivePacingArmsOneTimerAFrame(t *testing.T) {
	for _, adaptive := range []bool{false, true} {
		s := newPacingScreen(t)
		if !adaptive {
			s.ui.SetFramePacing(nil)
		}
		armed, rendered := len(s.delays), s.renders
		for range 50 {
			s.at = s.at.Add(2 * time.Millisecond)
			s.ui.requestRender(false)
			s.ui.requestRender(false)
			if s.timer != nil && s.at.Sub(s.ui.lastRenderAt) >= s.lastDelay() {
				s.fire()
			}
		}
		if timers, frames := len(s.delays)-armed, s.renders-rendered; timers < frames || timers > frames+1 {
			t.Errorf("adaptive=%v: %d timers armed for %d frames, want one a frame", adaptive, timers, frames)
		}
	}
}

// Content that arrives every 2 ms is drawn once per floor under the policy and once per 16 ms without it.
func TestAdaptivePacingCoalescesAFastStreamAtTheFloor(t *testing.T) {
	for _, tc := range []struct {
		adaptive bool
		want     int
	}{{false, 62}, {true, 100}} {
		s := newPacingScreen(t)
		if !tc.adaptive {
			s.ui.SetFramePacing(nil)
		}
		s.frame(0, 0)
		start, rendered := s.at, s.renders
		var due time.Time
		for s.at.Sub(start) < time.Second {
			s.at = s.at.Add(time.Millisecond)
			if s.timer != nil && !s.at.Before(due) {
				s.fire()
			}
			if s.at.Sub(start)%(2*time.Millisecond) == 0 {
				armed := len(s.delays)
				s.ui.requestRender(false)
				if len(s.delays) > armed {
					due = s.at.Add(s.lastDelay())
				}
			}
		}
		if got := s.renders - rendered; got < tc.want-1 || got > tc.want+1 {
			t.Errorf("adaptive=%v: %d frames in a second of 2 ms deltas, want %d", tc.adaptive, got, tc.want)
		}
	}
}

func TestSynchronizedUpdatesTrustedIn(t *testing.T) {
	implementers := map[string]map[string]string{
		"kitty by window id":       {"TERM": "xterm-kitty", "KITTY_WINDOW_ID": "1"},
		"kitty by program":         {"TERM": "xterm-256color", "TERM_PROGRAM": "kitty"},
		"Ghostty by TERM":          {"TERM": "xterm-ghostty"},
		"Ghostty by program":       {"TERM": "xterm-256color", "TERM_PROGRAM": "ghostty"},
		"Ghostty by resources dir": {"TERM": "xterm-256color", "GHOSTTY_RESOURCES_DIR": "/usr/share/ghostty"},
		"WezTerm by pane":          {"TERM": "xterm-256color", "WEZTERM_PANE": "0"},
		"WezTerm by program":       {"TERM": "xterm-256color", "TERM_PROGRAM": "WezTerm"},
		"Warp by program":          {"TERM": "xterm-256color", "TERM_PROGRAM": "WarpTerminal"},
		"Warp by session":          {"TERM": "xterm-256color", "WARP_SESSION_ID": "1"},
		"Warp by session uuid":     {"TERM": "xterm-256color", "WARP_TERMINAL_SESSION_UUID": "1"},
		"iTerm2 by session":        {"TERM": "xterm-256color", "ITERM_SESSION_ID": "w0t0p0"},
		"iTerm2 by program":        {"TERM": "xterm-256color", "TERM_PROGRAM": "iTerm.app"},
		"Alacritty by TERM":        {"TERM": "alacritty"},
		"Alacritty by window id":   {"TERM": "xterm-256color", "ALACRITTY_WINDOW_ID": "1"},
		"Alacritty by program":     {"TERM": "xterm-256color", "TERM_PROGRAM": "alacritty"},
	}
	trusted := func(env map[string]string) bool {
		return synchronizedUpdatesTrustedIn(func(name string) string { return env[name] })
	}
	with := func(env map[string]string, name, value string) map[string]string {
		changed := map[string]string{name: value}
		for k, v := range env {
			if k != name {
				changed[k] = v
			}
		}
		return changed
	}
	for name, env := range implementers {
		if !trusted(env) {
			t.Errorf("%s: not trusted, want trusted", name)
		}
		for _, in := range []struct{ what, name, value string }{
			{"tmux (TMUX)", "TMUX", "/tmp/tmux-1000/default,1,0"},
			{"tmux (TERM)", "TERM", "tmux-256color"},
			{"GNU screen (STY)", "STY", "1234.pts-0.host"},
			{"GNU screen (TERM)", "TERM", "screen-256color"},
			{"Zellij", "ZELLIJ", "0"},
			{"Herdr", "HERDR_ENV", "1"},
			{"PIG_TUI_SCROLL_ROWS=0", "PIG_TUI_SCROLL_ROWS", "0"},
			{"TERM=dumb", "TERM", "dumb"},
			{"no TERM", "TERM", ""},
		} {
			if trusted(with(env, in.name, in.value)) {
				t.Errorf("%s behind %s: trusted, want Pi's interval", name, in.what)
			}
		}
	}
	for name, env := range map[string]map[string]string{
		"xterm-256color alone":      {"TERM": "xterm-256color"},
		"xterm-256color, truecolor": {"TERM": "xterm-256color", "COLORTERM": "truecolor"},
		"Apple Terminal":            {"TERM": "xterm-256color", "TERM_PROGRAM": "Apple_Terminal"},
		"Windows Terminal":          {"TERM": "xterm-256color", "WT_SESSION": "1"},
		"VS Code terminal":          {"TERM": "xterm-256color", "TERM_PROGRAM": "vscode", "TERM_PROGRAM_VERSION": "1.99.0"},
		"GNOME Terminal (VTE)":      {"TERM": "xterm-256color", "VTE_VERSION": "7600"},
		"Linux console":             {"TERM": "linux"},
		"xterm":                     {"TERM": "xterm", "XTERM_VERSION": "XTerm(390)"},
		"dumb":                      {"TERM": "dumb"},
		"tmux alone":                {"TERM": "tmux-256color", "TMUX": "/tmp/tmux-1000/default,1,0"},
		"screen alone":              {"TERM": "screen-256color", "STY": "1234.pts-0.host"},
		"empty environment":         {},
	} {
		if trusted(env) {
			t.Errorf("%s: trusted, want Pi's interval", name)
		}
	}
}

// A frame whose interval has already passed is armed with no delay under the adaptive policy, as under Pi's: the first frame of a run on a screen that was idle, and the first frame after the policy turns on in the middle of a stream.
func TestFrameWhoseIntervalHasPassedIsArmedWithNoDelay(t *testing.T) {
	for _, open := range []bool{false, true} {
		s := newPacingScreen(t)
		s.frame(time.Millisecond, 0)
		s.gate.Store(open)
		s.at = s.at.Add(500 * time.Millisecond)
		s.ui.requestRender(false)
		if got := s.lastDelay(); got != 0 {
			t.Errorf("gate open=%v: the first frame after an idle screen waits %v, want no delay", open, got)
		}
	}

	// The last frame began 12 ms ago at Pi's interval. With the gate closed the next one waits Pi's 4 ms; once the gate is open the adaptive interval of 10 ms has passed.
	for _, tc := range []struct {
		open bool
		want time.Duration
	}{{false, 4 * time.Millisecond}, {true, 0}} {
		s := newPacingScreen(t)
		s.gate.Store(false)
		s.frame(time.Millisecond, 0)
		s.at = s.at.Add(11 * time.Millisecond)
		s.gate.Store(tc.open)
		s.ui.requestRender(false)
		if got := s.lastDelay(); got != tc.want {
			t.Errorf("gate open=%v: a frame asked for 12 ms after the last one began waits %v, want %v", tc.open, got, tc.want)
		}
	}
}

// The loop that keeps the frame deadline itself is handed a deadline that has already come for such a frame, so its timer ticks at once.
func TestOwnerDeadlineOfAFrameWhoseAdaptiveIntervalHasPassedIsNow(t *testing.T) {
	s := newPacingScreen(t)
	s.gate.Store(false)
	s.frame(time.Millisecond, 0)
	var due []time.Time
	s.ui.SetRenderDeadline(func(d time.Time) { due = append(due, d) })
	s.at = s.at.Add(11 * time.Millisecond)
	s.gate.Store(true)
	s.ui.requestRender(false)
	if len(due) == 0 || !due[len(due)-1].Equal(s.at) {
		t.Fatalf("deadlines handed to the owner = %v, want the last one now, %v", due, s.at)
	}
}

// A frame already pending when the policy turns on keeps the deadline Pi's interval gave it: the one timer armed for it draws it, no sooner and no later.
func TestPendingFrameKeepsItsDeadlineWhenThePolicyTurnsOn(t *testing.T) {
	s := newPacingScreen(t)
	s.gate.Store(false)
	s.frame(time.Millisecond, 0)
	s.at = s.at.Add(time.Millisecond)
	s.ui.requestRender(false)
	armed := len(s.delays)
	s.gate.Store(true)
	s.at = s.at.Add(3 * time.Millisecond)
	s.ui.requestRender(false)
	if len(s.delays) != armed || s.lastDelay() != 14*time.Millisecond {
		t.Fatalf("timers armed %v, want the one of 14 ms and no other", s.delays[armed-1:])
	}
}
