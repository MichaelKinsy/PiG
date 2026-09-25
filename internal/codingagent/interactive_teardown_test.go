package codingagent

import (
	"bytes"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// stopSpyRenderer embeds a real renderer (so it satisfies tui.Renderer) and
// counts Stop() invocations, proving the centralized teardown fires exactly once.
type stopSpyRenderer struct {
	*tui.TuiAltScreen
	stops int
}

func (s *stopSpyRenderer) Stop() { s.stops++ }

// TestStopInteractiveTuiIsIdempotentAndTearsDown proves the fix for the
// cancellation/error-path teardown gap: stopInteractiveTui stops the renderer and
// disposes the transcript view, and repeated calls (the Run() defer plus an
// explicit quit handler both firing) stop the renderer only once. Before the fix,
// ctx.Done and input-error paths returned without any renderer teardown, leaving
// the terminal in the alternate buffer.
func TestStopInteractiveTuiIsIdempotentAndTearsDown(t *testing.T) {
	spy := &stopSpyRenderer{TuiAltScreen: tui.NewTuiAltScreenWithOutput(io.Discard, 80, 24, tui.TuiAltScreenOptions{})}
	sv := tui.NewScrollView(tui.NewText("row"), tui.ScrollViewOptions{})
	m := &InteractiveMode{tuiInst: spy, transcriptScrollView: sv}

	m.stopInteractiveTui()
	if !m.tuiTornDown {
		t.Fatal("first teardown must set tuiTornDown")
	}
	if spy.stops != 1 {
		t.Fatalf("renderer Stop calls after first teardown = %d, want 1", spy.stops)
	}

	// A second call (e.g. the explicit quit handler after the deferred teardown,
	// or vice versa) must be a no-op.
	m.stopInteractiveTui()
	if spy.stops != 1 {
		t.Fatalf("renderer Stop calls after second teardown = %d, want 1 (idempotent)", spy.stops)
	}
}

// TestStopInteractiveTuiNilTranscriptView proves teardown is safe in regular
// (non-fullscreen) mode where no transcript scroll view is constructed.
func TestStopInteractiveTuiNilTranscriptView(t *testing.T) {
	spy := &stopSpyRenderer{TuiAltScreen: tui.NewTuiAltScreenWithOutput(io.Discard, 80, 24, tui.TuiAltScreenOptions{})}
	m := &InteractiveMode{tuiInst: spy} // transcriptScrollView nil
	m.stopInteractiveTui()
	if spy.stops != 1 {
		t.Fatalf("renderer Stop calls = %d, want 1", spy.stops)
	}
}

func TestStopInteractiveTuiFullscreenExitOutput(t *testing.T) {
	for _, test := range []struct {
		name           string
		exitOutput     string
		wantTranscript bool
	}{
		{name: "transcript", exitOutput: "transcript", wantTranscript: true},
		{name: "resume-hint", exitOutput: "resume-hint", wantTranscript: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			renderer := tui.NewTuiAltScreenWithOutput(&output, 80, 24, tui.TuiAltScreenOptions{})
			renderer.Add(tui.NewText("transcript row"))
			renderer.Start()
			output.Reset()
			mode := &InteractiveMode{
				tuiInst:   renderer,
				altScreen: renderer,
				opts: InteractiveOptions{Settings: Settings{
					TuiMode: "fullscreen", FullscreenExitOutput: test.exitOutput,
				}},
			}

			mode.stopInteractiveTui()

			got := output.String()
			if strings.Contains(got, "transcript row") != test.wantTranscript {
				t.Fatalf("stop output contains transcript = %t, want %t; output=%q", strings.Contains(got, "transcript row"), test.wantTranscript, got)
			}
		})
	}
}

