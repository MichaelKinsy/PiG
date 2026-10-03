package tui

// Ports packages/coding-agent/src/modes/interactive/theme/system-theme.ts
//
// The `system` theme: pi's colors derived from the terminal's own theme. Every token belongs to a color family (its hue) and has contrast rules: it must reach a contrast level on the background and on the panels it is drawn on. Hue and saturation come from the terminal's palette color for the family's ANSI slot, or from the family's own hue when the terminal reports no palette. Lightness comes from the rules alone. Colors are built in OKHSL, whose saturation is relative to the sRGB gamut, and fade toward gray near black and white. A palette color never gains OKLCH chroma when it moves to another lightness, so pastel palettes stay pastel.
//
// A contrast level is a target-lightness curve: the OKLab lightness a token needs, given the lightness of the surface below it. On dark backgrounds they aim for nearly fixed lightness; on light backgrounds the required difference grows as the background darkens.
//
// Depending on what the terminal reports, the theme is generated in one of three tiers: background and palette (hues from the palette, lightness from the background); background only (the families' own hues, lightness from the background); nothing (ANSI palette indices and the default colors, which the terminal renders itself).

import (
	"math"
	"slices"
	"strconv"
	"strings"
)

// SystemThemeName is the reserved name of the theme generated from the terminal's own colors.
const SystemThemeName = "system"

// SystemThemeInput is what the terminal reported plus generation options. A nil Saturation is 1.
type SystemThemeInput struct {
	Foreground *RgbColor
	Background *RgbColor
	// Palette is ANSI colors 0-15.
	Palette []RgbColor
	// Saturation is a multiplier from 0 (grayscale) to 1. The first frame renders in grayscale until colors arrive.
	Saturation *float64
	// AppearanceHint applies when the terminal did not report its background, e.g. from its light/dark report or COLORFGBG.
	AppearanceHint TerminalTheme
}

// SystemThemeColors is the generated theme: hex colors, ANSI palette indices, or "" for the terminal default per token.
type SystemThemeColors struct {
	Colors map[string]ThemeColorValue
	// Dim lists the foreground tokens rendered faint (SGR 2), for terminals that did not report colors.
	Dim []string
	// Appearance is empty when unknown.
	Appearance TerminalTheme
}

// ─── Recipe: color families and their tokens ─────────────────────────────────

// systemFamily is a family's OKHSL hue and saturation: max at mid lightness, falling toward min at black and white.
type systemFamily struct {
	hue      float64
	min, max float64
	// slot is the ANSI palette slot the family takes its hue and saturation from.
	slot int
}

var systemFamilies = map[string]systemFamily{
	"neutral":            {231.49, 0.02, 0.08, 8},
	"blue":               {231.49, 0.1, 0.68, 4},
	"green":              {158.68, 0.1, 0.76, 2},
	"red":                {20, 0.1, 0.92, 1},
	"yellow":             {82.36, 0.5, 1, 3},
	"orange":             {52, 0.12, 0.85, 3},
	"violet":             {295, 0.2, 0.6, 5},
	"calamine":           {202.43, 0.1, 0.74, 6},
	"thinkingSlate":      {231.49, 0.08, 0.2, 4},
	"thinkingBlue":       {231.49, 0.2, 0.45, 4},
	"thinkingPeriwinkle": {263.25, 0.3, 0.6, 6},
	"thinkingViolet":     {295, 0.4, 0.75, 5},
	"thinkingMagenta":    {337.5, 0.5, 0.85, 13},
	"thinkingRed":        {20, 0.95, 1, 1},
}

