package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The block lexer of Markdown (markdown_marked_lexer.go) is a port of marked's Lexer.blockTokens and the block tokenizers that pi-tui's Markdown component reads. This test compares its token tree with the one the pinned Pi
// records: testdata/markdown_marked_lexer.mjs patches Marked.prototype.lexer before Pi renders, so the tree of every call of the component's own parser is serialized as it returns, and lexes each source through that instance.
// A document is equal when the canonical forms below are equal: the kind, raw, text, language, depth, list and item flags, quote, list item and table children, the source of each inline token list and the link definitions.
// The one difference of representation is a task item's checkbox: marked adds a checkbox token to the item (or to the inline tokens of a loose item's first paragraph, with its raw ahead of that paragraph's raw and text);
// PiG keeps the state in item.checked and never builds the token, and the script drops it from its form after checking that marked put it where it expects.

// markedBlockForm is the canonical form of src: its block tokens as PiG's lexer returns them, for the source text normalized the way Markdown.render normalizes it. state is reused for the next call, as the renderer reuses
// one per component, so a token a reused lexer would leak into the next source shows up as a difference.
func markedBlockForm(state *markedInlineState, src string) (form string) {
	defer func() {
		if r := recover(); r != nil {
			form = fmt.Sprintf("PANIC %v", r)
		}
	}()
	state.startDocument()
	normalized := normalizeSource(src)
	tokens := state.blockLexer().lexRunes(state.blockRunes(normalized), normalized)
	var b strings.Builder
	for i, token := range tokens {
		if i > 0 {
			b.WriteByte(' ')
		}
		writeMarkedBlockForm(&b, token)
	}
	tags := make([]string, 0, len(state.links))
	for tag, href := range state.links {
		var link strings.Builder
		link.WriteByte('(')
		writeMarkedFormString(&link, tag)
		link.WriteByte(' ')
		writeMarkedFormString(&link, href)
		link.WriteByte(')')
		tags = append(tags, link.String())
	}
	sort.Strings(tags)
	b.WriteString(" {" + strings.Join(tags, " ") + "}")
	return b.String()
}

// writeMarkedFormString writes s quoted with only \" \\ and \xHH escapes; a lone surrogate, which PiG spells in WTF-8, is U+FFFD, as it is on the script's side.
func writeMarkedFormString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range jsstring.StreamText(s) {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}

func writeMarkedBlockForms(b *strings.Builder, tokens []*markedBlock) {
	b.WriteByte('[')
	for i, token := range tokens {
		if i > 0 {
			b.WriteByte(' ')
		}
		writeMarkedBlockForm(b, token)
	}
	b.WriteByte(']')
}

func writeMarkedBlockForm(b *strings.Builder, token *markedBlock) {
	str := func(s string) {
		b.WriteByte(' ')
		writeMarkedFormString(b, s)
	}
	inline := func(entry *markedInlineEntry) {
		if entry == nil {
			b.WriteString(" <no inline entry>")
			return
		}
		str(entry.src)
	}
	b.WriteString("(" + token.kind)
	switch token.kind {
	case "space", "hr":
		str(token.raw)
	case "code":
		str(token.raw)
		str(token.text)
		str(token.lang)
	case "heading":
		str(token.raw)
		str(token.text)
		b.WriteString(" " + strconv.Itoa(token.depth))
		inline(token.entry)
	case "paragraph", "text":
		str(token.raw)
		str(token.text)
		inline(token.entry)
	case "html":
		str(token.raw)
		str(token.text)
	case "def":
		str(token.raw)
		str(token.tag)
		str(token.text)
	case "latexBlock":
		str(token.raw)
		str(token.text)
		b.WriteString(" " + strconv.FormatBool(token.pending))
	case "blockquote":
		str(token.raw)
		str(token.text)
		b.WriteByte(' ')
		writeMarkedBlockForms(b, token.tokens)
	case "list":
		str(token.raw)
		b.WriteString(" " + strconv.FormatBool(token.ordered) + " " + strconv.Itoa(token.start) + " " + strconv.FormatBool(token.loose) + " [")
		for i, item := range token.items {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString("(item")
			str(item.raw)
			str(item.text)
			b.WriteString(" " + strconv.FormatBool(item.task) + " " + strconv.FormatBool(item.checked) + " " + strconv.FormatBool(item.loose) + " ")
			writeMarkedBlockForms(b, item.tokens)
			b.WriteByte(')')
		}
		b.WriteByte(']')
	case "table":
		str(token.raw)
		cell := func(entry *markedInlineEntry) {
			b.WriteString("(cell")
			str(entry.src)
			str(entry.src)
			b.WriteByte(')')
		}
		b.WriteString(" [")
		for i, entry := range token.header {
			if i > 0 {
				b.WriteByte(' ')
			}
			cell(entry)
		}
		b.WriteString("] [")
		for i, row := range token.rows {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteByte('[')
			for j, entry := range row {
				if j > 0 {
					b.WriteByte(' ')
				}
				cell(entry)
			}
			b.WriteByte(']')
		}
		b.WriteByte(']')
	default:
		b.WriteString(" <unknown kind>")
	}
	b.WriteByte(')')
}

