// SPDX-License-Identifier: MIT

package codingagent

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/compactprogress"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// pig divergence (D652): the compaction line's text for each elapsed time and streamed size.
func TestCompactionStatusMessage(t *testing.T) {
	const hint = "(escape to cancel)"
	for _, c := range []struct {
		label        string
		secs, tokens int
		want         string
	}{
		{"Compacting context... ", 0, 0, "Compacting context... (escape to cancel)"},
		{"Compacting context... ", 12, 0, "Compacting context... 12s (escape to cancel)"},
		{"Compacting context... ", 72, 14_200, "Compacting context... 1m 12s · ~14k tokens (escape to cancel)"},
		{"Compacting context... ", 5, 999, "Compacting context... 5s · ~999 tokens (escape to cancel)"},
		{"Compacting context... ", 3723, 1500, "Compacting context... 1h 2m 3s · ~1.5k tokens (escape to cancel)"},
		{"Auto-compacting... ", 0, 40, "Auto-compacting... ~40 tokens (escape to cancel)"},
		{"Context overflow detected, Auto-compacting... ", 61, 0, "Context overflow detected, Auto-compacting... 1m 1s (escape to cancel)"},
	} {
		if got := compactionStatusMessage(c.label, c.secs, c.tokens, hint, math.MaxInt); got != c.want {
			t.Errorf("compactionStatusMessage(%q, %d, %d) = %q, want %q", c.label, c.secs, c.tokens, got, c.want)
		}
	}
}

// The tick rewrites the line only when the elapsed seconds or the streamed size changed, from its own clock reading; a mode without a counter shows the elapsed time alone.
func TestTickCompactionMessageFollowsClockAndCounter(t *testing.T) {
	m, _, _ := newFrontendEditorProbe(t)
	progress := new(compactprogress.Progress)
	m.opts.SessionHandle = &recordingCompactHandle{progress: progress}
	m.showCompactionStatusIndicator("manual")
	base := m.statusLastFrame
	line := func() string { return m.activeStatusIndicator.Message }
	if m.tickCompactionMessage(base.Add(300 * time.Millisecond)) {
		t.Fatal("the line changed within the first second with no tokens")
	}
	if !m.tickCompactionMessage(base.Add(7*time.Second)) || line() != "Compacting context... 7s (escape to cancel)" {
		t.Fatalf("line %q", line())
	}
	if m.tickCompactionMessage(base.Add(7*time.Second + 400*time.Millisecond)) {
		t.Fatal("the line was rewritten with neither seconds nor tokens changed")
	}
	progress.Add(56_800)
	if !m.tickCompactionMessage(base.Add(8*time.Second)) || line() != "Compacting context... 8s · ~14k tokens (escape to cancel)" {
		t.Fatalf("line %q", line())
	}
	// a retry of the summary shows the indicator again; the compaction's start is kept
	m.showCompactionStatusIndicator("manual")
	if !m.tickCompactionMessage(base.Add(30 * time.Second)) {
		t.Fatal("the retry's line was not written")
	}
	if want := "Compacting context... 30s · ~14k tokens (escape to cancel)"; line() != want {
		t.Fatalf("line %q, want %q", line(), want)
	}
	m.clearStatusIndicator("")
}

func TestTickCompactionMessageWithoutCounter(t *testing.T) {
	m, _, _ := newFrontendEditorProbe(t)
	m.opts.SessionHandle = &recordingCompactHandle{}
	m.showCompactionStatusIndicator("overflow")
	m.tickCompactionMessage(m.statusLastFrame.Add(65 * time.Second))
	if want := "Context overflow detected, Auto-compacting... 1m 5s (escape to cancel)"; m.activeStatusIndicator.Message != want {
		t.Fatalf("line %q, want %q", m.activeStatusIndicator.Message, want)
	}
	m.clearStatusIndicator("")
}

// The per-delta count allocates nothing; the line's per-tick format allocates the one string for a size below a thousand tokens.
func TestCompactionProgressAllocations(t *testing.T) {
	progress := new(compactprogress.Progress)
	if got := testing.AllocsPerRun(100, func() { progress.Add(12) }); got != 0 {
		t.Fatalf("Progress.Add allocates %v times, want 0", got)
	}
	var sink string
	if got := testing.AllocsPerRun(100, func() {
		sink = compactionStatusMessage("Compacting context... ", 72, 500, "(escape to cancel)", math.MaxInt)
	}); got != 1 {
		t.Fatalf("compactionStatusMessage allocates %v times, want 1", got)
	}
	if got := testing.AllocsPerRun(100, func() { sink = compactionStatusMessage("Compacting context... ", 72, 500, "(escape to cancel)", 30) }); got != 1 {
		t.Fatalf("compactionStatusMessage in a narrow room allocates %v times, want 1", got)
	}
	_ = sink
}

