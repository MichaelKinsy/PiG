package sprite

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image/color"
	"math"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/piglogin"
)

type rect struct {
	X     int    `xml:"x,attr"`
	Y     int    `xml:"y,attr"`
	W     int    `xml:"width,attr"`
	H     int    `xml:"height,attr"`
	Fill  string `xml:"fill,attr"`
	Alpha string `xml:"fill-opacity,attr"`
}

type document struct {
	ViewBox string `xml:"viewBox,attr"`
	Render  string `xml:"shape-rendering,attr"`
	Title   string `xml:"title"`
	Rects   []rect `xml:"rect"`
}

func parse(t *testing.T, svg []byte) document {
	t.Helper()
	var parsed document
	if err := xml.Unmarshal(svg, &parsed); err != nil {
		t.Fatalf("not XML: %v\n%s", err, svg)
	}
	return parsed
}

// Every opaque pixel of the sprite's grid is in the SVG once, in its color, and nothing else is.
func TestRenderDrawsTheSpritesGridAndPalette(t *testing.T) {
	for _, id := range []string{"pig-default", "pink", "kratos", "sheriff", "spider-ham"} {
		variant, ok := piglogin.ByID(id)
		if !ok {
			t.Fatalf("no sprite %s", id)
		}
		parsed := parse(t, Render(variant))
		if parsed.ViewBox != "0 0 16 14" || parsed.Render != "crispEdges" || parsed.Title != variant.Name {
			t.Errorf("%s: %+v", id, parsed)
		}
		drawn := map[[2]int]string{}
		for _, r := range parsed.Rects {
			if r.H != 1 || r.W < 1 {
				t.Errorf("%s: rect %+v", id, r)
			}
			for x := r.X; x < r.X+r.W; x++ {
				if _, twice := drawn[[2]int{x, r.Y}]; twice {
					t.Errorf("%s: pixel %d,%d drawn twice", id, x, r.Y)
				}
				drawn[[2]int{x, r.Y}] = r.Fill + r.Alpha
			}
		}
		want := map[[2]int]string{}
		for _, pixel := range piglogin.HeadPixels(variant) {
			value := hex6(pixel.Color) + strings.TrimSpace(opacityOf(pixel.Color))
			want[[2]int{pixel.X, pixel.Y}] = value
		}
		if len(drawn) != len(want) {
			t.Errorf("%s: %d pixels drawn, grid has %d", id, len(drawn), len(want))
		}
		for at, value := range want {
			if drawn[at] != value {
				t.Errorf("%s: pixel %v is %q, want %q", id, at, drawn[at], value)
			}
		}
	}
}

func opacityOf(c color.RGBA) string {
	text := opacity(c)
	matched := regexp.MustCompile(`"([0-9.]+)"`).FindStringSubmatch(text)
	if matched == nil {
		return ""
	}
	return matched[1]
}

func TestDifferentSpritesAreDifferentAssets(t *testing.T) {
	pink, _ := Asset("pink")
	kratos, _ := Asset("kratos")
	if pink.Key == kratos.Key || string(pink.SVG) == string(kratos.SVG) {
		t.Fatal("two sprites rendered alike")
	}
	if !strings.HasPrefix(pink.Key, "pink-") {
		t.Errorf("key %q", pink.Key)
	}
	again, _ := Asset("pink")
	if again.Key != pink.Key {
		t.Error("a sprite's key is not stable")
	}
	if _, ok := Asset("no-such-sprite"); ok {
		t.Error("an unknown sprite has an asset")
	}
}

func waitFor(t *testing.T, changes <-chan struct{}) {
	t.Helper()
	select {
	case <-changes:
	case <-time.After(10 * time.Second):
		t.Fatal("no change within 10 s")
	}
}

// The selection under a config root drives the source: the default before one is chosen, the chosen sprite after, a new one
// when the choice changes, and the default again for a sprite no extension registered.
func TestSourceFollowsTheSavedSelection(t *testing.T) {
	home := t.TempDir()
	source, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	// As the terminal header: the default sprite until a choice is made.
	if initial, ok := source.Current(); !ok || !strings.HasPrefix(initial.Key, "pig-default-") {
		t.Fatalf("before any selection: %q %v", initial.Key, ok)
	}
	changes := make(chan struct{}, 8)
	cancel := source.OnChange(func() { changes <- struct{}{} })
	defer cancel()

	if err := piglogin.SaveVariant(home, "pink"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, changes)
	first, ok := source.Current()
	if want, _ := Asset("pink"); !ok || first.Key != want.Key {
		t.Fatalf("selected pink, got %q %v", first.Key, ok)
	}

	if err := piglogin.SaveVariant(home, "kratos"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, changes)
	second, _ := source.Current()
	if want, _ := Asset("kratos"); second.Key != want.Key || second.Key == first.Key {
		t.Fatalf("selected kratos, got %q", second.Key)
	}

	if err := piglogin.SaveVariant(home, "an-extension-sprite-that-is-not-loaded"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, changes)
	if fallback, ok := source.Current(); !ok || !strings.HasPrefix(fallback.Key, "pig-default-") {
		t.Fatalf("an unregistered sprite draws the default one: %q %v", fallback.Key, ok)
	}
}

