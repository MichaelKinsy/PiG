package widthx

import (
	"strconv"
	"strings"
	"testing"
)

// splitProcessSGR is Process's SGR walk as it was written over strings.Split(params, ";"), kept as the oracle that the
// allocation-free walk must match.
func splitProcessSGR(t *AnsiCodeTracker, code string) {
	if strings.HasPrefix(code, "\x1b]8;") || !strings.HasSuffix(code, "m") {
		t.Process(code)
		return
	}
	params, ok := firstSGRParams(code)
	if !ok {
		return
	}
	if params == "" || params == "0" {
		t.resetSGR()
		return
	}
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); {
		c, err := strconv.Atoi(parts[i])
		if err != nil {
			i++
			continue
		}
		if (c == 38 || c == 48) && i+1 < len(parts) {
			if parts[i+1] == "5" && i+2 < len(parts) {
				colorCode := parts[i] + ";" + parts[i+1] + ";" + parts[i+2]
				if c == 38 {
					t.fgColor = colorCode
				} else {
					t.bgColor = colorCode
				}
				i += 3
				continue
			}
			if parts[i+1] == "2" && i+4 < len(parts) {
				colorCode := parts[i] + ";" + parts[i+1] + ";" + parts[i+2] + ";" + parts[i+3] + ";" + parts[i+4]
				if c == 38 {
					t.fgColor = colorCode
				} else {
					t.bgColor = colorCode
				}
				i += 5
				continue
			}
		}
		switch {
		case c == 0:
			t.resetSGR()
		case c == 1:
			t.bold = true
		case c == 2:
			t.dim = true
		case c == 3:
			t.italic = true
		case c == 4:
			t.underline = true
		case c == 5:
			t.blink = true
		case c == 7:
			t.inverse = true
		case c == 8:
			t.hidden = true
		case c == 9:
			t.strike = true
		case c == 21:
			t.bold = false
		case c == 22:
			t.bold = false
			t.dim = false
		case c == 23:
			t.italic = false
		case c == 24:
			t.underline = false
		case c == 25:
			t.blink = false
		case c == 27:
			t.inverse = false
		case c == 28:
			t.hidden = false
		case c == 29:
			t.strike = false
		case c == 39:
			t.fgColor = ""
		case c == 49:
			t.bgColor = ""
		case (c >= 30 && c <= 37) || (c >= 90 && c <= 97):
			t.fgColor = strconv.Itoa(c)
		case (c >= 40 && c <= 47) || (c >= 100 && c <= 107):
			t.bgColor = strconv.Itoa(c)
		}
		i++
	}
}

// trackerStarts are the states a code is applied to: empty, and every attribute and both colors set.
func trackerStarts() []AnsiCodeTracker {
	full := AnsiCodeTracker{bold: true, dim: true, italic: true, underline: true, blink: true, inverse: true, hidden: true, strike: true,
		fgColor: "38;2;1;2;3", bgColor: "48;5;17", activeHyperlinkOpen: "\x1b]8;;https://e.com\x07", activeHyperlinkClose: "\x1b]8;;\x07"}
	return []AnsiCodeTracker{{}, full}
}

func assertProcessMatchesSplit(t *testing.T, code string) {
	t.Helper()
	for _, start := range trackerStarts() {
		got, want := start, start
		got.Process(code)
		splitProcessSGR(&want, code)
		if got != want {
			t.Fatalf("Process(%q) from %+v:\n got %+v\nwant %+v", code, start, got, want)
		}
		if got.ActiveCodes() != want.ActiveCodes() || got.LineEndReset() != want.LineEndReset() {
			t.Fatalf("Process(%q): ActiveCodes %q / %q, LineEndReset %q / %q", code, got.ActiveCodes(), want.ActiveCodes(), got.LineEndReset(), want.LineEndReset())
		}
	}
}

