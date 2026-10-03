package codingagent

import (
	"encoding/json"
	"io"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Upstream 0.99.1: createInteractiveTui passes `wheelScrollLines: options.fullscreenWheelScrollLines ?? "auto"` to the fullscreen renderer (tui-renderer.ts:38); InteractiveMode reads getFullscreenWheelScrollLines() when it creates (interactive-mode.ts:593) or switches (interactive-mode.ts:881) the renderer and again when settings apply (interactive-mode.ts:2001). interactive-tui.test.ts:128-152 supplies that getter to the renderer switch.

// The region replaces the layout root because a mounted interactive layout fills the viewport and would take the wheel event first (tui-alt-screen.ts handleViewportInput dispatches to the rendered layout).
func wheelDeltas(t *testing.T, renderer *tui.TuiAltScreen, events ...string) []int {
	t.Helper()
	var deltas []int
	renderer.SetLayoutRoot(tui.NewMouseRegion(tui.NewText("wheel target"), func(event tui.TuiMouseEvent) *tui.TuiMouseEventResult {
		if event.Type != tui.MouseWheel {
			return nil
		}
		deltas = append(deltas, event.WheelDelta)
		return &tui.TuiMouseEventResult{Handled: true}
	}))
	renderer.SetRenderDispatcher(func(func()) {})
	renderer.Start()
	t.Cleanup(func() { renderer.StopWithOptions(tui.StopOptions{PreserveScreen: true}) })
	renderer.Render()
	for _, event := range events {
		renderer.HandleViewportInput(event)
	}
	return deltas
}

func TestInteractiveTuiWheelScrollLinesFromSettingsUpstream(t *testing.T) {
	const wheelDown = "\x1b[<65;1;1M"
	for _, tc := range []struct {
		name string
		raw  string
		want []int
	}{
		{"a configured count", "7", []int{7}},
		{"auto", `"auto"`, []int{1}},
		// tui-renderer.ts:38: an unset option is auto.
		{"unset", "", []int{1}},
	} {
		t.Run("creation with "+tc.name, func(t *testing.T) {
			mode := NewInteractiveMode(InteractiveOptions{Settings: Settings{TuiMode: "fullscreen", FullscreenWheelScrollLines: json.RawMessage(tc.raw)}, AgentDir: t.TempDir()})
			mode.rendererOut = io.Discard
			handle := mode.createInteractiveTui(t.Context())
			defer handle.cleanup()
			if got := wheelDeltas(t, mode.altScreen, wheelDown); !slices.Equal(got, tc.want) {
				t.Fatalf("wheel deltas = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("a renderer switch", func(t *testing.T) {
		m := newSwitchTuiProbeWithOptions(t, InteractiveOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), Settings: Settings{TuiMode: "regular", FullscreenWheelScrollLines: json.RawMessage("7")}})
		if !m.switchTuiMode("fullscreen", false, true) {
			t.Fatal("switch to fullscreen returned false")
		}
		if got := wheelDeltas(t, m.altScreen, wheelDown); !slices.Equal(got, []int{7}) {
			t.Fatalf("wheel deltas after the switch = %v, want [7]", got)
		}
	})

	// interactive-mode.ts:5006-5008 (onFullscreenWheelScrollLinesChange) and :2001 (applyRuntimeSettings).
	t.Run("a settings change", func(t *testing.T) {
		for _, tc := range []struct {
			value string
			want  int
		}{{"3", 3}, {"auto", 1}} {
			m := newSwitchTuiProbeWithOptions(t, InteractiveOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), Settings: Settings{TuiMode: "fullscreen", FullscreenWheelScrollLines: json.RawMessage("7")}})
			m.buildSlashContext(t.Context()).OnSettingApplied("fullscreen-wheel-scroll-lines", tc.value)
			if got := wheelDeltas(t, m.altScreen, wheelDown); !slices.Equal(got, []int{tc.want}) {
				t.Fatalf("wheel deltas after choosing %s = %v, want [%d]", tc.value, got, tc.want)
			}
		}
	})
}
