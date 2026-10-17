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

// SetFocused is the TUI's focus flag (tui.Focusable). The TUI calls it on its owner loop whenever focus moves to or from the
// component, and the component reads the terminal exactly while it holds focus: Pi's TUI hands each key to its focused component
// only (tui.ts handleInput), so of overlapping custom components only the focused one, the top of the focus stack, receives keys.
func (c *customOverlay) SetFocused(focused bool) {
	c.mu.RLock()
	onFocus := c.onFocus
	c.mu.RUnlock()
	if onFocus != nil {
		onFocus(focused)
	}
}

func (c *customOverlay) setInputActive(m *InteractiveMode, active bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if active && c.inputRetired {
		return
	}
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

// retireInput releases the component's input lease for good once its RunRemoteOverlay call returns, whatever focus does later.
func (c *customOverlay) retireInput(m *InteractiveMode) {
	c.mu.Lock()
	c.inputRetired = true
	c.mu.Unlock()
	c.setInputActive(m, false)
}

// bindOverlayControls keeps every TUI mutation on its owner loop. Visibility and focus changes move the input route through
// SetFocused, so a noncapturing overlay can release the editor without unmounting or replaying its transcript.
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
				result <- outcome{state: extension.RemoteOverlayState{FocusOwner: u.m.tuiInst.GetFocusedComponent()}}
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
				handle.UnfocusWith(tui.OverlayUnfocusOptions{Target: u.focusTargetComponent(*target)})
			default:
				result <- outcome{err: fmt.Errorf("unknown overlay control: %s", action)}
				return
			}
			state := extension.RemoteOverlayState{Hidden: handle.IsHidden(), Focused: handle.IsFocused(), Visible: !removed && !handle.IsHidden(), FocusOwner: u.m.tuiInst.GetFocusedComponent()}
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
			// A raw overlay (ShowRemoteOverlay) has no ui.custom call to end it: its handle's hide is its end.
			if action == "hide" && value.err == nil && c.endOnHide {
				c.Close(nil)
			}
			return value.state, value.err
		case <-ctx.Done():
			return extension.RemoteOverlayState{}, ctx.Err()
		}
	}
	c.mu.Unlock()
}

// focusTargetComponent is the component an explicit focus target names: nothing, the editor, or a mounted remote overlay.
func (u *ExtUIContext) focusTargetComponent(target extension.RemoteOverlayFocusTarget) tui.Component {
	other, _ := target.Overlay.(*customOverlay)
	switch {
	case target.Editor && u.m.editor != nil:
		return u.m.editor
	case other != nil:
		return other
	}
	return nil
}
