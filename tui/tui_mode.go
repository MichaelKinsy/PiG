package tui

// TuiMode is the renderer mode. Mirrors upstream TuiMode (tui.ts:448).
type TuiMode string

const (
	TuiModeRegular    TuiMode = "regular"
	TuiModeFullscreen TuiMode = "fullscreen"
)
