// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-FileCopyrightText: Copyright (c) 2026 OpenSec (Pi-Bolt)
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// rowsVT is a terminal reduced to what the alternate screen's frames do with it: rows written whole (cursor position,
// erase line, the line), the screen erased, and the rows of a scroll region scrolled by line feed at its bottom and
// reverse index at its top. A row holds the text, SGR and OSC sequences written to it since it was erased; APC
// sequences (Kitty graphics, Pi's cursor marker), which end at ST or BEL, are not kept. Equal rows show the same cells,
// since the renderer writes each row whole with autowrap off, and a scroll moves whole rows.
type rowsVT struct {
	rows        []string
	row         int
	top, bottom int
}

func newRowsVT(height int) *rowsVT { return &rowsVT{rows: make([]string, height), bottom: height - 1} }

func (v *rowsVT) apply(s string) {
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == '\n':
			if v.row == v.bottom {
				copy(v.rows[v.top:v.bottom], v.rows[v.top+1:v.bottom+1])
				v.rows[v.bottom] = ""
			} else if v.row < len(v.rows)-1 {
				v.row++
			}
			i++
		case c == '\r':
			i++
		case c != '\x1b':
			j := i + 1
			for j < len(s) && s[j] != '\x1b' && s[j] != '\n' && s[j] != '\r' {
				j++
			}
			v.rows[v.row] += s[i:j]
			i = j
		case i+1 == len(s):
			return
		case s[i+1] == '[':
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j == len(s) {
				return
			}
			params, final := s[i+2:j], s[j]
			switch {
			case final == 'm':
				v.rows[v.row] += s[i : j+1]
			case params != "" && strings.ContainsRune("?<=>", rune(params[0])):
			case final == 'H':
				first, _, _ := strings.Cut(params, ";")
				row, err := strconv.Atoi(first)
				if err != nil || row < 1 {
					row = 1
				}
				v.row = min(row, len(v.rows)) - 1
			case final == 'K' && params == "2":
				v.rows[v.row] = ""
			case final == 'J' && params == "2":
				clear(v.rows)
			case final == 'r':
				v.top, v.bottom = 0, len(v.rows)-1
				if top, bottom, ok := strings.Cut(params, ";"); ok {
					t, _ := strconv.Atoi(top)
					b, _ := strconv.Atoi(bottom)
					v.top, v.bottom = t-1, b-1
				}
				v.row = 0
			}
			i = j + 1
		case s[i+1] == ']':
			j := i + 2
			for j < len(s) && s[j] != '\x07' && (s[j] != '\x1b' || j+1 == len(s) || s[j+1] != '\\') {
				j++
			}
			if j < len(s) && s[j] == '\x1b' {
				j++
			}
			j = min(j+1, len(s))
			v.rows[v.row] += s[i:j]
			i = j
		case s[i+1] == '_':
			j := i + 2
			for j < len(s) && s[j] != '\x07' && (s[j] != '\x1b' || j+1 == len(s) || s[j+1] != '\\') {
				j++
			}
			if j < len(s) && s[j] == '\x1b' {
				j++
			}
			i = j + 1
		case s[i+1] == 'M':
			if v.row == v.top {
				copy(v.rows[v.top+1:v.bottom+1], v.rows[v.top:v.bottom])
				v.rows[v.top] = ""
			} else if v.row > 0 {
				v.row--
			}
			i += 2
		default:
			i += 2
		}
	}
}

// scrollTestLines is a component that renders its lines.
type scrollTestLines struct{ lines []string }

func (c *scrollTestLines) Render(int) []string { return slices.Clone(c.lines) }
func (*scrollTestLines) Invalidate()           {}

// newScrollTestScreen is an alternate screen of cols x rows writing to out, whose frames are drawn only by Render.
func newScrollTestScreen(t testing.TB, out *strings.Builder, cols, rows int, components ...Component) *TuiAltScreen {
	t.Helper()
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	t.Cleanup(ResetCapabilitiesCache)
	ui := NewTuiAltScreenWithOutput(out, cols, rows, TuiAltScreenOptions{})
	ui.afterFunc = func(time.Duration, func()) stoppableTimer { return stoppedOracleTimer{} }
	for _, component := range components {
		ui.Add(component)
	}
	ui.Start()
	t.Cleanup(ui.Stop)
	return ui
}

