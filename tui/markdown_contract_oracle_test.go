package tui

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui/widthx"
)

// Pi-oracle contract tests for the Markdown renderers of packages/tui/src/components/markdown.ts (render, renderBlock, renderToken, renderInlineTokens,
// renderList, renderTable). markdown_render_oracle_test.go compares the default constructor; these tests vary everything else a caller can pass or
// observe: the theme (tagged, dark, light), the default text style (foreground, background, bold, italic, strikethrough, underline), the options
// (source marker and escape preservation, LaTeX, transform), the hyperlink capability, the code highlighter and the code indent, image rows, and
// sequences of render, setText and invalidate with the callbacks they trigger.

type markdownContractStyle struct {
	Color         bool `json:"color"`
	Bg            bool `json:"bg"`
	Bold          bool `json:"bold"`
	Italic        bool `json:"italic"`
	Strikethrough bool `json:"strikethrough"`
	Underline     bool `json:"underline"`
}

type markdownContractOptions struct {
	PreserveOrderedListMarkers bool   `json:"preserveOrderedListMarkers"`
	PreserveBackslashEscapes   bool   `json:"preserveBackslashEscapes"`
	RenderLatex                *bool  `json:"renderLatex"`
	Transform                  string `json:"transform,omitempty"`
}

type markdownContractOp struct {
	Op    string `json:"op"`
	Width int    `json:"width"`
	Text  string `json:"text"`
}

type markdownContractProbe struct {
	Text      string                  `json:"text"`
	PaddingX  int                     `json:"paddingX"`
	PaddingY  int                     `json:"paddingY"`
	Theme     string                  `json:"theme"`
	Style     *markdownContractStyle  `json:"style"`
	Options   markdownContractOptions `json:"options"`
	Links     bool                    `json:"links"`
	Highlight bool                    `json:"highlight"`
	Indent    *string                 `json:"indent"`
	Ops       []markdownContractOp    `json:"ops"`
}

type markdownContractLog struct {
	Transform [][2]any   `json:"transform"`
	Bg        int        `json:"bg"`
	Highlight [][]string `json:"highlight"`
}

type markdownContractRender struct {
	Rows []string            `json:"rows"`
	Log  markdownContractLog `json:"log"`
}

func (l markdownContractLog) normalized() markdownContractLog {
	out := markdownContractLog{Bg: l.Bg, Transform: l.Transform, Highlight: l.Highlight}
	if out.Transform == nil {
		out.Transform = [][2]any{}
	}
	if out.Highlight == nil {
		out.Highlight = [][]string{}
	}
	return out
}

// markdownContractTransforms are the display transforms the probes name.
var markdownContractTransforms = map[string]func(markdown string, width int) string{
	"append": func(markdown string, width int) string { return markdown + "\n\n[" + strconv.Itoa(width) + "]" },
	"empty":  func(string, int) string { return "" },
	"blank":  func(string, int) string { return "  \n" },
	"width":  func(_ string, width int) string { return strconv.Itoa(width) },
}

// build is the Pig component of a probe, with the callbacks it makes appended to log.
func (p markdownContractProbe) build(log *markdownContractLog) *Markdown {
	var theme *MarkdownTheme
	if p.Theme == "tagged" {
		tagged := taggedMarkdownTheme()
		theme = &tagged
		if p.Highlight {
			theme.HighlightCode = func(code, lang string) []string {
				log.Highlight = append(log.Highlight, []string{code, lang})
				lines := strings.Split(code, "\n")
				for i, line := range lines {
					lines[i] = "<hl " + lang + ">" + line + "</hl>"
				}
				return lines
			}
		}
		theme.CodeBlockIndent = p.Indent
	} else {
		SetTheme(p.Theme)
	}
	var style *DefaultTextStyle
	if p.Style != nil {
		style = &DefaultTextStyle{Bold: p.Style.Bold, Italic: p.Style.Italic, Strikethrough: p.Style.Strikethrough, Underline: p.Style.Underline}
		if p.Style.Color {
			style.Color = func(text string) string { return "<dc>" + text + "</dc>" }
		}
		if p.Style.Bg {
			style.BgColor = func(text string) string {
				log.Bg++
				return "<bg>" + text + "</bg>"
			}
		}
	}
	options := &MarkdownOptions{PreserveOrderedListMarkers: p.Options.PreserveOrderedListMarkers, PreserveBackslashEscapes: p.Options.PreserveBackslashEscapes, RenderLatex: p.Options.RenderLatex}
	if p.Options.Transform != "" {
		transform := markdownContractTransforms[p.Options.Transform]
		options.Transform = func(markdown string, width int) string {
			log.Transform = append(log.Transform, [2]any{markdown, float64(width)})
			return transform(markdown, width)
		}
	}
	return NewMarkdownWithOptions(p.Text, p.PaddingX, p.PaddingY, theme, style, options)
}

