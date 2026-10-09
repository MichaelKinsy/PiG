package tui

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ─── Theme ───────────────────────────────────────────────────────────────────
//
// Complete theme system matching upstream's dark.json / light.json with
// all 50+ color tokens. Auto-detects dark/light via COLORFGBG env var
// (same as upstream theme.ts:detectTerminalBackground).
//
// Upstream reference: theme/dark.json, theme/light.json, theme.ts.

// Theme holds the resolved color palette for the current session.
// All fields are pre-computed ANSI escape sequences (fg or bg).
type Theme struct {
	// Name of the theme (e.g. "dark", "light", or a custom name).
	Name string
	// SourceInfo is where the theme came from, set by the resource loader after it loads the theme (theme.ts Theme.sourceInfo).
	SourceInfo *source.SourceInfo
	// SourcePath is the file the theme was loaded from, empty for a built-in or in-memory theme (theme.ts Theme.sourcePath).
	SourcePath string

	// ─── Core colors ─────────────────────────────────────────
	Accent  string // accent text (teal/cyan)
	Success string // green
	Error   string // red
	Warning string // yellow
	Muted   string // gray
	Dim     string // dim gray
	Text    string // default text (empty = terminal default)

	// ─── Border colors ───────────────────────────────────────
	Border       string // blue
	BorderAccent string // cyan
	BorderMuted  string // dark gray

	// ─── Background colors ───────────────────────────────────
	UserMessageBg      string // ANSI bg escape
	UserMessageText    string // fg text on user message bg
	ToolPendingBg      string // tool running
	ToolSuccessBg      string // tool completed successfully
	ToolErrorBg        string // tool failed
	ToolTitle          string // tool header text
	ToolOutput         string // tool output text (gray)
	SelectedBg         string // selected item bg
	CustomMessageBg    string // custom message bg
	CustomMessageText  string // custom message text
	CustomMessageLabel string // custom message label

	// ─── Markdown colors ─────────────────────────────────────
	MDHeading         string
	MDLink            string
	MDLinkUrl         string
	MDCode            string
	MDCodeBlock       string
	MDCodeBlockBorder string
	MDQuote           string
	MDQuoteBorder     string
	MDHr              string
	MDListBullet      string

	// ─── Diff colors ─────────────────────────────────────────
	ToolDiffAdded   string
	ToolDiffRemoved string
	ToolDiffContext string

	// ─── Syntax highlighting ─────────────────────────────────
	SyntaxComment     string
	SyntaxKeyword     string
	SyntaxFunction    string
	SyntaxVariable    string
	SyntaxString      string
	SyntaxNumber      string
	SyntaxType        string
	SyntaxOperator    string
	SyntaxPunctuation string

	// ─── Thinking level indicators ───────────────────────────
	ThinkingText    string
	ThinkingOff     string
	ThinkingMinimal string
	ThinkingLow     string
	ThinkingMedium  string
	ThinkingHigh    string
	ThinkingXhigh   string

	// ─── Misc ────────────────────────────────────────────────
	BashMode string // bash mode indicator

	// ─── Export colors (for HTML export) ─────────────────────
	ExportPageBg string // hex string (not ANSI)
	ExportCardBg string // hex string
	ExportInfoBg string // hex string

	// Reset escapes.
	BgClose string
	Reset   string

	// colorKeys lists the color tokens in upstream's Theme construction order: concrete tokens (foregrounds, then backgrounds), then the tokens set to "" (terminal default). The HTML export emits CSS vars in this order.
	colorKeys []string
	// fgAnsi and bgAnsi hold the precomputed escape sequences, keeping Fg and Bg on the render hot path to a lookup.
	fgAnsi, bgAnsi map[string]string
	// concreteColors are the tokens with a color of their own; the others follow the terminal's default colors.
	concreteColors                                   map[string]Color
	defaultForegroundTokens, defaultBackgroundTokens []string
	// dimTokens are the foreground tokens rendered faint (SGR 2) on top of their color.
	dimTokens        map[string]bool
	ownAppearance    ThemeAppearance
	resolvedColorSet atomic.Pointer[resolvedThemeColors]

	// mode is the color mode the ANSI fields were built for (theme.ts
	// Theme.mode). The zero value is truecolor.
	mode TerminalColorMode
	// source is the theme JSON this theme was resolved from; it rebuilds the
	// theme in the other color mode. Nil for a theme assembled in code.
	source *ThemeJSON
}

// TerminalColorMode mirrors theme.ts Theme.getColorMode.
func (t *Theme) GetColorMode() TerminalColorMode {
	if t.mode == "" {
		return TerminalColorModeTrueColor
	}
	return t.mode
}

