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

// Plates the banner sits on: the page's dark surface and white. A sprite's wordmark colors are made for a dark header, so the
// banner carries its own plate and the palette stays legible whatever the page's theme.
const (
	darkPlate  = "#101c1a"
	lightPlate = "#ffffff"
)

// plateFor is the plate under the wordmark's colors: the one with the better mean contrast (WCAG ratio) over them. The mean, not
// the worst: the classic gradient runs from near-white to deep blue on purpose, and either plate loses its extreme rows.
func plateFor(letters []color.RGBA) string {
	if meanContrast(letters, parseHex(lightPlate)) > meanContrast(letters, parseHex(darkPlate)) {
		return lightPlate
	}
	return darkPlate
}

func meanContrast(letters []color.RGBA, plate color.RGBA) float64 {
	if len(letters) == 0 {
		return 0
	}
	total := 0.0
	for _, letter := range letters {
		total += contrast(letter, plate)
	}
	return total / float64(len(letters))
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

// Banner grid: the sprite, a gap, the wordmark, each inside a margin, on the plate.
const (
	bannerMargin = 2
	bannerGap    = 2
	// BannerWidth and BannerHeight are the banner's grid in cells (its SVG viewBox).
	BannerWidth  = bannerMargin + extension.LoginMascotWidth + bannerGap + extension.LoginHeroWidth + bannerMargin
	BannerHeight = bannerMargin + extension.LoginMascotHeight + bannerMargin
)

// RenderBanner is the empty state's banner: the sprite's pig beside its "PiG." wordmark (the sprite's login definition, the same
// grids and palette the terminal header draws) on a plate, in cells at whole pixels.
func RenderBanner(variant piglogin.Variant) []byte {
	definition := piglogin.LoginDefinitionFor(variant)
	var wordmark []color.RGBA
	heroText := strings.Join(definition.Hero, "")
	for symbol, value := range definition.Palette {
		if symbol != "#" && strings.Contains(heroText, symbol) { // the drop shadow is not a letter, a mascot symbol is not the wordmark
			wordmark = append(wordmark, parseHex(value))
		}
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="%s"><title>%s</title>`,
		BannerWidth, BannerHeight, attribute(variant.Name), attribute(variant.Name))
	fmt.Fprintf(&out, `<rect width="%d" height="%d" fill="%s"/>`, BannerWidth, BannerHeight, plateFor(wordmark))
	draw := func(grid []string, offsetX int) {
		for y, row := range grid {
			for x := 0; x < len(row); {
				symbol := row[x]
				value, ok := definition.Palette[string(symbol)]
				if symbol == '.' || !ok {
					x++
					continue
				}
				end := x + 1
				for end < len(row) && row[end] == symbol {
					end++
				}
				fmt.Fprintf(&out, `<rect x="%d" y="%d" width="%d" height="1" fill="%s"/>`, offsetX+x, bannerMargin+y, end-x, strings.ToLower(value))
				x = end
			}
		}
	}
	draw(definition.Mascot, bannerMargin)
	draw(definition.Hero, bannerMargin+extension.LoginMascotWidth+bannerGap)
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
	svg, banner := Render(variant), RenderBanner(variant)
	digest := sha256.Sum256(append(append([]byte{}, svg...), banner...))
	return hosts.SpriteAsset{Key: variant.ID + "-" + hex.EncodeToString(digest[:4]), Name: variant.Name, Tagline: variant.Tagline, SVG: svg, Login: banner}, true
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