// systemTokenFamilies lists every token and its family in upstream's insertion order.
var systemTokenFamilies = []struct{ token, family string }{
	{"selectedBg", "blue"},
	{"searchMatchBg", "orange"},
	{"userMessageBg", "blue"},
	{"customMessageBg", "violet"},
	{"toolPendingBg", "neutral"},
	{"toolSuccessBg", "green"},
	{"toolErrorBg", "red"},
	{"text", "neutral"},
	{"userMessageText", "neutral"},
	{"customMessageText", "neutral"},
	{"toolTitle", "neutral"},
	{"syntaxOperator", "neutral"},
	{"syntaxPunctuation", "neutral"},
	{"muted", "neutral"},
	{"dim", "neutral"},
	{"thinkingText", "neutral"},
	{"toolOutput", "neutral"},
	{"mdLinkUrl", "neutral"},
	{"mdQuote", "neutral"},
	{"mdQuoteBorder", "neutral"},
	{"mdHr", "neutral"},
	{"mdCodeBlockBorder", "neutral"},
	{"toolDiffContext", "neutral"},
	{"syntaxComment", "neutral"},
	{"scrollbarTrack", "neutral"},
	{"scrollbarThumb", "neutral"},
	{"searchMatchText", "neutral"},
	{"borderMuted", "neutral"},
	{"accent", "violet"},
	{"borderAccent", "violet"},
	{"customMessageLabel", "violet"},
	{"mdCode", "violet"},
	{"mdListBullet", "violet"},
	{"syntaxType", "violet"},
	{"border", "blue"},
	{"mdLink", "blue"},
	{"syntaxKeyword", "blue"},
	{"syntaxVariable", "calamine"},
	{"success", "green"},
	{"mdCodeBlock", "green"},
	{"toolDiffAdded", "green"},
	{"bashMode", "green"},
	{"syntaxNumber", "green"},
	{"error", "red"},
	{"toolDiffRemoved", "red"},
	{"warning", "yellow"},
	{"mdHeading", "yellow"},
	{"syntaxFunction", "yellow"},
	{"syntaxString", "orange"},
	{"thinkingOff", "neutral"},
	{"thinkingMinimal", "thinkingSlate"},
	{"thinkingLow", "thinkingBlue"},
	{"thinkingMedium", "thinkingPeriwinkle"},
	{"thinkingHigh", "thinkingViolet"},
	{"thinkingXhigh", "thinkingMagenta"},
	{"thinkingMax", "thinkingRed"},
}

var systemTokenFamily = func() map[string]string {
	families := make(map[string]string, len(systemTokenFamilies))
	for _, entry := range systemTokenFamilies {
		families[entry.token] = entry.family
	}
	return families
}()

// systemTokenSlots are palette slots for tokens that would otherwise share a hue with a similar token.
var systemTokenSlots = map[string]int{"syntaxString": 2, "syntaxNumber": 5, "searchMatchBg": 3}

// ─── Contrast levels and rules ───────────────────────────────────────────────

// systemCurve is a target-lightness curve: a polynomial in the surface's OKLab lightness giving the OKLab lightness a token needs on it. reachable is the range of surface lightness where the level can be reached; beyond it the level is relaxed.
type systemCurve struct {
	coefficients []float64
	reachable    [2]float64
}

type systemLevel struct{ dark, light systemCurve }

func (l systemLevel) curve(appearance TerminalTheme) systemCurve {
	if appearance == "dark" {
		return l.dark
	}
	return l.light
}

