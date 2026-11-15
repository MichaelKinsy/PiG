// SPDX-License-Identifier: MIT

package codingagent

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/compactprogress"
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
		if got := compactionStatusMessage(c.label, c.secs, c.tokens, hint); got != c.want {
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
	if got := testing.AllocsPerRun(100, func() { sink = compactionStatusMessage("Compacting context... ", 72, 500, "(escape to cancel)") }); got != 1 {
		t.Fatalf("compactionStatusMessage allocates %v times, want 1", got)
	}
	_ = sink
}
