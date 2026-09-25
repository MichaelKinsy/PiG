package extension

import (
	"encoding/json"
	"regexp"
	"strconv"
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

var overlayPercentRE = regexp.MustCompile(`^(\d+(?:\.\d+)?)%$`)

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
