package codingagent

import (
	"io"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui"
)

// stepRecordingTUI counts what a status-indicator tick asks its TUI for.
type stepRecordingTUI struct {
	tui.TUI
	renders  int
	requests int
	within   []time.Duration
}

func (r *stepRecordingTUI) RequestRender(...bool)               { r.requests++ }
func (r *stepRecordingTUI) Render()                             { r.renders++ }
func (r *stepRecordingTUI) RequestRenderWithin(d time.Duration) { r.within = append(r.within, d) }

// plainRenderTUI is a TUI with no RequestRenderWithin: it draws the tick at once, as Pi does.
type plainRenderTUI struct {
	tui.TUI
	renders int
}

func (r *plainRenderTUI) Render() { r.renders++ }

// Pi's Loader asks for the frame of an animation step; PiG's renderers draw it within min(40 ms, interval/2), so that a frame the stream asks for in that time draws it (D619).
func TestStatusTickAsksForItsFrameWithinTheStepDelay(t *testing.T) {
	m := statusBorderMode(t, false)
	ui := &stepRecordingTUI{TUI: m.tuiInst}
	m.tuiInst = ui
	m.isIdle = true
	m.handleAgentEvent(agent.CompactionStartEvent{Reason: "manual"})
	before, requestsBefore := ui.renders, ui.requests
	m.tickStatusIndicators(m.statusLastFrame.Add(80 * time.Millisecond))
	if m.activeStatusIndicator.Frame != 1 {
		t.Fatalf("the tick did not advance the spinner (frame %d)", m.activeStatusIndicator.Frame)
	}
	if len(ui.within) != 1 || ui.within[0] != 40*time.Millisecond {
		t.Fatalf("the tick asked within %v, want one request of 40ms", ui.within)
	}
	if ui.renders != before {
		t.Fatalf("the tick drew %d frames at once, want none", ui.renders-before)
	}
	if ui.requests != requestsBefore {
		t.Fatalf("the tick requested %d frames at once, want none", ui.requests-requestsBefore)
	}
}

func TestStatusTickDrawsAtOnceOnATUIThatCannotWait(t *testing.T) {
	m := statusBorderMode(t, false)
	ui := &plainRenderTUI{TUI: tui.NewWithOutput(io.Discard, 80, 24)}
	m.tuiInst = ui
	m.isIdle = true
	m.handleAgentEvent(agent.CompactionStartEvent{Reason: "manual"})
	before := ui.renders
	m.tickStatusIndicators(m.statusLastFrame.Add(80 * time.Millisecond))
	if ui.renders != before+1 {
		t.Fatalf("the tick drew %d frames at once, want 1", ui.renders-before)
	}
}
