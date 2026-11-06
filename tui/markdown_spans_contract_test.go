package tui

import (
	"strings"
	"testing"
)

// themeCalls are the calls a counting theme sees.
type themeCalls struct{ code, link, url int }

// markdown.ts renderInlineTokens calls the theme function of a code span, a link and its URL once per token. The one-pass span writer measures a list of
// tokens before it writes them and must reuse what the first pass computed, so a theme whose emphasis functions are the stock ones but whose code and link
// functions count their calls sees the same calls as the level-by-level path.
func TestStackedInlineRenderingCallsThemeFunctionsOncePerToken(t *testing.T) {
	const source = "**a `c1` *b `c2`* [l](http://u.v)** ~~`c3`~~ <http://a.b>"
	for name, build := range spanTestContexts(source) {
		render := func(stacked bool) (calls themeCalls, out string) {
			m, style := build()
			theme := GetMarkdownTheme()
			theme.Code = func(s string) string { calls.code++; return "[" + s + "]" }
			theme.Link = func(s string) string { calls.link++; return "{" + s + "}" }
			theme.LinkUrl = func(s string) string { calls.url++; return "<" + s + ">" }
			m.theme = &theme
			tokens := lexMarkedInline(source, m.lexerState())
			if !stacked {
				out = m.renderInlineTokensSequential(tokens, style)
				return calls, out
			}
			out, ok := m.renderInlineTokensStacked(tokens, style)
			if !ok {
				t.Errorf("%s: the one-pass path did not take a theme with stock emphasis functions", name)
			}
			return calls, out
		}
		want, wantOut := render(false)
		if want.code != 3 || want.link != 2 {
			t.Fatalf("%s: the per-level path called Code %d times for 3 code spans and Link %d times for 2 links", name, want.code, want.link)
		}
		if got, gotOut := render(true); got != want || gotOut != wantOut {
			t.Errorf("%s: one pass called Code, Link, LinkUrl %+v and rendered %q; per level %+v and %q", name, got, gotOut, want, wantOut)
		}
	}
}

// markdownForeground leaves text alone when the theme token has no color, and closes a color it opens with the foreground reset.
func TestMarkdownForegroundWithoutAColorLeavesTextAlone(t *testing.T) {
	if got := markdownForeground("", "x"); got != "x" {
		t.Errorf("markdownForeground with no color = %q, want %q", got, "x")
	}
	if got := markdownForeground("\x1b[31m", "x"); got != "\x1b[31mx\x1b[39m" {
		t.Errorf("markdownForeground = %q", got)
	}
}

// markdown.ts getStylePrefix is the text before the sentinel the style function was given, or "" when the style drops it.
func TestMarkdownStylePrefixIsEmptyWhenTheStyleDropsTheText(t *testing.T) {
	for name, c := range map[string]struct {
		style func(string) string
		want  string
	}{
		"drops the text":     {func(string) string { return "\x1b[31m" }, ""},
		"keeps the text":     {func(s string) string { return "\x1b[31m" + s + "\x1b[39m" }, "\x1b[31m"},
		"text first":         {func(s string) string { return s + "\x1b[39m" }, ""},
		"constant":           {func(string) string { return "plain" }, ""},
		"empty":              {func(string) string { return "" }, ""},
		"nested decorations": {func(s string) string { return "\x1b[1m\x1b[3m" + s + "\x1b[23m\x1b[22m" }, "\x1b[1m\x1b[3m"},
	} {
		if got := markdownStylePrefix(c.style); got != c.want {
			t.Errorf("%s: markdownStylePrefix = %q, want %q", name, got, c.want)
		}
	}
}

