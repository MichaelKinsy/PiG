// Package theme is p2's TUI theme: coding-agent's Theme class, color resolution and appearance made per-instance
// (p2-tui-port.md section 5.3): no fs, watchers, process globals, registries or env. Terminal colors and the color mode are constructor data
// supplied by the launcher; built-in themes are embedded documents.
//
// pi: apps/p2/src/tui/theme/theme.ts
package theme

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/tui"
)

// SystemThemeName is system-theme.ts SYSTEM_THEME_NAME.
const SystemThemeName = "system"

// TerminalTheme and ThemeAppearance are theme.ts TerminalTheme and ThemeAppearance: "dark" or "light".
type (
	TerminalTheme   = tui.TerminalTheme
	ThemeAppearance = tui.TerminalTheme
)

// TerminalState is what the launcher learned about the visitor's terminal (section 3.2 `terminalColors`).
type TerminalState struct {
	Colors tui.TerminalColors
	// Scheme is the light/dark report (mode 2031) or COLORFGBG, evaluated by the launcher; "" when none.
	Scheme TerminalTheme
	// Pending is true until the launcher's color query completed or timed out: the system theme renders in grayscale.
	Pending bool
}

// BuiltinThemes is theme.ts BUILTIN_THEMES.
func BuiltinTheme(name string) (ThemeJSON, bool) {
	switch name {
	case "dark":
		return DarkTheme, true
	case "light":
		return LightTheme, true
	}
	return ThemeJSON{}, false
}

// BuiltinThemeNames lists BUILTIN_THEMES' keys in declaration order.
func BuiltinThemeNames() []string { return []string{"dark", "light"} }

// StyleColor is a ThemeStyle color: a theme token (Token) or a concrete Color; the zero value is unset.
type StyleColor struct {
	Token string
	Color tui.Color
}

// Token is a StyleColor naming a theme token.
func Token(name string) StyleColor { return StyleColor{Token: name} }

// Concrete is a StyleColor of a concrete color.
func Concrete(color tui.Color) StyleColor { return StyleColor{Color: color} }

// ThemeStyle is theme.ts ThemeStyle.
type ThemeStyle struct {
	tui.TextAttributes
	Fg StyleColor
	Bg StyleColor
}

func (v ColorValue) isEmpty() bool { return !v.IsIndex && v.Text == "" }

func isHexOrOk(text string) bool {
	return strings.HasPrefix(text, "#") || strings.HasPrefix(strings.ToLower(text), "oklch(") || strings.HasPrefix(strings.ToLower(text), "okhsl(")
}

// resolveVarRefs is theme.ts resolveVarRefs: a variable reference resolves through vars; a cycle or a missing variable throws.
func resolveVarRefs(value ColorValue, vars map[string]ColorValue, visited map[string]bool) (ColorValue, error) {
	if value.IsIndex || value.Text == "" || isHexOrOk(value.Text) {
		return value, nil
	}
	if visited[value.Text] {
		return ColorValue{}, fmt.Errorf("Circular variable reference detected: %s", value.Text)
	}
	next, ok := vars[value.Text]
	if !ok {
		return ColorValue{}, fmt.Errorf("Variable reference not found: %s", value.Text)
	}
	if visited == nil {
		visited = map[string]bool{}
	}
	visited[value.Text] = true
	return resolveVarRefs(next, vars, visited)
}

// withThemeTokenFallbacks is theme.ts withThemeTokenFallbacks: an omitted optional token takes its fallback's value.
func withThemeTokenFallbacks(values OrderedColors) (map[string]ColorValue, error) {
	resolved := make(map[string]ColorValue, len(ThemeTokens))
	for _, entry := range values {
		resolved[entry.Name] = entry.Value
	}
	for _, descriptor := range ThemeTokens {
		if descriptor.Fallback == "" {
			continue
		}
		if _, ok := resolved[descriptor.Name]; ok {
			continue
		}
		fallback, ok := resolved[descriptor.Fallback]
		if !ok {
			return nil, fmt.Errorf("Theme token %s has unresolved fallback %s", descriptor.Name, descriptor.Fallback)
		}
		resolved[descriptor.Name] = fallback
	}
	return resolved, nil
}

