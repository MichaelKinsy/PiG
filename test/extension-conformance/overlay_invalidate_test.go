package extensionconformance

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// overlayInvalidateThemes are the theme a fixture starts under and the one the host switches to. The second has a name no fixture
// and no SDK holds by default and draws its accent in the first one's error color, so a row built from an SDK's fallback palette,
// or from the palette before the switch, differs from the row the host's own theme builds.
func overlayInvalidateThemes(t *testing.T) (base, probe *tui.Theme) {
	t.Helper()
	base = tui.NewThemeRegistry().Get("dark")
	if base == nil {
		t.Fatal("no dark theme")
	}
	foregrounds, backgrounds := base.ANSIPalette()
	foregrounds = maps.Clone(foregrounds)
	foregrounds["accent"] = foregrounds["error"]
	probe, err := tui.NewThemeFromANSIPalette("zz-overlay-invalidate-probe", foregrounds, backgrounds, tui.TerminalColorModeTrueColor)
	if err != nil {
		t.Fatal(err)
	}
	if themeProbeRow(base) == themeProbeRow(probe) || base.Fg("accent", "x") == probe.Fg("accent", "x") {
		t.Fatal("the two themes are indistinguishable")
	}
	return base, probe
}

// themeProbeRow is the in-process reference of the row every fixture builds: the theme's name and the host's own accent foreground.
func themeProbeRow(theme *tui.Theme) string { return theme.Name + "|" + theme.Fg("accent", "x") }

// overlayInvalidateCases are every subprocess SDK isolated (with the Node runtime packed and the Go SDK fused into the host, which
// sdkHarnessCases carries) and the Go, Rust, Python and Lisp SDKs packed.
func overlayInvalidateCases() []harnessCase {
	return append(sdkHarnessCases(), packedSDKHarnessCases()...)
}

// publishTheme makes active the theme the harness's host holds: the palette its state carries and the one its UI answers.
func publishTheme(h *harness, active *atomic.Pointer[tui.Theme]) {
	palette := func() extension.Theme { return codingagent.ExtensionThemePalette(active.Load()) }
	h.ui.theme = palette
	h.bridge.SetThemeFunc(func() any { return palette() })
}

// distinctFrames is frames without a frame equal to the one before it.
func distinctFrames(frames []string) []string {
	return slices.Compact(slices.Clone(frames))
}

