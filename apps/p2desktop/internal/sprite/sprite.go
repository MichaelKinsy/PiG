// Package sprite is the user's PiG sprite for the native client: the same catalogue and the same saved selection as
// PiG's /sprite (coding/piglogin: the built-in variants and those extensions registered, the state file under the config
// root), so the terminal and the app always agree. A change made in either place reaches the other: Watch polls the state
// file and calls changed when the selection moved.
package sprite

import (
	"context"
	"image"
	"image/color"
	"os"
	"strconv"
	"sync"
	"time"

	websprite "github.com/MichaelKinsy/PiG/apps/p2/brand/sprite"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/internal/configroot"
)

// Variant is one entry of the catalogue.
type Variant = piglogin.Variant

// Catalogue is every sprite: the built-in ones, then those extensions registered in this process.
func Catalogue() []Variant { return piglogin.All() }

// Active is the sprite the user selected (the default when the selection is unknown).
func Active() Variant { return piglogin.Active() }

// Select saves the selection to the state file /sprite writes and makes it the active sprite.
func Select(id string) error { return piglogin.Activate(id) }

// StatePath is the state file shared with the terminal.
func StatePath() (string, error) {
	home, err := configroot.Resolve()
	if err != nil {
		return "", err
	}
	return piglogin.StatePath(home), nil
}

// Grid is the sprite's pixels: piglogin.HeadWidth by piglogin.HeadHeight cells, transparent where the grid is empty.
func Grid(v Variant) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, piglogin.HeadWidth, piglogin.HeadHeight))
	for _, p := range piglogin.HeadPixels(v) {
		img.SetNRGBA(p.X, p.Y, color.NRGBA{R: p.Color.R, G: p.Color.G, B: p.Color.B, A: p.Color.A})
	}
	return img
}

// Scaled is Grid with every cell scale pixels square, so it draws crisp at that size.
func Scaled(v Variant, scale int) *image.NRGBA {
	return upscale(Grid(v), scale)
}

// The banner is the empty state's head, the web's login.svg grid (apps/p2/brand/sprite.BannerCells, which both clients call, so
// they draw the same pixels): the pig, a gap, the "PiG." wordmark, each inside a margin, on no plate (transparent outside the
// painted cells, as the terminal's header). The wordmark's colours are darkened to 3:1 on a light page. BannerWidth by
// BannerHeight cells.
const (
	BannerWidth  = 2 + extension.LoginMascotWidth + 2 + extension.LoginHeroWidth + 2
	BannerHeight = 2 + extension.LoginMascotHeight + 2
)

// Banner is v's banner for a page (dark or light) with every cell scale pixels square (6 is the empty state's size, 5 under
// 560 px wide).
func Banner(v Variant, dark bool, scale int) *image.NRGBA {
	theme := websprite.Light
	if dark {
		theme = websprite.Dark
	}
	cells := image.NewNRGBA(image.Rect(0, 0, BannerWidth, BannerHeight))
	for _, cell := range websprite.BannerCells(v, websprite.Page(theme)) {
		cells.SetNRGBA(cell.X, cell.Y, color.NRGBA{R: cell.Color.R, G: cell.Color.G, B: cell.Color.B, A: cell.Color.A})
	}
	return upscale(cells, scale)
}

func upscale(grid *image.NRGBA, scale int) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, grid.Bounds().Dx()*scale, grid.Bounds().Dy()*scale))
	for y := range out.Bounds().Dy() {
		for x := range out.Bounds().Dx() {
			out.SetNRGBA(x, y, grid.NRGBAAt(x/scale, y/scale))
		}
	}
	return out
}

// Watch calls changed whenever the saved selection changes (written by /sprite in a terminal, a game, or this app) until ctx
// ends. It reads the state file's modification time every interval and makes piglogin read the selection again when it moved.
func Watch(ctx context.Context, interval time.Duration, changed func()) {
	path, err := StatePath()
	if err != nil {
		return
	}
	var mu sync.Mutex
	last := stamp(path)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			now := stamp(path)
			mu.Lock()
			moved := now != last
			last = now
			mu.Unlock()
			if moved {
				piglogin.Refresh()
				changed()
			}
		}
	}()
}

// stamp identifies the file's content version: its modification time and size ("" when missing).
func stamp(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return info.ModTime().Format(time.RFC3339Nano) + "/" + strconv.FormatInt(info.Size(), 10)
}