// shownRows is the frame's rows as a terminal of height rows shows them: rows past the frame are blank.
func shownRows(screen []string, height int) []string {
	rows := make([]string, height)
	copy(rows, screen)
	return rows
}

var scrollRegionRe = regexp.MustCompile(`\x1b\[(\d+);(\d+)r`)

// When the transcript grows by a line, every row of the viewport changes, though all but one only moved up: the frame
// scrolls them in a scroll region and draws the new row, and the terminal shows what drawing every row shows.
func TestAltScreenScrollsTheRowsThatMoved(t *testing.T) {
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("\x1b[3%dmline %d\x1b[0m", i%7, i+1)
	}
	var out strings.Builder
	text := &scrollTestLines{lines: lines}
	ui := newScrollTestScreen(t, &out, 40, 12, text)
	vt := newRowsVT(12)
	vt.apply(out.String())
	drawnAll := len(out.String())
	out.Reset()

	text.lines = append(text.lines, "line 31")
	ui.Render()
	frame := out.String()
	if !strings.Contains(frame, "\x1b[1;12r\x1b[12;1H\n\x1b[r") {
		t.Fatalf("the frame does not scroll rows 1-12 up by one: %q", frame)
	}
	if strings.Count(frame, "\x1b[2K") != 1 || len(frame) > drawnAll/4 {
		t.Fatalf("the frame draws %d rows in %d bytes; drawing every row took %d", strings.Count(frame, "\x1b[2K"), len(frame), drawnAll)
	}
	vt.apply(frame)
	if want := shownRows(ui.GetScreenLines(), 12); !slices.Equal(vt.rows, want) {
		t.Fatalf("the terminal shows\n%q\nwant\n%q", vt.rows, want)
	}
}

// Scrolled back, the rows move down: reverse index at the top of the region.
func TestAltScreenScrollsRowsDownWhenScrolledBack(t *testing.T) {
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	var out strings.Builder
	ui := newScrollTestScreen(t, &out, 40, 12, &scrollTestLines{lines: lines})
	vt := newRowsVT(12)
	vt.apply(out.String())
	out.Reset()

	ui.ScrollBy(-3)
	ui.Render()
	frame := out.String()
	if !strings.Contains(frame, "\x1bM\x1bM\x1bM\x1b[r") {
		t.Fatalf("the frame does not scroll the rows down by three: %q", frame)
	}
	vt.apply(frame)
	if want := shownRows(ui.GetScreenLines(), 12); !slices.Equal(vt.rows, want) {
		t.Fatalf("the terminal shows\n%q\nwant\n%q", vt.rows, want)
	}
}

