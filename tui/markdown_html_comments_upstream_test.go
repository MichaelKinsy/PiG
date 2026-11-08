package tui

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// Ports the "HTML comments" tests of packages/tui/test/markdown.test.ts at Pi main 54280c4ff (fix(tui): hide HTML comments in
// Markdown rendering, #11755, fixing #11710), which is after the pinned v1.5.0.

// mdHTMLCommentPlain is renderPlain of the test: the rows at width 60 without styling and without trailing spaces.
func mdHTMLCommentPlain(t *testing.T, text string) []string {
	t.Helper()
	mdCaseSetup(t)
	return mdPlain(NewMarkdownWithOptions(text, 0, 0, mdUpstreamTheme(), nil, nil).Render(60))
}

func mdHTMLCommentEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %q, want %q", got, want)
	}
}

func TestUpstreamMarkdownHTMLComments(t *testing.T) {
	t.Run("hides block and inline comments without extra spacing", func(t *testing.T) {
		mdHTMLCommentEqual(t,
			mdHTMLCommentPlain(t, "## [1.0.0]\n\n<!-- release-notes:start -->\nFixed a bug.\n\nSee <!-- todo: link --> the docs."),
			mdHTMLCommentPlain(t, "## [1.0.0]\n\nFixed a bug.\n\nSee the docs."))
	})

	t.Run("drops one blank line around comment-only blocks", func(t *testing.T) {
		mdHTMLCommentEqual(t,
			mdHTMLCommentPlain(t, "<!-- start -->\n\n## [1.0.0]\n\n<!-- a -->\n\n- item\n\n<!-- b -->\n\n## [0.9.0]\n\n<!-- end -->"),
			mdHTMLCommentPlain(t, "## [1.0.0]\n\n- item\n\n## [0.9.0]"))
	})

	t.Run("hides multi-line and unclosed comments", func(t *testing.T) {
		mdHTMLCommentEqual(t, mdHTMLCommentPlain(t, "<!--\nmulti\nline\n-->\ntext"), mdHTMLCommentPlain(t, "text"))
		mdHTMLCommentEqual(t, mdHTMLCommentPlain(t, "text\n\n<!-- streaming"), mdHTMLCommentPlain(t, "text"))
	})

	t.Run("hides comments in blockquotes and list items", func(t *testing.T) {
		mdHTMLCommentEqual(t,
			mdHTMLCommentPlain(t, "> q\n>\n> <!-- c -->\n>\n> r\n\n- a\n\n  <!-- c -->\n\n  b\n- <!-- c --> item"),
			mdHTMLCommentPlain(t, "> q\n>\n> r\n\n- a\n\n  b\n- item"))
	})

	t.Run("keeps other HTML and the text after a comment in a block", func(t *testing.T) {
		mdHTMLCommentEqual(t, mdHTMLCommentPlain(t, "with <tag> markers"), []string{"with <tag> markers"})
		mdHTMLCommentEqual(t, mdHTMLCommentPlain(t, "<!-- c --> trailing text"), []string{"trailing text"})
		mdHTMLCommentEqual(t, mdHTMLCommentPlain(t, "<div>\n<!-- c -->a</div>"), []string{"<div>", "a</div>"})
	})
}

