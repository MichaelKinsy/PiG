package tui

import (
	"bytes"
	"testing"
	"time"
)

// cachedRows keeps its row until Invalidate, as Pi's Text, Box and Markdown keep theirs and as an extension's component may.
type cachedRows struct {
	label       string
	state       *string
	rows        []string
	built       int
	invalidated int
}

func (c *cachedRows) Render(int) []string {
	if c.rows == nil {
		c.built++
		c.rows = []string{c.label + "=" + *c.state}
	}
	return c.rows
}

func (c *cachedRows) Invalidate() {
	c.invalidated++
	c.rows = nil
}

type invalidateScreen struct {
	add        func(Component)
	show       func(Component, OverlayOptions) *OverlayHandle
	render     func()
	invalidate func()
}

func invalidateScreens(t *testing.T) map[string]func() invalidateScreen {
	t.Helper()
	alt := func() *TuiAltScreen {
		ui := newAltScreenForTest(&bytes.Buffer{}, 60, 14, TuiAltScreenOptions{})
		ui.Start()
		t.Cleanup(func() { ui.StopWithOptions(StopOptions{}) })
		return ui
	}
	return map[string]func() invalidateScreen{
		"main screen": func() invalidateScreen {
			ui := NewWithOutput(&bytes.Buffer{}, 60, 14)
			// The test is the loop that owns the components and the frames, as the interactive mode is (SetRenderDeadline): a frame is drawn when the test
			// renders, not on a timer goroutine beside the lines that read and invalidate the components.
			ui.SetRenderDeadline(func(time.Time) {})
			return invalidateScreen{ui.Add, ui.ShowOverlay, ui.Render, ui.Invalidate}
		},
		"alternate screen": func() invalidateScreen {
			ui := alt()
			return invalidateScreen{ui.Add, ui.ShowOverlay, ui.Render, ui.Invalidate}
		},
		"alternate screen with a layout root": func() invalidateScreen {
			ui := alt()
			root := &Container{}
			ui.SetLayoutRoot(root)
			return invalidateScreen{root.Add, ui.ShowOverlay, ui.Render, ui.Invalidate}
		},
	}
}

// Pi 1.5.2 packages/tui/src/tui.ts:951-954, TUI.invalidate:
//
//	for (const root of this.getMountedRoots()) root.invalidate();
//	for (const overlay of this.overlayStack) overlay.component.invalidate();
//
// and Container.invalidate (tui.ts:383-386) forwards to every child. Every overlay of the stack is reached, hidden and
// non-capturing ones too, once per call; a frame invalidates nothing. Measured through the parity runner against Pi 1.5.2
// (extensions-runtime 98 to 101): an extension overlay that keeps a themed row until invalidate() shows the new theme after a
// theme change, where PiG kept the old row in the regular and the alternate screen.
func TestInvalidateReachesMountedRootsAndEveryOverlayComponent(t *testing.T) {
	for name, build := range invalidateScreens(t) {
		t.Run(name, func(t *testing.T) {
			s := build()
			state := "OLD"
			leaf := &cachedRows{label: "root", state: &state}
			nested := &Container{}
			nested.Add(leaf)
			s.add(nested)
			plain := &cachedRows{label: "plain", state: &state}
			s.show(plain, OverlayOptions{})
			hidden := &cachedRows{label: "hidden", state: &state}
			s.show(hidden, OverlayOptions{}).SetHidden(true)
			passive := &cachedRows{label: "passive", state: &state}
			s.show(passive, OverlaySpec{NonCapturing: true}.Options())
			inner := &cachedRows{label: "titled", state: &state}
			framed := &Container{}
			framed.Add(inner)
			s.show(framed, OverlayOptions{Title: "titled"})
			all := []*cachedRows{leaf, plain, hidden, passive, inner}

			s.render()
			state = "NEW"
			s.render()
			s.render()
			for _, c := range all {
				if c.invalidated != 0 {
					t.Errorf("%s: ordinary frames invalidated it %d times, want 0", c.label, c.invalidated)
				}
				if c != hidden && (c.built != 1 || c.rows[0] != c.label+"=OLD") {
					t.Errorf("%s: built %d times with rows %q before Invalidate, want its one cached OLD row", c.label, c.built, c.rows)
				}
			}

			s.invalidate()
			for _, c := range all {
				if c.invalidated != 1 {
					t.Errorf("%s: Invalidate reached it %d times, want 1", c.label, c.invalidated)
				}
			}
			s.render()
			for _, c := range all {
				if c != hidden && (c.built != 2 || c.rows[0] != c.label+"=NEW") {
					t.Errorf("%s: built %d times with rows %q after Invalidate and a frame, want the NEW row", c.label, c.built, c.rows)
				}
			}
		})
	}
}

