// Package glyphs is p2's project-glyph plugin (apps/p2/plugins/glyphs): a 3x3 identicon in the Radius mark's four colors,
// hashed from a project's path, and `GET /glyph.svg`.
package glyphs

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"strconv"
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"golang.org/x/text/unicode/norm"
)

// Ports apps/p2/plugins/glyphs/glyph.ts. Project glyphs: a 3x3 identicon in the Radius mark's four colors, hashed from a
// project's path. The sidebar draws it inline (Pi's browser client), `/glyph.svg` (server.go) serves it as a file. It is the
// "Tiles, 3x3" generator of the project-glyphs prototype with its recommended settings (loose masks, rich shapes, four
// mirror axes, color regions, calm tone), drawing the same glyph for the same seed, here and in the browser client
// (glyph.ts, vendored; the test compares them). Every glyph is mirror-symmetric, so none is a pinwheel (a swastika); a 3x3
// grid cannot hold a pinwheel anyway.

// Path is one path of a glyph: a fill and its cells' outlines.
type Path struct {
	Fill string
	D    string
}

// Glyph is one path per color in a 3x3 view box, cells merged per color (no hairlines between them).
type Glyph struct{ Paths []Path }

// Of is the glyph of seed, a path or a name (`glyphOf`). Paths count alike in their short form (`/Users/me/x` is `~/x`).
func Of(seed string) Glyph {
	digest := sha256.Sum256([]byte("glyph-v1:" + normalize(seed)))
	word := func(i int) uint32 { return binary.BigEndian.Uint32(digest[i*4:]) }
	// Two streams: the family (colors, mirror axis, corner style) and the structure (cells, shapes).
	family := newRandom(word(0), word(1), word(2), word(3))
	structure := newRandom(word(4), word(5), word(6), word(7))
	order := family.shuffle([]int{0, 1, 2, 3})
	orbits := orbitsOf(mirrors[family.weighted(mirrorWeights)])
	scheme := schemes[family.weighted(schemeWeights)]
	style := styles[family.weighted(styleWeights)]
	// The prototype draws a corner radius here, which tiles do not use; drawing it keeps the glyphs the prototype's.
	family.next()
	flip := family.next() < 0.5
	mask := maskOf(orbits, structure)
	colors := paint(orbits, mask, structure, order, scheme)
	var byColor [len(palette)]strings.Builder
	var seen []int
	for _, cell := range tiles(orbits, colors, stylesOf(orbits, colors, order, style, flip), structure) {
		if byColor[cell.color].Len() == 0 {
			seen = append(seen, cell.color)
		}
		byColor[cell.color].WriteString(cellPath(cell))
	}
	paths := make([]Path, 0, len(seen))
	for _, color := range seen {
		paths = append(paths, Path{Fill: palette[color], D: byColor[color].String()})
	}
	return Glyph{Paths: paths}
}

// SVG is glyph as a standalone SVG document, size pixels square (`glyphSvg`).
func SVG(glyph Glyph, size int) string {
	var paths strings.Builder
	for _, path := range glyph.Paths {
		paths.WriteString(`<path fill="` + path.Fill + `" d="` + path.D + `"/>`)
	}
	pixels := strconv.Itoa(size)
	return `<svg xmlns="http://www.w3.org/2000/svg" width="` + pixels + `" height="` + pixels + `" viewBox="0 0 3 3">` + paths.String() + `</svg>`
}

// palette is coral, blue, gold, teal: the Radius mark (`RadiusWordMark.tsx` in Radius).
var palette = [...]string{"#f09082", "#4d9abf", "#f1be58", "#83ccd2"}

const n = 3