// piMarkedBlockForms are the canonical forms of the sources as the pinned Pi's Markdown parser lexes them; every renderEvery-th source (0: none) is also rendered by a Markdown component, which the script requires to lex the same text to the same tree.
func piMarkedBlockForms(ctx context.Context, sources []string, renderEvery int) ([]string, error) {
	input, err := json.Marshal(struct {
		Sources []string `json:"sources"`
		Render  int      `json:"render"`
	}{sources, renderEvery})
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "node", "testdata/markdown_marked_lexer.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("Pi oracle: %w\n%s", err, &stderr)
	}
	var forms []string
	if err := json.Unmarshal(output, &forms); err != nil {
		return nil, err
	}
	if len(forms) != len(sources) {
		return nil, fmt.Errorf("Pi oracle returned %d forms for %d sources", len(forms), len(sources))
	}
	return forms, nil
}

// markedLexerDifference is a source on which PiG's lexer returns other tokens than marked.
type markedLexerDifference struct{ src, got, want string }

// markedLexerOracleDifferences compares the sources chunk by chunk, in parallel, and returns the differing ones in source order.
func markedLexerOracleDifferences(t *testing.T, sources []string, renderEvery int) []markedLexerDifference {
	t.Helper()
	const chunk = 8000
	var chunks [][]string
	for start := 0; start < len(sources); start += chunk {
		chunks = append(chunks, sources[start:min(len(sources), start+chunk)])
	}
	results := make([][]markedLexerDifference, len(chunks))
	errs := make([]error, len(chunks))
	var wg sync.WaitGroup
	slots := make(chan struct{}, max(1, min(8, runtime.NumCPU())))
	for i, part := range chunks {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			want, err := piMarkedBlockForms(t.Context(), part, renderEvery)
			if err != nil {
				errs[i] = err
				return
			}
			state := &markedInlineState{}
			for j, src := range part {
				if got := markedBlockForm(state, src); got != want[j] {
					results[i] = append(results[i], markedLexerDifference{src, got, want[j]})
				}
			}
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	return slices.Concat(results...)
}

func requireMarkedLexerMatchesPi(t *testing.T, name string, sources []string, renderEvery int) {
	t.Helper()
	sources = uniqueMarkedSources(sources)
	differences := markedLexerOracleDifferences(t, sources, renderEvery)
	for i, d := range differences {
		if i == 10 {
			break
		}
		t.Errorf("%s: source %q\n got %s\nwant %s", name, d.src, d.got, d.want)
	}
	if len(differences) > 0 {
		t.Errorf("%s: %d of %d sources lex to other tokens than marked's", name, len(differences), len(sources))
	}
	t.Logf("%s: %d sources", name, len(sources))
}

func uniqueMarkedSources(sources []string) []string {
	seen := make(map[string]struct{}, len(sources))
	out := make([]string, 0, len(sources))
	for _, src := range sources {
		if _, dup := seen[src]; !dup {
			seen[src] = struct{}{}
			out = append(out, src)
		}
	}
	return out
}

// markedLexerOracleScale multiplies the sizes of the generated sources (PIG_MARKED_LEXER_ORACLE_SCALE, default 1).
func markedLexerOracleScale(t *testing.T) int {
	scale := 1
	if v := os.Getenv("PIG_MARKED_LEXER_ORACLE_SCALE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("PIG_MARKED_LEXER_ORACLE_SCALE=%q is no positive integer", v)
		}
		scale = n
	}
	return scale
}

