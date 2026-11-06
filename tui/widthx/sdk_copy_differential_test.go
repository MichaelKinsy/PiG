package widthx_test

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	sdkwidthx "github.com/MichaelKinsy/PiG/extensions/sdk/tui/widthx"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// The application links extensions/sdk/tui/widthx, a generated copy of this package (tui/sdk_tui_copy_test.go proves the text equal),
// and this package is the tested source. This test closes the loop on behavior: the copy and the source must agree on every code point
// and on a corpus of terminal strings, for every exported function of the width engine. A table, a branch or an import rewrite that
// made the copy diverge from the oracle-tested source fails here.

type widthEngine struct {
	visibleWidth     func(string) int
	graphemeWidth    func(string) int
	eastAsianWidth   func(rune) int
	isJSSpace        func(rune) bool
	firstGrapheme    func(string) (string, string)
	stripAnsi        func(string) string
	stripTerminal    func(string) string
	extractAnsiCode  func(string, int) (string, int)
	truncate         func(string, int, string, bool) string
	wrap             func(string, int) []string
	sliceByColumn    func(string, int, int, bool) string
	sliceWithWidth   func(string, int, int, bool) (string, int)
	extractSegments  func(string, int, int, int, bool) [4]string
	osc8AtColumn     func(string, int) (string, bool)
	activeBackground func(string) string
	graphemeRange    func(string, int) [3]int
	applyLineReset   func(string) string
	isImageLine      func(string) bool
	isCJKBreak       func(string) bool
	jsTrim           func(string) string
	normalize        func(string) string
	replaceControl   func(string) string
	renderFakeCursor func(string) string
	resolveFake      func(string, bool) string
	cursorPosition   func([]string, int) [3]int
	frameAt          func([]string, int, int) []string
	tracker          func(string) [5]string
}

var sourceEngine = widthEngine{
	visibleWidth:    widthx.VisibleWidth,
	graphemeWidth:   widthx.GraphemeWidth,
	eastAsianWidth:  widthx.EastAsianWidth,
	isJSSpace:       widthx.IsJSSpace,
	firstGrapheme:   widthx.FirstGrapheme,
	stripAnsi:       widthx.StripAnsi,
	stripTerminal:   widthx.StripTerminalSequences,
	extractAnsiCode: widthx.ExtractAnsiCode,
	truncate:        widthx.TruncateToWidth,
	wrap:            widthx.WrapTextWithAnsi,
	sliceByColumn:   widthx.SliceByColumn,
	sliceWithWidth: func(line string, start, length int, strict bool) (string, int) {
		r := widthx.SliceWithWidth(line, start, length, strict)
		return r.Text, r.Width
	},
	extractSegments: func(line string, beforeEnd, afterStart, afterLen int, strict bool) [4]string {
		r := widthx.ExtractSegments(line, beforeEnd, afterStart, afterLen, strict)
		return [4]string{r.Before, fmt.Sprint(r.BeforeWidth), r.After, fmt.Sprint(r.AfterWidth)}
	},
	osc8AtColumn:     widthx.GetOsc8LinkAtColumn,
	activeBackground: widthx.GetActiveBackgroundAnsi,
	graphemeRange: func(line string, column int) [3]int {
		s, e, ok := widthx.GraphemeCellRange(line, column)
		return [3]int{s, e, b2i(ok)}
	},
	applyLineReset:   widthx.ApplyLineReset,
	isImageLine:      widthx.IsImageLine,
	isCJKBreak:       widthx.IsCJKBreak,
	jsTrim:           widthx.JSTrim,
	normalize:        widthx.NormalizeTerminalOutput,
	replaceControl:   widthx.ReplaceControlCharacters,
	renderFakeCursor: widthx.RenderFakeCursor,
	resolveFake:      widthx.ResolveFakeCursorLine,
	cursorPosition: func(lines []string, height int) [3]int {
		p, ok := widthx.ExtractCursorPosition(lines, height)
		return [3]int{p.Row, p.Col, b2i(ok)}
	},
	frameAt: widthx.FrameAt,
	tracker: func(text string) [5]string {
		var t widthx.AnsiCodeTracker
		t.UpdateFromText(text)
		return [5]string{t.ActiveCodes(), fmt.Sprint(t.HasActiveCodes()), t.LineEndReset(), t.ActiveBackgroundCode(), t.ActiveCodes()}
	},
}