// WithColorMode returns this theme resolved in mode, mirroring theme.ts createTheme(themeJson, mode). Rebuilt themes are not cached, so obsolete themes can be collected. A theme without JSON source is returned unchanged.
// The variant keeps the theme's name: it is the same theme in another mode, and a registry entry activated by name must stay that name even when its JSON names another theme.
func (t *Theme) WithColorMode(mode TerminalColorMode) *Theme {
	if t == nil || t.GetColorMode() == mode || t.source == nil {
		return t
	}
	variant, err := resolveThemeWithMode(cloneThemeJSON(t.source), mode)
	if err != nil {
		return t
	}
	variant.Name = t.Name
	return variant
}

// currentColorMode mirrors createTheme's default mode:
// getCapabilities().trueColor ? "truecolor" : "256color".
func currentColorMode() TerminalColorMode {
	if GetCapabilities().TrueColor {
		return TerminalColorModeTrueColor
	}
	return TerminalColorMode256
}

// storeActiveTheme activates t in the color mode the terminal supports now,
// as upstream loadTheme creates the theme when it is set.
func storeActiveTheme(t *Theme) {
	activeTheme.Store(t.WithColorMode(currentColorMode()))
}

// RefreshActiveThemeColorMode re-activates the active theme in the color mode
// the terminal supports now. Upstream re-creates the theme from settings after
// applying capability overrides on reload.
func RefreshActiveThemeColorMode() {
	themeMutationMu.Lock()
	defer themeMutationMu.Unlock()
	if t := ActiveTheme(); t != nil {
		storeActiveTheme(t)
	}
}

// ThemeHexFg returns the active theme's foreground escape for a fixed hex color, in the theme's color mode.
func ThemeHexFg(hex string) string {
	color, err := ParseColor(hex)
	if err != nil {
		return ""
	}
	return ForegroundAnsi(color, ActiveTheme().GetColorMode())
}

// ThemeHexBg returns the active theme's background escape for a fixed hex color, in the theme's color mode.
func ThemeHexBg(hex string) string {
	color, err := ParseColor(hex)
	if err != nil {
		return ""
	}
	return BackgroundAnsi(color, ActiveTheme().GetColorMode())
}

const (
	// Scoped SGR resets mirror upstream Pi's theme helpers:
	// foreground styles close with 39m and background styles close with 49m.
	// Avoid using SGRResetAll inside bg-painted content because it also clears
	// background color and creates visual gaps/stripes.
	SGRResetAll       = "\x1b[0m"
	SGRFgReset        = "\x1b[39m"
	SGRBgReset        = "\x1b[49m"
	SGRBoldDimReset   = "\x1b[22m"
	SGRItalicReset    = "\x1b[23m"
	SGRUnderlineReset = "\x1b[24m"
	SGRInverseReset   = "\x1b[27m"
	SGRStrikeReset    = "\x1b[29m"
)

type TerminalTheme string

// RgbColor preserves JavaScript numeric channels, including NaN when an overflowing channel is scaled by an infinite maximum.
type RgbColor struct {
	R float64
	G float64
	B float64
}

// GetFgAnsi returns the opening escape sequence of a foreground token; faint tokens include SGR 2, which "\x1b[22m" closes.
// Mirrors upstream theme.getFgAnsi(tokenName).
// Like upstream it panics with `Unknown theme color: <token>` for a token the theme does not define (theme.ts:tokenAnsi).
func (t *Theme) GetFgAnsi(token string) string {
	ansi, ok := t.fgAnsi[token]
	if !ok {
		panic(unknownThemeColor(token))
	}
	if t.dimTokens[token] {
		return ansi + "\x1b[2m"
	}
	return ansi
}

func unknownThemeColor(token string) error { return fmt.Errorf("Unknown theme color: %s", token) }

// GetBgAnsi returns the opening escape sequence of a background token.
// Mirrors upstream theme.getBgAnsi(tokenName).
// Like upstream it panics with `Unknown theme color: <token>` for a token the theme does not define.
func (t *Theme) GetBgAnsi(token string) string {
	ansi, ok := t.bgAnsi[token]
	if !ok {
		panic(unknownThemeColor(token))
	}
	return ansi
}

// ANSIPalette returns every resolved token as foreground and background ANSI
// openings. It is used at process boundaries where theme helper functions
// cannot cross but their current immutable token table can.
func (t *Theme) ANSIPalette() (map[string]string, map[string]string) {
	fg := make(map[string]string, len(t.fgAnsi))
	bg := make(map[string]string, len(t.bgAnsi))
	for token := range t.fgAnsi {
		fg[token] = t.GetFgAnsi(token)
	}
	for token := range t.bgAnsi {
		bg[token] = t.GetBgAnsi(token)
	}
	return fg, bg
}

// Fg returns text in the foreground color of a token, closing the color (and the faint attribute of a faint token).
// Mirrors upstream theme.fg(tokenName, text); an unknown token panics with `Unknown theme color: <token>`, as upstream throws.
func (t *Theme) Fg(token, text string) string {
	ansi, ok := t.fgAnsi[token]
	if !ok {
		panic(unknownThemeColor(token))
	}
	if t.dimTokens[token] {
		return ansi + "\x1b[2m" + text + "\x1b[22;39m"
	}
	return ansi + text + SGRFgReset
}

