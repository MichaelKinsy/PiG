// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package gridterm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// tmuxCase is a sequence of writes to a tmux pane of cols by rows cells.
type tmuxCase struct {
	name       string
	cols, rows int
	steps      []tmuxStep
}

// tmuxStep is a write; when cols is not 0 the pane takes that size first.
type tmuxStep struct {
	write      string
	cols, rows int
}

// tmuxLines is what tmux shows of the lines of a screen: for every row the cells capture-pane -N prints (the cells the line has)
// and the cells capture-pane -J prints (how far cells were stored in it).
type tmuxLines struct {
	N, J []int
}

// tmuxRecord is what a tmux version showed after every step of every case.
type tmuxRecord struct {
	Tmux   string                 `json:"tmux"`
	Digest string                 `json:"digest"`
	Lines  map[string][]tmuxLines `json:"lines"`
}

// tmuxCases are rows written, grown, shortened, padded, erased and scrolled the ways that tell tmux's rules for a line apart, in the
// alternate screen with autowrap off, at three widths: text of every length around a quarter and a half of the width, written after
// an erase of the line and over what the row showed; blanks under every kind of pen; erases under a pen; a scroll; a wide
// character and a first cell at the columns where the line's size steps; another screen size.
func tmuxCases() []tmuxCase {
	const erase, reset = "\x1b[2K", "\x1b[0m"
	enter := "\x1b[?1049h\x1b[?7l\x1b[2J\x1b[H"
	at := func(row, col int) string { return fmt.Sprintf("\x1b[%d;%dH", row, col) }
	x := func(n int) string { return strings.Repeat("x", n) }
	pad := func(text string, cols int) string { return text + strings.Repeat(" ", cols-len(text)) }
	var cases []tmuxCase
	add := func(name string, cols int, writes ...string) {
		c := tmuxCase{name: name, cols: cols, rows: 6}
		for _, write := range writes {
			c.steps = append(c.steps, tmuxStep{write: write})
		}
		cases = append(cases, c)
	}
	each := func(lengths []int, write func(n int) string) []string {
		var writes []string
		for _, n := range lengths {
			writes = append(writes, write(n))
		}
		return writes
	}
	for _, cols := range []int{120, 49, 20} {
		q, h := cols/4, cols/2
		lengths := []int{0, 1, q - 1, q, q + 1, h - 1, h, h + 1, cols - 1, cols}
		grown, padded := lengths[2:], lengths[2:len(lengths)-1]
		name := func(s string) string { return fmt.Sprintf("%s at %d", s, cols) }
		add(name("written whole"), cols, append([]string{enter}, each(lengths, func(n int) string { return at(2, 1) + erase + x(n) })...)...)
		add(name("grown in place"), cols, append([]string{enter, at(2, 1) + erase + "x"}, each(grown, func(n int) string { return at(2, 1) + x(n) })...)...)
		add(name("grown, written whole"), cols, append([]string{enter, at(2, 1) + erase + "x"}, each(grown, func(n int) string { return at(2, 1) + erase + x(n) })...)...)
		add(name("padded, grown in place"), cols, append([]string{enter, at(2, 1) + erase + pad("x", cols)}, each(padded, func(n int) string { return at(2, 1) + x(n) })...)...)
		add(name("padded, grown in place through the line's end"), cols, append([]string{enter, at(2, 1) + erase + pad("x", cols)}, each(padded, func(n int) string { return at(2, 1) + pad(x(n), cols) })...)...)
		add(name("padded, grown, written whole"), cols, append([]string{enter, at(2, 1) + erase + pad("x", cols)}, each(padded, func(n int) string { return at(2, 1) + erase + pad(x(n), cols) })...)...)
	}
	const cols = 120
	add("shorter, written whole", cols, enter, at(2, 1)+erase+x(100), at(2, 1)+erase+x(10))
	add("shorter, erased from its end", cols, enter, at(2, 1)+erase+x(100), at(2, 1)+x(10)+"\x1b[K")
	add("shorter, blanks over the rest", cols, enter, at(2, 1)+erase+x(100), at(2, 1)+x(10)+strings.Repeat(" ", 90))
	for _, old := range []int{73, 37} {
		add(fmt.Sprintf("padded %d, shorter, written whole", old), cols, enter, at(2, 1)+erase+pad(x(old), cols), at(2, 1)+erase+pad(strings.Repeat("y", 20), cols))
		add(fmt.Sprintf("padded %d, shorter, in place", old), cols, enter, at(2, 1)+erase+pad(x(old), cols), at(2, 1)+pad(strings.Repeat("y", 20), cols))
	}
	add("blank after padded, written whole", cols, enter, at(2, 1)+erase+pad(x(73), cols), at(2, 1)+erase)
	add("blank after padded, blanks over it", cols, enter, at(2, 1)+erase+pad(x(73), cols), at(2, 1)+strings.Repeat(" ", cols))
	add("blanks, then erased", cols, enter, at(2, 1)+erase+strings.Repeat(" ", cols), at(2, 1)+erase)
	add("blanks, then nothing", cols, enter, at(2, 1)+erase+strings.Repeat(" ", cols), at(2, 1))
	for _, pen := range [][2]string{{"bold", "\x1b[1m"}, {"background", "\x1b[41m"}, {"colour", "\x1b[31m"}, {"underline", "\x1b[4m"}, {"reverse", "\x1b[7m"},
		{"dim", "\x1b[2m"}, {"hyperlink", "\x1b]8;;http://a\x07"}, {"default pen", ""}} {
		add("blanks under "+pen[0], cols, enter, at(2, 1)+reset+erase+"ab"+pen[1]+strings.Repeat(" ", 38)+reset+"\x1b]8;;\x07")
	}
	add("erases under a pen", cols, enter, at(2, 1)+"\x1b[41m"+erase+reset, at(2, 1)+erase, at(2, 1)+"abc\x1b[41m\x1b[K"+reset, at(2, 1)+erase+"abc", at(2, 1)+"\x1b[1m"+erase+reset,
		at(3, 1)+"abcdefgh"+at(3, 5)+"\x1b[42m\x1b[1K"+reset, at(4, 1)+"abcdefgh"+at(4, 5)+"\x1b[1K", "\x1b[43m\x1b[2J"+reset, "\x1b[2J", at(2, 1)+"\x1b[31m"+erase+reset,
		at(5, 1)+"abc"+at(5, 1)+"\x1b[K", at(5, 1)+"abcdef"+at(5, 2)+"\x1b[K")
	add("scrolled", cols, enter, at(6, 1)+erase+x(100)+at(5, 1)+erase+strings.Repeat("y", 10), "\x1b[2;6r"+at(6, 1)+"\n\x1b[r", "\x1b[2;6r"+at(2, 1)+"\x1bM\x1b[r",
		"\x1b[41m\x1b[2;6r"+at(6, 1)+"\n\x1b[r"+reset, "\x1b[42m\x1b[2;6r"+at(2, 1)+"\x1bM\x1b[r"+reset)
	add("blanks inside and past the line", cols, enter, at(2, 1)+erase+"abc", at(2, 10)+" ", at(2, 40)+" ", at(2, 40)+"\x1b[1m "+reset, at(2, 100)+" ")
	add("wide character at a step", cols, enter, at(2, 1)+erase+x(27)+"\u65e5", at(2, 1)+erase+x(28)+"\u65e5", at(2, 1)+erase+x(29)+"\u65e5", at(2, 1)+erase+strings.Repeat(" ", 28)+"\u65e5")
	add("alternate screen left and entered", cols, enter, at(2, 1)+erase+x(100), "\x1b[?1049l\x1b[?1049h\x1b[?7l", at(2, 1)+"ab")
	add("first cell at a quarter", cols, enter, at(2, 1)+erase+strings.Repeat(" ", 29)+"x")
	add("first cell at a half", cols, enter, at(2, 1)+erase+strings.Repeat(" ", 59)+"x")
	add("cell at a half after text", cols, enter, at(2, 1)+erase+"abc"+strings.Repeat(" ", 56)+"x")
	add("first cell at a quarter, in place", cols, enter, at(2, 1)+erase+pad("a", cols), at(2, 1)+pad(strings.Repeat(" ", 29)+"x", cols))
	add("cell at a half after text, in place", cols, enter, at(2, 1)+erase+pad(x(40), cols), at(2, 1)+pad("abc"+strings.Repeat(" ", 56)+"x", cols))
	add("first cell at a quarter, addressed", cols, enter, at(2, 1)+erase+at(2, 30)+"x")
	// An image's rows under an overlay, in the three frames the alternate screen writes for them (the image, an overlay over it, the
	// image again once the overlay is closed), each followed by a text row as the screen writer sends it after a frame it sent as it
	// came: whole, and then grown in place. For a Kitty terminal a frame that draws an image deletes the placements, erases every
	// row and writes the text rows and then the image's; for iTerm2 it erases the screen and writes every row, the image's with the
	// cursor moved up first. tmux shows no image: it stores no cell for one and leaves the cursor where it was.
	const narrow = 40
	kitty, iterm, unplace := "\x1b_Ga=T,f=100,q=2,C=1,c=4,r=3,i=7;AAAA\x1b\\", "\x1b]1337;File=inline=1;width=4;height=3:AAAA\x07", "\x1b_Ga=d,d=a,q=2\x1b\\"
	text := func(row int, s string) string { return at(row, 1) + pad(s, narrow) + reset }
	whole := func(row int, s string) string { return at(row, 1) + erase + s + reset }
	erased := ""
	for row := 1; row <= 6; row++ {
		erased += at(row, 1) + erase
	}
	add("Kitty image rows under an overlay", narrow, enter,
		unplace+erased+text(1, "top")+text(5, "after")+at(2, 1)+kitty+reset,
		whole(5, pad("after!", narrow)), at(5, 1)+"after!!",
		unplace+erased+text(1, "top")+text(3, " PANEL")+text(5, "after!!"),
		whole(3, pad(" PANEL!", narrow)), at(3, 1)+" PANEL!!",
		unplace+erased+text(1, "top")+text(5, "after!!")+at(2, 1)+kitty+reset,
		whole(3, pad("x", narrow)), at(3, 1)+"xy")
	add("iTerm2 image rows under an overlay", narrow, enter,
		"\x1b[2J"+whole(1, pad("top", narrow))+whole(2, "")+whole(3, "")+whole(4, "\x1b[2A"+iterm)+whole(5, pad("after", narrow)),
		whole(5, pad("after!", narrow)), at(5, 1)+"after!!",
		"\x1b[2J"+whole(1, pad("top", narrow))+whole(2, "")+whole(3, pad(" PANEL", narrow))+whole(4, "")+whole(5, pad("after!!", narrow)),
		whole(3, pad(" PANEL!", narrow)), at(3, 1)+" PANEL!!",
		"\x1b[2J"+whole(1, pad("top", narrow))+whole(2, "")+whole(3, "")+whole(4, "\x1b[2A"+iterm)+whole(5, pad("after!!", narrow)),
		whole(3, pad("x", narrow)), at(3, 1)+"xy")
	resized := tmuxCase{name: "another size", cols: cols, rows: 6, steps: []tmuxStep{{write: enter}, {write: at(2, 1) + erase + x(100) + at(3, 1) + erase + strings.Repeat("y", 10)},
		{cols: 60, rows: 6}, {write: at(3, 1) + strings.Repeat("y", 20)}, {cols: 160, rows: 6}, {write: at(3, 1) + strings.Repeat("y", 30)}, {write: at(2, 1) + erase + "x"},
		{write: at(4, 1) + erase + strings.Repeat("z", 39)}}}
	return append(cases, resized)
}

