package subprocess

import (
	"fmt"
	"image"
	"image/color"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// halfBlockCell is one drawn cell as a terminal reads it: the SGR colour parameters in force for the foreground (the upper pixel) and the background (the lower pixel), "2;r;g;b" or "5;n", empty after a reset.
type halfBlockCell struct{ fg, bg string }

func halfBlockRGBParams(r, g, b int) string { return fmt.Sprintf("2;%d;%d;%d", r, g, b) }

// halfBlockParse reads a row the way a terminal does. It accepts only SGR sequences and U+2580, so a newline, a cursor movement or any other character fails the test.
func halfBlockParse(t *testing.T, row string) []halfBlockCell {
	t.Helper()
	var cells []halfBlockCell
	var cur halfBlockCell
	for len(row) > 0 {
		if rest, ok := strings.CutPrefix(row, "▀"); ok {
			cells = append(cells, cur)
			row = rest
			continue
		}
		rest, ok := strings.CutPrefix(row, "\x1b[")
		if !ok {
			t.Fatalf("row holds something that is neither U+2580 nor an SGR sequence at %q", row)
		}
		body, rest, ok := strings.Cut(rest, "m")
		if !ok || strings.Trim(body, "0123456789;") != "" {
			t.Fatalf("row holds an escape sequence that is not SGR at %q", row)
		}
		row = rest
		params := strings.Split(body, ";")
		for i := 0; i < len(params); {
			var take int
			switch {
			case params[i] == "0":
				cur = halfBlockCell{}
				i++
				continue
			case (params[i] == "38" || params[i] == "48") && i+1 < len(params) && params[i+1] == "2":
				take = 4
			case (params[i] == "38" || params[i] == "48") && i+1 < len(params) && params[i+1] == "5":
				take = 2
			default:
				t.Fatalf("unexpected SGR parameter %q in %q", params[i], body)
			}
			if i+1+take > len(params) {
				t.Fatalf("short colour in %q", body)
			}
			value := strings.Join(params[i+1:i+1+take], ";")
			if params[i] == "38" {
				cur.fg = value
			} else {
				cur.bg = value
			}
			i += 1 + take
		}
	}
	return cells
}

func halfBlockTestImage(pixels [][]color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, len(pixels[0]), len(pixels)))
	for y, line := range pixels {
		for x, c := range line {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// halfBlockPlainImage hides every method but the three of image.Image, so the renderer cannot take a faster pixel path.
type halfBlockPlainImage struct{ img image.Image }

func (p halfBlockPlainImage) ColorModel() color.Model { return p.img.ColorModel() }
func (p halfBlockPlainImage) Bounds() image.Rectangle { return p.img.Bounds() }
func (p halfBlockPlainImage) At(x, y int) color.Color { return p.img.At(x, y) }

func halfBlockGradient(width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 255 / max(width-1, 1)), G: uint8(y * 255 / max(height-1, 1)), B: uint8((x*7 + y*13) % 256), A: 255})
		}
	}
	return img
}

func TestHalfBlockSizeKeepsTheAspectRatioInsideTheCellBox(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		pxWidth, pxHeight, maxCols, maxRows int
		cols, rows                          int
	}{
		{"a 2 x 2 image fills two cells of one row", 2, 2, 2, 1, 2, 1},
		{"a 4 x 2 image in a 2 x 1 box is one row of two cells", 4, 2, 2, 1, 2, 1},
		{"200 x 100 in 60 x 30 is 60 columns of 30 pixel rows", 200, 100, 60, 30, 60, 15},
		{"a square is as many pixel rows as columns", 512, 512, 60, 30, 60, 30},
		{"1600 x 1067 in 60 x 30 rounds 40.0125 pixel rows to 40", 1600, 1067, 60, 30, 60, 20},
		{"a tall image is limited by the rows", 100, 400, 60, 30, 15, 30},
		{"an image narrower than the box is not scaled up", 10, 6, 60, 30, 10, 3},
		{"an odd pixel height rounds up to a whole cell", 3, 3, 60, 30, 3, 2},
		{"one pixel is one cell", 1, 1, 60, 30, 1, 1},
		{"a very wide image keeps one row", 1000, 1, 60, 30, 60, 1},
		{"a very tall image keeps one column", 1, 1000, 60, 30, 1, 30},
		{"the box is clamped to the cap", 4000, 4000, 1000, 1000, halfBlockMaxCols, halfBlockMaxRows},
		{"a box below one cell is one cell", 200, 100, 0, -3, 1, 1},
		{"an empty image is one cell", 0, 0, 60, 30, 1, 1},
		{"a huge image does not overflow", 1 << 30, 1 << 30, 60, 30, 60, 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cols, rows := halfBlockSize(tc.pxWidth, tc.pxHeight, tc.maxCols, tc.maxRows)
			if cols != tc.cols || rows != tc.rows {
				t.Fatalf("halfBlockSize(%d, %d, %d, %d) = %d x %d cells, want %d x %d", tc.pxWidth, tc.pxHeight, tc.maxCols, tc.maxRows, cols, rows, tc.cols, tc.rows)
			}
		})
	}
}