func normalize(seed string) string {
	path := p2runtime.ShortPath(collapseSlashes(strings.ReplaceAll(strings.TrimFunc(norm.NFC.String(seed), isScriptSpace), `\`, "/")))
	if len(path) > 1 {
		return strings.TrimRight(path, "/")
	}
	return path
}

// isScriptSpace is JavaScript's white space (`String.prototype.trim`).
func isScriptSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// collapseSlashes is `.replace(/\/{2,}/g, "/")`.
func collapseSlashes(path string) string {
	var out strings.Builder
	slash := false
	for _, r := range path {
		if r == '/' && slash {
			continue
		}
		slash = r == '/'
		out.WriteRune(r)
	}
	return out.String()
}

// random is sfc32 seeded with four words, warmed up as the prototype does.
type random struct{ a, b, c, d uint32 }

func newRandom(a, b, c, d uint32) *random {
	r := &random{a, b, c, d}
	for range 12 {
		r.next()
	}
	return r
}

func (r *random) next() float64 {
	t := r.a + r.b + r.d
	r.d++
	r.a = r.b ^ (r.b >> 9)
	r.b = r.c + (r.c << 3)
	r.c = (r.c << 21) | (r.c >> 11)
	r.c += t
	return float64(t) / 4294967296
}

func (r *random) int(count int) int { return int(math.Floor(r.next() * float64(count))) }

// weighted is the index of one key of a table of weights (in order), by weight.
func (r *random) weighted(weights []int) int {
	total := 0
	for _, weight := range weights {
		total += weight
	}
	x := r.next() * float64(total)
	for index, weight := range weights {
		x -= float64(weight)
		if x < 0 {
			return index
		}
	}
	return len(weights) - 1
}

func shuffle[T any](r *random, items []T) []T {
	out := append([]T(nil), items...)
	for i := len(out) - 1; i > 0; i-- {
		j := r.int(i + 1)
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (r *random) shuffle(items []int) []int { return shuffle(r, items) }

// mirror is a cell map of a mirror and how it moves a cell's corners (0 top left, 1 top right, 2 bottom right, 3 bottom left).
type mirror struct {
	mapCell func(x, y int) (int, int)
	perm    [4]int
}

var identity = mirror{func(x, y int) (int, int) { return x, y }, [4]int{0, 1, 2, 3}}

var mirrors = []mirror{
	{func(x, y int) (int, int) { return n - 1 - x, y }, [4]int{1, 0, 3, 2}},
	{func(x, y int) (int, int) { return x, n - 1 - y }, [4]int{3, 2, 1, 0}},
	{func(x, y int) (int, int) { return y, x }, [4]int{0, 3, 2, 1}},
	{func(x, y int) (int, int) { return n - 1 - y, n - 1 - x }, [4]int{2, 1, 0, 3}},
}
var mirrorWeights = []int{34, 22, 22, 22}

type scheme int

// One color and an accent, two colors, or two and an accent.
const (
	mono scheme = iota
	duo
	duoA
)

var schemes = []scheme{mono, duo, duoA}
var schemeWeights = []int{30, 45, 25}

type style int

const (
	styleRound style = iota
	styleChamfer
	stylePerColor
)

var styles = []style{styleRound, styleChamfer, stylePerColor}
var styleWeights = []int{55, 15, 30}

// orbit is a cell and its mirror image: they always look alike. stab: the corner moves of a mirror that keeps the cell.
type orbit struct {
	cells []orbitCell
	stab  [][4]int
}

type orbitCell struct {
	idx  int
	perm [4]int
}

func orbitsOf(m mirror) []orbit {
	var seen [n * n]bool
	var orbits []orbit
	for y := range n {
		for x := range n {
			if seen[y*n+x] {
				continue
			}
			var o orbit
			for index, each := range []mirror{identity, m} {
				a, b := each.mapCell(x, y)
				idx := b*n + a
				if index == 1 && idx == y*n+x {
					o.stab = append(o.stab, each.perm)
				}
				duplicate := false
				for _, cell := range o.cells {
					duplicate = duplicate || cell.idx == idx
				}
				if duplicate {
					continue
				}
				o.cells = append(o.cells, orbitCell{idx, each.perm})
				seen[idx] = true
			}
			orbits = append(orbits, o)
		}
	}
	return orbits
}

const (
	fillMin = 4 // math.Ceil(0.4 * n * n)
	fillMax = 7 // math.Floor(0.8 * n * n)
)

// maskOf is the filled cells: 4 to 7, touching all four edges (every glyph the same optical size), not pure stripes.
func maskOf(orbits []orbit, r *random) [n * n]uint8 {
	var best [n * n]uint8
	bestScore := math.MinInt
	for range 80 {
		var mask [n * n]uint8
		for _, o := range orbits {
			if r.next() < 0.5 {
				for _, cell := range o.cells {
					mask[cell.idx] = 1
				}
			}
		}
		score := maskScore(mask)
		if score >= 0 {
			return mask
		}
		if score > bestScore {
			bestScore = score
			best = mask
		}
	}
	return best
}

func maskScore(mask [n * n]uint8) int {
	at := func(x, y int) int { return int(mask[y*n+x]) }
	line := func(cell func(a, b int) int, a int) int {
		sum := 0
		for b := range n {
			sum += cell(a, b)
		}
		return sum
	}
	stripes := func(cell func(a, b int) int) bool {
		for a := range n {
			if sum := line(cell, a); sum != 0 && sum != n {
				return false
			}
		}
		return true
	}
	rows := func(y, x int) int { return at(x, y) }
	columns := func(x, y int) int { return at(x, y) }
	penalty := 0
	if stripes(rows) || stripes(columns) {
		penalty += 2
	}
	filled := 0
	for _, cell := range mask {
		filled += int(cell)
	}
	if filled < fillMin {
		penalty += fillMin - filled
	}
	if filled > fillMax {
		penalty += filled - fillMax
	}
	for _, edge := range []int{0, n - 1} {
		if line(rows, edge) == 0 {
			penalty += 2
		}
		if line(columns, edge) == 0 {
			penalty += 2
		}
	}
	return -penalty
}

var steps = [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}

// neighbours are the cells next to cell i, in the prototype's order.
func neighbours(i int) []int {
	x, y := i%n, i/n
	var out []int
	for _, step := range steps {
		nx, ny := x+step[0], y+step[1]
		if nx >= 0 && ny >= 0 && nx < n && ny < n {
			out = append(out, ny*n+nx)
		}
	}
	return out
}

// pieces is the pieces of the cells set in mask, of one color each (colors).
func pieces(mask [n * n]bool, colors [n * n]int8) int {
	var seen [n * n]bool
	count := 0
	for start := range n * n {
		if !mask[start] || seen[start] {
			continue
		}
		stack := []int{start}
		seen[start] = true
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, j := range neighbours(i) {
				if mask[j] && !seen[j] && colors[j] == colors[i] {
					seen[j] = true
					stack = append(stack, j)
				}
			}
		}
		count++
	}
	return count
}

func count(colors [n * n]int8, color int) int {
	total := 0
	for _, each := range colors {
		if int(each) == color {
			total++
		}
	}
	return total
}

func unpainted() (colors [n * n]int8) {
	for i := range colors {
		colors[i] = -1
	}
	return colors
}

// paint colors as regions: one seed orbit per color, each cell the nearest seed's color, keeping the draw with the fewest
// same-color pieces; the accent takes a smallest orbit at a tip (fewest filled neighbours).
func paint(orbits []orbit, mask [n * n]uint8, r *random, order []int, kind scheme) [n * n]int8 {
	colors := unpainted()
	var filled []orbit
	for _, o := range orbits {
		if mask[o.cells[0].idx] != 0 {
			filled = append(filled, o)
		}
	}
	k := 2
	if kind == mono {
		k = 1
	}
	base := order[:k]
	const minCells = 2
	var best *[n * n]int8
	fewest := math.MaxInt
	attempts := 1
	if k > 1 {
		attempts = 8
	}
	for range attempts {
		seeds := r.shuffleOrbits(filled)
		if len(seeds) > k {
			seeds = seeds[:k]
		}
		trial := unpainted()
		for _, o := range filled {
			for _, cell := range o.cells {
				nearest := math.Inf(1)
				seed := 0
				for index, each := range seeds {
					for _, other := range each.cells {
						dx, dy := other.idx%n-cell.idx%n, other.idx/n-cell.idx/n
						distance := float64(dx*dx + dy*dy)
						if distance < nearest-1e-9 {
							nearest = distance
							seed = index
						}
					}
				}
				trial[cell.idx] = int8(base[seed])
			}
		}
		enough := true
		for _, color := range base {
			enough = enough && count(trial, color) >= minCells
		}
		if !enough {
			continue
		}
		var mask [n * n]bool
		for i, color := range trial {
			mask[i] = color >= 0
		}
		if pieceCount := pieces(mask, trial); pieceCount < fewest {
			fewest = pieceCount
			kept := trial
			best = &kept
		}
	}
	if best != nil {
		colors = *best
	} else {
		for _, o := range filled {
			for _, cell := range o.cells {
				colors[cell.idx] = int8(base[0])
			}
		}
	}
	if kind != duo {
		small := math.MaxInt
		for _, o := range filled {
			small = min(small, len(o.cells))
		}
		tip := func(o orbit) int {
			total := 0
			for _, j := range neighbours(o.cells[0].idx) {
				if mask[j] != 0 {
					total++
				}
			}
			return total
		}
		var candidates []orbit
		for _, o := range filled {
			if len(o.cells) == small && count(colors, int(colors[o.cells[0].idx]))-len(o.cells) >= minCells {
				candidates = append(candidates, o)
			}
		}
		least := math.MaxInt
		for _, o := range candidates {
			least = min(least, tip(o))
		}
		var tips []orbit
		for _, o := range candidates {
			if tip(o) == least {
				tips = append(tips, o)
			}
		}
		if len(tips) > 0 {
			for _, cell := range tips[r.int(len(tips))].cells {
				colors[cell.idx] = int8(order[k])
			}
		}
	}
	return colors
}

func (r *random) shuffleOrbits(items []orbit) []orbit { return shuffle(r, items) }

type cellStyle int

const (
	cellRound cellStyle = iota
	cellChamfer
)

// stylesOf is the corner style of each cell: the glyph's, or by color (primary and accent one way, secondary the other).
func stylesOf(orbits []orbit, colors [n * n]int8, order []int, kind style, flip bool) [n * n]cellStyle {
	var out [n * n]cellStyle
	for _, o := range orbits {
		rank := -1
		for i, color := range order {
			if color == int(colors[o.cells[0].idx]) {
				rank = i
				break
			}
		}
		each := cellRound
		switch {
		case kind == styleChamfer:
			each = cellChamfer
		case kind == stylePerColor && (rank%2 == 0) == flip:
			each = cellChamfer
		}
		for _, cell := range o.cells {
			out[cell.idx] = each
		}
	}
	return out
}

type cornerKind int

const (
	square cornerKind = iota
	round
	cut
)

type corner struct {
	kind cornerKind
	r    float64
}

type cell struct {
	x, y, color int
	corners     [4]corner
}

var squareCorner = corner{square, 0}

func roundCorner(r float64) corner { return corner{round, r} }

func cornersOf(f func(i int) corner) [4]corner {
	return [4]corner{f(0), f(1), f(2), f(3)}
}

// shapes are the shapes a cell may take, pointing at corner k: fan, leaf, drop, dome, wedge, circle, diamond, square.
var shapes = []func(k int) [4]corner{
	func(k int) [4]corner {
		return cornersOf(func(i int) corner {
			if i == k {
				return roundCorner(1)
			}
			return squareCorner
		})
	},
	func(k int) [4]corner {
		return cornersOf(func(i int) corner {
			if i == k || i == (k+2)%4 {
				return roundCorner(1)
			}
			return squareCorner
		})
	},
	func(k int) [4]corner {
		return cornersOf(func(i int) corner {
			if i == k {
				return squareCorner
			}
			return roundCorner(0.5)
		})
	},
	func(k int) [4]corner {
		return cornersOf(func(i int) corner {
			if i == k || i == (k+1)%4 {
				return roundCorner(0.5)
			}
			return squareCorner
		})
	},
	func(k int) [4]corner {
		return cornersOf(func(i int) corner {
			if i == k {
				return corner{cut, 1}
			}
			return squareCorner
		})
	},
	func(int) [4]corner { return cornersOf(func(int) corner { return roundCorner(0.5) }) },
	func(int) [4]corner { return cornersOf(func(int) corner { return corner{cut, 0.5} }) },
	func(int) [4]corner { return cornersOf(func(int) corner { return squareCorner }) },
}
var shapeWeights = []int{20, 14, 12, 12, 10, 10, 8, 14}

// tiles: each filled orbit draws a shape and where it points (outward half the time), for its first cell; its mirror image
// gets the mirrored shape. A cell on the axis takes only shapes symmetric across it.
func tiles(orbits []orbit, colors [n * n]int8, styles [n * n]cellStyle, r *random) []cell {
	var cells []cell
	const middle = float64(n) / 2
	cornerAt := [4][2]int{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
	for _, o := range orbits {
		first := o.cells[0].idx
		if colors[first] < 0 {
			continue
		}
		x, y := first%n, first/n
		outward, farthest := 0, math.Inf(-1)
		for i, at := range cornerAt {
			dx, dy := float64(x+at[0])-middle, float64(y+at[1])-middle
			if distance := dx*dx + dy*dy; distance > farthest {
				outward, farthest = i, distance
			}
		}
		shape := cornersOf(func(int) corner { return roundCorner(0.5) })
		for range 16 {
			k := outward
			if r.next() >= 0.5 {
				k = r.int(4)
			}
			candidate := shapes[r.weighted(shapeWeights)](k)
			symmetric := true
			for _, perm := range o.stab {
				for i, each := range candidate {
					symmetric = symmetric && each == candidate[perm[i]]
				}
			}
			if symmetric {
				shape = candidate
				break
			}
		}
		for _, member := range o.cells {
			var mapped [4]corner
			for i := range 4 {
				mapped[member.perm[i]] = shape[i]
			}
			// A leaf cut a full cell at two opposite corners would leave nothing: half a cell there.
			leaf := mapped[0].r+mapped[2].r > 1 || mapped[1].r+mapped[3].r > 1
			shown := mapped
			if styles[member.idx] != cellRound {
				for i, each := range mapped {
					if each.kind != square {
						radius := each.r
						if leaf {
							radius = 0.5
						}
						shown[i] = corner{cut, radius}
					}
				}
			}
			cells = append(cells, cell{x: member.idx % n, y: member.idx / n, color: int(colors[member.idx]), corners: shown})
		}
	}
	return cells
}

// num is `+value.toFixed(4)` as text.
func num(value float64) string {
	rounded, _ := strconv.ParseFloat(strconv.FormatFloat(value, 'f', 4, 64), 64)
	return strconv.FormatFloat(rounded, 'f', -1, 64)
}

// cellPath is one cell's outline, clockwise from its top edge: arcs for round corners, lines for cut ones.
func cellPath(c cell) string {
	x, y := float64(c.x), float64(c.y)
	var r [4]float64
	for i, each := range c.corners {
		if each.kind != square {
			r[i] = each.r
		}
	}
	at := func(i int, tx, ty float64) string {
		switch {
		case r[i] == 0:
			return ""
		case c.corners[i].kind == round:
			return "A" + num(r[i]) + " " + num(r[i]) + " 0 0 1 " + num(tx) + " " + num(ty)
		}
		return "L" + num(tx) + " " + num(ty)
	}
	return "M" + num(x+r[0]) + " " + num(y) + "H" + num(x+1-r[1]) + at(1, x+1, y+r[1]) +
		"V" + num(y+1-r[2]) + at(2, x+1-r[2], y+1) +
		"H" + num(x+r[3]) + at(3, x, y+1-r[3]) +
		"V" + num(y+r[0]) + at(0, x+r[0], y) + "Z"
}
