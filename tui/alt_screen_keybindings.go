package tui

// Alt-screen viewport actions added in pi-tui v0.87.1 keybindings.ts. The
// action names are defined here; their default keys join tuiKeybindingDefs
// with the per-platform TUI defaults.
const (
	KBAltScreenHalfPageUp     TUIKeybinding = "tui.altScreen.halfPageUp"
	KBAltScreenHalfPageDown   TUIKeybinding = "tui.altScreen.halfPageDown"
	KBAltScreenLineUp         TUIKeybinding = "tui.altScreen.lineUp"
	KBAltScreenLineDown       TUIKeybinding = "tui.altScreen.lineDown"
	KBAltScreenSearch         TUIKeybinding = "tui.altScreen.search"
	KBAltScreenSearchNext     TUIKeybinding = "tui.altScreen.searchNext"
	KBAltScreenSearchPrevious TUIKeybinding = "tui.altScreen.searchPrevious"
	KBAltScreenSearchClose    TUIKeybinding = "tui.altScreen.searchClose"
)