func TestHalfBlockRowsDrawTheUpperPixelAsForegroundAndTheLowerAsBackground(t *testing.T) {
	red, green, blue, white := color.NRGBA{255, 0, 0, 255}, color.NRGBA{0, 255, 0, 255}, color.NRGBA{0, 0, 255, 255}, color.NRGBA{255, 255, 255, 255}
	none := color.NRGBA{}
	for _, tc := range []struct {
		name       string
		img        image.Image
		cols, rows int
		background color.Color
		want       [][]halfBlockCell
	}{
		{
			name: "a 2 x 2 image in a 2 x 1 box: each cell has its own two pixels",
			img:  halfBlockTestImage([][]color.NRGBA{{red, green}, {blue, white}}),
			cols: 2, rows: 1,
			want: [][]halfBlockCell{{
				{halfBlockRGBParams(255, 0, 0), halfBlockRGBParams(0, 0, 255)},
				{halfBlockRGBParams(0, 255, 0), halfBlockRGBParams(255, 255, 255)},
			}},
		},
		{
			// Upper: (255+0)/2 = 127.5 rounds half up to 128; (10+30)/2, (20+40)/2, (30+50)/2. Lower: (0+255)/2 = 127.5 to 128; (100+101)/2 = 100.5 to 101.
			name: "a 4 x 2 image scaled to 2 x 1 cells averages each pair of pixels",
			img: halfBlockTestImage([][]color.NRGBA{
				{red, blue, {10, 20, 30, 255}, {30, 40, 50, 255}},
				{{0, 0, 0, 255}, white, {100, 100, 100, 255}, {101, 100, 100, 255}},
			}),
			cols: 2, rows: 1,
			want: [][]halfBlockCell{{
				{halfBlockRGBParams(128, 0, 128), halfBlockRGBParams(128, 128, 128)},
				{halfBlockRGBParams(20, 30, 40), halfBlockRGBParams(101, 100, 100)},
			}},
		},
		{
			// Three pixels over two columns: the middle pixel lies half in each, so the weights are 2:1 and 1:2 of three. One pixel row over two half cells: both halves show it.
			name: "a 3 x 1 image in 2 columns weights each pixel by the area it covers",
			img:  halfBlockTestImage([][]color.NRGBA{{{90, 0, 0, 255}, {0, 90, 0, 255}, {0, 0, 90, 255}}}),
			cols: 2, rows: 1,
			want: [][]halfBlockCell{{
				{halfBlockRGBParams(60, 30, 0), halfBlockRGBParams(60, 30, 0)},
				{halfBlockRGBParams(0, 30, 60), halfBlockRGBParams(0, 30, 60)},
			}},
		},
		{
			// Alpha 128 of 255 is 0.50196: red 255 x 0.50196 = 128, blue background 255 x 0.49804 = 127.
			name: "a transparent pixel shows the background and a half transparent one blends with it",
			img:  halfBlockTestImage([][]color.NRGBA{{none}, {{255, 0, 0, 128}}}),
			cols: 1, rows: 1, background: blue,
			want: [][]halfBlockCell{{{halfBlockRGBParams(0, 0, 255), halfBlockRGBParams(128, 0, 127)}}},
		},
		{
			name: "no background is black",
			img:  halfBlockTestImage([][]color.NRGBA{{none}, {{255, 0, 0, 128}}}),
			cols: 1, rows: 1,
			want: [][]halfBlockCell{{{halfBlockRGBParams(0, 0, 0), halfBlockRGBParams(128, 0, 0)}}},
		},
		{
			name: "the caller may ask for more cells than the image has pixels",
			img:  halfBlockTestImage([][]color.NRGBA{{{10, 20, 30, 255}}}),
			cols: 3, rows: 2,
			want: [][]halfBlockCell{
				slices.Repeat([]halfBlockCell{{halfBlockRGBParams(10, 20, 30), halfBlockRGBParams(10, 20, 30)}}, 3),
				slices.Repeat([]halfBlockCell{{halfBlockRGBParams(10, 20, 30), halfBlockRGBParams(10, 20, 30)}}, 3),
			},
		},
		{
			name: "pixels are read from the image's own origin",
			img:  halfBlockTestImage([][]color.NRGBA{{white, white, white}, {white, red, green}, {white, blue, white}}).SubImage(image.Rect(1, 1, 3, 3)),
			cols: 2, rows: 1,
			want: [][]halfBlockCell{{
				{halfBlockRGBParams(255, 0, 0), halfBlockRGBParams(0, 0, 255)},
				{halfBlockRGBParams(0, 255, 0), halfBlockRGBParams(255, 255, 255)},
			}},
		},
		{
			name: "an image with no pixels is the background",
			img:  image.NewNRGBA(image.Rectangle{}),
			cols: 2, rows: 1, background: green,
			want: [][]halfBlockCell{slices.Repeat([]halfBlockCell{{halfBlockRGBParams(0, 255, 0), halfBlockRGBParams(0, 255, 0)}}, 2)},
		},
		{
			name: "an image as large as the whole plane is sampled, not walked",
			img:  image.NewUniform(color.NRGBA{10, 20, 30, 255}),
			cols: 3, rows: 1,
			want: [][]halfBlockCell{slices.Repeat([]halfBlockCell{{halfBlockRGBParams(10, 20, 30), halfBlockRGBParams(10, 20, 30)}}, 3)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, img := range []image.Image{tc.img, halfBlockPlainImage{tc.img}} {
				rows := halfBlockRows(img, tc.cols, tc.rows, halfBlockTrueColor, tc.background)
				if len(rows) != len(tc.want) {
					t.Fatalf("%T: %d rows, want %d", img, len(rows), len(tc.want))
				}
				for y, row := range rows {
					if got := halfBlockParse(t, row); !slices.Equal(got, tc.want[y]) {
						t.Errorf("%T row %d: cells %v, want %v (bytes %q)", img, y, got, tc.want[y], row)
					}
				}
			}
		})
	}
}

