package tui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
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

	// colors is a map of all resolved colors keyed by token name.
	// Used by Fg() and Bg() for dynamic lookup.
	colors map[string]ThemeColorValue
	// colorKeys preserves the insertion order of color tokens from the
	// theme JSON file. Upstream JS iterates Object.entries(colors) which
	// preserves insertion order; Go maps are unordered. The HTML export
	// emits CSS vars in this order for byte-exact parity.
	colorKeys []string

	// mode is the color mode the ANSI fields were built for (theme.ts
	// Theme.mode). The zero value is truecolor.
	mode ColorMode
	// source is the theme JSON this theme was resolved from; it rebuilds the
	// theme in the other color mode. Nil for a theme assembled in code.
	source *ThemeJSON
}

// ColorMode mirrors theme.ts Theme.getColorMode.
func (t *Theme) ColorMode() ColorMode {
	if t.mode == "" {
		return ColorModeTrueColor
	}
	return t.mode
}

// WithColorMode returns this theme resolved in mode, mirroring theme.ts createTheme(themeJson, mode). Rebuilt themes are not cached, so obsolete themes can be collected. A theme without JSON source is returned unchanged.
func (t *Theme) WithColorMode(mode ColorMode) *Theme {
	if t == nil || t.ColorMode() == mode || t.source == nil {
		return t
	}
	variant, err := resolveThemeWithMode(cloneThemeJSON(t.source), mode)
	if err != nil {
		return t
	}
	return variant
}

// currentColorMode mirrors createTheme's default mode:
// getCapabilities().trueColor ? "truecolor" : "256color".
func currentColorMode() ColorMode {
	if GetCapabilities().TrueColor {
		return ColorModeTrueColor
	}
	return ColorMode256
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
	if t := ActiveTheme(); t != nil {
		storeActiveTheme(t)
	}
}

// ThemeHexFg returns the active theme's foreground escape for a fixed hex
// color, in the theme's color mode.
func ThemeHexFg(hex string) string { return hexFgANSI(hex, ActiveTheme().ColorMode()) }

// ThemeHexBg returns the active theme's background escape for a fixed hex
// color, in the theme's color mode.
func ThemeHexBg(hex string) string { return hexBgANSI(hex, ActiveTheme().ColorMode()) }

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

type RgbColor struct {
	R int
	G int
	B int
}

type TerminalThemeDetection struct {
	Theme      TerminalTheme
	Source     string
	Detail     string
	Confidence string
}

type TerminalThemeDetectionOptions struct {
	Env map[string]string
}

var osc11BackgroundColorPattern = regexp.MustCompile(`^\x1b\]11;([^\x07\x1b]*)(?:\x07|\x1b\\)$`)

// Fg returns the ANSI foreground escape for a named color token.
// Mirrors upstream theme.fg(tokenName, text).
// Returns empty string if token not found (terminal default).
func (t *Theme) Fg(token string) string {
	return themeFgANSI(t.colors[token], t.ColorMode())
}

// Bg returns the ANSI background escape for a named color token.
// Mirrors upstream theme.bg(tokenName, text).
func (t *Theme) Bg(token string) string {
	return themeBgANSI(t.colors[token], t.ColorMode())
}

// ANSIPalette returns every resolved token as foreground and background ANSI
// openings. It is used at process boundaries where theme helper functions
// cannot cross but their current immutable token table can.
func (t *Theme) ANSIPalette() (map[string]string, map[string]string) {
	fg := make(map[string]string, len(t.colorKeys))
	bg := make(map[string]string, len(t.colorKeys))
	for _, token := range t.colorKeys {
		fg[token] = t.Fg(token)
		bg[token] = t.Bg(token)
	}
	return fg, bg
}

// FgText returns text wrapped in the foreground color and reset.
// Mirrors upstream theme.fg(tokenName, text).
func (t *Theme) FgText(token, text string) string {
	esc := t.Fg(token)
	if esc == "" {
		return text
	}
	return esc + text + SGRFgReset
}

