// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

//go:build linux || darwin

package tui

import (
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/screenwriter/gridterm"
)

// The adaptive policy (D625) while it is active, which it never is inside a multiplexer: the scenarios of the terminal-engine corpus are replayed as streams through a fullscreen renderer on a ProcessTerminal over a real pseudo-terminal, with the screen writer on, a terminal of the list declared in the environment, the gate open, the scheduler on the real clock and a loop that owns the frame deadline as interactive mode's does (SetRenderDeadline, RunDueRender). Each step of a scenario changes the screen's content and requests a frame, pacedStepGap after the step before it, so the scheduler decides which states get a frame. Every pacedSettleEvery steps, and at the end, the stream pauses until a frame has drawn the newest state and the terminal's output has ended, and what the master end read so far is shown on gridterm's screens. A scenario is replayed once with Pi's interval and once with the policy: the settled screens must be the same, and the same as the screens of the scenario drawn frame by frame, which are the terminal-engine corpus.

const (
	pacedStepGap     = 2 * time.Millisecond
	pacedSettleEvery = 40
)

// pacedReplay is one replay of a scenario and the screens it left at its settle points.
type pacedReplay struct {
	t      *testing.T
	screen *swScreen
	ui     *TuiAltScreen
	// owner is held by whoever touches the component tree: the scenario while it changes the content, the loop while it draws a frame.
	owner sync.Mutex
	// wake tells the loop that the frame deadline changed.
	wake chan struct{}
	done chan struct{}
	// version counts the steps; drawn is the version the last frame showed. Both under owner.
	version, drawn int
	// frames are the frames the loop drew; paced are those, forced ones aside, that began sooner after the frame before them than Pi's interval lets one begin. Both under owner.
	frames, paced int
	// shown is when each frame began that sent the terminal something; quietFrames are the frames that sent it nothing. Both under owner.
	shown       []time.Time
	quietFrames int
	// at is the manual clock when a stream steps deterministically instead of sleeping on the wall clock; zero means wall clock. Under owner.
	at time.Time
	// settled are the screens at the settle points, on every width model, and settledAt the version each shows.
	settled   [][]*gridterm.Terminal
	settledAt []int
}

// pacedContent is the scenario's transcript, which notes the version every frame shows.
type pacedContent struct {
	*termLines
	r *pacedReplay
}

func (c pacedContent) Render(width int) []string {
	c.r.drawn = c.r.version
	return c.termLines.Render(width)
}

// runPacedReplay replays sc with the adaptive policy or with Pi's interval.
func runPacedReplay(t *testing.T, sc termScenario, adaptive bool) *pacedReplay {
	t.Helper()
	r, text, dock := startPacedReplay(t, sc, adaptive)
	sc.play(&termSession{t: t, ui: r.ui, draw: r.step, quiet: r.quiet}, text, dock)
	if r.settledAt[len(r.settledAt)-1] != r.version {
		r.settle()
	}
	r.owner.Unlock()
	return r
}

// startPacedReplay starts the renderer of a replay on a screen of sc's size, with the adaptive policy or with Pi's interval, and returns once the first screen has settled, holding owner; text is the transcript and dock what is under it.
func startPacedReplay(t *testing.T, sc termScenario, adaptive bool) (r *pacedReplay, text, dock *termLines) {
	t.Helper()
	for name, value := range map[string]string{"TERM": "xterm-256color", "ALACRITTY_WINDOW_ID": "1", "TERM_PROGRAM": "", "TERM_PROGRAM_VERSION": "", "TMUX": "", "STY": "", "ZELLIJ": "", "HERDR_ENV": "", "PIG_TUI_SCROLL_ROWS": ""} {
		t.Setenv(name, value)
	}
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	t.Cleanup(ResetCapabilitiesCache)
	r = &pacedReplay{t: t, wake: make(chan struct{}, 1), done: make(chan struct{})}
	r.screen = newSWScreen(t, true, sc.cols, sc.rows, 4096, 0)
	r.ui = NewTuiAltScreen(r.screen.term, nil, "")
	if adaptive {
		gate := new(atomic.Bool)
		gate.Store(true)
		pacing := NewAdaptiveFramePacing(gate)
		if !pacing.terminalTrusted {
			t.Fatal("the declared terminal is not trusted with synchronized updates: no frame would be paced")
		}
		r.ui.SetFramePacing(pacing)
	}
	r.ui.SetRenderDeadline(func(time.Time) {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	})
	go r.loop()
	text, dock = &termLines{unclipped: sc.unclipped}, &termLines{unclipped: sc.unclipped}
	r.ui.Add(pacedContent{text, r})
	if sc.dock != nil {
		dock.lines = slices.Clone(sc.dock.lines)
		r.ui.Add(dock)
	}
	r.owner.Lock()
	r.ui.Start()
	t.Cleanup(func() {
		r.ui.Stop()
		close(r.done)
	})
	if !r.screen.term.ScreenWriterActive() {
		t.Fatal("the screen writer is not active on the alternate screen: no frame would be paced")
	}
	r.settle()
	return r, text, dock
}

