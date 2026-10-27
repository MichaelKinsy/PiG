package extensionconformance

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// controllableOverlay is the host's mounted overlay as ui.custom.control sees it: a state machine that applies each control and answers
// the state it holds (coding/extension/host/subprocess/overlay_control.go). It starts focused and visible, so a handle that reports a
// constant, or never asks the host, disagrees with it.
type controllableOverlay struct {
	mu      sync.Mutex
	state   extension.RemoteOverlayState
	actions []string
	changed chan struct{}
	closed  chan struct{}
	result  any
}

func (o *controllableOverlay) UpdateLines([]string) {}
func (o *controllableOverlay) Close(result any) {
	o.mu.Lock()
	o.result = result
	select {
	case <-o.closed:
	default:
		close(o.closed)
	}
	o.mu.Unlock()
}

func (o *controllableOverlay) Control(_ context.Context, action string, hidden bool, target *extension.RemoteOverlayFocusTarget) (extension.RemoteOverlayState, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	switch action {
	case "setHidden":
		o.state.Hidden = hidden
		o.state.Visible = !hidden
		o.state.Focused = o.state.Focused && !hidden
	case "hide":
		o.state.Hidden, o.state.Visible, o.state.Focused = true, false, false
	case "focus":
		o.state.Focused = !o.state.Hidden
	case "unfocus":
		o.state.Focused = false
		if target != nil {
			action += map[bool]string{true: ":editor", false: ":none"}[target.Editor]
		}
	}
	if action != "" {
		o.actions = append(o.actions, action)
	}
	select {
	case o.changed <- struct{}{}:
	default:
	}
	return o.state, nil
}

func (o *controllableOverlay) seen() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.actions)
}

// Pi's ui.custom(factory, {overlay: true, onHandle}) hands the mounted OverlayHandle to onHandle (interactive-mode.ts:2918-2920): hide,
// setHidden, isHidden, focus, unfocus, isFocused and getBounds. Every SDK sets hasHandle on the open call, receives ui.custom.opened and
// answers the handle from the state the host returned for each control.
func TestCustomOverlayHandleSDKsMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping overlay handle conformance in short mode (builds subprocess fixtures)")
	}
	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t) // building a subprocess fixture can take minutes: the call's own deadline starts after it
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			overlay := &controllableOverlay{
				state:   extension.RemoteOverlayState{Focused: true, Visible: true, Bounds: &extension.RemoteOverlayBounds{Row: 3, Col: 4, Width: 20, Height: 5}},
				changed: make(chan struct{}, 1),
				closed:  make(chan struct{}),
			}
			var optsMu sync.Mutex
			var overlayOpen bool
			h.ui.runOverlay = func(opts extension.RemoteOverlayOptions, host extension.RemoteOverlayHost, onHandle func(extension.RemoteOverlayHandle)) (any, bool) {
				optsMu.Lock()
				overlayOpen = opts.Overlay
				optsMu.Unlock()
				onHandle(overlay) // the host mounted the overlay: it sends ui.custom.opened with the state
				deadline := time.After(10 * time.Second)
				for len(overlay.seen()) < 6 {
					select {
					case <-overlay.changed:
					case <-deadline:
						return nil, false
					}
				}
				host.OnInput("q")
				select {
				case <-overlay.closed:
					return overlay.result, true
				case <-time.After(5 * time.Second):
					return nil, false
				}
			}
			command, ok := findCommand(h.runner, "overlay-handle-probe")
			if !ok {
				t.Fatal("overlay-handle-probe command not registered")
			}
			if err := command.Handler(ctx, ""); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return slices.Contains(h.ui.Recorded(), "overlay-handle=ok:info") })
			optsMu.Lock()
			if !overlayOpen {
				t.Error("the extension opened a plain focused component, want an overlay")
			}
			optsMu.Unlock()
			want := []string{"focus", "setHidden", "setHidden", "focus", "unfocus", "unfocus:none"}
			if got := overlay.seen(); !slices.Equal(got, want) {
				t.Fatalf("controls the host applied = %v, want %v", got, want)
			}
		})
	}
}

// focusRecordingOverlay is a mounted overlay that keeps every frame the extension rendered into it.
type focusRecordingOverlay struct {
	*controllableOverlay
	framesMu sync.Mutex
	frames   []string
}

func (o *focusRecordingOverlay) UpdateLines(lines []string) {
	o.framesMu.Lock()
	o.frames = append(o.frames, strings.Join(lines, "\n"))
	o.framesMu.Unlock()
}

// sawFrames reports whether the frames rendered so far contain want in order.
func (o *focusRecordingOverlay) sawFrames(want ...string) bool {
	o.framesMu.Lock()
	defer o.framesMu.Unlock()
	next := 0
	for _, frame := range o.frames {
		if next < len(want) && frame == want[next] {
			next++
		}
	}
	return next == len(want)
}

// Pi's TUI.setFocus clears the focused flag of the component that loses focus and sets it on the one that gains it (tui.ts:602-665):
// a ui.custom component that another custom component, dialog or overlay opens over loses focus, and gets it back when that one
// closes. The host publishes each change to the extension (ui.custom.focus); every SDK updates the handle's isFocused and renders
// the component again, so a component that draws its focus shows it lost and regained.
func TestCustomOverlayFocusChangesReachSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping overlay focus conformance in short mode (builds subprocess fixtures)")
	}
	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			overlay := &focusRecordingOverlay{controllableOverlay: &controllableOverlay{
				state:   extension.RemoteOverlayState{Focused: true, Visible: true},
				changed: make(chan struct{}, 1),
				closed:  make(chan struct{}),
			}}
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
				focusHost, ok := host.(extension.RemoteOverlayFocusHost)
				if !ok {
					return fail("the bridge does not publish focus changes to the extension")
				}
				await := func(frames ...string) bool {
					deadline := time.Now().Add(10 * time.Second)
					for !overlay.sawFrames(frames...) {
						if time.Now().After(deadline) {
							return false
						}
						time.Sleep(5 * time.Millisecond)
					}
					return true
				}
				// Another component opened over this one, then closed.
				focusHost.OnFocus(false)
				if !await("focused=false") {
					return fail("the component did not render focused=false after it lost focus")
				}
				focusHost.OnFocus(true)
				if !await("focused=false", "focused=true") {
					return fail("the component did not render focused=true after it regained focus")
				}
				host.OnInput("q")
				select {
				case <-overlay.closed:
					return overlay.result, true
				case <-time.After(5 * time.Second):
					return fail("the component did not close")
				}
			}
			command, ok := findCommand(h.runner, "overlay-focus-probe")
			if !ok {
				t.Fatal("overlay-focus-probe command not registered")
			}
			if err := command.Handler(ctx, ""); err != nil {
				t.Fatal(err)
			}
			select {
			case message := <-failure:
				overlay.framesMu.Lock()
				frames := slices.Clone(overlay.frames)
				overlay.framesMu.Unlock()
				t.Fatalf("%s; frames %q", message, frames)
			default:
			}
			waitFor(t, func() bool { return slices.Contains(h.ui.Recorded(), "overlay-focus=ok:info") })
		})
	}
}
