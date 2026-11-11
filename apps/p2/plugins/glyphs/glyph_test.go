package glyphs

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Ports test/p2/glyphs.test.ts (Pi main ea153dd28): project glyphs: the prototype's glyphs for the same seeds, seeds that name
// one place drawing one glyph, the Radius colors only, mirror symmetry, and how far apart the glyphs of similar seeds are.

var palettes = map[string]bool{"#f09082": true, "#4d9abf": true, "#f1be58": true, "#83ccd2": true}

func TestGlyphOfDrawsThePrototypesGlyphForASeed(t *testing.T) {
	// From the project-glyphs prototype (Tiles, 3x3, recommended settings), which glyph.ts and glyph.go port.
	for seed, want := range map[string]Glyph{
		"~/Development/pi-mono": {Paths: []Path{
			{Fill: "#f1be58", D: "M1 0H1V0A1 1 0 0 1 0 1H0V1A1 1 0 0 1 1 0ZM1 0H2V0A1 1 0 0 1 1 1H1V0ZM0 1H1V1A1 1 0 0 1 0 2H0V1Z"},
			{Fill: "#4d9abf", D: "M2.5 0H2.5L3 0.5V0.5L2.5 1H2.5L2 0.5V0.5L2.5 0ZM0.5 2H0.5L1 2.5V2.5L0.5 3H0.5L0 2.5V2.5L0.5 2Z"},
		}},
		"radius": {Paths: []Path{
			{Fill: "#4d9abf", D: "M1.5 0H1.5A0.5 0.5 0 0 1 2 0.5V1H1V0.5A0.5 0.5 0 0 1 1.5 0ZM1 2H2V3H1V2Z"},
			{Fill: "#83ccd2", D: "M0 2H1V3H0V2ZM2 2H3V3H2V2Z"},
		}},
	} {
		if got := Of(seed); !reflect.DeepEqual(got, want) {
			t.Errorf("Of(%q) = %+v, want %+v", seed, got, want)
		}
	}
}

func TestGlyphOfDrawsOneGlyphForTheWaysOnePlaceIsWritten(t *testing.T) {
	glyph := Of("~/Development/pi-mono")
	for _, seed := range []string{
		"/Users/someone/Development/pi-mono",
		"/home/someone/Development/pi-mono/",
		"/data/data/com.termux/files/home/Development/pi-mono",
		" ~/Development//pi-mono ",
		`~\Development\pi-mono`,
	} {
		if got := Of(seed); !reflect.DeepEqual(got, glyph) {
			t.Errorf("Of(%q) = %+v, want %+v", seed, got, glyph)
		}
	}
	if reflect.DeepEqual(Of("~/Development/pi-mono-2"), glyph) {
		t.Error("another place drew the same glyph")
	}
}

var numbers = regexp.MustCompile(`-?\d+(\.\d+)?`)

func TestGlyphOfUsesTheRadiusColorsAtMostThreeInsideTheBox(t *testing.T) {
	for i := range 300 {
		paths := Of(fmt.Sprintf("~/Development/project-%d", i)).Paths
		if len(paths) < 1 || len(paths) > 3 {
			t.Fatalf("project-%d: %d paths", i, len(paths))
		}
		for _, path := range paths {
			if !palettes[path.Fill] {
				t.Fatalf("project-%d: fill %s", i, path.Fill)
			}
			for _, text := range numbers.FindAllString(path.D, -1) {
				if value, _ := strconv.ParseFloat(text, 64); value < 0 || value > 3 {
					t.Fatalf("project-%d: %s outside the box", i, text)
				}
			}
		}
	}
}

func TestGlyphOfIsMirrorSymmetricAPinwheelCannotComeOut(t *testing.T) {
	for i := range 300 {
		picture := raster(Of(fmt.Sprintf("~/Development/project-%d", i)))
		mirrors := []func(x, y int) string{
			func(x, y int) string { return picture[y][resolution-1-x] },
			func(x, y int) string { return picture[resolution-1-y][x] },
			func(x, y int) string { return picture[x][y] },
			func(x, y int) string { return picture[resolution-1-x][resolution-1-y] },
		}
		// Sample points on an edge are in or out by rounding, not by symmetry. With R = 49 (coprime with 6) no point lies on
		// a circle or a straight edge, except on the grid's two diagonals (a 45-degree cut along one): at most 2R points may
		// differ. A shape pointing the wrong way differs in about half a cell (about 130 points).
		fewest := math.MaxInt
		for _, mirror := range mirrors {
			mismatches := 0
			for y, line := range picture {
				for x, color := range line {
					if color != mirror(x, y) {
						mismatches++
					}
				}
			}
			fewest = min(fewest, mismatches)
		}
		if fewest > 2*resolution {
			t.Fatalf("project-%d: %d mismatches", i, fewest)
		}
	}
}

