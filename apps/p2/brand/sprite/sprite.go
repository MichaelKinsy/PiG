// Package sprite is the PiG Standard sprite hook of the branded p2 web UI (pig additive: Pi has no sprites): the sprite the user
// chose with `/sprite` (coding/piglogin: the built-in variants and the extensions' registered ones, the selection saved in
// piglogin.StatePath) as SVGs rendered from its grid and palette, kept current by watching the state file. Only a branded
// executable passes a Source to hosts.ServerOptions.Sprite; the Stock server has no sprites.
package sprite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image/color"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/piglogin"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	nodeenv "github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/internal/configroot"
)

// fallbackInterval is how often the state file is read without a working watcher (its directory does not exist before the
// first selection).
const fallbackInterval = 5 * time.Second

// Render is a sprite as an SVG: one rect per horizontal run of one color in the sprite's grid, crisp edges, the grid as the
// viewBox so any whole multiple of its size shows whole pixels. Transparent pixels stay empty.
func Render(variant piglogin.Variant) []byte {
	pixels := piglogin.HeadPixels(variant)
	var out bytes.Buffer
	title := new(bytes.Buffer)
	_ = xml.EscapeText(title, []byte(variant.Name))
	fmt.Fprintf(&out, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="%s"><title>%s</title>`,
		piglogin.HeadWidth, piglogin.HeadHeight, attribute(variant.Name), title.String())
	for index := 0; index < len(pixels); {
		start := pixels[index]
		end := index + 1
		for end < len(pixels) && pixels[end].Y == start.Y && pixels[end].X == pixels[end-1].X+1 && pixels[end].Color == start.Color {
			end++
		}
		fmt.Fprintf(&out, `<rect x="%d" y="%d" width="%d" height="1" fill="%s"%s/>`, start.X, start.Y, end-index, hex6(start.Color), opacity(start.Color))
		index = end
	}
	out.WriteString("</svg>\n")
	return out.Bytes()
}

func attribute(text string) string {
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(text))
	return escaped.String()
}

func hex6(c color.RGBA) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

func opacity(c color.RGBA) string {
	if c.A == 255 {
		return ""
	}
	return fmt.Sprintf(` fill-opacity="%.3f"`, float64(c.A)/255)
}

// Theme is the page theme a banner is drawn for.
type Theme string

// The page themes. A sprite's wordmark colors are made for a terminal's dark background; the banner has no background of its
// own (outside the painted cells the page shows through, as the terminal does). On the dark page the banner is the terminal's
// pixels exactly; on the light page a wordmark color that does not reach 3:1 against the page is darkened (legible).
const (
	Light Theme = "light"
	Dark  Theme = "dark"
)

// LightBackground and DarkBackground are the page backgrounds the banner is drawn for (web/brand/tokens.json `bg`; a test ties
// them).
const (
	LightBackground = "#ffffff"
	DarkBackground  = "#101c1a"
)

// Page is the background color of a theme's page.
func Page(theme Theme) color.RGBA {
	if theme == Light {
		return parseHex(LightBackground)
	}
	return parseHex(DarkBackground)
}

// MinWordmarkContrast is the contrast (WCAG ratio, large display text) a wordmark color keeps against the page where it can.
const MinWordmarkContrast = 3.0

// legible is c, or c with its OKLCH lightness lowered (hue and chroma kept, clipped to sRGB) just far enough to reach
// MinWordmarkContrast against the page: the hue and the order of a gradient survive, a color that reads already is not touched.
// Lowering lightness only helps against a light page; against a dark one (no darker color reaches the ratio) c stays as the
// terminal draws it.
func legible(c, page color.RGBA) color.RGBA {
	if contrast(c, page) >= MinWordmarkContrast {
		return c
	}
	l, chroma, hue := toOKLCH(c)
	best := fromOKLCH(0, chroma, hue)
	if contrast(best, page) < MinWordmarkContrast {
		return c
	}
	low, high := 0.0, l // contrast grows as lightness falls: the largest lightness that reaches it
	for range 24 {
		mid := (low + high) / 2
		candidate := fromOKLCH(mid, chroma, hue)
		if contrast(candidate, page) >= MinWordmarkContrast {
			best, low = candidate, mid
		} else {
			high = mid
		}
	}
	return best
}

func srgbToLinear(value uint8) float64 {
	v := float64(value) / 255
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func linearToSRGB(v float64) uint8 {
	v = math.Max(0, math.Min(1, v))
	if v <= 0.0031308 {
		v *= 12.92
	} else {
		v = 1.055*math.Pow(v, 1/2.4) - 0.055
	}
	return uint8(math.Round(v * 255))
}

// toOKLCH converts an sRGB color to OKLab's polar form (Björn Ottosson's matrices).
func toOKLCH(c color.RGBA) (l, chroma, hue float64) {
	r, g, b := srgbToLinear(c.R), srgbToLinear(c.G), srgbToLinear(c.B)
	lc := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	mc := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	sc := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	l = 0.2104542553*lc + 0.7936177850*mc - 0.0040720468*sc
	a := 1.9779984951*lc - 2.4285922050*mc + 0.4505937099*sc
	bb := 0.0259040371*lc + 0.7827717662*mc - 0.8086757660*sc
	return l, math.Hypot(a, bb), math.Atan2(bb, a)
}

func fromOKLCH(l, chroma, hue float64) color.RGBA {
	a, b := chroma*math.Cos(hue), chroma*math.Sin(hue)
	lc := math.Pow(l+0.3963377774*a+0.2158037573*b, 3)
	mc := math.Pow(l-0.1055613458*a-0.0638541728*b, 3)
	sc := math.Pow(l-0.0894841775*a-1.2914855480*b, 3)
	return color.RGBA{
		R: linearToSRGB(4.0767416621*lc - 3.3077115913*mc + 0.2309699292*sc),
		G: linearToSRGB(-1.2684380046*lc + 2.6097574011*mc - 0.3413193965*sc),
		B: linearToSRGB(-0.0041960863*lc - 0.7034186147*mc + 1.7076147010*sc),
		A: 255,
	}
}

func parseHex(text string) color.RGBA {
	var r, g, b uint8
	_, _ = fmt.Sscanf(strings.ToLower(text), "#%02x%02x%02x", &r, &g, &b)
	return color.RGBA{R: r, G: g, B: b, A: 255}
}

func luminance(c color.RGBA) float64 {
	channel := func(value uint8) float64 {
		v := float64(value) / 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(c.R) + 0.7152*channel(c.G) + 0.0722*channel(c.B)
}

// contrast is the WCAG contrast ratio of two colors.
func contrast(a, b color.RGBA) float64 {
	hi, lo := luminance(a), luminance(b)
	if lo > hi {
		hi, lo = lo, hi
	}
	return (hi + 0.05) / (lo + 0.05)
}

// Banner grid: the sprite, a gap, the wordmark, each inside a margin.
const (
	bannerMargin = 2
	bannerGap    = 2
	// BannerWidth and BannerHeight are the banner's grid in cells (its SVG viewBox).
	BannerWidth  = bannerMargin + extension.LoginMascotWidth + bannerGap + extension.LoginHeroWidth + bannerMargin
	BannerHeight = bannerMargin + extension.LoginMascotHeight + bannerMargin
)

// Cell is one painted cell of the banner's BannerWidth x BannerHeight grid.
type Cell struct {
	X, Y  int
	Color color.RGBA
}

// BannerCells are the painted cells of the empty state's banner for a page background, row-major: the sprite's pig beside its
// "PiG." wordmark (the sprite's login definition, the same grids and palette the terminal header draws), each inside the
// banner's margin. Nothing else is painted: the page shows through, as a terminal's background does. The pig is the sprite's own
// palette; a wordmark letter that would not reach 3:1 against the page is darkened in its hue (legible); the drop shadow is
// never changed. Native clients draw these same cells.
func BannerCells(variant piglogin.Variant, page color.RGBA) []Cell {
	definition := piglogin.LoginDefinitionFor(variant)
	heroText := strings.Join(definition.Hero, "")
	cells := make([]Cell, 0, 400)
	rowCells := func(y int, mascot, hero string) {
		add := func(grid string, offsetX int, wordmark bool) {
			for x := range len(grid) {
				symbol := grid[x]
				value, ok := definition.Palette[string(symbol)]
				if symbol == '.' || !ok {
					continue
				}
				fill := parseHex(value)
				if wordmark && symbol != '#' && strings.IndexByte(heroText, symbol) >= 0 {
					fill = legible(fill, page)
				}
				cells = append(cells, Cell{X: offsetX + x, Y: bannerMargin + y, Color: fill})
			}
		}
		add(mascot, bannerMargin, false)
		add(hero, bannerMargin+extension.LoginMascotWidth+bannerGap, true)
	}
	for y := range max(len(definition.Mascot), len(definition.Hero)) {
		var mascot, hero string
		if y < len(definition.Mascot) {
			mascot = definition.Mascot[y]
		}
		if y < len(definition.Hero) {
			hero = definition.Hero[y]
		}
		rowCells(y, mascot, hero)
	}
	return cells
}

// RenderBanner is BannerCells for a page theme as an SVG on the banner's grid (viewBox BannerWidth x BannerHeight), a rect per
// horizontal run of one color, crisp edges, no background.
func RenderBanner(variant piglogin.Variant, theme Theme) []byte {
	var out bytes.Buffer
	fmt.Fprintf(&out, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="%s"><title>%s</title>`,
		BannerWidth, BannerHeight, attribute(variant.Name), attribute(variant.Name))
	cells := BannerCells(variant, Page(theme))
	for index := 0; index < len(cells); {
		start := cells[index]
		end := index + 1
		for end < len(cells) && cells[end].Y == start.Y && cells[end].X == cells[end-1].X+1 && cells[end].Color == start.Color {
			end++
		}
		fmt.Fprintf(&out, `<rect x="%d" y="%d" width="%d" height="1" fill="%s"/>`, start.X, start.Y, end-index, hex6(start.Color))
		index = end
	}
	out.WriteString("</svg>\n")
	return out.Bytes()
}