// loop keeps the frame deadline and draws the frame when it comes, as the owner loop of interactive mode does.
func (r *pacedReplay) loop() {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-r.wake:
		case <-timer.C:
		}
		r.owner.Lock()
		r.ui.mu.Lock()
		due := !r.ui.frameDue.IsZero() && !r.ui.now().Before(r.ui.frameDue)
		// Pi's frames begin 15 to 16 ms apart (armFrameLocked cuts the delay to whole milliseconds, as Node does); a forced frame is drawn at once under either policy.
		paced := due && !r.ui.forceRedraw && !r.ui.lastRenderAt.IsZero() && r.ui.now().Sub(r.ui.lastRenderAt) < minRenderInterval-time.Millisecond
		r.ui.mu.Unlock()
		if due {
			r.frames++
			if paced {
				r.paced++
			}
			began, sent := time.Now(), r.screen.term.writesSent()
			r.ui.RunDueRender()
			if r.screen.term.writesSent() != sent {
				r.shown = append(r.shown, began)
			} else {
				r.quietFrames++
			}
		}
		r.ui.mu.Lock()
		next := r.ui.frameDue
		r.ui.mu.Unlock()
		r.owner.Unlock()
		if next.IsZero() {
			timer.Reset(time.Hour)
		} else {
			timer.Reset(time.Until(next))
		}
	}
}

// step is one step of the stream: the content changed (and the terminal's size, when resize is set), a frame is requested, and the next step follows pacedStepGap later or after a settle point. The scenario holds owner.
func (r *pacedReplay) step(resize []int) {
	r.t.Helper()
	if resize != nil {
		// The renderer has the new size and has drawn nothing with it: the loop cannot draw before the pseudo-terminal has it too.
		r.screen.resize(resize[0], resize[1])
	}
	r.version++
	r.ui.requestRender(false)
	if r.version%pacedSettleEvery == 0 {
		r.settle()
		return
	}
	r.owner.Unlock()
	time.Sleep(pacedStepGap)
	r.owner.Lock()
}

// quiet waits until a frame has drawn the newest state and none is pending. The caller holds owner.
func (r *pacedReplay) quiet() {
	r.t.Helper()
	r.owner.Unlock()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(time.Millisecond) {
		r.owner.Lock()
		r.ui.mu.Lock()
		quiet := r.drawn == r.version && r.ui.frameDue.IsZero() && !r.ui.renderRequested
		r.ui.mu.Unlock()
		if quiet {
			return
		}
		r.owner.Unlock()
		if time.Now().After(deadline) {
			r.t.Fatal("no frame drew the stream's newest state")
		}
	}
}

// settle is a settle point: once the stream is quiet and the terminal's output and the screen writer's settle write have ended, the screens are kept. The cursor is hidden, and where the last frame's last write left it is not on the screen (it depends on which rows that frame changed), so the kept screens have it at home. The caller holds owner.
func (r *pacedReplay) settle() {
	r.t.Helper()
	r.quiet()
	r.owner.Unlock()
	r.screen.settle()
	r.owner.Lock()
	if sent := r.screen.transcript(); strings.LastIndex(sent, "\x1b[?25l") < strings.LastIndex(sent, "\x1b[?25h") || !strings.Contains(sent, "\x1b[?25l") {
		r.t.Fatal("the cursor is shown at a settle point: where it is would be on the screen")
	}
	screens := r.screen.screens(0)
	for _, screen := range screens {
		screen.Write("\x1b[H")
	}
	r.settled = append(r.settled, screens)
	r.settledAt = append(r.settledAt, r.version)
}

