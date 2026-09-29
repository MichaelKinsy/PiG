package tui

// Consumers hold overlay handles behind interface{ Unfocus() }. Keeping the zero-argument method and adding UnfocusWith for Pi's unfocus(options) leaves those assignments compiling; a variadic Unfocus fails to build here.
var (
	_ interface{ Unfocus() }                          = (*OverlayHandle)(nil)
	_ interface{ UnfocusWith(OverlayUnfocusOptions) } = (*OverlayHandle)(nil)
)