// A selection that exists when the source opens is current at once.
func TestSourceReadsTheSelectionAtStart(t *testing.T) {
	home := t.TempDir()
	if err := piglogin.SaveVariant(home, "green"); err != nil {
		t.Fatal(err)
	}
	source, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	asset, ok := source.Current()
	if want, _ := Asset("green"); !ok || asset.Key != want.Key {
		t.Fatalf("got %q %v", asset.Key, ok)
	}
}

// wordmarkColors are the colors of the wordmark's letters in a sprite's login definition (the shadow excluded).
func wordmarkColors(variant piglogin.Variant) []color.RGBA {
	definition := piglogin.LoginDefinitionFor(variant)
	heroText := strings.Join(definition.Hero, "")
	var colors []color.RGBA
	for symbol, value := range definition.Palette {
		if symbol != "#" && strings.Contains(heroText, symbol) {
			colors = append(colors, parseHex(value))
		}
	}
	return colors
}

// The banner has no background of its own: it paints exactly the cells of the sprite's and the wordmark's grids, as the terminal
// does, in both themes, and the SVG draws those cells and no others.
func TestBannerPaintsOnlyTheSpritesCells(t *testing.T) {
	for _, variant := range piglogin.All() {
		definition := piglogin.LoginDefinitionFor(variant)
		for _, theme := range []Theme{Light, Dark} {
			cells := BannerCells(variant, Page(theme))
			want := 0
			for _, row := range definition.Mascot {
				for x := range len(row) {
					if _, ok := definition.Palette[string(row[x])]; row[x] != '.' && ok {
						want++
					}
				}
			}
			for _, row := range definition.Hero {
				for x := range len(row) {
					if _, ok := definition.Palette[string(row[x])]; row[x] != '.' && ok {
						want++
					}
				}
			}
			if len(cells) != want || want == 0 {
				t.Errorf("%s/%s: %d cells painted, the grids have %d", variant.ID, theme, len(cells), want)
			}
			parsed := parse(t, RenderBanner(variant, theme))
			if parsed.ViewBox != "0 0 54 18" {
				t.Errorf("%s/%s: viewBox %s", variant.ID, theme, parsed.ViewBox)
			}
			drawn := map[[2]int]string{}
			for _, r := range parsed.Rects {
				if r.W == BannerWidth && r.H == BannerHeight {
					t.Errorf("%s/%s: a full-size rect paints a background", variant.ID, theme)
				}
				for x := r.X; x < r.X+r.W; x++ {
					drawn[[2]int{x, r.Y}] = r.Fill
				}
			}
			if len(drawn) != len(cells) {
				t.Errorf("%s/%s: the SVG paints %d cells, BannerCells %d", variant.ID, theme, len(drawn), len(cells))
			}
			for _, cell := range cells {
				if drawn[[2]int{cell.X, cell.Y}] != hex6(cell.Color) {
					t.Errorf("%s/%s: cell %d,%d is %s, want %s", variant.ID, theme, cell.X, cell.Y, drawn[[2]int{cell.X, cell.Y}], hex6(cell.Color))
				}
			}
		}
	}
}