// Bg wraps text in the background color of a token and resets only the background (theme.bg). An unknown token panics with `Unknown theme color: <token>`, as upstream throws.
func (t *Theme) Bg(token, text string) string {
	ansi, ok := t.bgAnsi[token]
	if !ok {
		panic(unknownThemeColor(token))
	}
	return ansi + text + SGRBgReset
}

// ThemeStyle is the style theme.style applies: a foreground and a background, each a theme token or a concrete Color, and text attributes. Setting both the token and the color of a slot is an error.
type ThemeStyle struct {
	TextAttributes
	FgToken, BgToken string
	Fg, Bg           Color
}

// Style renders text in style. A token is only accepted in its own slot, because "" (terminal default) means the default foreground or background depending on the slot.
// Mirrors upstream theme.style(text, options).
func (t *Theme) Style(text string, style ThemeStyle) (string, error) {
	if (style.FgToken != "" && style.Fg != nil) || (style.BgToken != "" && style.Bg != nil) {
		return "", fmt.Errorf("theme style sets both a token and a color for one slot")
	}
	attributes := style.TextAttributes
	var fgAnsi, bgAnsi string
	switch {
	case style.FgToken != "":
		ansi, ok := t.fgAnsi[style.FgToken]
		if !ok {
			return "", fmt.Errorf("Unknown theme color: %s", style.FgToken)
		}
		fgAnsi = ansi
		if t.dimTokens[style.FgToken] {
			attributes.Dim = true
		}
	case style.Fg != nil:
		fgAnsi = ForegroundAnsi(style.Fg, t.mode)
	}
	switch {
	case style.BgToken != "":
		ansi, ok := t.bgAnsi[style.BgToken]
		if !ok {
			return "", fmt.Errorf("Unknown theme color: %s", style.BgToken)
		}
		bgAnsi = ansi
	case style.Bg != nil:
		bgAnsi = BackgroundAnsi(style.Bg, t.mode)
	}
	return StyleTextWithAnsi(text, fgAnsi, bgAnsi, attributes), nil
}

// Inverse wraps text in reverse video. Mirrors upstream theme.inverse().
func (t *Theme) Inverse(text string) string {
	return "\x1b[7m" + text + SGRInverseReset
}

// Bold wraps text in bold, closing with SGR 22. Mirrors upstream theme.bold(text); an empty text stays empty.
func (t *Theme) Bold(text string) string { return markdownDecoration("\x1b[1m", SGRBoldDimReset, text) }

// Italic wraps text in italic. Mirrors upstream theme.italic(text).
func (t *Theme) Italic(text string) string {
	return markdownDecoration("\x1b[3m", SGRItalicReset, text)
}

// Underline wraps text in underline. Mirrors upstream theme.underline(text).
func (t *Theme) Underline(text string) string {
	return markdownDecoration("\x1b[4m", SGRUnderlineReset, text)
}

// Strikethrough wraps text in strikethrough. Mirrors upstream theme.strikethrough(text).
func (t *Theme) Strikethrough(text string) string {
	return markdownDecoration("\x1b[9m", SGRStrikeReset, text)
}

// GetThinkingBorderColor returns the function that colors a border for a thinking level with the level's theme token; a level without a token uses thinkingOff. Mirrors upstream theme.getThinkingBorderColor(level).
func (t *Theme) GetThinkingBorderColor(level string) func(string) string {
	token := thinkingBorderToken(level)
	if token == "" {
		token = "thinkingOff"
	}
	return func(text string) string { return t.Fg(token, text) }
}

// thinkingBorderToken is the theme token of a thinking level's border, empty for a level that has none.
func thinkingBorderToken(level string) string {
	switch level {
	case "off":
		return "thinkingOff"
	case "minimal":
		return "thinkingMinimal"
	case "low":
		return "thinkingLow"
	case "medium":
		return "thinkingMedium"
	case "high":
		return "thinkingHigh"
	case "xhigh":
		return "thinkingXhigh"
	case "max":
		return "thinkingMax"
	}
	return ""
}

// GetBashModeBorderColor returns the function that colors a border in bash mode with the bashMode token. Mirrors upstream theme.getBashModeBorderColor().
func (t *Theme) GetBashModeBorderColor() func(string) string {
	return func(text string) string { return t.Fg("bashMode", text) }
}

// Built-in production themes are resolved from the embedded pinned JSON so
// fields used directly by components and dynamic color maps share one source.
func mustLoadBuiltinTheme(name string) *Theme {
	theme, err := loadBuiltinThemeWithMode(name, TerminalColorModeTrueColor)
	if err != nil {
		panic(fmt.Sprintf("load builtin theme %q: %v", name, err))
	}
	return theme
}