// Ticks within one second that bring no new tokens change nothing: no rewrite, so no render is asked for and nothing is allocated.
func TestTickCompactionMessageChangesNothingWithinASecond(t *testing.T) {
	m, _, _ := newFrontendEditorProbe(t)
	m.opts.SessionHandle = &recordingCompactHandle{progress: new(compactprogress.Progress)}
	m.showCompactionStatusIndicator("manual")
	base := m.statusLastFrame
	m.tickCompactionMessage(base.Add(2 * time.Second))
	changed := 0
	for i := range 10 {
		if m.tickCompactionMessage(base.Add(2*time.Second + time.Duration(i)*80*time.Millisecond)) {
			changed++
		}
	}
	if changed != 0 {
		t.Fatalf("%d of 10 ticks within one second rewrote the line", changed)
	}
	if got := testing.AllocsPerRun(100, func() { m.tickCompactionMessage(base.Add(2 * time.Second)) }); got != 0 {
		t.Fatalf("an unchanged tick allocates %v times, want 0", got)
	}
	m.clearStatusIndicator("")
}

// pig divergence (D652): in a narrow terminal the elapsed time and the size give way before the cancel hint does. At every width where Pi's own line (label and hint) fits the border, the hint is on the row; the forms fall back full, without the size, Pi's line, then today's truncation.
func TestCompactionLineInANarrowTerminal(t *testing.T) {
	const (
		full = "⠋ Compacting context... 1m 12s · ~14k tokens (escape to cancel)"
		mid  = "⠋ Compacting context... 1m 12s (escape to cancel)"
		pi   = "⠋ Compacting context... (escape to cancel)"
		cut  = "⠋ Compacting context... (escape to"
		stub = "⠋ Compacting context..."
	)
	for _, c := range []struct {
		width  int
		status string
	}{{120, full}, {60, mid}, {48, pi}, {40, cut}, {30, stub}} {
		m, _, _ := newFrontendEditorProbe(t)
		tuiInst := m.tuiInst
		m.tuiInst = &fixedWidthTUI{TUI: tuiInst, width: c.width}
		m.editor.EmbedWorkingStatus = true
		progress := new(compactprogress.Progress)
		progress.Add(56_800)
		m.opts.SessionHandle = &recordingCompactHandle{progress: progress}
		m.showCompactionStatusIndicator("manual")
		m.tickCompactionMessage(m.statusLastFrame.Add(72 * time.Second))
		row := widthx.StripAnsi(m.editor.Render(c.width)[0])
		want := "── " + c.status + " " + strings.Repeat("─", c.width-widthx.VisibleWidth(c.status)-4)
		if row != want {
			t.Errorf("row at %d columns = %q, want %q", c.width, row, want)
		}
		if piFits := c.width-5 >= widthx.VisibleWidth(pi); piFits && !strings.Contains(row, "(escape to cancel)") {
			t.Errorf("row at %d columns lost the hint where Pi's line fits: %q", c.width, row)
		}
		m.clearStatusIndicator("")
		m.tuiInst = tuiInst
	}
}

// fixedWidthTUI is a TUI that reports the width a test sets, the width the editor border is drawn at.
type fixedWidthTUI struct {
	tui.TUI
	width int
}

func (u *fixedWidthTUI) Width() int { return u.width }

// pig divergence (D652): the line is rewritten when the room for it changes, with no tick bringing a new second or size.
func TestTickCompactionMessageFollowsTheWidth(t *testing.T) {
	m, _, _ := newFrontendEditorProbe(t)
	tuiInst := m.tuiInst
	defer func() { m.tuiInst = tuiInst }()
	m.opts.SessionHandle = &recordingCompactHandle{progress: new(compactprogress.Progress)}
	m.tuiInst = &fixedWidthTUI{TUI: tuiInst, width: 120}
	m.showCompactionStatusIndicator("manual")
	at := m.statusLastFrame.Add(72 * time.Second)
	m.tickCompactionMessage(at)
	if want := "Compacting context... 1m 12s (escape to cancel)"; m.activeStatusIndicator.Message != want {
		t.Fatalf("line %q, want %q", m.activeStatusIndicator.Message, want)
	}
	m.tuiInst = &fixedWidthTUI{TUI: tuiInst, width: 48}
	if !m.tickCompactionMessage(at) || m.activeStatusIndicator.Message != "Compacting context... (escape to cancel)" {
		t.Fatalf("line %q after the terminal narrowed", m.activeStatusIndicator.Message)
	}
	if m.tickCompactionMessage(at) {
		t.Fatal("an unchanged width rewrote the line")
	}
	m.clearStatusIndicator("")
}