// Inverse wraps text in reverse video. Mirrors upstream theme.inverse().
func (t *Theme) Inverse(text string) string {
	return "\x1b[7m" + text + SGRInverseReset
}

// Built-in production themes are resolved from the embedded pinned JSON so
// fields used directly by components and dynamic color maps share one source.
func mustLoadBuiltinTheme(name string) *Theme {
	theme, err := LoadBuiltinTheme(name)
	if err != nil {
		panic(fmt.Sprintf("load builtin theme %q: %v", name, err))
	}
	return theme
}

var (
	darkTheme  = mustLoadBuiltinTheme("dark")
	lightTheme = mustLoadBuiltinTheme("light")
)

// activeTheme is the current theme, set once at startup by DetectTheme() and
// swapped by SetTheme/SetThemeByName. It is read on the hot render/highlight
// path from the main goroutine and background render workers, so stores and
// loads use an atomic pointer.
var activeTheme atomic.Pointer[Theme]

func init() {
	activeTheme.Store(darkTheme)
}

// ActiveTheme returns the current theme.
func ActiveTheme() *Theme { return activeTheme.Load() }

// Colors returns the resolved theme colors as CSS values keyed by token name,
// with 256-color indexes converted to hex and terminal-default colors mapped
// to Pi's light/dark HTML fallback (theme.ts getResolvedThemeColors).
// Used by HTML export to mirror upstream CSS variable generation.
func (t *Theme) Colors() map[string]string {
	if t == nil || t.colors == nil {
		return nil
	}
	defaultText := "#e5e5e7"
	if t.Name == "light" {
		defaultText = "#000000"
	}
	css := make(map[string]string, len(t.colors))
	for token, value := range t.colors {
		css[token] = value.cssColor(defaultText)
	}
	return css
}

// ColorKeys returns the color token names in their original JSON insertion order.
// Used by HTML export to emit CSS variables in the same order as upstream.
func (t *Theme) ColorKeys() []string {
	if t == nil {
		return nil
	}
	return t.colorKeys
}

// SetTheme switches the active theme by name ("dark" or "light").
func SetTheme(name string) {
	switch name {
	case "light":
		storeActiveTheme(lightTheme)
	default:
		storeActiveTheme(darkTheme)
	}
}

