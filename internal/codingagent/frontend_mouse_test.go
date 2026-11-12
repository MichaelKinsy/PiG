package codingagent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

// A frontend session's click on an extension's ui.custom overlay (D91)
// reaches the extension through the production hooks and overlay only in the
// mode where Pi's terminal reports the mouse: fullscreen hands the component
// the terminal's press, release and click at the clicked cell
// (tui-alt-screen.ts handleMouseEvent), regular mode hands it nothing, as a
// terminal outside fullscreen sends no mouse.
func TestFrontendClickReachesAnExtensionOverlayOnlyInFullscreen(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want []string
	}{
		{"fullscreen", []string{"press 2,1 c0", "release 2,1 c0", "click 2,1 c1"}},
		{"regular", nil},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			session := &fakeFrontendSession{}
			m, _ := newFrontendProbe(t, &fakeFrontend{session: session}, tc.mode)
			overlay := newCustomOverlay(nil)
			overlay.UpdateLines([]string{"probe 0", "probe 1", "probe 2"})
			var got []string
			overlay.SetOnMouse(func(event extension.RemoteMouseEvent) extension.ViewMouseResult {
				got = append(got, fmt.Sprintf("%s %d,%d c%d", event.Type, event.X, event.Y, event.ClickCount))
				return extension.ViewMouseResult{Handled: true}
			})
			handle := m.tuiInst.ShowOverlay(overlay, tui.OverlayOptions{})
			t.Cleanup(func() {
				if handle != nil {
					handle.Close()
				}
			})
			m.tuiInst.Render()
			var node string
			for _, frame := range session.frames {
				for _, op := range frame.Ops {
					if strings.HasPrefix(op.ID, "overlay.") {
						node = op.ID
					}
				}
			}
			if node == "" {
				t.Fatalf("no overlay node in %d frames", len(session.frames))
			}
			m.surface.Click(frontend.Click{Node: node, Row: 1, Column: 2})
			if !slices.Equal(got, tc.want) {
				t.Fatalf("%s: the overlay got %q, want %q", tc.mode, got, tc.want)
			}
		})
	}
}

// A frontend session's Env.Wheel over an extension's ui.custom overlay (D91)
// reaches the extension through the production hooks and the owner loop, and
// only in the mode where Pi's terminal reports the mouse: fullscreen hands the
// component the one wheel event a terminal's report gives it, with the cell
// and wheelDelta (tui-alt-screen.ts:719-733), regular mode hands it nothing.
// The test calls Env.Wheel as Open received it.
func TestFrontendWheelReachesAnExtensionOverlayOnlyInFullscreen(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want []string
	}{
		{"fullscreen", []string{"wheel 2,1 d-3"}},
		{"regular", nil},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			session := &fakeFrontendSession{}
			fe := &fakeFrontend{session: session}
			m, _ := newFrontendProbe(t, fe, tc.mode)
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			m.backgroundCtx = ctx
			overlay := newCustomOverlay(nil)
			overlay.UpdateLines([]string{"probe 0", "probe 1", "probe 2"})
			var got []string
			overlay.SetOnMouse(func(event extension.RemoteMouseEvent) extension.ViewMouseResult {
				got = append(got, fmt.Sprintf("%s %d,%d d%d", event.Type, event.X, event.Y, event.WheelDelta))
				return extension.ViewMouseResult{Handled: true}
			})
			handle := m.tuiInst.ShowOverlay(overlay, tui.OverlayOptions{})
			t.Cleanup(func() {
				if handle != nil {
					handle.Close()
				}
			})
			m.tuiInst.Render()
			var node string
			for _, frame := range session.frames {
				for _, op := range frame.Ops {
					if strings.HasPrefix(op.ID, "overlay.") {
						node = op.ID
					}
				}
			}
			if node == "" {
				t.Fatalf("no overlay node in %d frames", len(session.frames))
			}
			fe.env.Wheel(frontend.Wheel{Node: node, Row: 1, Column: 2, Delta: -3})
			runPostedTasks(t, m)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("%s: the overlay got %q, want %q", tc.mode, got, tc.want)
			}
		})
	}
}