var copyEngine = widthEngine{
	visibleWidth:    sdkwidthx.VisibleWidth,
	graphemeWidth:   sdkwidthx.GraphemeWidth,
	eastAsianWidth:  sdkwidthx.EastAsianWidth,
	isJSSpace:       sdkwidthx.IsJSSpace,
	firstGrapheme:   sdkwidthx.FirstGrapheme,
	stripAnsi:       sdkwidthx.StripAnsi,
	stripTerminal:   sdkwidthx.StripTerminalSequences,
	extractAnsiCode: sdkwidthx.ExtractAnsiCode,
	truncate:        sdkwidthx.TruncateToWidth,
	wrap:            sdkwidthx.WrapTextWithAnsi,
	sliceByColumn:   sdkwidthx.SliceByColumn,
	sliceWithWidth: func(line string, start, length int, strict bool) (string, int) {
		r := sdkwidthx.SliceWithWidth(line, start, length, strict)
		return r.Text, r.Width
	},
	extractSegments: func(line string, beforeEnd, afterStart, afterLen int, strict bool) [4]string {
		r := sdkwidthx.ExtractSegments(line, beforeEnd, afterStart, afterLen, strict)
		return [4]string{r.Before, fmt.Sprint(r.BeforeWidth), r.After, fmt.Sprint(r.AfterWidth)}
	},
	osc8AtColumn:     sdkwidthx.GetOsc8LinkAtColumn,
	activeBackground: sdkwidthx.GetActiveBackgroundAnsi,
	graphemeRange: func(line string, column int) [3]int {
		s, e, ok := sdkwidthx.GraphemeCellRange(line, column)
		return [3]int{s, e, b2i(ok)}
	},
	applyLineReset:   sdkwidthx.ApplyLineReset,
	isImageLine:      sdkwidthx.IsImageLine,
	isCJKBreak:       sdkwidthx.IsCJKBreak,
	jsTrim:           sdkwidthx.JSTrim,
	normalize:        sdkwidthx.NormalizeTerminalOutput,
	replaceControl:   sdkwidthx.ReplaceControlCharacters,
	renderFakeCursor: sdkwidthx.RenderFakeCursor,
	resolveFake:      sdkwidthx.ResolveFakeCursorLine,
	cursorPosition: func(lines []string, height int) [3]int {
		p, ok := sdkwidthx.ExtractCursorPosition(lines, height)
		return [3]int{p.Row, p.Col, b2i(ok)}
	},
	frameAt: sdkwidthx.FrameAt,
	tracker: func(text string) [5]string {
		var t sdkwidthx.AnsiCodeTracker
		t.UpdateFromText(text)
		return [5]string{t.ActiveCodes(), fmt.Sprint(t.HasActiveCodes()), t.LineEndReset(), t.ActiveBackgroundCode(), t.ActiveCodes()}
	},
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// sameOn reports the first function whose result differs between the two engines on s.
func sameOn(a, b *widthEngine, s string) string {
	check := func(name string, x, y any) string {
		if !reflect.DeepEqual(x, y) {
			return fmt.Sprintf("%s(%q): source %#v, copy %#v", name, s, x, y)
		}
		return ""
	}
	for _, c := range []string{
		check("visibleWidth", a.visibleWidth(s), b.visibleWidth(s)),
		check("graphemeWidth", a.graphemeWidth(s), b.graphemeWidth(s)),
		check("stripAnsi", a.stripAnsi(s), b.stripAnsi(s)),
		check("stripTerminal", a.stripTerminal(s), b.stripTerminal(s)),
		check("normalize", a.normalize(s), b.normalize(s)),
		check("replaceControl", a.replaceControl(s), b.replaceControl(s)),
		check("applyLineReset", a.applyLineReset(s), b.applyLineReset(s)),
		check("isImageLine", a.isImageLine(s), b.isImageLine(s)),
		check("isCJKBreak", a.isCJKBreak(s), b.isCJKBreak(s)),
		check("jsTrim", a.jsTrim(s), b.jsTrim(s)),
		check("activeBackground", a.activeBackground(s), b.activeBackground(s)),
		check("tracker", a.tracker(s), b.tracker(s)),
		check("renderFakeCursor", a.renderFakeCursor(s), b.renderFakeCursor(s)),
		check("resolveFake", a.resolveFake(s, false), b.resolveFake(s, false)),
		check("resolveFakeHW", a.resolveFake(s, true), b.resolveFake(s, true)),
		check("cursorPosition", a.cursorPosition([]string{"x", s}, 4), b.cursorPosition([]string{"x", s}, 4)),
		check("frameAt", a.frameAt([]string{s}, 6, 9), b.frameAt([]string{s}, 6, 9)),
	} {
		if c != "" {
			return c
		}
	}
	g1, r1 := a.firstGrapheme(s)
	g2, r2 := b.firstGrapheme(s)
	if g1 != g2 || r1 != r2 {
		return fmt.Sprintf("firstGrapheme(%q): source %q|%q, copy %q|%q", s, g1, r1, g2, r2)
	}
	for pos := 0; pos <= len(s) && pos < 8; pos++ {
		c1, n1 := a.extractAnsiCode(s, pos)
		c2, n2 := b.extractAnsiCode(s, pos)
		if c1 != c2 || n1 != n2 {
			return fmt.Sprintf("extractAnsiCode(%q,%d): source %q|%d, copy %q|%d", s, pos, c1, n1, c2, n2)
		}
	}
	for _, w := range []int{0, 1, 2, 3, 5, 8, 20} {
		for _, pad := range []bool{false, true} {
			for _, ell := range []string{"", "...", "…", "\x1b[0m…"} {
				if c := check("truncate", a.truncate(s, w, ell, pad), b.truncate(s, w, ell, pad)); c != "" {
					return c + fmt.Sprintf(" width=%d pad=%v ellipsis=%q", w, pad, ell)
				}
			}
		}
		if c := check("wrap", a.wrap(s, w), b.wrap(s, w)); c != "" {
			return c + fmt.Sprintf(" width=%d", w)
		}
	}
	for _, start := range []int{0, 1, 2, 4} {
		for _, length := range []int{0, 1, 2, 3, 6} {
			for _, strict := range []bool{false, true} {
				if c := check("sliceByColumn", a.sliceByColumn(s, start, length, strict), b.sliceByColumn(s, start, length, strict)); c != "" {
					return c + fmt.Sprintf(" start=%d length=%d strict=%v", start, length, strict)
				}
				t1, w1 := a.sliceWithWidth(s, start, length, strict)
				t2, w2 := b.sliceWithWidth(s, start, length, strict)
				if t1 != t2 || w1 != w2 {
					return fmt.Sprintf("sliceWithWidth(%q,%d,%d,%v): source %q|%d, copy %q|%d", s, start, length, strict, t1, w1, t2, w2)
				}
				if c := check("extractSegments", a.extractSegments(s, start, start+length, 4, strict), b.extractSegments(s, start, start+length, 4, strict)); c != "" {
					return c + fmt.Sprintf(" start=%d length=%d strict=%v", start, length, strict)
				}
			}
		}
		u1, ok1 := a.osc8AtColumn(s, start)
		u2, ok2 := b.osc8AtColumn(s, start)
		if u1 != u2 || ok1 != ok2 {
			return fmt.Sprintf("osc8AtColumn(%q,%d): source %q|%v, copy %q|%v", s, start, u1, ok1, u2, ok2)
		}
		if c := check("graphemeRange", a.graphemeRange(s, start), b.graphemeRange(s, start)); c != "" {
			return c
		}
	}
	return ""
}

// widthDifferentialCorpus holds terminal strings the width engine meets: styled text, links, the cursor marker, emoji sequences,
// scripts with conjuncts, East Asian width, control bytes, truncated and malformed escapes.
var widthDifferentialCorpus = []string{
	"", "a", "hello world", "\t", "a\tb", "\x1b[31mred\x1b[0m", "日本語テキスト", "ｈａｌｆ ｶﾀｶﾅ",
	"👨\u200d👩\u200d👧\u200d👦", "🏳\ufe0f\u200d🌈", "🏴\u200d☠\ufe0f", "❤\ufe0f\u200d🔥", "❤\u200d🔥", "🇺🇸🇬🇧", "🇺", "1\ufe0f\u20e3", "#\u20e3", "👍🏽", "🫱🏻\u200d🫲🏿",
	"⚠", "⚠\ufe0f", "⚠\ufe0e", "☺", "☺\ufe0f", "©", "©\ufe0f", "™\ufe0f", "↔\ufe0f", "◽", "⬛", "⬜\ufe0f", "⭐", "⌚", "⏺", "⎿", "✓", "✔", "✗",
	"क\u094dष", "क\u094d\u200dष", "स\u094dत\u094dर", "ক\u09cdষ", "ન\u0acdન", "ำ", "กำ", "ຳ", "ລຳ", "é", "e\u0301", "Z\u0334\u0322\u031b\u0317a\u0337\u035a\u034el\u0336g\u0335o\u0337",
	"한국어", "각", "ᄀ가", "\u200b\u200c\u200d\ufeff", "\u2060", "\u00ad", "\u0000\u0007\u007f\u0085",
	"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\", "\x1b]8;;http://a\x07x\x1b]8;;\x07", "\x1b_pi:c\x07", "ab\x1b_pi:c\x07cd", "\x1b[3Aabc", "\x1b[2K", "\x1b[?25lhi\x1b[0m",
	"\x1b[?2026h", "\x1b", "\x1b[", "x\x1b]133;A\x07y", "𝕳𝖊𝖑𝖑𝖔", "𠀀𠀁", "\ue000", "\U0010fffd", "\uffff", "\u0378",
	"\u1734", "\u302e", "\u065f", "\u0f7f", "\u102b\u102c", "a\u0903", "\u0e31", "a\u20e3", "\U000e0067",
	"╭──────╮ │ ok │ ╰──────╯", "⠋ Working...", "↑12k ↓3.4k R1.2M $0.123 (sub) 45.2%/200k (auto)", "~/src/pig (main) • claude-opus",
	"| 名前 | 説明 |\n|---|---|", "→ ← ⏎ ⇧ ⌥ ⌘ ⌃", "• item", "▶ ▼ ◆ ● ○ ◉", "━━━ ─── ═══",
	"\x1b[1;38;5;196mbold \x1b[48;2;1;2;3mbg\x1b[39m fg\x1b[0m tail", "\x1b[7mrev\x1b[27m \x1b[4munder\x1b[24m \x1b[9mstrike\x1b[29m",
	"\x1b]8;id=1;http://q\x07linked text that wraps across a few columns\x1b]8;;\x07 after",
	"the quick brown fox jumps over the lazy dog and keeps running past the edge of the terminal",
	"日本語のテキストを折り返す　全角スペース　と English mixed 한국어 テキスト",
	"\xff\xfe", "\xed\xa0\x80", "\xed\xb0\x80x", "a\xe2\x82", "\xf0\x9f\x91", "https://example.com/a/very/long/url/that/has/no/spaces/at/all/anywhere?x=1&y=2#frag",
	"\x1b_Gf=100,i=1;AAAA\x1b\\", "\x1b]1337;File=inline=1:AAAA\x07",
	"line one\nline two with 日本語\nline three \x1b[31mred\x1b[0m",
	"    indented\tby tab\r\n and crlf", "a\u00a0b\u3000c\u2003d",
}