// On the light page every wordmark letter of every catalogue sprite reaches 3:1 against the page, in the hue it had (OKLCH
// lightness only); the pig and the drop shadow are the sprite's own colors; a color that read already is untouched.
func TestWordmarkIsLegibleOnTheLightPage(t *testing.T) {
	light := Page(Light)
	for _, variant := range piglogin.All() {
		definition := piglogin.LoginDefinitionFor(variant)
		heroText := strings.Join(definition.Hero, "")
		byCell := map[[2]int]color.RGBA{}
		for _, cell := range BannerCells(variant, light) {
			byCell[[2]int{cell.X, cell.Y}] = cell.Color
		}
		for y, row := range definition.Hero {
			for x := range len(row) {
				symbol := row[x]
				value, ok := definition.Palette[string(symbol)]
				if symbol == '.' || !ok {
					continue
				}
				original, got := parseHex(value), byCell[[2]int{bannerMargin + 16 + bannerGap + x, bannerMargin + y}]
				if symbol == '#' {
					if got != original {
						t.Errorf("%s: the drop shadow changed", variant.ID)
					}
					continue
				}
				if !strings.Contains(heroText, string(symbol)) {
					continue
				}
				if ratio := contrast(got, light); ratio < MinWordmarkContrast-0.01 {
					t.Errorf("%s: wordmark %s became %s, %.2f:1 on the light page", variant.ID, value, hex6(got), ratio)
				}
				if contrast(original, light) >= MinWordmarkContrast && got != original {
					t.Errorf("%s: %s read already and was changed to %s", variant.ID, value, hex6(got))
				}
				if got != original {
					_, chromaBefore, hueBefore := toOKLCH(original)
					_, _, hueAfter := toOKLCH(got)
					// A near-gray has no stable hue: compare hues only for colors with chroma.
					if chromaBefore > 0.04 && math.Abs(math.Remainder(hueBefore-hueAfter, 2*math.Pi)) > 0.12 {
						t.Errorf("%s: %s changed hue %.2f to %.2f rad", variant.ID, value, hueBefore, hueAfter)
					}
				}
			}
		}
	}
}

// On the dark page the banner is the terminal's pixels: a wordmark color is never darkened (it cannot help there), and no
// letter that read as designed is touched. Colors below 3:1 on the dark page stay as the sprite defines them (the logotype
// of a dark sprite is dark on purpose); the table lists them.
func TestWordmarkIsTheTerminalsOnTheDarkPage(t *testing.T) {
	dark := Page(Dark)
	for _, variant := range piglogin.All() {
		definition := piglogin.LoginDefinitionFor(variant)
		for _, cell := range BannerCells(variant, dark) {
			x, y := cell.X-(bannerMargin+16+bannerGap), cell.Y-bannerMargin
			if x < 0 || y < 0 || y >= len(definition.Hero) {
				continue
			}
			symbol := definition.Hero[y][x]
			if value, ok := definition.Palette[string(symbol)]; ok && symbol != '.' && parseHex(value) != cell.Color {
				t.Errorf("%s: wordmark color %s drawn as %s on the dark page", variant.ID, value, hex6(cell.Color))
			}
		}
	}
}

// TestWordmarkContrastTable writes the wordmark contrast table of every catalogue sprite to $P2_CONTRAST_OUT (markdown): each
// letter color before and after, on both pages.
func TestWordmarkContrastTable(t *testing.T) {
	out := os.Getenv("P2_CONTRAST_OUT")
	if out == "" {
		t.Skip("P2_CONTRAST_OUT is not set")
	}
	var table strings.Builder
	table.WriteString("| sprite | letter color | on light: as drawn | ratio | on dark: as drawn | ratio |\n|---|---|---|---|---|---|\n")
	light, dark := Page(Light), Page(Dark)
	for _, variant := range piglogin.All() {
		colors := wordmarkColors(variant)
		sort.Slice(colors, func(i, j int) bool { return hex6(colors[i]) < hex6(colors[j]) })
		colors = slices.CompactFunc(colors, func(a, b color.RGBA) bool { return a == b })
		for _, original := range colors {
			onLight, onDark := legible(original, light), legible(original, dark)
			fmt.Fprintf(&table, "| %s | %s | %s | %.2f | %s | %.2f |\n", variant.ID, hex6(original), hex6(onLight), contrast(onLight, light), hex6(onDark), contrast(onDark, dark))
		}
	}
	if err := os.WriteFile(out, []byte(table.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The page colors the banner adapts to are the brand tokens' (web/brand/tokens.json): one source for web and native.
func TestThemeColorsAreTheBrandTokens(t *testing.T) {
	raw, err := os.ReadFile("../../web/brand/tokens.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Light map[string]string `json:"light"`
		Dark  map[string]string `json:"dark"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	tokens := map[string]map[string]string{"light": file.Light, "dark": file.Dark}
	if tokens["light"]["bg"] != LightBackground || tokens["dark"]["bg"] != DarkBackground {
		t.Errorf("banner pages %s %s, tokens.json has %s %s", LightBackground, DarkBackground, tokens["light"]["bg"], tokens["dark"]["bg"])
	}
}

func TestAssetCarriesTheSpritesTexts(t *testing.T) {
	asset, ok := Asset("pink")
	if !ok || asset.Name != "Classic PiG" || asset.Tagline != "Pink, polite, and patient." || len(asset.LoginLight) == 0 || len(asset.LoginDark) == 0 {
		t.Fatalf("%+v", asset)
	}
}
