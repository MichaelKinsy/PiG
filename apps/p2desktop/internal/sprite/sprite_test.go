package sprite

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	websprite "github.com/MichaelKinsy/PiG/apps/p2/brand/sprite"
	"github.com/MichaelKinsy/PiG/coding/piglogin"
)

func useHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HOME", home)
	return home
}

func TestCatalogueHasTheBuiltInsAndGridsAreCrisp(t *testing.T) {
	all := Catalogue()
	if len(all) < 2 {
		t.Fatalf("catalogue has %d sprites", len(all))
	}
	grid := Grid(all[0])
	if grid.Bounds().Dx() != piglogin.HeadWidth || grid.Bounds().Dy() != piglogin.HeadHeight {
		t.Fatalf("grid %v", grid.Bounds())
	}
	scaled := Scaled(all[0], 3)
	if scaled.Bounds().Dx() != 3*piglogin.HeadWidth {
		t.Fatalf("scaled %v", scaled.Bounds())
	}
	opaque := 0
	for y := range scaled.Bounds().Dy() {
		for x := range scaled.Bounds().Dx() {
			if scaled.NRGBAAt(x, y) != scaled.NRGBAAt(x-x%3, y-y%3) {
				t.Fatalf("pixel %d,%d is not its cell's colour", x, y)
			}
			if scaled.NRGBAAt(x, y).A != 0 {
				opaque++
			}
		}
	}
	if opaque == 0 {
		t.Fatal("the sprite has no pixels")
	}
}

func TestSelectIsTheStateFileOfSpriteAndWatchSeesAnotherWriter(t *testing.T) {
	useHome(t)
	path, err := StatePath()
	if err != nil {
		t.Fatal(err)
	}
	var moves atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	Watch(ctx, 10*time.Millisecond, func() { moves.Add(1) })
	other := Catalogue()[1].ID
	if err := Select(other); err != nil {
		t.Fatal(err)
	}
	if Active().ID != other {
		t.Fatalf("active %q, want %q", Active().ID, other)
	}
	waitFor(t, func() bool { return moves.Load() >= 1 })
	// Another process (the terminal's /sprite) writes the same file: the app sees it and follows.
	if err := piglogin.SaveVariant(filepath.Dir(filepath.Dir(filepath.Dir(path))), piglogin.DefaultID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return Active().ID == piglogin.DefaultID })
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestBannerIsTheWebsBanner draws every built-in's banner from the SVG the branded web UI serves (apps/p2/brand/sprite) and
// requires the same pixels: one banner across web and native.
func TestBannerIsTheWebsBanner(t *testing.T) {
	rect := regexp.MustCompile(`<rect (?:x="(\d+)" y="(\d+)" )?width="(\d+)" height="(\d+)" fill="(#[0-9a-f]{6})"`)
	rect2 := regexp.MustCompile(`<rect x="(\d+)" y="(\d+)" width="(\d+)" height="1" fill="(#[0-9a-f]{6})"`)
	for _, v := range Catalogue() {
		svg := string(websprite.RenderBanner(v))
		want := image.NewNRGBA(image.Rect(0, 0, BannerWidth, BannerHeight))
		plate := rect.FindStringSubmatch(svg)
		pc := parse(plate[5])
		for y := range BannerHeight {
			for x := range BannerWidth {
				want.SetNRGBA(x, y, pc)
			}
		}
		for _, m := range rect2.FindAllStringSubmatch(svg, -1) {
			x, _ := strconv.Atoi(m[1])
			y, _ := strconv.Atoi(m[2])
			w, _ := strconv.Atoi(m[3])
			for i := range w {
				want.SetNRGBA(x+i, y, parse(m[4]))
			}
		}
		got := Banner(v, 1)
		if got.Bounds() != want.Bounds() {
			t.Fatalf("%s: bounds %v want %v", v.ID, got.Bounds(), want.Bounds())
		}
		for i := range got.Pix {
			if got.Pix[i] != want.Pix[i] {
				t.Fatalf("%s: banner differs from the web's at byte %d", v.ID, i)
			}
		}
		if s := Banner(v, 6); s.Bounds().Dx() != 324 || s.Bounds().Dy() != 108 {
			t.Fatalf("%s: 6x banner is %v, want 324x108", v.ID, s.Bounds())
		}
	}
}

func parse(hex string) color.NRGBA {
	var r, g, b uint8
	_, _ = fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b)
	return color.NRGBA{R: r, G: g, B: b, A: 0xFF}
}
