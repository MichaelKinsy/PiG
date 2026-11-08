package runtime

// The TuiContext the runtime hands to commands, overlays and surfaces.
//
// pi: apps/p2/src/runtime/tui.ts (createContext)

import (
	"maps"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	chordtui "github.com/MichaelKinsy/PiG/chord/next/tui"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// TuiRuntimeContext is the runtime's TuiContext.
type TuiRuntimeContext struct{ rt *TuiRuntime }

var _ TuiContext = (*TuiRuntimeContext)(nil)

func (c *TuiRuntimeContext) formatKeys(id string, capitalize bool) string {
	return render.FormatKeyText(strings.Join(c.rt.Commands.GetKeys(id), "/"), capitalize)
}

// Theme is the instance theme.
func (c *TuiRuntimeContext) Theme() *theme.ThemeRef { return c.rt.Theme() }

// KeyText is the formatted keys of a binding id.
func (c *TuiRuntimeContext) KeyText(id string) string { return c.formatKeys(id, false) }

// RequestRender requests a render. Any goroutine may call it; after Dispose it does nothing.
func (c *TuiRuntimeContext) RequestRender() {
	c.rt.renderGate.RLock()
	defer c.rt.renderGate.RUnlock()
	if !c.rt.disposed.Load() {
		c.rt.TUI.RequestRender()
	}
}

// HintKey is hints.key.
func (c *TuiRuntimeContext) HintKey(id, description string) string {
	return c.Hints().Key(id, description)
}

// Host is the Chord host.
func (c *TuiRuntimeContext) Host() host.Host { return c.rt.Host }

// Keys is key matching over dynamic ids.
func (c *TuiRuntimeContext) Keys() TuiKeys { return tuiKeys{c.rt} }

// Hints are the key hints.
func (c *TuiRuntimeContext) Hints() TuiHints { return tuiHints{c} }

// Settings are the guest-local display preferences.
func (c *TuiRuntimeContext) Settings() prefs.TuiSettings { return c.rt.Appearance.Settings }

// Role is the visitor's role.
func (c *TuiRuntimeContext) Role() TuiRole { return c.rt.Role }

// Identity is the viewed session and conversation.
func (c *TuiRuntimeContext) Identity() TuiIdentity {
	return TuiIdentity{Session: c.rt.Session, Conversation: c.rt.Conversation, Main: c.rt.MainOf(c.rt.Session)}
}

// Navigate shows a screen; an empty session keeps the viewed one.
func (c *TuiRuntimeContext) Navigate(screen, session string, conversation *int) {
	if session == "" {
		c.rt.Navigate(screen, c.rt.Session, c.rt.HasSession, conversation)
		return
	}
	c.rt.Navigate(screen, session, true, conversation)
}

// View is the live view toggles.
func (c *TuiRuntimeContext) View() TuiView {
	return TuiView{ExpandedTools: c.rt.ExpandedTools, ShowThinking: c.rt.ShowThinking}
}

// SetExpandedTools flips the tool expansion.
func (c *TuiRuntimeContext) SetExpandedTools(expanded bool) {
	c.rt.ExpandedTools = expanded
	c.rt.Changed()
}

// SetShowThinking flips the thinking blocks.
func (c *TuiRuntimeContext) SetShowThinking(show bool) {
	c.rt.ShowThinking = show
	c.rt.Changed()
}

// Commands are the commands as seen from contexts.
func (c *TuiRuntimeContext) Commands() TuiCommandRunner { return tuiCommandRunner{c.rt} }

// Surface is a mounted surface by slot key, nil when none.
func (c *TuiRuntimeContext) Surface(name string) pitui.Component {
	if surface, ok := c.rt.surfaces[name]; ok {
		return surface
	}
	return nil
}

// Notify shows one status line.
func (c *TuiRuntimeContext) Notify(message string, kind NoticeKind) { c.rt.Notify(message, kind) }

// Fail shows a failure.
func (c *TuiRuntimeContext) Fail(err error) { c.rt.Fail(err) }

// Perform shows action's failure, if it fails.
func (c *TuiRuntimeContext) Perform(action func() error) { c.rt.Perform(action) }

// Scope is a scope for an overlay or a command, disposed by its owner when it closes.
func (c *TuiRuntimeContext) Scope() *chordtui.TuiScope {
	return chordtui.NewTuiScope(c.rt.ctx, c.rt.Host, c.rt.postChanged)
}

// Overlay shows an overlay on the tui stack.
func (c *TuiRuntimeContext) Overlay(id string, component pitui.Component, options pitui.OverlaySpec) TuiOverlayHandle {
	return c.rt.Overlay(id, component, options)
}

// Panel is the side panel.
func (c *TuiRuntimeContext) Panel() TuiPanel { return tuiPanel{c.rt} }

// Post queues fn on the owner loop.
func (c *TuiRuntimeContext) Post(fn func()) { c.rt.Post(fn) }

// Exit asks the launcher to end the session.
func (c *TuiRuntimeContext) Exit() { c.rt.OnExit() }

// Reload reloads the TUI from the server.
func (c *TuiRuntimeContext) Reload() { c.rt.OnReload() }

// Grant asks the visitor's launcher for a C action.
func (c *TuiRuntimeContext) Grant(action string, payload any) GrantResult {
	return c.rt.OnGrant(action, payload)
}

// Prefs is the visitor's preferences as this runtime applies them.
func (c *TuiRuntimeContext) Prefs() TuiPrefsView { return tuiPrefs{c.rt} }

type tuiKeys struct{ rt *TuiRuntime }

func (k tuiKeys) Matches(data, id string) bool          { return k.rt.Commands.Matches(data, id) }
func (k tuiKeys) GetKeys(id string) []string            { return k.rt.Commands.GetKeys(id) }
func (k tuiKeys) Manager() *pitui.TUIKeybindingsManager { return k.rt.Commands.Manager() }

type tuiHints struct{ c *TuiRuntimeContext }

func (h tuiHints) KeyText(id string) string        { return h.c.formatKeys(id, false) }
func (h tuiHints) KeyDisplayText(id string) string { return h.c.formatKeys(id, true) }
func (h tuiHints) Key(id, description string) string {
	current := h.c.rt.Theme().Current()
	return current.Fg("dim", h.KeyText(id)) + current.Fg("muted", " "+description)
}
func (h tuiHints) Raw(key, description string) string {
	current := h.c.rt.Theme().Current()
	return current.Fg("dim", render.FormatKeyText(key, false)) + current.Fg("muted", " "+description)
}

type tuiCommandRunner struct{ rt *TuiRuntime }

func (r tuiCommandRunner) Run(id, args string) bool  { return r.rt.Commands.Run(id, args, nil) }
func (r tuiCommandRunner) RunSlash(text string) bool { return r.rt.Commands.RunSlash(text, nil) }
func (r tuiCommandRunner) Expandable(name string) bool {
	return slices.ContainsFunc(r.rt.Commands.SlashEntries(), func(each SlashEntryWithSource) bool { return each.Entry.Name == name })
}
func (r tuiCommandRunner) Expand(text string) (string, error) { return r.rt.Commands.Expand(text) }
func (r tuiCommandRunner) Autocomplete() pitui.AutocompleteProvider {
	return r.rt.Commands.Autocomplete()
}
func (r tuiCommandRunner) Palette() { r.rt.Commands.Run("p2.palette", "", nil) }
func (r tuiCommandRunner) Hotkeys() { r.rt.Commands.Run("p2.hotkeys", "", nil) }
func (r tuiCommandRunner) List(active map[string]bool) []TuiCommandListing {
	return r.rt.Commands.List(active)
}
func (r tuiCommandRunner) Contexts() map[string]bool { return r.rt.Contexts() }
func (r tuiCommandRunner) Report() TuiCommandReport {
	rt := r.rt
	problems := slices.Clone(rt.Appearance.Local.Problems)
	if rt.Appearance.ThemeError != "" {
		problems = append(problems, rt.Appearance.ThemeError)
	}
	return TuiCommandReport{
		Conflicts: rt.Commands.Conflicts(), Privileged: rt.Commands.Privileged(), Unknown: rt.Commands.Unknown(), Problems: problems,
	}
}

type tuiPanel struct{ rt *TuiRuntime }

func (p tuiPanel) Open(id string, content pitui.Component, title func() string) TuiPanelHandle {
	return p.rt.OpenPanel(id, content, title)
}
func (p tuiPanel) Fits() bool { return p.rt.panelFits() }
func (p tuiPanel) Current() string {
	if p.rt.panel == nil {
		return ""
	}
	return p.rt.panel.ID
}
func (p tuiPanel) Focused() bool      { return p.rt.panelFocused }
func (p tuiPanel) Focus(focused bool) { p.rt.FocusPanel(focused) }
func (p tuiPanel) Close()             { p.rt.ClosePanel() }

type tuiPrefs struct{ rt *TuiRuntime }

func (p tuiPrefs) ThemeSetting() string { return p.rt.Appearance.ThemeSetting }
func (p tuiPrefs) ThemeNames() []string {
	names := append([]string{theme.SystemThemeName}, theme.BuiltinThemeNames()...)
	custom := slices.Sorted(maps.Keys(p.rt.Appearance.Local.Themes))
	for _, name := range custom {
		if _, builtin := theme.BuiltinTheme(name); name != theme.SystemThemeName && !builtin {
			names = append(names, name)
		}
	}
	return names
}
func (p tuiPrefs) PreviewTheme(setting string) *theme.ThemeRef {
	a := p.rt.Appearance
	resolved, _ := theme.ResolveTheme(theme.ResolveOptions{Setting: setting, Themes: a.Local.Themes, Mode: a.ColorMode(), Terminal: a.Terminal})
	return theme.NewThemeRef(resolved)
}
func (p tuiPrefs) Keybindings() map[string][]string { return p.rt.Appearance.Keybindings }
func (p tuiPrefs) Save(patch any) GrantResult {
	return p.rt.OnGrant("savePrefs", map[string]any{"patch": patch})
}

type selectorContext struct{ TuiContext }

func (s selectorContext) Keys() components.KeyMatcher { return s.TuiContext.Keys() }
func (s selectorContext) Overlay(id string, component pitui.Component, options pitui.OverlaySpec) components.OverlayHandle {
	return s.TuiContext.Overlay(id, component, options)
}

// Selectors is ctx as the selectors and list overlays take it (components.OverlayHost): Go has no covariant return types, so the key matcher and the overlay
// handle are adapted.
func Selectors(ctx TuiContext) components.OverlayHost { return selectorContext{ctx} }