// Whatever a frame changes (lines added, extended, deleted, inserted or changed, the viewport scrolled, the rows below the
// transcript changed), the terminal shows the frame's rows after it, and many frames scroll. Rows that are alike (empty,
// repeated) must not be taken for rows that moved.
func TestAltScreenScrolledFramesShowEveryRow(t *testing.T) {
	scrolled := 0
	for seed := range uint64(24) {
		rng := rand.New(rand.NewPCG(seed, 205))
		cols, rows := 30+rng.IntN(30), 6+rng.IntN(20)
		text, dock := &scrollTestLines{}, &scrollTestLines{lines: []string{"dock"}}
		for i := range rng.IntN(60) {
			text.lines = append(text.lines, fmt.Sprintf("line %d", i))
		}
		var out strings.Builder
		ui := newScrollTestScreen(t, &out, cols, rows, text, dock)
		vt := newRowsVT(rows)
		vt.apply(out.String())
		styled := func(value string) string {
			switch kind := rng.Float64(); {
			case kind < 0.25:
				return ""
			case kind < 0.5:
				return "the same line"
			case kind < 0.75:
				return fmt.Sprintf("\x1b[4%d;1m%s\x1b[0m", rng.IntN(7), value)
			}
			return value
		}
		for step := range 80 {
			out.Reset()
			switch action := rng.Float64(); {
			case action < 0.35:
				for count := 1 + rng.IntN(4); count > 0; count-- {
					text.lines = append(text.lines, styled(fmt.Sprintf("added %d.%d.%d", seed, step, count)))
				}
			case action < 0.5 && len(text.lines) > 0:
				text.lines[len(text.lines)-1] += " more"
			case action < 0.6 && len(text.lines) > 0:
				at := rng.IntN(len(text.lines))
				text.lines = slices.Delete(text.lines, at, min(len(text.lines), at+1+rng.IntN(3)))
			case action < 0.7:
				text.lines = slices.Insert(text.lines, rng.IntN(len(text.lines)+1), styled(fmt.Sprintf("inserted %d", step)), "")
			case action < 0.8 && len(text.lines) > 0:
				text.lines[rng.IntN(len(text.lines))] = styled(fmt.Sprintf("changed %d %s", step, strings.Repeat("wide 日本語 ", rng.IntN(8))))
			case action < 0.9:
				ui.ScrollBy((1 + rng.IntN(5)) * []int{-1, 1}[rng.IntN(2)])
			default:
				dock.lines = []string{fmt.Sprintf("dock %d", step)}
				if rng.IntN(2) == 0 {
					dock.lines = append(dock.lines, "second row")
				}
			}
			ui.Render()
			frame := out.String()
			if scrollRegionRe.MatchString(frame) {
				scrolled++
			}
			vt.apply(frame)
			if want := shownRows(ui.GetScreenLines(), rows); !slices.Equal(vt.rows, want) {
				t.Fatalf("seed %d step %d (%dx%d): the terminal shows\n%q\nwant\n%q\nframe %q", seed, step, cols, rows, vt.rows, want, frame)
			}
		}
	}
	if scrolled < 100 {
		t.Fatalf("only %d frames scrolled rows", scrolled)
	}
}

// Images are placed by their own sequences, so no frame scrolls a row that holds one in the frame before or in its own.
func TestAltScreenDoesNotScrollImageRows(t *testing.T) {
	withImages, scrolled := 0, 0
	for seed := range uint64(8) {
		rng := rand.New(rand.NewPCG(seed, 205))
		text := &scrollTestLines{}
		for i := range 20 {
			text.lines = append(text.lines, fmt.Sprintf("line %d", i))
		}
		var out strings.Builder
		ui := newScrollTestScreen(t, &out, 50, 10, text)
		for step := range 60 {
			out.Reset()
			previous := shownRows(ui.GetScreenLines(), 10)
			switch action := rng.Float64(); {
			case action < 0.4:
				text.lines = append(text.lines, fmt.Sprintf("added %d", step))
			case action < 0.6:
				// Images repeat, so an image row can stay what it was while the rows around it move.
				text.lines = append(text.lines, fmt.Sprintf("\x1b_Ga=p,i=%d,q=2;\x1b\\image", 1+rng.IntN(2)))
			case action < 0.75:
				text.lines = append(text.lines, "\x1b]1337;File=inline=1:AAAA\x07 image")
			case action < 0.9 && len(text.lines) > 0:
				at := rng.IntN(len(text.lines))
				text.lines = slices.Delete(text.lines, at, at+1)
			default:
				ui.ScrollBy([]int{-3, 3}[rng.IntN(2)])
			}
			ui.Render()
			screen := shownRows(ui.GetScreenLines(), 10)
			if slices.ContainsFunc(screen, widthx.IsImageLine) {
				withImages++
			}
			for _, region := range scrollRegionRe.FindAllStringSubmatch(out.String(), -1) {
				scrolled++
				top, _ := strconv.Atoi(region[1])
				bottom, _ := strconv.Atoi(region[2])
				for row := top - 1; row < bottom; row++ {
					if widthx.IsImageLine(previous[row]) || widthx.IsImageLine(screen[row]) {
						t.Fatalf("seed %d step %d: rows %d-%d are scrolled with an image in row %d", seed, step, top, bottom, row+1)
					}
				}
			}
		}
	}
	if withImages < 100 || scrolled == 0 {
		t.Fatalf("%d frames showed images and %d scrolled rows", withImages, scrolled)
	}
}