var systemLevels = map[string]systemLevel{
	"panel": {
		dark:  systemCurve{[]float64{0.29131, -0.39746, 2.33185, -0.85524, -1.2076, 0.86276}, [2]float64{0, 0.979}},
		light: systemCurve{[]float64{-3.74073, 27.94549, -78.44258, 112.6798, -79.60015, 22.11277}, [2]float64{0.348, 1}},
	},
	"track": {
		dark:  systemCurve{[]float64{0.39028, -0.23015, 0.83573, 2.43829, -4.38292, 2.01582}, [2]float64{0, 0.946}},
		light: systemCurve{[]float64{-5.24921, 38.37322, -107.28833, 152.10005, -106.17127, 29.18061}, [2]float64{0.368, 1}},
	},
	"thinking0": {
		dark:  systemCurve{[]float64{0.52988, -0.05809, -0.30924, 4.63567, -6.52933, 2.89108}, [2]float64{0, 0.873}},
		light: systemCurve{[]float64{-28.27749, 182.85284, -469.62416, 603.15916, -384.59976, 97.35147}, [2]float64{0.51, 1}},
	},
	"thinking1": {
		dark:  systemCurve{[]float64{0.55278, -0.03667, -0.45659, 4.95347, -6.90265, 3.0706}, [2]float64{0, 0.858}},
		light: systemCurve{[]float64{-37.10484, 235.86282, -596.62344, 754.3633, -474.00763, 118.3551}, [2]float64{0.535, 1}},
	},
	"thinking2": {
		dark:  systemCurve{[]float64{0.57486, -0.01765, -0.58987, 5.25227, -7.27175, 3.25532}, [2]float64{0, 0.842}},
		light: systemCurve{[]float64{-59.89653, 377.05024, -945.07843, 1182.03145, -734.96375, 181.68658}, [2]float64{0.556, 1}},
	},
	"thinking3": {
		dark:  systemCurve{[]float64{0.59621, -0.00062, -0.71148, 5.53588, -7.6392, 3.44606}, [2]float64{0, 0.827}},
		light: systemCurve{[]float64{-72.07122, 445.84082, -1099.57352, 1353.88793, -829.53392, 202.26164}, [2]float64{0.58, 1}},
	},
	"thinking4": {
		dark:  systemCurve{[]float64{0.61691, 0.01462, -0.82288, 5.80651, -8.00641, 3.64333}, [2]float64{0, 0.811}},
		light: systemCurve{[]float64{-110.14338, 674.21488, -1645.75941, 2004.32367, -1215.15899, 293.3183}, [2]float64{0.6, 1}},
	},
	"thinking5": {
		dark:  systemCurve{[]float64{0.63702, 0.02826, -0.92498, 6.06465, -8.37246, 3.84651}, [2]float64{0, 0.795}},
		light: systemCurve{[]float64{-175.47701, 1063.54495, -2570.70594, 3098.80776, -1860.15527, 444.76392}, [2]float64{0.62, 1}},
	},
	"thinking6": {
		dark:  systemCurve{[]float64{0.65658, 0.04044, -1.01835, 6.30989, -8.73529, 4.05439}, [2]float64{0, 0.779}},
		light: systemCurve{[]float64{-183.81712, 1094.70055, -2602.68539, 3088.71276, -1826.91131, 430.75931}, [2]float64{0.643, 1}},
	},
	"subtle": {
		dark:  systemCurve{[]float64{0.56762, -0.02475, -0.5383, 5.12628, -7.10931, 3.17324}, [2]float64{0, 0.848}},
		light: systemCurve{[]float64{-232.85459, 1376.54473, -3249.11801, 3827.91186, -2248.29472, 526.55751}, [2]float64{0.657, 1}},
	},
	"thumb": {
		dark:  systemCurve{[]float64{0.60323, 0.00278, -0.73328, 5.57157, -7.68067, 3.46933}, [2]float64{0, 0.823}},
		light: systemCurve{[]float64{-82.89897, 511.01355, -1255.98095, 1540.76821, -940.68087, 228.58523}, [2]float64{0.586, 1}},
	},
	"readable": {
		dark:  systemCurve{[]float64{0.66937, 0.04704, -1.06871, 6.43941, -8.9332, 4.17229}, [2]float64{0, 0.77}},
		light: systemCurve{[]float64{-1554.52576, 8733.56817, -19604.93507, 21977.72696, -12300.99599, 2749.81288}, [2]float64{0.751, 1}},
	},
	"emphasis": {
		dark:  systemCurve{[]float64{0.7303, 0.07695, -1.31626, 7.1681, -10.14436, 4.92846}, [2]float64{0, 0.712}},
		light: systemCurve{[]float64{-4948.31942, 26870.91986, -58334.48399, 63280.17197, -34298.01053, 7430.30146}, [2]float64{0.811, 1}},
	},
	"textOnPanel": {
		dark:  systemCurve{[]float64{0.86713, 0.05232, -0.89428, 4.79014, -5.5432, 1.75023}, [2]float64{0, 0.542}},
		light: systemCurve{[]float64{-8570.89457, 43954.60805, -90084.00702, 92220.6791, -47152.15802, 9632.27113}, [2]float64{0.867, 1}},
	},
	"text": {
		dark:  systemCurve{[]float64{0.89242, 0.02311, -0.44862, 2.34417, -0.06084, -2.63844}, [2]float64{0, 0.5}},
		light: systemCurve{[]float64{-2004.67048, 6664.47299, -6060.70202, -1792.61209, 5133.82359, -1939.85583}, [2]float64{0.894, 1}},
	},
}