func piMarkdownContract(t *testing.T, probes []markdownContractProbe) [][]markdownContractRender {
	t.Helper()
	return piOracleProbes[markdownContractProbe, []markdownContractRender](t, "markdown_contract.mjs", probes)
}

// compareMarkdownContract requires the Pig component of every probe to render the rows and make the callbacks pi-tui does, and returns the number of probes that differ.
// The rows and the transform calls must match on every render. The background function is called once for each row that is not an image row, in
// order, and then once for each padding row. markdown.ts also reuses the rows of a block of the same source and width, from the same render or the render
// before (held in a WeakRef), without calling the function again, so when every block is laid out again (the first render, the render after invalidate and a
// render at another width) PiG's calls must equal that count and Pi's must not exceed it. The highlighter is called once per code block with new code: Pi
// keeps the highlighting per block source and PiG per language and code, so PiG's calls must be among Pi's.
func compareMarkdownContract(t *testing.T, probes []markdownContractProbe) int {
	t.Helper()
	differences, rowFailures := markdownContractDifferences(t, probes)
	reported := map[string]bool{}
	for i := range probes {
		if reason, differs := differences[i]; differs {
			if key := probes[i].Text; !reported[key] && len(reported) < 30 {
				reported[key] = true
				t.Errorf("%s", reason)
			}
		}
	}
	t.Logf("%d probes: %d differ from Pi, %d of them in the rows", len(probes), len(differences), rowFailures)
	return len(differences)
}

// markdownContractDifferences returns, for each probe whose rows or callbacks differ from pi-tui's (see compareMarkdownContract), the index of the probe and the reason, and the number of
// probes that differ in the rows.
func markdownContractDifferences(t *testing.T, probes []markdownContractProbe) (differences map[int]string, rowFailures int) {
	t.Helper()
	expected := piMarkdownContract(t, probes)
	previous := GetCapabilities()
	t.Cleanup(func() {
		SetCapabilities(previous)
		SetTheme("dark")
	})
	differences = map[int]string{}
	for i, probe := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: true, Hyperlinks: probe.Links})
		var log markdownContractLog
		md := probe.build(&log)
		var got []markdownContractRender
		var exactBg []bool
		cold, lastWidth := true, -1
		for _, op := range probe.Ops {
			switch op.Op {
			case "render":
				rows := nonNil(md.Render(op.Width))
				got = append(got, markdownContractRender{Rows: rows, Log: log.normalized()})
				exactBg = append(exactBg, cold || op.Width != lastWidth)
				log = markdownContractLog{}
				cold, lastWidth = false, op.Width
			case "setText":
				md.SetText(op.Text)
			case "invalidate":
				md.Invalidate()
				cold = true
			}
		}
		reason := ""
		for j, render := range expected[i] {
			want := markdownContractRender{Rows: nonNil(render.Rows), Log: render.Log.normalized()}
			if !reflect.DeepEqual(got[j].Rows, want.Rows) {
				rowFailures++
				reason = markdownContractRowDifference(probe, j, got[j].Rows, want.Rows)
				break
			}
			if reason == "" {
				reason = markdownContractCallbackDifference(probe, j, got[j], want.Log, exactBg[j])
			}
		}
		if reason != "" {
			differences[i] = reason
		}
	}
	return differences, rowFailures
}

func markdownContractVariantOf(probe markdownContractProbe) string {
	variant := probe
	variant.Text, variant.Ops = "", nil
	head, _ := json.Marshal(variant)
	return string(head)
}

// markdownContractRowDifference names the first row of render n that differs from Pi's.
func markdownContractRowDifference(probe markdownContractProbe, n int, got, want []string) string {
	for row := range max(len(got), len(want)) {
		var g, e string
		if row < len(got) {
			g = got[row]
		}
		if row < len(want) {
			e = want[row]
		}
		if g != e || len(got) != len(want) {
			return fmt.Sprintf("%q with %s, render %d (width %d) of %q: %d rows, Pi %d; first difference at row %d\n  Pig %q\n  Pi  %q", probe.Text, markdownContractVariantOf(probe), n, markdownContractWidthOf(probe, n), markdownContractTextAt(probe, n), len(got), len(want), row, g, e)
		}
	}
	return ""
}