// splitThemeColors is theme.ts splitThemeColors: foreground and background tokens, each in THEME_TOKENS order.
func splitThemeColors(colors map[string]ColorValue) (foregrounds, backgrounds OrderedColors) {
	for _, descriptor := range ThemeTokens {
		value, ok := colors[descriptor.Name]
		if !ok {
			continue
		}
		if descriptor.Slot == SlotBackground {
			backgrounds = append(backgrounds, NamedColor{Name: descriptor.Name, Value: value})
		} else {
			foregrounds = append(foregrounds, NamedColor{Name: descriptor.Name, Value: value})
		}
	}
	return foregrounds, backgrounds
}

func guessedDefaultColors(appearance ThemeAppearance) (foreground, background tui.Color) {
	if appearance == "light" {
		return tui.RgbColorValue{}, tui.RgbColorValue{R: 255, G: 255, B: 255}
	}
	return tui.RgbColorValue{R: 0xe5, G: 0xe5, B: 0xe7}, tui.RgbColorValue{}
}

func averageLightness(colors []tui.Color) (float64, bool) {
	sum, count := 0.0, 0
	for _, color := range colors {
		if indexed, ok := color.(tui.IndexedColor); ok && indexed.Index < 16 {
			continue
		}
		sum += tui.ColorToOklch(color).L
		count++
	}
	if count == 0 {
		return 0, false
	}
	return sum / float64(count), true
}

