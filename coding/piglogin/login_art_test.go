package piglogin

// The art tests of PiG Standard's piglogin (extension_test.go and login_art_test.go at MichaelKinsy/PiG d86eb93, carried by
// Pigpen's pig-login and pig-play/libraries/sprite). Renamed identifiers only: the default sprite is pig-default (was
// hpe-agentic), loadVariant/saveVariant/variantByID/defaultVariant/defaultVariantID are LoadVariant/SaveVariant/ByID/
// Default/DefaultID, ActiveVariant(root) is LoadVariant(root), and the goldens are testdata/login-pig-default-*.golden,
// byte-identical to the original login-hpe-agentic-*.golden. TestVariantStateDefaultsAndPersists and
// TestUnknownSavedVariantFallsBackToDefault are state_test.go's TestVariantStateDefaultsAndPersists and
// TestUnreadableSavedSelectionFallsBackToDefault. TestEveryVariantSpriteIsTheSharedGrid, TestFindVariantFallsBackToDefault
// and TestEveryVariantPaletteCoversItsSprite are pig-play's additions.

import (
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// websiteArt is testdata/website-art.txt: colors sampled from the PiG
// website art, with the SHA-256 of each source file in its header.
type websiteArt struct {
	logo map[string]color.RGBA
	pig  [][]color.RGBA
}

func loadWebsiteArt(t *testing.T) websiteArt {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "website-art.txt"))
	if err != nil {
		t.Fatal(err)
	}
	art := websiteArt{logo: map[string]color.RGBA{}}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		colors := make([]color.RGBA, 0, len(fields)-1)
		for _, hex := range fields[1:] {
			value, err := strconv.ParseUint(hex, 16, 32)
			if err != nil || len(hex) != 6 {
				t.Fatalf("website-art.txt: bad color %q", hex)
			}
			colors = append(colors, color.RGBA{uint8(value >> 16), uint8(value >> 8), uint8(value), 0xFF})
		}
		if fields[0] == "pig" {
			art.pig = append(art.pig, colors)
		} else if len(colors) == 1 {
			art.logo[fields[0]] = colors[0]
		}
	}
	if len(art.pig) != 12 || len(art.pig[0]) != 14 || len(art.logo) != 4 {
		t.Fatalf("website-art.txt: %d pig rows, %d logo colors", len(art.pig), len(art.logo))
	}
	return art
}

func TestDefaultVariantIsTheWebsiteGreenPig(t *testing.T) {
	if Variants[0].ID != "pig-default" || DefaultID != "pig-default" {
		t.Fatalf("default variant = %q (Variants[0] = %q), want pig-default", DefaultID, Variants[0].ID)
	}
	if got := LoadVariant(t.TempDir()); got.ID != "pig-default" {
		t.Fatalf("fresh config loads %q, want pig-default", got.ID)
	}
	if got := LoadVariant(t.TempDir()); got.ID != "pig-default" {
		t.Fatalf("ActiveVariant on a fresh config = %q, want pig-default", got.ID)
	}
}

func TestPigDefaultResolvesByIDAndFromSavedState(t *testing.T) {
	if got := FindVariant("pig-default"); got.ID != "pig-default" || got.Logo == nil {
		t.Fatalf("FindVariant(pig-default) = %q", got.ID)
	}
	if _, ok := ByID("pig-default"); !ok {
		t.Fatal("/sprite set pig-default would be rejected")
	}
	root := t.TempDir()
	if err := SaveVariant(root, "pink"); err != nil {
		t.Fatal(err)
	}
	if got := LoadVariant(root); got.ID != "pink" {
		t.Fatalf("saved pink loads %q; other variants must stay selectable", got.ID)
	}
	if err := SaveVariant(root, "pig-default"); err != nil {
		t.Fatal(err)
	}
	if got := LoadVariant(root); got.ID != "pig-default" {
		t.Fatalf("saved pig-default loads %q", got.ID)
	}
	if !strings.Contains(spriteList(), "pig-default: PiG: ") {
		t.Fatalf("sprite list lacks pig-default:\n%s", spriteList())
	}
}

// The website pig is a 14-by-12 pixel grid. The mascot grid is the same pig
// with a one-pixel transparent margin, and every cell matches the sampled
// website color.
func TestPigDefaultMascotMatchesWebsitePig(t *testing.T) {
	art := loadWebsiteArt(t)
	variant := FindVariant("pig-default")
	palette := MascotPalette(variant)
	sprite := MascotSpriteFor(variant)
	for gy := range 12 {
		for gx := range 14 {
			symbol := sprite[gy+1][gx+1]
			want, opaque := palette[symbol]
			if symbol == '.' || !opaque {
				continue
			}
			if got := art.pig[gy][gx]; distance(got, want) > 6 {
				t.Fatalf("cell (%d,%d) symbol %q = %s, website pig = %s", gx, gy, symbol, rgbaHex(want), rgbaHex(got))
			}
		}
	}
}