// systemRule says a token must reach a contrast level on each of its surfaces ("background" or a panel token).
type systemRule struct {
	token string
	on    []string
	level string
}

var (
	systemToolPanels    = []string{"toolPendingBg", "toolSuccessBg", "toolErrorBg"}
	systemMessagePanels = []string{"userMessageBg", "customMessageBg"}
	systemPanels        = []string{"userMessageBg", "toolPendingBg", "toolSuccessBg", "toolErrorBg", "selectedBg", "searchMatchBg", "customMessageBg"}
	systemThinking      = []string{"thinkingOff", "thinkingMinimal", "thinkingLow", "thinkingMedium", "thinkingHigh", "thinkingXhigh", "thinkingMax"}
	systemThinkingLevel = []string{"thinking0", "thinking1", "thinking2", "thinking3", "thinking4", "thinking5", "thinking6"}
)

func systemEach(tokens []string, on []string, level string) []systemRule {
	rules := make([]systemRule, len(tokens))
	for i, token := range tokens {
		rules[i] = systemRule{token, on, level}
	}
	return rules
}

func systemConcat(parts ...[]string) []string {
	var joined []string
	for _, part := range parts {
		joined = append(joined, part...)
	}
	return joined
}

var systemRules = func() []systemRule {
	background := []string{"background"}
	var rules []systemRule
	add := func(more ...systemRule) { rules = append(rules, more...) }
	add(systemEach(systemPanels, background, "panel")...)
	add(systemRule{"text", background, "text"})
	add(systemRule{"text", []string{"selectedBg"}, "textOnPanel"})
	add(systemRule{"userMessageText", []string{"userMessageBg"}, "textOnPanel"})
	add(systemRule{"toolTitle", systemToolPanels, "textOnPanel"})
	add(systemEach([]string{"accent", "success", "error", "warning"}, systemConcat(background, []string{"selectedBg"}, systemToolPanels), "readable")...)
	add(systemRule{"muted", systemConcat(background, []string{"selectedBg", "customMessageBg"}, systemToolPanels), "readable"})
	add(systemRule{"dim", systemConcat(background, []string{"selectedBg", "customMessageBg"}, systemToolPanels), "subtle"})
	add(systemRule{"thinkingText", background, "readable"})
	add(systemRule{"customMessageText", systemConcat([]string{"customMessageBg"}, systemToolPanels), "readable"})
	add(systemRule{"customMessageLabel", systemConcat(background, []string{"customMessageBg", "selectedBg"}, systemToolPanels), "readable"})
	add(systemRule{"toolOutput", systemConcat(background, systemToolPanels), "readable"})
	add(systemEach([]string{"mdHeading", "mdLink", "mdLinkUrl", "mdCode", "mdQuote", "mdCodeBlockBorder", "mdListBullet"}, systemConcat(background, systemMessagePanels), "readable")...)
	add(systemRule{"mdCodeBlock", systemConcat(background, systemMessagePanels, systemToolPanels), "readable"})
	add(systemEach([]string{"toolDiffAdded", "toolDiffRemoved", "toolDiffContext"}, systemConcat(background, systemToolPanels), "readable")...)
	add(systemEach([]string{"syntaxComment", "syntaxKeyword", "syntaxFunction", "syntaxVariable", "syntaxString", "syntaxNumber", "syntaxType", "syntaxOperator", "syntaxPunctuation"},
		systemConcat(background, systemMessagePanels, systemToolPanels), "readable")...)
	add(systemRule{"searchMatchText", []string{"searchMatchBg"}, "readable"})
	add(systemEach([]string{"bashMode", "border", "borderAccent"}, background, "readable")...)
	add(systemRule{"borderMuted", background, "subtle"})
	add(systemEach([]string{"mdQuoteBorder", "mdHr"}, systemConcat(background, systemMessagePanels, systemToolPanels), "readable")...)
	add(systemRule{"scrollbarTrack", background, "track"})
	add(systemRule{"scrollbarThumb", []string{"scrollbarTrack"}, "thumb"})
	for index, token := range systemThinking {
		add(systemRule{token, background, systemThinkingLevel[index]})
	}
	return rules
}()

