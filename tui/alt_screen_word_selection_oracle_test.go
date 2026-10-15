package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// tui-alt-screen.ts:1175-1210 getWordSelection against pinned pi-tui: a double-click selects the Intl.Segmenter word (getWordSegmenter, utils.ts) under the clicked column, measured with visibleWidth and joined across "/" and "-". Thai, Lao, Khmer and Burmese need ICU's dictionary words, Devanagari conjuncts are one grapheme, and emoji ZWJ sequences are one cluster; every column of every line must select Pi's range.
// mutation-checked: segmenting with rivo/uniseg (Unicode 15 UAX #29 without dictionaries) fails the Thai, Lao, Khmer and Devanagari lines; dropping the joiners fails the path line.
func TestAltScreenWordSelectionMatchesPi(t *testing.T) {
	lines := []string{
		"สวัสดีครับ",
		"ภาษาไทยง่ายนิดเดียว test",
		"ສະບາຍດີ ທຸກຄົນ",
		"សួស្តីពិភពលោក",
		"မင်္ဂလာပါ",
		"क्षत्रिय नमस्ते",
		"👨‍👩‍👧 family 🏳️‍🌈 flag",
		"日本語のテキスト 中文分词",
		"path/to/some-file.go:12 ok",
		"e\u0301te\u0301 café",
		"\x1b[31mred\x1b[0m words-here",
		"   ",
		"",
	}
	type probe struct {
		Line string `json:"line"`
		Col  int    `json:"col"`
	}
	var probes []probe
	for _, line := range lines {
		for col := range widthx.VisibleWidth(widthx.StripTerminalSequences(line)) + 2 {
			probes = append(probes, probe{line, col})
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/alt_screen_word_selection.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want struct {
		Results [][]int `json:"results"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, p := range probes {
		screen := &TuiAltScreen{previousScreen: []string{p.Line}}
		got, ok := screen.getWordSelection(selectionPoint{row: 0, col: p.Col})
		var gotRange []int
		if ok {
			gotRange = []int{got.start.col, got.end.col}
		}
		if !slices.Equal(gotRange, want.Results[i]) {
			if failures++; failures <= 20 {
				t.Errorf("getWordSelection(%q, col %d) = %v, Pi %v", p.Line, p.Col, gotRange, want.Results[i])
			}
		}
	}
	if failures > 20 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
