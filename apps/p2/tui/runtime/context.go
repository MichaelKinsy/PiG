package runtime

// The per-runtime context handed to TUI commands, overlays and surfaces (p2-tui-port.md sections 5.3, 6.3 and 7.2). These are the parts that do
// not reach the Chord host or the command registry, then TuiContext itself.
//
// pi: apps/p2/src/runtime/tui-context.ts

import (
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	chordtui "github.com/MichaelKinsy/PiG/chord/next/tui"
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

// TuiIdentity is the viewed session and conversation, and the session's main conversation (1, or its current `/tree` branch). Session is "" off a session.
type TuiIdentity struct {
	Session      string
	Conversation int
	Main         int
}

// TuiView is the view toggles the visitor flips.
type TuiView struct {
	ExpandedTools bool
	ShowThinking  bool
}

// TuiCommandReport is what /hotkeys shows: conflicts, launcher-owned keys and preference problems.
type TuiCommandReport struct {
	Conflicts  []TuiCommandConflict
	Privileged prefs.PrivilegedResolution
	Unknown    []string
	Problems   []string
}

// TuiCommandListing is one registered command as the palette, slash completion and /hotkeys list it.
type TuiCommandListing struct {
	ID      string
	Command TuiCommand
	Keys    []string
	// Slash is the enabled slash name, "" when none.
	Slash string
}

// TuiCommandRunner is commands as seen from contexts: run by id, slash text, palette and help.
type TuiCommandRunner interface {
	// Run runs a registered command if its `when` currently holds. Returns false if unknown or not applicable.
	Run(id, args string) bool
	// RunSlash runs `/name args` through the slash-name index. Returns false if text names no enabled slash command.
	RunSlash(text string) bool
	// Expandable is whether `/name` is a template or skill from a TuiSlashSources entry (not a command).
	Expandable(name string) bool
	// Expand is the text to send for `/template args` or `/skill:name args` (expanded on the session worker), else text.
	Expand(text string) (string, error)
	// Autocomplete is `/` completion over enabled slash names and their argument completers (section 6.3).
	Autocomplete() tui.AutocompleteProvider
	Palette()
	Hotkeys()
	// List is the registered commands (registration order), filtered to those applicable in active unless it is nil.
	List(active map[string]bool) []TuiCommandListing
	// Contexts is the active context set right now.
	Contexts() map[string]bool
	Report() TuiCommandReport
}

// TuiContext is everything a TUI contribution may use; it is a superset of render.RenderContext.
type TuiContext interface {
	render.RenderContext
	Host() host.Host
	Keys() TuiKeys
	Hints() TuiHints
	// Settings are the guest-local display preferences (validated data from the launcher over server defaults).
	Settings() prefs.TuiSettings
	// Role is the visitor's role, "" when not known yet.
	Role() TuiRole
	Identity() TuiIdentity
	// Navigate shows a screen; with a non-nil conversation, that conversation of the session (default: the main one on a switch).
	Navigate(screen, session string, conversation *int)
	// View is the live view toggles; SetExpandedTools and SetShowThinking flip them (and redraw).
	View() TuiView
	SetExpandedTools(expanded bool)
	SetShowThinking(show bool)
	Commands() TuiCommandRunner
	// Surface is a mounted surface by slot key (e.g. "chat"), nil when none; commands use it to reach their plugin's live instance.
	Surface(name string) tui.Component
	// Notify shows one status line.
	Notify(message string, kind NoticeKind)
	// Fail shows a failure (a refused call, a rolled-back guess) as an error status line.
	Fail(err error)
	// Perform shows action's failure, if it fails (an optimistic call's rejection is its caller's).
	Perform(action func() error)
	// Scope is a scope for an overlay or a command (references, optimistic guesses), disposed by its owner when it closes.
	Scope() *chordtui.TuiScope
	// Overlay shows an overlay on the tui stack. While it is focused, commands whose `when` names `overlay` or `overlay:<id>` run before the component
	// (section 6.2 stage 3). Closed on navigation, reload and plugin replacement.
	Overlay(id string, component tui.Component, options tui.OverlaySpec) TuiOverlayHandle
	Panel() TuiPanel
	Exit()
	Reload()
	// Grant asks the visitor's launcher for a C action (section 3.2), e.g. `openUrl`; the launcher shows its own consent UI with the exact data.
	Grant(action string, payload any) GrantResult
	// Prefs is the visitor's preferences as this runtime applies them.
	Prefs() TuiPrefsView
}
