// Command gen makes every icon of the p2 desktop app from one master, the PiG pixel pig of the site and press kit
// (icons/pig-pixel-green.png, a copy of handoffs/brand/pig-pixel-green.png, itself from site-r5/lead/public/ of the site tree).
//
//	go run ./icons/gen            # from apps/p2desktop; rewrites icons/ and the files the build embeds
//
// The pig is a 14 x 13 grid of flat cells. The script samples one pixel at each cell's centre of the master and snaps it to the
// logo's palette, so the master's own resampling noise never reaches an icon. Every size scales the grid by a whole number
// of pixels, nearest neighbour, never a smoothing resample; a tile's rounded corners are the only anti-aliased edge. At 16 px
// the grid does not fit twice, and is drawn at one pixel per cell.
//
// Outputs: icons/AppIcon.icns (macOS: the Dock icon on the standard 824 px tile of a 1024 px canvas), resources/icon.png (the
// 1024 px full-bleed tile, which `mygo build` derives the Windows and Linux icons from), icons/p2.ico (Windows, 16-256),
// icons/linux/p2-<size>.png, and the tray icons (a monochrome template of the same pig for the macOS menu bar, a coloured one
// for Windows and Linux) in cmd/p2-desktop/resources/.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
)

// The site's near-black green, behind the pig on every tile.
var tileColor = color.NRGBA{0x18, 0x30, 0x2c, 0xff}

// Cell classes of the pig, with the logo's own colours.
type cell struct {
	name string
	rgb  color.NRGBA
	// alpha is the weight of the class in the monochrome template (1 - luminance of its colour).
	alpha float64
}

var palette = []cell{
	{"bg", color.NRGBA{0xd5, 0xf2, 0xe8, 0xff}, 0},
	{"outline", color.NRGBA{0x18, 0x15, 0x1c, 0xff}, 1},
	{"body", color.NRGBA{0x48, 0xa3, 0x82, 0xff}, 0.55},
	{"brow", color.NRGBA{0x63, 0xbe, 0x9d, 0xff}, 0.38},
	{"cheek", color.NRGBA{0x82, 0xda, 0xba, 0xff}, 0.25},
	{"white", color.NRGBA{0xfe, 0xfe, 0xfe, 0xff}, 0},
	{"nose", color.NRGBA{0x33, 0x77, 0x5e, 0xff}, 0.75},
}

const gridCols, gridRows = 14, 13

// The silhouette's bounding box in the master (the clip polygon of pig-pixel.svg).
const x0, x1, y0, y1 = 103.0, 1240.0, 39.0, 1123.0

func nearest(c color.NRGBA) int {
	best, bd := 0, math.MaxFloat64
	for i, p := range palette {
		dr, dg, db := float64(c.R)-float64(p.rgb.R), float64(c.G)-float64(p.rgb.G), float64(c.B)-float64(p.rgb.B)
		if d := dr*dr + dg*dg + db*db; d < bd {
			best, bd = i, d
		}
	}
	return best
}

func readGrid(path string) [gridRows][gridCols]int {
	f, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		log.Fatal(err)
	}
	var grid [gridRows][gridCols]int
	cw, ch := (x1-x0)/gridCols, (y1-y0)/gridRows
	for j := range gridRows {
		for i := range gridCols {
			c := color.NRGBAModel.Convert(img.At(int(x0+(float64(i)+0.5)*cw), int(y0+(float64(j)+0.5)*ch))).(color.NRGBA)
			grid[j][i] = nearest(c)
		}
	}
	return grid
}

type canvas struct {
	img  *image.NRGBA
	size int
}

func newCanvas(size int) *canvas { return &canvas{image.NewNRGBA(image.Rect(0, 0, size, size)), size} }

// roundedTile fills the square [off, off+side) with c and rounds its corners with radius r, anti-aliasing the edge only.
func (c *canvas) roundedTile(off, side, r float64, fill color.NRGBA) {
	for y := range c.size {
		for x := range c.size {
			px, py := float64(x)+0.5-off-side/2, float64(y)+0.5-off-side/2
			qx, qy := math.Abs(px)-(side/2-r), math.Abs(py)-(side/2-r)
			d := math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
			a := math.Max(0, math.Min(1, 0.5-d))
			if a > 0 {
				c.img.SetNRGBA(x, y, color.NRGBA{fill.R, fill.G, fill.B, uint8(a*255 + 0.5)})
			}
		}
	}
}

// pig draws the grid at scale pixels per cell with its top-left corner at (ox, oy). template draws the monochrome version:
// black, with the alpha of each class.
func (c *canvas) pig(grid [gridRows][gridCols]int, scale, ox, oy int, template bool) {
	for j := range gridRows {
		for i := range gridCols {
			class := palette[grid[j][i]]
			if class.name == "bg" {
				continue
			}
			col := class.rgb
			if template {
				col = color.NRGBA{0, 0, 0, uint8(class.alpha*255 + 0.5)}
			}
			if col.A == 0 {
				continue
			}
			for y := range scale {
				for x := range scale {
					c.img.SetNRGBA(ox+i*scale+x, oy+j*scale+y, col)
				}
			}
		}
	}
}

