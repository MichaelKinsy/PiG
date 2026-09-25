package widthx

// Differential parity test: runs upstream pi-tui's own utils.ts (vendored
// verbatim in testdata/pi) under Node and compares visibleWidth, grapheme
// segmentation, truncateToWidth and wrapTextWithAnsi against this package
// over an exhaustive single-code-point sweep, a fixed-seed adversarial
// corpus, a curated list and the text lines in tui/testdata. PiG adopts Pi's
// crash-on-overflow render check, which is only as safe as this parity.
//
// Skips when node is absent. When node's Unicode version differs from the
// generated tables, it skips locally and fails under CI (regenerate with
// gen/gen_tables.mjs).

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type piCorpus struct {
	Width   []string `json:"width"`
	Segs    []string `json:"segs"`
	Trunc   [][4]any `json:"trunc"`
	Wrap    [][2]any `json:"wrap"`
	Slice   [][4]any `json:"slice"`
	Extract [][5]any `json:"extract"`
}

type piResult struct {
	Unicode string     `json:"unicode"`
	Node    string     `json:"node"`
	Width   []int      `json:"width"`
	Segs    [][]string `json:"segs"`
	Trunc   []string   `json:"trunc"`
	Wrap    [][]string `json:"wrap"`
	Slice   []struct {
		Text  string `json:"text"`
		Width int    `json:"width"`
	} `json:"slice"`
	Extract []struct {
		Before      string `json:"before"`
		BeforeWidth int    `json:"beforeWidth"`
		After       string `json:"after"`
		AfterWidth  int    `json:"afterWidth"`
	} `json:"extract"`
}

var curatedWidthStrings = []string{
	"", "a", "hello world", "\t", "a\tb", "\x1b[31mred\x1b[0m", "日本語テキスト", "ｈａｌｆ ｶﾀｶﾅ",
	"👨\u200d👩\u200d👧\u200d👦", "🏳\ufe0f\u200d🌈", "🏴\u200d☠\ufe0f", "❤\ufe0f\u200d🔥", "❤\u200d🔥", "🇺🇸🇬🇧", "🇺", "1\ufe0f\u20e3", "#\u20e3", "👍🏽", "🫱🏻\u200d🫲🏿", "🧑🏾\u200d🦰",
	"⚠", "⚠\ufe0f", "⚠\ufe0e", "☺", "☺\ufe0f", "©", "©\ufe0f", "™\ufe0f", "↔\ufe0f", "◽", "⬛", "⬜\ufe0f", "⭐", "⌚", "⏺", "⎿", "✓", "✔", "✗",
	"क\u094dष", "क\u094d\u200dष", "स\u094dत\u094dर", "ক\u09cdষ", "ન\u0acdન", "ำ", "กำ", "ຳ", "ລຳ", "é", "é", "Z\u0334\u0322\u031b\u0317\u0353\u0356\u0339\u032b\u0323\u033c\u0353\u0300\u0314\u0312\u0352\u0351\u033e\u0358a\u0337\u035a\u034e\u031e\u033b\u0323\u030dl\u0336\u0330\u0325\u032f\u0348\u0309g\u0335\u031d\u032e\u0339\u0354\u0308\u0301\u0306\u0304o\u0337\u034d\u034e\u0313",
	"한국어", "각", "ᄀ가", "\u200b\u200c\u200d\ufeff", "\u2060", "\u00ad", "\u0000\u0007\u007f\u0085",
	"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\", "\x1b]8;;http://a\x07x\x1b]8;;\x07", "\x1b_pi:c\x07", "\x1b[3Aabc", "\x1b[2K", "\x1b[?25lhi\x1b[0m",
	"\x1b[?2026h", "\x1b[?2026l", "\x1b", "\x1b[", "x\x1b]133;A\x07y", "𝕳𝖊𝖑𝖑𝖔", "𠀀𠀁", "\ue000", "\U0010fffd", "\uffff", "\u0378",
	"\u1734", "\u302e", "\u065f", "\u0f7f", "\u102b\u102c", "a\u0903", "\u0903", "\u0e31", "a\u20e3", "\U000e0067",
	"╭──────╮ │ ok │ ╰──────╯", "⠋ Working...", "↑12k ↓3.4k R1.2M $0.123 (sub) 45.2%/200k (auto)", "~/src/pig (main) • claude-opus",
	"| 名前 | 説明 |\n|---|---|", "→ ← ⏎ ⇧ ⌥ ⌘ ⌃", "• item", "▶ ▼ ◆ ● ○ ◉", "━━━ ─── ═══",
}

