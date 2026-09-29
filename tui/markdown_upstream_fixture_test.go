package tui

import (
	"bytes"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/tui/termsim"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func mdCaseSetup(t *testing.T) {
	t.Helper()
	previous := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(previous) })
	SetCapabilities(TerminalCapabilities{TrueColor: true})
}
func mdChalk(name string) func(string) string {
	codes := map[string][2]string{"bold": {"\x1b[1m", "\x1b[22m"}, "dim": {"\x1b[2m", "\x1b[22m"}, "italic": {"\x1b[3m", "\x1b[23m"}, "underline": {"\x1b[4m", "\x1b[24m"}, "strike": {"\x1b[9m", "\x1b[29m"}, "blue": {"\x1b[34m", "\x1b[39m"}, "cyan": {"\x1b[36m", "\x1b[39m"}, "yellow": {"\x1b[33m", "\x1b[39m"}, "green": {"\x1b[32m", "\x1b[39m"}, "gray": {"\x1b[90m", "\x1b[39m"}, "magenta": {"\x1b[35m", "\x1b[39m"}}
	pair := codes[name]
	return func(text string) string {
		if text == "" {
			return ""
		}
		return ansiSpan(pair[0], pair[1], text)
	}
}

// .upstream/v0.87.1/packages/tui/test/test-themes.ts:18-33
func mdUpstreamTheme() *MarkdownTheme {
	return &MarkdownTheme{Heading: func(text string) string { return mdChalk("bold")(mdChalk("cyan")(text)) }, Link: mdChalk("blue"), LinkUrl: mdChalk("dim"), Code: mdChalk("yellow"), CodeBlock: mdChalk("green"), CodeBlockBorder: mdChalk("dim"), Quote: mdChalk("italic"), QuoteBorder: mdChalk("dim"), Hr: mdChalk("dim"), ListBullet: mdChalk("cyan"), Bold: mdChalk("bold"), Italic: mdChalk("italic"), Strikethrough: mdChalk("strike"), Underline: mdChalk("underline")}
}
func mdAssert(t *testing.T, value any, condition string) {
	t.Helper()
	ok := false
	switch v := value.(type) {
	case bool:
		ok = v
	case string:
		ok = v != ""
	case int:
		ok = v != 0
	default:
		ok = value != nil
	}
	if !ok {
		t.Fatalf("upstream assertion failed: %s", condition)
	}
}
func mdEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
func mdNotEqual(t *testing.T, got, want any) {
	t.Helper()
	if reflect.DeepEqual(got, want) {
		t.Fatalf("got unexpected %#v", got)
	}
}
func mdLen(value any) int {
	switch v := value.(type) {
	case string:
		return len(utf16.Encode([]rune(v)))
	case []string:
		return len(v)
	default:
		panic("unexpected upstream length operand")
	}
}
func mdReplace(s, pattern, replacement string) string {
	return regexp.MustCompile(pattern).ReplaceAllString(s, replacement)
}
func mdMatch(pattern, s string) bool       { return regexp.MustCompile(pattern).MatchString(s) }
func mdMatches(s, pattern string) []string { return regexp.MustCompile(pattern).FindAllString(s, -1) }
func mdStripSGR(s string) string           { return mdReplace(s, `\x1b\[[0-9;]*m`, "") }
func mdMap(values []string, fn func(string) string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = fn(v)
	}
	return out
}
func mdFilter(values []string, fn func(string) bool) []string {
	var out []string
	for _, v := range values {
		if fn(v) {
			out = append(out, v)
		}
	}
	return out
}
func mdFind(values []string, fn func(string) bool) string {
	for _, v := range values {
		if fn(v) {
			return v
		}
	}
	return ""
}
func mdAt(values []string, index int) string {
	if index < 0 {
		index += len(values)
	}
	if index < 0 || index >= len(values) {
		return ""
	}
	return values[index]
}
func mdSlice(values []string, start int, end ...int) []string {
	if start < 0 {
		start += len(values)
	}
	stop := len(values)
	if len(end) > 0 {
		stop = end[0]
		if stop < 0 {
			stop += len(values)
		}
	}
	start = max(0, min(start, len(values)))
	stop = max(start, min(stop, len(values)))
	return values[start:stop]
}
func mdStringSlice(value string, start, end int) string {
	return string(utf16.Decode(utf16.Encode([]rune(value))[start:end]))
}
func mdPlain(lines []string) []string {
	return mdMap(lines, func(line string) string { return widthx.JSTrimEnd(mdStripSGR(line)) })
}
func mdGrid(component Component, width, height int) (*termsim.Grid, []string) {
	var out bytes.Buffer
	ui := NewWithOutput(&out, width, height)
	ui.Add(component)
	ui.Render()
	grid := termsim.New(height, width)
	grid.Write(out.Bytes())
	return grid, strings.Split(grid.String(), "\n")
}
func mdColumn(line, needle string, after int) int {
	runes := []rune(line)
	if after < 0 || after > len(runes) {
		return -1
	}
	part := string(runes[after:])
	before, _, ok := strings.Cut(part, needle)
	if !ok {
		return -1
	}
	return after + utf8.RuneCountInString(before)
}
func mdLastColumn(line, needle string) int {
	index := strings.LastIndex(line, needle)
	if index < 0 {
		return -1
	}
	return utf8.RuneCountInString(line[:index])
}
func mdRGB(color termsim.Color) int {
	if color.Mode == termsim.ColorTrueColor {
		return int(color.R)<<16 | int(color.G)<<8 | int(color.B)
	}
	return int(color.N)
}