// The SGR walk without strings.Split leaves the tracker exactly as the split walk did, for the edge cases by name.
func TestAnsiCodeTrackerProcessMatchesSplitWalk(t *testing.T) {
	for _, params := range []string{
		"", "0", "00", ";", ";;", "0;", ";0", "1;", ";1", "1;;3", ";;;;", "1;2;3;4;5;7;8;9", "21;22;23;24;25;27;28;29",
		"38;5;196", "48;5;17", "38;5", "38;5;", "38;5;;1", "48;5;;", "38;2;255;128;0", "48;2;1;2;3", "38;2;1;2", "38;2;1;2;",
		"38;2;;;", "38;2;1;2;3;4", "38;2;1;2;3;1", "38;9;1", "38", "48", "38;", "48;", "38;05;1", "38;02;1;2;3", "038;5;1",
		"0038;2;1;2;3", "30", "37", "38;5;1;39", "90;97", "40;47;100;107", "108", "98", "39;49", "1;0", "0;1", "1;00;3",
		"99999999999999999999", "99999999999999999999;1", "1;99999999999999999999;3", "38;99999999999999999999;1",
		"1;2;3;4;5;7;8;9;21;22;23;24;25;27;28;29;30;40;1", "38;2;1;2;3;48;2;4;5;6;1;4;9;38;5;7;48;5;8;0;1",
		"38;5;1;38;5;2;38;5;3;38;5;4;38;5;5;38;5;6", "4;5;6;7;8;9;10;11;12;13;14;15;16;17;18;19;20;21",
	} {
		for _, code := range []string{"\x1b[" + params + "m", "x\x1b[" + params + "m", "\x1b[" + params + ":1m", "\x1b[" + params + "m\x1b[1m"} {
			assertProcessMatchesSplit(t, code)
		}
	}
	for _, code := range []string{
		"\x1b[38:2:1:2:3m", "\x1b[4:3m", "\x1b[1;4:3m", "\x1b[?25m", "\x1b[ m", "\x1b[1", "\x1b]8;;https://e.com\x07",
		"\x1b]8;;\x1b\\", "\x1b[1K", "m", "\x1bm", "\x1b[m",
	} {
		assertProcessMatchesSplit(t, code)
	}
}

// Every parameter list of up to five parameters over the values that steer the walk matches the split walk.
func TestAnsiCodeTrackerProcessMatchesSplitWalkExhaustively(t *testing.T) {
	values := []string{"", "0", "1", "2", "5", "22", "38", "48", "39", "49", "31", "41", "91", "101", "007", "99999999999999999999"}
	var walk func(params string, depth int)
	walk = func(params string, depth int) {
		assertProcessMatchesSplit(t, "\x1b["+params+"m")
		if depth == 5 {
			return
		}
		for _, value := range values {
			if depth == 0 {
				walk(value, 1)
			} else {
				walk(params+";"+value, depth+1)
			}
		}
	}
	walk("", 0)
}

// Fuzzed codes match the split walk.
func FuzzAnsiCodeTrackerProcessMatchesSplitWalk(f *testing.F) {
	for _, seed := range []string{"\x1b[38;2;1;2;3m", "\x1b[;;m", "\x1b[48;5;m", "\x1b[0;1;38;5;9;48;2;1;2;3;4m", "\x1b[38:5:1m"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, code string) { assertProcessMatchesSplit(t, code) })
}

// An SGR code with up to sgrStackParams parameters is processed without allocating, so wrapping styled text allocates
// only for the lines it builds.
func TestAnsiCodeTrackerProcessDoesNotAllocate(t *testing.T) {
	var tracker AnsiCodeTracker
	for _, code := range []string{"\x1b[38;2;24;48;44m", "\x1b[48;2;1;2;3;38;2;4;5;6m", "\x1b[1;3;4;38;5;196m", "\x1b[22;39m", "\x1b[0m", "\x1b[1;2;3;4;5;7;8;9;21;22;23;24;25;27;28;29m", "\x1b[1;;3m", "\x1b[;4m"} {
		if allocs := testing.AllocsPerRun(100, func() { tracker.Process(code) }); allocs != 0 {
			t.Errorf("Process(%q) allocates %.0f times per call", code, allocs)
		}
	}
}

// BenchmarkAnsiCodeTrackerProcess processes the codes of one half-block row of the pig head: a truecolor foreground and
// background per cell, then a reset.
func BenchmarkAnsiCodeTrackerProcess(b *testing.B) {
	codes := []string{"\x1b[38;2;24;48;44m", "\x1b[48;2;232;244;240m", "\x1b[39;49m", "\x1b[38;2;91;141;239m", "\x1b[0m", "\x1b[1;38;5;196m"}
	var tracker AnsiCodeTracker
	b.ReportAllocs()
	for b.Loop() {
		for _, code := range codes {
			tracker.Process(code)
		}
	}
}