var ansiTokens = []string{
	"\x1b[31m", "\x1b[0m", "\x1b[1;38;5;196m", "\x1b[48;2;1;2;3m", "\x1b[39m", "\x1b[7m", "\x1b[4m", "\x1b[24m",
	"\x1b]8;;https://x.y/z\x1b\\", "\x1b]8;;\x1b\\", "\x1b]8;id=1;http://q\x07", "\x1b]8;;\x07", "\x1b_pi:c\x07",
	"\x1b[3A", "\x1b[2K", "\x1b[H", "\x1b[10G", "\x1b[J", "\x1b[?25l", "\x1b[?2026h", "\x1b]133;A\x07", "\x1b", "\x1b[",
}

var runeRanges = [][2]rune{
	{0x20, 0x7e}, {0x00, 0x1f}, {0x7f, 0x9f}, {0xa0, 0x2ff}, {0x300, 0x36f}, {0x483, 0x489}, {0x591, 0x5c7}, {0x600, 0x6ff},
	{0x900, 0x97f}, {0x980, 0x9ff}, {0xa80, 0xaff}, {0xb00, 0xb7f}, {0xc00, 0xc7f}, {0xd00, 0xd7f}, {0xe00, 0xe7f}, {0xe80, 0xeff},
	{0xf00, 0xfff}, {0x1000, 0x109f}, {0x1100, 0x11ff}, {0x1700, 0x177f}, {0x1780, 0x17ff}, {0x1ab0, 0x1aff}, {0x1dc0, 0x1dff},
	{0x200b, 0x200f}, {0x2028, 0x202e}, {0x2060, 0x206f}, {0x20d0, 0x20ff}, {0x2100, 0x2bff}, {0x2e80, 0x2fff}, {0x3000, 0x30ff},
	{0x3130, 0x318f}, {0x4e00, 0x4e80}, {0xa960, 0xa97f}, {0xac00, 0xac40}, {0xd7b0, 0xd7ff}, {0xe000, 0xe010}, {0xfe00, 0xfe0f},
	{0xfe20, 0xfe2f}, {0xfeff, 0xfeff}, {0xff00, 0xffef}, {0xfff0, 0xffff}, {0x1f000, 0x1faff}, {0x1f1e6, 0x1f1ff}, {0x1f3fb, 0x1f3ff},
	{0x1f9b0, 0x1f9b3}, {0x1d400, 0x1d4ff}, {0x20000, 0x20010}, {0xe0020, 0xe007f}, {0xe0100, 0xe01ef}, {0x10fff0, 0x10ffff},
	{0x11000, 0x1107f}, {0x11180, 0x111df}, {0x16fe0, 0x16fff},
}

var joiners = []string{"\u200d", "\ufe0f", "\ufe0e", "\u20e3", "\u094d", "\u09cd", "\u0acd", "\u0b4d", "\u0c4d", "\u0d4d", "\u1039", "\u17d2", " ", "\t", "\n"}

func randRune(r *rand.Rand) rune {
	for {
		rg := runeRanges[r.IntN(len(runeRanges))]
		c := rg[0] + rune(r.IntN(int(rg[1]-rg[0]+1)))
		if utf8.ValidRune(c) {
			return c
		}
	}
}

func randToken(r *rand.Rand) string {
	switch k := r.IntN(20); {
	case k < 7:
		return string(randRune(r))
	case k < 10:
		return rgiSequences[r.IntN(len(rgiSequences))]
	case k < 12:
		return joiners[r.IntN(len(joiners))]
	case k < 14:
		return ansiTokens[r.IntN(len(ansiTokens))]
	case k < 16:
		return curatedWidthStrings[r.IntN(len(curatedWidthStrings))]
	case k < 17:
		// perturbed emoji sequence: drop or duplicate one code point
		rs := []rune(rgiSequences[r.IntN(len(rgiSequences))])
		i := r.IntN(len(rs))
		if r.IntN(2) == 0 {
			rs = append(rs[:i], rs[i+1:]...)
		} else {
			rs = append(rs[:i+1], rs[i:]...)
		}
		return string(rs)
	default:
		words := []string{"the", "quick", "brown", "fox", "path/to/file.go:12", "https://example.com/a/b?c=d", "--flag=value", "x"}
		return words[r.IntN(len(words))]
	}
}