func TestGlyphOfTellsSimilarSeedsApart(t *testing.T) {
	glyphs := map[string]bool{}
	for i := range 200 {
		text, err := json.Marshal(Of(fmt.Sprintf("~/Development/pi-mono-%d", i)))
		if err != nil {
			t.Fatal(err)
		}
		glyphs[string(text)] = true
	}
	if len(glyphs) != 200 {
		t.Fatalf("%d different glyphs of 200 seeds", len(glyphs))
	}
}

func TestGlyphSVGIsAStandaloneSVGDocumentOfTheRequestedSize(t *testing.T) {
	svg := SVG(Of("radius"), 48)
	prefix := `<svg xmlns="http://www.w3.org/2000/svg" width="48" height="48" viewBox="0 0 3 3">`
	if !strings.HasPrefix(svg, prefix) {
		t.Fatalf("svg = %s", svg)
	}
	if got := strings.Count(svg, "<path "); got != 2 {
		t.Fatalf("%d paths", got)
	}
}

const resolution = 49

type point struct{ x, y float64 }

// raster is the glyph's colors at the centres of an R x R grid ("": empty), by its paths: each path is cells, one `M…Z` each,
// whose outline is straight lines and 90-degree arcs; a point is inside when the polygon through the outline's points, arcs
// sampled, contains it.
func raster(glyph Glyph) [][]string {
	type shape struct {
		fill    string
		polygon []point
	}
	var shapes []shape
	for _, path := range glyph.Paths {
		for _, cell := range strings.Split(path.D, "Z") {
			if cell != "" {
				shapes = append(shapes, shape{path.Fill, outline(cell)})
			}
		}
	}
	picture := make([][]string, resolution)
	for py := range picture {
		picture[py] = make([]string, resolution)
		for px := range picture[py] {
			x := (float64(px) + 0.5) / resolution * 3
			y := (float64(py) + 0.5) / resolution * 3
			for _, each := range shapes {
				if contains(each.polygon, x, y) {
					picture[py][px] = each.fill
					break
				}
			}
		}
	}
	return picture
}

var commands = regexp.MustCompile(`([MHVLA])([^MHVLA]*)`)

// outline is the points of one cell's outline (M, H, V, L, A commands), arcs as 8 segments each.
func outline(cell string) []point {
	var points []point
	var at point
	for _, match := range commands.FindAllStringSubmatch(cell, -1) {
		var n []float64
		for _, field := range strings.FieldsFunc(match[2], func(r rune) bool { return r == ' ' || r == ',' }) {
			value, _ := strconv.ParseFloat(field, 64)
			n = append(n, value)
		}
		switch match[1] {
		case "M", "L":
			at = point{n[0], n[1]}
		case "H":
			at = point{n[0], at.y}
		case "V":
			at = point{at.x, n[0]}
		default:
			// A clockwise (sweep 1) quarter arc of radius r from `at` to the end: its centre is to the right of the chord.
			r, end := n[0], point{n[5], n[6]}
			mx, my := (at.x+end.x)/2, (at.y+end.y)/2
			dx, dy := end.x-at.x, end.y-at.y
			half := math.Hypot(dx, dy) / 2
			toCentre := math.Sqrt(math.Max(0, r*r-half*half)) / (2 * half)
			centre := point{mx - dy*toCentre, my + dx*toCentre}
			from := math.Atan2(at.y-centre.y, at.x-centre.x)
			to := math.Atan2(end.y-centre.y, end.x-centre.x)
			if to < from {
				to += 2 * math.Pi
			}
			for i := 1; i <= 8; i++ {
				angle := from + (to-from)*float64(i)/8
				points = append(points, point{centre.x + r*math.Cos(angle), centre.y + r*math.Sin(angle)})
			}
			at = end
			continue
		}
		points = append(points, at)
	}
	return points
}

func contains(polygon []point, x, y float64) bool {
	inside := false
	for i, j := 0, len(polygon)-1; i < len(polygon); j, i = i, i+1 {
		a, b := polygon[i], polygon[j]
		if (a.y > y) != (b.y > y) && x < (b.x-a.x)*(y-a.y)/(b.y-a.y)+a.x {
			inside = !inside
		}
	}
	return inside
}
