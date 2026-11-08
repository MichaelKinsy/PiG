package theme

import (
	"github.com/MichaelKinsy/PiG/tui"
)

// pi: apps/p2/src/tui/theme/ref.ts

// ThemeRef is a runtime-owned, swappable theme (p2-tui-port.md section 5.3). Derived pi-tui themes are created once per ref and dereference
// Current on every call, so a swap never leaves stale derived objects. Components that bake colors into strings subscribe (or implement
// ThemeChanged) and rebuild; caches key on Version.
//
// A ThemeRef is used from the UI goroutine, as the TypeScript's single event loop: it is not safe for concurrent use.
type ThemeRef struct {
	theme     *Theme
	listeners []*themeListener
	// Version counts the swaps.
	Version int
	// Markdown, SelectList and Editor are the pi-tui themes derived from the ref.
	Markdown   tui.MarkdownTheme
	SelectList tui.SelectListTheme
	Editor     tui.EditorTheme
}

type themeListener struct{ fn func(*Theme) }

// NewThemeRef is the ThemeRef constructor.
func NewThemeRef(theme *Theme) *ThemeRef {
	r := &ThemeRef{theme: theme}
	fg := func(token string) func(string) string {
		return func(text string) string { return r.theme.Fg(token, text) }
	}
	r.Markdown = tui.MarkdownTheme{
		Heading: fg("mdHeading"), Link: fg("mdLink"), LinkUrl: fg("mdLinkUrl"), Code: fg("mdCode"), CodeBlock: fg("mdCodeBlock"),
		CodeBlockBorder: fg("mdCodeBlockBorder"), Quote: fg("mdQuote"), QuoteBorder: fg("mdQuoteBorder"), Hr: fg("mdHr"), ListBullet: fg("mdListBullet"),
		Bold:          func(text string) string { return r.theme.Bold(text) },
		Italic:        func(text string) string { return r.theme.Italic(text) },
		Underline:     func(text string) string { return r.theme.Underline(text) },
		Strikethrough: func(text string) string { return r.theme.Strikethrough(text) },
		HighlightCode: func(code, lang string) []string { return r.theme.Highlight(code, lang) },
	}
	r.SelectList = tui.SelectListTheme{
		SelectedPrefix: fg("accent"), SelectedText: fg("accent"), Description: fg("muted"), ScrollInfo: fg("muted"), NoMatch: fg("muted"),
	}
	r.Editor = tui.EditorTheme{BorderColor: fg("borderMuted"), SelectList: r.SelectList}
	return r
}

// SettingsList is the derived SettingsListTheme. The TypeScript's `cursor` is a getter that reads the current theme on each access; a Go struct field
// is a value, so the cursor is the current theme's at the time of the call: a settings list built from it before a swap keeps the old cursor color.
func (r *ThemeRef) SettingsList() tui.SettingsListTheme {
	fg := func(token string) func(string) string {
		return func(text string) string { return r.theme.Fg(token, text) }
	}
	return tui.SettingsListTheme{
		Label: func(text string, selected bool) string {
			if selected {
				return r.theme.Fg("accent", text)
			}
			return text
		},
		Value: func(text string, selected bool) string {
			if selected {
				return r.theme.Fg("accent", text)
			}
			return r.theme.Fg("muted", text)
		},
		Description: fg("dim"),
		Cursor:      r.theme.Fg("accent", "→ "),
		Hint:        fg("dim"),
	}
}

// Current is the ref's theme.
func (r *ThemeRef) Current() *Theme { return r.theme }

// Set swaps the theme. Runtime only: plugins change themes through preferences, never directly.
func (r *ThemeRef) Set(theme *Theme) {
	if theme == r.theme {
		return
	}
	r.theme = theme
	r.Version++
	for _, listener := range append([]*themeListener(nil), r.listeners...) {
		listener.fn(theme)
	}
}

// Subscribe registers a listener for swaps and returns its unsubscribe function.
func (r *ThemeRef) Subscribe(listener func(*Theme)) (unsubscribe func()) {
	entry := &themeListener{fn: listener}
	r.listeners = append(r.listeners, entry)
	return func() {
		for i, l := range r.listeners {
			if l == entry {
				r.listeners = append(r.listeners[:i:i], r.listeners[i+1:]...)
				return
			}
		}
	}
}
