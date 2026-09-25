package widthx

// FrameAt returns the rows of a pre-rendered component frame for painting at
// width. frameWidth is the width the frame was rendered at; 0 means the
// producer did not report one, and the rows are painted as rendered (an
// over-wide row then reaches the renderer's overflow check, as in Pi).
//
// A frame rendered for any other width is stale and is never painted,
// whatever its rows contain: Pi always renders components at the current
// width. The caller has already requested a frame at the current width.
func FrameAt(lines []string, frameWidth, width int) []string {
	if frameWidth == 0 || frameWidth == width {
		return lines
	}
	return nil
}