func randString(r *rand.Rand, maxTokens int) string {
	var b strings.Builder
	n := 1 + r.IntN(maxTokens)
	for range n {
		b.WriteString(randToken(r))
	}
	return b.String()
}

// testdataLines collects text lines from tui/testdata goldens and fixtures:
// the strings PiG's tools, markdown, footers and extensions actually render.
func testdataLines(t *testing.T) []string {
	var lines []string
	root := filepath.Join("..", "testdata")
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || info.Size() > 256<<10 {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil || !utf8.Valid(data) {
			return nil
		}
		for l := range strings.SplitSeq(string(data), "\n") {
			if l != "" && len(lines) < 20000 {
				lines = append(lines, l)
			}
		}
		return nil
	})
	return lines
}

func buildPiCorpus(t *testing.T) piCorpus {
	var c piCorpus
	// Exhaustive single code points (visibleWidth + segmentation-free).
	for cp := rune(0); cp <= 0x10ffff; cp++ {
		if utf8.ValidRune(cp) {
			c.Width = append(c.Width, string(cp))
		}
	}
	for _, s := range rgiSequences {
		c.Width = append(c.Width, s)
	}
	c.Width = append(c.Width, curatedWidthStrings...)
	real := testdataLines(t)
	c.Width = append(c.Width, real...)
	r := rand.New(rand.NewPCG(0x5eed, 0x87_1))
	for range 150000 {
		c.Width = append(c.Width, randString(r, 8))
	}
	for range 60000 {
		c.Segs = append(c.Segs, stripForSegs(randString(r, 6)))
	}
	ellipses := []string{"...", "…", "", "\x1b[2m…\x1b[22m"}
	truncSrc := append(append([]string{}, curatedWidthStrings...), real...)
	for range 40000 {
		truncSrc = append(truncSrc, randString(r, 12))
	}
	for _, s := range truncSrc {
		c.Trunc = append(c.Trunc, [4]any{s, r.IntN(40), ellipses[r.IntN(len(ellipses))], r.IntN(2) == 0})
	}
	wrapSrc := append(append([]string{}, curatedWidthStrings...), real...)
	for range 25000 {
		var b strings.Builder
		for range 1 + r.IntN(10) {
			b.WriteString(randString(r, 5))
			b.WriteString([]string{" ", " ", "  ", "\n", "\t", ""}[r.IntN(6)])
		}
		wrapSrc = append(wrapSrc, b.String())
	}
	// sliceWithWidth / extractSegments: the overlay compositing primitives.
	for _, s := range truncSrc {
		c.Slice = append(c.Slice, [4]any{s, r.IntN(30), r.IntN(30), r.IntN(2) == 0})
		be := r.IntN(30)
		c.Extract = append(c.Extract, [5]any{s, be, be + r.IntN(20), r.IntN(30), r.IntN(2) == 0})
	}
	for _, s := range wrapSrc {
		c.Wrap = append(c.Wrap, [2]any{s, 1 + r.IntN(40)})
	}
	return c
}

func stripForSegs(s string) string { return StripAnsi(s) }

func segsOf(s string) []string {
	var out []string
	for s != "" {
		var g string
		g, s = FirstGrapheme(s)
		out = append(out, g)
	}
	return out
}

func runPiOracle(t *testing.T, c piCorpus) piResult {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		// The oracle is the acceptance evidence for faithful overflow: CI must
		// not report PASS without it.
		if os.Getenv("CI") != "" {
			t.Fatal("node not found; the differential Pi width oracle is required in CI")
		}
		t.Skip("node not found; differential Pi width test skipped")
	}
	dir := t.TempDir()
	abs, _ := filepath.Abs(filepath.Join("testdata", "pi"))
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, filepath.Join(abs, "get-east-asian-width"), filepath.Join(dir, "node_modules", "get-east-asian-width"))
	src, err := os.ReadFile(filepath.Join(abs, "utils.ts"))
	if err != nil {
		t.Fatal(err)
	}
	utils := filepath.Join(dir, "utils.ts")
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o644)
	if err := os.WriteFile(utils, src, 0o644); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(c)
	corpus := filepath.Join(dir, "corpus.json")
	if err := os.WriteFile(corpus, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--no-warnings", filepath.Join(abs, "oracle.mjs"), corpus, "file://"+utils)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("pi oracle: %v", err)
	}
	var res piResult
	dec := json.NewDecoder(strings.NewReader(string(out)))
	if err := dec.Decode(&res); err != nil {
		t.Fatalf("decode oracle output: %v", err)
	}
	return res
}

func TestPiWidthDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("differential Pi width test skipped in -short mode")
	}
	c := buildPiCorpus(t)
	res := runPiOracle(t, c)
	if want := strings.Fields(unicodeTablesVersion)[1]; res.Unicode != want {
		msg := fmt.Sprintf("node %s has Unicode %s but tables are %s; regenerate with gen/gen_tables.mjs", res.Node, res.Unicode, want)
		if os.Getenv("CI") != "" {
			t.Fatal(msg)
		}
		t.Skip(msg)
	}
	var mism []string
	report := func(kind string, format string, args ...any) {
		mism = append(mism, kind+": "+fmt.Sprintf(format, args...))
	}
	counts := map[string]int{}
	for i, s := range c.Width {
		if got := VisibleWidth(s); got != res.Width[i] {
			counts["visibleWidth"]++
			report("visibleWidth", "%+q go=%d pi=%d goSegs=%+q", s, got, res.Width[i], segsOf(StripAnsi(s)))
		}
	}
	for i, s := range c.Segs {
		if got := segsOf(s); !reflect.DeepEqual(got, res.Segs[i]) && (len(got) != 0 || len(res.Segs[i]) != 0) {
			counts["segment"]++
			report("segment", "%+q go=%+q pi=%+q", s, got, res.Segs[i])
		}
	}
	for i, tc := range c.Trunc {
		s, w, e, p := tc[0].(string), tc[1].(int), tc[2].(string), tc[3].(bool)
		if got := TruncateToWidth(s, w, e, p); got != res.Trunc[i] {
			counts["truncateToWidth"]++
			report("truncateToWidth", "(%+q, %d, %+q, %v) go=%+q pi=%+q", s, w, e, p, got, res.Trunc[i])
		}
	}
	for i, tc := range c.Wrap {
		s, w := tc[0].(string), tc[1].(int)
		got := WrapTextWithAnsi(s, w)
		if !reflect.DeepEqual(got, res.Wrap[i]) {
			counts["wrapTextWithAnsi"]++
			report("wrapTextWithAnsi", "(%+q, %d) go=%+q pi=%+q", s, w, got, res.Wrap[i])
		}
	}
	for i, tc := range c.Slice {
		s, a, n, strict := tc[0].(string), tc[1].(int), tc[2].(int), tc[3].(bool)
		got := SliceWithWidth(s, a, n, strict)
		if want := res.Slice[i]; got.Text != want.Text || got.Width != want.Width {
			counts["sliceWithWidth"]++
			report("sliceWithWidth", "(%+q, %d, %d, %v) go=%+q/%d pi=%+q/%d", s, a, n, strict, got.Text, got.Width, want.Text, want.Width)
		}
	}
	for i, tc := range c.Extract {
		s, be, as, al, strict := tc[0].(string), tc[1].(int), tc[2].(int), tc[3].(int), tc[4].(bool)
		got := ExtractSegments(s, be, as, al, strict)
		want := res.Extract[i]
		if got.Before != want.Before || got.BeforeWidth != want.BeforeWidth || got.After != want.After || got.AfterWidth != want.AfterWidth {
			counts["extractSegments"]++
			report("extractSegments", "(%+q, %d, %d, %d, %v) go=%+v pi=%+v", s, be, as, al, strict, got, want)
		}
	}
	total := len(c.Width) + len(c.Segs) + len(c.Trunc) + len(c.Wrap) + len(c.Slice) + len(c.Extract)
	t.Logf("pi width differential: node %s unicode %s; cases width=%d segs=%d trunc=%d wrap=%d slice=%d extract=%d total=%d; mismatches=%d %v",
		res.Node, res.Unicode, len(c.Width), len(c.Segs), len(c.Trunc), len(c.Wrap), len(c.Slice), len(c.Extract), total, len(mism), counts)
	for i, m := range mism {
		if i >= 40 {
			break
		}
		t.Error(m)
	}
	if len(mism) > 0 {
		t.Fatalf("%d mismatches against upstream pi-tui (%v)", len(mism), counts)
	}
}
