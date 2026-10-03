package piglogin

import (
	"image/color"
	"maps"
)

// Variant is one entry in the PiG sprite catalogue.
type Variant struct {
	ID      string
	Name    string
	Tagline string
	// Sprite is the 16-by-14 pig: the head the startup header draws and the mascot of /sprite preview. Nil draws the
	// standard pig.
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

// The character sprites of the owner's earlier PiG piglogin (variants.go): the mascots are its pixel art, unchanged.

var pigMascotPiGrogu = []string{
	"OO............OO",
	"OOO..........OOO",
	".OeeO......OeeO.",
	"..OeeOOOOOOeeO..",
	"..OPPPPPPPPPPO..",
	".OPPPpPPPPpPPPO.",
	".OPPKKPPPPKKPPO.",
	".OPPKWPPPPWKPPO.",
	".OPPPssssssPPPO.",
	".OPPPsKssKsPPPO.",
	".OPPPPssssPPPPO.",
	"..OPPPPPPPPPPO..",
	"..OBBBBBBBBBBO..",
	".OBbbbbbbbbbbBO.",
}

var pigMascotDarthVader = []string{
	"................",
	"...OOOO..OOOO...",
	"...OPPO..OPPO...",
	"..OPPPPPPPPPPO..",
	".OPPpPPPPPPpPPO.",
	".OPPbbbKKbbbPPO.",
	".OPPbbWssWbbPPO.",
	".OeePPssssPPeeO.",
	".OPPPssssssPPPO.",
	".OPPPsKssKsPPPO.",
	".OPPPPssssPPPPO.",
	"..OPPPPPPPPPPO..",
	"...OOOOOOOOOO...",
	"................",
}

var pigMascotKratos = []string{
	"................",
	"...OOOO..OOOO...",
	"...OeeO..OeeO...",
	"..OeeEEEERRReO..",
	".OEEEEEERRREEEO.",
	".OEEWWERRRWWEEO.",
	".OEEWKERRRKWEEO.",
	".OEERssssssEEEO.",
	".ORRRsKssKsEEEO.",
	".OERRRssssEEEEO.",
	".OEEERRREEEEEEO.",
	"..OEERRRRRREEO..",
	"...OOOOOOOOOO...",
	"................",
}

var pigMascotPiglet = []string{
	"..OOOO....OOOO..",
	".OeeeO....OeeeO.",
	".OeeeeO..OeeeeO.",
	"..OeeeOPPOeeeO..",
	"...OPPPPPPPPO...",
	".OPPWWPPPPWWPPO.",
	".OPPWKPPPPKWPPO.",
	".OPPPssssssPPPO.",
	".OPPPsKssKsPPPO.",
	".OPPPPPPPPPPPPO.",
	"..OPPPPKKPPPPO..",
	"..ORRRRRRRRRRO..",
	".OrrrrrrrrrrrrO.",
	"ORRRRRRRRRRRRRRO",
}

var pigMascotSpiderHam = []string{
	"................",
	"...ORrO..OrRO...",
	"...OrRO..ORrO...",
	"..ORRrRrrRrRRO..",
	".ORKKKrRRrKKKRO.",
	".OrKWWKRRKWWKrO.",
	".ORKWWKrrKWWKRO.",
	".OrRKKssssKKRrO.",
	".ORrRsKssKsRrRO.",
	".OrRrRssssRrRrO.",
	".ORrRrRrrRrRrRO.",
	"..ORrRrRRrRrRO..",
	"...OOOOOOOOOO...",
	"................",
}

// Variants is the built-in catalogue in /sprite order: the default first, then the color sprites, then the characters.
// The first entry is the default fallback. Every built-in variant is original PiG artwork.
var Variants = []Variant{
	// pig-default is the green pixel pig and "PiG." wordmark from the PiG
	// website. Pig colors are the per-cell
	// modal colors of public/pig-pixel-green.png. Logo colors come from
	// public/pig-logo.svg (ink #18302C and period #16866F) and the dark-theme
	// text color in app/pig-site.tsx (#E8F4F0), because terminals render the
	// wordmark on a dark background. testdata/website-art.txt holds the
	// sampled colors and each source file's SHA-256.
	{
		ID:        "pig-default",
		Name:      "PiG",
		Tagline:   "The minimal coding agent, in Go.",
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
		ID:        "pigrogu",
		Name:      "PiGrogu",
		Tagline:   "Passing tests, this is the way.",
		Sprite:    pigMascotPiGrogu,
		Body:      rgb(0x8B, 0xB5, 0x70),
		Highlight: rgb(0xB7, 0xD4, 0x96),
		Snout:     rgb(0x72, 0x99, 0x5B),
		Blush:     rgb(0xA8, 0xC9, 0x87),
		InnerEar:  rgb(0xC7, 0x8E, 0x94),
		PaletteOverrides: map[byte]color.RGBA{
			'B': rgb(0xB7, 0x98, 0x6D),
			'b': rgb(0x78, 0x5B, 0x3D),
		},
	},
	{
		ID:        "darth-vader",
		Name:      "Darth Vader",
		Tagline:   "I find your lack of tests disturbing.",
		Sprite:    pigMascotDarthVader,
		Body:      rgb(0x0B, 0x0C, 0x14),
		Highlight: rgb(0x2E, 0x31, 0x45),
		Snout:     rgb(0x22, 0x24, 0x32),
		Blush:     rgb(0xD8, 0x00, 0x00),
		InnerEar:  rgb(0x16, 0x18, 0x24),
		PaletteOverrides: map[byte]color.RGBA{
			'O': rgb(0x03, 0x03, 0x07),
			'K': rgb(0x00, 0x00, 0x03),
		},
	},
	{
		ID:      "kratos",
		Name:    "Kratos",
		Tagline: "War on bugs.",
		Sprite:  pigMascotKratos,
		// The original Kratos sets no body colors: its pig draws only its own symbols. Body is the ash 'E', so the
		// wordmark's period, which takes the body color, is the pig's skin rather than black.
		Body: rgb(0xD1, 0xC7, 0xB5),
		PaletteOverrides: map[byte]color.RGBA{
			'E': rgb(0xD1, 0xC7, 0xB5),
			'e': rgb(0xAD, 0xA1, 0x91),
			's': rgb(0xA6, 0x48, 0x32),
			'R': rgb(0x91, 0x1F, 0x1F),
			'W': rgb(0xB5, 0xBE, 0xBE),
			'O': rgb(0x16, 0x12, 0x11),
			'K': rgb(0x16, 0x12, 0x11),
		},
	},
	{
		ID:        "piglet",
		Name:      "Piglet",
		Tagline:   "A Very Small Animal with very large diffs.",
		Sprite:    pigMascotPiglet,
		Body:      rgb(0xF6, 0xA6, 0xB6),
		Highlight: rgb(0xFF, 0xC5, 0xCF),
		Snout:     rgb(0xE8, 0x6F, 0x88),
		Blush:     rgb(0xE8, 0x6F, 0x88),
		InnerEar:  rgb(0xF0, 0x6A, 0x8A),
		PaletteOverrides: map[byte]color.RGBA{
			'R': rgb(0xD9, 0x1F, 0x4E),
			'r': rgb(0xA8, 0x0F, 0x38),
		},
	},
	{
		ID:        "spider-ham",
		Name:      "Spider-Ham",
		Tagline:   "Does whatever a spider can.",
		Sprite:    pigMascotSpiderHam,
		Body:      rgb(0xE6, 0x24, 0x29),
		Highlight: rgb(0xFF, 0x4A, 0x4F),
		Snout:     rgb(0xA9, 0x13, 0x1B),
		Blush:     rgb(0xFF, 0x4A, 0x4F),
		InnerEar:  rgb(0xC8, 0x18, 0x20),
		PaletteOverrides: map[byte]color.RGBA{
			'R': rgb(0xE6, 0x24, 0x29),
			'r': rgb(0x7A, 0x11, 0x18),
			'O': rgb(0x12, 0x0B, 0x0D),
			'K': rgb(0x12, 0x0B, 0x0D),
		},
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

// LogoFor returns the wordmark colors for a variant.
func LogoFor(variant Variant) Logo {
	if variant.Logo != nil {
		return *variant.Logo
	}
	return Logo{Ramp: classicLogoRamp, Shadow: classicLogoShadow, Period: variant.Body}
}

// MascotPalette returns the resolved colors for every mascot symbol of a
// variant. '.' is transparent.
func MascotPalette(variant Variant) map[byte]color.RGBA { return paletteFor(variant) }

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

// Default returns the default sprite, the first catalogue entry.
func Default() Variant { return Variants[0] }

// ByID looks a sprite up by ID, among the built-in sprites and those extensions registered, and reports whether it exists.
func ByID(id string) (Variant, bool) {
	for _, variant := range Variants {
		if variant.ID == id {
			return variant, true
		}
	}
	return registeredByID(id)
}

// FindVariant looks up by ID. It returns the default when the ID is unknown.
func FindVariant(id string) Variant {
	if variant, ok := ByID(id); ok {
		return variant
	}
	return Default()
}
