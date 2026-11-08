package sprite

import (
	"encoding/xml"
	"image/color"
	"regexp"
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

func TestAssetCarriesTheSpritesName(t *testing.T) {
	asset, ok := Asset("pink")
	if !ok || asset.Name != "Classic PiG" || len(asset.SVG) == 0 {
		t.Fatalf("%+v", asset)
	}
}