// SetThemeByName switches the active theme using the global registry.
// Falls back to the built-in dark theme if name is not found.
func SetThemeByName(name string) {
	if r := globalRegistry.Load(); r != nil {
		if t := r.Get(name); t != nil {
			storeActiveTheme(t)
			return
		}
	}
	SetTheme(name)
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

// DetectTheme auto-detects dark/light mode and sets the active theme.
// Uses COLORFGBG env var (same heuristic as upstream theme.ts).
// Falls back to dark if detection fails.
func DetectTheme() {
	SetTheme(string(DetectTerminalBackground(TerminalThemeDetectionOptions{}).Theme))
}

// ParseAutoThemeSetting parses the upstream automatic theme setting format
// "lightTheme/darkTheme". Empty or malformed values are not automatic.
func ParseAutoThemeSetting(themeSetting string) (lightTheme, darkTheme string, ok bool) {
	if themeSetting == "" {
		return "", "", false
	}
	slash := strings.Index(themeSetting, "/")
	if slash == -1 || strings.Contains(themeSetting[slash+1:], "/") {
		return "", "", false
	}
	lightTheme = strings.TrimSpace(themeSetting[:slash])
	darkTheme = strings.TrimSpace(themeSetting[slash+1:])
	if lightTheme == "" || darkTheme == "" {
		return "", "", false
	}
	return lightTheme, darkTheme, true
}

// ResolveThemeSetting resolves a stored theme setting to the concrete theme
// name for the detected terminal appearance. Non-automatic settings resolve to
// themselves unless they are malformed slash values.
func ResolveThemeSetting(themeSetting string, terminalTheme TerminalTheme) (string, bool) {
	if lightTheme, darkTheme, ok := ParseAutoThemeSetting(themeSetting); ok {
		if terminalTheme == TerminalTheme("light") {
			return lightTheme, true
		}
		return darkTheme, true
	}
	if strings.Contains(themeSetting, "/") {
		return "", false
	}
	if themeSetting == "" {
		return "", false
	}
	return themeSetting, true
}

// SetThemeSetting applies a stored theme setting. An empty setting means
// automatic built-in dark/light detection; a light/dark automatic setting may
// resolve to any registered theme name.
func SetThemeSetting(themeSetting string) {
	if themeSetting == "" {
		DetectTheme()
		return
	}
	detected := DetectTerminalBackground(TerminalThemeDetectionOptions{}).Theme
	if name, ok := ResolveThemeSetting(themeSetting, detected); ok {
		SetThemeByName(name)
		return
	}
	SetThemeByName(themeSetting)
}

func getColorFgBgBackgroundIndex(colorfgbg string) (int, bool) {
	parts := strings.Split(colorfgbg, ";")
	for _, part := range slices.Backward(parts) {
		bg, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && bg >= 0 && bg <= 255 {
			return bg, true
		}
	}
	return 0, false
}

func getRgbColorLuminance(rgb RgbColor) float64 {
	toLinear := func(channel int) float64 {
		value := float64(channel) / 255
		if value <= 0.03928 {
			return value / 12.92
		}
		return math.Pow((value+0.055)/1.055, 2.4)
	}
	return 0.2126*toLinear(rgb.R) + 0.7152*toLinear(rgb.G) + 0.0722*toLinear(rgb.B)
}

func getAnsiColorLuminance(index int) float64 {
	r, g, b, ok := parseHex(ansi256ToHex(index))
	if !ok {
		return 0
	}
	return getRgbColorLuminance(RgbColor{R: r, G: g, B: b})
}

func GetThemeForRgbColor(rgb RgbColor) TerminalTheme {
	if getRgbColorLuminance(rgb) >= 0.5 {
		return TerminalTheme("light")
	}
	return TerminalTheme("dark")
}

func parseOscHexChannel(channel string) (int, bool) {
	if channel == "" {
		return 0, false
	}
	for _, r := range channel {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return 0, false
		}
	}
	value, err := strconv.ParseInt(channel, 16, 64)
	if err != nil {
		return 0, false
	}
	maxValue := math.Pow(16, float64(len(channel))) - 1
	if maxValue <= 0 {
		return 0, false
	}
	return int(math.Round((float64(value) / maxValue) * 255)), true
}

func ParseOsc11BackgroundColor(data string) *RgbColor {
	match := osc11BackgroundColorPattern.FindStringSubmatch(data)
	if match == nil {
		return nil
	}

	value := strings.TrimSpace(match[1])
	if after, ok := strings.CutPrefix(value, "#"); ok {
		hex := after
		switch len(hex) {
		case 6:
			r, g, b, ok := parseHex(hex)
			if !ok {
				return nil
			}
			return &RgbColor{R: r, G: g, B: b}
		case 12:
			r, okR := parseOscHexChannel(hex[0:4])
			g, okG := parseOscHexChannel(hex[4:8])
			b, okB := parseOscHexChannel(hex[8:12])
			if !okR || !okG || !okB {
				return nil
			}
			return &RgbColor{R: r, G: g, B: b}
		default:
			return nil
		}
	}

	rgbValue := value
	if stripped, ok := strings.CutPrefix(strings.ToLower(rgbValue), "rgb:"); ok {
		rgbValue = stripped
	} else if stripped, ok := strings.CutPrefix(strings.ToLower(rgbValue), "rgba:"); ok {
		rgbValue = stripped
	}
	parts := strings.Split(rgbValue, "/")
	if len(parts) != 3 {
		return nil
	}
	r, okR := parseOscHexChannel(parts[0])
	g, okG := parseOscHexChannel(parts[1])
	b, okB := parseOscHexChannel(parts[2])
	if !okR || !okG || !okB {
		return nil
	}
	return &RgbColor{R: r, G: g, B: b}
}