// An image row that stays where it was while the rows around it change (an image in the dock under a growing transcript, the
// dock's last row changing) is not scrolled, though the rows of the transcript moved: scrolling the region would move the
// image's placement. The same layout with a text row in its place scrolls.
func TestAltScreenDoesNotScrollAnImageRowThatStays(t *testing.T) {
	for _, pinned := range []string{"\x1b_Ga=p,i=1,q=2;\x1b\\image", "pinned text"} {
		lines := &scrollTestLines{}
		for i := range 30 {
			lines.lines = append(lines.lines, fmt.Sprintf("line %d", i))
		}
		dock := &scrollTestLines{lines: []string{"dock"}}
		root := NewVStack([]StackEntry{
			{Component: NewScrollView(lines, ScrollViewOptions{Follow: "end", Primary: true}), StackEntryOptions: StackEntryOptions{Basis: new(0), Grow: new(1), Shrink: new(1), MinSize: new(1)}},
			{Component: &scrollTestLines{lines: []string{pinned}}},
			{Component: dock},
		}, StackOptions{})
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		t.Cleanup(ResetCapabilitiesCache)
		var out strings.Builder
		ui := NewTuiAltScreenWithOutput(&out, 40, 12, TuiAltScreenOptions{})
		ui.afterFunc = func(time.Duration, func()) stoppableTimer { return stoppedOracleTimer{} }
		ui.SetLayoutRoot(root)
		ui.Start()
		t.Cleanup(ui.Stop)
		scrolled := 0
		for step := range 10 {
			out.Reset()
			lines.lines = append(lines.lines, fmt.Sprintf("added %d", step))
			dock.lines = []string{fmt.Sprintf("dock %d", step)}
			ui.Render()
			if scrollRegionRe.MatchString(out.String()) {
				scrolled++
			}
		}
		if image := widthx.IsImageLine(pinned); image && scrolled > 0 || !image && scrolled == 0 {
			t.Fatalf("with %q pinned in the dock, %d of 10 frames scrolled", pinned, scrolled)
		}
	}
}

// A transcript of styled lines that gains a line per frame in a 160x48 screen: with the rows that moved scrolled, a frame
// writes the new row instead of every row of the viewport.
func BenchmarkAltScreenFrameWhileTheTranscriptGrows(b *testing.B) {
	line := func(i int) string {
		return fmt.Sprintf("\x1b[3%dmline %d\x1b[0m of the transcript, with \x1b[1mbold\x1b[0m words and a \x1b]8;;http://x\x07link\x1b]8;;\x07", i%7, i)
	}
	for _, mode := range []struct{ name, env string }{{"scroll-rows", ""}, {"draw-every-row", "0"}} {
		b.Run(mode.name, func(b *testing.B) {
			b.Setenv("PIG_TUI_SCROLL_ROWS", mode.env)
			lines := &scrollTestLines{}
			for i := range 200 {
				lines.lines = append(lines.lines, line(i))
			}
			var out strings.Builder
			ui := newScrollTestScreen(b, &out, 160, 48, lines)
			written := 0
			b.ReportAllocs()
			for i := 0; b.Loop(); i++ {
				lines.lines = append(lines.lines[1:], line(200+i))
				out.Reset()
				ui.Render()
				written += out.Len()
			}
			b.ReportMetric(float64(written)/float64(b.N), "bytes/frame")
		})
	}
}

// PIG_TUI_SCROLL_ROWS=0 draws every row that changed, as Pi does.
func TestAltScreenScrollRowsOff(t *testing.T) {
	t.Setenv("PIG_TUI_SCROLL_ROWS", "0")
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	var out strings.Builder
	text := &scrollTestLines{lines: lines}
	ui := newScrollTestScreen(t, &out, 40, 12, text)
	vt := newRowsVT(12)
	vt.apply(out.String())
	for step := range 10 {
		out.Reset()
		text.lines = append(text.lines, fmt.Sprintf("added %d", step))
		ui.Render()
		frame := out.String()
		if scrollRegionRe.MatchString(frame) || strings.Count(frame, "\x1b[2K") != 12 {
			t.Fatalf("step %d: with scrolling off the frame scrolls or does not draw every row: %q", step, frame)
		}
		vt.apply(frame)
		if want := shownRows(ui.GetScreenLines(), 12); !slices.Equal(vt.rows, want) {
			t.Fatalf("step %d: the terminal shows\n%q\nwant\n%q", step, vt.rows, want)
		}
	}
}