// .upstream/v0.87.1/packages/tui/test/markdown.test.ts:31
func testMDTransformCache(t *testing.T) {
	t.Helper()
	type call struct {
		source string
		width  int
	}
	var calls []call
	md := NewMarkdownWithOptions("source", 2, 0, mdUpstreamTheme(), nil, &MarkdownOptions{Transform: func(source string, width int) string {
		calls = append(calls, call{source, width})
		return source + " " + strconv.Itoa(width)
	}})
	trimmed := func() []string {
		return mdMap(md.Render(60), func(line string) string { return widthx.JSTrim(mdStripSGR(line)) })
	}
	mdEqual(t, mdMap(md.Render(80), func(line string) string { return widthx.JSTrim(mdStripSGR(line)) }), []string{"source 76"})
	md.Render(80)
	mdEqual(t, trimmed(), []string{"source 56"})
	mdEqual(t, calls, []call{{"source", 76}, {"source", 56}})
	md.SetText("updated")
	mdEqual(t, trimmed(), []string{"updated 56"})
	mdEqual(t, calls[len(calls)-1], call{"updated", 56})
	md.Invalidate()
	md.Render(60)
	mdEqual(t, calls[len(calls)-1], call{"updated", 56})
	mdEqual(t, len(calls), 4)
}

// .upstream/v0.87.1/packages/tui/test/markdown.test.ts:472
func testMDTableLinkStyles(t *testing.T) {
	t.Helper()
	source := "| Link | Plain |\n| --- | --- |\n| [**one two three four five six**](https://example.com) | normal text |"
	for _, links := range []bool{true, false} {
		SetCapabilities(TerminalCapabilities{Hyperlinks: links})
		md := NewMarkdownWithOptions(source, 0, 0, mdUpstreamTheme(), nil, nil)
		grid, view := mdGrid(md, 24, 16)
		row := slices.IndexFunc(view, func(line string) bool { return strings.Contains(line, "one") && strings.Contains(line, "norm") })
		if row < 0 {
			t.Fatalf("missing wrapped row: %q", view)
		}
		line := view[row]
		link := mdColumn(line, "one", 0)
		separator := mdColumn(line, "│", link)
		plain := mdColumn(line, "norm", 0)
		if link < 0 || separator <= link || plain <= separator {
			t.Fatalf("invalid cell positions: %q", line)
		}
		mdEqual(t, grid.CellAt(row, link).Style.FG.Mode == termsim.ColorDefault, false)
		mdEqual(t, grid.CellAt(row, separator).Style.FG.Mode == termsim.ColorDefault, true)
		mdEqual(t, grid.CellAt(row, plain).Style.FG.Mode == termsim.ColorDefault, true)
		mdEqual(t, grid.CellAt(row, link).Style.Bold, true)
		mdEqual(t, grid.CellAt(row, separator).Style.Bold, false)
		mdEqual(t, grid.CellAt(row, plain).Style.Bold, false)
		if !links {
			urlRow := slices.IndexFunc(view, func(line string) bool { return strings.Contains(line, "https") })
			if urlRow < 0 {
				t.Fatalf("missing URL row: %q", view)
			}
			urlLine := view[urlRow]
			url := mdColumn(urlLine, "https", 0)
			separator := mdColumn(urlLine, "│", url)
			border := mdLastColumn(urlLine, "│")
			if url < 0 || separator <= url || border <= separator {
				t.Fatalf("invalid URL cells: %q", urlLine)
			}
			mdEqual(t, grid.CellAt(urlRow, url).Style.Dim, true)
			mdEqual(t, grid.CellAt(urlRow, separator).Style.Dim, false)
			mdEqual(t, grid.CellAt(urlRow, border).Style.Dim, false)
		}
	}
}

