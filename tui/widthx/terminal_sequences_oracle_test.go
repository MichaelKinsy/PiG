package widthx

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/tui/src/utils.ts

// stripTerminalSequences, normalizeTerminalOutput, getActiveBackgroundAnsi, extractAnsiCode at every ESC, getGraphemeCellRange and getOsc8LinkAtColumn at every column
// over lines mixing text (ASCII, tab, CJK, combining marks, ZWJ emoji, Thai/Lao AM vowels) with complete, truncated, nested and unterminated CSI, OSC 8, other OSC and APC sequences.
func TestTerminalSequenceHelpersMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(311))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	text := func() string {
		return pick("abc", "x", " ", "\t", "日本", "e\u0301", "👨‍👩‍👧", "🙂", "\u0e33", "\u0eb3", "ก\u0e33", "\u200b", "a\u00a0b", "[", "]", "\\", ";", "\u0085", "é", "Ａ", "🇯🇵", "\u2028")
	}
	sgr := func() string {
		return "\x1b[" + pick("0", "1", "4", "7", "22", "39", "49", "41", "47", "100", "107", "48;5;12", "48;5;300", "48;2;1;2;3", "48;2;1;2", "38;2;1;2;3", "1;48;5;9", "", "0;41", "49;1", "48", "48;5", "041", "4:3", "1:41", "48:5:7") + "m"
	}
	seq := func() string {
		switch rng.Intn(10) {
		case 0, 1, 2:
			return sgr()
		case 3:
			return pick("\x1b[K", "\x1b[2K", "\x1b[H", "\x1b[1;1H", "\x1b[J", "\x1b[3G", "\x1b[?25l", "\x1b[2~", "\x1b[1;2A")
		case 4:
			return "\x1b]8;" + pick("", "id=1", "a;b") + ";" + pick("", "http://a/b", "http://c?d=e;f", "x y") + pick("\x07", "\x1b\\")
		case 5:
			return "\x1b]" + pick("0;title", "133;A", "7;file:///x", "52;c;Zm9v") + pick("\x07", "\x1b\\")
		case 6:
			return "\x1b_" + pick("Ga=T;AAAA", "pi:c", "") + pick("\x07", "\x1b\\")
		case 7:
			return pick("\x1b", "\x1b[", "\x1b]", "\x1b]8;;http://u", "\x1b_x", "\x1b[1;2", "\x1b[999999999m", "\x1b\\", "\x1bO", "\x1b[31")
		case 8:
			return pick("\x1b[0m", "\x1b[49m", "\x1b]8;;\x07", "\x1b]8;;\x1b\\")
		default:
			return pick("\x1b[41m", "\x1b[48;5;12m", "\x1b[48;2;5;6;7m")
		}
	}
	var inputs []string
	for range 4000 {
		var b strings.Builder
		for i, n := 0, 1+rng.Intn(8); i < n; i++ {
			if rng.Intn(5) < 2 {
				b.WriteString(text())
			} else {
				b.WriteString(seq())
			}
		}
		inputs = append(inputs, b.String())
	}
	type result struct {
		Strip string    `json:"strip"`
		Norm  string    `json:"norm"`
		Bg    string    `json:"bg"`
		Esc   []*string `json:"esc"`
		Cells []*[2]int `json:"cells"`
		Links []*string `json:"links"`
		Width int       `json:"width"`
	}
	var want []result
	pioracle.Run(t, `
const u = await load("pi-tui/utils.js");
emit(input.map((line) => {
	const esc = [];
	for (let i = 0; i < line.length; i++) {
		if (line[i] === "\x1b") esc.push(u.extractAnsiCode(line, i)?.code ?? null);
	}
	const width = u.visibleWidth(line);
	const cells = [];
	const links = [];
	for (let column = 0; column <= width + 1; column++) {
		const range = u.getGraphemeCellRange(line, column);
		cells.push(range ? [range.start, range.end] : null);
		links.push(u.getOsc8LinkAtColumn(line, column) ?? null);
	}
	return { strip: u.stripTerminalSequences(line), norm: u.normalizeTerminalOutput(line), bg: u.getActiveBackgroundAnsi(line), esc, cells, links, width };
}));`, inputs, &want)
	failures, escapes, linked := 0, 0, 0
	for i, line := range inputs {
		w := want[i]
		esc := []*string{}
		for pos := 0; pos < len(line); pos++ {
			if line[pos] != 0x1b {
				continue
			}
			code, length := ExtractAnsiCode(line, pos)
			if length == 0 {
				esc = append(esc, nil)
				continue
			}
			escapes++
			esc = append(esc, &code)
		}
		var cells []*[2]int
		var links []*string
		for column := 0; column <= w.Width+1; column++ {
			if start, end, ok := GraphemeCellRange(line, column); ok {
				cells = append(cells, &[2]int{start, end})
			} else {
				cells = append(cells, nil)
			}
			// ok is compared, not only the URL: Pi returns undefined for an empty URL (hyperlink[1] || undefined), so ("", true) must differ from it.
			if url, ok := GetOsc8LinkAtColumn(line, column); ok {
				linked++
				links = append(links, &url)
			} else {
				links = append(links, nil)
			}
		}
		got := result{Strip: StripTerminalSequences(line), Norm: NormalizeTerminalOutput(line), Bg: GetActiveBackgroundAnsi(line), Esc: esc, Cells: cells, Links: links, Width: VisibleWidth(line)}
		if !reflect.DeepEqual(got, w) {
			if failures++; failures <= 8 {
				t.Errorf("%q:\n  PiG %s\n  Pi  %s", line, dump(got.Strip, got.Norm, got.Bg, got.Esc, got.Cells, got.Links, got.Width), dump(w.Strip, w.Norm, w.Bg, w.Esc, w.Cells, w.Links, w.Width))
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d lines differ", failures, len(inputs))
	}
	if escapes < 3000 || linked < 500 {
		t.Fatalf("corpus too thin: %d escapes, %d linked columns", escapes, linked)
	}
}

func dump(strip, norm, bg string, esc []*string, cells []*[2]int, links []*string, width int) string {
	var b strings.Builder
	b.WriteString(strings.Join([]string{strip, norm, bg}, "|"))
	for _, e := range esc {
		if e == nil {
			b.WriteString(" esc=nil")
		} else {
			b.WriteString(" esc=" + *e)
		}
	}
	for _, c := range cells {
		if c == nil {
			b.WriteString(" c=nil")
		} else {
			b.WriteString(" c=" + string(rune('0'+c[0]%10)) + string(rune('0'+c[1]%10)))
		}
	}
	for _, l := range links {
		if l == nil {
			b.WriteString(" l=nil")
		} else {
			b.WriteString(" l=" + *l)
		}
	}
	return b.String() + " w=" + string(rune('0'+width%10))
}