// pacedOracle is the scenario drawn frame by frame with no scheduler, as the terminal-engine corpus records it, shown on gridterm's first width model after the frame of every version in at.
func pacedOracle(t *testing.T, sc termScenario, at []int) []*gridterm.Terminal {
	t.Helper()
	variant := &termCorpusVariant{}
	playTermScenario(t, sc, &termRecorder{variant}, true)
	var screens []*gridterm.Terminal
	for _, version := range at {
		if version >= len(variant.Steps) {
			break
		}
		screen := gridterm.New(sc.cols, sc.rows, gridterm.Models[0])
		start := 0
		for _, step := range variant.Steps[:version+1] {
			if step.Resize != nil {
				screen.Resize(step.Resize[0], step.Resize[1])
			}
			screen.Write(string(variant.data[start:step.End]))
			start = step.End
		}
		screens = append(screens, screen)
	}
	return screens
}

// The settled screens of every stream are the same under the adaptive policy as under Pi's interval, on every width model, though the policy draws other frames between them, and their cells are those of the corpus. With Pi's interval no frame begins sooner than Pi's lets it; with the policy frames do, or the stream would prove nothing.
func TestAdaptivePacingShowsTheSameSettledScreens(t *testing.T) {
	var streams, points, steps, pacedFrames, differences int
	for _, sc := range append(slices.Concat(termCorpusScenarios, termKnownScenarios), termRandomMix(2)) {
		t.Run(sc.name, func(t *testing.T) {
			pi := runPacedReplay(t, sc, false)
			adaptive := runPacedReplay(t, sc, true)
			t.Logf("%d steps, %d settle points; Pi's interval: %d frames, %d paced; adaptive: %d frames, %d paced", pi.version, len(pi.settled), pi.frames, pi.paced, adaptive.frames, adaptive.paced)
			if pi.paced != 0 {
				t.Errorf("with Pi's interval %d of %d frames began sooner than Pi's interval lets one begin", pi.paced, pi.frames)
			}
			if adaptive.paced == 0 {
				t.Errorf("with the adaptive policy none of %d frames was paced: the stream proves nothing", adaptive.frames)
			}
			if !slices.Equal(pi.settledAt, adaptive.settledAt) {
				t.Fatalf("the two replays settled at different steps: %v and %v", pi.settledAt, adaptive.settledAt)
			}
			for point := range pi.settled {
				for i, model := range gridterm.Models {
					if d := gridterm.Differ(pi.settled[point][i], adaptive.settled[point][i]); d != "" {
						differences++
						t.Errorf("settle point %d (step %d), widths %s: %s", point, pi.settledAt[point], model.Name, d)
						break
					}
				}
			}
			oracle := pacedOracle(t, sc, pi.settledAt)
			if len(oracle) != len(pi.settled) {
				t.Fatalf("the corpus has %d of the %d settle points", len(oracle), len(pi.settled))
			}
			for point, screen := range oracle {
				for name, replay := range map[string]*pacedReplay{"Pi's interval": pi, "the adaptive policy": adaptive} {
					// The corpus stream has no screen writer under it and ends where the renderer's last write did: only the cells are compared.
					if d := gridterm.Differ(screen, replay.settled[point][0]); strings.HasPrefix(d, "row ") {
						differences++
						t.Errorf("settle point %d (step %d) with %s against the corpus: %s", point, pi.settledAt[point], name, d)
					}
				}
			}
			streams++
			points += len(pi.settled)
			steps += pi.version
			pacedFrames += adaptive.paced
		})
	}
	t.Logf("%d streams, %d steps, %d settle points, %d frames paced, %d settled screens that differ", streams, steps, points, pacedFrames, differences)
}

