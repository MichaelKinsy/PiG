package surfacehost

import (
	"fmt"
	"math"
	"strconv"

	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/surfaces"
)

// the native client's PiG colours are two themes in Chord's `Themes` registry, the same registry
// every web page and terminal reads, so the user's theme choice (`chord.themes.choice`) restyles the native window too.

// Roles beyond the kit's base set (surfaces.BaseRoles) that the native views use: the raised surface, the soft fill of
// selected and hovered rows, the line between areas, and the colour of text on the accent.
const (
	RoleSurface  = "surface"
	RoleSoft     = "soft"
	RoleLine     = "line"
	RoleOnAccent = "onAccent"
)

// BrandThemes are the PiG site's palette as Chord themes: dark (page #101c1a) and light (page #fff). The values are
// apps/p2/web/brand/tokens.json, the one token set of the web and native clients (TestBrandThemesAreTheWebTokens fails on a
// difference); the role names are the token names, except bg is page, fg is text, error is bad, line is border, and
// success (the added-line ink) and onAccent (the page) are derived.
func BrandThemes() []surfaces.Theme {
	return []surfaces.Theme{
		{ID: "pig.dark", Name: "PiG dark", Mode: "dark", Colors: map[string]string{
			"bg":         "#101c1a",
			RoleSurface:  "#172824",
			RoleSoft:     "#1d342b",
			"code":       "#172824",
			RoleLine:     "#30463e",
			"fg":         "#e8f4f0",
			"muted":      "#a0b5ac",
			"faint":      "#7f958c",
			"accent":     "#25c99b",
			"warning":    "#f0c674",
			"error":      "#f4948c",
			"add":        "#12342a",
			"addInk":     "#7fe0bf",
			"del":        "#3a2220",
			"delInk":     "#f4a19a",
			"scrollbar":  "#3f574e",
			"keyword":    "#f2a0d2",
			"string":     "#9fe3b8",
			"number":     "#f5b073",
			"title":      "#8fbaff",
			"type":       "#f0cf7a",
			"attr":       "#cfaef7",
			"accentText": "#25c99b",
			"success":    "#7fe0bf", RoleOnAccent: "#101c1a",
		}},
		{ID: "pig.light", Name: "PiG light", Mode: "light", Colors: map[string]string{
			"bg":         "#ffffff",
			RoleSurface:  "#eef4f1",
			RoleSoft:     "#eef4f1",
			"code":       "#f3f7f5",
			RoleLine:     "#dce5e0",
			"fg":         "#18302c",
			"muted":      "#52645e",
			"faint":      "#6f827b",
			"accent":     "#008567",
			"warning":    "#8a5a00",
			"error":      "#b3261e",
			"add":        "#e1f4ec",
			"addInk":     "#0b6b50",
			"del":        "#fde7e6",
			"delInk":     "#a8302a",
			"scrollbar":  "#b4c5be",
			"keyword":    "#8a2f7a",
			"string":     "#0b6b50",
			"number":     "#9a4f00",
			"title":      "#1f55b3",
			"type":       "#7a5200",
			"attr":       "#6e35a8",
			"accentText": "#006e55",
			"success":    "#0b6b50", RoleOnAccent: "#ffffff",
		}},
	}
}

// BrandHostFacets are the plugins that contribute BrandThemes, one per theme (the registry takes one contribution
// per plugin): `pig-theme-dark` and `pig-theme-light`.
func BrandHostFacets() []host.HostFacet {
	var facets []host.HostFacet
	for _, theme := range BrandThemes() {
		facets = append(facets, host.HostFacet{Name: "pig-theme-" + theme.Mode, Facet: host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
			return self.Contribute(registries.Registry[any, any](surfaces.Themes), theme)
		})})
	}
	return facets
}

// RGB is an 8-bit colour.
type RGB struct{ R, G, B uint8 }

// ParseHex reads `#rrggbb`.
func ParseHex(text string) (RGB, error) {
	if len(text) != 7 || text[0] != '#' {
		return RGB{}, fmt.Errorf("surfacehost: %q is not #rrggbb", text)
	}
	value, err := strconv.ParseUint(text[1:], 16, 32)
	if err != nil {
		return RGB{}, fmt.Errorf("surfacehost: %q is not #rrggbb", text)
	}
	return RGB{uint8(value >> 16), uint8(value >> 8), uint8(value)}, nil
}

func linear(channel uint8) float64 {
	c := float64(channel) / 255
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// Luminance is WCAG's relative luminance.
func (c RGB) Luminance() float64 {
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}

// Contrast is WCAG's contrast ratio of two colours (1 to 21).
func Contrast(a, b RGB) float64 {
	la, lb := a.Luminance(), b.Luminance()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// Palette is the roles of a theme as colours the views draw with.
type Palette struct {
	Theme surfaces.Theme
}

// Color is role's colour in the theme (the fallback theme's for a role the theme lacks), black when it is no #rrggbb.
func (p Palette) Color(role string) RGB {
	text, ok := p.Theme.Colors[role]
	if !ok {
		text = surfaces.FallbackThemes[p.Theme.Mode].Colors[role]
	}
	color, err := ParseHex(text)
	if err != nil {
		return RGB{}
	}
	return color
}

// aa is WCAG AA for normal text.
const aa = 4.5

// Secondary is the colour of secondary text on background role bg: the theme's muted colour when it reaches AA there, else
// its text colour. A view asks instead of choosing.
func (p Palette) Secondary(bg string) RGB { return p.readable("muted", bg) }

// Accented is the colour of accent-coloured text (a link, a name) on background role bg: the tokens' accentText (#006e55 in
// light, where the accent #008567 is 4.14:1 on soft) when it reaches AA there, else the text colour.
func (p Palette) Accented(bg string) RGB {
	if _, ok := p.Theme.Colors["accentText"]; ok {
		return p.readable("accentText", bg)
	}
	return p.readable("accent", bg) // a theme that is not the PiG one
}

func (p Palette) readable(role, bg string) RGB {
	if color := p.Color(role); Contrast(color, p.Color(bg)) >= aa {
		return color
	}
	return p.Color("fg")
}