// icon is the app icon at size. margin is the fraction of the canvas left transparent around the tile (macOS: 100/1024 on
// each side, the 824 px tile), share the fraction of the tile the pig's width takes.
func icon(grid [gridRows][gridCols]int, size int, margin, share float64) *image.NRGBA {
	c := newCanvas(size)
	tile := float64(size) * (1 - 2*margin)
	off := float64(size) * margin
	if tile < float64(gridCols)*1.0/share && margin > 0 {
		// Too small for the grid on a margin: the tile takes the whole canvas.
		tile, off = float64(size), 0
	}
	c.roundedTile(off, tile, tile*0.2237, tileColor)
	scale := max(1, int(tile*share)/gridCols)
	w, h := gridCols*scale, gridRows*scale
	c.pig(grid, scale, int(math.Round(off+(tile-float64(w))/2)), int(math.Round(off+(tile-float64(h))/2)), false)
	return c.img
}

// trayIcon is the pig alone, scale pixels per cell, centred in a size x size square.
func trayIcon(grid [gridRows][gridCols]int, size, scale int, template bool) *image.NRGBA {
	c := newCanvas(size)
	w, h := gridCols*scale, gridRows*scale
	c.pig(grid, scale, (size-w)/2, (size-h)/2, template)
	return c.img
}

func encodePNG(img image.Image) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		log.Fatal(err)
	}
	return b.Bytes()
}

func write(path string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Println(path, len(data))
}

// icns writes an Apple icon family with PNG payloads: 16, 32, 64, 128, 256, 512 and 1024 px, and the @2x entries.
func icns(images map[string][]byte) []byte {
	order := []string{"icp4", "icp5", "icp6", "ic07", "ic08", "ic09", "ic10", "ic11", "ic12", "ic13", "ic14"}
	var body bytes.Buffer
	for _, t := range order {
		data := images[t]
		body.WriteString(t)
		_ = binary.Write(&body, binary.BigEndian, uint32(8+len(data)))
		body.Write(data)
	}
	var out bytes.Buffer
	out.WriteString("icns")
	_ = binary.Write(&out, binary.BigEndian, uint32(8+body.Len()))
	out.Write(body.Bytes())
	return out.Bytes()
}

// ico writes a Windows icon with PNG payloads.
func ico(sizes []int, images [][]byte) []byte {
	var out bytes.Buffer
	_ = binary.Write(&out, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		dim := byte(s)
		if s >= 256 {
			dim = 0
		}
		out.Write([]byte{dim, dim, 0, 0})
		_ = binary.Write(&out, binary.LittleEndian, [2]uint16{1, 32})
		_ = binary.Write(&out, binary.LittleEndian, [2]uint32{uint32(len(images[i])), uint32(offset)})
		offset += len(images[i])
	}
	for _, p := range images {
		out.Write(p)
	}
	return out.Bytes()
}

func main() {
	grid := readGrid("icons/pig-pixel-green.png")
	const macMargin, macShare = 100.0 / 1024, 0.62
	const flatMargin, flatShare = 0.0, 0.70

	mac := func(size int) []byte { return encodePNG(icon(grid, size, macMargin, macShare)) }
	write("icons/AppIcon.icns", icns(map[string][]byte{
		"icp4": mac(16), "icp5": mac(32), "icp6": mac(64), "ic07": mac(128), "ic08": mac(256), "ic09": mac(512), "ic10": mac(1024),
		"ic11": mac(32), "ic12": mac(64), "ic13": mac(256), "ic14": mac(512),
	}))
	flat := func(size int) []byte { return encodePNG(icon(grid, size, flatMargin, flatShare)) }
	write("resources/icon.png", flat(1024))
	var sizes []int
	var images [][]byte
	for _, s := range []int{16, 24, 32, 40, 48, 64, 128, 256} {
		sizes, images = append(sizes, s), append(images, flat(s))
	}
	write("icons/p2.ico", ico(sizes, images))
	for _, s := range []int{16, 24, 32, 48, 64, 128, 256, 512} {
		write(fmt.Sprintf("icons/linux/p2-%d.png", s), flat(s))
	}
	write("cmd/p2-desktop/resources/tray-template.png", encodePNG(trayIcon(grid, 32, 2, true)))
	write("cmd/p2-desktop/resources/tray-color-32.png", encodePNG(trayIcon(grid, 32, 2, false)))
	write("cmd/p2-desktop/resources/tray-color-64.png", encodePNG(trayIcon(grid, 64, 4, false)))
}
