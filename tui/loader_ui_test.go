package tui

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// renderCountingTUI is the TUI a Loader reports to: only RequestRender is implemented, which is all loader.ts calls.
type renderCountingTUI struct {
	TUI
	requests atomic.Int64
}

func (r *renderCountingTUI) RequestRender(...bool) { r.requests.Add(1) }

// loader.ts:93-100 updateDisplay: every change to the shown text ends in `this.ui.requestRender()`. The constructor's setIndicator starts the loader
// (loader.ts:41,70-77), setMessage and invalidate update the display, and so does each animation tick (loader.ts:80-84).
func TestLoaderRequestsARenderAfterEveryChangeToItsText(t *testing.T) {
	ui := &renderCountingTUI{}
	loader := NewLoader(ui, nil, nil, "Working", nil)
	if got := ui.requests.Load(); got != 1 {
		t.Fatalf("constructing a Loader requested %d renders, want 1 (setIndicator)", got)
	}
	loader.SetMessage("Still working")
	if got := ui.requests.Load(); got != 2 {
		t.Fatalf("setMessage left %d requests, want 2", got)
	}
	loader.SetIndicator(&LoaderIndicatorOptions{Frames: []string{"x"}})
	if got := ui.requests.Load(); got != 3 {
		t.Fatalf("setIndicator left %d requests, want 3", got)
	}
	loader.Invalidate()
	if got := ui.requests.Load(); got != 4 {
		t.Fatalf("invalidate left %d requests, want 4", got)
	}

	// A running animation requests a render on each tick.
	ticking := &renderCountingTUI{}
	animated := NewLoader(ticking, nil, nil, "Working", &LoaderIndicatorOptions{Frames: []string{"a", "b"}, IntervalMs: 1})
	before := ticking.requests.Load()
	animated.Start()
	t.Cleanup(animated.Stop)
	deadline := time.Now().Add(5 * time.Second)
	for ticking.requests.Load() < before+3 {
		if time.Now().After(deadline) {
			t.Fatalf("the animation never requested a render after Start (requests %d, before %d)", ticking.requests.Load(), before)
		}
		time.Sleep(time.Millisecond)
	}
}

// loader.ts:29,36,97: ui is optional (`TUI | null`), so a Loader without one changes its text and requests nothing.
func TestLoaderWithoutAUITakesNoRenderRequests(t *testing.T) {
	loader := NewLoader(nil, nil, nil, "Working", nil)
	loader.SetMessage("x")
	loader.Invalidate()
	loader.Start()
	loader.Stop()
}

// loader.ts:25-27,86-99: the spinner frame goes through spinnerColorFn unless the indicator is verbatim, and the message goes through messageColorFn.
func TestLoaderStylesTheSpinnerAndTheMessageSeparately(t *testing.T) {
	spinner := func(text string) string { return "<s>" + text + "</s>" }
	message := func(text string) string { return "<m>" + text + "</m>" }
	line := func(l *Loader) string { return strings.TrimSpace(l.Render(40)[1]) }

	if got := line(NewLoader(nil, spinner, message, "go", nil)); got != "<s>⠋</s> <m>go</m>" {
		t.Fatalf("default spinner = %q", got)
	}
	if got := line(NewLoader(nil, spinner, message, "go", &LoaderIndicatorOptions{Frames: []string{"*"}})); got != "* <m>go</m>" {
		t.Fatalf("an indicator renders verbatim, without the spinner color: %q", got)
	}
	if got := line(NewLoader(nil, nil, nil, "go", nil)); got != "⠋ go" {
		t.Fatalf("without color functions the text stays as it is: %q", got)
	}
}

// The remote editor carries a color function as the escape sequence it writes before its text.
func TestLoaderColorPrefixIsWhatTheFunctionWritesBeforeItsText(t *testing.T) {
	loader := NewLoader(nil, SGRColor("\x1b[36m"), ThemeFg("muted"), "go", nil)
	if got := loader.SpinnerColorPrefix(); got != "\x1b[36m" {
		t.Fatalf("spinner prefix = %q", got)
	}
	if got, want := loader.MessageColorPrefix(), strings.TrimSuffix(strings.SplitN(ActiveTheme().Fg("muted", "\x00"), "\x00", 2)[0], ""); got != want || got == "" {
		t.Fatalf("message prefix = %q, want the active theme's muted prefix %q", got, want)
	}
	if got := NewLoader(nil, nil, nil, "go", nil).SpinnerColorPrefix(); got != "" {
		t.Fatalf("no color function has prefix %q", got)
	}
}