// onlyCompleteSGR is true exactly when every ESC of the string starts a complete `ESC [ digits/semicolons m` code.
func TestOnlyCompleteSGRAcceptsOnlyCompleteCodes(t *testing.T) {
	for s, want := range map[string]bool{
		"":                         true,
		"plain":                    true,
		"\x1b[0m":                  true,
		"\x1b[9m":                  true,
		"\x1b[m":                   true,
		"\x1b[1;22m":               true,
		"\x1b[38;2;103;113;118m":   true,
		"a\x1b[1mb":                true,
		"\x1b[1m\x1b[22m":          true,
		"\x1b[1mabc\x1b[23mdef":    true,
		"\x1b":                     false,
		"\x1b[":                    false,
		"\x1b[1":                   false,
		"\x1b[1;":                  false,
		"\x1bx":                    false,
		"\x1bx1m":                  false,
		"\x1b[x":                   false,
		"\x1b[xm":                  false,
		"\x1b[1x":                  false,
		"\x1b[1m\x1b":              false,
		"\x1b[1m\x1b[":             false,
		"\x1b[1mabc\x1b[2":         false,
		"\x1b[1\x1b[22m":           false,
		"\x1b]8;;http://u.v\x07x":  false,
		"\x1b[1m\x1b]8;;\x07":      false,
		"\x1b[1:2m":                false,
		"\x1b[\x1b[1m":             false,
		"text \x1b[12;34;56m more": true,
	} {
		if got := onlyCompleteSGR(s); got != want {
			t.Errorf("onlyCompleteSGR(%q) = %t, want %t", s, got, want)
		}
	}
}

// The one-pass path takes complete escape codes of the theme's own output and of text outside every level, however many levels closed before them.
func TestStackedInlineRenderingTakesCodesOutsideEveryLevel(t *testing.T) {
	for _, source := range []string{
		"**a** \x1b[1mb",
		"*a* **b** \x1b[3mc\x1b[0m",
		"**a *b* c** `k` \x1b[1mx",
		"~~**a**~~\x1b[9mb\x1b[29m",
		"**a `\x1b[1mk` b**",
		"**a [l](u) b** \x1b[0m",
	} {
		for name, build := range spanTestContexts(source) {
			m, style := build()
			if name != "default" && name != "heading" && name != "quote" {
				continue
			}
			tokens := lexMarkedInline(source, m.lexerState())
			got, ok := m.renderInlineTokensStacked(tokens, style)
			if !ok {
				t.Errorf("%s, source %q: the one-pass path declined a context it takes", name, source)
				continue
			}
			if want := m.renderInlineTokensSequential(tokens, style); got != want {
				t.Errorf("%s, source %q:\none pass: %q\nper level: %q", name, source, got, want)
			}
		}
	}
}