// markdownHTMLCommentDocs are documents with HTML comments in every position the renderer treats separately: block and inline,
// at the start and end of the text and of a block, between and inside blocks, in quotes, lists, headings, tables, emphasis and
// links, unclosed, and where marked does not read a comment (code).
func markdownHTMLCommentDocs() []string {
	return []string{
		// Pi's tests.
		"## [1.0.0]\n\n<!-- release-notes:start -->\nFixed a bug.\n\nSee <!-- todo: link --> the docs.",
		"<!-- start -->\n\n## [1.0.0]\n\n<!-- a -->\n\n- item\n\n<!-- b -->\n\n## [0.9.0]\n\n<!-- end -->",
		"<!--\nmulti\nline\n-->\ntext", "text\n\n<!-- streaming",
		"> q\n>\n> <!-- c -->\n>\n> r\n\n- a\n\n  <!-- c -->\n\n  b\n- <!-- c --> item",
		"with <tag> markers", "<!-- c --> trailing text", "<div>\n<!-- c -->a</div>",
		// Block position and blank lines.
		"<!-- c -->", "<!-- c -->\n", "\n<!-- c -->\n", "<!-- c -->\n\n", "\n\n<!-- c -->", "a\n\n<!-- c -->", "a\n\n<!-- c -->\n\n", "<!-- c -->\n\na",
		"a\n<!-- c -->\nb", "a\n<!-- c -->\n\nb", "a\n\n<!-- c -->\nb", "# h\n<!-- c -->\ntext", "# h\n\n<!-- c -->\n\ntext", "---\n<!-- c -->\ntext",
		"a\n\n<!-- a -->\n\n<!-- b -->\n\nc", "<!-- a -->\n\n<!-- b -->", "<!-- a --><!-- b -->", "<!-- a -->\n<!-- b -->\ntext", "a\n\n\n\n<!-- c -->\n\n\n\nb",
		"<!-- a -->\n\n\n\nb", "a\n\n<!-- c -->\n\n\n\n", "<!-->\ntext", "<!--->\ntext", "<!---->\ntext", "<!-- a -- b -->\ntext", "<!-- a --\nb -->\ntext",
		"<!-- c -->text\n\nafter", "  <!-- c -->\n\n    <!-- c -->", "   <!-- c -->  \n\ntext", "<!-- a -->x<!-- b -->", "<!-- a --> <b>bold</b> <!-- b -->",
		"<div><!-- c --></div>", "<div>\n\n<!-- c -->\n\n</div>", "<div>\n<!-- c -->\n</div>", "<p>a <!-- c --> b</p>", "<!-- c -->\n```\ncode\n```",
		"```\n<!-- c -->\n```", "    <!-- indented code -->", "`<!-- code span -->` and <!-- c --> after", "<!-- unclosed\n\ntext still comment", "<!-- unclosed\n```\ncode\n```",
		// Quotes and lists.
		"> <!-- c -->", "> <!-- c -->\n> text", "> text\n> <!-- c -->", "> a\n>\n> <!-- c -->", "> a\n> <!-- c -->\n> b", "> > <!-- c -->\n> > q", "> <!-- c -->\n\nafter",
		"- <!-- c -->", "- <!-- c -->\n- b", "- a\n- <!-- c -->", "- a\n  <!-- c -->\n- b", "- a\n\n  <!-- c -->\n\n- b", "- a\n  - <!-- c -->\n  - b", "1. <!-- c --> x\n2. y",
		"- [ ] <!-- c --> task", "- a\n\n  <!-- c -->", "- a\n\n<!-- c -->\n\n- b", "* <!-- a -->\n\n  <!-- b -->\n\n  text", "- <!-- c --> item\n  <!-- d --> more",
		// Inline position.
		"See <!-- c --> the docs.", "See<!-- c --> the docs.", "See <!-- c -->the docs.", "See<!-- c -->the docs.", "See  <!-- c -->  the docs.", "See\t<!-- c -->\tthe docs.",
		"a <!-- c --><!-- d --> b", "a <!-- c --> <!-- d --> b", "a <!-- c -->", "a<!-- c -->", "<!-- c --> a", "x <!-- c -->\ny", "x\n<!-- c --> y", "x <!-- c\ny -->\nz",
		"a <!-- unclosed", "a <!-- unclosed\nb", "a <!--> b", "a <!---> b", "a <!----> b", "**a <!-- c --> b**", "**a<!-- c --> b**", "*a <!-- c --> b*", "~~a <!-- c --> b~~",
		"[a <!-- c --> b](https://x.y)", "[<!-- c --> b](https://x.y)", "[a <!-- c -->](https://x.y) b", "<!-- c -->[a](https://x.y)", "a <!-- c --> `code` <!-- d --> **b**",
		"a \\<!-- not a comment -->", "a &lt;!-- not a comment --&gt;", "a <!-- c --> &amp; b", "line one  \n<!-- c --> line two", "line one\\\n<!-- c --> line two",
		"# heading <!-- c --> text", "# <!-- c --> heading", "# heading <!-- c -->", "## <!-- c -->", "Setext <!-- c -->\n=====", "# a **b <!-- c --> c**",
		"| a <!-- c --> b | c |\n|---|---|\n| <!-- c --> 1 | 2 <!-- d --> |", "| a | b |\n|---|---|\n| <!-- c --> | x |",
		"a <!-- c --> b\n\n<!-- c -->\n\nd <!-- e --> f", "$a <!-- c --> b$ and <!-- d --> x", "https://a.b <!-- c --> https://c.d",
	}
}

