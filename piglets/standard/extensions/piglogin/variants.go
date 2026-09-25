package standardlogin

import (
	"image/color"
	"maps"
)

// Variant is one entry in the PiG sprite catalogue.
type Variant struct {
	ID               string
	Name             string
	Tagline          string
	Sprite           []string
	Body             color.RGBA
	Highlight        color.RGBA
	Snout            color.RGBA
	Blush            color.RGBA
	InnerEar         color.RGBA
	PaletteOverrides map[byte]color.RGBA
	// Logo colors the hero wordmark. Nil uses the classic gradient with the
	// period in the variant's body color.
	Logo *Logo
}

var pigMascotSheriff = []string{
	".....HHHHHH.....",
	"..OeHHHHHHHHeO..",
	".OeehhSSSShheeO.",
	"HHHHHHHHHHHHHHHH",
	".OPPPpPPPPpPPPO.",
	".OPPWWPPPPWWPPO.",
	".OPPWKPPPPKWPPO.",
	".OPbbPssssPbbPO.",
	".OPPPsKssKsPPPO.",
	".OPPPPssssPPPPO.",
	".OPPPPPPPPPPPPO.",
	"..OPPPPPPPPPPO..",
	"...OOOOOOOOOO...",
	"................",
}

// Variants is the live catalogue. The first entry is the default fallback.
// Every built-in variant is original PiG artwork.
var Variants = []Variant{
	// hpe-agentic is the green pixel pig and "PiG." wordmark from the PiG
	// website (the private platform repository). Pig colors are the per-cell
	// modal colors of public/pig-pixel-green.png. Logo colors come from
	// public/pig-logo.svg (ink #18302C and period #16866F) and the dark-theme
	// text color in app/pig-site.tsx (#E8F4F0), because terminals render the
	// wordmark on a dark background. testdata/website-art.txt holds the
	// sampled colors and each source file's SHA-256.
	{
		ID:        "hpe-agentic",
		Name:      "Agentic PiG",
		Tagline:   "Your tools. Your models. Your workflow.",
		Body:      rgb(0x48, 0xA3, 0x81),
		Highlight: rgb(0x64, 0xBE, 0x9E),
		Snout:     rgb(0x32, 0x77, 0x5E),
		Blush:     rgb(0x82, 0xDB, 0xBA),
		InnerEar:  rgb(0x48, 0xA3, 0x81),
		PaletteOverrides: map[byte]color.RGBA{
			'O': rgb(0x18, 0x15, 0x1D),
			'K': rgb(0x18, 0x15, 0x1D),
			'W': rgb(0xFE, 0xFE, 0xFE),
		},
		Logo: &Logo{
			Ramp:   flatRamp(rgb(0xE8, 0xF4, 0xF0)),
			Shadow: rgb(0x18, 0x30, 0x2C),
			Period: rgb(0x16, 0x86, 0x6F),
		},
	},
	{
		ID:        "pink",
		Name:      "Classic PiG",
		Tagline:   "Pink, polite, and patient.",
		Body:      rgb(0xFF, 0xA8, 0xB7),
		Highlight: rgb(0xFF, 0xC4, 0xCE),
		Snout:     rgb(0xE8, 0x83, 0x96),
		Blush:     rgb(0xFF, 0x86, 0x9A),
		InnerEar:  rgb(0xFF, 0x90, 0xA4),
	},
	{
		ID:        "green",
		Name:      "Green PiG",
		Tagline:   "Ready to build.",
		Body:      rgb(0x16, 0xA3, 0x6A),
		Highlight: rgb(0x45, 0xC8, 0x91),
		Snout:     rgb(0x0F, 0x7A, 0x50),
		Blush:     rgb(0x77, 0xD9, 0xAC),
		InnerEar:  rgb(0x16, 0xA3, 0x6A),
	},
	{
		ID:        "mint",
		Name:      "Mint PiG",
		Tagline:   "Refactors before breakfast.",
		Body:      rgb(0xC8, 0xF0, 0xDD),
		Highlight: rgb(0xDF, 0xF6, 0xEB),
		Snout:     rgb(0x96, 0xD0, 0xB4),
		Blush:     rgb(0xF4, 0x9A, 0xA6),
		InnerEar:  rgb(0xB8, 0xE4, 0xCF),
	},
	{
		ID:        "sandy",
		Name:      "Sandy PiG",
		Tagline:   "All tabs, no spaces.",
		Body:      rgb(0xE8, 0xC8, 0x9A),
		Highlight: rgb(0xF4, 0xDB, 0xAF),
		Snout:     rgb(0xC0, 0x99, 0x66),
		Blush:     rgb(0xE8, 0x83, 0x96),
		InnerEar:  rgb(0xD8, 0xAC, 0x78),
	},
	{
		ID:        "grey",
		Name:      "Slate PiG",
		Tagline:   "Reads RFCs for fun.",
		Body:      rgb(0xB6, 0xB2, 0xC0),
		Highlight: rgb(0xD2, 0xCF, 0xDB),
		Snout:     rgb(0x88, 0x84, 0x95),
		Blush:     rgb(0xE8, 0x83, 0x96),
		InnerEar:  rgb(0xA2, 0x9E, 0xB0),
	},
	{
		ID:        "blush",
		Name:      "Rosy PiG",
		Tagline:   "Compiles with feelings.",
		Body:      rgb(0xFF, 0xB8, 0xC9),
		Highlight: rgb(0xFF, 0xD4, 0xDD),
		Snout:     rgb(0xE8, 0x6E, 0x88),
		Blush:     rgb(0xFF, 0x52, 0x6E),
		InnerEar:  rgb(0xFF, 0x8C, 0xA2),
	},
	{
		ID:        "lavender",
		Name:      "Lavender PiG",
		Tagline:   "Dreams in async.",
		Body:      rgb(0xC9, 0xB4, 0xEA),
		Highlight: rgb(0xDD, 0xCC, 0xF2),
		Snout:     rgb(0x9F, 0x82, 0xC4),
		Blush:     rgb(0xE8, 0x83, 0x96),
		InnerEar:  rgb(0xB6, 0x9D, 0xDC),
	},
	{
		ID:        "cloud",
		Name:      "Cloud PiG",
		Tagline:   "Lives at 99.99% uptime.",
		Body:      rgb(0xB0, 0xD8, 0xEC),
		Highlight: rgb(0xCC, 0xE8, 0xF4),
		Snout:     rgb(0x7C, 0xAE, 0xC8),
		Blush:     rgb(0xF4, 0x9A, 0xA6),
		InnerEar:  rgb(0x9C, 0xCA, 0xE0),
	},
	{
		ID:        "sheriff",
		Name:      "Sheriff PiG",
		Tagline:   "Laying down the law, one commit at a time.",
		Sprite:    pigMascotSheriff,
		Body:      rgb(0xF2, 0xB8, 0xA8),
		Highlight: rgb(0xFF, 0xD2, 0xC7),
		Snout:     rgb(0xD9, 0x89, 0x78),
		Blush:     rgb(0xD9, 0x5F, 0x4C),
		InnerEar:  rgb(0xE7, 0x98, 0x88),
		PaletteOverrides: map[byte]color.RGBA{
			'H': rgb(0x6B, 0x4C, 0x3A),
			'h': rgb(0x8B, 0x6F, 0x47),
			'S': rgb(0xFF, 0xD7, 0x00),
		},
	},
}

