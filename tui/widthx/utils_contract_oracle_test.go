package widthx

// Pi-oracle contract tests for the wrapping and truncation functions of utils.ts: splitIntoTokensWithAnsi, wrapSingleLine,
// breakLongWord, wrapTextWithAnsi and truncateToWidth. The module-private functions are exported by editing the pinned source
// text before Node loads it, so the oracle is Pi's own code. The corpus is every one-atom and two-atom combination of the atoms
// below (plain words, spaces, tabs, line breaks, CJK, emoji, zero-width and combining characters, every SGR/OSC 8/APC kind and
// malformed escapes) plus a fixed-seed sample of longer combinations, each at every width the case needs to hit a boundary.

import (
	"encoding/json"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// contractAtoms are the building blocks of the corpus: each exercises one branch family of the pinned functions.
var contractAtoms = []string{
	"a", "word", "longerword", "x ", " ", "  ", "\t", "\n", "\r\n", "\r",
	"日本", "語", "한", "ｱ", "👍", "👍🏽", "e\u0301", "\u200b", "🇺🇸", "👨\u200d👩\u200d👧", "⚠\ufe0f", "—", "…",
	"\x1b[1m", "\x1b[22m", "\x1b[2m", "\x1b[3m", "\x1b[23m", "\x1b[4m", "\x1b[24m", "\x1b[7m", "\x1b[27m", "\x1b[9m", "\x1b[29m", "\x1b[5m", "\x1b[8m",
	"\x1b[31m", "\x1b[39m", "\x1b[38;5;196m", "\x1b[38;2;1;2;3m", "\x1b[41m", "\x1b[49m", "\x1b[48;5;21m", "\x1b[1;31;4m", "\x1b[0m", "\x1b[m",
	"\x1b]8;;http://x\x07", "\x1b]8;;\x07", "\x1b]8;id=1;http://y\x1b\\", "\x1b]8;;\x1b\\", "\x1b_apc\x07", "\x1b_apc\x1b\\",
	"\x1b[2K", "\x1b[3A", "\x1b[?25l", "\x1b", "\x1b[", "\x1b[31", "\x1b]8;;", "\x1b]", "\x1bM", "\x1bx",
}

// contractCorpus is the deterministic corpus of strings every function is run over.
func contractCorpus() []string {
	var out []string
	out = append(out, "")
	out = append(out, contractAtoms...)
	for _, a := range contractAtoms {
		for _, b := range contractAtoms {
			out = append(out, a+b)
		}
	}
	r := rand.New(rand.NewPCG(0xB1, 0x70))
	for range 2500 {
		var b strings.Builder
		for range 3 + r.IntN(6) {
			b.WriteString(contractAtoms[r.IntN(len(contractAtoms))])
		}
		out = append(out, b.String())
	}
	// Prose around styled words, links and long unbroken runs, so wrapping happens between words as well as inside them.
	out = append(out, []string{
		"aaaa \x1b[1mbold\x1b[22m next word", "aaaa \x1b[31mred\x1b[39m  next", "\x1b[41m" + strings.Repeat(" ", 20) + "\x1b[0m",
		"ab\x1b[41m" + strings.Repeat(" ", 20) + "\x1b[49mcd", "aaa\x1b[9m    abcdefghij\x1b[29m", "\x1b[41;9mstrike me\x1b[0m",
		"see \x1b]8;;http://example.com\x07the link text\x1b]8;;\x07 and more words after it", strings.Repeat("abcdefghij", 5),
		strings.Repeat("日本語", 9), strings.Repeat("a ", 30), "  leading spaces and trailing spaces  ", "\x1b[4munderlined words that wrap around\x1b[24m done",
		"first line\n\x1b[31mred second line that is long\nthird\x1b[39m line", "x\x1b[0m   \x1b[0m y", "ab \x1b[22m cd", "ab\x1b[22m  cd",
	}...)
	return out
}

type contractCorpusJSON struct {
	Tokens    []string `json:"tokens"`
	WrapLine  [][2]any `json:"wrapLine"`
	BreakWord [][3]any `json:"breakWord"`
	Wrap      [][2]any `json:"wrap"`
	Trunc     [][4]any `json:"trunc"`
}

type contractResult struct {
	Tokens    [][]string `json:"tokens"`
	WrapLine  [][]string `json:"wrapLine"`
	BreakWord []struct {
		Lines  []string `json:"lines"`
		Active string   `json:"active"`
		Reset  string   `json:"reset"`
	} `json:"breakWord"`
	Wrap  [][]string `json:"wrap"`
	Trunc []string   `json:"trunc"`
}

// contractEllipses are the ellipsis arguments: Pi's default, the single-cell one, none, a styled one, blanks, wide ones that a narrow width clips in the middle of a character, tab ones with and without escape sequences, and one with a hyperlink.
var contractEllipses = []string{
	"...", "…", "", "\x1b[2m…\x1b[22m", " ", "  ", "日", "……", "\t", "x\x1b]8;;http://u\x07y\x1b]8;;\x07", "\x1b[31m...\x1b[0m",
	"日日", "日日日", "\t\t", "ab\t", "\x1b[2m\t\x1b[22m", "\x1b[2m日日\x1b[22mx", "日日\x1b[0mx", "\x1b[1m日\x1b[0m日", "\x1b[2m\tx\x1b[0m", "x\x1b[2m\t\x1b[0m\ty",
}

// contractWidths are the widths the wrapping functions are run at: a single cell, the widths that split a wide character, a
// short, a medium and a roomy line, and the two the renderers clamp away.
var contractWidths = []int{-1, 0, 1, 2, 3, 5, 8, 20}

func contractTokensCorpus() contractCorpusJSON { return contractCorpusJSON{Tokens: contractCorpus()} }

func contractWrapLineCorpus() contractCorpusJSON {
	var c contractCorpusJSON
	for _, s := range contractCorpus() {
		for _, w := range contractWidths {
			c.WrapLine = append(c.WrapLine, [2]any{s, w})
		}
	}
	return c
}

func contractWrapCorpus() contractCorpusJSON {
	var c contractCorpusJSON
	for _, s := range contractCorpus() {
		for _, w := range contractWidths {
			c.Wrap = append(c.Wrap, [2]any{s, w})
		}
	}
	return c
}

// contractTrackerStates are the tracker states breakLongWord starts from: none, bold, colored underline, background with strikethrough,
// a BEL- and an ST-terminated hyperlink, and the rarely used decorations.
var contractTrackerStates = []string{"", "\x1b[1m", "\x1b[31;4m", "\x1b[41m\x1b[9m", "\x1b]8;;http://x\x07", "\x1b]8;;http://y\x1b\\\x1b[4m", "\x1b[7m\x1b[2m\x1b[3m\x1b[5m\x1b[8m", "\x1b[38;5;1m\x1b[48;2;1;2;3m"}

func contractBreakWordCorpus() contractCorpusJSON {
	var c contractCorpusJSON
	for i, s := range contractCorpus() {
		for j, w := range contractWidths {
			c.BreakWord = append(c.BreakWord, [3]any{s, w, contractTrackerStates[(i+j)%len(contractTrackerStates)]})
		}
	}
	return c
}

func contractTruncCorpus() contractCorpusJSON {
	var c contractCorpusJSON
	r := rand.New(rand.NewPCG(0xB3, 0x72))
	for _, s := range contractCorpus() {
		for _, w := range []int{-1, 0, 1, 2, 3, 4, 6, 10, 40} {
			for _, e := range contractEllipses {
				if r.IntN(6) == 0 {
					c.Trunc = append(c.Trunc, [4]any{s, w, e, r.IntN(2) == 0})
				}
			}
			c.Trunc = append(c.Trunc, [4]any{s, w, "...", false}, [4]any{s, w, "...", true})
		}
	}
	return c
}

// runContractOracle runs the corpus through the pinned utils.ts.
func runContractOracle(t *testing.T, c contractCorpusJSON) contractResult {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node not found; the Pi oracle is required: %v", err)
	}
	dir := t.TempDir()
	abs, _ := filepath.Abs(filepath.Join("testdata", "pi"))
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.RequireDirectoryLink(t, filepath.Join(abs, "get-east-asian-width"), filepath.Join(dir, "node_modules", "get-east-asian-width"))
	src := piUtilsSource(t, abs)
	text := string(src)
	for _, decl := range []string{"class AnsiCodeTracker {", "function updateTrackerFromText(", "function splitIntoTokensWithAnsi(", "function wrapSingleLine(", "function breakLongWord("} {
		if strings.Count(text, "\n"+decl) != 1 {
			t.Fatalf("utils.ts has no unique %q declaration to export", decl)
		}
		text = strings.Replace(text, "\n"+decl, "\nexport "+decl, 1)
	}
	utils := filepath.Join(dir, "utils.ts")
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(utils, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	corpus := filepath.Join(dir, "corpus.json")
	if err := os.WriteFile(corpus, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), node, "--no-warnings", filepath.Join(abs, "contract_oracle.mjs"), corpus, "file://"+utils)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("pi oracle: %v", err)
	}
	var res contractResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode oracle output: %v", err)
	}
	return res
}

