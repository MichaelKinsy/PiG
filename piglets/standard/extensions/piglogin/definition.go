package standardlogin

import (
	"fmt"
	"image/color"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const loginDescription = "Command-line coding agent"

// Native login template grid sizes.
const (
	brandWidth  = 41
	brandHeight = 5
)

// LoginDefinitionFor expresses a PiG Standard sprite through the public native
// login contract.
func LoginDefinitionFor(variant Variant) sdk.LoginDefinition {
	// The brand band stays transparent: the large hero wordmark is the only
	// logo, and the host omits an empty brand band.
	brand := make([]string, brandHeight)
	for i := range brand {
		brand[i] = strings.Repeat(".", brandWidth)
	}

	hero, palette := heroFor(LogoFor(variant))

	mascot := append([]string(nil), MascotSpriteFor(variant)...)
	usedMascotSymbols := make(map[byte]struct{})
	for _, row := range mascot {
		for i := range len(row) {
			usedMascotSymbols[row[i]] = struct{}{}
		}
	}
	for symbol, value := range paletteFor(variant) {
		if symbol == '.' || value.A == 0 {
			continue
		}
		if _, used := usedMascotSymbols[symbol]; !used {
			continue
		}
		palette[string(symbol)] = rgbaHex(value)
	}

	return sdk.LoginDefinition{
		Brand:       brand,
		Hero:        hero,
		Mascot:      mascot,
		Palette:     palette,
		Name:        variant.Name,
		Description: loginDescription,
		Tagline:     variant.Tagline,
	}
}

// Hero grid size and palette symbols. Each letter row has its own symbol so a
// logo ramp can shade the wordmark from top to bottom.
const (
	heroWidth     = 32
	heroHeight    = 14
	heroRamp      = "123456789ABC"
	heroShadow    = 'D'
	heroPeriodKey = 'Q'
)

// heroFor draws the "PiG." wordmark with a one-pixel drop shadow.
func heroFor(logo Logo) ([]string, map[string]string) {
	pixels := make([][]byte, heroHeight)
	for y := range pixels {
		pixels[y] = []byte(strings.Repeat(".", heroWidth))
	}
	for _, glyph := range heroGlyphs {
		for y, row := range glyph.rows {
			for x := range len(row) {
				if row[x] == '1' {
					pixels[glyph.y+y+1][glyph.x+x+1] = heroShadow
				}
			}
		}
	}
	palette := map[string]string{string(heroShadow): rgbaHex(logo.Shadow)}
	for _, glyph := range heroGlyphs {
		for y, row := range glyph.rows {
			symbol := heroRamp[glyph.y+y]
			if glyph.period {
				symbol = heroPeriodKey
			}
			for x := range len(row) {
				if row[x] == '1' {
					pixels[glyph.y+y][glyph.x+x] = symbol
					if glyph.period {
						palette[string(symbol)] = rgbaHex(logo.Period)
					} else {
						palette[string(symbol)] = rgbaHex(logo.Ramp[glyph.y+y])
					}
				}
			}
		}
	}
	hero := make([]string, len(pixels))
	for y, row := range pixels {
		hero[y] = string(row)
	}
	return hero, palette
}

func rgbaHex(value color.RGBA) string {
	return fmt.Sprintf("#%02X%02X%02X", value.R, value.G, value.B)
}