func TestSDKCopyAgreesWithSourceOnCorpus(t *testing.T) {
	for _, s := range widthDifferentialCorpus {
		if diff := sameOn(&sourceEngine, &copyEngine, s); diff != "" {
			t.Fatal(diff)
		}
	}
	// Every ordered pair of corpus entries joined, so clusters and escapes that meet across an edge are covered.
	short := widthDifferentialCorpus[:60]
	for _, a := range short {
		for _, b := range short {
			if diff := sameOn(&sourceEngine, &copyEngine, a+b); diff != "" {
				t.Fatal(diff)
			}
		}
	}
}

// TestSDKCopyAgreesWithSourceOnEveryCodePoint compares the table-driven functions on every code point from 0 to U+10FFFF, alone, after a
// base letter and before VS16, and joined to itself by a ZWJ.
func TestSDKCopyAgreesWithSourceOnEveryCodePoint(t *testing.T) {
	var sb strings.Builder
	for r := rune(0); r <= 0x10FFFF; r++ {
		if sourceEngine.eastAsianWidth(r) != copyEngine.eastAsianWidth(r) {
			t.Fatalf("eastAsianWidth(%U): source %d, copy %d", r, sourceEngine.eastAsianWidth(r), copyEngine.eastAsianWidth(r))
		}
		if sourceEngine.isJSSpace(r) != copyEngine.isJSSpace(r) {
			t.Fatalf("isJSSpace(%U): source %v, copy %v", r, sourceEngine.isJSSpace(r), copyEngine.isJSSpace(r))
		}
		sb.Reset()
		sb.WriteRune(r)
		s := sb.String()
		for _, in := range [...]string{s, "a" + s + "\ufe0f", s + "\u200d" + s} {
			if sourceEngine.visibleWidth(in) != copyEngine.visibleWidth(in) {
				t.Fatalf("visibleWidth(%q): source %d, copy %d", in, sourceEngine.visibleWidth(in), copyEngine.visibleWidth(in))
			}
			if sourceEngine.graphemeWidth(in) != copyEngine.graphemeWidth(in) {
				t.Fatalf("graphemeWidth(%q): source %d, copy %d", in, sourceEngine.graphemeWidth(in), copyEngine.graphemeWidth(in))
			}
			g1, r1 := sourceEngine.firstGrapheme(in)
			g2, r2 := copyEngine.firstGrapheme(in)
			if g1 != g2 || r1 != r2 {
				t.Fatalf("firstGrapheme(%q): source %q|%q, copy %q|%q", in, g1, r1, g2, r2)
			}
		}
	}
}

// TestSDKCopyAgreesWithSourceOnCodePointLines runs the layout functions (truncate, wrap, slice, trackers) on a line built around code
// points. A stride keeps the run short; the full sweep above already covers every code point and the boundaries the tables change at.
// PIG_WIDTH_DIFF_FULL=1 runs every code point.
func TestSDKCopyAgreesWithSourceOnCodePointLines(t *testing.T) {
	step := rune(211)
	if os.Getenv("PIG_WIDTH_DIFF_FULL") != "" {
		step = 1
	}
	for r := rune(0); r <= 0x10FFFF; r += step {
		s := "ab\x1b[1m" + string(r) + "\x1b[0mcd" + string(r) + "e"
		if diff := sameOn(&sourceEngine, &copyEngine, s); diff != "" {
			t.Fatal(diff)
		}
	}
}