// runDueManual draws any frame due at r.at on the manual clock, updating r's counters the same way loop does. Caller holds owner and ui uses r.at.
func (r *pacedReplay) runDueManual() {
	for {
		r.ui.mu.Lock()
		due := !r.ui.frameDue.IsZero() && !r.at.Before(r.ui.frameDue)
		paced := due && !r.ui.forceRedraw && !r.ui.lastRenderAt.IsZero() && r.at.Sub(r.ui.lastRenderAt) < minRenderInterval-time.Millisecond
		r.ui.mu.Unlock()
		if !due {
			return
		}
		r.frames++
		if paced {
			r.paced++
		}
		began, sent := r.at, r.screen.term.writesSent()
		r.ui.RunDueRender()
		if r.screen.term.writesSent() != sent {
			r.shown = append(r.shown, began)
		} else {
			r.quietFrames++
		}
	}
}

// advanceManual advances r.at to target, running any frame deadlines that fall at or before target in chronological order. Caller holds owner.
func (r *pacedReplay) advanceManual(target time.Time) {
	for {
		r.ui.mu.Lock()
		next := r.ui.frameDue
		r.ui.mu.Unlock()
		if next.IsZero() || next.After(target) {
			r.at = target
			return
		}
		if next.After(r.at) {
			r.at = next
		}
		r.runDueManual()
	}
}

// runPacedStream asks for a frame every pacedStepGap, steps times, and changes what the screen shows at every changeEvery-th of them (never when it is 0), as a stream does whose deltas mostly change nothing on the screen. It drives the real fullscreen renderer, ProcessTerminal, screen writer and SetRenderDeadline/RunDueRender path on a deterministic manual clock so frame counts and visible-change gaps measure the pacing policy rather than host sleep jitter. It returns the replay and the bytes the terminal was sent after the first screen.
func runPacedStream(t *testing.T, adaptive bool, steps, changeEvery int) (*pacedReplay, int) {
	t.Helper()
	for name, value := range map[string]string{"TERM": "xterm-256color", "ALACRITTY_WINDOW_ID": "1", "TERM_PROGRAM": "", "TERM_PROGRAM_VERSION": "", "TMUX": "", "STY": "", "ZELLIJ": "", "HERDR_ENV": "", "PIG_TUI_SCROLL_ROWS": ""} {
		t.Setenv(name, value)
	}
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	t.Cleanup(ResetCapabilitiesCache)
	r := &pacedReplay{t: t, at: time.Unix(1_000_000, 0)}
	r.screen = newSWScreen(t, true, 80, 24, 4096, 0)
	r.ui = NewTuiAltScreen(r.screen.term, nil, "")
	r.ui.now = func() time.Time { return r.at }
	if adaptive {
		gate := new(atomic.Bool)
		gate.Store(true)
		pacing := NewAdaptiveFramePacing(gate)
		if !pacing.terminalTrusted {
			t.Fatal("the declared terminal is not trusted with synchronized updates: no frame would be paced")
		}
		r.ui.SetFramePacing(pacing)
	}
	r.ui.SetRenderDeadline(func(time.Time) {})
	text := &termLines{}
	r.ui.Add(pacedContent{text, r})
	r.ui.Start()
	t.Cleanup(r.ui.Stop)
	if !r.screen.term.ScreenWriterActive() {
		t.Fatal("the screen writer is not active on the alternate screen: no frame would be paced")
	}
	r.screen.settle()
	// Start after the initial screen has settled long enough that the first stream step is not throttled against Start's frame, matching a wall-clock settle.
	r.at = r.at.Add(time.Second)
	before, frames := len(r.screen.transcript()), r.frames
	r.shown, r.quietFrames = nil, 0
	for i := range steps {
		if changeEvery > 0 && i%changeEvery == 0 {
			text.lines = []string{"a file is being written", "written so far: " + strconv.Itoa(i) + " steps"}
		}
		r.version++
		r.ui.requestRender(false)
		r.runDueManual()
		r.advanceManual(r.at.Add(pacedStepGap))
	}
	// Flush any final pending frame due after the last step.
	for {
		r.ui.mu.Lock()
		next := r.ui.frameDue
		r.ui.mu.Unlock()
		if next.IsZero() {
			break
		}
		r.advanceManual(next)
	}
	if r.drawn != r.version {
		t.Fatalf("stream ended at version %d, last drawn %d", r.version, r.drawn)
	}
	r.frames -= frames
	r.screen.settle()
	return r, len(r.screen.transcript()) - before
}