func TestHalfBlockRowsWriteEachColourOncePerRun(t *testing.T) {
	red, blue, white := color.NRGBA{255, 0, 0, 255}, color.NRGBA{0, 0, 255, 255}, color.NRGBA{255, 255, 255, 255}
	img := halfBlockTestImage([][]color.NRGBA{{red, red, red, blue}, {blue, blue, white, white}, {red, red, red, red}, {red, red, red, red}})
	for _, tc := range []struct {
		name  string
		depth halfBlockDepth
		want  []string
	}{
		{"truecolor", halfBlockTrueColor, []string{
			"\x1b[38;2;255;0;0;48;2;0;0;255m▀▀\x1b[48;2;255;255;255m▀\x1b[38;2;0;0;255m▀\x1b[0m",
			"\x1b[38;2;255;0;0;48;2;255;0;0m▀▀▀▀\x1b[0m",
		}},
		// Red is cube step 5, 0, 0: 16 + 36 x 5 = 196. Blue is 16 + 5 = 21. White is 16 + 215 = 231.
		{"256 colours", halfBlock256, []string{
			"\x1b[38;5;196;48;5;21m▀▀\x1b[48;5;231m▀\x1b[38;5;21m▀\x1b[0m",
			"\x1b[38;5;196;48;5;196m▀▀▀▀\x1b[0m",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := halfBlockRows(img, 4, 2, tc.depth, nil); !slices.Equal(got, tc.want) {
				t.Fatalf("rows\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// halfBlockXtermPalette is xterm's default table for indexes 16 to 255, from the rule of its 256colres.pl: a 6 x 6 x 6 cube whose channel is 0 or 55 + 40 x step, then 24 greys of 8 + 10 x step.
func halfBlockXtermPalette() [256][3]int {
	var palette [256][3]int
	level := func(step int) int {
		if step == 0 {
			return 0
		}
		return 55 + 40*step
	}
	for r := range 6 {
		for g := range 6 {
			for b := range 6 {
				palette[16+36*r+6*g+b] = [3]int{level(r), level(g), level(b)}
			}
		}
	}
	for step := range 24 {
		palette[232+step] = [3]int{8 + 10*step, 8 + 10*step, 8 + 10*step}
	}
	return palette
}

func TestHalfBlock256IndexIsTheNearestPaletteEntry(t *testing.T) {
	palette := halfBlockXtermPalette()
	distance := func(entry [3]int, r, g, b int) int {
		return (entry[0]-r)*(entry[0]-r) + (entry[1]-g)*(entry[1]-g) + (entry[2]-b)*(entry[2]-b)
	}
	// nearestOfAll is the least squared distance over the cube and the grey ramp together, all 240 entries.
	nearestOfAll := func(r, g, b int) int {
		nearest := distance(palette[16], r, g, b)
		for _, entry := range palette[17:] {
			nearest = min(nearest, distance(entry, r, g, b))
		}
		return nearest
	}
	for _, tc := range []struct {
		name    string
		r, g, b uint8
		want    uint8
	}{
		{"pure red is the cube corner 5, 0, 0", 255, 0, 0, 196},
		{"mid grey is the ramp entry of exactly 128", 128, 128, 128, 244},
		// 100 is nearer 95 than 135, 150 nearer 135 than 175, 200 nearer 215 than 175: steps 1, 2, 4, index 16 + 36 + 12 + 4.
		{"a colour between cube steps takes the nearer step of each channel", 100, 150, 200, 68},
		{"black is the cube's black, not a theme colour", 0, 0, 0, 16},
		{"white is the cube's white", 255, 255, 255, 231},
		{"the darkest ramp entry", 8, 8, 8, 232},
		{"the lightest ramp entry", 238, 238, 238, 255},
		// The cube has no grey between 95 and 135; the ramp has 118.
		{"a grey between cube steps takes the ramp", 117, 117, 117, 243},
		// A warm brown, the trees of a photograph: the cube's (135, 95, 95) is at 36 + 361 + 49 = 446, its (135, 135, 95) at 526 and the nearest grey, 108, at 877. The nearest entry looks pink.
		{"a warm brown is the cube's 135, 95, 95", 129, 114, 88, 95},
		// The cube's (95, 135, 95) and (95, 95, 95) are both at 850; the grey 98 is at 144 + 289 + 324 = 757.
		{"a mid olive is nearer a grey than any cube entry", 110, 115, 80, 241},
		// The cube's (95, 135, 135) is at 25 + 225 + 225 = 475; the nearest grey, 128, is at 1332.
		{"a mid grey-blue is the cube's 95, 135, 135", 100, 120, 150, 66},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := halfBlock256Index(tc.r, tc.g, tc.b)
			if got != tc.want {
				t.Fatalf("halfBlock256Index(%d, %d, %d) = %d, want %d", tc.r, tc.g, tc.b, got, tc.want)
			}
			if d, nearest := distance(palette[got], int(tc.r), int(tc.g), int(tc.b)), nearestOfAll(int(tc.r), int(tc.g), int(tc.b)); d != nearest {
				t.Fatalf("(%d, %d, %d) maps to %d at squared distance %d; the nearest of the 240 entries is at %d", tc.r, tc.g, tc.b, got, d, nearest)
			}
		})
	}

	t.Run("no palette entry is nearer over the whole colour lattice", func(t *testing.T) {
		for r := 0; r <= 255; r += 5 {
			for g := 0; g <= 255; g += 5 {
				for b := 0; b <= 255; b += 5 {
					got := halfBlock256Index(uint8(r), uint8(g), uint8(b))
					if got < 16 {
						t.Fatalf("(%d, %d, %d) maps to %d, a colour the terminal theme owns", r, g, b, got)
					}
					if d, nearest := distance(palette[got], r, g, b), nearestOfAll(r, g, b); d != nearest {
						t.Fatalf("(%d, %d, %d) maps to %d %v at squared distance %d; the nearest entry is at %d", r, g, b, got, palette[got], d, nearest)
					}
				}
			}
		}
	})
}

func TestHalfBlockRowsAreExactlyTheAskedCellsAndEndWithAReset(t *testing.T) {
	transparent := image.NewNRGBA(image.Rect(0, 0, 9, 7))
	for _, tc := range []struct {
		name       string
		img        image.Image
		cols, rows int
	}{
		{"a photograph sized box", halfBlockGradient(160, 107), 60, 20},
		{"an odd scale", halfBlockGradient(37, 53), 11, 9},
		{"scaled up", halfBlockGradient(5, 3), 17, 4},
		{"one cell", halfBlockGradient(64, 64), 1, 1},
		{"fully transparent", transparent, 9, 4},
	} {
		for _, depth := range []halfBlockDepth{halfBlockTrueColor, halfBlock256} {
			t.Run(fmt.Sprintf("%s depth %d", tc.name, depth), func(t *testing.T) {
				rows := halfBlockRows(tc.img, tc.cols, tc.rows, depth, color.NRGBA{40, 40, 40, 255})
				if len(rows) != tc.rows {
					t.Fatalf("%d rows, want %d", len(rows), tc.rows)
				}
				for y, row := range rows {
					if width := widthx.VisibleWidth(row); width != tc.cols {
						t.Errorf("row %d is %d cells wide, want %d", y, width, tc.cols)
					}
					if !strings.HasSuffix(row, "\x1b[0m") {
						t.Errorf("row %d does not end with the SGR reset: %q", y, row)
					}
					cells := halfBlockParse(t, row)
					if len(cells) != tc.cols {
						t.Errorf("row %d draws %d half blocks, want %d", y, len(cells), tc.cols)
					}
					for x, cell := range cells {
						if cell.fg == "" || cell.bg == "" {
							t.Fatalf("row %d cell %d is drawn with a colour the row did not set: %+v", y, x, cell)
						}
					}
				}
				if again := halfBlockRows(tc.img, tc.cols, tc.rows, depth, color.NRGBA{40, 40, 40, 255}); !slices.Equal(rows, again) {
					t.Error("the same input gave different bytes")
				}
			})
		}
	}
}

func TestHalfBlockRowsAreBoundedByTheCap(t *testing.T) {
	img := halfBlockGradient(64, 64)
	for _, depth := range []halfBlockDepth{halfBlockTrueColor, halfBlock256} {
		atCap := halfBlockRows(img, halfBlockMaxCols, halfBlockMaxRows, depth, nil)
		if len(atCap) != halfBlockMaxRows {
			t.Fatalf("depth %d: %d rows at the cap, want %d", depth, len(atCap), halfBlockMaxRows)
		}
		// The longest cell sets both colours: ESC [ 38;2;255;255;255 ; 48;2;255;255;255 m is 36 bytes, U+2580 is 3; a row adds the 4 bytes of the reset.
		const longestCell, reset = 36 + 3, 4
		for y, row := range atCap {
			if width := widthx.VisibleWidth(row); width != halfBlockMaxCols {
				t.Fatalf("depth %d row %d is %d cells wide at the cap, want %d", depth, y, width, halfBlockMaxCols)
			}
			if len(row) > halfBlockMaxCols*longestCell+reset {
				t.Fatalf("depth %d row %d is %d bytes, above the bound %d", depth, y, len(row), halfBlockMaxCols*longestCell+reset)
			}
		}
		if beyond := halfBlockRows(img, halfBlockMaxCols+1, halfBlockMaxRows+1, depth, nil); !slices.Equal(beyond, atCap) {
			t.Fatalf("depth %d: a box above the cap is not clamped to it: %d rows, first %d cells wide", depth, len(beyond), widthx.VisibleWidth(beyond[0]))
		}
	}
	for _, box := range [][2]int{{0, 5}, {5, 0}, {-1, -1}} {
		if rows := halfBlockRows(img, box[0], box[1], halfBlockTrueColor, nil); len(rows) != 0 {
			t.Fatalf("a %d x %d box gave %d rows, want none", box[0], box[1], len(rows))
		}
	}
}

// halfBlockPhoto is a 1600 x 1067 image in the form a JPEG decodes to (Y'CbCr, 4:2:0), with smooth areas, edges and fine detail.
func halfBlockPhoto() *image.YCbCr {
	img := image.NewYCbCr(image.Rect(0, 0, 1600, 1067), image.YCbCrSubsampleRatio420)
	for y := range 1067 {
		for x := range 1600 {
			img.Y[img.YOffset(x, y)] = uint8((x*255/1599 + y*255/1066) / 2)
			if (x/40+y/40)%2 == 0 {
				img.Y[img.YOffset(x, y)] ^= uint8((x*31 + y*17) % 64)
			}
			img.Cb[img.COffset(x, y)] = uint8(x * 255 / 1599)
			img.Cr[img.COffset(x, y)] = uint8(y * 255 / 1066)
		}
	}
	return img
}

func BenchmarkHalfBlockRows(b *testing.B) {
	img := halfBlockPhoto()
	fitCols, fitRows := halfBlockSize(1600, 1067, 60, 30)
	for _, bc := range []struct {
		name       string
		cols, rows int
		depth      halfBlockDepth
	}{
		{"truecolor/fit60x30", fitCols, fitRows, halfBlockTrueColor},
		{"256/fit60x30", fitCols, fitRows, halfBlock256},
		{"truecolor/cells60x30", 60, 30, halfBlockTrueColor},
		{"256/cells60x30", 60, 30, halfBlock256},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			var bytes int
			for b.Loop() {
				bytes = 0
				for _, row := range halfBlockRows(img, bc.cols, bc.rows, bc.depth, nil) {
					bytes += len(row)
				}
			}
			b.ReportMetric(float64(bytes), "escape-bytes/op")
			b.ReportMetric(float64(bc.cols*bc.rows), "cells/op")
		})
	}
}
