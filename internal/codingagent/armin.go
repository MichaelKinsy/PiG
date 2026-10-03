package codingagent

import (
	"context"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/src/modes/interactive/components/armin.ts.
//
// pig divergence (D87): the image is a pig head and the label is "pigsayhi" (armin.ts draws Armin and "ARMIN SAYS HI").
// The effects, their selection, random-call order and timing are Pi's.
const arminWidth, arminHeight = 31, 34
const arminDisplayHeight = (arminHeight + 1) / 2

// arminLabel is the line under the image.
const arminLabel = "pigsayhi"

// arminImage is the pig head, one string per pixel row; '#' is foreground.
var arminImage = [arminHeight]string{
	"...............................",
	".##.........................##.",
	".#.##.....................##.#.",
	".#...##.................##...#.",
	".#.....##.............##.....#.",
	".#.......##.........##.......#.",
	".#.........#########.........#.",
	".#...........................#.",
	".#...........................#.",
	".#...........................#.",
	"#.............................#",
	"#.............................#",
	"#.......##...........##.......#",
	"#......####.........####......#",
	"#......####.........####......#",
	"#.......##...........##.......#",
	"#.............................#",
	"#.............................#",
	"#.........###########.........#",
	"#.......##...........##.......#",
	"#......#...............#......#",
	"#......#....##...##....#......#",
	"#......#...####.####...#......#",
	"#......#....##...##....#......#",
	"#.......#.............#.......#",
	"#........##.........##........#",
	".#.........#########.........#.",
	".#...........................#.",
	"..#.........................#..",
	"..#...........#.#...........#..",
	"...##..........#..........##...",
	".....##.................##.....",
	".......###...........###.......",
	"..........###########..........",
}

var arminEffects = [...]string{"typewriter", "scanline", "rain", "fade", "crt", "glitch", "dissolve"}

type arminGrid [arminDisplayHeight][arminWidth]rune

type arminComponent struct {
	tui.BaseComponent
	effect                                  string
	random                                  func() float64
	finalGrid, currentGrid                  arminGrid
	pos, row, idx, expansion, phase         int
	drops                                   [arminWidth]struct{ y, settled int }
	positions                               [][2]int
	cachedLines                             []string
	cachedWidth, gridVersion, cachedVersion int
	cancel                                  context.CancelFunc
	animationDone                           chan struct{}
}

func arminPixel(x, y int) bool {
	if y >= arminHeight {
		return false
	}
	return arminImage[y][x] == '#'
}

func emptyArminGrid() (grid arminGrid) {
	for row := range grid {
		for x := range grid[row] {
			grid[row][x] = ' '
		}
	}
	return grid
}

func newArminComponent(random func() float64) *arminComponent {
	a := &arminComponent{random: random, currentGrid: emptyArminGrid(), cachedVersion: -1}
	a.effect = arminEffects[int(random()*float64(len(arminEffects)))]
	for row := range a.finalGrid {
		for x := range a.finalGrid[row] {
			upper, lower := arminPixel(x, row*2), arminPixel(x, row*2+1)
			ch := ' '
			switch {
			case upper && lower:
				ch = '█'
			case upper:
				ch = '▀'
			case lower:
				ch = '▄'
			}
			a.finalGrid[row][x] = ch
		}
	}
	a.initEffect()
	return a
}

func (a *arminComponent) initEffect() {
	switch a.effect {
	case "rain":
		for x := range a.drops {
			a.drops[x].y = -int(a.random() * arminDisplayHeight * 2)
		}
	case "dissolve", "fade":
		if a.effect == "dissolve" {
			chars := []rune(" ░▒▓█▀▄")
			for row := range a.currentGrid {
				for x := range a.currentGrid[row] {
					a.currentGrid[row][x] = chars[int(a.random()*float64(len(chars)))]
				}
			}
		}
		for row := range arminDisplayHeight {
			for x := range arminWidth {
				a.positions = append(a.positions, [2]int{row, x})
			}
		}
		for i := len(a.positions) - 1; i > 0; i-- {
			j := int(a.random() * float64(i+1))
			a.positions[i], a.positions[j] = a.positions[j], a.positions[i]
		}
	}
}

func (a *arminComponent) Invalidate() {
	a.BaseComponent.Invalidate()
	a.cachedWidth = 0
}

func (a *arminComponent) Render(width int) []string {
	if width == a.cachedWidth && a.cachedVersion == a.gridVersion {
		return a.cachedLines
	}
	end := width - 1
	// Array.slice interprets a negative end relative to the row length.
	if end < 0 {
		end = max(0, arminWidth+end)
	}
	end = min(end, arminWidth)
	lines := make([]string, 0, arminDisplayHeight+1)
	theme := tui.ActiveTheme()
	for _, row := range a.currentGrid {
		lines = append(lines, " "+theme.FgText("accent", string(row[:end]))+strings.Repeat(" ", max(0, width-1-end)))
	}
	lines = append(lines, " "+theme.FgText("accent", arminLabel)+strings.Repeat(" ", max(0, width-1-len(arminLabel))))
	a.cachedLines, a.cachedWidth, a.cachedVersion = lines, width, a.gridVersion
	return lines
}

func (a *arminComponent) frameInterval() time.Duration {
	fps := 30
	if a.effect == "glitch" {
		fps = 60
	}
	// Node truncates setInterval's fractional milliseconds.
	return time.Duration(1000/fps) * time.Millisecond
}

// startAnimation owns one timer worker and at most one queued frame. Only the UI owner mutates grids. Dispose cancels and joins even when the owner no longer drains its queue.
func (a *arminComponent) startAnimation(parent context.Context, post func(context.Context, func()) error, requestRender func()) {
	ctx, cancel := context.WithCancel(parent)
	a.cancel = cancel
	a.animationDone = make(chan struct{})
	go func() {
		defer close(a.animationDone)
		ticker := time.NewTicker(a.frameInterval())
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			frameDone := make(chan bool, 1)
			if err := post(ctx, func() {
				if ctx.Err() != nil {
					return
				}
				done := a.tickEffect()
				a.gridVersion++
				a.BaseComponent.Invalidate()
				requestRender()
				frameDone <- done
			}); err != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case done := <-frameDone:
				if done {
					return
				}
			}
		}
	}()
}