// markdownContractCallbackDifference reports the callbacks of render n that differ from Pi's, or "" when they are the ones the contract requires.
func markdownContractCallbackDifference(probe markdownContractProbe, n int, render markdownContractRender, want markdownContractLog, exactBg bool) string {
	problem := ""
	got := render.Log
	switch {
	case !reflect.DeepEqual(got.Transform, want.Transform):
		problem = "transform calls"
	case exactBg && probe.Style != nil && probe.Style.Bg && (got.Bg != markdownContractBackgroundCalls(render.Rows, probe.PaddingY) || want.Bg > got.Bg):
		problem = "background calls"
	case !markdownContractCallsAmong(got.Highlight, want.Highlight):
		problem = "highlighter calls"
	}
	if problem == "" {
		return ""
	}
	g, _ := json.Marshal(got)
	e, _ := json.Marshal(want)
	return fmt.Sprintf("%q with %s, render %d (width %d): %s differ\n  Pig %s\n  Pi  %s", probe.Text, markdownContractVariantOf(probe), n, markdownContractWidthOf(probe, n), problem, g, e)
}

// markdownContractBackgroundCalls is the calls of the background function a render of these rows makes when every block is laid out: one per row that is not an
// image row, with the padding rows counted once, and none when the text renders no rows.
func markdownContractBackgroundCalls(rows []string, paddingY int) int {
	if len(rows) == 1 && rows[0] == "" {
		return 0
	}
	calls := 0
	for i, row := range rows {
		if !widthx.IsImageLine(row) && i >= paddingY && i < len(rows)-paddingY {
			calls++
		}
	}
	if len(rows) > 0 {
		calls += paddingY
	}
	return calls
}

// markdownContractCallsAmong reports that every call of got is one of want, each used once.
func markdownContractCallsAmong(got, want [][]string) bool {
	left := slices.Clone(want)
	for _, call := range got {
		at := slices.IndexFunc(left, func(other []string) bool { return slices.Equal(call, other) })
		if at < 0 {
			return false
		}
		left = slices.Delete(left, at, at+1)
	}
	return true
}

// markdownContractTextAt is the text of the component at the n-th render of a probe.
func markdownContractTextAt(probe markdownContractProbe, n int) string {
	text := probe.Text
	for _, op := range probe.Ops {
		switch op.Op {
		case "setText":
			text = op.Text
		case "render":
			if n == 0 {
				return text
			}
			n--
		}
	}
	return text
}

// markdownContractWidthOf is the width of the n-th render of a probe.
func markdownContractWidthOf(probe markdownContractProbe, n int) int {
	for _, op := range probe.Ops {
		if op.Op == "render" {
			if n == 0 {
				return op.Width
			}
			n--
		}
	}
	return 0
}

func markdownContractRenderOps(widths ...int) []markdownContractOp {
	ops := make([]markdownContractOp, len(widths))
	for i, width := range widths {
		ops[i] = markdownContractOp{Op: "render", Width: width}
	}
	return ops
}