// Asset is the sprite with the given id as an asset: its key is the id and a digest of the SVGs, so a sprite an extension
// redefines under the same id is a new key. false for an id no sprite has (an extension's that is not loaded here).
func Asset(id string) (hosts.SpriteAsset, bool) {
	variant, ok := piglogin.ByID(id)
	if !ok {
		return hosts.SpriteAsset{}, false
	}
	svg, light, dark := Render(variant), RenderBanner(variant, Light), RenderBanner(variant, Dark)
	digest := sha256.New()
	digest.Write(svg)
	digest.Write(light)
	digest.Write(dark)
	return hosts.SpriteAsset{Key: variant.ID + "-" + hex.EncodeToString(digest.Sum(nil)[:4]), Name: variant.Name, Tagline: variant.Tagline, SVG: svg, LoginLight: light, LoginDark: dark}, true
}

// Source watches the saved selection under a config root and serves it as a hosts.SpriteSource.
type Source struct {
	home     string
	observer *p2runtime.Observer

	mu        sync.Mutex
	asset     hosts.SpriteAsset
	has       bool
	listeners map[int]func()
	next      int
}

// Open starts watching the selection saved under configHome (piglogin.StatePath). There is none until the user chooses one.
func Open(configHome string) (*Source, error) {
	source := &Source{home: configHome, listeners: map[int]func(){}}
	path := piglogin.StatePath(configHome)
	files := []p2runtime.ObservedFiles{{
		Env: nodeenv.NewNodeExecutionEnv(nodeenv.NodeExecutionEnvOptions{Cwd: configHome}),
		Targets: func(context.Context) ([]durableenv.WatchTarget, error) {
			return []durableenv.WatchTarget{{Path: path}}, nil
		},
	}}
	source.observer = p2runtime.NewObserver(files, source.load, fallbackInterval)
	if err := source.observer.Start(context.Background()); err != nil {
		source.observer.Close()
		return nil, err
	}
	return source, nil
}

