package extension

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// RemoteOverlayOptions controls how a remote (subprocess-backed) overlay
// is positioned and sized when the host opens it.
//
// Upstream's `ui.custom()` takes an `overlay` boolean plus an
// `OverlayOptions` value. The OverlayOptions reference TUI primitives that
// cannot cross a process boundary, so the subprocess bridge ports the
// overlay flag and the size/position fields into this serialisable struct.
type RemoteOverlayOptions struct {
	// Title is the overlay's titlebar text. Empty hides the bar.
	Title string `json:"title,omitempty"`
	// WidthFraction is the fraction of terminal width the overlay
	// should occupy. Zero falls back to the overlay default.
	WidthFraction float64 `json:"widthFraction,omitempty"`
	// HeightFraction is the fraction of terminal height the
	// overlay should occupy. Zero falls back to the overlay default.
	HeightFraction float64 `json:"heightFraction,omitempty"`
	// Overlay opens a floating viewport overlay over the whole screen
	// instead of replacing the inline editor slot. Mirrors upstream
	// ui.custom()'s `overlay: true`. When false the component replaces
	// the editor slot.
	Overlay bool `json:"overlay,omitempty"`
	// Layout carries upstream ui.custom()'s overlayOptions (the pi-tui
	// OverlayOptions fields that can cross a process boundary). When set, or
	// when no legacy Title/fractions are given, an overlay is mounted
	// component-framed with exactly upstream's showOverlay geometry.
	Layout *OverlayLayout `json:"overlayOptions,omitempty"`
}

// OverlayLayout is the serialisable subset of upstream pi-tui
// OverlayOptions: every field except the visible callback.
type OverlayLayout struct {
	Width        *OverlaySizeValue   `json:"width,omitempty"`
	MinWidth     *int                `json:"minWidth,omitempty"`
	MaxHeight    *OverlaySizeValue   `json:"maxHeight,omitempty"`
	Anchor       string              `json:"anchor,omitempty"`
	OffsetX      int                 `json:"offsetX,omitempty"`
	OffsetY      int                 `json:"offsetY,omitempty"`
	Row          *OverlaySizeValue   `json:"row,omitempty"`
	Col          *OverlaySizeValue   `json:"col,omitempty"`
	Margin       *OverlayMarginValue `json:"margin,omitempty"`
	NonCapturing bool                `json:"nonCapturing,omitempty"`
}

// OverlaySizeValue is upstream SizeValue: a cell count or an "N%" string.
// A string that is not a percentage is kept with Invalid set, matching
// upstream parseSizeValue returning undefined.
type OverlaySizeValue struct {
	Value   float64
	Percent bool
	Invalid bool
}

var overlayPercentRE = lazyregexp.New(`^(\d+(?:\.\d+)?)%$`)

func (v *OverlaySizeValue) UnmarshalJSON(data []byte) error {
	var n float64
	if err := json.Unmarshal(data, &n); err == nil {
		*v = OverlaySizeValue{Value: n}
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		*v = OverlaySizeValue{Invalid: true}
		return nil
	}
	m := overlayPercentRE.FindStringSubmatch(s)
	if m == nil {
		*v = OverlaySizeValue{Invalid: true}
		return nil
	}
	f, _ := strconv.ParseFloat(m[1], 64)
	*v = OverlaySizeValue{Value: f, Percent: true}
	return nil
}

func (v OverlaySizeValue) MarshalJSON() ([]byte, error) {
	if v.Percent {
		return json.Marshal(strconv.FormatFloat(v.Value, 'f', -1, 64) + "%")
	}
	return json.Marshal(v.Value)
}

// OverlayMarginValue is upstream `OverlayMargin | number`.
type OverlayMarginValue struct {
	All                      *int
	Top, Right, Bottom, Left int
}

func (m *OverlayMarginValue) UnmarshalJSON(data []byte) error {
	var n float64
	if err := json.Unmarshal(data, &n); err == nil {
		all := int(n)
		*m = OverlayMarginValue{All: &all}
		return nil
	}
	var edges struct {
		Top, Right, Bottom, Left float64
	}
	if err := json.Unmarshal(data, &edges); err != nil {
		*m = OverlayMarginValue{}
		return nil
	}
	*m = OverlayMarginValue{Top: int(edges.Top), Right: int(edges.Right), Bottom: int(edges.Bottom), Left: int(edges.Left)}
	return nil
}