func distance(a, b color.RGBA) int {
	abs := func(v int) int { return max(v, -v) }
	return abs(int(a.R)-int(b.R)) + abs(int(a.G)-int(b.G)) + abs(int(a.B)-int(b.B))
}

func TestPigDefaultLogoMatchesWebsiteWordmark(t *testing.T) {
	art := loadWebsiteArt(t)
	logo := LogoFor(FindVariant("pig-default"))
	for _, tc := range []struct {
		name      string
		got, want color.RGBA
	}{
		{"period", logo.Period, art.logo["logo-period"]},
		{"shadow", logo.Shadow, art.logo["logo-ink"]},
	} {
		if tc.got != tc.want {
			t.Fatalf("%s = %s, website = %s", tc.name, rgbaHex(tc.got), rgbaHex(tc.want))
		}
	}
	for row, value := range logo.Ramp {
		if value != art.logo["logo-dark-text"] {
			t.Fatalf("letter row %d = %s, want the website dark-theme text %s", row, rgbaHex(value), rgbaHex(art.logo["logo-dark-text"]))
		}
	}
}

func TestOtherVariantLogosFollowTheirPig(t *testing.T) {
	for _, variant := range Variants[1:] {
		definition := LoginDefinitionFor(variant)
		if got, want := definition.Palette[string(heroPeriodKey)], rgbaHex(variant.Body); got != want {
			t.Fatalf("%s period = %s, want body %s", variant.ID, got, want)
		}
	}
}

func TestHeroDrawsPiGWithPeriodInsideTheGrid(t *testing.T) {
	definition := LoginDefinitionFor(Default())
	var periodCells int
	for y, row := range definition.Hero {
		periodCells += strings.Count(row, string(heroPeriodKey))
		if y >= logoRows+1 && strings.Trim(row, ".") != "" {
			t.Fatalf("hero row %d below the shadow draws pixels: %q", y, row)
		}
	}
	if periodCells != 9 {
		t.Fatalf("period has %d pixels, want a 3x3 dot", periodCells)
	}
	if !strings.Contains(definition.Hero[logoRows-1], strings.Repeat(string(heroPeriodKey), 3)) {
		t.Fatalf("period is not on the baseline row: %q", definition.Hero[logoRows-1])
	}
}

// Every set pixel of a glyph is drawn in its ramp color (the period in the period color), and the pixel one down and one right
// of it is the drop shadow unless another glyph pixel is there. No other cell is drawn. The rule is computed from the glyph
// bitmaps alone, for every wordmark variant.
func TestHeroPixelsAreTheGlyphsAndTheirDropShadow(t *testing.T) {
	for _, variant := range Variants {
		logo := LogoFor(variant)
		hero, palette := heroFor(logo)
		type cell struct{ x, y int }
		glyph := map[cell]heroGlyph{}
		rowOf := map[cell]int{}
		for _, g := range heroGlyphs {
			for y, row := range g.rows {
				for x := range len(row) {
					if row[x] == '1' {
						glyph[cell{g.x + x, g.y + y}] = g
						rowOf[cell{g.x + x, g.y + y}] = g.y + y
					}
				}
			}
		}
		if len(glyph) == 0 {
			t.Fatal("no glyph pixels")
		}
		for y, row := range hero {
			for x := range len(row) {
				c := cell{x, y}
				symbol := string(row[x])
				switch g, isGlyph := glyph[c]; {
				case isGlyph && g.period:
					if row[x] != heroPeriodKey || palette[symbol] != rgbaHex(logo.Period) {
						t.Errorf("%s: period pixel (%d,%d) is %q %s", variant.ID, x, y, symbol, palette[symbol])
					}
				case isGlyph:
					if want := rgbaHex(logo.Ramp[rowOf[c]]); palette[symbol] != want || row[x] == '.' || row[x] == heroShadow {
						t.Errorf("%s: glyph pixel (%d,%d) is %q %s, want ramp row %d %s", variant.ID, x, y, symbol, palette[symbol], rowOf[c], want)
					}
				default:
					_, shadowed := glyph[cell{x - 1, y - 1}]
					want := byte('.')
					if shadowed {
						want = heroShadow
					}
					if row[x] != want {
						t.Errorf("%s: cell (%d,%d) is %q, want %q", variant.ID, x, y, row[x], want)
					}
				}
			}
		}
		if palette[string(heroShadow)] != rgbaHex(logo.Shadow) {
			t.Errorf("%s: shadow color %s, want %s", variant.ID, palette[string(heroShadow)], rgbaHex(logo.Shadow))
		}
	}
}