func DetectTerminalBackground(options TerminalThemeDetectionOptions) TerminalThemeDetection {
	env := options.Env
	if env == nil {
		env = map[string]string{"COLORFGBG": os.Getenv("COLORFGBG")}
	}
	colorfgbg := env["COLORFGBG"]
	if bg, ok := getColorFgBgBackgroundIndex(colorfgbg); ok {
		theme := TerminalTheme("dark")
		if getAnsiColorLuminance(bg) >= 0.5 {
			theme = TerminalTheme("light")
		}
		return TerminalThemeDetection{
			Theme:      theme,
			Source:     "COLORFGBG",
			Detail:     fmt.Sprintf("background color index %d", bg),
			Confidence: "high",
		}
	}
	return TerminalThemeDetection{
		Theme:      TerminalTheme("dark"),
		Source:     "fallback",
		Detail:     "no terminal background hint found",
		Confidence: "low",
	}
}

func GetDefaultTheme() string {
	return string(DetectTerminalBackground(TerminalThemeDetectionOptions{}).Theme)
}

func ansi256ToHex(index int) string {
	switch {
	case index < 0:
		index = 0
	case index > 255:
		index = 255
	}

	base := [16][3]int{
		{0, 0, 0}, {128, 0, 0}, {0, 128, 0}, {128, 128, 0},
		{0, 0, 128}, {128, 0, 128}, {0, 128, 128}, {192, 192, 192},
		{128, 128, 128}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0},
		{0, 0, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
	}
	if index < 16 {
		rgb := base[index]
		return fmt.Sprintf("#%02x%02x%02x", rgb[0], rgb[1], rgb[2])
	}
	if index >= 232 {
		level := 8 + (index-232)*10
		return fmt.Sprintf("#%02x%02x%02x", level, level, level)
	}
	idx := index - 16
	levels := []int{0, 95, 135, 175, 215, 255}
	r := levels[idx/36]
	g := levels[(idx/6)%6]
	b := levels[idx%6]
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

// ThemeRegistry holds all loaded themes and enables switching.
type ThemeRegistry struct {
	themes map[string]*Theme
	names  []string // ordered list of theme names
	// paths records the file each JSON-loaded theme came from. Built-in
	// themes have no entry, which extensions report as an absent path.
	paths map[string]string
}

// NewThemeRegistry creates a registry with the built-in dark and light themes.
func NewThemeRegistry() *ThemeRegistry {
	r := &ThemeRegistry{
		themes: make(map[string]*Theme),
	}
	r.themes["dark"] = darkTheme
	r.themes["light"] = lightTheme
	r.names = []string{"dark", "light"}
	return r
}

// Add registers a theme. If a theme with the same name already exists, it is
// replaced. The name is derived from the theme's Name field.
func (r *ThemeRegistry) Add(t *Theme) {
	name := t.Name
	if name == "" {
		return
	}
	if _, exists := r.themes[name]; !exists {
		r.names = append(r.names, name)
	}
	r.themes[name] = t
}

// Get returns a theme by name, or nil.
func (r *ThemeRegistry) Get(name string) *Theme { return r.themes[name] }

// PathOf returns the file a theme was loaded from, or "" for built-ins.
func (r *ThemeRegistry) PathOf(name string) string { return r.paths[name] }

// Names returns the list of available theme names.
func (r *ThemeRegistry) Names() []string { return r.names }

// LoadDir scans a directory for .json theme files and registers them,
// recording each theme's source file so extensions can report it.
func (r *ThemeRegistry) LoadDir(dir string) error {
	themes, err := LoadThemeDir(dir)
	if err != nil {
		return err
	}
	for name, t := range themes {
		r.Add(t)
		if r.paths == nil {
			r.paths = make(map[string]string)
		}
		r.paths[name] = filepath.Join(dir, name+".json")
	}
	return nil
}