// builtinThemePair resolves the built-in themes on first use, as theme.ts getBuiltinThemes reads them on demand; resolving them in package initialization would parse every theme color before main.
var builtinThemePair = sync.OnceValues(func() (dark, light *Theme) {
	return mustLoadBuiltinTheme("dark"), mustLoadBuiltinTheme("light")
})

func builtinDarkTheme() *Theme {
	dark, _ := builtinThemePair()
	return dark
}

func builtinLightTheme() *Theme {
	_, light := builtinThemePair()
	return light
}

// activeTheme is the current theme, nil until ActiveTheme first falls back to
// the built-in dark theme or SetTheme/SetThemeByName selects one. It is read on the hot render/highlight
// path from the main goroutine and background render workers, so stores and
// loads use an atomic pointer.
var activeTheme atomic.Pointer[Theme]

// ActiveTheme returns the current theme; before any selection it is the built-in dark theme.
func ActiveTheme() *Theme {
	if theme := activeTheme.Load(); theme != nil {
		return theme
	}
	activeTheme.CompareAndSwap(nil, builtinDarkTheme())
	return activeTheme.Load()
}

// GetResolvedThemeColors returns the resolved theme colors as CSS values keyed by token name: the concrete colors as hex, and tokens set to "" as the terminal's default colors (theme.ts getResolvedThemeColors). Used by HTML export to mirror upstream CSS variable generation.
func (t *Theme) GetResolvedThemeColors() map[string]string {
	if t == nil || t.fgAnsi == nil {
		return nil
	}
	values := t.Colors()
	css := make(map[string]string, len(values))
	for token, color := range values {
		css[token] = ColorToHex(color)
	}
	return css
}

// ColorKeys returns the color token names in upstream's Theme construction order.
// Used by HTML export to emit CSS variables in the same order as upstream.
func (t *Theme) ColorKeys() []string {
	if t == nil {
		return nil
	}
	return t.colorKeys
}

// SetTheme switches the active built-in theme. enableWatcher restarts the owned watcher; previews leave the current watch registration unchanged.
func SetTheme(name string, enableWatcher ...bool) {
	themeMutationMu.Lock()
	defer themeMutationMu.Unlock()
	setBuiltinTheme(name, len(enableWatcher) > 0 && enableWatcher[0])
}

func setBuiltinTheme(name string, enableWatcher bool) {
	switch name {
	case "light":
		storeActiveTheme(builtinLightTheme())
		noteSelectedTheme("light", enableWatcher)
	case "dark":
		storeActiveTheme(builtinDarkTheme())
		noteSelectedTheme("dark", enableWatcher)
	default:
		// The system theme is generated from the terminal's current colors each time it is selected.
		storeActiveTheme(createSystemTheme(currentColorMode()))
		noteSelectedTheme(SystemThemeName, false)
	}
}

// InMemoryThemeName is the name of a theme set directly from an instance (theme.ts setThemeInstance's currentThemeName).
const InMemoryThemeName = "<in-memory>"

// SetThemeInstance makes theme the active theme (theme.ts:795 setThemeInstance). An instance has no file to watch, so the owned watcher selects
// nothing for it.
func SetThemeInstance(theme *Theme) {
	themeMutationMu.Lock()
	defer themeMutationMu.Unlock()
	storeActiveTheme(theme)
	noteSelectedTheme(InMemoryThemeName, false)
}

// SetThemeByName activates a registered theme, or the system theme when it is absent or invalid (theme.ts setTheme). enableWatcher restarts the interactive owner's watch after a successful selection; it defaults to false for previews.
func SetThemeByName(name string, enableWatcher ...bool) {
	_ = SetThemeByNameChecked(name, enableWatcher...)
}

// SetThemeByNameChecked is SetThemeByName that reports why a theme could not be activated; the system theme is active then (theme.ts setTheme's `{ success: false, error }`).
func SetThemeByNameChecked(name string, enableWatcher ...bool) error {
	themeMutationMu.Lock()
	defer themeMutationMu.Unlock()
	watch := len(enableWatcher) > 0 && enableWatcher[0]
	if r := globalRegistry.Load(); r != nil {
		if t := r.Get(name); t != nil {
			storeActiveTheme(t)
			noteSelectedTheme(name, watch && name != SystemThemeName)
			return nil
		}
	} else if name == "dark" || name == "light" || name == SystemThemeName {
		setBuiltinTheme(name, watch && name != SystemThemeName)
		return nil
	}
	setBuiltinTheme(SystemThemeName, false)
	return fmt.Errorf("Theme not found: %s", name)
}

// globalRegistry holds the theme registry for /theme command. It is read on
// the render path and written by /theme setup, so it is stored behind an
// atomic pointer rather than a bare package variable (see activeTheme).
var globalRegistry atomic.Pointer[ThemeRegistry]

