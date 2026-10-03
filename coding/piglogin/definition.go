package piglogin

import (
	"fmt"
	"image/color"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

const loginDescription = "Command-line coding agent"

// Native login template grid sizes.
const (
	brandWidth  = extension.LoginBrandWidth
	brandHeight = extension.LoginBrandHeight
)

// LoginDefinitionFor expresses a sprite through the native login contract (ctx.ui.setLogin): the hero "PiG." wordmark,
// the mascot pig and the palette that colors them. The built-in startup header draws its art from this definition, and
// an extension that sets the same definition draws the same pixels.
func LoginDefinitionFor(variant Variant) extension.LoginDefinition {
	// The brand band stays transparent: the large hero wordmark is the only
	// logo, and the host omits an empty brand band.
	brand := make([]string, brandHeight)
	for i := range brand {
		brand[i] = strings.Repeat(".", brandWidth)
	}

	hero, palette := heroFor(LogoFor(variant))

	// The mascot shares the palette with the hero. A mascot symbol the hero uses (an extension's sprite may use any
	// symbol) moves to a free symbol, so it neither recolors the wordmark nor takes its color.
	sprite, colors := MascotSpriteFor(variant), paletteFor(variant)
	rename := map[byte]byte{}
	taken := func(symbol byte) bool {
		if _, ok := palette[string(symbol)]; ok {
			return true
		}
		for _, row := range sprite {
			if strings.IndexByte(row, symbol) >= 0 {
				return true
			}
		}
		for _, to := range rename {
			if to == symbol {
				return true
			}
		}
		return false
	}
	mascot := make([]string, len(sprite))
	for y, row := range sprite {
		pixels := []byte(row)
		for i, symbol := range pixels {
			if symbol == '.' {
				continue
			}
			if _, clash := palette[string(symbol)]; clash {
				if _, ok := rename[symbol]; !ok {
					for free := byte('!'); free <= '~'; free++ {
						if free != '.' && !taken(free) {
							rename[symbol] = free
							break
						}
					}
				}
				pixels[i] = rename[symbol]
			}
		}
		mascot[y] = string(pixels)
	}
	for _, row := range sprite {
		for i := range len(row) {
			symbol := row[i]
			value, ok := colors[symbol]
			if symbol == '.' || !ok || value.A == 0 {
				continue
			}
			if to, renamed := rename[symbol]; renamed {
				symbol = to
			}
			palette[string(symbol)] = rgbaHex(value)
		}
	}

	return extension.LoginDefinition{
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
// logo ramp can shade the wordmark from top to bottom. The hero shares the
// palette with the mascot, whose symbols are letters, so the hero uses digits
// and punctuation only.
const (
	heroWidth     = extension.LoginHeroWidth
	heroHeight    = extension.LoginHeroHeight
	heroRamp      = "123456789+-="
	heroShadow    = '#'
	heroPeriodKey = '@'
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