func detectAppearance(foregrounds, backgrounds []tui.Color) ThemeAppearance {
	fg, hasFg := averageLightness(foregrounds)
	bg, hasBg := averageLightness(backgrounds)
	pick := func(dark bool) ThemeAppearance {
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

const textMinimumWcagContrast = 4.5

// terminalAppearance is system-theme.ts terminalAppearance: whether a terminal is dark or light, from its reported colors: the direction of its own
// foreground when text can be readable that way, otherwise dark when white text has more contrast on the background than black text.
func terminalAppearance(background tui.RgbColor, foreground *tui.RgbColor) ThemeAppearance {
	white := tui.RgbColor{R: 255, G: 255, B: 255}
	black := tui.RgbColor{}
	whiteContrast := tui.WcagContrast(white, background)
	blackContrast := tui.WcagContrast(black, background)
	if foreground != nil {
		lightness := func(color tui.RgbColor) float64 { return tui.ColorToOklch(tui.RgbColorValue(color)).L }
		foregroundL, backgroundL := lightness(*foreground), lightness(background)
		if math.Abs(foregroundL-backgroundL) > 0.05 {
			appearance, best := ThemeAppearance("light"), blackContrast
			if foregroundL > backgroundL {
				appearance, best = "dark", whiteContrast
			}
			if best >= textMinimumWcagContrast {
				return appearance
			}
		}
	}
	if whiteContrast >= blackContrast {
		return "dark"
	}
	return "light"
}

// DetectTerminalTheme is detectTerminalTheme: dark or light from the terminal's background, then its scheme report, then dark.
func DetectTerminalTheme(terminal TerminalState) TerminalTheme {
	if background := terminal.Colors.Background; background != nil {
		return terminalAppearance(*background, terminal.Colors.Foreground)
	}
	if terminal.Scheme != "" {
		return terminal.Scheme
	}
	return "dark"
}

// scopeStyles maps highlight.js scopes to syntax* tokens (or an attribute), mirroring coding-agent's cli-highlight theme (theme.ts SCOPES).
var scopeStyles = map[string]string{
	"keyword": "syntaxKeyword", "built_in": "syntaxType", "literal": "syntaxNumber", "number": "syntaxNumber", "regexp": "syntaxString",
	"string": "syntaxString", "subst": "text", "comment": "syntaxComment", "doctag": "syntaxComment", "meta": "muted",
	"function": "syntaxFunction", "title": "syntaxFunction", "class": "syntaxType", "type": "syntaxType", "tag": "syntaxPunctuation",
	"name": "syntaxKeyword", "attr": "syntaxVariable", "variable": "syntaxVariable", "params": "syntaxVariable", "operator": "syntaxOperator",
	"punctuation": "syntaxPunctuation", "emphasis": "italic", "strong": "bold", "link": "underline", "addition": "toolDiffAdded",
	"deletion": "toolDiffRemoved",
}

var languageAliases = map[string]string{"mjs": "js", "cjs": "js", "mts": "ts", "cts": "ts", "jsonc": "json", "zsh": "bash"}

// Options are the Theme constructor's options (theme.ts Theme constructor `options`).
type Options struct {
	Mode       tui.TerminalColorMode
	Terminal   TerminalState
	Name       string
	Appearance ThemeAppearance
	// Dim lists the foreground tokens to render faint (SGR 2).
	Dim []string
}

// Theme is theme.ts Theme: one instance's resolved colors for one terminal.
type Theme struct {
	// Name is the theme's name, "" for an unnamed one.
	Name     string
	Mode     tui.TerminalColorMode
	Terminal TerminalState

	fgAnsi, bgAnsi                                   map[string]string
	concreteColors                                   map[string]tui.Color
	defaultForegroundTokens, defaultBackgroundTokens []string
	dimTokens                                        map[string]bool
	ownAppearance                                    ThemeAppearance
	resolvedColors                                   map[string]tui.Color
}

// NewTheme is the Theme constructor. Both slices are the already split foreground and background token values (splitThemeColors).
func NewTheme(fgColors, bgColors OrderedColors, options Options) (*Theme, error) {
	t := &Theme{
		Name: options.Name, Mode: options.Mode, Terminal: options.Terminal,
		fgAnsi: map[string]string{}, bgAnsi: map[string]string{}, concreteColors: map[string]tui.Color{}, dimTokens: map[string]bool{},
	}
	for _, token := range options.Dim {
		t.dimTokens[token] = true
	}
	var concreteForegrounds, concreteBackgrounds []tui.Color
	add := func(entry NamedColor, isBackground bool) error {
		var ansi string
		if entry.Value.isEmpty() {
			if isBackground {
				t.defaultBackgroundTokens = append(t.defaultBackgroundTokens, entry.Name)
				ansi = "\x1b[49m"
			} else {
				t.defaultForegroundTokens = append(t.defaultForegroundTokens, entry.Name)
				ansi = "\x1b[39m"
			}
		} else {
			var color tui.Color
			var err error
			if entry.Value.IsIndex {
				color, err = tui.NewIndexedColor(entry.Value.Index)
			} else {
				color, err = tui.ParseColor(entry.Value.Text)
			}
			if err != nil {
				return err
			}
			t.concreteColors[entry.Name] = color
			if isBackground {
				concreteBackgrounds = append(concreteBackgrounds, color)
				ansi = tui.BackgroundAnsi(color, t.Mode)
			} else {
				concreteForegrounds = append(concreteForegrounds, color)
				ansi = tui.ForegroundAnsi(color, t.Mode)
			}
		}
		if isBackground {
			t.bgAnsi[entry.Name] = ansi
		} else {
			t.fgAnsi[entry.Name] = ansi
		}
		return nil
	}
	for _, entry := range fgColors {
		if err := add(entry, false); err != nil {
			return nil, err
		}
	}
	for _, entry := range bgColors {
		if err := add(entry, true); err != nil {
			return nil, err
		}
	}
	t.ownAppearance = options.Appearance
	if t.ownAppearance == "" {
		t.ownAppearance = detectAppearance(concreteForegrounds, concreteBackgrounds)
	}
	return t, nil
}

// Appearance is declared, detected from the theme colors, or (for colorless themes) this instance's terminal appearance.
func (t *Theme) Appearance() ThemeAppearance {
	if t.ownAppearance != "" {
		return t.ownAppearance
	}
	return DetectTerminalTheme(t.Terminal)
}

// Colors are concrete colors for all tokens; "" tokens use this instance's terminal defaults (or a guess).
func (t *Theme) Colors() map[string]tui.Color {
	if t.resolvedColors != nil {
		return t.resolvedColors
	}
	guessForeground, guessBackground := guessedDefaultColors(t.Appearance())
	foreground, background := guessForeground, guessBackground
	if rgb := t.Terminal.Colors.Foreground; rgb != nil {
		foreground = tui.RgbColorValue(*rgb)
	}
	if rgb := t.Terminal.Colors.Background; rgb != nil {
		background = tui.RgbColorValue(*rgb)
	}
	colors := make(map[string]tui.Color, len(t.concreteColors)+len(t.defaultForegroundTokens)+len(t.defaultBackgroundTokens))
	for token, color := range t.concreteColors {
		colors[token] = color
	}
	for _, token := range t.defaultForegroundTokens {
		colors[token] = foreground
	}
	for _, token := range t.defaultBackgroundTokens {
		colors[token] = background
	}
	for token := range t.dimTokens {
		if color, ok := colors[token]; ok {
			if mixed, err := tui.MixColors(color, background, 0.4, tui.ColorMixSpaceOklch); err == nil {
				colors[token] = mixed
			}
		}
	}
	t.resolvedColors = colors
	return colors
}

func tokenAnsi(ansi map[string]string, token string) string {
	value, ok := ansi[token]
	if !ok {
		panic(fmt.Errorf("Unknown theme color: %s", token))
	}
	return value
}

// Style is theme.ts Theme.style: text with the attributes and colors (tokens or concrete colors) of options.
func (t *Theme) Style(text string, options ThemeStyle) string {
	if options.Fg.Token != "" && t.dimTokens[options.Fg.Token] {
		options.Dim = true
	}
	var fgAnsi, bgAnsi string
	switch {
	case options.Fg.Token != "":
		fgAnsi = tokenAnsi(t.fgAnsi, options.Fg.Token)
	case options.Fg.Color != nil:
		fgAnsi = tui.ForegroundAnsi(options.Fg.Color, t.Mode)
	}
	switch {
	case options.Bg.Token != "":
		bgAnsi = tokenAnsi(t.bgAnsi, options.Bg.Token)
	case options.Bg.Color != nil:
		bgAnsi = tui.BackgroundAnsi(options.Bg.Color, t.Mode)
	}
	return tui.StyleTextWithAnsi(text, fgAnsi, bgAnsi, options.TextAttributes)
}

// Fg is theme.ts Theme.fg. An unknown token panics with `Unknown theme color: <token>`, as the TypeScript throws.
func (t *Theme) Fg(color ThemeColor, text string) string {
	ansi := tokenAnsi(t.fgAnsi, color)
	if t.dimTokens[color] {
		return ansi + "\x1b[2m" + text + "\x1b[22;39m"
	}
	return ansi + text + "\x1b[39m"
}

// Bg is theme.ts Theme.bg.
func (t *Theme) Bg(color ThemeBg, text string) string {
	return tokenAnsi(t.bgAnsi, color) + text + "\x1b[49m"
}

// Bold, Italic, Underline, Inverse and Strikethrough are plain SGR in place of Chalk, whose color level is a process global (section 5.1).
func (t *Theme) Bold(text string) string          { return "\x1b[1m" + text + "\x1b[22m" }
func (t *Theme) Italic(text string) string        { return "\x1b[3m" + text + "\x1b[23m" }
func (t *Theme) Underline(text string) string     { return "\x1b[4m" + text + "\x1b[24m" }
func (t *Theme) Inverse(text string) string       { return "\x1b[7m" + text + "\x1b[27m" }
func (t *Theme) Strikethrough(text string) string { return "\x1b[9m" + text + "\x1b[29m" }

// GetFgAnsi is theme.ts Theme.getFgAnsi.
func (t *Theme) GetFgAnsi(color ThemeColor) string {
	ansi := tokenAnsi(t.fgAnsi, color)
	if t.dimTokens[color] {
		return ansi + "\x1b[2m"
	}
	return ansi
}

// GetBgAnsi is theme.ts Theme.getBgAnsi.
func (t *Theme) GetBgAnsi(color ThemeBg) string { return tokenAnsi(t.bgAnsi, color) }

// GetColorMode is theme.ts Theme.getColorMode.
func (t *Theme) GetColorMode() tui.TerminalColorMode { return t.Mode }

// GetThinkingBorderColor is theme.ts Theme.getThinkingBorderColor.
func (t *Theme) GetThinkingBorderColor(level string) func(string) string {
	token := "thinkingOff"
	switch level {
	case "minimal":
		token = "thinkingMinimal"
	case "low":
		token = "thinkingLow"
	case "medium":
		token = "thinkingMedium"
	case "high":
		token = "thinkingHigh"
	case "xhigh":
		token = "thinkingXhigh"
	case "max":
		token = "thinkingMax"
	}
	return func(text string) string { return t.Fg(token, text) }
}

// GetBashModeBorderColor is theme.ts Theme.getBashModeBorderColor.
func (t *Theme) GetBashModeBorderColor() func(string) string {
	return func(text string) string { return t.Fg("bashMode", text) }
}

// Language is Theme.language: the highlight.js language for a fence info string or file path, if bundled; "" otherwise.
func Language(hint string) string {
	if hint == "" {
		return ""
	}
	name := strings.ToLower(hint)
	if end := strings.IndexAny(name, " \t\r\n\f\v{"); end >= 0 {
		name = name[:end]
	}
	if slash := strings.LastIndexAny(name, `/\`); slash >= 0 {
		name = name[slash+1:]
	}
	key := name
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		key = name[dot+1:]
	}
	if alias, ok := languageAliases[key]; ok {
		key = alias
	}
	if key != "" && tui.SupportsLanguage(key) {
		return key
	}
	return ""
}

// styleLines styles every line part of text separately (an empty part stays empty), so each rendered line is self-contained.
func styleLines(text string, style func(string) string) string {
	parts := strings.Split(text, "\n")
	for i, part := range parts {
		if part != "" {
			parts[i] = style(part)
		}
	}
	return strings.Join(parts, "\n")
}

func (t *Theme) highlightTheme() tui.HighlightTheme {
	theme := make(tui.HighlightTheme, len(scopeStyles))
	for scope, style := range scopeStyles {
		theme[scope] = func(text string) string {
			return styleLines(text, func(part string) string {
				switch style {
				case "italic":
					return t.Italic(part)
				case "bold":
					return t.Bold(part)
				case "underline":
					return t.Underline(part)
				}
				return t.Fg(style, part)
			})
		}
	}
	return theme
}

// utf16Length is a JS string's length.
func utf16Length(text string) int { return len(utf16.Encode([]rune(text))) }

// Highlight is Theme.highlight: syntax-highlighted lines; unknown languages (and huge inputs) use `mdCodeBlock` like coding-agent.
func (t *Theme) Highlight(code, lang string) []string {
	plain := func() []string {
		lines := strings.Split(code, "\n")
		for i, line := range lines {
			lines[i] = t.Fg("mdCodeBlock", line)
		}
		return lines
	}
	language := Language(lang)
	if language == "" || utf16Length(code) > 200_000 {
		return plain()
	}
	highlighted, err := tui.Highlight(code, tui.HighlightOptions{Language: language, Theme: t.highlightTheme()})
	if err != nil {
		return plain()
	}
	return strings.Split(highlighted, "\n")
}

func (t ThemeJSON) resolved() (OrderedColors, error) {
	out := make(OrderedColors, 0, len(t.Colors))
	for _, entry := range t.Colors {
		value, err := resolveVarRefs(entry.Value, t.Vars, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, NamedColor{Name: entry.Name, Value: value})
	}
	return out, nil
}

// CreateTheme is createTheme: a theme from validated JSON for one terminal. It fails on unresolved variables or colors.
func CreateTheme(json ThemeJSON, mode tui.TerminalColorMode, terminal TerminalState) (*Theme, error) {
	resolved, err := json.resolved()
	if err != nil {
		return nil, err
	}
	withFallbacks, err := withThemeTokenFallbacks(resolved)
	if err != nil {
		return nil, err
	}
	fgColors, bgColors := splitThemeColors(withFallbacks)
	return NewTheme(fgColors, bgColors, Options{Mode: mode, Terminal: terminal, Name: json.Name, Appearance: json.Appearance})
}

// CreateSystemTheme is createSystemTheme: the system theme generated from this terminal's reported colors (grayscale while they are pending).
func CreateSystemTheme(mode tui.TerminalColorMode, terminal TerminalState) *Theme {
	saturation := 1.0
	if terminal.Pending {
		saturation = 0
	}
	generated := tui.GenerateSystemThemeColors(tui.SystemThemeInput{
		Foreground: terminal.Colors.Foreground, Background: terminal.Colors.Background, Palette: terminal.Colors.Palette,
		Saturation: &saturation, AppearanceHint: DetectTerminalTheme(terminal),
	})
	colors := make(map[string]ColorValue, len(generated.Colors))
	for token, value := range generated.Colors {
		colors[token] = ColorValue{Text: value.Text, Index: value.Index, IsIndex: value.IsIndex}
	}
	withFallbacks, err := withThemeTokenFallbacks(orderedOf(colors))
	if err != nil {
		panic(fmt.Sprintf("generate system theme: %v", err))
	}
	fgColors, bgColors := splitThemeColors(withFallbacks)
	theme, err := NewTheme(fgColors, bgColors, Options{
		Mode: mode, Terminal: terminal, Name: SystemThemeName, Appearance: generated.Appearance, Dim: generated.Dim,
	})
	if err != nil {
		panic(fmt.Sprintf("generate system theme: %v", err))
	}
	return theme
}

func orderedOf(colors map[string]ColorValue) OrderedColors {
	out := make(OrderedColors, 0, len(colors))
	for _, descriptor := range ThemeTokens {
		if value, ok := colors[descriptor.Name]; ok {
			out = append(out, NamedColor{Name: descriptor.Name, Value: value})
		}
	}
	return out
}

// AutoThemeSetting is a light/dark theme pair setting ("light/dark").
type AutoThemeSetting struct{ LightTheme, DarkTheme string }

// ParseAutoThemeSetting is parseAutoThemeSetting.
func ParseAutoThemeSetting(setting string) (AutoThemeSetting, bool) {
	if setting == "" {
		return AutoThemeSetting{}, false
	}
	slash := strings.IndexByte(setting, '/')
	if slash == -1 || strings.IndexByte(setting[slash+1:], '/') != -1 {
		return AutoThemeSetting{}, false
	}
	light := trimJS(setting[:slash])
	dark := trimJS(setting[slash+1:])
	if light == "" || dark == "" {
		return AutoThemeSetting{}, false
	}
	return AutoThemeSetting{LightTheme: light, DarkTheme: dark}, true
}

func trimJS(text string) string { return strings.TrimFunc(text, isJSSpace) }

func isJSSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\v', '\f', '\r', 0xa0, 0xfeff, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// ResolveThemeSetting is resolveThemeSetting; ok is false where the TypeScript returns undefined (an unset setting, or a malformed pair).
func ResolveThemeSetting(setting string, terminalTheme TerminalTheme) (name string, ok bool) {
	if pair, isPair := ParseAutoThemeSetting(setting); isPair {
		if terminalTheme == "light" {
			return pair.LightTheme, true
		}
		return pair.DarkTheme, true
	}
	if strings.Contains(setting, "/") {
		return "", false
	}
	return setting, setting != ""
}

// ResolveOptions are ResolveTheme's options.
type ResolveOptions struct {
	// Setting is the theme setting; "" is unset (the system theme).
	Setting  string
	Themes   map[string]ThemeJSON
	Mode     tui.TerminalColorMode
	Terminal TerminalState
}

// ResolveTheme is resolveTheme: the theme a setting selects for one terminal: a light/dark pair, a built-in, a custom theme supplied as data, or the
// system theme. Unknown or invalid themes fall back to the system theme and report an error (Err; nil when the setting resolved).
func ResolveTheme(options ResolveOptions) (theme *Theme, err error) {
	name, ok := ResolveThemeSetting(options.Setting, DetectTerminalTheme(options.Terminal))
	if !ok {
		name = SystemThemeName
	}
	if name == SystemThemeName {
		return CreateSystemTheme(options.Mode, options.Terminal), nil
	}
	json, found := BuiltinTheme(name)
	if !found {
		json, found = options.Themes[name]
	}
	if !found {
		err = fmt.Errorf("Theme not found: %s", name)
	} else {
		theme, err = CreateTheme(json, options.Mode, options.Terminal)
	}
	if err != nil {
		return CreateSystemTheme(options.Mode, options.Terminal), err
	}
	return theme, nil
}

func rgbOf(value any) (tui.RgbColor, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return tui.RgbColor{}, false
	}
	channel := func(raw any) (float64, bool) {
		n, ok := raw.(float64)
		return n, ok && n == math.Trunc(n) && n >= 0 && n <= 255
	}
	r, okR := channel(object["r"])
	g, okG := channel(object["g"])
	b, okB := channel(object["b"])
	if !okR || !okG || !okB {
		return tui.RgbColor{}, false
	}
	return tui.RgbColor{R: r, G: g, B: b}, true
}

// ParseTerminalState is parseTerminalState: it validates a `terminalColors` launcher message body.
func ParseTerminalState(raw any) TerminalState {
	value, _ := raw.(map[string]any)
	colors, _ := value["colors"].(map[string]any)
	var state TerminalState
	if foreground, ok := rgbOf(colors["foreground"]); ok {
		state.Colors.Foreground = &foreground
	}
	if background, ok := rgbOf(colors["background"]); ok {
		state.Colors.Background = &background
	}
	if palette, ok := colors["palette"].([]any); ok && len(palette) == 16 {
		parsed := make([]tui.RgbColor, 0, 16)
		for _, entry := range palette {
			rgb, ok := rgbOf(entry)
			if !ok {
				parsed = nil
				break
			}
			parsed = append(parsed, rgb)
		}
		state.Colors.Palette = parsed
	}
	if scheme, _ := value["scheme"].(string); scheme == "dark" || scheme == "light" {
		state.Scheme = TerminalTheme(scheme)
	}
	state.Pending = value["pending"] == true
	return state
}