// markdownContractVariants name the constructor and capability variations, each applied to every document of markdownRenderDocs.
func markdownContractVariants() []struct {
	name  string
	apply func(*markdownContractProbe)
} {
	style := func(s markdownContractStyle) func(*markdownContractProbe) {
		return func(p *markdownContractProbe) { p.Style = &s }
	}
	empty, tab := "", "\t>"
	no := false
	return []struct {
		name  string
		apply func(*markdownContractProbe)
	}{
		{"default style color", style(markdownContractStyle{Color: true})},
		{"default style background", style(markdownContractStyle{Bg: true})},
		{"default style bold", style(markdownContractStyle{Bold: true})},
		{"default style italic", style(markdownContractStyle{Italic: true})},
		{"default style strikethrough", style(markdownContractStyle{Strikethrough: true})},
		{"default style underline", style(markdownContractStyle{Underline: true})},
		{"default style everything", style(markdownContractStyle{Color: true, Bg: true, Bold: true, Italic: true, Strikethrough: true, Underline: true})},
		{"preserve list markers", func(p *markdownContractProbe) { p.Options.PreserveOrderedListMarkers = true }},
		{"preserve backslash escapes", func(p *markdownContractProbe) { p.Options.PreserveBackslashEscapes = true }},
		{"latex off", func(p *markdownContractProbe) { p.Options.RenderLatex = &no }},
		{"transform append", func(p *markdownContractProbe) { p.Options.Transform = "append" }},
		{"transform empty", func(p *markdownContractProbe) { p.Options.Transform = "empty" }},
		{"transform blank", func(p *markdownContractProbe) { p.Options.Transform = "blank" }},
		{"hyperlinks", func(p *markdownContractProbe) { p.Links = true }},
		{"highlighter", func(p *markdownContractProbe) { p.Highlight = true }},
		{"highlighter and code indent", func(p *markdownContractProbe) { p.Highlight, p.Indent = true, &tab }},
		{"empty code indent", func(p *markdownContractProbe) { p.Indent = &empty }},
		{"dark theme", func(p *markdownContractProbe) { p.Theme = "dark" }},
		{"light theme and hyperlinks", func(p *markdownContractProbe) { p.Theme, p.Links = "light", true }},
		{"dark theme default style", func(p *markdownContractProbe) {
			p.Theme = "dark"
			p.Style = &markdownContractStyle{Bold: true, Italic: true}
		}},
	}
}