// ActiveThemeRegistry returns the global theme registry.
// Initializes with built-in themes on first call.
func ActiveThemeRegistry() *ThemeRegistry {
	if r := globalRegistry.Load(); r != nil {
		return r
	}
	created := NewThemeRegistry()
	if globalRegistry.CompareAndSwap(nil, created) {
		return created
	}
	return globalRegistry.Load()
}

// SetThemeRegistry sets the global theme registry.
func SetThemeRegistry(r *ThemeRegistry) {
	globalRegistry.Store(r)
}

// ParseAutoThemeSetting parses "lightTheme/darkTheme", trimming ECMAScript whitespace around each name. Empty or malformed values are not automatic.
func ParseAutoThemeSetting(themeSetting string) (lightTheme, darkTheme string, ok bool) {
	if themeSetting == "" {
		return "", "", false
	}
	slash := strings.Index(themeSetting, "/")
	if slash == -1 || strings.Contains(themeSetting[slash+1:], "/") {
		return "", "", false
	}
	lightTheme = widthx.JSTrim(themeSetting[:slash])
	darkTheme = widthx.JSTrim(themeSetting[slash+1:])
	if lightTheme == "" || darkTheme == "" {
		return "", "", false
	}
	return lightTheme, darkTheme, true
}

// ResolveThemeSettingPresence resolves a theme setting to the concrete theme
// name for the detected terminal appearance. A nil setting and a malformed
// slash value resolve to nothing; any other string, including an empty one,
// is a fixed name that resolves to itself.
func ResolveThemeSettingPresence(themeSetting *string, terminalTheme TerminalTheme) (string, bool) {
	if themeSetting == nil {
		return "", false
	}
	setting := *themeSetting
	if lightTheme, darkTheme, ok := ParseAutoThemeSetting(setting); ok {
		if terminalTheme == TerminalTheme("light") {
			return lightTheme, true
		}
		return darkTheme, true
	}
	if strings.Contains(setting, "/") {
		return "", false
	}
	return setting, true
}

// ResolveThemeSetting resolves a stored theme setting to the concrete theme
// name for the detected terminal appearance.
//
// Deprecated: use ResolveThemeSettingPresence; an empty string means no setting.
func ResolveThemeSetting(themeSetting string, terminalTheme TerminalTheme) (string, bool) {
	return ResolveThemeSettingPresence(themeSettingPresence(themeSetting), terminalTheme)
}

// SetThemeSettingPresence applies a theme setting before the terminal answers: the theme it resolves to for the terminal's appearance so far, or the system theme when there is no setting or it is malformed (theme.ts initTheme, interactive-mode.ts initTheme). A name that is not registered also selects the system theme.
func SetThemeSettingPresence(themeSetting *string) {
	if name, ok := ResolveThemeSettingPresence(themeSetting, GetTerminalTheme()); ok {
		SetThemeByName(name)
		return
	}
	SetThemeByName(SystemThemeName)
}

// SetThemeSetting applies a stored theme setting.
//
// Deprecated: use SetThemeSettingPresence; an empty string means no setting.
func SetThemeSetting(themeSetting string) {
	SetThemeSettingPresence(themeSettingPresence(themeSetting))
}

// themeSettingPresence maps the deprecated string form, where "" means no setting, to a presence-aware setting.
func themeSettingPresence(themeSetting string) *string {
	if themeSetting == "" {
		return nil
	}
	return &themeSetting
}

// ThemeRegistry holds all loaded themes and enables switching.
type ThemeRegistry struct {
	mu     sync.RWMutex
	themes map[string]*Theme
	names  []string // ordered list of theme names
	// paths records the file each JSON-loaded theme came from. Built-in
	// themes have no entry, which extensions report as an absent path.
	paths map[string]string
}

// NewThemeRegistry creates a registry with the system theme and the built-in dark and light themes.
func NewThemeRegistry() *ThemeRegistry {
	r := &ThemeRegistry{
		themes: make(map[string]*Theme),
	}
	r.themes["dark"] = builtinDarkTheme()
	r.themes["light"] = builtinLightTheme()
	r.names = []string{SystemThemeName, "dark", "light"}
	return r
}

// Add registers a theme. If a theme with the same name already exists, it is
// replaced. The name is derived from the theme's Name field.
func (r *ThemeRegistry) Add(t *Theme) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addLocked(t)
}

func (r *ThemeRegistry) addLocked(t *Theme) {
	name := t.Name
	// The system theme name is reserved: it takes precedence over custom themes of the same name (theme.ts loadTheme).
	if name == "" || name == SystemThemeName {
		return
	}
	if _, exists := r.themes[name]; !exists {
		r.names = append(r.names, name)
	}
	r.themes[name] = t
	delete(r.paths, name)
}