// A mismatch reporter that keeps the first lines readable.
type contractReporter struct {
	t          *testing.T
	mismatches int
}

func (r *contractReporter) report(format string, args ...any) {
	r.mismatches++
	if r.mismatches <= 40 {
		r.t.Errorf(format, args...)
	}
}

// TestSplitIntoTokensWithAnsiContractMatchesPi compares splitIntoTokensWithAnsi (utils.ts) with the pinned Pi over the corpus above.
func TestSplitIntoTokensWithAnsiContractMatchesPi(t *testing.T) {
	c := contractTokensCorpus()
	res := runContractOracle(t, c)
	if len(res.Tokens) != len(c.Tokens) {
		t.Fatalf("oracle answered %d cases for %d", len(res.Tokens), len(c.Tokens))
	}
	r := contractReporter{t: t}
	for i, s := range c.Tokens {
		if got := splitIntoTokensWithAnsi(s); !reflect.DeepEqual(got, res.Tokens[i]) && (len(got) != 0 || len(res.Tokens[i]) != 0) {
			r.report("splitIntoTokensWithAnsi(%+q) = %+q, Pi %+q", s, got, res.Tokens[i])
		}
	}
	t.Logf("cases=%d mismatches=%d", len(c.Tokens), r.mismatches)
}

// TestWrapSingleLineContractMatchesPi compares wrapSingleLine (utils.ts) with the pinned Pi over the corpus above at every contract width.
func TestWrapSingleLineContractMatchesPi(t *testing.T) {
	c := contractWrapLineCorpus()
	res := runContractOracle(t, c)
	if len(res.WrapLine) != len(c.WrapLine) {
		t.Fatalf("oracle answered %d cases for %d", len(res.WrapLine), len(c.WrapLine))
	}
	r := contractReporter{t: t}
	for i, tc := range c.WrapLine {
		s, w := tc[0].(string), tc[1].(int)
		if got := wrapSingleLine(s, w); !reflect.DeepEqual(got, res.WrapLine[i]) {
			r.report("wrapSingleLine(%+q, %d) = %+q, Pi %+q", s, w, got, res.WrapLine[i])
		}
	}
	t.Logf("cases=%d mismatches=%d", len(c.WrapLine), r.mismatches)
}