// Invalidate runs on Pi's causes only. Showing, hiding and closing an overlay ask for a frame and must not invalidate the
// components that stay: an overlay whose frames arrive from an extension would be told on every one of them.
func TestOverlayBookkeepingInvalidatesNoComponent(t *testing.T) {
	for name, build := range invalidateScreens(t) {
		t.Run(name, func(t *testing.T) {
			s := build()
			state := "OLD"
			leaf := &cachedRows{label: "root", state: &state}
			s.add(leaf)
			first := &cachedRows{label: "first", state: &state}
			s.show(first, OverlayOptions{})
			s.render()
			second := s.show(&cachedRows{label: "second", state: &state}, OverlayOptions{})
			s.render()
			second.SetHidden(true)
			s.render()
			second.SetHidden(false)
			s.render()
			second.Close()
			s.render()
			for _, c := range []*cachedRows{leaf, first} {
				if c.invalidated != 0 || c.built != 1 {
					t.Errorf("%s: invalidated %d times and built %d times over overlay bookkeeping, want 0 and 1", c.label, c.invalidated, c.built)
				}
			}
		})
	}
}

// Pi 1.5.2 packages/tui/src/tui-alt-screen.ts:357-361: start() with iTerm2 images drops the image capability and calls
// this.invalidate(), which is TUI.invalidate: the overlays' components too.
func TestAltScreenStartInvalidatesOverlayComponentsWhenITerm2ImagesAreDropped(t *testing.T) {
	saved := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(saved) })
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolITerm2, TrueColor: true, Hyperlinks: true})
	ui := newAltScreenForTest(&bytes.Buffer{}, 60, 14, TuiAltScreenOptions{})
	state := "OLD"
	leaf := &cachedRows{label: "root", state: &state}
	ui.Add(leaf)
	overlay := &cachedRows{label: "overlay", state: &state}
	ui.ShowOverlay(overlay, OverlayOptions{})
	ui.Start()
	defer ui.StopWithOptions(StopOptions{})
	for _, c := range []*cachedRows{leaf, overlay} {
		if c.invalidated != 1 {
			t.Errorf("%s: start with iTerm2 images invalidated it %d times, want 1", c.label, c.invalidated)
		}
	}
}

// renderInvalidates asks for an invalidation from its first Render, on another goroutine whose return the render waits for, as the
// render of a subprocess extension's component does when the extension calls its screen's invalidate.
type renderInvalidates struct {
	cachedRows
	invalidate func()
	asked      bool
	returned   bool
}

func (c *renderInvalidates) Render(width int) []string {
	if !c.asked {
		c.asked = true
		done := make(chan struct{})
		go func() {
			defer close(done)
			c.invalidate()
		}()
		select {
		case <-done:
			c.returned = true
		case <-time.After(10 * time.Second):
		}
	}
	return c.cachedRows.Render(width)
}

// A frame holds the renderer's lock while a component renders, and Pi's single thread lets a render call tui.invalidate(). The
// call returns without that lock; the walk runs when the frame ends, and the next frame draws what the components build then.
func TestInvalidateFromARenderDoesNotWaitForTheFrame(t *testing.T) {
	for name, build := range invalidateScreens(t) {
		t.Run(name, func(t *testing.T) {
			s := build()
			state := "OLD"
			asking := &renderInvalidates{label: "asking", state: &state, invalidate: s.invalidate}
			overlay := &cachedRows{label: "overlay", state: &state}
			s.add(asking)
			s.show(overlay, OverlayOptions{})
			s.render()
			if !asking.returned {
				t.Fatal("Invalidate from a render waited for the frame that runs the render")
			}
			if asking.invalidated != 1 || overlay.invalidated != 1 {
				t.Fatalf("after the frame the component and the overlay were invalidated %d and %d times, want 1 and 1", asking.invalidated, overlay.invalidated)
			}
		})
	}
}
