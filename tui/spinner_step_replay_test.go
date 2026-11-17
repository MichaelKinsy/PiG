// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

//go:build linux || darwin

package tui

import (
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/screenwriter/gridterm"
)

// stepAtOnce is the TUI of a Loader that asks for the frame of an animation step at once, as Pi's loader.ts does: it has no RequestRenderWithin, so the Loader's step goes through RequestRender.
type stepAtOnce struct {
	TUI
	asked atomic.Int64
}

func (s *stepAtOnce) RequestRender(force ...bool) {
	s.asked.Add(1)
	s.TUI.RequestRender(force...)
}

// stepCoalescing is the TUI of a Loader whose step may wait for a frame drawn for something else (D619): RequestRenderWithin, which counts the steps that came through it.
type stepCoalescing struct {
	TUI
	within atomic.Int64
}

func (s *stepCoalescing) RequestRenderWithin(delay time.Duration) {
	s.within.Add(1)
	s.TUI.(FrameWithinRequester).RequestRenderWithin(delay)
}

// spinnerStepReplay is the replay of one stream with a spinner, with the steps' frames asked for at once or coalesced.
type spinnerStepReplay struct {
	*pacedReplay
	loader *Loader
	// steps is how many animation steps the stream took, and stepFrames how many of them asked through the TUI the policy under test provides.
	steps, stepFrames int64
}

// playSpinnerStream replays a reply that streams under a spinner (the loader below the transcript steps every few bursts of the stream, some right after a frame, some after a pause), then a tool call whose output streams, then a resize in the middle of the stream, and keeps the screens it settles at.
// The spinner steps are scripted, so both replays advance the glyph at the same points of the stream; only the frame each step asks for differs.
func playSpinnerStream(t *testing.T, coalesce bool) *spinnerStepReplay {
	t.Helper()
	r, text, _ := startPacedReplay(t, termScenario{cols: 80, rows: 24}, false)
	var ui TUI
	var asked, within *atomic.Int64
	if coalesce {
		c := &stepCoalescing{TUI: r.ui}
		ui, within = c, &c.within
	} else {
		c := &stepAtOnce{TUI: r.ui}
		ui, asked = c, &c.asked
	}
	loader := NewLoader(ui, nil, nil, "Working", nil)
	tool := &termLines{}
	r.ui.Add(tool)
	r.ui.Add(loader)
	out := &spinnerStepReplay{pacedReplay: r, loader: loader}

	pause := func(d time.Duration) {
		r.owner.Unlock()
		time.Sleep(d)
		r.owner.Lock()
	}
	// An animation step: the owner of the spinner advances it and asks for the step's frame, as the interactive mode's status tick does.
	step := func() {
		loader.TickStep()
		r.version++
		out.steps++
		loader.requestStepRender(80 * time.Millisecond)
	}
	// burst is n deltas of the stream, 2 ms apart; the first of them is the one the frame follows.
	burst := func(n int, delta func(i int)) {
		for i := range n {
			delta(i)
			r.step(nil)
		}
	}
	reply := func(i int) {
		text.lines = append(text.lines, "reply line "+strconv.Itoa(len(text.lines))+" "+strings.Repeat("w", i%7))
	}
	toolOutput := func(i int) {
		tool.lines = append(tool.lines, "  tool output "+strconv.Itoa(len(tool.lines))+strings.Repeat("o", i%5))
	}

	// A reply streams under a spinner: a step comes right after a burst's frame (it may wait for the next frame), another long after the last one (it asks at once).
	for round := range 6 {
		burst(8, reply)
		pause(22 * time.Millisecond)
		step()
		if round%2 == 1 {
			pause(90 * time.Millisecond)
			step()
		}
	}
	r.settle()
	// A tool call: its header, then its output streams while the spinner steps.
	tool.lines = []string{"$ go test ./..."}
	r.step(nil)
	for range 6 {
		burst(8, toolOutput)
		pause(22 * time.Millisecond)
		step()
	}
	r.settle()
	// A resize in the middle of the stream, between a step and the frames that follow it.
	burst(4, reply)
	step()
	r.step([]int{100, 30})
	burst(8, toolOutput)
	pause(22 * time.Millisecond)
	step()
	burst(8, reply)
	pause(22 * time.Millisecond)
	step()
	if out.settledAt[len(out.settledAt)-1] != r.version {
		r.settle()
	}
	r.owner.Unlock()
	if coalesce {
		out.stepFrames = within.Load()
	} else {
		out.stepFrames = asked.Load()
	}
	return out
}

// D619: the Loader asks for the frame of a spinner step through RequestRenderWithin where Pi asks for it at once, so a step that a streaming frame draws adds no frame of its own. The record says the settled screen is the same: every screen the stream settles at is equal with the steps coalesced and with them asked for at once, on every width model,
// and the spinner shows the glyph of the last step on both.
func TestSpinnerStepCoalescingShowsTheSameSettledScreens(t *testing.T) {
	atOnce := playSpinnerStream(t, false)
	coalesced := playSpinnerStream(t, true)
	t.Logf("%d steps, %d settle points; steps at once: %d frames drawn; coalesced: %d frames drawn", atOnce.steps, len(atOnce.settled), atOnce.frames, coalesced.frames)
	if coalesced.stepFrames != coalesced.steps {
		t.Fatalf("%d of the %d steps asked for their frame through RequestRenderWithin: the replay does not exercise the coalescing", coalesced.stepFrames, coalesced.steps)
	}
	if atOnce.stepFrames < atOnce.steps {
		t.Fatalf("%d requests for %d steps with the steps asked for at once: the replay does not exercise Pi's request", atOnce.stepFrames, atOnce.steps)
	}
	if !slices.Equal(atOnce.settledAt, coalesced.settledAt) {
		t.Fatalf("the two replays settled at different steps: %v and %v", atOnce.settledAt, coalesced.settledAt)
	}
	for point := range atOnce.settled {
		for i, model := range gridterm.Models {
			if d := gridterm.Differ(atOnce.settled[point][i], coalesced.settled[point][i]); d != "" {
				t.Errorf("settle point %d (step %d), widths %s: %s", point, atOnce.settledAt[point], model.Name, d)
				break
			}
		}
	}
	// The spinner of the last settled screen shows the glyph the last step moved it to.
	glyph := DefaultSpinnerFrames[atOnce.loader.Frame%len(DefaultSpinnerFrames)] + " Working"
	for name, replay := range map[string]*spinnerStepReplay{"steps at once": atOnce, "coalesced steps": coalesced} {
		last := replay.settled[len(replay.settled)-1][0]
		shown := false
		for row := range 40 {
			shown = shown || strings.Contains(last.Row(row), glyph)
		}
		if !shown {
			t.Errorf("with %s the last settled screen has no row %q", name, glyph)
		}
	}
}