// TestBreakLongWordContractMatchesPi compares breakLongWord (utils.ts) with the pinned Pi: the rows, and the active codes and line-end
// reset of the tracker it leaves, from eight starting tracker states.
func TestBreakLongWordContractMatchesPi(t *testing.T) {
	c := contractBreakWordCorpus()
	res := runContractOracle(t, c)
	if len(res.BreakWord) != len(c.BreakWord) {
		t.Fatalf("oracle answered %d cases for %d", len(res.BreakWord), len(c.BreakWord))
	}
	r := contractReporter{t: t}
	for i, tc := range c.BreakWord {
		word, w, before := tc[0].(string), tc[1].(int), tc[2].(string)
		var tr AnsiCodeTracker
		tr.UpdateFromText(before)
		got := breakLongWord(word, w, &tr)
		want := res.BreakWord[i]
		if !reflect.DeepEqual(got, want.Lines) || tr.ActiveCodes() != want.Active || tr.LineEndReset() != want.Reset {
			r.report("breakLongWord(%+q, %d) after %+q = %+q active %+q reset %+q, Pi %+q active %+q reset %+q", word, w, before, got, tr.ActiveCodes(), tr.LineEndReset(), want.Lines, want.Active, want.Reset)
		}
	}
	t.Logf("cases=%d mismatches=%d", len(c.BreakWord), r.mismatches)
}

// TestWrapTextWithAnsiContractMatchesPi compares wrapTextWithAnsi (utils.ts) with the pinned Pi over the corpus above at every contract width.
func TestWrapTextWithAnsiContractMatchesPi(t *testing.T) {
	c := contractWrapCorpus()
	res := runContractOracle(t, c)
	if len(res.Wrap) != len(c.Wrap) {
		t.Fatalf("oracle answered %d cases for %d", len(res.Wrap), len(c.Wrap))
	}
	r := contractReporter{t: t}
	for i, tc := range c.Wrap {
		s, w := tc[0].(string), tc[1].(int)
		if got := WrapTextWithAnsi(s, w); !reflect.DeepEqual(got, res.Wrap[i]) {
			r.report("WrapTextWithAnsi(%+q, %d) = %+q, Pi %+q", s, w, got, res.Wrap[i])
		}
	}
	t.Logf("cases=%d mismatches=%d", len(c.Wrap), r.mismatches)
}

// TestTruncateToWidthContractMatchesPi compares truncateToWidth (utils.ts) with the pinned Pi over the corpus above, with and without
// padding, for the ellipses in contractEllipses and every width from below one cell to wider than the text.
func TestTruncateToWidthContractMatchesPi(t *testing.T) {
	c := contractTruncCorpus()
	res := runContractOracle(t, c)
	if len(res.Trunc) != len(c.Trunc) {
		t.Fatalf("oracle answered %d cases for %d", len(res.Trunc), len(c.Trunc))
	}
	r := contractReporter{t: t}
	for i, tc := range c.Trunc {
		s, w, e, p := tc[0].(string), tc[1].(int), tc[2].(string), tc[3].(bool)
		if got := TruncateToWidth(s, w, e, p); got != res.Trunc[i] {
			r.report("TruncateToWidth(%+q, %d, %+q, %v) = %+q, Pi %+q", s, w, e, p, got, res.Trunc[i])
		}
	}
	t.Logf("cases=%d mismatches=%d", len(c.Trunc), r.mismatches)
}
