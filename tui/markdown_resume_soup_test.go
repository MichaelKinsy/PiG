package tui

import (
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// markdownSoup is a set of Markdown-significant fragments. A document of random fragments makes block boundaries, markers and
// delimiters fall in arbitrary combinations that a block-level generator does not produce: list markers after quote
// markers, fences inside list items, table pipes in headings, setext underlines under list items, emphasis and code spans
// opened in one block and closed in another, and HTML, LaTeX and definition starts mid-line.
var markdownSoup = []string{
	"\n", "\n", "\n", "\n\n", "\n\n", "\n\n\n", "\r\n", "\n \n", "\t", " ", "  ", "    ",
	"- ", "* ", "+ ", "1. ", "2) ", "10. ", "> ", ">", "  - ", "    - ", "   ",
	"```", "~~~", "```go\n", "````", "    code",
	"| a | b |", "|---|---|", "| 1 | 2 |", "|", ":-:", "--|--",
	"---", "===", "***", "___", "# ", "## ", "###### ",
	"**", "*", "_", "__", "~~", "`", "``",
	"[", "]", "(", ")", "[x]", "[r]", "[r]: http://a.b", "[r]: ", "\"t\"", "'q'", "![i](u.png)", "<http://x.y>",
	"<div>", "</div>", "<!--", "-->", "<pre>", "</pre>", "<a href=\"u\">", "</a>", "<br>",
	"$", "$$", "\\[", "\\]", "\\(", "\\)", "\\*", "\\",
	"word", "words here", "longwordlongwordlongwordlongword", "日本語", "😀", "café", "http://c.d/e", "www.f.g",
	"&amp;", "&#42;", ":", "!", "?",
}

// markdownSparseSoup is a small fragment set whose documents are mostly blank lines, list markers, quote markers and LaTeX
// delimiters, so blocks that render nothing, lists ending in spaces and paragraphs clipped before a LaTeX start meet resume
// points often.
var markdownSparseSoup = []string{"- a", "* b", "1. c", "  ", " ", "\n", "\n\n", "\n\n", ">", "> ", "# ", "x", "```", "\t", "$$", "<!-- c -->", "<div>", "\n \n"}

func markdownSoupDocument(rng *rand.Rand) string {
	fragments, count := markdownSoup, 20+rng.IntN(220)
	if rng.IntN(2) == 0 {
		fragments, count = markdownSparseSoup, 4+rng.IntN(40)
	}
	var doc strings.Builder
	for range count {
		doc.WriteString(fragments[rng.IntN(len(fragments))])
	}
	return doc.String()
}

// Every frame of a stream of a random fragment document, chunked anywhere
// (including inside a multi-byte character, a CRLF pair and every delimiter), equals a new component given the same text,
// under several component configurations.
func TestResumedMarkdownSoupEqualsAFreshRender(t *testing.T) {
	mdCaseSetup(t)
	seed := uint64(7)
	if s := os.Getenv("PIG_MD_SOUP_SEED"); s != "" {
		seed, _ = strconv.ParseUint(s, 10, 64)
	}
	documents := 300
	if s := os.Getenv("PIG_MD_SOUP_DOCS"); s != "" {
		documents, _ = strconv.Atoi(s)
	}
	rng := rand.New(rand.NewPCG(seed, 99))
	indent, latexOn := "  ", true
	production := GetMarkdownTheme()
	production.CodeBlockIndent = &indent
	configs := []struct {
		theme   *MarkdownTheme
		options *MarkdownOptions
	}{
		{nil, nil},
		{&production, nil},
		{nil, &MarkdownOptions{RenderLatex: &latexOn}},
		{&production, &MarkdownOptions{PreserveOrderedListMarkers: true, PreserveBackslashEscapes: true}},
	}
	frames, resumed := 0, 0
	for index := range documents {
		document := markdownSoupDocument(rng)
		config := configs[index%len(configs)]
		paddingX, paddingY := rng.IntN(3), rng.IntN(2)
		width := 8 + rng.IntN(70)
		streamed := NewMarkdownWithOptions("", paddingX, paddingY, config.theme, nil, config.options)
		for end := 0; end < len(document); {
			end = min(len(document), end+1+rng.IntN(1+rng.IntN(16)))
			text := document[:end]
			if streamed.resume != nil && streamed.resume.pick(text) != nil {
				resumed++
			}
			streamed.SetText(text)
			got := streamed.Render(width)
			want := NewMarkdownWithOptions(text, paddingX, paddingY, config.theme, nil, config.options).Render(width)
			frames++
			if !slices.Equal(got, want) {
				t.Fatalf("seed %d document %d config %d, %d bytes at width %d:\ntext %q\n got %q\nwant %q", seed, index, index%len(configs), end, width, text, got, want)
			}
		}
	}
	if resumed < frames/10 {
		t.Fatalf("only %d of %d frames resumed", resumed, frames)
	}
	t.Logf("%d of %d frames resumed", resumed, frames)
}

// The interactive block (thinking and text segments, the interactive theme, an identity transform) streamed
// renders every frame as a new block given the same content.
func TestResumedAssistantBlockEqualsAFreshBlock(t *testing.T) {
	mdCaseSetup(t)
	rng := rand.New(rand.NewPCG(5, 6))
	indent := "  "
	theme := GetMarkdownTheme()
	theme.CodeBlockIndent = &indent
	newBlock := func() *AssistantMessageComponent {
		block := NewAssistantMessageComponent(nil, false, &theme, "", nil, nil)
		block.SetMarkdownTransform(func(markdown string, width int) string { return markdown })
		return block
	}
	frames := 0
	for range 60 {
		thinkingDoc, textDoc := markdownSoupDocument(rng), resumeGeneratedDocument(rng, rng.IntN(2) == 0)
		streamed := newBlock()
		width := 20 + rng.IntN(80)
		t1, t2 := 0, 0
		for t1 < len(thinkingDoc) || t2 < len(textDoc) {
			if t1 < len(thinkingDoc) {
				t1 = min(len(thinkingDoc), t1+1+rng.IntN(20))
			} else {
				t2 = min(len(textDoc), t2+1+rng.IntN(20))
			}
			content := []AssistantSegment{{Thinking: true, Text: thinkingDoc[:t1]}, {Text: textDoc[:t2]}}
			streamed.SetContent(content)
			got := streamed.Render(width)
			fresh := newBlock()
			fresh.SetContent(content)
			want := fresh.Render(width)
			frames++
			if !slices.Equal(got, want) {
				t.Fatalf("thinking %q text %q width %d:\n got %q\nwant %q", thinkingDoc[:t1], textDoc[:t2], width, got, want)
			}
		}
	}
	t.Logf("%d frames", frames)
}