// AddFile registers a theme loaded from path and records path as its source,
// which the startup resource listing and getAllThemes report.
func (r *ThemeRegistry) AddFile(t *Theme, path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t.Name == "" || t.Name == SystemThemeName {
		return
	}
	r.addLocked(t)
	if r.paths == nil {
		r.paths = make(map[string]string)
	}
	r.paths[t.Name] = path
}

// Get returns a theme by name, or nil.
func (r *ThemeRegistry) Get(name string) *Theme {
	if name == SystemThemeName {
		return createSystemTheme(currentColorMode())
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.themes[name]
}

// PathOf returns the file a theme was loaded from, or "" for built-ins.
func (r *ThemeRegistry) PathOf(name string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.paths[name]
}

// Names returns a copy of the available theme names: the system theme first, then the others in localeCompare order (theme.ts getAvailableThemesWithPaths).
func (r *ThemeRegistry) Names() []string {
	r.mu.RLock()
	names := slices.Clone(r.names)
	r.mu.RUnlock()
	// The system theme comes first: it is the default and adapts to every terminal. The others follow in localeCompare order (theme.ts getAvailableThemesWithPaths).
	collator := collate.New(language.Und)
	slices.SortStableFunc(names, func(a, b string) int {
		switch {
		case a == SystemThemeName:
			return -1
		case b == SystemThemeName:
			return 1
		}
		return collator.CompareString(a, b)
	})
	return names
}

// LoadDir scans a directory for .json theme files and registers them,
// recording each theme's source file so extensions can report it.
func (r *ThemeRegistry) LoadDir(dir string) error {
	themes, err := LoadThemeDir(dir)
	if err != nil {
		return err
	}
	for _, fileName := range slices.Sorted(maps.Keys(themes)) {
		r.AddFile(themes[fileName], filepath.Join(dir, fileName+".json"))
	}
	return nil
}

// ThemeTokenValue is one theme token with its color, in the order the Theme constructor takes them (theme.ts fgColors and bgColors records).
type ThemeTokenValue struct {
	Token string
	Value ThemeColorValue
}

// ThemeOptions are the Theme constructor's options (theme.ts Theme constructor `options`).
type ThemeOptions struct {
	Name       string
	SourcePath string
	// SourceInfo is where the theme came from; the resource loader also sets it after loading (theme.ts Theme.sourceInfo).
	SourceInfo *source.SourceInfo
	Appearance ThemeAppearance
	// Dim lists the foreground tokens to render faint (SGR 2).
	Dim []string
}

// resolvedThemeColors caches ColorValues by the identity of the terminal colors it was resolved against.
type resolvedThemeColors struct {
	terminal *TerminalColors
	colors   map[string]Color
}

// guessedDefaultColors are the terminal default colors assumed when the terminal does not report them.
func guessedDefaultColors(appearance ThemeAppearance) (foreground, background Color) {
	if appearance == "light" {
		return RgbColorValue{}, RgbColorValue{R: 255, G: 255, B: 255}
	}
	return RgbColorValue{R: 0xe5, G: 0xe5, B: 0xe7}, RgbColorValue{}
}

// averageLightness is the mean OKLCH lightness of the colors, ignoring palette colors 0-15, which follow the user's terminal palette and say nothing about the theme.
func averageLightness(colors []Color) (float64, bool) {
	sum, count := 0.0, 0
	for _, color := range colors {
		if indexed, ok := color.(IndexedColor); ok && indexed.Index < 16 {
			continue
		}
		sum += ColorToOklch(color).L
		count++
	}
	if count == 0 {
		return 0, false
	}
	return sum / float64(count), true
}

// detectAppearance detects the background a theme is designed for from the lightness of its own colors, or "" when they say nothing.
func detectAppearance(foregrounds, backgrounds []Color) TerminalTheme {
	fg, hasFg := averageLightness(foregrounds)
	bg, hasBg := averageLightness(backgrounds)
	pick := func(dark bool) TerminalTheme {
		if dark {
			return "dark"
		}
		return "light"
	}
	switch {
	case hasFg && hasBg:
		return pick(bg < fg)
	case hasBg:
		return pick(bg < 0.5)
	case hasFg:
		return pick(fg > 0.5)
	}
	return ""
}

// NewTheme is the theme.ts Theme constructor: a token set to "" has no color of its own and follows the terminal's default colors.
func NewTheme(foregrounds, backgrounds []ThemeTokenValue, mode TerminalColorMode, options ThemeOptions) (*Theme, error) {
	t := &Theme{
		Name:           options.Name,
		SourcePath:     options.SourcePath,
		SourceInfo:     options.SourceInfo,
		mode:           mode,
		fgAnsi:         make(map[string]string, len(foregrounds)),
		bgAnsi:         make(map[string]string, len(backgrounds)),
		concreteColors: make(map[string]Color, len(foregrounds)+len(backgrounds)),
		dimTokens:      make(map[string]bool, len(options.Dim)),
	}
	for _, token := range options.Dim {
		t.dimTokens[token] = true
	}
	var concreteForegrounds, concreteBackgrounds []Color
	var concreteKeys []string
	add := func(entry ThemeTokenValue, isBackground bool) error {
		value := entry.Value
		var ansi string
		switch {
		case !value.isSet:
			return fmt.Errorf("Invalid color value: undefined")
		case !value.IsIndex && value.Text == "":
			if isBackground {
				t.defaultBackgroundTokens = append(t.defaultBackgroundTokens, entry.Token)
				ansi = "\x1b[49m"
			} else {
				t.defaultForegroundTokens = append(t.defaultForegroundTokens, entry.Token)
				ansi = "\x1b[39m"
			}
		default:
			var color Color
			if value.IsIndex {
				indexed, err := NewIndexedColor(value.Index)
				if err != nil {
					return err
				}
				color = indexed
			} else {
				parsed, err := ParseColor(value.Text)
				if err != nil {
					return err
				}
				color = parsed
			}
			t.concreteColors[entry.Token] = color
			concreteKeys = append(concreteKeys, entry.Token)
			if isBackground {
				concreteBackgrounds = append(concreteBackgrounds, color)
				ansi = BackgroundAnsi(color, mode)
			} else {
				concreteForegrounds = append(concreteForegrounds, color)
				ansi = ForegroundAnsi(color, mode)
			}
		}
		if isBackground {
			t.bgAnsi[entry.Token] = ansi
		} else {
			t.fgAnsi[entry.Token] = ansi
		}
		return nil
	}
	for _, entry := range foregrounds {
		if err := add(entry, false); err != nil {
			return nil, err
		}
	}
	for _, entry := range backgrounds {
		if err := add(entry, true); err != nil {
			return nil, err
		}
	}
	t.colorKeys = slices.Concat(concreteKeys, t.defaultForegroundTokens, t.defaultBackgroundTokens)
	t.ownAppearance = options.Appearance
	if t.ownAppearance == "" {
		t.ownAppearance = detectAppearance(concreteForegrounds, concreteBackgrounds)
	}
	t.populateFields()
	return t, nil
}

// populateFields fills the exported escape fields from the token tables.
func (t *Theme) populateFields() {
	for token, dst := range map[string]*string{
		"accent": &t.Accent, "success": &t.Success, "error": &t.Error, "warning": &t.Warning,
		"muted": &t.Muted, "dim": &t.Dim, "text": &t.Text,
		"border": &t.Border, "borderAccent": &t.BorderAccent, "borderMuted": &t.BorderMuted,
		"userMessageText": &t.UserMessageText, "toolTitle": &t.ToolTitle, "toolOutput": &t.ToolOutput,
		"customMessageText": &t.CustomMessageText, "customMessageLabel": &t.CustomMessageLabel,
		"mdHeading": &t.MDHeading, "mdLink": &t.MDLink, "mdLinkUrl": &t.MDLinkUrl, "mdCode": &t.MDCode,
		"mdCodeBlock": &t.MDCodeBlock, "mdCodeBlockBorder": &t.MDCodeBlockBorder, "mdQuote": &t.MDQuote,
		"mdQuoteBorder": &t.MDQuoteBorder, "mdHr": &t.MDHr, "mdListBullet": &t.MDListBullet,
		"toolDiffAdded": &t.ToolDiffAdded, "toolDiffRemoved": &t.ToolDiffRemoved, "toolDiffContext": &t.ToolDiffContext,
		"syntaxComment": &t.SyntaxComment, "syntaxKeyword": &t.SyntaxKeyword, "syntaxFunction": &t.SyntaxFunction,
		"syntaxVariable": &t.SyntaxVariable, "syntaxString": &t.SyntaxString, "syntaxNumber": &t.SyntaxNumber,
		"syntaxType": &t.SyntaxType, "syntaxOperator": &t.SyntaxOperator, "syntaxPunctuation": &t.SyntaxPunctuation,
		"thinkingText": &t.ThinkingText, "thinkingOff": &t.ThinkingOff, "thinkingMinimal": &t.ThinkingMinimal,
		"thinkingLow": &t.ThinkingLow, "thinkingMedium": &t.ThinkingMedium, "thinkingHigh": &t.ThinkingHigh,
		"thinkingXhigh": &t.ThinkingXhigh, "bashMode": &t.BashMode,
	} {
		*dst = t.GetFgAnsi(token)
	}
	for token, dst := range map[string]*string{
		"userMessageBg": &t.UserMessageBg, "toolPendingBg": &t.ToolPendingBg, "toolSuccessBg": &t.ToolSuccessBg,
		"toolErrorBg": &t.ToolErrorBg, "selectedBg": &t.SelectedBg, "customMessageBg": &t.CustomMessageBg,
	} {
		*dst = t.GetBgAnsi(token)
	}
	t.BgClose = SGRBgReset
	t.Reset = SGRResetAll
}

// ThemeAppearance is the background a theme is designed for (theme.ts:186 `type ThemeAppearance = TerminalTheme`).
type ThemeAppearance = TerminalTheme

// Appearance is the background the theme is designed for: declared in the theme JSON, detected from its colors, or, for a theme without usable colors, the terminal's appearance.
func (t *Theme) Appearance() ThemeAppearance {
	if t.ownAppearance != "" {
		return t.ownAppearance
	}
	return GetTerminalTheme()
}

// Colors returns a concrete color for every token. Tokens set to "" (terminal default) use the terminal's reported default colors, or a guess based on the appearance when the terminal did not report them. Faint tokens are approximated by mixing their color toward the background. The map is shared and must not be modified.
func (t *Theme) Colors() map[string]Color {
	terminal := currentTerminalColors()
	if cached := t.resolvedColorSet.Load(); cached != nil && cached.terminal == terminal {
		return cached.colors
	}
	guessForeground, guessBackground := guessedDefaultColors(t.Appearance())
	foreground, background := guessForeground, guessBackground
	if terminal.Foreground != nil {
		foreground = RgbColorValue(*terminal.Foreground)
	}
	if terminal.Background != nil {
		background = RgbColorValue(*terminal.Background)
	}
	colors := maps.Clone(t.concreteColors)
	for _, token := range t.defaultForegroundTokens {
		colors[token] = foreground
	}
	for _, token := range t.defaultBackgroundTokens {
		colors[token] = background
	}
	for token := range t.dimTokens {
		if color, ok := colors[token]; ok {
			if mixed, err := MixColors(color, background, 0.4, ColorMixSpaceOklch); err == nil {
				colors[token] = mixed
			}
		}
	}
	resolved := &resolvedThemeColors{terminal: terminal, colors: colors}
	t.resolvedColorSet.Store(resolved)
	return colors
}

// createSystemTheme generates the system theme from the terminal's reported colors (grayscale while they are pending).
func createSystemTheme(mode TerminalColorMode) *Theme {
	terminal := currentTerminalColors()
	saturation := 1.0
	if terminalColorsPending.Load() {
		saturation = 0
	}
	generated := GenerateSystemThemeColors(SystemThemeInput{
		Foreground: terminal.Foreground, Background: terminal.Background, Palette: terminal.Palette,
		Saturation: &saturation, AppearanceHint: GetTerminalTheme(),
	})
	foregrounds, backgrounds := splitThemeTokenValues(generated.Colors)
	theme, err := NewTheme(foregrounds, backgrounds, mode, ThemeOptions{Name: SystemThemeName, Appearance: generated.Appearance, Dim: generated.Dim})
	if err != nil {
		panic(fmt.Sprintf("generate system theme: %v", err))
	}
	return theme
}

// splitThemeTokenValues splits generated colors into foreground and background tokens in the recipe's order, with the optional tokens' fallbacks the Theme constructor applies.
func splitThemeTokenValues(colors map[string]ThemeColorValue) (foregrounds, backgrounds []ThemeTokenValue) {
	for _, entry := range systemTokenFamilies {
		value, ok := colors[entry.token]
		if !ok {
			continue
		}
		if themeBackgroundTokens[entry.token] {
			backgrounds = append(backgrounds, ThemeTokenValue{entry.token, value})
		} else {
			foregrounds = append(foregrounds, ThemeTokenValue{entry.token, value})
		}
	}
	return foregrounds, backgrounds
}

// FgClose is the sequence that ends a foreground opened by prefix, a Theme.Fg result: a dim token sets faint too, so it closes both (theme.ts fg of a dim token, `\x1b[2m...\x1b[22;39m`).
func FgClose(prefix string) string {
	if strings.HasSuffix(prefix, "\x1b[2m") {
		return "\x1b[22;39m"
	}
	return SGRFgReset
}

// ThemeByName is theme.ts getThemeByName: the registered theme with the name, or nil when there is none.
func ThemeByName(name string) *Theme {
	return ActiveThemeRegistry().Get(name)
}

// ExportThemeNamed is the theme HTML export reads for an export theme name (theme.ts getResolvedThemeColors(themeName)): the named
// theme, or [ExportTheme] when the name is empty or names no theme.
func ExportThemeNamed(name string) *Theme {
	if name != "" {
		if t := ThemeByName(name); t != nil {
			return t
		}
	}
	return ExportTheme()
}

// ExportTheme is the theme HTML export reads: the selected theme, or a generated system theme until one is selected (theme.ts getResolvedThemeColors).
func ExportTheme() *Theme {
	if selectedThemeName.Load() == nil {
		return createSystemTheme(currentColorMode())
	}
	return ActiveTheme()
}