// longGaps counts the times from one frame that showed something to the next that are 18 ms or more: two of the adaptive policy's 10 ms intervals.
func longGaps(shown []time.Time) (n int) {
	for i := 1; i < len(shown); i++ {
		if shown[i].Sub(shown[i-1]) >= 18*time.Millisecond {
			n++
		}
	}
	return n
}

// A stream shaped like a file being written asks for a frame every 2 ms and changes the screen every 12 ms. Under Pi's interval every frame shows a change. Under the adaptive policy a frame 10 ms after the last finds nothing changed and the screen writer sends nothing; that frame does not restart the interval, so the change that comes 2 ms later is drawn then and not 10 ms later: the gaps of 18 ms or more between frames that showed something are as rare as under Pi's interval.
func TestAdaptivePacingDoesNotWaitOutAFrameThatSentNothing(t *testing.T) {
	const steps, changeEvery = 600, 6
	pi, _ := runPacedStream(t, false, steps, changeEvery)
	adaptive, _ := runPacedStream(t, true, steps, changeEvery)
	piLong, adaptiveLong := longGaps(pi.shown), longGaps(adaptive.shown)
	t.Logf("%d steps, the screen changes at every %dth; Pi's interval: %d frames, %d showed something, %d gaps of 18 ms or more; adaptive: %d frames, %d showed something, %d sent nothing, %d gaps of 18 ms or more", steps, changeEvery, pi.frames, len(pi.shown), piLong, adaptive.frames, len(adaptive.shown), adaptive.quietFrames, adaptiveLong)
	if adaptive.quietFrames == 0 || adaptive.paced == 0 || len(pi.shown) < steps/changeEvery/2 {
		t.Fatalf("with the adaptive policy %d frames sent nothing and %d were paced, with Pi's interval %d frames showed something: the stream proves nothing", adaptive.quietFrames, adaptive.paced, len(pi.shown))
	}
	if limit := piLong + len(adaptive.shown)/10; adaptiveLong > limit {
		t.Errorf("with the adaptive policy %d of %d gaps between frames that showed something are 18 ms or more, with Pi's interval %d of %d: want no more than %d", adaptiveLong, len(adaptive.shown)-1, piLong, len(pi.shown)-1, limit)
	}
	if len(adaptive.shown) < len(pi.shown) {
		t.Errorf("with the adaptive policy %d frames showed something, with Pi's interval %d: want no fewer", len(adaptive.shown), len(pi.shown))
	}
}

// A stream that changes nothing on the screen, as a reply does that streams below the viewport of a transcript scrolled up, asks for a frame every 2 ms. Under Pi's interval that is a frame every 16 ms, none of which the screen writer sends on. Under the adaptive policy it is no more: after the second frame in a row that sent nothing the interval is Pi's.
func TestAdaptivePacingDrawsAStreamThatChangesNothingAsOftenAsPi(t *testing.T) {
	const steps = 600
	pi, piBytes := runPacedStream(t, false, steps, 0)
	adaptive, adaptiveBytes := runPacedStream(t, true, steps, 0)
	t.Logf("%d steps that change nothing; Pi's interval: %d frames, %d bytes sent; adaptive: %d frames, %d bytes sent", steps, pi.frames, piBytes, adaptive.frames, adaptiveBytes)
	if len(pi.shown) != 0 || len(adaptive.shown) != 0 {
		t.Fatalf("%d frames with Pi's interval and %d with the adaptive policy sent the terminal something: the stream changes the screen", len(pi.shown), len(adaptive.shown))
	}
	if limit := pi.frames + pi.frames/10 + 2; adaptive.frames > limit {
		t.Errorf("with the adaptive policy %d frames were drawn, with Pi's interval %d: want no more than %d", adaptive.frames, pi.frames, limit)
	}
	if adaptiveBytes > piBytes {
		t.Errorf("with the adaptive policy the terminal was sent %d bytes, with Pi's interval %d", adaptiveBytes, piBytes)
	}
}
