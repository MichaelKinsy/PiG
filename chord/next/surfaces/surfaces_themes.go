package surfaces

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Ports packages/chord/src/surfaces/themes.ts. Looks and themes (slice 11 §4b, decision "Looks and themes"): a theme is
// data in one registry, `Themes`, that plugins contribute to on any host kind; a page writes the active theme as CSS
// custom properties on `:root`, a terminal looks colours up by role.
//
// Go form: a theme's colour, web and tui records are maps; ThemeVariables returns a map, so the order of the CSS
// variables a caller writes is the caller's (JavaScript's insertion order has no Go carrier in a map).

// BaseRoles are the roles every theme names: the kit's base set. Apps add their own roles; themes fill at least these.
var BaseRoles = []string{"accent", "bg", "fg", "muted", "error", "warning", "success"}

// Theme is an id, a name, its mode, a colour per role (CSS colours; `#rrggbb` for terminals), web and terminal extras.
type Theme struct {
	ID   string
	Name string
	// Mode is "dark" or "light".
	Mode   string
	Colors map[string]string
	// Web is more CSS variables on `:root`, by name without `--` (`radius`, `font`); URLs of the plugin's assets allowed.
	Web map[string]string
	// Tui is terminal colours by role, before Colors: `#rrggbb` or an xterm 256-colour index (`"208"`).
	Tui map[string]string
}

// Themes is the registry of the themes plugins contribute, in plugin-name order; served on every client and on the
// server by the kit.
var Themes = mustRegistry[Theme, []Theme]("chord.themes")

// ThemesFacet is the kit's facet serving `Themes` on a host: the contributions as they are, in plugin-name order.
func ThemesFacet() host.Facet {
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		provider := registries.RegistryProvider[Theme, []Theme]{
			Compose: func(table registries.RegistryTable[Theme], _ registries.ComposeContext) ([]Theme, error) {
				themes := make([]Theme, len(table))
				for i, entry := range table {
					themes[i] = entry.Value
				}
				return themes, nil
			},
		}
		return h.Serve(registries.Registry[any, any](Themes), provider.Erase(), host.ServeOptions{})
	})
}

// FallbackThemes is the theme used when no plugin contributed one of a mode: neutral colours.
var FallbackThemes = map[string]Theme{
	"dark": {ID: "chord.dark", Name: "Dark", Mode: "dark", Colors: map[string]string{
		"accent": "#4c9aff", "bg": "#16181d", "fg": "#e6e6e6", "muted": "#8a8f98",
		"error": "#ff6b6b", "warning": "#f0b429", "success": "#3ecf8e",
	}},
	"light": {ID: "chord.light", Name: "Light", Mode: "light", Colors: map[string]string{
		"accent": "#0b62d6", "bg": "#ffffff", "fg": "#1c1e21", "muted": "#6b7280",
		"error": "#c62828", "warning": "#b26a00", "success": "#1b7f4b",
	}},
}

// ThemePick is the user's choice: a theme id for dark and one for light (nil: the first contributed of that mode).
type ThemePick struct {
	Dark, Light *string
}

// ActiveTheme is the active theme for mode: the one pick names if contributed, else the first of that mode, else the
// fallback.
func ActiveTheme(themes []Theme, pick ThemePick, mode string) Theme {
	wanted := pick.Dark
	if mode == "light" {
		wanted = pick.Light
	}
	var first *Theme
	for i := range themes {
		if themes[i].Mode != mode {
			continue
		}
		if wanted != nil && themes[i].ID == *wanted {
			return themes[i]
		}
		if first == nil {
			first = &themes[i]
		}
	}
	if first != nil {
		return *first
	}
	return FallbackThemes[mode]
}

// ThemeVariables are the CSS custom properties of theme: `--<role>` for every colour, then `--<name>` for each web extra.
func ThemeVariables(theme Theme) map[string]string {
	variables := make(map[string]string, len(theme.Colors)+len(theme.Web))
	for role, color := range theme.Colors {
		variables["--"+role] = color
	}
	for name, value := range theme.Web {
		variables["--"+name] = value
	}
	return variables
}

var (
	indexColor = regexp.MustCompile(`^\d{1,3}$`)
	hexColor   = regexp.MustCompile(`(?i)^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$`)
)

// TerminalColor is the SGR parameters of role's colour in theme for a terminal of depth "truecolor" or "256"
// (`38;2;r;g;b`, `38;5;n`; `48;…` for a background layer "bg"): the role's `tui` override first, else its colour, else the
// fallback theme's; a `#rrggbb` colour is the nearest of the 256 xterm colours on a 256-colour terminal. A role nobody
// defines, or a colour that is no `#rrggbb`, gives the terminal's default (`39`, `49`).
func TerminalColor(theme Theme, role, depth, layer string) string {
	color, has := theme.Tui[role]
	if !has {
		color, has = theme.Colors[role]
	}
	if !has {
		color, has = FallbackThemes[theme.Mode].Colors[role]
	}
	base := "38"
	if layer != "fg" {
		base = "48"
	}
	if has && indexColor.MatchString(color) {
		if n, _ := strconv.Atoi(color); n < 256 {
			return base + ";5;" + color
		}
	}
	var rgb [3]int
	match := hexColor.FindStringSubmatch(color)
	if !has || match == nil {
		if layer == "fg" {
			return "39"
		}
		return "49"
	}
	for i := range rgb {
		value, _ := strconv.ParseInt(match[i+1], 16, 0)
		rgb[i] = int(value)
	}
	if depth == "truecolor" {
		return fmt.Sprintf("%s;2;%s", base, strings.Join([]string{strconv.Itoa(rgb[0]), strconv.Itoa(rgb[1]), strconv.Itoa(rgb[2])}, ";"))
	}
	return fmt.Sprintf("%s;5;%d", base, nearest256(rgb))
}

// cube is the levels of the xterm 6×6×6 colour cube.
var cube = [6]int{0, 95, 135, 175, 215, 255}

// nearest256 is the nearest xterm 256-colour index to rgb: the nearer of the colour cube's (16–231) and the grey ramp's
// (232–255).
func nearest256(rgb [3]int) int {
	level := func(value int) int {
		best := 0
		for index := 1; index < len(cube); index++ {
			if math.Abs(float64(cube[index]-value)) < math.Abs(float64(cube[best]-value)) {
				best = index
			}
		}
		return best
	}
	r, g, b := level(rgb[0]), level(rgb[1]), level(rgb[2])
	near := [3]int{cube[r], cube[g], cube[b]}
	average := float64(rgb[0]+rgb[1]+rgb[2]) / 3
	// Math.round rounds half up.
	grey := int(math.Max(0, math.Min(23, math.Floor((average-8)/10+0.5))))
	greyValue := 8 + grey*10
	distance := func(other [3]int) int {
		return (other[0]-rgb[0])*(other[0]-rgb[0]) + (other[1]-rgb[1])*(other[1]-rgb[1]) + (other[2]-rgb[2])*(other[2]-rgb[2])
	}
	if distance([3]int{greyValue, greyValue, greyValue}) < distance(near) {
		return 232 + grey
	}
	return 16 + 36*r + 6*g + b
}
