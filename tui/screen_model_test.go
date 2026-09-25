package tui

import (
	"strconv"
	"strings"

	"github.com/rivo/uniseg"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// screen is a minimal terminal model for the escape sequences the differential
// renderer emits. Byte-level assertions cannot catch a stale row: the renderer
// can emit perfectly reasonable output and still leave a duplicate on screen
// because it wrote to the wrong row. Replaying onto a screen answers the only
// question that matters, which is what the user is looking at.
//
// Supported: CR, LF, CUU/CUD (CSI A/B), EL (CSI 2K), ED (CSI 2J, CSI 3J),
// CUP home (CSI H), and synchronized-output toggles, which are no-ops here.
// SGR and other CSI sequences are consumed without effect. Anything the
// renderer starts emitting that is not modelled will surface as a decode gap
// rather than a silent wrong answer.
type screen struct {
	rows      []string
	cursorRow int
	cursorCol int
	height    int
	width     int
	unhandled []string
}

func newScreen(width, height int) *screen {
	s := &screen{height: height, width: width}
	s.rows = make([]string, height)
	return s
}

// advanceRow moves the cursor down one row, scrolling when it is already on the
// last row, which is what a terminal does when text runs past the bottom.
func (s *screen) advanceRow() {
	if s.cursorRow == s.height-1 {
		s.scrollUp()
		return
	}
	s.cursorRow++
}

// Visible returns the current on-screen rows, top to bottom.
func (s *screen) Visible() []string { return append([]string(nil), s.rows...) }

func (s *screen) scrollUp() {
	copy(s.rows, s.rows[1:])
	s.rows[len(s.rows)-1] = ""
}

func (s *screen) writeText(text string) {
	if text == "" {
		return
	}
	row := s.rows[s.cursorRow]
	// Pad to the cursor column so a write past the end of a short row lands in
	// the right place instead of being appended.
	for len([]rune(row)) < s.cursorCol {
		row += " "
	}
	runes := []rune(row)
	// Cell widths come from uniseg rather than from widthx, so this model is an
	// independent oracle. Measuring with the same function under test would make
	// it inherit any width bug and report agreement with itself.
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		cluster := g.Str()
		// Auto-wrap. A logical row wider than the terminal occupies more than
		// one physical row, which is the whole reason a width miscount turns
		// into a stale-row bug: every position below it shifts.
		if s.width > 0 && s.cursorCol >= s.width {
			s.rows[s.cursorRow] = string(runes)
			s.advanceRow()
			s.cursorCol = 0
			runes = []rune(s.rows[s.cursorRow])
		}
		for _, r := range cluster {
			if s.cursorCol < len(runes) {
				runes[s.cursorCol] = r
			} else {
				runes = append(runes, r)
			}
		}
		s.cursorCol += max(1, g.Width())
	}
	s.rows[s.cursorRow] = string(runes)
}

// Write replays renderer output onto the screen.
func (s *screen) Write(p []byte) (int, error) {
	data := string(p)
	for i := 0; i < len(data); {
		c := data[i]
		switch {
		case c == '\r':
			s.cursorCol = 0
			i++
		case c == '\n':
			s.advanceRow()
			i++
		case c == 0x1b && i+1 < len(data) && data[i+1] == '[':
			i += s.applyCSI(data[i:])
		case c == 0x1b && i+1 < len(data) && data[i+1] == ']':
			// OSC, used here for hyperlinks. It carries no visible cells, but it
			// must be consumed to its terminator or the payload would be painted
			// onto the screen as text.
			i += oscLength(data[i:])
		case c == 0x1b && i+1 < len(data) && (data[i+1] == '7' || data[i+1] == '8'):
			s.unhandled = append(s.unhandled, "DECSC/DECRC")
			i += 2
		case c == 0x1b:
			s.unhandled = append(s.unhandled, "bare ESC")
			i++
		default:
			j := i
			for j < len(data) && data[j] != '\r' && data[j] != '\n' && data[j] != 0x1b {
				j++
			}
			s.writeText(data[i:j])
			i = j
		}
	}
	return len(p), nil
}

// applyCSI consumes one CSI sequence starting at data[0] and returns its length.
func (s *screen) applyCSI(data string) int {
	end := 2
	for end < len(data) && !isCSIFinal(data[end]) {
		end++
	}
	if end >= len(data) {
		return len(data)
	}
	params := data[2:end]
	final := data[end]
	n := end + 1

	private := strings.HasPrefix(params, "?")
	arg := func(def int) int {
		v, err := strconv.Atoi(strings.TrimPrefix(params, "?"))
		if err != nil {
			return def
		}
		return v
	}

	switch final {
	case 'A':
		s.cursorRow = max(0, s.cursorRow-arg(1))
	case 'B':
		s.cursorRow = min(s.height-1, s.cursorRow+arg(1))
	case 'C':
		s.cursorCol += arg(1)
	case 'D':
		s.cursorCol = max(0, s.cursorCol-arg(1))
	case 'H':
		s.cursorRow, s.cursorCol = 0, 0
	case 'K':
		switch arg(0) {
		case 2:
			s.rows[s.cursorRow] = ""
		case 0:
			runes := []rune(s.rows[s.cursorRow])
			if s.cursorCol < len(runes) {
				s.rows[s.cursorRow] = string(runes[:s.cursorCol])
			}
		}
	case 'J':
		switch arg(0) {
		case 2:
			for i := range s.rows {
				s.rows[i] = ""
			}
		case 3:
			// Clears scrollback, which this model does not retain.
		}
	case 'h', 'l':
		if !private {
			s.unhandled = append(s.unhandled, "SM/RM "+params)
		}
	case 'm':
		// SGR: colour only, no layout effect.
	default:
		s.unhandled = append(s.unhandled, string(final)+" ("+params+")")
	}
	return n
}

func isCSIFinal(b byte) bool { return b >= 0x40 && b <= 0x7e }

// oscLength returns the length of the OSC sequence at the start of data,
// terminated by BEL or ST.
func oscLength(data string) int {
	for i := 2; i < len(data); i++ {
		if data[i] == 0x07 {
			return i + 1
		}
		if data[i] == 0x1b && i+1 < len(data) && data[i+1] == '\\' {
			return i + 2
		}
	}
	return len(data)
}

// countRowsContaining reports how many visible rows contain the needle, after
// stripping SGR so a colour change cannot hide a match.
func (s *screen) countRowsContaining(needle string) int {
	n := 0
	for _, row := range s.rows {
		if strings.Contains(widthx.StripAnsi(row), needle) {
			n++
		}
	}
	return n
}
