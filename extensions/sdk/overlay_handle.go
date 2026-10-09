package sdk

import (
	"encoding/json"
	"errors"
)

// OverlayBounds is the last rendered terminal-relative rectangle of a mounted overlay.
type OverlayBounds struct {
	Row    int `json:"row"`
	Col    int `json:"col"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type overlayHandleState struct {
	Hidden  bool           `json:"hidden"`
	Focused bool           `json:"focused"`
	Visible bool           `json:"visible"`
	Bounds  *OverlayBounds `json:"bounds,omitempty"`
}

// UnfocusOptions is Pi's unfocus({target}). Target is nil to focus nothing, the component of the editor installed with
// SetEditorComponent, or the component of another overlay this extension mounted.
type UnfocusOptions struct {
	Target any
}

// OverlayHandle is Pi's OverlayHandle (tui.ts) of a mounted overlay: pass an onHandle function in [RemoteOverlayOptions.OnHandle] (or the
// "onHandle" key of a map of options) and [Context.Custom] calls it once, on the component's own input queue, when the host mounted the
// overlay. Each control is a host call that returns after the host applied it; IsHidden, IsFocused and GetBounds read the state it
// returned. After the overlay closed they read the last state and the controls do nothing, except SetHidden, which records its value.
type OverlayHandle struct {
	ext     *Extension
	key     string
	overlay *remoteOverlay
}

// OverlayHandleFunc receives the mounted overlay's handle.
type OverlayHandleFunc func(handle *OverlayHandle)

func (o *remoteOverlay) applyHandleState(state overlayHandleState) {
	o.stateMu.Lock()
	o.handleState = state
	o.stateMu.Unlock()
	if setter, ok := o.component.(interface{ SetFocused(bool) }); ok {
		setter.SetFocused(state.Focused)
	}
}

func (h *OverlayHandle) control(action string, hidden *bool, target map[string]any) error {
	overlay := h.overlay
	if !overlay.active.Load() {
		if action == "setHidden" && hidden != nil {
			overlay.stateMu.Lock()
			overlay.handleState.Hidden = *hidden
			overlay.stateMu.Unlock()
		}
		return nil
	}
	args := map[string]any{"key": h.key, "action": action}
	if hidden != nil {
		args["hidden"] = *hidden
	}
	if target != nil {
		args["target"] = target
	}
	result, err := Context{ext: h.ext}.callHost("ui.custom.control", args)
	if err := callResultError(result, err); err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	var state overlayHandleState
	if err := json.Unmarshal(result.Result, &state); err != nil {
		return err
	}
	overlay.applyHandleState(state)
	return nil
}

// Hide removes the overlay (Pi's hide() disposes it).
func (h *OverlayHandle) Hide() error { return h.control("hide", nil, nil) }

// SetHidden hides or shows the overlay without disposing it.
func (h *OverlayHandle) SetHidden(hidden bool) error { return h.control("setHidden", &hidden, nil) }

// IsHidden reports whether the overlay is hidden.
func (h *OverlayHandle) IsHidden() bool {
	h.overlay.stateMu.Lock()
	defer h.overlay.stateMu.Unlock()
	return h.overlay.handleState.Hidden
}

// Focus gives the overlay keyboard focus.
func (h *OverlayHandle) Focus() error { return h.control("focus", nil, nil) }

// Unfocus takes keyboard focus from the overlay. With no options focus returns to where the host puts it; with options it goes to
// exactly options.Target.
func (h *OverlayHandle) Unfocus(options ...UnfocusOptions) error {
	if len(options) == 0 {
		return h.control("unfocus", nil, nil)
	}
	target, err := h.focusTarget(options[0].Target)
	if err != nil {
		return err
	}
	return h.control("unfocus", nil, target)
}

// IsFocused reports whether the overlay has keyboard focus.
func (h *OverlayHandle) IsFocused() bool {
	h.overlay.stateMu.Lock()
	defer h.overlay.stateMu.Unlock()
	return h.overlay.handleState.Focused
}

// GetBounds is the overlay's last rendered rectangle, nil while it is not visible.
func (h *OverlayHandle) GetBounds() *OverlayBounds {
	h.overlay.stateMu.Lock()
	defer h.overlay.stateMu.Unlock()
	state := h.overlay.handleState
	if !state.Visible || state.Bounds == nil {
		return nil
	}
	bounds := *state.Bounds
	return &bounds
}

func (h *OverlayHandle) focusTarget(target any) (map[string]any, error) {
	if target == nil {
		return map[string]any{"kind": "null"}, nil
	}
	h.ext.overlaysMu.RLock()
	for key, mounted := range h.ext.overlays {
		if mounted.active.Load() && sameOverlayComponent(mounted.component, target) {
			h.ext.overlaysMu.RUnlock()
			return map[string]any{"kind": "overlay", "key": key}, nil
		}
	}
	h.ext.overlaysMu.RUnlock()
	h.ext.editorMu.Lock()
	session := h.ext.editor
	h.ext.editorMu.Unlock()
	if session != nil && session.component != nil && sameOverlayComponent(session.component, target) {
		return map[string]any{"kind": "editor"}, nil
	}
	// pig divergence (D73): a component that is not mounted by this extension has no main-process identity to focus.
	return nil, errors.New("Overlay unfocus(target) requires nil, the editor component, or a mounted overlay of this extension (D73)")
}

func sameOverlayComponent(a, b any) (same bool) {
	defer func() {
		if recover() != nil {
			same = false // an uncomparable dynamic type is never the mounted component
		}
	}()
	return a == b
}

// overlayOnHandle reads the onHandle function out of a custom() call's options (a [RemoteOverlayOptions] or a map) and returns the
// options without it. The function cannot cross the process boundary: the host is told only that a handle was asked for.
func overlayOnHandle(options any) (OverlayHandleFunc, any) {
	switch value := options.(type) {
	case RemoteOverlayOptions:
		return value.OnHandle, value
	case *RemoteOverlayOptions:
		if value != nil {
			return value.OnHandle, *value
		}
	case map[string]any:
		for _, candidate := range []any{value["onHandle"]} {
			var fn OverlayHandleFunc
			switch typed := candidate.(type) {
			case OverlayHandleFunc:
				fn = typed
			case func(*OverlayHandle):
				fn = typed
			}
			if fn != nil || value["onHandle"] != nil {
				rest := make(map[string]any, len(value))
				for key, entry := range value {
					if key != "onHandle" {
						rest[key] = entry
					}
				}
				return fn, rest
			}
		}
	}
	return nil, options
}