// TestMarkdownRenderVariantsMatchPi renders every document of markdownRenderDocs under each variant of markdownContractVariants at five widths
// and two paddings, and requires the rows and every callback (transform, background, highlight) to equal pi-tui's.
// Pi source: packages/tui/src/components/markdown.ts
func TestMarkdownRenderVariantsMatchPi(t *testing.T) {
	var probes []markdownContractProbe
	for _, variant := range markdownContractVariants() {
		for _, doc := range markdownRenderDocs() {
			if doc == markdownOpenLinkRepeatDoc {
				continue
			}
			for _, pad := range [][2]int{{1, 1}, {2, 0}} {
				probe := markdownContractProbe{Text: doc, PaddingX: pad[0], PaddingY: pad[1], Theme: "tagged", Ops: markdownContractRenderOps(2, 7, 16, 40, 80)}
				variant.apply(&probe)
				probes = append(probes, probe)
			}
		}
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// markdownContractMediaDocs are the documents of the code, image and highlighter shapes the variants above cannot reach: a code block of each language kind
// (none, known, unknown, with an info string of several words), indented code, repeated code, code in quotes and lists, a row that is an image sequence
// and the escape sequences of Kitty and iTerm2 images inside text.
func markdownContractMediaDocs() []string {
	return []string{
		"```\nplain\n```", "```go\nfunc main() {}\n```", "```nosuchlanguage\nx\n```", "```js title=\"a\"\nlet a\n```", "    indented\n    code", "```go\nsame\n```\n\n```go\nsame\n```",
		"```go\none\n```\n\n```py\ntwo\n```\n\ntext", "> ```go\n> in quote\n> ```", "- ```go\n  in list\n  ```", "```\nmulti\nline\n\nwith blank\n```", "```go\n```", "```\n\n```",
		"\x1b_Gf=100,m=0;QUJD\x1b\\", "text\n\n\x1b_Gf=100;QUJD\x1b\\\n\n\x1b]1337;File=inline=1:QUJD\x07", "before \x1b_Gf=100;QUJD\x1b\\ after", "\x1b]1337;File=size=4:QUJD\x07",
		"- \x1b_Gf=1;QUJD\x1b\\", "> \x1b_Gf=1;QUJD\x1b\\", "| a |\n|---|\n| \x1b_Gf=1;QUJD\x1b\\ |", "# \x1b_Gf=1;QUJD\x1b\\",
	}
}

// TestMarkdownCodeAndImageRowsMatchPi covers markdownContractMediaDocs under the highlighter, indent, default-style and background variants.
func TestMarkdownCodeAndImageRowsMatchPi(t *testing.T) {
	tab, empty := "\t>", ""
	var probes []markdownContractProbe
	for _, doc := range markdownContractMediaDocs() {
		for _, apply := range []func(*markdownContractProbe){
			func(*markdownContractProbe) {},
			func(p *markdownContractProbe) { p.Highlight = true },
			func(p *markdownContractProbe) { p.Highlight, p.Indent = true, &tab },
			func(p *markdownContractProbe) { p.Indent = &empty },
			func(p *markdownContractProbe) { p.Style = &markdownContractStyle{Bg: true, Color: true} },
			func(p *markdownContractProbe) { p.Theme = "dark" },
			func(p *markdownContractProbe) { p.Theme = "light" },
		} {
			for _, pad := range [][2]int{{0, 0}, {2, 1}} {
				probe := markdownContractProbe{Text: doc, PaddingX: pad[0], PaddingY: pad[1], Theme: "tagged", Ops: markdownContractRenderOps(1, 3, 10, 40)}
				apply(&probe)
				probes = append(probes, probe)
			}
		}
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// markdownContractSequences are the call sequences that show when a render is served from the cache or from the rendered blocks of the previous render and
// when it is computed again: the same width, another width, the same text, other text, invalidate and a streamed text that grows one character at a time.
func markdownContractSequences() [][]markdownContractOp {
	render := func(width int) markdownContractOp { return markdownContractOp{Op: "render", Width: width} }
	setText := func(text string) markdownContractOp { return markdownContractOp{Op: "setText", Text: text} }
	invalidate := markdownContractOp{Op: "invalidate"}
	const doc = "intro\n\n```go\nfn()\n```\n\n- a\n- b\n\n| h |\n|---|\n| c |\n\n> q"
	return [][]markdownContractOp{
		{render(80), render(80), render(80)},
		{render(80), render(40), render(80)},
		{render(80), setText(doc), render(80)},
		{render(80), setText("other"), render(80), render(80)},
		{render(80), invalidate, render(80), render(80)},
		{invalidate, render(80)},
		{render(80), setText("  "), render(80), setText(doc), render(80)},
		{render(30), render(60), invalidate, render(30), setText(doc + "\n\n```go\nfn()\n```"), render(30)},
		{setText(""), render(20), setText("x"), render(20)},
	}
}

// TestMarkdownRenderSequencesMatchPi runs the sequences of markdownContractSequences and the streaming growth of documents character by character with
// callbacks that record every call (transform, background, highlight), against pi-tui.
func TestMarkdownRenderSequencesMatchPi(t *testing.T) {
	const doc = "intro\n\n```go\nfn()\n```\n\n- a\n- b\n\n| h |\n|---|\n| c |\n\n> q"
	var probes []markdownContractProbe
	no := false
	for _, ops := range markdownContractSequences() {
		for _, apply := range []func(*markdownContractProbe){
			func(p *markdownContractProbe) { p.Highlight = true },
			func(p *markdownContractProbe) {
				p.Highlight, p.Style, p.Options.Transform = true, &markdownContractStyle{Bg: true}, "append"
			},
			func(p *markdownContractProbe) { p.Theme = "dark" },
			func(p *markdownContractProbe) { p.Options.Transform, p.Options.RenderLatex = "width", &no },
		} {
			for _, text := range []string{doc, "short"} {
				probe := markdownContractProbe{Text: text, PaddingX: 1, PaddingY: 1, Theme: "tagged", Ops: ops}
				apply(&probe)
				probes = append(probes, probe)
			}
		}
	}
	streamed := []string{
		doc, "# head\n\ntext with **bold** and `code` and [link](https://x.y)\n\n1. one\n2. two\n   - nested\n\n```\nfenced\n```\n\ndone", "> quote\n> more\n\n---\n\n    indented\n\nend",
		"- [ ] task\n- [x] done\n\n| a | b |\n|:--|--:|\n| 1 | 2 |\n\ntail", "$$x^2$$ and $a+b$ and \\(c\\)\n\n\\[\nd\n\\]", "日本語の **強調** と 🇺🇸 emoji\n\n~~strike~~ <b>html</b>\n\n&amp; entity",
	}
	for _, text := range streamed {
		for _, apply := range []func(*markdownContractProbe){
			func(p *markdownContractProbe) { p.Highlight = true },
			func(p *markdownContractProbe) { p.Theme = "dark" },
		} {
			var ops []markdownContractOp
			for i := 0; i <= len(text); i++ {
				if i < len(text) && !utf8.RuneStart(text[i]) {
					continue
				}
				ops = append(ops, markdownContractOp{Op: "setText", Text: text[:i]}, markdownContractOp{Op: "render", Width: 24})
			}
			probe := markdownContractProbe{Text: "", PaddingX: 1, PaddingY: 0, Theme: "tagged", Ops: ops}
			apply(&probe)
			probes = append(probes, probe)
		}
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
