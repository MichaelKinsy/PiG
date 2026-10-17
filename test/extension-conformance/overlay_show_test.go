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

// rawProbeOverlay is a raw overlay as the host's ShowRemoteOverlay mounts it: it records the controls the extension applies and the
// frames it draws, holds focus after focus(), and ends (ended closes) with its hide.
type rawProbeOverlay struct {
	mu      sync.Mutex
	host    extension.RemoteOverlayHost
	focused bool
	frames  []string
	record  func(string)
	once    sync.Once
	ended   chan struct{}
}

func (o *rawProbeOverlay) UpdateLines(lines []string) {
	o.mu.Lock()
	o.frames = append(o.frames, strings.Join(lines, "\n"))
	o.mu.Unlock()
}

func (o *rawProbeOverlay) Close(any) { o.once.Do(func() { close(o.ended) }) }

func (o *rawProbeOverlay) Control(_ context.Context, action string, _ bool, _ *extension.RemoteOverlayFocusTarget) (extension.RemoteOverlayState, error) {
	o.mu.Lock()
	switch action {
	case "focus":
		o.focused = true
	case "hide":
		o.focused = false
	}
	state := extension.RemoteOverlayState{Focused: o.focused, Visible: action != "hide"}
	if o.focused {
		state.FocusOwner = o
	}
	o.mu.Unlock()
	if action != "" {
		o.record("control:" + action)
	}
	if action == "hide" {
		o.Close(nil)
	}
	return state, nil
}

// sawFrame reports whether the panel drew a frame ending in suffix; the focus it draws is the one this fake host reported.
func (o *rawProbeOverlay) sawFrame(suffix string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.ContainsFunc(o.frames, func(frame string) bool { return strings.HasSuffix(frame, suffix) })
}

// Pi hands showExtensionCustom's factory the TUI itself (interactive-mode.ts:3008), so a factory shows a raw overlay with
// tui.showOverlay and focuses it through the handle before the factory returns (tui.ts:729-826, overlay-qa-tests.ts
// FocusDemoController). The Node runtime shows it on the host first, applies the focus, and only then opens the ui.custom
// component; the panel's keys arrive under its own key, and its hide ends it before done() closes the component.
func TestFactoryTuiShowOverlayReachesTheHost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping factory tui conformance in short mode (builds subprocess fixtures)")
	}
	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.HasPrefix(tc.name, "subprocess-node") {
				t.Skip("exempt: the Go, Rust and Python Custom APIs take a component, not a factory, and hand out no TUI, so no component of theirs can call showOverlay")
			}
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
			var eventsMu sync.Mutex
			var events []string
			record := func(event string) {
				eventsMu.Lock()
				events = append(events, event)
				eventsMu.Unlock()
			}
			panel := &rawProbeOverlay{record: record, ended: make(chan struct{})}
			h.ui.showOverlay = func(opts extension.RemoteOverlayOptions, host extension.RemoteOverlayHost) (extension.RemoteOverlayHandle, <-chan struct{}, bool) {
				if opts.Layout == nil || !opts.Layout.NonCapturing {
					t.Errorf("showOverlay options = %+v, want the panel's nonCapturing layout", opts.Layout)
				}
				record("show")
				panel.host = host
				return panel, panel.ended, true
			}
			owner := &controllableOverlay{state: extension.RemoteOverlayState{Focused: true, Visible: true}, changed: make(chan struct{}, 1), closed: make(chan struct{})}
			h.ui.runOverlay = func(_ extension.RemoteOverlayOptions, _ extension.RemoteOverlayHost, onHandle func(extension.RemoteOverlayHandle)) (any, bool) {
				record("custom")
				onHandle(owner)
				panel.host.OnInput("a")
				deadline := time.Now().Add(10 * time.Second)
				for !panel.sawFrame(" keys=a") {
					if time.Now().After(deadline) {
						t.Error("the panel did not draw the key it received")
						return nil, false
					}
					time.Sleep(5 * time.Millisecond)
				}
				panel.host.OnInput("x")
				select {
				case <-owner.closed:
					return owner.result, true
				case <-time.After(10 * time.Second):
					t.Error("the component did not close")
					return nil, false
				}
			}
			command, ok := findCommand(h.runner, "overlay-show-probe")
			if !ok {
				t.Fatal("overlay-show-probe command not registered")
			}
			if err := command.Handler(ctx, ""); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return slices.Contains(h.ui.Recorded(), "overlay-show=closed:false:info") })
			select {
			case <-panel.ended:
			case <-time.After(5 * time.Second):
				t.Fatal("the panel did not end with its hide")
			}
			eventsMu.Lock()
			defer eventsMu.Unlock()
			if want := []string{"show", "control:focus", "custom", "control:hide"}; !slices.Equal(events, want) {
				t.Fatalf("host saw %q, want %q", events, want)
			}
		})
	}
}