// .upstream/v0.87.1/packages/tui/test/markdown.test.ts:523
func testMDQuoteTableStyles(t *testing.T) {
	t.Helper()
	theme := mdUpstreamTheme()
	theme.Quote = func(text string) string { return "\x1b[38;2;18;52;86m" + text + "\x1b[39m" }
	theme.Link = func(text string) string { return "\x1b[38;2;129;162;190m" + text + "\x1b[39m" }
	SetCapabilities(TerminalCapabilities{TrueColor: true, Hyperlinks: true})
	source := "> | Link | Plain |\n> | --- | --- |\n> | [one two three four five six](https://example.com) | normal text |"
	md := NewMarkdownWithOptions(source, 0, 0, theme, nil, nil)
	grid, view := mdGrid(md, 28, 10)
	row := slices.IndexFunc(view, func(line string) bool { return strings.Contains(line, "one") && strings.Contains(line, "normal") })
	if row < 0 {
		t.Fatalf("missing quote table row: %q", view)
	}
	line := view[row]
	link := mdColumn(line, "one", 0)
	separator := mdColumn(line, "│", link)
	plain := mdColumn(line, "normal", 0)
	if link < 0 || separator <= link || plain <= separator {
		t.Fatalf("invalid quote cells: %q", line)
	}
	mdNotEqual(t, mdRGB(grid.CellAt(row, link).Style.FG), 0x123456)
	mdEqual(t, mdRGB(grid.CellAt(row, separator).Style.FG), 0x123456)
	mdEqual(t, mdRGB(grid.CellAt(row, plain).Style.FG), 0x123456)
	final := slices.IndexFunc(view, func(line string) bool { return strings.Contains(line, "five six") })
	if final < 0 {
		t.Fatalf("missing final link row: %q", view)
	}
	line = view[final]
	link = mdColumn(line, "five six", 0)
	separator = mdColumn(line, "│", link)
	border := mdLastColumn(line, "│")
	if link < 0 || separator <= link || border <= separator {
		t.Fatalf("invalid final cells: %q", line)
	}
	mdEqual(t, mdRGB(grid.CellAt(final, separator).Style.FG), 0x123456)
	mdEqual(t, mdRGB(grid.CellAt(final, border).Style.FG), 0x123456)
}

// .upstream/v0.87.1/packages/tui/test/markdown.test.ts:1060
func testMDStyledInputLine(t *testing.T) {
	t.Helper()
	md := NewMarkdownWithOptions("This is thinking with `inline code`", 1, 0, mdUpstreamTheme(), &DefaultTextStyle{Color: mdChalk("gray"), Italic: true}, nil)
	count := 0
	component := renderFuncComponent(func(width int) []string {
		lines := md.Render(width)
		count = len(lines)
		return append(slices.Clone(lines), "INPUT")
	})
	grid, _ := mdGrid(component, 80, 6)
	if count <= 0 {
		t.Fatal("no markdown lines")
	}
	mdEqual(t, grid.CellAt(count, 0).Style.Italic, false)
}

// .upstream/v0.87.1/packages/tui/test/markdown.test.ts:1524
func testMDHeadingPadding(t *testing.T) {
	t.Helper()
	md := NewMarkdownWithOptions("# Important distinction from `open()`", 0, 0, mdUpstreamTheme(), nil, nil)
	grid, _ := mdGrid(md, 80, 4)
	lines := md.Render(80)
	if len(lines) == 0 {
		t.Fatal("no heading")
	}
	contentWidth := mdLen(widthx.JSTrimEnd(mdStripSGR(lines[0])))
	if contentWidth <= 0 {
		t.Fatal("no heading content")
	}
	for col := contentWidth; col < 80; col++ {
		mdEqual(t, grid.CellAt(0, col).Style.Underline, false)
	}
}

// .upstream/v0.87.1/packages/tui/test/markdown.test.ts:1733
func testMDStreamingFences(t *testing.T) {
	t.Helper()
	cases := []struct {
		input string
		want  []string
	}{
		{"```ts\nconst x = 1;\n``", []string{"```ts", "  const x = 1;", "```"}},
		{"```md\nnot a closing fence:\n``\n```", []string{"```md", "  not a closing fence:", "  ``", "```"}},
		{"```ts\n``", []string{"```ts", "", "```"}},
		{"````\n```", []string{"```", "", "```"}},
		{"~~~~~\n~~~~", []string{"```", "", "```"}},
		{"```md\nnot a closing fence:\n``\n```\n\nafter", []string{"```md", "  not a closing fence:", "  ``", "```", "", "after"}},
	}
	for _, tt := range cases {
		mdEqual(t, mdPlain(NewMarkdownWithOptions(tt.input, 0, 0, mdUpstreamTheme(), nil, nil).Render(80)), tt.want)
	}
	partial := NewMarkdownWithOptions("```ts\nconst x = 1;\n``", 0, 0, mdUpstreamTheme(), nil, nil)
	complete := NewMarkdownWithOptions("```ts\nconst x = 1;\n```", 0, 0, mdUpstreamTheme(), nil, nil)
	mdEqual(t, len(partial.Render(80)), len(complete.Render(80)))
}
