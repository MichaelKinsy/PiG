// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package tui

import (
	"slices"
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
	for name, value := range map[string]string{"TERM": "xterm-256color", "ALACRITTY_WINDOW_ID": "1", "TERM_PROGRAM": "", "TERM_PROGRAM_VERSION": "", "TMUX": "", "STY": "", "ZELLIJ": "", "HERDR_ENV": "", "PIG_TUI_SCROLL_ROWS": ""} {
		t.Setenv(name, value)
	}
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	t.Cleanup(ResetCapabilitiesCache)
	r := &pacedReplay{t: t, wake: make(chan struct{}, 1), done: make(chan struct{})}
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
	text, dock := &termLines{unclipped: sc.unclipped}, &termLines{unclipped: sc.unclipped}
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
	sc.play(&termSession{t: t, ui: r.ui, draw: r.step, quiet: r.quiet}, text, dock)
	if r.settledAt[len(r.settledAt)-1] != r.version {
		r.settle()
	}
	r.owner.Unlock()
	return r
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
			r.ui.RunDueRender()
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
