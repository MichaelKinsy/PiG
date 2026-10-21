package tui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestMarkedCJKClassMatchesPinnedPackage compares markedCJK with the CJK character class of the vendored marked-cjk-friendly 0.1.2 (tui/widthx/testdata/pi/marked-cjk-friendly) over every code point.
func TestMarkedCJKClassMatchesPinnedPackage(t *testing.T) {
	script := `
import { readFileSync } from "node:fs";
const source = readFileSync("widthx/testdata/pi/marked-cjk-friendly/dist/index.js", "utf8");
const cjk = JSON.parse(/const CJK = ("(?:[^"\\]|\\.)*");/.exec(source)[1]);
const test = new RegExp("[" + cjk + "]", "u");
const ranges = [];
for (let cp = 0; cp <= 0x10ffff; cp++) {
	if (cp >= 0xd800 && cp <= 0xdfff) continue;
	if (!test.test(String.fromCodePoint(cp))) continue;
	const last = ranges[ranges.length - 1];
	if (last && last[1] === cp - 1) last[1] = cp; else ranges.push([cp, cp]);
}
console.log(JSON.stringify(ranges));`
	out, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script).Output()
	if err != nil {
		t.Fatalf("pinned marked-cjk-friendly: %v", err)
	}
	var want [][2]rune
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	if got := markedCJKRanges[:]; !reflect.DeepEqual(got, want) {
		t.Errorf("markedCJKRanges differ from the package's CJK class:\n got %v\nwant %v", got, want)
	}
}

// TestMarkdownCJKEmphasisMatchesPi renders Pi's #10730 cases (packages/tui/test/markdown.test.ts "CJK emphasis"), their flanking-rule neighbours, astral and variation-selector previous characters, underscores and mixed delimiter runs through the installed pi-tui Markdown and compares the rows byte for byte.
func TestMarkdownCJKEmphasisMatchesPi(t *testing.T) {
	if _, err := os.Stat(filepath.Join("..", "extensions", "sdk-ts", "node_modules")); err != nil {
		t.Skip("the Pi oracle is not installed (make parity-deps)")
	}
	docs := []string{
		"**这是测试。**后面", "**一句话说清：**一台机器", "**要点到此为止。**20 小时", "字**「引用」**字", "*斜体。*后面",
		// Non-CJK text keeps the CommonMark flanking rules.
		"**end.**Then next", "**end.** Then next", "字**end.**字", "**这是测试。** 后面", "前**这是测试。**", "__这是测试。__后面", "_斜体。_后面", "字_斜体。_字",
		"***强调。***后面", "**a*b。**后面", "*a**b。*后面", "**「引用」**後ろ", "한글**「인용」**한글", "ひらがな**『引用』**かな",
		"𠀀**「引用」**字", "字**「引用」**𠀀", "𠀀**。**字", "\u8fd4\ufe0f**「引用」**字", "\U000e0100**「引用」**字", "😀**「引用」**😀", "a😀**。**b",
		"**テスト。**\n**続き。**", "- **项目。**说明\n- *次。*目", "> **引用。**文", "# **标题。**文", "`code`**。**字", "[**链接。**](https://example.com)后",
		"**foo**bar", "foo**bar**", "**「a」**b", "a**「b」**", "a**。**b", "a_。_b", "字**。**a", "a**。**字", "**\u3000a**", "**a\u3000**b",
	}
	compareMarkdownWithPi(t, docs)
}

// TestMarkdownCJKEmphasisPiCases is Pi's "CJK emphasis" block (packages/tui/test/markdown.test.ts, #10154) with Pi's expected strings:
// bold and italic delimited next to CJK punctuation render, and CommonMark flanking holds for non-CJK text.
func TestMarkdownCJKEmphasisPiCases(t *testing.T) {
	tag := func(name string) func(string) string {
		return func(text string) string { return "<" + name + ">" + text + "</" + name + ">" }
	}
	theme := MarkdownTheme{Bold: tag("b"), Italic: tag("i")}
	render := func(text string) []string {
		lines := NewMarkdownWithOptions(text, 0, 0, &theme, nil, nil).Render(80)
		for i, line := range lines {
			lines[i] = strings.TrimRight(line, " ")
		}
		return lines
	}
	for _, tc := range []struct{ source, want string }{
		{"**这是测试。**后面", "<b>这是测试。</b>后面"},
		{"**一句话说清：**一台机器", "<b>一句话说清：</b>一台机器"},
		{"**要点到此为止。**20 小时", "<b>要点到此为止。</b>20 小时"},
		{"字**「引用」**字", "字<b>「引用」</b>字"},
		{"*斜体。*后面", "<i>斜体。</i>后面"},
		{"**end.**Then next", "**end.**Then next"},
	} {
		if got := render(tc.source); !reflect.DeepEqual(got, []string{tc.want}) {
			t.Errorf("%q rendered %q, want %q", tc.source, got, []string{tc.want})
		}
	}
}
