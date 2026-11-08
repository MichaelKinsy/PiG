package runtime

// The per-runtime context handed to TUI commands, overlays and surfaces (p2-tui-port.md sections 5.3, 6.3 and 7.2). These are the parts that do
// not reach the Chord host or the command registry; TuiContext itself follows with TuiRuntime.
//
// pi: apps/p2/src/runtime/tui-context.ts

import (
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/tui"
)

// TuiKeys is key matching over dynamic (plugin) ids.
type TuiKeys interface {
	Matches(data, id string) bool
	// GetKeys are the key ids bound to id.
	GetKeys(id string) []string
	// Manager is the current manager, rebuilt whenever commands or preferences change and installed with tui.SetKeybindings.
	Manager() *tui.TUIKeybindingsManager
}

// TuiHints is coding-agent components/keybinding-hints.ts over the instance keymap and theme.
type TuiHints interface {
	// KeyText is the "ctrl+o" style key text of a binding, "" when unbound.
	KeyText(id string) string
	// KeyDisplayText is the capitalized key text ("Ctrl+O").
	KeyDisplayText(id string) string
	// Key is dim keys and a muted description, like pi's `keyHint`.
	Key(id, description string) string
	Raw(key, description string) string
}

// TuiOverlayHandle is a shown overlay.
type TuiOverlayHandle interface {
	ID() string
	Close()
	Closed() bool
}

// TuiPanelHandle is the content shown in the side panel.
type TuiPanelHandle interface {
	ID() string
	Close()
	Closed() bool
}

// Disposable is a component with a Dispose method; the panel and overlays call it, once, when the content leaves.
type Disposable interface{ Dispose() }

// TuiPanel is the side panel next to the screen: one at a time, hidden on narrow terminals.
type TuiPanel interface {
	// Open shows content in the panel under title, replacing what it showed (that content is disposed). The editor keeps the focus; `p2.panel.focus`
	// moves it into the panel. Closed on navigation to another conversation and on reload.
	Open(id string, content tui.Component, title func() string) TuiPanelHandle
	// Fits is whether the terminal is wide enough to show the panel next to the screen.
	Fits() bool
	// Current is the id of the panel's content, "" when closed.
	Current() string
	// Focused is whether the panel has the focus (it keeps scroll keys; Escape gives the focus back).
	Focused() bool
	// Focus moves the focus into the panel or back to the screen.
	Focus(focused bool)
	Close()
}

// TuiRole is the visitor's role.
type TuiRole string

const (
	RoleOwner TuiRole = "owner"
	RoleGuest TuiRole = "guest"
)

// GrantResult is a launcher action's result.
type GrantResult struct {
	OK    bool
	Value any
	// Error is set when OK is false: "denied", "failed", "busy", ...
	Error string
}

// NoticeKind is the kind of a status line.
type NoticeKind string

const (
	NoticeInfo    NoticeKind = "info"
	NoticeWarning NoticeKind = "warning"
	NoticeError   NoticeKind = "error"
)

// Notifier shows one status line.
type Notifier interface {
	Notify(message string, kind NoticeKind)
}

// TuiPrefsView is the visitor's preferences as the runtime applies them.
type TuiPrefsView interface {
	// ThemeSetting is the effective theme setting (a name, "light/dark" pair, or "" for the system theme).
	ThemeSetting() string
	// ThemeNames are "system", the built-ins, then the visitor's custom themes.
	ThemeNames() []string
	// PreviewTheme is a separate ThemeRef for a picker's preview pane: the live theme never changes until the patch is saved.
	PreviewTheme(setting string) *theme.ThemeRef
	// Keybindings are the effective user keybindings (server defaults under the visitor's own).
	Keybindings() map[string][]string
	// Save proposes a `prefs.json` patch to the launcher: it shows the diff and writes after confirmation.
	Save(patch any) GrantResult
}