// The hero and the mascot share one palette. A mascot symbol that is also a hero symbol (pigrogu's robe 'B' was the
// hero's eleventh ramp row) would recolor the wordmark, so every hero pixel keeps its logo color in every sprite.
func TestMascotSymbolsDoNotRecolorTheWordmark(t *testing.T) {
	for _, variant := range Variants {
		definition := LoginDefinitionFor(variant)
		hero, heroPalette := heroFor(LogoFor(variant))
		for _, row := range hero {
			for i := range len(row) {
				symbol := string(row[i])
				if symbol == "." {
					continue
				}
				if got, want := definition.Palette[symbol], heroPalette[symbol]; got != want {
					t.Fatalf("%s: hero symbol %q is %s, want the wordmark's %s", variant.ID, symbol, got, want)
				}
			}
		}
	}
}

func TestEveryVariantProducesCompleteLoginDefinition(t *testing.T) {
	for _, variant := range Variants {
		definition := LoginDefinitionFor(variant)
		if len(definition.Brand) != 5 || len(definition.Hero) != 14 || len(definition.Mascot) != 14 {
			t.Fatalf("%s grid heights = brand %d, hero %d, mascot %d", variant.ID, len(definition.Brand), len(definition.Hero), len(definition.Mascot))
		}
		for _, row := range definition.Brand {
			if len(row) != 41 {
				t.Fatalf("%s brand width = %d, want 41", variant.ID, len(row))
			}
		}
		for _, row := range definition.Hero {
			if len(row) != 32 {
				t.Fatalf("%s hero width = %d, want 32", variant.ID, len(row))
			}
		}
		for _, row := range definition.Mascot {
			if len(row) != 16 {
				t.Fatalf("%s mascot width = %d, want 16", variant.ID, len(row))
			}
		}
	}
}

func TestNoVariantDrawsTheSmallBrandWordmark(t *testing.T) {
	for _, variant := range Variants {
		definition := LoginDefinitionFor(variant)
		for y, row := range definition.Brand {
			if strings.Trim(row, ".") != "" {
				t.Fatalf("%s brand row %d draws pixels: %q", variant.ID, y, row)
			}
		}
	}
}

// Every catalogue entry keeps the shared 16-by-14 grid, so a game can crop and
// scale any sprite from the same source.
func TestEveryVariantSpriteIsTheSharedGrid(t *testing.T) {
	for _, variant := range Variants {
		rows := MascotSpriteFor(variant)
		if len(rows) != 14 {
			t.Fatalf("%s sprite height = %d, want 14", variant.ID, len(rows))
		}
		for y, row := range rows {
			if len(row) != 16 {
				t.Fatalf("%s sprite row %d width = %d, want 16", variant.ID, y, len(row))
			}
		}
	}
}

// An unknown or empty ID resolves to the default sprite, never to a zero value.
func TestFindVariantFallsBackToDefault(t *testing.T) {
	for _, id := range []string{"", "no-such-pig"} {
		if got := FindVariant(id); got.ID != DefaultID {
			t.Fatalf("FindVariant(%q) = %q, want %q", id, got.ID, DefaultID)
		}
	}
	if _, ok := ByID("no-such-pig"); ok {
		t.Fatal("ByID accepted an unknown sprite")
	}
}

// Every symbol a sprite draws resolves to a color, and '.' is transparent.
func TestEveryVariantPaletteCoversItsSprite(t *testing.T) {
	for _, variant := range Variants {
		palette := MascotPalette(variant)
		if palette['.'].A != 0 {
			t.Fatalf("%s: '.' is not transparent", variant.ID)
		}
		for y, row := range MascotSpriteFor(variant) {
			for x := range len(row) {
				if _, ok := palette[row[x]]; !ok {
					t.Fatalf("%s: symbol %q at (%d,%d) has no palette color", variant.ID, row[x], x, y)
				}
			}
		}
	}
}

// The default wordmark is PiG's own, not the classic blue Pi logo color.
func TestDefaultWordmarkIsNotTheClassicBlue(t *testing.T) {
	definition := LoginDefinitionFor(Default())
	for _, row := range definition.Hero {
		for i := range len(row) {
			if strings.EqualFold(definition.Palette[string(row[i])], "#67E8F9") {
				t.Fatalf("default hero row %q uses the classic blue wordmark color", row)
			}
		}
	}
}
