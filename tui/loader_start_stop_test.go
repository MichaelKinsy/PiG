package tui

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"
)

func loaderFrame(l *Loader) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.Frame
}

// upstream: components/loader.ts start() advances the frame every intervalMs while there is more than one frame; stop() ends the timer.
func TestLoaderStartAnimatesAndStopHalts(t *testing.T) {
	l := NewLoader(nil, nil, nil, "Working", nil)
	l.Frames = []string{"a", "b", "c"}
	l.IntervalMs = 5
	l.Start()
	deadline := time.Now().Add(2 * time.Second)
	for loaderFrame(l) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	l.Stop()
	if loaderFrame(l) == 0 {
		t.Fatal("Start did not advance the frame")
	}
	if !l.IsDirty() {
		t.Fatal("the animation did not mark the loader for repaint")
	}
	stopped := loaderFrame(l)
	time.Sleep(40 * time.Millisecond)
	if got := loaderFrame(l); got != stopped {
		t.Fatalf("frame moved from %d to %d after Stop", stopped, got)
	}
	l.Stop() // a second Stop is a no-op
}

// upstream: restartAnimation returns without a timer for one frame or fewer.
func TestLoaderStartWithOneFrameStartsNoTimer(t *testing.T) {
	l := NewLoader(nil, nil, nil, "x", nil)
	l.Frames = []string{"only"}
	l.IntervalMs = 1
	l.Start()
	defer l.Stop()
	time.Sleep(20 * time.Millisecond)
	if l.stopCh != nil || loaderFrame(l) != 0 {
		t.Fatalf("a single-frame loader animated: timer=%v frame=%d", l.stopCh != nil, loaderFrame(l))
	}
}

// upstream: Loader extends Text, so setCustomBgFn applies to the rendered line, and updateDisplay rewrites a setText on the next render.
func TestLoaderInheritsTextMembers(t *testing.T) {
	l := NewLoader(nil, nil, nil, "msg", nil)
	l.Frames = nil
	l.SetCustomBgFn(func(s string) string { return "[" + s + "]" })
	lines := l.Render(20)
	if len(lines) != 2 || lines[0] != "" || !strings.HasPrefix(lines[1], "[") || !strings.Contains(lines[1], "msg") {
		t.Fatalf("lines = %q, want the custom background applied", lines)
	}
	l.SetText("overwritten")
	if got := strings.Join(l.Render(20), "\n"); strings.Contains(got, "overwritten") || !strings.Contains(got, "msg") {
		t.Fatalf("render after SetText = %q, want the loader's own message", got)
	}
}

// upstream: components/loader.ts setIndicator(indicator?) — undefined restores the default frames and spinner color; a defined indicator renders verbatim, keeps an explicit empty frames array (hidden), and takes intervalMs only when positive.
func TestLoaderSetIndicatorOptions(t *testing.T) {
	cases := []struct {
		name         string
		options      *LoaderIndicatorOptions
		frames       []string
		verbatim     bool
		intervalMs   int
		renderedText string
	}{
		{"undefined", nil, DefaultSpinnerFrames, false, DefaultLoaderIntervalMs, "<s>⠋</s> m"},
		{"empty options keep the defaults but render verbatim", &LoaderIndicatorOptions{}, DefaultSpinnerFrames, true, DefaultLoaderIntervalMs, "⠋ m"},
		{"custom frames and interval", &LoaderIndicatorOptions{Frames: []string{"x", "y"}, IntervalMs: 33}, []string{"x", "y"}, true, 33, "x m"},
		{"empty frames hide the indicator", &LoaderIndicatorOptions{Frames: []string{}, IntervalMs: 5}, []string{}, true, 5, "m"},
		{"non-positive interval selects the default", &LoaderIndicatorOptions{Frames: []string{"x"}, IntervalMs: -4}, []string{"x"}, true, DefaultLoaderIntervalMs, "x m"},
		{"fractional interval below one is one", &LoaderIndicatorOptions{Frames: []string{"x"}, IntervalMs: 0.5}, []string{"x"}, true, 1, "x m"},
		// Node's setInterval turns a delay above TIMEOUT_MAX (2^31-1), Infinity included, into 1 and truncates a fraction.
		{"fractional interval is truncated", &LoaderIndicatorOptions{Frames: []string{"x"}, IntervalMs: 33.9}, []string{"x"}, true, 33, "x m"},
		{"interval at TIMEOUT_MAX is kept", &LoaderIndicatorOptions{Frames: []string{"x"}, IntervalMs: math.MaxInt32}, []string{"x"}, true, math.MaxInt32, "x m"},
		{"interval above TIMEOUT_MAX is one", &LoaderIndicatorOptions{Frames: []string{"x"}, IntervalMs: math.MaxInt32 + 1}, []string{"x"}, true, 1, "x m"},
		{"infinite interval is one", &LoaderIndicatorOptions{Frames: []string{"x"}, IntervalMs: math.Inf(1)}, []string{"x"}, true, 1, "x m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := NewLoader(nil, SGRColor("<s>"), nil, "m", nil)
			l.SetIndicator(&LoaderIndicatorOptions{Frames: []string{"stale"}, IntervalMs: 99})
			l.Frame = 1
			l.SetIndicator(tc.options)
			if !slices.Equal(l.Frames, tc.frames) || l.IndicatorVerbatim != tc.verbatim || l.IntervalMs != tc.intervalMs || l.Frame != 0 {
				t.Fatalf("frames=%q verbatim=%v interval=%d frame=%d", l.Frames, l.IndicatorVerbatim, l.IntervalMs, l.Frame)
			}
			lines := l.Render(40)
			got := strings.TrimSpace(strings.ReplaceAll(lines[1], "\x1b[39m", "</s>"))
			if got != tc.renderedText {
				t.Fatalf("rendered %q, want %q", got, tc.renderedText)
			}
		})
	}
}