// TestMarkdownHTMLCommentsMatchPiMain compares the rows of every document with Pi's markdown.ts with 54280c4ff applied to the pinned
// dist, at several widths and paddings with every style call tagged.
// Pi source: packages/tui/src/components/markdown.ts
func TestMarkdownHTMLCommentsMatchPiMain(t *testing.T) {
	compareMarkdownWithPi(t, markdownHTMLCommentDocs())
}

// TestMarkdownHTMLCommentsStreamLikeAFreshRender (tagged theme: every render starts at the top; the resumed path is TestMarkdownHTMLCommentsResumedRenderEqualsAFreshRender) renders every prefix of every document through one component that is given each
// longer text in turn, as the interactive mode streams an answer, and requires the rows of a fresh component and of Pi (with
// 54280c4ff) for the same text: a comment whose removal depends on what follows it, an unclosed one and one that a later chunk
// closes must not leave a stale or missing row behind.
func TestMarkdownHTMLCommentsStreamLikeAFreshRender(t *testing.T) {
	var probes []markdownRenderProbe
	var texts []string
	for _, doc := range markdownHTMLCommentDocs() {
		for end := 0; end <= len(doc); end++ {
			if end < len(doc) && !utf8.RuneStart(doc[end]) {
				continue
			}
			probes = append(probes, markdownRenderProbe{Text: doc[:end], Widths: []int{40}})
			texts = append(texts, doc[:end])
		}
	}
	expected := piMarkdownRows(t, probes)
	theme := taggedMarkdownTheme()
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	t.Cleanup(ResetCapabilitiesCache)
	streamed := NewMarkdownWithOptions("", 0, 0, &theme, nil, nil)
	previous := ""
	reported := 0
	for i, text := range texts {
		if !strings.HasPrefix(text, previous) {
			streamed = NewMarkdownWithOptions("", 0, 0, &theme, nil, nil)
		}
		previous = text
		streamed.SetText(text)
		got := nonNil(streamed.Render(40))
		fresh := nonNil(NewMarkdownWithOptions(text, 0, 0, &theme, nil, nil).Render(40))
		want := nonNil(expected[i][0])
		if (!reflect.DeepEqual(got, want) || !reflect.DeepEqual(fresh, want)) && reported < 10 {
			reported++
			t.Errorf("%q:\n streamed %q\n    fresh %q\n       Pi %q", text, got, fresh, want)
		}
	}
}

// TestMarkdownHTMLCommentsResumedRenderEqualsAFreshRender streams documents of several HTML-comment entries joined by blank lines, single
// newlines and nothing, byte by byte, through a component whose default theme resumes from the last block start the longer text cannot have
// changed, and requires the rows of a render with a theme that never resumes (opaqueMarkdownTheme), at a random width. A comment block removed
// together with a neighbouring blank line, or still open at the end of the text, must not leave a stale or missing row in a resumed render; the
// test requires that a large share of the frames did resume.
func TestMarkdownHTMLCommentsResumedRenderEqualsAFreshRender(t *testing.T) {
	pauseGC(t) // the memos are inspected between renders, as a WeakRef target is kept until its job ends
	mdCaseSetup(t)
	rng := rand.New(rand.NewPCG(11, 12))
	entries := markdownHTMLCommentDocs()
	separators := []string{"\n\n", "\n", "\n\n\n", ""}
	reference := opaqueMarkdownTheme()
	frames, resumed := 0, 0
	for range 300 {
		var document strings.Builder
		for n := 3 + rng.IntN(6); n > 0; n-- {
			document.WriteString(entries[rng.IntN(len(entries))])
			if n > 1 {
				document.WriteString(separators[rng.IntN(len(separators))])
			}
		}
		text := document.String()
		width := 10 + rng.IntN(60)
		streamed := NewMarkdownWithOptions("", 0, 0, nil, nil, nil)
		for end := 0; end <= len(text); end++ {
			if end < len(text) && !utf8.RuneStart(text[end]) {
				continue
			}
			prefix := text[:end]
			if memosOf(streamed).resume != nil && memosOf(streamed).resume.pick(prefix) != nil {
				resumed++
			}
			streamed.SetText(prefix)
			got := streamed.Render(width)
			want := NewMarkdownWithOptions(prefix, 0, 0, reference, nil, nil).Render(width)
			frames++
			if !slices.Equal(got, want) {
				t.Fatalf("%d bytes of %q at width %d:\n got %q\nwant %q", end, text, width, got, want)
			}
		}
	}
	if resumed < frames/10 {
		t.Fatalf("only %d of %d frames resumed", resumed, frames)
	}
	t.Logf("%d of %d frames resumed", resumed, frames)
}
