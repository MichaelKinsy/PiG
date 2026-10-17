package tui

// OverlayValue is a serialisable upstream SizeValue: an absolute cell count
// or, when Percent is set, a percentage of the terminal dimension.
type OverlayValue struct {
	Value   float64
	Percent bool
	// Invalid preserves a supplied but malformed size, distinct from omission.
	Invalid bool
}

// OverlayMarginSpec mirrors upstream OverlayMargin: a margin per edge.
type OverlayMarginSpec struct {
	Top, Right, Bottom, Left int
}

// OverlayMarginOption is upstream's `margin?: number | OverlayMargin` (tui.ts:243): the variants are OverlayMarginAll and OverlayMarginSpec, and nil is an omitted margin.
type OverlayMarginOption interface {
	overlayMarginOption()
}

// OverlayMarginAll is the numeric form of margin: the same margin on all four edges.
type OverlayMarginAll int

func (OverlayMarginAll) overlayMarginOption()  {}
func (OverlayMarginSpec) overlayMarginOption() {}

// OverlaySpec is the serialisable subset of upstream pi-tui OverlayOptions
// (everything except the visible callback). It lets callers outside this
// package, such as the subprocess extension bridge, open a component-framed
// overlay whose geometry resolves exactly like upstream showOverlay.
type OverlaySpec struct {
	Width        *OverlayValue
	MinWidth     *int
	MaxHeight    *OverlayValue
	Anchor       string
	OffsetX      int
	OffsetY      int
	Row          *OverlayValue
	Col          *OverlayValue
	Margin       OverlayMarginOption
	NonCapturing bool
}

func (v *OverlayValue) size() overlaySize {
	if v == nil {
		return overlaySize{}
	}
	return overlaySize{value: v.Value, percent: v.Percent, set: true, invalid: v.Invalid}
}

// Options converts the spec into OverlayOptions. The result carries no modal
// title or fractions, so ShowOverlay mounts the component without a frame.
func (s OverlaySpec) Options() OverlayOptions {
	opts := OverlayOptions{
		width:        s.Width.size(),
		maxHeight:    s.MaxHeight.size(),
		anchor:       overlayAnchor(s.Anchor),
		offsetX:      s.OffsetX,
		offsetY:      s.OffsetY,
		row:          s.Row.size(),
		col:          s.Col.size(),
		nonCapturing: s.NonCapturing,
	}
	if s.MinWidth != nil {
		opts.minWidth = *s.MinWidth
	}
	switch margin := s.Margin.(type) {
	case OverlayMarginAll:
		all := int(margin)
		opts.marginAll = &all
	case OverlayMarginSpec:
		opts.margin = overlayMargin(margin)
	case *OverlayMarginSpec:
		// A pointer satisfies the interface through the value receiver; it is the same per-edge margin, and nil is omitted.
		if margin != nil {
			opts.margin = overlayMargin{Top: margin.Top, Right: margin.Right, Bottom: margin.Bottom, Left: margin.Left}
		}
	}
	return opts
}

// OverlayGeometry is the resolved placement of an overlay.
type OverlayGeometry struct {
	Width, Row, Col int
	MaxHeight       int
	HasMaxHeight    bool
}

// ResolveOverlayGeometry resolves opts for a component of overlayHeight lines
// on a termWidth x termHeight screen, using the same resolver the compositor
// uses. Width is the width the component is rendered at.
func ResolveOverlayGeometry(opts OverlayOptions, overlayHeight, termWidth, termHeight int) OverlayGeometry {
	l := resolveOverlayLayout(opts, overlayHeight, termWidth, termHeight)
	return OverlayGeometry{Width: l.width, Row: l.row, Col: l.col, MaxHeight: l.maxHeight, HasMaxHeight: l.hasMaxHeight}
}