// TestMarkdownInlineLatexOffMatchesPi renders inline and display math inside emphasis, links and headings with renderLatex off and on, in the stock themes (whose emphasis the one-pass writer renders) and the tagged one, with and without a text style.
func TestMarkdownInlineLatexOffMatchesPi(t *testing.T) {
	off, on := false, true
	// pi-tui's first theme initialisation of the oracle process runs before it sets the terminal capabilities, so a stock theme first in the batch would render in 256 colors.
	probes := []markdownContractProbe{{Text: "x", Theme: "tagged", Ops: markdownContractRenderOps(5)}}
	for _, doc := range []string{"$x^2$", "**$x^2$**", "*a $\\alpha$ b* **$y_1$**", "~~$a+b$~~ and `$c$`", "[$x$](u) **[$y$](v)**", "# $x$ **$y$**", "> *$x$*", "- **$x$** $y$", "$$\nz\n$$", "**a** \\(x\\) \\[y\\]", "$a$$b$ **$c$$d$**"} {
		for _, latex := range []*bool{&off, &on, nil} {
			for _, apply := range []func(*markdownContractProbe){
				func(p *markdownContractProbe) { p.Theme = "dark" },
				func(p *markdownContractProbe) { p.Theme = "light" },
				func(p *markdownContractProbe) {},
				func(p *markdownContractProbe) {
					p.Theme, p.Style = "dark", &markdownContractStyle{Color: true, Italic: true}
				},
			} {
				probe := markdownContractProbe{Text: doc, Theme: "tagged", Options: markdownContractOptions{RenderLatex: latex}, Ops: markdownContractRenderOps(8, 60)}
				apply(&probe)
				probes = append(probes, probe)
			}
		}
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// TestMarkdownTextStyleCombinationsMatchPi renders emphasis, code, links, quotes, lists and headings under every combination of the five text style flags (color, bold,
// italic, strikethrough, underline) in the stock dark theme, so each flag is the only one set in some probe.
func TestMarkdownTextStyleCombinationsMatchPi(t *testing.T) {
	// pi-tui's first theme initialisation of the oracle process runs before it sets the terminal capabilities, so a stock theme first in the batch would render in 256 colors.
	probes := []markdownContractProbe{{Text: "x", Theme: "tagged", Ops: markdownContractRenderOps(5)}}
	for _, doc := range []string{"plain **b** *i* ~~s~~ `c` [l](u)", "**a *b ~~c~~ d* e**\nline two", "> quote **b**\n> *i*", "- item **b**\n- [ ] task *i*", "### head **b**", "<b>x</b>", "a\r\nb"} {
		for flags := range 32 {
			style := markdownContractStyle{Color: flags&1 != 0, Bold: flags&2 != 0, Italic: flags&4 != 0, Strikethrough: flags&8 != 0, Underline: flags&16 != 0}
			probes = append(probes, markdownContractProbe{Text: doc, Theme: "dark", Style: &style, Ops: markdownContractRenderOps(10, 60)})
		}
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// SetDefaultColor and the default italic of an assistant thinking block style text as a default text style whose color opens with the same code does: through every container that renders its content again (list items, quotes), HTML blocks and display math included.
func TestMarkdownDefaultColorEqualsAColorTextStyle(t *testing.T) {
	const open = "\x1b[38;5;244m"
	docs := []string{
		"plain **b** `c`", "- <div>x</div>", "- a\n  <div>y</div>", "> <div>z</div>", "- > <b>q</b>", "1. $$\n   m\n   $$", "- [ ] t <i>u</i>", "<div>top</div>", "$$\nm\n$$", "- a\n  - b <u>c</u>\n\n  d", "> - <p>x</p>\n> y",
		"# h <i>j</i>", "text <br> more\n\n<hr>", "- **a**\n\n  <p>b</p>\n- c",
	}
	for _, italic := range []bool{false, true} {
		for _, doc := range docs {
			for _, width := range []int{6, 40} {
				viaSetter := NewMarkdown(doc)
				viaSetter.SetDefaultColor(open)
				viaSetter.defaultItalic = italic
				viaStyle := NewMarkdownWithOptions(doc, 0, 0, nil, &DefaultTextStyle{Color: func(s string) string { return open + s + FgClose(open) }, Italic: italic}, nil)
				got, want := strings.Join(viaSetter.Render(width), "\n"), strings.Join(viaStyle.Render(width), "\n")
				if got != want {
					t.Errorf("italic %t, width %d, %q:\nSetDefaultColor: %q\ntext style:      %q", italic, width, doc, got, want)
				}
			}
		}
	}
}

// markdown.ts renderTable returns no lines for a table without a header cell.
func TestMarkdownTableWithoutHeaderCellsRendersNothing(t *testing.T) {
	for _, width := range []int{0, 1, 5, 80} {
		if rows := NewMarkdown("").renderMarkdownTable(nil, &markedBlock{kind: "table"}, width, nil); len(rows) != 0 {
			t.Errorf("width %d: %q", width, rows)
		}
	}
}

// TestMarkdownBackslashEscapesInEmphasisMatchPi renders escapes inside emphasis, links and plain text with preserveBackslashEscapes on and off, in the stock themes (whose emphasis the one-pass writer renders), the tagged theme and under a text style.
func TestMarkdownBackslashEscapesInEmphasisMatchPi(t *testing.T) {
	probes := []markdownContractProbe{{Text: "x", Theme: "tagged", Ops: markdownContractRenderOps(5)}}
	for _, doc := range []string{"\\*a\\*", "**\\*b\\***", "*a \\_ b* ~~\\~c~~", "[\\[l\\]](u) **\\#**", "a\\\\b **c\\\\d**", "\\<x\\> **\\&**"} {
		for _, preserve := range []bool{false, true} {
			for _, apply := range []func(*markdownContractProbe){
				func(p *markdownContractProbe) { p.Theme = "dark" },
				func(p *markdownContractProbe) {},
				func(p *markdownContractProbe) {
					p.Theme, p.Style = "light", &markdownContractStyle{Color: true, Italic: true}
				},
			} {
				probe := markdownContractProbe{Text: doc, Theme: "tagged", Options: markdownContractOptions{PreserveBackslashEscapes: preserve}, Ops: markdownContractRenderOps(8, 60)}
				apply(&probe)
				probes = append(probes, probe)
			}
		}
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