// Pi's TUI.invalidate calls invalidate() on every mounted root and every overlay's component (tui.ts:951-954): after a theme change
// a component that keeps themed rows builds them again. The host tells a remote component with ui.custom.invalidate {key, count},
// preceded on the connection by theme_change when the palette changed; every SDK applies the theme, calls the component's
// invalidate count times on the component's worker, and renders once. The fixture's component keeps a row built at its invalidate
// and names the theme it sees at its render, so each frame says what the component SAW: the order theme, invalidate, render, and
// the count. A component without an invalidate is rendered again all the same.
func TestCustomOverlayInvalidateReachesSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping overlay invalidate conformance in short mode (builds subprocess fixtures)")
	}
	base, probe := overlayInvalidateThemes(t)
	variants := []struct {
		name, args string
		want       []string
	}{
		{"counting", "", []string{
			"inv=0 kept=" + themeProbeRow(base) + " live=" + base.Name,
			"inv=1 kept=" + themeProbeRow(probe) + " live=" + probe.Name,
			"inv=3 kept=" + themeProbeRow(probe) + " live=" + probe.Name,
		}},
		{"without-invalidate", "plain", []string{
			"plain live=" + themeProbeRow(base),
			"plain live=" + themeProbeRow(probe),
		}},
	}
	for _, tc := range overlayInvalidateCases() {
		for _, variant := range variants {
			t.Run(tc.name+"/"+variant.name, func(t *testing.T) {
				h := tc.make(t)
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				t.Cleanup(func() {
					if h.cleanup != nil {
						h.cleanup()
					}
					if h.host != nil {
						h.host.Shutdown("test done")
					}
				})
				var active atomic.Pointer[tui.Theme]
				active.Store(base)
				publishTheme(h, &active)
				overlay := &focusRecordingOverlay{controllableOverlay: &controllableOverlay{
					state:   extension.RemoteOverlayState{Focused: true, Visible: true},
					changed: make(chan struct{}, 1),
					closed:  make(chan struct{}),
				}}
				frames := func() []string {
					overlay.framesMu.Lock()
					defer overlay.framesMu.Unlock()
					return distinctFrames(overlay.frames)
				}
				failure := make(chan string, 1)
				h.ui.runOverlay = func(_ extension.RemoteOverlayOptions, host extension.RemoteOverlayHost, onHandle func(extension.RemoteOverlayHandle)) (any, bool) {
					onHandle(overlay)
					fail := func(message string) (any, bool) {
						failure <- message
						host.OnInput("q")
						select {
						case <-overlay.closed:
						case <-time.After(5 * time.Second):
						}
						return nil, false
					}
					invalidateHost, ok := host.(extension.RemoteOverlayInvalidateHost)
					if !ok {
						return fail("the bridge does not publish invalidations to the extension")
					}
					// Each wanted frame, in order: a runtime may draw a frame between two of them (the Node runtime draws its components on
					// theme_change itself), never one that skips or reorders them.
					await := func(n int) bool {
						deadline := time.Now().Add(15 * time.Second)
						for !overlay.sawFrames(variant.want[:n]...) {
							if time.Now().After(deadline) {
								return false
							}
							time.Sleep(5 * time.Millisecond)
						}
						return true
					}
					if !await(1) {
						return fail("the first frame is not the one built under the theme at mount")
					}
					// The host's theme changed and its TUI was invalidated once.
					active.Store(probe)
					invalidateHost.OnInvalidate(1)
					if !await(2) {
						return fail("the component did not draw the new theme after the invalidation")
					}
					if len(variant.want) > 2 {
						// Two invalidations the host counted while the connection had no room arrive as one notify: two calls, one frame.
						invalidateHost.OnInvalidate(2)
						if !await(3) {
							return fail("the component did not count two invalidations in one frame")
						}
					}
					host.OnInput("q")
					select {
					case <-overlay.closed:
						return overlay.result, true
					case <-time.After(5 * time.Second):
						return fail("the component did not close")
					}
				}
				command, ok := findCommand(h.runner, "overlay-invalidate-probe")
				if !ok {
					t.Fatal("overlay-invalidate-probe command not registered")
				}
				if err := command.Handler(ctx, variant.args); err != nil {
					t.Fatal(err)
				}
				select {
				case message := <-failure:
					t.Fatalf("%s; frames %q, want %q", message, frames(), variant.want)
				default:
				}
				got := frames()
				if len(got) == 0 || got[len(got)-1] != variant.want[len(variant.want)-1] {
					t.Fatalf("frames %q, want the last to be %q", got, variant.want[len(variant.want)-1])
				}
				for _, frame := range got {
					// What the component saw: no invalidate before the theme it was sent with, and the two coalesced invalidations in one frame.
					if strings.HasPrefix(frame, "inv=1 kept="+themeProbeRow(base)) || strings.HasPrefix(frame, "inv=2 ") {
						t.Fatalf("frames %q: an invalidate ran before its theme, or two coalesced invalidations drew two frames", got)
					}
				}
				waitFor(t, func() bool { return slices.Contains(h.ui.Recorded(), "overlay-invalidate=ok:info") })
			})
		}
	}
}

// theme_change's reach: the host sends theme_change only to the connection of a component it invalidates, ahead of the
// invalidation. An extension with nothing mounted is not told when the palette changes; it learns the palette from the state the
// host sends ahead of each request. So a handler that reads the theme after the host changed it, with no component mounted and no
// theme_change sent, must hold the new theme in every SDK: the narrower send loses nothing an extension can observe.
func TestThemeReadWithoutAMountedComponentFollowsTheHostAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping theme reach conformance in short mode (builds subprocess fixtures)")
	}
	base, probe := overlayInvalidateThemes(t)
	for _, tc := range overlayInvalidateCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			var active atomic.Pointer[tui.Theme]
			active.Store(base)
			publishTheme(h, &active)
			command, ok := findCommand(h.runner, "theme-read-probe")
			if !ok {
				t.Fatal("theme-read-probe command not registered")
			}
			read := func(theme *tui.Theme) {
				t.Helper()
				if err := command.Handler(ctx, ""); err != nil {
					t.Fatal(err)
				}
				want := "theme-read=" + themeProbeRow(theme) + ":info"
				deadline := time.Now().Add(10 * time.Second)
				for !slices.Contains(h.ui.Recorded(), want) {
					if time.Now().After(deadline) {
						t.Fatalf("recorded %q, want %q", h.ui.Recorded(), want)
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
			read(base)
			active.Store(probe)
			read(probe)
		})
	}
}
