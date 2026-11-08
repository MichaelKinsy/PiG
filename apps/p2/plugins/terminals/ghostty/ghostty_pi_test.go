package ghostty_test

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/terminals/ghostty"
)

// These are Pi's test/p2/ghostty.test.ts, case for case, with its inputs and expectations (the titles are Pi's, after
// "ghostty: "). The corpus test in ghostty_test.go is the differential against Pi's ghostty.ts.

// piText is Pi's `text(frame)`: the changed rows by y, their cells joined with " " for empty ones, trimmed on the right.
func piText(frame ghostty.Frame) []string {
	rows := []string{}
	for _, row := range frame.Changed {
		for len(rows) <= row.Y {
			rows = append(rows, "")
		}
		var line strings.Builder
		for _, cell := range row.Cells {
			if cell.Text == "" {
				line.WriteString(" ")
			} else {
				line.WriteString(cell.Text)
			}
		}
		rows[row.Y] = strings.TrimRight(line.String(), " ")
	}
	return rows
}

func piTerminal(t *testing.T, g *ghostty.Ghostty, cols, rows int, onReply func([]byte)) *ghostty.VtTerminal {
	t.Helper()
	return terminal(t, g, ghostty.Options{Cols: cols, Rows: rows, Scrollback: 1000, OnReply: onReply})
}

func equal(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %#v, want %#v", what, got, want)
	}
}

// The web view joins these rows when copying, so a long command copies as one line.
func TestGhosttyMarksSoftWrappedRows(t *testing.T) {
	vt := piTerminal(t, load(t), 10, 4, nil)
	vt.Write([]byte("0123456789abcdef\r\nshort"))
	frame := vt.Frame(true)
	equal(t, piText(frame)[:3], []string{"0123456789", "abcdef", "short"}, "text")
	wrapped := []bool{}
	for _, row := range frame.Changed {
		wrapped = append(wrapped, row.Wrapped)
	}
	equal(t, wrapped, []bool{true, false, false, false}, "wrapped")
}

func TestGhosttyRendersTextStylesColorsAndTheCursor(t *testing.T) {
	vt := piTerminal(t, load(t), 20, 5, nil)
	vt.Write([]byte("hello\r\n\x1b[1;31mred\x1b[0m \x1b[7mrev\x1b[0m\r\n\x1b[38;2;1;2;3m日本"))
	frame := vt.Frame(false)
	equal(t, frame.Dirty, "full", "dirty")
	equal(t, piText(frame)[:3], []string{"hello", "red rev", "日 本"}, "text")
	red := frame.Changed[1].Cells[0]
	if red.Flags&ghostty.BOLD != ghostty.BOLD {
		t.Fatalf("red flags = %d", red.Flags)
	}
	if red.Fg == -1 {
		t.Fatal("red has the default foreground")
	}
	if frame.Changed[1].Cells[4].Flags&ghostty.INVERSE != ghostty.INVERSE {
		t.Fatalf("rev flags = %d", frame.Changed[1].Cells[4].Flags)
	}
	equal(t, frame.Changed[2].Cells[0].Fg, 0x010203, "fg")
	widths := []int{}
	for _, cell := range frame.Changed[2].Cells[:4] {
		widths = append(widths, cell.Width)
	}
	equal(t, widths, []int{2, 0, 2, 0}, "widths")
	if frame.Cursor == nil {
		t.Fatal("no cursor")
	}
	equal(t, [2]int{frame.Cursor.X, frame.Cursor.Y}, [2]int{4, 2}, "cursor")
	// Only what changed afterwards.
	equal(t, vt.Frame(false).Dirty, "none", "dirty after")
	vt.Write([]byte("\x1b[1;1Hj"))
	next := vt.Frame(false)
	if next.Dirty == "none" {
		t.Fatal("nothing changed")
	}
	// The edited row, plus the row the cursor left.
	ys := []int{}
	for _, row := range next.Changed {
		ys = append(ys, row.Y)
	}
	equal(t, ys, []int{0, 2}, "changed rows")
}

func TestGhosttyASnapshotReproducesScreenScrollbackAndModes(t *testing.T) {
	g := load(t)
	source := piTerminal(t, g, 20, 3, nil)
	source.Write([]byte("one\r\ntwo\r\nthree\r\nfour\r\n\x1b[?2004h\x1b[?1h\x1b[1mbold"))
	copied := piTerminal(t, g, 20, 3, nil)
	copied.Write(source.Snapshot())
	equal(t, piText(copied.Frame(true)), piText(source.Frame(true)), "text")
	copyTotal, _, _ := copied.Scrollbar()
	sourceTotal, _, _ := source.Scrollbar()
	equal(t, copyTotal, sourceTotal, "scrollback total")
	equal(t, copied.Mode(2004, false), true, "mode 2004")
	equal(t, copied.Mode(1, false), true, "mode 1")
	copied.Write([]byte("er"))
	equal(t, copied.Frame(true).Changed[2].Cells[5].Flags, ghostty.BOLD, "flags")
}

func TestGhosttyASnapshotOfTheAlternateScreenKeepsItActive(t *testing.T) {
	g := load(t)
	source := piTerminal(t, g, 20, 3, nil)
	source.Write([]byte("shell\r\n\x1b[?1049h\x1b[2J\x1b[Hfull screen"))
	copied := piTerminal(t, g, 20, 3, nil)
	copied.Write(source.Snapshot())
	equal(t, copied.AlternateScreen(), true, "alternate screen")
	equal(t, piText(copied.Frame(true)), piText(source.Frame(true)), "text")
}

