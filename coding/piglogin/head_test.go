package piglogin

import (
	"image/color"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Every sprite's header head is pinned by testdata/head-<id>.golden: its HeadRows lines in truecolor.
func TestHeadGoldens(t *testing.T) {
	for _, variant := range Variants {
		t.Run(variant.ID, func(t *testing.T) {
			got := strings.Join(HeadLines(variant, tui.TerminalColorModeTrueColor), "\n") + "\n"
			path := filepath.Join("testdata", "head-"+variant.ID+".golden")
			if *updateGolden {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Fatalf("%s head differs from %s\ngot:\n%q", variant.ID, path, got)
			}
		})
	}
}

// The head is one size for every sprite: the 16-by-14 pig, 16 cells by 7 lines, each cell a half block or a space.
func TestEveryHeadIsTheSharedGrid(t *testing.T) {
	if HeadWidth != 16 || HeadHeight != 14 || HeadCells != 16 || HeadRows != 7 {
		t.Fatalf("head = %dx%d pixels, %dx%d cells", HeadWidth, HeadHeight, HeadCells, HeadRows)
	}
	for _, variant := range Variants {
		head := HeadFor(variant)
		if len(head) != HeadHeight {
			t.Fatalf("%s head height = %d", variant.ID, len(head))
		}
		palette := MascotPalette(variant)
		for y, row := range head {
			if len(row) != HeadWidth {
				t.Fatalf("%s head row %d width = %d", variant.ID, y, len(row))
			}
			for x := range len(row) {
				if value, ok := palette[row[x]]; row[x] != '.' && (!ok || value.A == 0) {
					t.Fatalf("%s: head symbol %q at (%d,%d) has no color", variant.ID, row[x], x, y)
				}
			}
		}
		for mode, lines := range map[tui.TerminalColorMode][]string{tui.TerminalColorModeTrueColor: HeadLines(variant, tui.TerminalColorModeTrueColor), tui.TerminalColorMode256: HeadLines(variant, tui.TerminalColorMode256)} {
			if len(lines) != HeadRows {
				t.Fatalf("%s/%s: %d head lines", variant.ID, mode, len(lines))
			}
			for i, line := range lines {
				if w := widthx.VisibleWidth(line); w != HeadCells {
					t.Fatalf("%s/%s: head line %d is %d cells", variant.ID, mode, i, w)
				}
				if strings.Trim(stripSGR(line), "▀▄ ") != "" {
					t.Fatalf("%s/%s: head line %d draws something other than half blocks: %q", variant.ID, mode, i, stripSGR(line))
				}
			}
		}
	}
}

// The header's head is the sprite's own 16-by-14 pig, the pixels /sprite preview draws beside the wordmark: the standard
// pig for the colors and each character's original art.
func TestEveryHeadIsTheSpritesPig(t *testing.T) {
	for _, variant := range Variants {
		if got, want := HeadFor(variant), MascotSpriteFor(variant); !slices.Equal(got, want) {
			t.Errorf("%s: head differs from its pig", variant.ID)
		}
		if len(variant.Sprite) == 0 && !slices.Equal(HeadFor(variant), pigMascot) {
			t.Errorf("%s: a color variant does not draw the standard pig", variant.ID)
		}
	}
	for _, id := range []string{"pigrogu", "darth-vader", "kratos", "piglet", "spider-ham", "sheriff"} {
		variant, _ := ByID(id)
		if slices.Equal(HeadFor(variant), pigMascot) {
			t.Errorf("%s draws the standard pig instead of its own art", id)
		}
	}
}

func TestHeadsDiffer(t *testing.T) {
	seen := map[string]string{}
	for _, variant := range Variants {
		key := strings.Join(HeadLines(variant, tui.TerminalColorModeTrueColor), "\n")
		if other, dup := seen[key]; dup {
			t.Errorf("%s and %s draw the same head", variant.ID, other)
		}
		seen[key] = variant.ID
	}
}

func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// SameHead reports a sprite and its copy as the same head, and never two heads whose pixels differ: no pair of sprites,
// and no copy with one input of its head changed (a pixel of its grid, a color the grid draws or an override). Each
// change alters some sprite's head, and a changed wordmark leaves the head the same.
func TestSameHeadMatchesHeadPixels(t *testing.T) {
	changes := []func(*Variant){
		func(v *Variant) { v.Sprite[7] = strings.Replace(v.Sprite[7], "P", "s", 1) },
		func(v *Variant) { v.Body.R ^= 1 },
		func(v *Variant) { v.Highlight.G ^= 1 },
		func(v *Variant) { v.Snout.B ^= 1 },
		func(v *Variant) { v.Blush.R ^= 1 },
		func(v *Variant) { v.InnerEar.G ^= 1 },
		func(v *Variant) { v.PaletteOverrides = map[byte]color.RGBA{'O': {1, 2, 3, 0xFF}} },
	}
	altered := make([]bool, len(changes))
	variants := slices.Clone(Variants)
	for _, variant := range Variants {
		copyOf := func() Variant {
			copied := variant
			copied.Sprite = slices.Clone(MascotSpriteFor(variant))
			copied.PaletteOverrides = maps.Clone(variant.PaletteOverrides)
			return copied
		}
		if !SameHead(variant, copyOf()) {
			t.Fatalf("%s is not the same head as its copy", variant.ID)
		}
		renamed := copyOf()
		renamed.Name, renamed.Tagline, renamed.Logo = "renamed", "retold", &Logo{Period: color.RGBA{1, 2, 3, 0xFF}}
		if !SameHead(variant, renamed) {
			t.Fatalf("%s with another wordmark is not the same head", variant.ID)
		}
		for i, change := range changes {
			changed := copyOf()
			change(&changed)
			altered[i] = altered[i] || !slices.Equal(HeadPixels(variant), HeadPixels(changed))
			variants = append(variants, changed)
		}
	}
	if i := slices.Index(altered, false); i >= 0 {
		t.Fatalf("change %d alters no sprite's head", i)
	}
	for i, a := range variants {
		for _, b := range variants[i:] {
			if SameHead(a, b) && !slices.Equal(HeadPixels(a), HeadPixels(b)) {
				t.Fatalf("SameHead(%s, %s) for heads whose pixels differ", a.ID, b.ID)
			}
		}
	}
}
