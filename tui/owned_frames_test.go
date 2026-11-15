package tui

import "time"

// ownFrames makes the test the only author of frames on ui: no timer goroutine starts, and a frame is drawn only when the test calls Render (row 95's
// cure, SetRenderDeadline). A test that reads a plain buffer handed to NewWithOutput needs it: ShowOverlay and RequestRender ask for a frame, and on a
// timer goroutine that frame writes the buffer while the test reads it. A test whose subject is the timer's frame does not use it: it gives the screen a
// locked output (lockedBuffer).
func ownFrames(ui *TuiMainScreen) { ui.SetRenderDeadline(func(time.Time) {}) }
