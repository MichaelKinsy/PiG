// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-FileCopyrightText: Copyright (c) 2026 OpenSec (Pi-Bolt)
// SPDX-License-Identifier: MIT

package tui

import (
	"os"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// The move detection follows Pi-Bolt's TuiAltScreen.findMovedRows (packages/tui/src/tui-alt-screen.ts, MIT; see
// THIRD_PARTY_NOTICES.md).

// minRowsSavedByScrolling is how many row writes a move must save before a frame scrolls rows instead of drawing them.
// pig divergence (D205): Pi draws every row that changed.
const minRowsSavedByScrolling = 3

// scrollRegionsUsable reports whether the terminal PiG runs in is trusted with scroll regions (DECSTBM). A frame that scrolls
// writes other bytes than Pi's, so it needs a terminal that implements CSI top;bottom r, line feed at the region's bottom and
// reverse index at its top. PIG_TUI_SCROLL_ROWS=0 turns it off. So does a missing or "dumb"/"unknown" TERM (no terminfo
// entry to say the terminal has a change-scroll-region capability), the terminals whose terminfo entries (vt52, adm3a, tvi912
// and the like) have no such capability, and Zellij, whose scroll-region handling is its own (ZELLIJ is set inside it). Every
// such terminal gets Pi's bytes, every changed row drawn.
// pig divergence (D205): Pi never scrolls a region.
func scrollRegionsUsable() bool {
	return scrollRegionsUsableIn(os.Getenv)
}

// scrollRegionsUsableIn is scrollRegionsUsable on the environment getenv reads.
func scrollRegionsUsableIn(getenv func(string) string) bool {
	if getenv("PIG_TUI_SCROLL_ROWS") == "0" || getenv("ZELLIJ") != "" {
		return false
	}
	switch term := strings.ToLower(getenv("TERM")); term {
	case "", "dumb", "unknown", "vt52", "adm3a", "tvi912", "tvi920", "tvi950", "lpr", "network", "ansi.sys":
		return false
	}
	return true
}

// synchronizedUpdatesTrusted reports whether the terminal PiG runs in is trusted to show a frame at once at its end (synchronized updates, mode 2026: the bracket every frame is written in). Frames drawn more often than Pi draws them (FramePacing) need it: a terminal that ignores the bracket would show their rows as they arrive.
// pig additive (D625): Pi never asks which terminal it runs in before it schedules a frame.
func synchronizedUpdatesTrusted() bool {
	return synchronizedUpdatesTrustedIn(os.Getenv)
}

// synchronizedUpdatesRelease is the positive list behind synchronizedUpdatesTrustedIn, by the name a terminal gives itself in TERM_PROGRAM (lower case): the first release that implements mode 2026, as the leading numbers of the TERM_PROGRAM_VERSION that release states, and whether the terminal must state a version to be trusted. The record (D625) names the source of every release.
//
//	kitty      0.22.0 (repository tag v0.22.0 holds the implementing commit 5768c54c, v0.21.2 does not); exports no version
//	Ghostty    1.0.0, its first public release (repository tag v1.0.0 holds the implementing commit 2cc1e437); exports TERM_PROGRAM_VERSION
//	WezTerm    20210814-124438-54e29167 (its changelog: "Added support for Synchronized Rendering"); exports TERM_PROGRAM_VERSION
//	Warp       v0.2025.01.15.08.02.stable_02 (a maintainer's statement on warpdotdev/Warp#2185, no changelog line)
//	iTerm2     3.5.0, first in 3.5.0beta1 (its release notes: "Add DECSET 2026 for synchronized updates"); exports TERM_PROGRAM_VERSION
//	Alacritty  0.13.0 (its changelog: "Synchronized updates now use CSI 2026"); exports no version
//	VS Code    1.108 (its release notes of December 2025: "Synchronized output"); exports TERM_PROGRAM_VERSION, and releases without the mode are common, so the version is required
//
// pig additive (D625): the list and its floors are PiG's; Pi has neither.
func synchronizedUpdatesRelease(termProgram string) (floor []int, versionRequired, listed bool) {
	switch termProgram {
	case "kitty", "alacritty":
		return nil, false, true
	case "ghostty":
		return []int{1, 0, 0}, false, true
	case "wezterm":
		return []int{20210814}, false, true
	case "warpterminal":
		return []int{0, 2025, 1, 15, 8, 2}, false, true
	case "iterm.app":
		return []int{3, 5, 0}, false, true
	case "vscode":
		return []int{1, 108}, true, true
	}
	return nil, false, false
}

// synchronizedUpdatesTrustedIn is synchronizedUpdatesTrusted on the environment getenv reads. A terminal is trusted when it is trusted with scroll regions (scrollRegionsUsableIn), nothing stands between it and PiG (inside tmux, GNU screen, Zellij or Herdr the environment names the multiplexer or still names the terminal behind it, and what reaches that terminal is the multiplexer's), and the environment names a terminal of the list (synchronizedUpdatesRelease) by a name Pi's capability detection reads (terminal_image.go).
//
// A terminal that names itself in TERM_PROGRAM is judged by that name alone, so a variable left by another terminal it was started from decides nothing: an unlisted name is not trusted; a listed one is trusted unless the TERM_PROGRAM_VERSION beside it states a release older than the first with mode 2026. A version that is missing or does not begin with a number says nothing, and the terminal is trusted by its name, except the VS Code terminal, which is trusted only from 1.108 on. Without TERM_PROGRAM the terminal's own variables name it, and none of them carries a version. Every other terminal, an unknown one included, gets Pi's interval, and so does Windows Terminal, which Pi's detection also names: nothing in its environment tells a release with the mode from one without.
func synchronizedUpdatesTrustedIn(getenv func(string) string) bool {
	if !scrollRegionsUsableIn(getenv) {
		return false
	}
	term := strings.ToLower(getenv("TERM"))
	if getenv("TMUX") != "" || getenv("STY") != "" || getenv("HERDR_ENV") != "" || strings.HasPrefix(term, "tmux") || strings.HasPrefix(term, "screen") {
		return false
	}
	if program := strings.ToLower(getenv("TERM_PROGRAM")); program != "" {
		floor, versionRequired, listed := synchronizedUpdatesRelease(program)
		if !listed {
			return false
		}
		version, stated := leadingVersionNumbers(getenv("TERM_PROGRAM_VERSION"))
		if !stated {
			return !versionRequired
		}
		return !versionBelow(version, floor)
	}
	if strings.Contains(term, "ghostty") || strings.HasPrefix(term, "alacritty") {
		return true
	}
	for _, name := range [...]string{"KITTY_WINDOW_ID", "GHOSTTY_RESOURCES_DIR", "WEZTERM_PANE", "WARP_SESSION_ID", "WARP_TERMINAL_SESSION_UUID", "ITERM_SESSION_ID", "ALACRITTY_WINDOW_ID"} {
		if getenv(name) != "" {
			return true
		}
	}
	return false
}

// leadingVersionNumbers reads the numbers a version begins with: decimal numbers separated by dots, after an optional "v" ("3.5.0beta1" is 3, 5, 0; "20210814-124438-54e29167" is 20210814; "v0.2025.01.15.08.02.stable_02" is 0, 2025, 1, 15, 8, 2). ok is false when the version does not begin with a number.
func leadingVersionNumbers(version string) (numbers []int, ok bool) {
	version = strings.TrimPrefix(version, "v")
	for {
		digits := 0
		for digits < len(version) && version[digits] >= '0' && version[digits] <= '9' {
			digits++
		}
		if digits == 0 || digits > 15 {
			break
		}
		number, _ := strconv.Atoi(version[:digits])
		numbers = append(numbers, number)
		if version = version[digits:]; !strings.HasPrefix(version, ".") {
			break
		}
		version = version[1:]
	}
	return numbers, len(numbers) > 0
}

// versionBelow reports whether version is older than floor, number by number; a number version lacks counts as 0.
func versionBelow(version, floor []int) bool {
	for i, want := range floor {
		have := 0
		if i < len(version) {
			have = version[i]
		}
		if have != want {
			return have < want
		}
	}
	return false
}

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