// systemReadableFloor is the level relaxation compresses stronger levels toward before weakening all levels.
func systemReadableFloor(appearance TerminalTheme) string {
	if appearance == "dark" {
		return "readable"
	}
	return "subtle"
}

// Body text uses the terminal's foreground when it reaches this level, which is clearly stronger than muted.
const systemForegroundLevel = "emphasis"

// systemForegroundTokens are the text-level tokens that take the terminal's foreground.
var systemForegroundTokens = []string{"text", "userMessageText", "toolTitle"}

// systemTextMinimumWcagContrast is the WCAG 2 contrast ratio that body text must reach on the surfaces it is drawn on.
const systemTextMinimumWcagContrast = 4.5

// systemSolveOrder is the tokens in dependency order: every surface before the tokens drawn on it.
var systemSolveOrder = func() []string {
	var order []string
	var visit func(token string)
	visit = func(token string) {
		if slices.Contains(order, token) {
			return
		}
		for _, rule := range systemRules {
			if rule.token != token {
				continue
			}
			for _, surface := range rule.on {
				if surface != "background" {
					visit(surface)
				}
			}
		}
		order = append(order, token)
	}
	for _, rule := range systemRules {
		visit(rule.token)
	}
	return order
}()

// ─── Public API ──────────────────────────────────────────────────────────────

// oklabLightness is the OKLab lightness of an sRGB color, 0-1.
func oklabLightness(color RgbColor) float64 {
	return ColorToOklch(RgbColorValue(color)).L
}

