package tui

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The memos and the resume point of a Markdown decide how much a streamed render computes, never its rows. The tests below count the work (the inline texts rendered
// and the entries a render holds) and compare the rows with a new component's, so a memo that is dropped, kept too long or keyed on too little fails one of them.

func countingInlineRenders(t *testing.T) *int {
	t.Helper()
	renders := 0
	previous := renderInline
	t.Cleanup(func() { renderInline = previous })
	renderInline = func(m *Markdown, s string, style inlineStyleContext) string {
		renders++
		return previous(m, s, style)
	}
	return &renders
}

func uniqueParagraphs(count int, format string) string {
	paragraphs := make([]string, count)
	for i := range paragraphs {
		paragraphs[i] = strings.ReplaceAll(format, "N", strconv.Itoa(i))
	}
	return strings.Join(paragraphs, "\n\n")
}

// A render of a text that grew resumes from the second-last point, so it renders the inline text of the last blocks only and holds only their renderings; a render of a
// text that changed before the point, or at another width, renders every block again.
func TestMarkdownStreamedRenderResumesFromTheSecondLastPoint(t *testing.T) {
	pauseGC(t) // the memos are inspected between renders, as a WeakRef target is kept until its job ends
	mdCaseSetup(t)
	renders := countingInlineRenders(t)
	text := uniqueParagraphs(31, "Paragraph N with **bold** and `code`.")
	m := NewMarkdown(text)
	m.Render(60)
	if len(memosOf(m).inlines) != 31 || memosOf(m).resume == nil || len(memosOf(m).resume.points) != 29 {
		t.Fatalf("first render holds %d inline renderings and %v points, want 31 and 29", len(memosOf(m).inlines), memosOf(m).resume)
	}
	for i, p := range memosOf(m).resume.points {
		if p.line != 2*(i+1) {
			t.Fatalf("point %d is on line %d, want %d", i, p.line, 2*(i+1))
		}
	}
	*renders = 0
	m.SetText(text + " more")
	got := m.Render(60)
	if *renders != 1 || len(memosOf(m).inlines) != 3 || len(memosOf(m).resume.points) != 29 {
		t.Fatalf("streamed render rendered %d inline texts, holds %d renderings and %d points; want 1, 3 (the last three paragraphs) and 29", *renders, len(memosOf(m).inlines), len(memosOf(m).resume.points))
	}
	if want := NewMarkdown(text + " more").Render(60); !slices.Equal(got, want) {
		t.Fatalf("resumed rows differ from a new component's:\n got %q\nwant %q", got, want)
	}
	m.SetText("changed first\n\n" + text[strings.Index(text, "\n\n")+2:] + " more")
	m.Render(60)
	if len(memosOf(m).inlines) != 31 {
		t.Fatalf("a change before the point holds %d renderings, want 31 (every block rendered again)", len(memosOf(m).inlines))
	}
	m.Render(50)
	if len(memosOf(m).inlines) != 31 {
		t.Fatalf("a render at another width holds %d renderings, want 31", len(memosOf(m).inlines))
	}
}

// The points a render records are the starts of the blocks that follow a blank line, up to the first line that may start a LaTeX block, and never the last line.
func TestMarkdownResumePointsOfCraftedDocuments(t *testing.T) {
	pauseGC(t) // the memos are inspected between renders, as a WeakRef target is kept until its job ends
	mdCaseSetup(t)
	for _, tc := range []struct {
		name, text string
		lines      []int
	}{
		{"leading blank line", "\nfirst\n\nsecond\n\nthird", []int{1, 3}},
		{"leading blank lines", "\n\n\nfirst\n\nsecond\n\nthird\n\nfourth", []int{3, 5, 7}},
		{"last line is no point", "a\n\nb\n\nc", []int{2}},
		{"a final newline makes the last block a point", "a\n\nb\n\nc\n", []int{2, 4}},
		{"point at the barrier", "a\n\nb\n\n$$ x\n\nc\n\nd", []int{2, 4}},
		{"no point after the barrier", "a\n\nb\n\n$$ x\n\nc\n\nd\n\ne", []int{2, 4}},
		{"no point before the first block", "a\n\nb", []int{}},
		{"blank line then text then blank", "a\n \nb\n\nc\n\nd", []int{2, 4}},
	} {
		m := NewMarkdown(tc.text)
		m.Render(40)
		got := []int{}
		if memosOf(m).resume == nil {
			t.Errorf("%s: no resume state", tc.name)
			continue
		}
		for _, p := range memosOf(m).resume.points {
			got = append(got, p.line)
			if p.rawOff != lineStartOffset(tc.text, p.line) {
				t.Errorf("%s: point on line %d is at byte %d, the line starts at %d", tc.name, p.line, p.rawOff, lineStartOffset(tc.text, p.line))
			}
		}
		if !slices.Equal(got, tc.lines) {
			t.Errorf("%s: points on lines %v, want %v", tc.name, got, tc.lines)
		}
	}
}

func lineStartOffset(text string, line int) int {
	offset := 0
	for range line {
		offset += strings.IndexByte(text[offset:], '\n') + 1
	}
	return offset
}

// A component whose styles are not tui's own keeps no resume point, also when it kept one before the styles changed.
func TestMarkdownKeepsNoResumePointForAnImpureTheme(t *testing.T) {
	pauseGC(t) // the memos are inspected between renders, as a WeakRef target is kept until its job ends
	mdCaseSetup(t)
	text := uniqueParagraphs(6, "Paragraph N with **bold**.")
	fresh := NewMarkdownWithOptions(text, 0, 0, opaqueMarkdownTheme(), nil, nil)
	fresh.Render(40)
	if memosOf(fresh).resume != nil || len(memosOf(fresh).inlines) != 0 {
		t.Fatalf("a new component with a foreign theme keeps %v and %d inline renderings", memosOf(fresh).resume, len(memosOf(fresh).inlines))
	}
	m := NewMarkdown(text)
	m.Render(40)
	if memosOf(m).resume == nil {
		t.Fatal("the pure component keeps no resume point, so the test proves nothing")
	}
	m.theme = opaqueMarkdownTheme()
	m.SetText(text + " more")
	got := m.Render(40)
	if memosOf(m).resume != nil || len(memosOf(m).inlines) != 0 {
		t.Fatalf("after the theme became foreign the component keeps %v and %d inline renderings", memosOf(m).resume, len(memosOf(m).inlines))
	}
	if want := NewMarkdownWithOptions(text+" more", 0, 0, m.theme, nil, nil).Render(40); !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	background := NewMarkdownWithOptions(text, 0, 0, nil, &DefaultTextStyle{BgColor: func(s string) string { return s }}, nil)
	background.Render(40)
	if memosOf(background).resume != nil {
		t.Fatal("a component with a background function keeps a resume point")
	}
}

// A foreign theme renders every occurrence of a text through its style functions: the document keeps no rendering for a text it rendered before.
func TestMarkdownForeignThemeRendersRepeatedTextEachTime(t *testing.T) {
	mdCaseSetup(t)
	calls := 0
	theme := GetMarkdownTheme()
	theme.Bold = func(s string) string { calls++; return s }
	m := NewMarkdownWithOptions("**a**\n\n**a**\n\n**a**", 0, 0, &theme, nil, nil)
	m.Render(40)
	if calls != 3 {
		t.Fatalf("Bold called %d times for three equal paragraphs, want 3", calls)
	}
}

// Text with a "[" renders the same without link definitions whatever follows, so a streamed frame renders only the text that changed.
func TestMarkdownBracketTextIsMemoizedWithoutDefinitions(t *testing.T) {
	mdCaseSetup(t)
	renders := countingInlineRenders(t)
	text := uniqueParagraphs(31, "Paragraph N with [x] and [text](http://a.b) and **bold**.")
	m := NewMarkdown(text)
	m.Render(60)
	*renders = 0
	m.SetText(text + " more")
	m.Render(60)
	if *renders != 1 {
		t.Fatalf("streamed render rendered %d inline texts, want 1", *renders)
	}
}

// A finished text keeps the memos for the render of a delta that was not drawn yet, and drops them after it.
func TestMarkdownFinishStreamingAfterAnUndrawnDelta(t *testing.T) {
	pauseGC(t) // the memos are inspected between renders, as a WeakRef target is kept until its job ends
	mdCaseSetup(t)
	renders := countingInlineRenders(t)
	text := uniqueParagraphs(31, "Paragraph N with **bold**.")
	m := NewMarkdown(text)
	m.Render(60)
	m.SetText(text + " more")
	m.FinishStreaming()
	*renders = 0
	m.Render(60)
	if *renders != 1 {
		t.Fatalf("the render of the last delta rendered %d inline texts, want 1", *renders)
	}
	if memosOf(m).resume != nil || len(memosOf(m).inlines) != 0 || len(memosOf(m).highlights) != 0 || len(memosOf(m).wraps) != 0 {
		t.Fatal("the finished component keeps memos after its last render")
	}
	// Content is a field: a text changed through it bumps no revision, and is a text not drawn since its last change all the same.
	direct := NewMarkdown(text)
	direct.Render(60)
	direct.Content = text + " more"
	direct.FinishStreaming()
	*renders = 0
	direct.Render(60)
	if *renders != 1 {
		t.Fatalf("the render of a text changed through Content rendered %d inline texts, want 1", *renders)
	}
	if m := finishedMarkdown(NewMarkdown("x **b**")); !m.finished {
		t.Fatal("finishedMarkdown does not finish the text")
	}
}

// A default text style without a color function, and the default color, keep the inline renderings.
func TestMarkdownDefaultStyleWithoutColorKeepsInlineRenderings(t *testing.T) {
	mdCaseSetup(t)
	renders := countingInlineRenders(t)
	text := uniqueParagraphs(31, "Paragraph N with **bold**.")
	for name, build := range map[string]func() *Markdown{
		"style":     func() *Markdown { return NewMarkdownWithOptions(text, 0, 0, nil, &DefaultTextStyle{Bold: true}, nil) },
		"color set": func() *Markdown { m := NewMarkdown(text); m.SetDefaultColor("\x1b[31m"); return m },
		"italic":    func() *Markdown { m := NewMarkdown(text); m.defaultItalic = true; return m },
	} {
		m := build()
		m.Render(60)
		*renders = 0
		m.SetText(text + " more")
		m.Render(60)
		if *renders != 1 {
			t.Errorf("%s: streamed render rendered %d inline texts, want 1", name, *renders)
		}
	}
}

// The rendering is a function of the style functions the theme holds when it renders: a theme struct that the caller changes between renders is read again.
func TestMarkdownThemeStyleChangedBetweenRendersIsReadAgain(t *testing.T) {
	mdCaseSetup(t)
	theme := GetMarkdownTheme()
	text := "first **bold** paragraph\n\nsecond *em* paragraph\n\nthird"
	m := NewMarkdownWithOptions(text, 0, 0, &theme, nil, nil)
	m.Render(60)
	other := GetMarkdownTheme()
	theme.Bold, theme.Italic = other.Underline, other.Strikethrough
	m.SetText(text + " more")
	got := m.Render(60)
	if want := NewMarkdownWithOptions(text+" more", 0, 0, &theme, nil, nil).Render(60); !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMarkdownSurfaceLive(t *testing.T) {
	for _, tc := range []struct {
		state bool
		async bool
	}{{false, false}, {true, false}, {false, true}, {true, true}} {
		m := NewMarkdown("x")
		if tc.state {
			m.TransformState = func() string { return "" }
		}
		if tc.async {
			m.AsyncTransform = &AsyncMarkdownTransform{}
		}
		if got := m.SurfaceLive(); got != (tc.state || tc.async) {
			t.Errorf("TransformState %v, AsyncTransform %v: SurfaceLive %v", tc.state, tc.async, got)
		}
	}
}

// SetDefaultColor repaints a drawn component, replaces an earlier color, and an empty color suppresses the text style's color function.
func TestMarkdownSetDefaultColorRepaints(t *testing.T) {
	mdCaseSetup(t)
	red, green := "\x1b[31m", "\x1b[32m"
	fresh := func(color string, set bool) []string {
		m := NewMarkdown("some text")
		if set {
			m.SetDefaultColor(color)
		}
		return m.Render(40)
	}
	m := NewMarkdown("some text")
	plain := m.Render(40)
	m.SetDefaultColor(red)
	if got := m.Render(40); !slices.Equal(got, fresh(red, true)) || slices.Equal(got, plain) {
		t.Fatalf("after SetDefaultColor(red): %q", got)
	}
	m.SetDefaultColor(green)
	if got := m.Render(40); !slices.Equal(got, fresh(green, true)) {
		t.Fatalf("after SetDefaultColor(green): %q, want %q", got, fresh(green, true))
	}
	m.SetDefaultColor(green)
	m.SetDefaultColor("")
	if got := m.Render(40); !slices.Equal(got, fresh("", true)) {
		t.Fatalf("after SetDefaultColor(\"\"): %q, want %q", got, fresh("", true))
	}
	styled := NewMarkdownWithOptions("some text", 0, 0, nil, &DefaultTextStyle{Color: func(s string) string { return "<c>" + s }}, nil)
	if got := styled.Render(40); !strings.Contains(got[0], "<c>") {
		t.Fatalf("the text style's color is not applied: %q", got)
	}
	styled.SetDefaultColor("")
	if got := styled.Render(40); strings.Contains(got[0], "<c>") || strings.TrimRight(got[0], " ") != "some text" {
		t.Fatalf("SetDefaultColor(\"\") keeps the text style's color: %q", got)
	}
	// An HTML block is styled with the default style as a whole (applyDefaultStyle): an empty color adds no codes.
	html := NewMarkdown("<div>x</div>")
	html.SetDefaultColor("")
	if got := html.Render(40); strings.TrimRight(got[0], " ") != "<div>x</div>" {
		t.Fatalf("an HTML block with the empty default color: %q", got)
	}
	html.SetDefaultColor(red)
	if got := html.Render(40); strings.TrimRight(got[0], " ") != red+"<div>x</div>"+FgClose(red) {
		t.Fatalf("an HTML block with the default color: %q", got)
	}
}

// After a render the component keeps no pointer to the lexer state it returned to the pool, so a later inline rendering outside Render starts from a clean state.
func TestMarkdownInlineMarkdownAfterRenderStartsClean(t *testing.T) {
	mdCaseSetup(t)
	m := NewMarkdown("<a href=\"x\">open")
	m.Render(40)
	if m.inlineState != nil {
		t.Fatal("the component keeps the lexer state it returned to the pool")
	}
	inline := NewMarkdown("")
	want := inline.renderInlineMarkdown("go http://a.b.c now", inline.defaultInlineStyleContext())
	if got := m.renderInlineMarkdown("go http://a.b.c now", m.defaultInlineStyleContext()); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A negative padding renders as none, with and without a background function.
func TestMarkdownNegativePaddingYRendersAsNone(t *testing.T) {
	mdCaseSetup(t)
	for _, style := range []*DefaultTextStyle{nil, {BgColor: func(s string) string { return "<bg>" + s }}} {
		got := NewMarkdownWithOptions("text", 0, -1, nil, style, nil).Render(10)
		want := NewMarkdownWithOptions("text", 0, 0, nil, style, nil).Render(10)
		if !slices.Equal(got, want) {
			t.Errorf("style %v: got %q, want %q", style != nil, got, want)
		}
		if got := NewMarkdownWithOptions("text", 0, 1, nil, style, nil).Render(10); len(got) != len(want)+2 {
			t.Errorf("style %v: paddingY 1 renders %d rows, want %d", style != nil, len(got), len(want)+2)
		}
	}
}