// markedLexerCommittedSources are the documents of the committed corpora: the Pi-oracle render corpora of the markdown surface (random, CommonMark, residual, block-edge, regression, media, emphasis, state, sweep and table
// documents), the marked and LaTeX corpora and the survivor documents.
func markedLexerCommittedSources(t *testing.T) map[string][]string {
	sets := map[string][]string{
		"random":      markdownRandomDocs(20260101, 12000),
		"commonmark":  markdownCommonmarkDocs(),
		"residual":    markdownResidualDocs(),
		"block-edge":  markdownBlockEdgeDocs(),
		"regression":  markdownContractRegressionDocs(),
		"render":      markdownRenderDocs(),
		"media":       markdownContractMediaDocs(),
		"orphan":      markdownOrphanDelimiterDocs(),
		"emphasis":    markdownRandomEmphasis(20260102, 1500),
		"state":       markdownStateDocs(),
		"sweep":       slices.Concat(markdownSweepTables(), markdownSweepBlocks(), markdownSweepInlines()),
		"tables":      markdownRandomTables(20260103, 1500),
		"survivor":    markdownCoreSurvivorDocs,
		"soup":        slices.Concat(markdownSoup, markdownSparseSoup),
		"resume-deps": resumeDependentDocuments,
		// An indented code line starts after U+2028 and U+2029 as it does after a line feed: marked's codeRemoveIndent has the multiline flag.
		"line-separators": {"    a\u2028 b", "    a\u2029  b", "     a\u2028     b", "    a\u2028\u2028 b", "    a\u2028", "    \u2028 b", "    a\u2028\tb", "    a\u2028\n b", "    a\r b"},
	}
	var corpus []struct {
		Source string `json:"source"`
	}
	data, err := os.ReadFile("testdata/markdown-marked-corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	for _, c := range corpus {
		sets["marked-corpus"] = append(sets["marked-corpus"], c.Source)
	}
	latex, err := os.ReadFile("testdata/markdown-latex-corpus.txt")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(latex), "\n") {
		if strings.HasPrefix(line, `"`) {
			var src string
			if err := json.Unmarshal([]byte(line), &src); err != nil {
				t.Fatal(err)
			}
			sets["latex-corpus"] = append(sets["latex-corpus"], src)
		}
	}
	sets["streaming"] = streamingCorpusDocuments(t)
	return sets
}

// TestMarkedBlockLexerEqualsMarkedOnCommittedCorpora requires PiG's block tokens to equal marked's on every document of the committed corpora, and the documents Markdown.render lexes to be the ones the script lexes.
func TestMarkedBlockLexerEqualsMarkedOnCommittedCorpora(t *testing.T) {
	sets := markedLexerCommittedSources(t)
	names := make([]string, 0, len(sets))
	for name := range sets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) { requireMarkedLexerMatchesPi(t, name, sets[name], 7) })
	}
}

// TestMarkedBlockLexerEqualsMarkedOnTokenizerSources requires the same on the sources of the tokenizer tests: every source of up to four characters from the alphabet of the characters the rules read, random longer ones, and
// documents of up to six of the lines that start, continue and end the blocks.
func TestMarkedBlockLexerEqualsMarkedOnTokenizerSources(t *testing.T) {
	var rules, blocks []string
	markedRuleSources(t, func(src string) { rules = append(rules, src) })
	markedBlockSources(func(src string) { blocks = append(blocks, src) })
	requireMarkedLexerMatchesPi(t, "rule sources", rules, 97)
	requireMarkedLexerMatchesPi(t, "block sources", blocks, 97)
}

// TestMarkedBlockLexerEqualsMarkedOnGeneratedDocuments requires the same on documents drawn from markedLexerDocument: the block kinds in any container (quotes, list items at every indent width and marker width, tasks), with
// setext and ATX headings, tables, HTML blocks of every kind, definitions, display math and fences, and every kind of line ending and blank line.
func TestMarkedBlockLexerEqualsMarkedOnGeneratedDocuments(t *testing.T) {
	scale := markedLexerOracleScale(t)
	for seed := range uint64(3) {
		rng := rand.New(rand.NewPCG(seed+1, 0xb5ad4eceda1ce2a9))
		var docs []string
		for range 150000 * scale {
			docs = append(docs, markedLexerDocument(rng))
		}
		requireMarkedLexerMatchesPi(t, "generated "+strconv.FormatUint(seed, 10), docs, 53)
	}
}