// TestOnSettingAppliedFullscreenScrollbarAppliesLive proves the fullscreen
// scrollbar setting is applied to the live transcript view (not just persisted),
// mirroring upstream applyFullscreenScrollbarSetting. Regular mode (nil view) is
// a safe no-op.
func TestOnSettingAppliedFullscreenScrollbarAppliesLive(t *testing.T) {
	dir := t.TempDir()
	sm := NewSettingsManager(dir, dir)
	sv := tui.NewScrollView(tui.NewText("x"), tui.ScrollViewOptions{Scrollbar: "auto"})
	mode := &InteractiveMode{
		opts:                 InteractiveOptions{AgentDir: dir, CWD: dir, SettingsManager: sm},
		transcriptScrollView: sv,
	}

	mode.buildSlashContext(t.Context()).OnSettingApplied("fullscreen-scrollbar", "always")
	if got := sv.Scrollbar(); got != "always" {
		t.Errorf("live scrollbar mode = %q, want %q", got, "always")
	}

	// Regular mode: nil transcript view must not panic.
	regular := &InteractiveMode{opts: InteractiveOptions{AgentDir: dir, CWD: dir, SettingsManager: sm}}
	regular.buildSlashContext(t.Context()).OnSettingApplied("fullscreen-scrollbar", "hidden")
}

func TestOnSettingAppliedFullscreenCopyOnSelectAppliesLive(t *testing.T) {
	dir := t.TempDir()
	manager := NewSettingsManager(dir, dir)
	renderer := tui.NewTuiAltScreenWithOutput(io.Discard, 80, 24, tui.TuiAltScreenOptions{})
	mode := &InteractiveMode{
		opts:      InteractiveOptions{AgentDir: dir, CWD: dir, SettingsManager: manager},
		altScreen: renderer,
		tuiInst:   renderer,
	}

	mode.buildSlashContext(t.Context()).OnSettingApplied("fullscreen-copy-on-select", "false")
	if renderer.GetCopyOnSelect() {
		t.Fatal("live fullscreen copy on select = true, want false")
	}
	mode.buildSlashContext(t.Context()).OnSettingApplied("fullscreen-copy-on-select", "true")
	if !renderer.GetCopyOnSelect() {
		t.Fatal("live fullscreen copy on select = false, want true")
	}
}

// TestStopInteractiveTuiDisposesTranscriptView binds teardown to disposal: it
// arms the transcript view's scrollbar-hide timer, tears down, and proves the
// timer no longer fires (a render callback would run otherwise). Without the
// Dispose() call in stopInteractiveTui the timer outlives the session and fires
// after teardown.
func TestStopInteractiveTuiDisposesTranscriptView(t *testing.T) {
	spy := &stopSpyRenderer{TuiAltScreen: tui.NewTuiAltScreenWithOutput(io.Discard, 80, 24, tui.TuiAltScreenOptions{})}
	var renders atomic.Int64
	delay := 40
	sv := tui.NewScrollView(tui.NewText("row"), tui.ScrollViewOptions{
		Scrollbar: "auto", ScrollbarHideDelayMs: &delay,
	})
	sv.UpdateLayout(100, 10, func() { renders.Add(1) }) // content > viewport
	sv.ScrollBy(5)                                      // activity arms the hide timer
	m := &InteractiveMode{tuiInst: spy, transcriptScrollView: sv}

	before := renders.Load()
	m.stopInteractiveTui()
	time.Sleep(time.Duration(delay*4) * time.Millisecond)
	if got := renders.Load(); got != before {
		t.Errorf("hide timer fired after teardown (renders %d -> %d): transcript view not disposed", before, got)
	}
}

// TestRequestShutdownDoesNotTearDownOffLoop proves extension-triggered shutdown
// (which runs off the owner loop) requests exit without mutating renderer state.
// requestShutdown must set the exit flag and post a wake, but leave the actual
// teardown to the owner loop; tearing down here would race the input loop and the
// deferred teardown over renderer state.
func TestRequestShutdownDoesNotTearDownOffLoop(t *testing.T) {
	spy := &stopSpyRenderer{TuiAltScreen: tui.NewTuiAltScreenWithOutput(io.Discard, 80, 24, tui.TuiAltScreenOptions{})}
	sv := tui.NewScrollView(tui.NewText("row"), tui.ScrollViewOptions{})
	m := &InteractiveMode{tuiInst: spy, transcriptScrollView: sv, uiTaskCh: make(chan func(), 64)}

	m.requestShutdown()

	if !m.requestExit.Load() {
		t.Error("requestShutdown must set the exit flag the loop observes")
	}
	if spy.stops != 0 {
		t.Errorf("requestShutdown tore down the renderer off-loop (Stop calls=%d): teardown must run on the owner loop", spy.stops)
	}
	select {
	case <-m.uiTaskCh:
	default:
		t.Error("requestShutdown did not post a wake task for the loop")
	}
}
