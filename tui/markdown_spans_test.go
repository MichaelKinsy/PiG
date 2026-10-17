package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"math/rand/v2"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Real Pi 1.1.0 (pi-tui Markdown with the coding agent's markdown theme, width 80, lines joined by "\n"): the sha256 of its output for each input, recorded with extensions/sdk-ts's installed Pi. Inputs use only bold, italic and strikethrough, whose codes do not depend on the active color theme. The nested-emphasis output is the byte stream chalk produces when an inner close code re-opens the outer style, quadratic in the delimiter count.
func TestMarkdownNestedEmphasisMatchesPiBytes(t *testing.T) {
	cases := []struct {
		name, text, sha string
		bytes           int
	}{
		{"stars125", strings.Repeat("*", 125) + "x" + strings.Repeat("*", 125), "913adcd6da4c336e6b1b60d0138240eeeff4f58647983463be5e40517201e92b", 8211},
		{"stars250", strings.Repeat("*", 250) + "x" + strings.Repeat("*", 250), "0306e20d04ea038911931491c1f4a6a9cebf67d53f69c88dc07987e1d895cf96", 32205},
		{"mixed", "**a *b* c**\n*d\r\n~~e~~* **x\ny** ***a\nb***", "c2bd756c355dd178115802caccedd920b0ae814d1b21c31c364d5c9444122658", 503},
		{"carriage returns", "*a\r*\n**\r**b", "95909758ee96fa730ad1ba93600568da1bd449efb3fda1d3bbf1bccb60b5cf26", 323},
		{"newlines", "***a\nb***\n\n_c_\r\n*d*", "b9780ebbf034e15fc34a5cb8c00e8bd0ed9cf01fcc2645caaca534437e3a7ecf", 458},
	}
	for _, c := range cases {
		got := strings.Join(NewMarkdown(c.text).Render(80), "\n")
		sum := sha256.Sum256([]byte(got))
		if hex.EncodeToString(sum[:]) != c.sha {
			t.Errorf("%s: sha256 %x (%d bytes), Pi's %s (%d bytes)", c.name, sum, len(got), c.sha, c.bytes)
		}
	}
}

// The same comparison for the contexts that style text: a thinking block (default color plus italic), a quote and headings, rendered by real Pi 1.1.0 with the coding agent's emphasis functions and fixed colors for the rest, since the active theme's colors are not constant across tests.
func TestMarkdownStyledContextsMatchPiBytes(t *testing.T) {
	color := func(n int) func(string) string {
		return func(s string) string { return "\x1b[38;5;" + strconv.Itoa(n) + "m" + s + "\x1b[39m" }
	}
	theme := GetMarkdownTheme()
	theme.Heading, theme.Quote, theme.QuoteBorder, theme.Code = color(3), color(4), color(5), color(6)
	thinking := &DefaultTextStyle{Color: color(244), Italic: true}
	stars := strings.Repeat("*", 125) + "x" + strings.Repeat("*", 125)
	mixed := "**a *b* c**\n*d\r\n~~e~~* **x\ny** ***a\nb*** `k` *z*"
	cases := []struct {
		name, text string
		style      *DefaultTextStyle
		sha        string
		bytes      int
	}{
		{"thinking stars125", stars, thinking, "04d05829ac618478ca203d2ef13eed62119e68015aaece79776074794f0db3c1", 8240},
		{"thinking mixed", mixed, thinking, "1ae702e58b6deb4515d750c185ded7c6ee3e78cfe962f5b9f9428568aff35e07", 1069},
		{"quote stars125", "> " + stars, nil, "cbfe213cb1869f88a002de548b992f49411a1d273bdac9aeeef30e4efe5f83a8", 8254},
		{"quote mixed", "> " + strings.ReplaceAll(mixed, "\n", "\n> "), nil, "9e517548c0d800dc6869c7232af185545ce6f5aadf8171616decc8319a66fc95", 803},
		{"heading stars125", "# " + stars, nil, "67f716066f0f46447ffff02602bbcd767a4ea592c836e014c42c76313aa7afa3", 8491},
		{"heading3 mixed", "### **a *b* c** ~~e~~ `k` *z*", nil, "dfc7e8411f049a07abdd19f4632e1a5286272eea4bc2bade964bc8503e1e426e", 401},
	}
	for _, c := range cases {
		got := strings.Join(NewMarkdownWithOptions(c.text, 0, 0, &theme, c.style, nil).Render(80), "\n")
		sum := sha256.Sum256([]byte(got))
		if hex.EncodeToString(sum[:]) != c.sha {
			t.Errorf("%s: sha256 %x (%d bytes), Pi's %s (%d bytes)", c.name, sum, len(got), c.sha, c.bytes)
		}
	}
}

// spanTestContexts are the inline contexts production renders with: the default one, a thinking block (default color plus italic), a default text style, a heading and a quote. Each is built as its owner builds it.
func spanTestContexts(source string) map[string]func() (*Markdown, inlineStyleContext) {
	return map[string]func() (*Markdown, inlineStyleContext){
		"default": func() (*Markdown, inlineStyleContext) {
			m := NewMarkdown(source)
			return m, m.defaultInlineStyleContext()
		},
		"thinking": func() (*Markdown, inlineStyleContext) {
			m := NewMarkdown(source)
			m.defaultItalic = true
			m.SetDefaultColor("\x1b[38;5;244m")
			return m, m.defaultInlineStyleContext()
		},
		"color style": func() (*Markdown, inlineStyleContext) {
			m := NewMarkdownWithOptions(source, 0, 0, nil, &DefaultTextStyle{Color: func(s string) string { return "\x1b[38;5;1m" + s + "\x1b[39m" }}, nil)
			return m, m.defaultInlineStyleContext()
		},
		"bold italic style": func() (*Markdown, inlineStyleContext) {
			m := NewMarkdownWithOptions(source, 0, 0, nil, &DefaultTextStyle{Bold: true, Italic: true, Underline: true}, nil)
			return m, m.defaultInlineStyleContext()
		},
		"color without a prefix": func() (*Markdown, inlineStyleContext) {
			m := NewMarkdownWithOptions(source, 0, 0, nil, &DefaultTextStyle{Color: strings.ToUpper}, nil)
			return m, m.defaultInlineStyleContext()
		},
		"heading": func() (*Markdown, inlineStyleContext) {
			m := NewMarkdown(source)
			theme := m.markdownTheme()
			style := func(text string) string { return theme.Heading(theme.Bold(text)) }
			return m, inlineStyleContext{applyText: style, stylePrefix: markdownStylePrefix(style)}
		},
		"quote": func() (*Markdown, inlineStyleContext) {
			m := NewMarkdown(source)
			theme := m.markdownTheme()
			prefix := markdownStylePrefix(func(text string) string { return theme.Quote(theme.Italic(text)) })
			return m, inlineStyleContext{applyText: func(s string) string { return s }, stylePrefix: prefix}
		},
	}
}

// renderInlineTokens computes nested emphasis in one pass; renderInlineTokensSequential applies each level to the whole text below it, as Pi does. Random delimiter soup, including newlines, CRs and fragments of the close codes the levels replace, must render to the same bytes through either, in every context production renders with, whether or not the one-pass path took the input.
func TestStackedInlineRenderingEqualsLevelByLevel(t *testing.T) {
	pieces := []string{"*", "**", "***", "_", "__", "~~", "a", "b c", " ", "\n", "\r", "\r\n", "`", "``", "`x`", "[", "]", "(u)", "[t](u)", "\\", "\x1b[22m", "\x1b[23m", "\x1b[29m", "\x1b[1m", "\x1b[3m", "\x1b[2", "2m", "\x1b", "é", "![](u)", ""}
	random := rand.New(rand.NewPCG(7, 11))
	taken := map[string]int{}
	const rounds = 20000
	for range rounds {
		var sb strings.Builder
		for range 1 + random.IntN(14) {
			sb.WriteString(pieces[random.IntN(len(pieces))])
		}
		source := sb.String()
		for name, build := range spanTestContexts(source) {
			m, style := build()
			tokens := lexMarkedInline(source, m.lexerState())
			want := m.renderInlineTokensSequential(tokens, style)
			if got := m.renderInlineTokens(tokens, style); got != want {
				t.Fatalf("%s, source %q:\none pass: %q\nper level: %q", name, source, got, want)
			}
			if out, ok := m.renderInlineTokensStacked(tokens, style); ok {
				taken[name]++
				if counted := countedSpanLength(m, tokens, style); counted != len(out) {
					t.Fatalf("%s, source %q: the counting pass sized %d bytes for %d bytes of output", name, source, counted, len(out))
				}
			}
		}
	}
	for name := range spanTestContexts("") {
		if taken[name] < rounds/2 {
			t.Errorf("%s: the one-pass path took %d of %d inputs; the comparison is not exercising it", name, taken[name], rounds)
		}
	}
}

// countedSpanLength runs renderInlineTokensStacked's counting pass alone and returns the size it reserves for the output.
func countedSpanLength(m *Markdown, tokens []markedToken, style inlineStyleContext) int {
	theme := m.markdownTheme()
	counted := newSpanWriter(true, style)
	m.writeInlineTokens(counted, tokens, style, &theme)
	counted.pending = 0
	counted.flushCR()
	return counted.length
}

// A theme that is not the stock one must not use the one-pass path: its functions see each level's whole text.
func TestStackedInlineRenderingLeavesOtherThemesAlone(t *testing.T) {
	source := "**a *b* c**"
	theme := GetMarkdownTheme()
	theme.Bold = func(s string) string { return "<" + s + ">" }
	custom := NewMarkdownWithOptions(source, 0, 0, &theme, nil, nil)
	if _, ok := custom.renderInlineTokensStacked(lexMarkedInline(source, custom.lexerState()), custom.defaultInlineStyleContext()); ok {
		t.Fatal("a theme with a custom Bold took the one-pass path")
	}
	if got := strings.Join(NewMarkdownWithOptions("**a *b* c**", 0, 0, nil, &DefaultTextStyle{Color: strings.ToUpper}, nil).Render(80), ""); !strings.Contains(got, "A") || strings.Contains(got, "a") {
		t.Fatalf("default text color not applied: %q", got)
	}
	if got := strings.Join(custom.Render(80), ""); !strings.Contains(got, "<") {
		t.Fatalf("custom Bold not applied: %q", got)
	}
}

// Hostile nesting must cost memory in proportion to its output, not to the cube of the delimiter count (QUALITY-AUDIT-050 item 10: 16 GB for the 8 MB Pi also produces). Allocation is measured as TotalAlloc growth, an exact count of bytes handed out.
func TestNestedEmphasisAllocatesInProportionToItsOutput(t *testing.T) {
	color := func(s string) string { return "\x1b[38;5;244m" + s + "\x1b[39m" }
	contexts := []struct {
		name   string
		prefix string
		style  *DefaultTextStyle
		// copies bounds allocation per byte of output: a quote also decorates, rewrites and wraps each whole line.
		copies uint64
	}{
		{"default", "", nil, 6},
		{"thinking", "", &DefaultTextStyle{Color: color, Italic: true}, 6},
		{"quote", "> ", nil, 10},
		{"heading", "# ", nil, 6},
	}
	for _, context := range contexts {
		t.Run(context.name, func(t *testing.T) {
			render := func(n int) (allocated, output uint64) {
				text := context.prefix + strings.Repeat("*", n) + "x" + strings.Repeat("*", n)
				var before, after runtime.MemStats
				runtime.GC()
				runtime.ReadMemStats(&before)
				lines := NewMarkdownWithOptions(text, 0, 0, nil, context.style, nil).Render(80)
				runtime.ReadMemStats(&after)
				for _, line := range lines {
					output += uint64(len(line))
				}
				return after.TotalAlloc - before.TotalAlloc, output
			}
			render(10)
			small, smallOut := render(500)
			large, largeOut := render(1000)
			if largeOut < 500_000 {
				t.Fatalf("output is only %d bytes; the input no longer nests", largeOut)
			}
			if large > context.copies*largeOut || small > context.copies*smallOut {
				t.Fatalf("allocated %d bytes for %d bytes of output at n=1000, %d for %d at n=500", large, largeOut, small, smallOut)
			}
			if large > 6*small {
				t.Fatalf("allocation grew %dx when n doubled, want about 4x for quadratic output", large/small)
			}
		})
	}
}

// The lexer never yields an emphasis token with empty text or a CR split from its newline across tokens, but the renderer must still treat them as Pi does: an emphasis whose text is empty renders as nothing (markdownDecoration returns "" for empty text), and a CR directly before a newline belongs to it even when a token boundary falls between them.
func TestStackedInlineRenderingHandlesTokenBoundaries(t *testing.T) {
	text := func(s string) markedToken { return markedToken{kind: markedText, text: s, raw: s} }
	wrap := func(kind markedTokenKind, children ...markedToken) markedToken {
		return markedToken{kind: kind, tokens: children}
	}
	br := markedToken{kind: markedBr}
	cases := map[string][]markedToken{
		"empty strong":                                {text("a"), wrap(markedStrong, text("")), text("b")},
		"nested empty":                                {wrap(markedEm, wrap(markedStrong, text("")), wrap(markedDel))},
		"empty inside non-empty":                      {wrap(markedStrong, text("x"), wrap(markedEm, text("")), text("y"))},
		"CR then br":                                  {text("a\r"), br, text("b")},
		"CR then br inside emphasis":                  {wrap(markedStrong, text("a\r"), br, text("b"))},
		"CR then empty then newline":                  {wrap(markedStrong, text("a\r"), wrap(markedEm, text("")), text("\nb"))},
		"CR then span then newline":                   {wrap(markedStrong, text("a\r"), wrap(markedEm, text("c")), text("\nb"))},
		"empty level releasing CR":                    {text("\r"), wrap(markedStrong, text("")), br},
		"lone CR":                                     {wrap(markedStrong, text("\r"))},
		"CR before strong then br":                    {text("x\r"), wrap(markedStrong, text("")), wrap(markedEm, br)},
		"copies of P before and after an empty level": {text("a"), wrap(markedStrong, text("b")), wrap(markedEm, text("")), wrap(markedStrong, text(""), wrap(markedDel)), br, wrap(markedEm, text("c"))},
		"level holding only copies of P":              {wrap(markedStrong, wrap(markedEm, text("x")), wrap(markedStrong, text(""))), text("y")},
		"newline in nested spans":                     {wrap(markedStrong, wrap(markedEm, text("a\nb"), br), wrap(markedDel, text("\r\n")))},
		"CR then CR-newline":                          {text("a\r"), text("\r\nb")},
		"CR then CR-newline in span":                  {wrap(markedStrong, text("a\r"), text("\r\nb"))},
	}
	for name, tokens := range cases {
		for contextName, build := range spanTestContexts("") {
			m, style := build()
			got, ok := m.renderInlineTokensStacked(tokens, style)
			if !ok {
				t.Errorf("%s in %s: the one-pass path did not take plain tokens", name, contextName)
				continue
			}
			if want := m.renderInlineTokensSequential(tokens, style); got != want {
				t.Errorf("%s in %s:\none pass: %q\nper level: %q", name, contextName, got, want)
			}
		}
	}
}

// Contexts whose style the one-pass path cannot reproduce exactly must go through the per-level path, and the result must still be the per-level result: a prefix that is not whole SGR codes, a prefix containing a close code a level rewrites, and styled text ending in the prefix's last code (which the trim would eat together with the copies after it).
func TestStackedInlineRenderingFallsBackForPrefixesItCannotTrack(t *testing.T) {
	source := "*a* b **c** `d` *e\nf*"
	cases := map[string]inlineStyleContext{
		"partial code prefix":   {applyText: func(s string) string { return s }, stylePrefix: "\x1b[3"},
		"prefix with a close":   {applyText: func(s string) string { return "\x1b[23m" + s }, stylePrefix: "\x1b[23m"},
		"styled text ends in P": {applyText: func(s string) string { return s + "\x1b[38;5;1m" }, stylePrefix: "\x1b[38;5;1m"},
	}
	for name, style := range cases {
		m := NewMarkdown(source)
		tokens := lexMarkedInline(source, m.lexerState())
		if _, ok := m.renderInlineTokensStacked(tokens, style); ok {
			t.Errorf("%s: the one-pass path took a context it cannot track", name)
		}
		if got, want := m.renderInlineTokens(tokens, style), m.renderInlineTokensSequential(tokens, style); got != want {
			t.Errorf("%s: %q, per level %q", name, got, want)
		}
	}
}

// The built-in markdown themes share the stock emphasis functions, so each takes the one-pass path: GetMarkdownTheme, the copy the interactive mode adjusts (getMarkdownThemeWithSettings), and MarkdownThemeFor, which extension components use.
func TestStackedInlineRenderingTakesEveryBuiltInTheme(t *testing.T) {
	source := "**a *b* c**"
	indent := "    "
	adjusted := GetMarkdownTheme()
	adjusted.CodeBlockIndent = &indent
	bound := MarkdownThemeFor(ActiveTheme())
	for name, theme := range map[string]*MarkdownTheme{"default": nil, "adjusted copy": &adjusted, "MarkdownThemeFor": &bound} {
		m := NewMarkdownWithOptions(source, 0, 0, theme, nil, nil)
		if _, ok := m.renderInlineTokensStacked(lexMarkedInline(source, m.lexerState()), m.defaultInlineStyleContext()); !ok {
			t.Errorf("%s theme did not take the one-pass path", name)
		}
	}
}

// Text the lexer splits into separate tokens must render as Pi's per-level path does: an image with empty alt text separates a CR from a CR-newline pair, or a partial escape code from the rest of a close code that the enclosing level replaces in its whole text. A theme whose code style adds no color leaves a partial code at a code span's edge.
func TestStackedInlineRenderingKeepsLexedTokenBoundaries(t *testing.T) {
	plainCode := GetMarkdownTheme()
	plainCode.Code = func(s string) string { return s }
	for _, c := range []struct {
		source string
		theme  *MarkdownTheme
	}{
		{"a\r![](u)\r\nb", nil},
		{"**a\r![](u)\r\nb**", nil},
		{"**x\x1b[2![](u)2m**", nil},
		{"*x\x1b[23![](u)m*", nil},
		{"**`\x1b[2`2m**", &plainCode},
		{"**`\x1b[22`m**", &plainCode},
	} {
		m := NewMarkdownWithOptions(c.source, 0, 0, c.theme, nil, nil)
		tokens := lexMarkedInline(c.source, m.lexerState())
		style := m.defaultInlineStyleContext()
		if got, want := m.renderInlineTokens(tokens, style), m.renderInlineTokensSequential(tokens, style); got != want {
			t.Errorf("source %q:\none pass: %q\nper level: %q", c.source, got, want)
		}
	}
}
