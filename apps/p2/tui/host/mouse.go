package host

// The launcher's view of the mouse (p2-tui-port.md section 3.1 rule 5): mouse reports go to the runtime as input (pi-tui scrolls and selects there),
// and the launcher recognizes, from the raw SGR reports alone, the release that ends a selection gesture: a drag with the left button, or a
// double/triple click. Only that gesture copies.
//
// pi: apps/p2/src/hosts/tui-mouse.ts

import (
	"strconv"
	"time"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

var sgrMouse = lazyregexp.New(`\x1b\[<(\d+);(\d+);(\d+)([Mm])`)

const multiClick = 500 * time.Millisecond

// SelectionGesture recognizes the release that ends a selection.
type SelectionGesture struct {
	pressed *struct{ x, y, clicks int }
	moved   bool
	last    *struct {
		x, y   int
		at     time.Time
		clicks int
	}
}

// Feed feeds raw input; it is true when it holds the left-button release that ends a drag or a multi-click.
func (g *SelectionGesture) Feed(data string, now time.Time) bool {
	gesture := false
	for _, match := range sgrMouse.FindAllStringSubmatch(data, -1) {
		code, _ := strconv.Atoi(match[1])
		x, _ := strconv.Atoi(match[2])
		y, _ := strconv.Atoi(match[3])
		release := match[4] == "m"
		wheel := code&64 != 0
		motion := code&32 != 0
		left := code&3 == 0
		if wheel || !left {
			continue
		}
		switch {
		case release:
			if g.pressed != nil {
				gesture = gesture || g.moved || g.pressed.clicks >= 2
			}
			g.pressed = nil
		case motion:
			if g.pressed != nil && (x != g.pressed.x || y != g.pressed.y) {
				g.moved = true
			}
		default:
			clicks := 1
			if last := g.last; last != nil && now.Sub(last.at) <= multiClick && abs(last.x-x) <= 1 && last.y == y {
				clicks = last.clicks + 1
			}
			g.pressed = &struct{ x, y, clicks int }{x, y, clicks}
			g.last = &struct {
				x, y   int
				at     time.Time
				clicks int
			}{x, y, now, clicks}
			g.moved = false
		}
	}
	return gesture
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