// tmuxDigest names the cases a record was made of.
func tmuxDigest(cases []tmuxCase) string {
	sum := sha256.New()
	for _, c := range cases {
		_, _ = fmt.Fprintf(sum, "%q %d %d\n", c.name, c.cols, c.rows)
		for _, step := range c.steps {
			_, _ = fmt.Fprintf(sum, "%q %d %d\n", step.write, step.cols, step.rows)
		}
	}
	return hex.EncodeToString(sum.Sum(nil))[:16]
}

// gridLines is what the grid has for the lines of c after every step.
func gridLines(c tmuxCase) []tmuxLines {
	v := New(c.cols, c.rows, Models[0])
	var all []tmuxLines
	for _, step := range c.steps {
		if step.cols != 0 {
			v.Resize(step.cols, step.rows)
		}
		v.Write(step.write)
		var lines tmuxLines
		for r := range v.rows {
			lines.N, lines.J = append(lines.N, min(v.size[r], v.cols)), append(lines.J, min(v.stored[r], v.cols))
		}
		all = append(all, lines)
	}
	return all
}

// tmuxNext is what to do about a tmux whose lines are not the grid's.
const tmuxNext = "The screen writer writes a row in place only where that leaves tmux the line the renderers' bytes leave " +
	"(internal/screenwriter), and the grid is its model of that line: under a tmux whose lines differ the writer may leave other " +
	"lines. Record this version's lines (PIG_GRIDTERM_RECORD=1 go test -run TestLinesAreWhatTmuxShows ./internal/screenwriter/gridterm) " +
	"and give the grid its rule; until then PIG_TUI_SCREEN_WRITER=pi selects the renderers' bytes."