// markedLexerDocument is one to eight lines, each a leaf block line inside a chain of containers.
func markedLexerDocument(rng *rand.Rand) string {
	pick := func(items ...string) string { return items[rng.IntN(len(items))] }
	chance := func(n int) bool { return rng.IntN(n) == 0 }
	// indent: every width a block line may open with, up to the tab and beyond code's four spaces.
	indent := func() string {
		if chance(2) {
			return ""
		}
		return pick(" ", "  ", "   ", "    ", "     ", "      ", "\t", " \t", "  \t", "   \t", "\t ", "\u00a0", " \u00a0")
	}
	word := func() string {
		return pick("a", "b", "word", "two words", "x_y", "**b**", "`c`", "[l](u)", "日本", "🙂", "é", "a|b", "$x$", "<b>", "&amp;", "a\\", "[a]", "a  ", "\u00a0b", "a\u2028b", "a\rb", "0")
	}
	text := func() string {
		out := word()
		for chance(3) {
			out += pick(" ", "  ", "\t") + word()
		}
		return out
	}
	number := func() string {
		return pick("0", "1", "2", "7", "9", "10", "99", "123456789", "1234567890", "00", "007", "999999999")
	}
	marker := func() string {
		switch rng.IntN(7) {
		case 0:
			return number() + "."
		case 1:
			return number() + ")"
		case 2:
			return "-"
		case 3:
			return "*"
		case 4:
			return "+"
		case 5:
			return pick("-", "*", "+", "1.", "2)")
		}
		return pick("-", "-", "*", "1.")
	}
	gap := func() string { return pick(" ", " ", " ", "  ", "   ", "    ", "     ", "\t", "") }
	task := func() string {
		if !chance(3) {
			return ""
		}
		return pick("[ ]", "[x]", "[X]", "[]", "[xx]", "[ ] ", "[x]  ", "[-]", "[ ]\t", "[x]x", "[ ")
	}
	fence := func() string {
		return pick("```", "```", "~~~", "````", "~~~~", "``", "~~", "``` ", " ```", "```go", "~~~ x", "``` a`b", "```` `", "\\```", "~~~`")
	}
	// leaf is a line that is, or continues, one block.
	leaf := func() string {
		switch rng.IntN(26) {
		case 0, 1, 2:
			return text()
		case 3:
			return ""
		case 4:
			return pick(" ", "  ", "   ", "    ", "\t", " \t")
		case 5, 6:
			return marker() + gap() + task() + pick("", " ", text(), text(), text())
		case 7:
			return pick("#", "##", "###### ", "####### ", "#h", "# h #", "## h ##", "# h \\#", "#\t", "# ") + pick("", " ", text())
		case 8:
			return pick("=", "==", "===", "= =", "-", "--", "---", "- - -", "***", "___", "* * *", "-=-", " ===", "  ---", "   --- ", "\t---", "=== =", "- -")
		case 9:
			return fence() + pick("", "", text())
		case 10:
			return pick("|", "| a |", "a|b", "|a|b|", "|-|", "|-|-|", "--|--", ":-:|-:", "|:-|-:|", "| - | - |", "-|-", " | - ", "|---|", "a | b", "| a | b | c |", "|a\\|b|", "||")
		case 11:
			return pick("<div>", "</div>", "<div", "<!--", "-->", "<!-- c -->", "<!-->", "<!--->", "<pre>", "</pre>", "<script>", "<style>", "<textarea>", "<?x", "?>", "<![CDATA[", "]]>", "<!A", "<a href=\"x\">", "<br/>", "<x-y>", "</x>", "<b>a</b>", "<a\n")
		case 12:
			return pick("[a]: u", "[a]: <u>", "[a]: u \"t\"", "[A]: v", "[a]:", "[ ]: u", "[a\\]]: u", "[a]: u t", "[a]: <>", "[b]:\n", "[a]: u 'x", "[^f]: u", "[a]: u\n\"t\"", "[ a ]: u")
		case 13:
			return pick("$$", "$$x$$", "$$ x", "x $$", "\\[", "\\[x\\]", "\\[ x", "\\]", "$$\\frac{a}{b}$$", "$$ a\n| - |", " $$", "$ $", "$$$")
		case 14:
			return pick("<>", "[x]", "x\\", "\\", "\\\\", "\r", "\u2028", "\u00a0", "a\u00a0", "\u00a0\u00a0")
		case 15, 16:
			return pick("    ", "     ", "      ") + text()
		case 17:
			return pick("> ", ">", " > ", "  >", ">  ", ">\t") + pick("", text())
		case 18:
			return indent() + marker() + gap() + task() + pick("", text())
		case 19:
			return pick("1.", "1)", "-", "*") + gap() + pick("", text(), "```", "# h", "> q", "- n", "1. n")
		default:
			return text()
		}
	}
	// container is the prefix a line keeps inside the blocks around it: quote markers and the indent of list item content.
	container := func() string {
		var out string
		for range rng.IntN(3) {
			switch rng.IntN(5) {
			case 0:
				out += pick(">", "> ", " > ", ">>", "> >", ">\t", "  > ", "   >  ")
			case 1:
				out += pick("  ", "   ", "    ", "      ", "\t")
			case 2:
				out += marker() + gap()
			case 3:
				out += pick(" ", "  ", "   ")
			default:
				out += pick("- ", "1. ", "* ", "> ")
			}
		}
		return out
	}
	var lines []string
	for range 1 + rng.IntN(8) {
		line := indent() + leaf()
		if chance(2) {
			line = container() + line
		}
		lines = append(lines, line)
		if chance(6) {
			lines = append(lines, pick("", "", " ", "  ", "\t", container()+""))
		}
	}
	return strings.Join(lines, pick("\n", "\n", "\n", "\n", "\r\n", "\r", "\n"))
}