// relativeLuminance is the WCAG 2 relative luminance.
func relativeLuminance(color RgbColor) float64 {
	linear := func(channel float64) float64 {
		value := channel / 255
		if value <= 0.04045 {
			return value / 12.92
		}
		return math.Pow((value+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(color.R) + 0.7152*linear(color.G) + 0.0722*linear(color.B)
}

// WcagContrast is the WCAG 2 contrast ratio, 1-21.
func WcagContrast(first, second RgbColor) float64 {
	a := relativeLuminance(first)
	b := relativeLuminance(second)
	return (math.Max(a, b) + 0.05) / (math.Min(a, b) + 0.05)
}

// terminalAppearance is whether a terminal is dark or light, from its reported colors: the direction of its own foreground when text can be readable that way, otherwise dark when white text has more contrast on the background than black text.
func terminalAppearance(background RgbColor, foreground *RgbColor) TerminalTheme {
	white := RgbColor{R: 255, G: 255, B: 255}
	black := RgbColor{}
	whiteContrast := WcagContrast(white, background)
	blackContrast := WcagContrast(black, background)
	if foreground != nil {
		foregroundL := oklabLightness(*foreground)
		backgroundL := oklabLightness(background)
		if math.Abs(foregroundL-backgroundL) > 0.05 {
			appearance, best := TerminalTheme("light"), blackContrast
			if foregroundL > backgroundL {
				appearance, best = "dark", whiteContrast
			}
			if best >= systemTextMinimumWcagContrast {
				return appearance
			}
		}
	}
	if whiteContrast >= blackContrast {
		return "dark"
	}
	return "light"
}

// ─── Generation ──────────────────────────────────────────────────────────────

func clampFloat(value, low, high float64) float64 { return math.Min(high, math.Max(low, value)) }

func hexOfRgb(color RgbColor) string {
	var hex strings.Builder
	hex.WriteByte('#')
	for _, channel := range [3]float64{color.R, color.G, color.B} {
		digits := strconv.FormatInt(int64(jsRound(channel)), 16)
		if len(digits) < 2 {
			digits = "0" + digits
		}
		hex.WriteString(digits)
	}
	return hex.String()
}

// bellWeight is the saturation weight at a lightness: a Gaussian (center 0.5, sigma 0.25), 0 at black and white, 1 in the middle.
func bellWeight(lightness float64) float64 {
	gaussian := func(x float64) float64 { return math.Exp(-((x - 0.5) * (x - 0.5)) / (2 * (0.25 * 0.25))) }
	return (gaussian(lightness) - gaussian(0)) / (1 - gaussian(0))
}

// saturationCurve is a family's saturation curve relative to its maximum: 1 at mid lightness, min / max at black and white.
func saturationCurve(family systemFamily, lightness float64) float64 {
	floor := 1.0
	if family.max > 0 {
		floor = family.min / family.max
	}
	return floor + (1-floor)*bellWeight(lightness)
}

// levelTarget is the target lightness for a level on a surface, or false where the level cannot be reached.
func levelTarget(level string, appearance TerminalTheme, surfaceL float64) (float64, bool) {
	curve := systemLevels[level].curve(appearance)
	if surfaceL < curve.reachable[0] || surfaceL > curve.reachable[1] {
		return 0, false
	}
	sum := 0.0
	for power, coefficient := range curve.coefficients {
		sum += coefficient * math.Pow(surfaceL, float64(power))
	}
	return sum, true
}

// okhslRgb is okhslColor for values the recipe keeps in range; an out-of-range saturation or lightness from rounding is clamped where upstream would throw.
func okhslRgb(h, s, l float64) RgbColor {
	color, err := NewOkhslColor(h, clampFloat(s, 0, 1), clampFloat(l, 0, 1))
	if err != nil {
		return RgbColor{}
	}
	return RgbColor(color)
}

func okhslOf(color RgbColor) OkhslChannels { return ColorToOkhsl(RgbColorValue(color)) }

// sourceColor is a terminal color's OKHSL channels and its OKLCH chroma.
type sourceColor struct {
	OkhslChannels
	chroma float64
}

func sourceOf(color RgbColor) sourceColor {
	return sourceColor{OkhslChannels: okhslOf(color), chroma: ColorToOklch(RgbColorValue(color)).C}
}

// anchored is a source color's hue at another OKHSL lightness. Its saturation applies at its own lightness and falls off toward black and white along the family's saturation curve, never rising above it.
//
// OKHSL saturation is relative to the most chroma sRGB allows at a lightness, so the same saturation can mean more chroma elsewhere: Catppuccin Frappe's pink #f4b8e4 (chroma 0.089) would become #eb76d1 (0.180) at the lightness the accent needs. Chroma is therefore also capped at the source's, with the same falloff (#10255).
func anchored(source sourceColor, family systemFamily, lightness, saturation float64) RgbColor {
	anchor := saturationCurve(family, source.L)
	falloff := 1.0
	if anchor > 0 {
		falloff = math.Min(1, saturationCurve(family, lightness)/anchor)
	}
	color := okhslRgb(source.H, source.S*falloff*saturation, lightness)
	limit := source.chroma * falloff * saturation
	oklch := ColorToOklch(RgbColorValue(color))
	if oklch.C <= limit {
		return color
	}
	return oklchToRgb(OklchChannels{L: oklch.L, C: limit, H: source.H})
}

// withTextContrast moves a text color toward white or black until it reaches the WCAG minimum on every surface.
func withTextContrast(color RgbColor, surfaces []RgbColor, lighter bool) RgbColor {
	meets := func(candidate RgbColor) bool {
		return !slices.ContainsFunc(surfaces, func(surface RgbColor) bool { return WcagContrast(candidate, surface) < systemTextMinimumWcagContrast })
	}
	if meets(color) {
		return color
	}
	okhsl := okhslOf(color)
	at := func(lightness float64) RgbColor { return okhslRgb(okhsl.H, okhsl.S, lightness) }
	extreme := 0.0
	if lighter {
		extreme = 1
	}
	if !meets(at(extreme)) {
		return at(extreme)
	}
	low, high := okhsl.L, extreme
	for range 20 {
		middle := (low + high) / 2
		if meets(at(middle)) {
			high = middle
		} else {
			low = middle
		}
	}
	return at(high)
}

func systemColorValue(text string) ThemeColorValue { return ThemeColorValue{Text: text, isSet: true} }

// GenerateSystemThemeColors generates the system theme's colors from the terminal's reported colors.
func GenerateSystemThemeColors(input SystemThemeInput) SystemThemeColors {
	saturation := 1.0
	if input.Saturation != nil {
		saturation = *input.Saturation
	}
	saturation = clampFloat(saturation, 0, 1)
	if input.Background == nil {
		return systemIndexedColors(saturation, input.AppearanceHint)
	}
	background, foreground := *input.Background, input.Foreground
	var palette []sourceColor
	if len(input.Palette) == 16 {
		for _, color := range input.Palette {
			palette = append(palette, sourceOf(color))
		}
	}

	appearance := terminalAppearance(background, foreground)
	lighter := appearance == "dark"
	extreme := 0.0
	if lighter {
		extreme = 1
	}
	backgroundL := oklabLightness(background)

	// paint is a token's color at an OKLab lightness. With a palette, the palette color's saturation applies at its own lightness and falls off toward black and white along the family's curve, never rising above it.
	paint := func(token string, oklabL float64) RgbColor {
		lightness := OklabToOkhslLightness(oklabL)
		family := systemFamilies[systemTokenFamily[token]]
		if palette == nil {
			return okhslRgb(family.hue, (family.min+(family.max-family.min)*bellWeight(lightness))*saturation, lightness)
		}
		slot, ok := systemTokenSlots[token]
		if !ok {
			slot = family.slot
		}
		return anchored(palette[slot], family, lightness, saturation)
	}

	// target is the lightness a rule needs on a surface, relaxed by t: from 0 to 1, levels stronger than the readable floor move toward it; from 1 to 2, all levels move toward the surface itself.
	target := func(level string, surfaceL, t float64) (float64, bool) {
		reached, reachable := levelTarget(level, appearance, surfaceL)
		if !reachable && t == 0 {
			return 0, false
		}
		if !reachable {
			reached = extreme
		}
		distance := reached - surfaceL
		floorTarget, floorReachable := levelTarget(systemReadableFloor(appearance), appearance, surfaceL)
		if !floorReachable {
			floorTarget = extreme
		}
		floor := floorTarget - surfaceL
		compressed := distance
		if math.Abs(distance) > math.Abs(floor) {
			compressed = distance - (distance-floor)*math.Min(t, 1)
		}
		return surfaceL + compressed*(1-math.Max(0, t-1)), true
	}

	// limitPanel keeps a panel light enough (or dark enough) that white (or black) text still reaches the body text minimum on it. This only matters for backgrounds near mid-gray, where it barely does on the background.
	extremeText := RgbColor{}
	if lighter {
		extremeText = RgbColor{R: 255, G: 255, B: 255}
	}
	readable := func(color RgbColor) bool { return WcagContrast(extremeText, color) >= systemTextMinimumWcagContrast }
	limitPanel := func(token string, l float64) RgbColor {
		color := paint(token, l)
		if readable(color) {
			return color
		}
		low, high := backgroundL, l
		for range 20 {
			middle := (low + high) / 2
			if readable(paint(token, middle)) {
				low = middle
			} else {
				high = middle
			}
		}
		return paint(token, low)
	}

	solve := func(t float64) map[string]RgbColor {
		colors := map[string]RgbColor{"background": background}
		surfaceColor := func(surface string) RgbColor {
			if color, ok := colors[surface]; ok {
				return color
			}
			return background
		}
		for _, token := range systemSolveOrder {
			var targets []float64
			for _, rule := range systemRules {
				if rule.token != token {
					continue
				}
				for _, surface := range rule.on {
					value, ok := target(rule.level, oklabLightness(surfaceColor(surface)), t)
					if !ok || value < 0 || value > 1 {
						return nil
					}
					targets = append(targets, value)
				}
			}
			l := slices.Min(targets)
			if lighter {
				l = slices.Max(targets)
			}
			if slices.Contains(systemPanels, token) {
				colors[token] = limitPanel(token, l)
			} else {
				colors[token] = paint(token, l)
			}
		}
		return colors
	}

	relaxation := 0.0
	colors := solve(0)
	if colors == nil {
		// Mid-gray backgrounds cannot fit every level: relax as little as possible. Full relaxation always fits.
		low, high := 0.0, 2.0
		colors = solve(high)
		for range 20 {
			middle := (low + high) / 2
			if attempt := solve(middle); attempt != nil {
				high, colors = middle, attempt
			} else {
				low = middle
			}
		}
		relaxation = high
	}
	solved := colors
	if solved == nil {
		solved = map[string]RgbColor{}
	}
	surfacesOf := func(token string) []RgbColor {
		var surfaces []RgbColor
		for _, rule := range systemRules {
			if rule.token != token {
				continue
			}
			for _, surface := range rule.on {
				if color, ok := solved[surface]; ok {
					surfaces = append(surfaces, color)
				} else {
					surfaces = append(surfaces, background)
				}
			}
		}
		return surfaces
	}

	result := make(map[string]ThemeColorValue, len(systemTokenFamilies))
	for _, entry := range systemTokenFamilies {
		if color, ok := solved[entry.token]; ok {
			result[entry.token] = systemColorValue(hexOfRgb(color))
		} else {
			result[entry.token] = systemColorValue("")
		}
	}

	for _, token := range systemForegroundTokens {
		surfaces := surfacesOf(token)
		// Body text uses the terminal's own foreground where it is clearly stronger than muted text; otherwise the foreground's hue at just enough lightness.
		text, hasText := solved[token]
		if foreground != nil {
			targets := make([]float64, len(surfaces))
			every := true
			for i, surface := range surfaces {
				value, ok := target(systemForegroundLevel, oklabLightness(surface), relaxation)
				if !ok || value < 0 || value > 1 {
					every = false
					break
				}
				targets[i] = value
			}
			if every {
				needed := slices.Min(targets)
				if lighter {
					needed = slices.Max(targets)
				}
				foregroundL := oklabLightness(*foreground)
				if (lighter && foregroundL >= needed) || (!lighter && foregroundL <= needed) {
					result[token] = systemColorValue("")
					continue
				}
				text, hasText = anchored(sourceOf(*foreground), systemFamilies["neutral"], OklabToOkhslLightness(needed), saturation), true
			}
		}
		// Body text keeps at least 4.5:1 on the surfaces it is drawn on, even on relaxed mid-gray backgrounds.
		if hasText {
			result[token] = systemColorValue(hexOfRgb(withTextContrast(text, surfaces, lighter)))
		}
	}
	return SystemThemeColors{Colors: result, Appearance: appearance}
}

// systemIndexedColors are colors for terminals that reported nothing: the terminal renders ANSI indices 0-15 and the default colors with its own theme, so they fit any background. Neutral tokens below body text are faint (SGR 2) instead of bright black, which some themes make nearly invisible. Panels have no background.
func systemIndexedColors(saturation float64, appearance TerminalTheme) SystemThemeColors {
	colors := make(map[string]ThemeColorValue, len(systemTokenFamilies))
	var dim []string
	for _, entry := range systemTokenFamilies {
		if slices.Contains(systemPanels, entry.token) {
			colors[entry.token] = systemColorValue("")
			continue
		}
		neutral := entry.family == "neutral"
		if !neutral && saturation > 0 {
			slot, ok := systemTokenSlots[entry.token]
			if !ok {
				slot = systemFamilies[entry.family].slot
			}
			colors[entry.token] = ThemeColorValue{Index: slot, IsIndex: true, isSet: true}
		} else {
			colors[entry.token] = systemColorValue("")
		}
		if neutral && !slices.Contains(systemForegroundTokens, entry.token) {
			dim = append(dim, entry.token)
		}
	}
	return SystemThemeColors{Colors: colors, Dim: dim, Appearance: appearance}
}