func (m OverlayMarginValue) MarshalJSON() ([]byte, error) {
	if m.All != nil {
		return json.Marshal(*m.All)
	}
	return json.Marshal(map[string]int{"top": m.Top, "right": m.Right, "bottom": m.Bottom, "left": m.Left})
}

// RemoteOverlayFocusTarget is an explicit OverlayHandle.unfocus target resolved by the host. The zero value is Pi's null target. Overlay is another mounted overlay of the same extension, as the host registered it; Editor selects the main editor component.
type RemoteOverlayFocusTarget struct {
	Overlay any
	Editor  bool
}

// RemoteOverlayState is the host's mounted-overlay state at a control/input boundary.
type RemoteOverlayState struct {
	Hidden  bool                 `json:"hidden"`
	Focused bool                 `json:"focused"`
	Visible bool                 `json:"visible"`
	Bounds  *RemoteOverlayBounds `json:"bounds,omitempty"`
	// FocusOwner is the component that holds TUI focus after the control, so the bridge can tell the extension which of its
	// overlays has focus: Pi's setFocus clears the focused flag of every other component (tui.ts:602-665).
	FocusOwner any `json:"-"`
}

// RemoteOverlayBounds is the last rendered terminal-relative rectangle.
type RemoteOverlayBounds struct {
	Row    int `json:"row"`
	Col    int `json:"col"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// RemoteOverlayHandle is returned to the caller of
// [UIContext.RunRemoteOverlay] so it can push rendered lines into the
// overlay and close it when the remote producer signals completion.
//
// pig-specific: no upstream equivalent.
type RemoteOverlayHandle interface {
	// UpdateLines replaces the overlay's cached lines and triggers
	// a TUI render. Safe to call from any goroutine.
	UpdateLines(lines []string)
	// Close signals that the overlay is finished and unblocks the
	// originating [UIContext.RunRemoteOverlay] call with the given
	// result value. Safe to call from any goroutine.
	Close(result any)
}

// RemoteOverlayHost is the callback surface the bridge provides so the
// overlay can forward user input back to the remote producer.
//
// pig-specific: no upstream equivalent.
type RemoteOverlayHost interface {
	// OnInput is invoked for every input chunk (a single key event
	// after [tui.ReadInput] segmentation) while the overlay is
	// open. Called from the input-reading goroutine.
	OnInput(data string)
}

// RemoteOverlayFocusHost is an optional [RemoteOverlayHost] that publishes the component's TUI focus to the remote producer: Pi's
// TUI.setFocus sets the focused flag of the component that gains focus and clears it on the one that loses it (tui.ts:602-665), so
// a custom component that another opens over learns it lost focus, and learns it has it again when that one closes.
//
// pig-specific: Pi's component is in-process; a remote component learns its focus through this callback.
type RemoteOverlayFocusHost interface {
	// OnFocus reports a change of the component's focus after it was mounted. Called from the goroutine that delivers its input,
	// in order with it.
	OnFocus(focused bool)
}

// RemoteTUI is an optional [UIContext] that drives the TUI a remote component factory receives. Pi hands showExtensionCustom's
// factory the TUI itself (interactive-mode.ts:3008), and the factory or its components may call its showOverlay, hideOverlay and
// setFocus (tui.ts:598-842); a subprocess factory makes those calls across the process boundary.
//
// pig-specific: Pi's factory holds the TUI in process.
type RemoteTUI interface {
	// ShowRemoteOverlay is TUI.showOverlay for a remote component: it mounts a raw overlay with no ui.custom call behind it and
	// returns once the overlay is on the stack. The handle takes the component's frames and applies OverlayHandle controls
	// (Control); a "hide" control ends the overlay. Close ends it too, removing its entry if it is still on the stack. ended is
	// closed once the overlay ended and stopped reading input. ok is false when the UI has no TUI.
	ShowRemoteOverlay(opts RemoteOverlayOptions, host RemoteOverlayHost) (handle RemoteOverlayHandle, ended <-chan struct{}, ok bool)
	// HideTopOverlay is TUI.hideOverlay: it pops the topmost overlay, whichever component it shows, and returns the component
	// that holds focus afterwards.
	HideTopOverlay(ctx context.Context) (focused any, err error)
	// SetRemoteFocus is TUI.setFocus with target and returns the component that holds focus afterwards.
	SetRemoteFocus(ctx context.Context, target RemoteOverlayFocusTarget) (focused any, err error)
}