func TestGhosttyAnswersQueriesThroughOnReply(t *testing.T) {
	replies := []string{}
	vt := piTerminal(t, load(t), 20, 5, func(b []byte) { replies = append(replies, string(b)) })
	vt.Write([]byte("ab\x1b[6n\x1b[c"))
	if len(replies) < 2 {
		t.Fatalf("replies = %q", replies)
	}
	equal(t, replies[0], "\x1b[1;3R", "cursor position")
	// Primary device attributes: CSI ? Ps ; ... c
	if !regexp.MustCompile(`^\[\?\d+(;\d+)*c$`).MatchString(replies[1][1:]) || replies[1][0] != '\x1b' {
		t.Fatalf("device attributes = %q", replies[1])
	}
}

func TestGhosttyEncodesKeysAndPastesInTheTerminalsModes(t *testing.T) {
	vt := piTerminal(t, load(t), 20, 5, nil)
	key := func(code, key string, ctrl, alt, shift bool) string {
		b, _ := vt.EncodeKey(ghostty.KeyInput{Code: code, Key: key, Ctrl: ctrl, Alt: alt, Shift: shift})
		return string(b)
	}
	equal(t, key("Enter", "Enter", false, false, false), "\r", "Enter")
	equal(t, key("Backspace", "Backspace", false, false, false), "\x7f", "Backspace")
	equal(t, key("ArrowUp", "ArrowUp", false, false, false), "\x1b[A", "ArrowUp")
	equal(t, key("KeyC", "c", true, false, false), "\x03", "ctrl-c")
	equal(t, key("KeyA", "a", false, false, false), "a", "a")
	equal(t, key("KeyA", "A", false, false, true), "A", "A")
	equal(t, key("KeyB", "b", false, true, false), "\x1bb", "alt-b")
	vt.Write([]byte("\x1b[?1h"))
	equal(t, key("ArrowUp", "ArrowUp", false, false, false), "\x1bOA", "ArrowUp, application cursor keys")
	equal(t, string(vt.EncodePaste("a\nb")), "a\rb", "paste")
	vt.Write([]byte("\x1b[?2004h"))
	equal(t, string(vt.EncodePaste("x\x1by")), "\x1b[200~x y\x1b[201~", "bracketed paste")
}

func TestGhosttyScrollsTheViewportThroughScrollback(t *testing.T) {
	vt := piTerminal(t, load(t), 10, 2, nil)
	vt.Write([]byte("1\r\n2\r\n3\r\n4"))
	equal(t, piText(vt.Frame(true)), []string{"3", "4"}, "text")
	vt.Scroll(-2)
	equal(t, piText(vt.Frame(true)), []string{"1", "2"}, "scrolled")
	_, offset, _ := vt.Scrollbar()
	equal(t, offset, 0, "offset")
	vt.ScrollBottom()
	equal(t, piText(vt.Frame(true)), []string{"3", "4"}, "bottom")
}

func TestGhosttyAnswersLightDarkQueriesAndColorQueriesWithTheConfiguredColors(t *testing.T) {
	replies := []string{}
	scheme := "light"
	vt := terminal(t, load(t), ghostty.Options{
		Cols: 20, Rows: 5, Scrollback: 0,
		Colors:      &ghostty.Colors{Foreground: 0x1d1b19, Background: 0xfbfaf8},
		OnReply:     func(b []byte) { replies = append(replies, string(b)) },
		ColorScheme: func() string { return scheme },
	})
	vt.Write([]byte("\x1b[?996n\x1b]11;?\x07"))
	equal(t, replies, []string{"\x1b[?997;2n", "\x1b]11;rgb:fbfb/fafa/f8f8\x07"}, "light replies")
	scheme = "dark"
	vt.SetColors(ghostty.Colors{Foreground: 0xe6e6e6, Background: 0x141414})
	vt.Write([]byte("\x1b[?996n\x1b]10;?\x07"))
	equal(t, replies[2:], []string{"\x1b[?997;1n", "\x1b]10;rgb:e6e6/e6e6/e6e6\x07"}, "dark replies")
}

func TestGhosttyEncodesMouseEventsInTheProgramsMouseMode(t *testing.T) {
	vt := piTerminal(t, load(t), 20, 5, nil)
	mouse := func(action, button string) *string {
		b, ok := vt.EncodeMouse(ghostty.MouseInput{Action: action, Button: button, X: 20, Y: 20, Width: 160, Height: 80, CellWidth: 8, CellHeight: 16})
		if !ok || b == nil {
			return nil
		}
		s := string(b)
		return &s
	}
	want := func(got *string, want string, what string) {
		t.Helper()
		if got == nil || *got != want {
			t.Fatalf("%s = %v, want %q", what, got, want)
		}
	}
	equal(t, vt.MouseTracking(), false, "tracking")
	if got := mouse("press", "wheelUp"); got != nil {
		t.Fatalf("wheel without tracking = %q", *got)
	}
	vt.Write([]byte("\x1b[?1000h\x1b[?1006h"))
	equal(t, vt.MouseTracking(), true, "tracking on")
	// SGR: wheel up 64, down 65, left button 0 (M press, m release), at column 3 (20 / 8, 1-based) and row 2.
	want(mouse("press", "wheelUp"), "\x1b[<64;3;2M", "wheel up")
	want(mouse("press", "wheelDown"), "\x1b[<65;3;2M", "wheel down")
	want(mouse("press", "left"), "\x1b[<0;3;2M", "left press")
	want(mouse("release", "left"), "\x1b[<0;3;2m", "left release")
	// Normal tracking (1000) reports no motion; button-event tracking (1002) reports drags.
	if got := mouse("motion", "left"); got != nil {
		t.Fatalf("motion under 1000 = %q", *got)
	}
	vt.Write([]byte("\x1b[?1002h"))
	want(mouse("motion", "left"), "\x1b[<32;3;2M", "drag")
}
