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
	"flag"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite golden login renders")

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

// renderHeader mirrors the host's native login template (internal/codingagent
// RenderLoginHeader): a transparent brand band is omitted, the hero starts at
// column 0, the mascot at column 35, pixels pair into half-block cells after a
// two-cell margin, and widths below 66 fall back to stacked text.
func renderHeader(definition LoginDefinitionView, width int) []string {
	margin := "  "
	var lines []string
	if width >= 66 {
		scene := make([][]byte, heroHeight)
		for y := range scene {
			scene[y] = []byte(strings.Repeat(".", 51))
			copy(scene[y], definition.Hero[y])
			copy(scene[y][35:], definition.Mascot[y])
		}
		for y := 0; y < len(scene); y += 2 {
			var line strings.Builder
			line.WriteString(margin)
			for x := range 51 {
				top, topOK := definition.color(scene[y][x])
				bottom, bottomOK := definition.color(scene[y+1][x])
				switch {
				case topOK && bottomOK:
					fmt.Fprintf(&line, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", top.R, top.G, top.B, bottom.R, bottom.G, bottom.B)
				case topOK:
					fmt.Fprintf(&line, "\x1b[38;2;%d;%d;%dm▀", top.R, top.G, top.B)
				case bottomOK:
					fmt.Fprintf(&line, "\x1b[38;2;%d;%d;%dm▄", bottom.R, bottom.G, bottom.B)
				default:
					line.WriteByte(' ')
				}
				line.WriteString("\x1b[0m")
			}
			lines = append(lines, line.String())
		}
		lines = append(lines, "", margin+"\x1b[1m"+definition.Name+"\x1b[0m  "+definition.Description+"\x1b[0m", margin+"\x1b[3m"+definition.Tagline+"\x1b[0m")
		return lines
	}
	for _, value := range []string{definition.Name, definition.Description, definition.Tagline} {
		lines = append(lines, margin+value+"\x1b[0m")
	}
	return lines
}

// LoginDefinitionView is a resolved login definition for golden rendering.
type LoginDefinitionView struct {
	Hero, Mascot               []string
	Palette                    map[string]string
	Name, Description, Tagline string
}

func (d LoginDefinitionView) color(symbol byte) (color.RGBA, bool) {
	value, ok := d.Palette[string(symbol)]
	if symbol == '.' || !ok {
		return color.RGBA{}, false
	}
	parsed, err := strconv.ParseUint(value[1:], 16, 32)
	if err != nil {
		return color.RGBA{}, false
	}
	return color.RGBA{uint8(parsed >> 16), uint8(parsed >> 8), uint8(parsed), 0xFF}, true
}

func TestDefaultHeaderGoldenRenders(t *testing.T) {
	definition := LoginDefinitionFor(Default())
	view := LoginDefinitionView{
		Hero: definition.Hero, Mascot: definition.Mascot, Palette: definition.Palette,
		Name: definition.Name, Description: definition.Description, Tagline: definition.Tagline,
	}
	for _, width := range []int{50, 66, 120} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			got := strings.Join(renderHeader(view, width), "\n") + "\n"
			path := filepath.Join("testdata", fmt.Sprintf("login-pig-default-%d.golden", width))
			if *updateGolden {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Fatalf("width %d header differs from %s\ngot:\n%s", width, path, got)
			}
			if strings.Contains(got, "\x1b[38;2;103;232;249m") {
				t.Fatal("default header still uses the classic blue wordmark")
			}
		})
	}
}