// tmuxDifference compares the grid's lines with the ones tmux showed: the rows compared, the steps that differ, and the first of them.
func tmuxDifference(cases []tmuxCase, shown map[string][]tmuxLines) (rows, differing int, first string) {
	differ := func(format string, args ...any) {
		if differing++; first == "" {
			first = fmt.Sprintf(format, args...)
		}
	}
	for _, c := range cases {
		grid, lines := gridLines(c), shown[c.name]
		if len(lines) != len(grid) {
			differ("case %q: %d steps shown, the case has %d", c.name, len(lines), len(grid))
			continue
		}
		for step := range grid {
			rows += len(grid[step].N)
			if !slices.Equal(grid[step].N, lines[step].N) || !slices.Equal(grid[step].J, lines[step].J) {
				differ("case %q, step %d: capture-pane -N shows lines of %v cells and capture-pane -J %v cells stored; the grid has lines of %v cells and %v stored",
					c.name, step, lines[step].N, lines[step].J, grid[step].N, grid[step].J)
			}
		}
	}
	return rows, differing, first
}

// The grid's lines are tmux's: after every step of every case the cells a line has and the cells stored in it are the ones
// tmux 3.4 and 3.6 showed (testdata/tmux-*.json, which TestLinesAreWhatTmuxShows records from the tmux it runs).
func TestLinesAreWhatTmuxRecorded(t *testing.T) {
	cases := tmuxCases()
	files, err := filepath.Glob("testdata/tmux-*.json")
	if err != nil || len(files) < 2 {
		t.Fatalf("%d records of tmux in testdata (%v), want those of 3.4 and 3.6", len(files), err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var record tmuxRecord
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if record.Digest != tmuxDigest(cases) {
			t.Fatalf("%s was recorded for other cases: record it again (PIG_GRIDTERM_RECORD=1 go test -run TestLinesAreWhatTmuxShows, with that tmux in PATH)", file)
		}
		rows, differing, first := tmuxDifference(cases, record.Lines)
		if differing > 0 {
			t.Errorf("tmux %s, as recorded in %s, shows other lines than the grid has after %d steps; the first: %s. %s", record.Tmux, file, differing, first, tmuxNext)
			continue
		}
		t.Logf("tmux %s (recorded): %d cases, %d rows compared", record.Tmux, len(cases), rows)
	}
}
