package tui

// Mode reports the renderer mode. Mirrors upstream `mode = "regular"` of TuiMainScreen.
func (t *TuiMainScreen) Mode() TuiMode { return TuiModeRegular }

// FullRedraws counts the full repaints of the screen. Mirrors upstream `fullRedraws` (tui.ts:553).
func (t *TuiMainScreen) FullRedraws() int { return int(t.fullRedrawCount.Load()) }
