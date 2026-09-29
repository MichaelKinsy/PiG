package codingagent

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Control applies one OverlayHandle operation. target is the explicit unfocus target, or nil for unfocus without options.
func (c *customOverlay) Control(ctx context.Context, action string, hidden bool, target *extension.RemoteOverlayFocusTarget) (extension.RemoteOverlayState, error) {
	c.mu.RLock()
	control := c.control
	c.mu.RUnlock()
	if control == nil {
		return extension.RemoteOverlayState{}, errors.New("overlay is not mounted")
	}
	return control(ctx, action, hidden, target)
}

func (c *customOverlay) inputRoute() (chan []byte, <-chan struct{}) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.inputCh, c.inputChanged
}

func (c *customOverlay) setInputActive(m *InteractiveMode, active bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if active == (c.inputCh != nil) {
		return
	}
	if active {
		c.inputCh, c.releaseInput = m.acquireModalInputChannel()
	} else {
		c.releaseInput()
		c.inputCh, c.releaseInput = nil, nil
	}
	close(c.inputChanged)
	c.inputChanged = make(chan struct{})
}

// bindOverlayControls keeps every TUI mutation on its owner loop. Visibility
// and focus also control the input route, so a noncapturing overlay can release
// the editor without unmounting or replaying its transcript.
func (u *ExtUIContext) bindOverlayControls(c *customOverlay, handle *tui.OverlayHandle, runOnOwner func(context.Context, func())) {
	removed := false
	c.mu.Lock()
	c.control = func(ctx context.Context, action string, hidden bool, target *extension.RemoteOverlayFocusTarget) (extension.RemoteOverlayState, error) {
		type outcome struct {
			state extension.RemoteOverlayState
			err   error
		}
		result := make(chan outcome, 1)
		runOnOwner(ctx, func() {
			if removed {
				result <- outcome{}
				return
			}
			switch action {
			case "":
			case "hide":
				handle.Hide()
				removed = true
			case "setHidden":
				handle.SetHidden(hidden)
			case "focus":
				handle.Focus()
			case "unfocus":
				if target == nil {
					handle.Unfocus()
					break
				}
				// upstream: packages/tui/src/tui.ts:unfocus focuses exactly the explicit target, including null.
				var component tui.Component
				other, _ := target.Overlay.(*customOverlay)
				switch {
				case target.Editor && u.m.editor != nil:
					component = u.m.editor
				case other != nil:
					component = other
				}
				handle.UnfocusWith(tui.OverlayUnfocusOptions{Target: component})
				if other != nil && other != c {
					other.setInputActive(u.m, u.m.tuiInst.FocusedComponent() == tui.Component(other))
				}
			default:
				result <- outcome{err: fmt.Errorf("unknown overlay control: %s", action)}
				return
			}
			state := extension.RemoteOverlayState{Hidden: handle.IsHidden(), Focused: handle.IsFocused(), Visible: !removed && !handle.IsHidden()}
			c.setInputActive(u.m, state.Focused)
			if bounds, ok := handle.GetBounds(); ok {
				state.Bounds = &extension.RemoteOverlayBounds{Row: bounds.Row, Col: bounds.Col, Width: bounds.Width, Height: bounds.Height}
			}
			if action != "" {
				u.m.requestRender()
			}
			result <- outcome{state: state}
		})
		select {
		case value := <-result:
			return value.state, value.err
		case <-ctx.Done():
			return extension.RemoteOverlayState{}, ctx.Err()
		}
	}
	c.mu.Unlock()
	c.setInputActive(u.m, handle.IsFocused())
}