// selected is the saved sprite's id, "" when none is saved or the file is unreadable (a half-written file is read again).
func selected(home string) (id string, retry bool) {
	data, err := os.ReadFile(piglogin.StatePath(home))
	if err != nil {
		return "", false
	}
	var saved struct {
		Variant string `json:"variant"`
	}
	if json.Unmarshal(data, &saved) != nil {
		return "", true
	}
	return saved.Variant, false
}

func (s *Source) load(_ context.Context, _ bool) bool {
	id, retry := selected(s.home)
	// As the terminal header: no selection, or one whose extension is not loaded, draws the default sprite.
	asset, ok := Asset(id)
	if !ok {
		asset, ok = Asset(piglogin.DefaultID)
	}
	s.mu.Lock()
	changed := ok != s.has || asset.Key != s.asset.Key
	s.asset, s.has = asset, ok
	var notify []func()
	if changed {
		for _, fn := range s.listeners {
			notify = append(notify, fn)
		}
	}
	s.mu.Unlock()
	for _, fn := range notify {
		fn()
	}
	return retry
}

// Current is the selected sprite, the default one before a choice is made or while the chosen sprite's extension is not loaded.
func (s *Source) Current() (hosts.SpriteAsset, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.asset, s.has
}

// OnChange registers fn for selection changes.
func (s *Source) OnChange(fn func()) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.next
	s.next++
	s.listeners[id] = fn
	return func() {
		s.mu.Lock()
		delete(s.listeners, id)
		s.mu.Unlock()
	}
}

// Close stops watching.
func (s *Source) Close() { s.observer.Close() }

// DefaultHome is the config root PiG reads its selection from (PIG_HOME, else XDG_CONFIG_HOME/pig, else ~/.pig).
func DefaultHome() (string, error) { return configroot.Resolve() }