func (a *arminComponent) Dispose() {
	if a.cancel != nil {
		a.cancel()
		<-a.animationDone
		a.cancel = nil
	}
}

func (a *arminComponent) tickEffect() bool {
	switch a.effect {
	case "typewriter":
		return a.tickTypewriter()
	case "scanline":
		return a.tickScanline()
	case "rain":
		return a.tickRain()
	case "fade":
		return a.tickResolve(15)
	case "crt":
		return a.tickCrt()
	case "glitch":
		return a.tickGlitch()
	case "dissolve":
		return a.tickResolve(20)
	default:
		return true
	}
}

func (a *arminComponent) tickTypewriter() bool {
	for range 3 {
		row, x := a.pos/arminWidth, a.pos%arminWidth
		if row >= arminDisplayHeight {
			return true
		}
		a.currentGrid[row][x] = a.finalGrid[row][x]
		a.pos++
	}
	return false
}

func (a *arminComponent) tickScanline() bool {
	if a.row >= arminDisplayHeight {
		return true
	}
	a.currentGrid[a.row] = a.finalGrid[a.row]
	a.row++
	return false
}

func (a *arminComponent) tickRain() bool {
	allSettled := true
	a.currentGrid = emptyArminGrid()
	for x := range arminWidth {
		drop := &a.drops[x]
		for row := arminDisplayHeight - 1; row >= arminDisplayHeight-drop.settled; row-- {
			if row >= 0 {
				a.currentGrid[row][x] = a.finalGrid[row][x]
			}
		}
		if drop.settled >= arminDisplayHeight {
			continue
		}
		allSettled = false
		targetRow := -1
		for row := arminDisplayHeight - 1 - drop.settled; row >= 0; row-- {
			if a.finalGrid[row][x] != ' ' {
				targetRow = row
				break
			}
		}
		drop.y++
		if drop.y >= 0 && drop.y < arminDisplayHeight {
			if targetRow >= 0 && drop.y >= targetRow {
				drop.settled = arminDisplayHeight - targetRow
				drop.y = -int(a.random()*5) - 1
			} else {
				a.currentGrid[drop.y][x] = '▓'
			}
		}
	}
	return allSettled
}

func (a *arminComponent) tickResolve(pixelsPerFrame int) bool {
	for range pixelsPerFrame {
		if a.idx >= len(a.positions) {
			return true
		}
		p := a.positions[a.idx]
		a.currentGrid[p[0]][p[1]] = a.finalGrid[p[0]][p[1]]
		a.idx++
	}
	return false
}

func (a *arminComponent) tickCrt() bool {
	midRow := arminDisplayHeight / 2
	a.currentGrid = emptyArminGrid()
	for row := max(0, midRow-a.expansion); row <= min(arminDisplayHeight-1, midRow+a.expansion); row++ {
		a.currentGrid[row] = a.finalGrid[row]
	}
	a.expansion++
	return a.expansion > arminDisplayHeight
}

func (a *arminComponent) tickGlitch() bool {
	if a.phase >= 8 {
		a.currentGrid = a.finalGrid
		return true
	}
	for row, final := range a.finalGrid {
		offset := int(a.random()*7) - 3
		switch {
		case a.random() < 0.3:
			if offset < 0 {
				offset += arminWidth
			}
			for x := range arminWidth {
				a.currentGrid[row][x] = final[(x+offset)%arminWidth]
			}
		case a.random() < 0.2:
			a.currentGrid[row] = a.finalGrid[int(a.random()*arminDisplayHeight)]
		default:
			a.currentGrid[row] = final
		}
	}
	a.phase++
	return false
}