func rgb(r, g, b uint8) color.RGBA {
	return color.RGBA{R: r, G: g, B: b, A: 0xFF}
}

func flatRamp(value color.RGBA) [logoRows]color.RGBA {
	var ramp [logoRows]color.RGBA
	for i := range ramp {
		ramp[i] = value
	}
	return ramp
}

func defaultVariant() Variant { return Variants[0] }

// LogoFor returns the wordmark colors for a variant.
func LogoFor(variant Variant) Logo {
	if variant.Logo != nil {
		return *variant.Logo
	}
	return Logo{Ramp: classicLogoRamp, Shadow: classicLogoShadow, Period: variant.Body}
}

// ActiveVariant returns the sprite selected with /sprite under configHome, or
// the default sprite when none is saved.
func ActiveVariant(configHome string) Variant { return loadVariant(configHome) }

// MascotPalette returns the resolved colors for every mascot symbol of a
// variant. '.' is transparent.
func MascotPalette(variant Variant) map[byte]color.RGBA { return paletteFor(variant) }

// FindVariant looks up by ID. It returns the default when the ID is unknown.
func FindVariant(id string) Variant {
	for _, variant := range Variants {
		if variant.ID == id {
			return variant
		}
	}
	return defaultVariant()
}

// MascotSpriteFor returns the variant-specific mascot or the standard mascot.
func MascotSpriteFor(variant Variant) []string {
	if len(variant.Sprite) > 0 {
		return variant.Sprite
	}
	return pigMascot
}

func paletteFor(variant Variant) map[byte]color.RGBA {
	palette := make(map[byte]color.RGBA, len(mascotPaletteBase))
	maps.Copy(palette, mascotPaletteBase)
	palette['P'] = variant.Body
	palette['p'] = variant.Highlight
	palette['s'] = variant.Snout
	palette['b'] = variant.Blush
	palette['e'] = variant.InnerEar
	maps.Copy(palette, variant.PaletteOverrides)
	return palette
}
