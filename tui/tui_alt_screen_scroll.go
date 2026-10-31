// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-FileCopyrightText: Copyright (c) 2026 OpenSec (Pi-Bolt)
// SPDX-License-Identifier: MIT

package tui

import (
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// The move detection follows Pi-Bolt's TuiAltScreen.findMovedRows (packages/tui/src/tui-alt-screen.ts, MIT; see
// THIRD_PARTY_NOTICES.md).

// minRowsSavedByScrolling is how many row writes a move must save before a frame scrolls rows instead of drawing them.
// pig divergence (D205): Pi draws every row that changed.
const minRowsSavedByScrolling = 3

// movedRows is the part of the screen whose rows moved since the last frame: rows top..bottom moved up by up rows, or down
// when up is negative.
type movedRows struct {
	top, bottom, up int
}

// findMovedRows finds the move after which the fewest rows of screen differ from the rows previous showed. When the
// transcript grows by a line or is scrolled, every row of its viewport changes, though all but a few only moved. ok is
// false when the move would save fewer than minRowsSavedByScrolling row writes, or when a row of the moved part of either
// frame holds an image, which its own sequences place. doRender redraws every row of a frame in which a changed row holds
// an image in either frame, and calls this only otherwise, so only the rows that stay are looked at for one.
func findMovedRows(screen, previous []string, height int) (moved movedRows, ok bool) {
	top := 0
	for top < height && altScreenRow(screen, top) == altScreenRow(previous, top) {
		top++
	}
	bottom := height - 1
	for bottom > top && altScreenRow(screen, bottom) == altScreenRow(previous, bottom) {
		bottom--
	}
	span := bottom - top + 1
	if span <= minRowsSavedByScrolling {
		return movedRows{}, false
	}
	changed := 0
	for row := top; row <= bottom; row++ {
		if altScreenRow(screen, row) != altScreenRow(previous, row) {
			changed++
		}
	}
	if changed <= minRowsSavedByScrolling {
		return movedRows{}, false
	}
	// The distance at which the most rows are what another row was; those need not be drawn after the move.
	best, up := 0, 0
	for distance := 1; span-distance > best; distance++ {
		sameUp, sameDown := 0, 0
		for row := top; row+distance <= bottom; row++ {
			if altScreenRow(screen, row) == altScreenRow(previous, row+distance) {
				sameUp++
			}
			if altScreenRow(screen, row+distance) == altScreenRow(previous, row) {
				sameDown++
			}
		}
		if sameUp > best {
			best, up = sameUp, distance
		}
		if sameDown > best {
			best, up = sameDown, -distance
		}
	}
	if changed-(span-best) < minRowsSavedByScrolling {
		return movedRows{}, false
	}
	for row := top; row <= bottom; row++ {
		if line := altScreenRow(screen, row); line == altScreenRow(previous, row) && widthx.IsImageLine(line) {
			return movedRows{}, false
		}
	}
	return movedRows{top: top, bottom: bottom, up: up}, true
}

// shownAfter is the row the terminal shows at row once moved has scrolled previous; known is false for a row scrolled
// in, which is blank.
func (moved movedRows) shownAfter(previous []string, row int) (shown string, known bool) {
	if row < moved.top || row > moved.bottom {
		return altScreenRow(previous, row), true
	}
	from := row + moved.up
	if from < moved.top || from > moved.bottom {
		return "", false
	}
	return altScreenRow(previous, from), true
}

// writeRowsMove scrolls rows moved.top..moved.bottom inside a scroll region: line feeds at its bottom move them up,
// reverse index at its top moves them down. The region is then reset to the whole screen, which homes the cursor.
func writeRowsMove(buf *strings.Builder, moved movedRows) {
	var digits [20]byte
	buf.WriteString("\x1b[")
	buf.Write(strconv.AppendInt(digits[:0], int64(moved.top+1), 10))
	buf.WriteByte(';')
	buf.Write(strconv.AppendInt(digits[:0], int64(moved.bottom+1), 10))
	buf.WriteByte('r')
	if moved.up > 0 {
		writeCursorTo(buf, moved.bottom+1, 1)
		buf.WriteString(strings.Repeat("\n", moved.up))
	} else {
		writeCursorTo(buf, moved.top+1, 1)
		buf.WriteString(strings.Repeat("\x1bM", -moved.up))
	}
	buf.WriteString("\x1b[r")
}
