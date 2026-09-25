package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func markdownPlainLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimRight(stripANSI(line), " ")
	}
	return out
}

func TestMarkdownNestedListWithWrappedContinuationAlignsUnderMarker(t *testing.T) {
	m := NewMarkdown("- This is a very long nested list item that should wrap onto another line when rendered in a narrow width")
	got := markdownPlainLines(m.Render(24))
	if len(got) < 2 {
		t.Fatalf("expected wrapped list item, got %v", got)
	}
	if got[0] != "- This is a very long" {
		t.Fatalf("first wrapped line = %q", got[0])
	}
	if got[1] != "  nested list item that" {
		t.Fatalf("second wrapped line = %q", got[1])
	}
	for _, line := range got[1:] {
		if strings.HasPrefix(line, "-") {
			t.Fatalf("continuation line should not repeat marker: %q", line)
		}
	}
}

func TestMarkdownTextTokenRendersInlineFormatting(t *testing.T) {
	m := NewMarkdown("- **bold** item")
	joined := strings.Join(m.Render(80), "\n")
	if !strings.Contains(joined, "\033[1mbold") {
		t.Fatalf("expected bold formatting inside list text token: %q", joined)
	}
}

func TestMarkdownHeadingsUseHashesForLevelThreeOnly(t *testing.T) {
	joined := strings.Join(markdownPlainLines(NewMarkdown("# One\n## Two\n### Three").Render(80)), "\n")
	if strings.Contains(joined, "# One") || strings.Contains(joined, "## Two") {
		t.Fatalf("h1/h2 should not include hash prefixes: %q", joined)
	}
	if !strings.Contains(joined, "### Three") {
		t.Fatalf("h3 should include hash prefix, got %q", joined)
	}
}

func TestMarkdownHeadings(t *testing.T) {
	m := NewMarkdown("# H1\n## H2\n### H3\nbody\n")
	out := strings.Join(m.Render(80), "\n")
	// Headings should contain the text with some ANSI styling.
	// Theme-based colors vary, so just check content presence.
	if !strings.Contains(out, "H1") {
		t.Errorf("H1 missing: %q", out)
	}
	if !strings.Contains(out, "H2") {
		t.Errorf("H2 missing: %q", out)
	}
	if !strings.Contains(out, "H3") {
		t.Errorf("H3 missing: %q", out)
	}
	// Should have ANSI color escapes (not plain text).
	if !strings.Contains(out, "\033[") {
		t.Errorf("headings should have ANSI styling: %q", out)
	}
}

func TestMarkdownInlineFormatting(t *testing.T) {
	m := NewMarkdown("This is **bold** and *italic* and `code`.")
	out := m.Render(80)[0]
	// Bold should use \033[1m
	if !strings.Contains(out, "\033[1mbold") {
		t.Errorf("missing bold in %q", out)
	}
	// Italic should use \033[3m
	if !strings.Contains(out, "\033[3mitalic") {
		t.Errorf("missing italic in %q", out)
	}
	// Code uses theme color (varies): just check content.
	if !strings.Contains(out, "code") {
		t.Errorf("missing code in %q", out)
	}
}

func TestMarkdownCodeFence(t *testing.T) {
	m := NewMarkdown("```go\nfunc main() {}\n```\n")
	out := m.Render(40)
	// header + body + footer = 3
	if len(out) < 3 {
		t.Fatalf("code fence rendered too few lines: %v", out)
	}
	if !strings.Contains(out[0], "go") {
		t.Errorf("header should have lang label: %q", out[0])
	}
	if !strings.Contains(stripANSI(out[1]), "func main()") {
		t.Errorf("body missing: %q", out[1])
	}
	// Header & footer must not exceed terminal width when printed (was
	// broken pre-fix because byte-slicing a `─`-runes string mangled
	// UTF-8). Spot-check that the header has no replacement char.
	if strings.Contains(out[0], "\ufffd") {
		t.Errorf("UTF-8 mangling in code-fence header: %q", out[0])
	}
}

func TestMarkdownBulletList(t *testing.T) {
	m := NewMarkdown("- one\n- two")
	out := markdownPlainLines(m.Render(80))
	if len(out) != 2 {
		t.Fatalf("expected 2 bullets, got %d: %v", len(out), out)
	}
	if out[0] != "- one" || out[1] != "- two" {
		t.Fatalf("bullet markers should be preserved: %v", out)
	}
}

// Ported from markdown.test.ts "should render task list markers".
func TestMarkdownRendersTaskListMarkers(t *testing.T) {
	got := markdownPlainLines(NewMarkdown("- [ ] beep\n- [x] boop").Render(80))
	want := []string{"- [ ] beep", "- [x] boop"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("task list = %q, want %q", got, want)
	}
}

// markdown.ts renderList styles bullet+taskMarker as one listBullet marker,
// normalizes marked's checked "[X]" to "[x]", and aligns wrapped task text
// under the text rather than under the checkbox.
func TestMarkdownTaskListMarkerStyleAndContinuation(t *testing.T) {
	bullet := ActiveTheme().MDListBullet
	if bullet == "" {
		t.Fatal("active theme has no list bullet color")
	}
	lines := NewMarkdown("- [X] done\n- [ ] alpha beta gamma delta").Render(16)
	if !strings.HasPrefix(lines[0], bullet+"- [x] \033[39m") {
		t.Fatalf("checked task marker = %q, want %q prefix", lines[0], bullet+"- [x] \033[39m")
	}
	got := markdownPlainLines(lines)
	want := []string{"- [x] done", "- [ ] alpha beta", "      gamma", "      delta"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("task list = %q, want %q", got, want)
	}
	// "[ ]" with no following text is not a task item (marked listIsTask
	// requires the trailing space).
	if got := markdownPlainLines(NewMarkdown("- [ ]").Render(80)); got[0] != "- [ ]" {
		t.Fatalf("bare checkbox = %q", got)
	}
}

// Pig's line-based markdown renderer preserves user-authored ordered-list
// markers verbatim rather than renumbering them from the list start. This is
// the behavior upstream opts into for user-message transcripts via
// preserveOrderedListMarkers:true (markdown.ts:576, user-message.ts:26, #5013).
// UserMessageBlock renders through NewMarkdown, so user-authored "3. 5. 7."
// survives in the transcript.
func TestMarkdownPreservesSourceOrderedListMarkers(t *testing.T) {
	m := NewMarkdown("3. three\n5. five\n7. seven")
	out := markdownPlainLines(m.Render(80))
	if len(out) != 3 {
		t.Fatalf("expected 3 items, got %d: %v", len(out), out)
	}
	want := []string{"3. three", "5. five", "7. seven"}
	for i, w := range want {
		if out[i] != w {
			t.Fatalf("ordered marker not preserved at item %d: got %q want %q (full: %v)", i, out[i], w, out)
		}
	}
}

func TestMarkdownWrapsLongParagraphs(t *testing.T) {
	long := "I need you to write multi echo bash scripts that make slide show art as they execute to show small animation. When it's done it should print Animation Complete!"
	m := NewMarkdown(long)
	out := m.Render(60)
	if len(out) < 2 {
		t.Fatalf("long paragraph should wrap into multiple lines, got %d: %v", len(out), out)
	}
	for i, line := range out {
		plain := stripANSI(line)
		if len(plain) > 60 {
			t.Errorf("line %d exceeds width 60: len=%d %q", i, len(plain), plain)
		}
	}
}

func TestMarkdownWrapsBlockquote(t *testing.T) {
	long := "> **You:** I need you to write multi echo bash scripts that make slide show art as they execute to show small animation"
	m := NewMarkdown(long)
	out := m.Render(60)
	if len(out) < 2 {
		t.Fatalf("long blockquote should wrap, got %d: %v", len(out), out)
	}
	for i, line := range out {
		// VisibleWidth measures terminal columns; the previous len(plain)
		// approach miscounted multi-byte runes ("│" is 3 bytes / 1 col).
		w := widthx.VisibleWidth(line)
		if w > 60 {
			t.Errorf("blockquote line %d exceeds width 60: cols=%d %q", i, w, line)
		}
	}
}

func TestMarkdownSpacingAfterHeading(t *testing.T) {
	m := NewMarkdown("# Hello\nThis is a paragraph")
	got := markdownPlainLines(m.Render(80))
	want := []string{"Hello", "", "This is a paragraph"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %v want %v", got, want)
	}

	m = NewMarkdown("# Hello")
	got = markdownPlainLines(m.Render(80))
	if got[len(got)-1] == "" {
		t.Fatalf("heading should not end with trailing blank line: %v", got)
	}
}

func TestMarkdownSpacingAfterCodeBlock(t *testing.T) {
	m := NewMarkdown("hello this is text\n```\ncode block\n```\nmore text")
	got := markdownPlainLines(m.Render(80))
	want := []string{"hello this is text", "", "```code", "  code block", "```", "", "more text"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %v want %v", got, want)
	}

	m = NewMarkdown("```js\nconst hello = 'world';\n```")
	got = markdownPlainLines(m.Render(80))
	if got[len(got)-1] == "" {
		t.Fatalf("code block should not end with trailing blank line: %v", got)
	}
}

func TestMarkdownSpacingAfterDividerAndBlockquote(t *testing.T) {
	m := NewMarkdown("---\nagain, hello world")
	got := markdownPlainLines(m.Render(20))
	if len(got) < 3 || got[1] != "" || got[2] != "again, hello world" {
		t.Fatalf("divider spacing mismatch: %v", got)
	}

	m = NewMarkdown("> This is a quote\n\nagain, hello world")
	got = markdownPlainLines(m.Render(80))
	want := []string{"│ This is a quote", "", "again, hello world"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("blockquote spacing mismatch: got %v want %v", got, want)
	}

	m = NewMarkdown("> This is a quote")
	got = markdownPlainLines(m.Render(80))
	if got[len(got)-1] == "" {
		t.Fatalf("blockquote should not end with trailing blank line: %v", got)
	}
}

func TestMarkdownBlockquoteLazyContinuation(t *testing.T) {
	m := NewMarkdown(">Foo\nbar")
	got := markdownPlainLines(m.Render(80))
	quoted := make([]string, 0, len(got))
	for _, line := range got {
		if strings.HasPrefix(line, "│ ") {
			quoted = append(quoted, line)
		}
	}
	if len(quoted) != 2 {
		t.Fatalf("expected 2 quoted lines, got %v", got)
	}
	if quoted[0] != "│ Foo" || quoted[1] != "│ bar" {
		t.Fatalf("lazy blockquote mismatch: %v", quoted)
	}
}

func TestMarkdownBlockquoteExplicitMultiline(t *testing.T) {
	m := NewMarkdown(">Foo\n>bar")
	got := markdownPlainLines(m.Render(80))
	quoted := make([]string, 0, len(got))
	for _, line := range got {
		if strings.HasPrefix(line, "│ ") {
			quoted = append(quoted, line)
		}
	}
	if len(quoted) != 2 {
		t.Fatalf("expected 2 quoted lines, got %v", got)
	}
	if quoted[0] != "│ Foo" || quoted[1] != "│ bar" {
		t.Fatalf("explicit blockquote mismatch: %v", quoted)
	}
}

func TestMarkdownBlockquoteListContent(t *testing.T) {
	m := NewMarkdown("> 1. bla bla\n> - nested bullet")
	got := markdownPlainLines(m.Render(80))
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "│ 1. bla bla") {
		t.Fatalf("missing ordered list item in quote: %v", got)
	}
	if !strings.Contains(joined, "│ - nested bullet") {
		t.Fatalf("missing unordered list item in quote: %v", got)
	}
}

func TestMarkdownBlockquoteWrapsEveryContinuationLine(t *testing.T) {
	m := NewMarkdown("> This is a very long blockquote line that should wrap to multiple lines when rendered")
	got := markdownPlainLines(m.Render(30))
	content := make([]string, 0, len(got))
	for _, line := range got {
		if strings.TrimSpace(line) != "" {
			content = append(content, line)
		}
	}
	if len(content) < 2 {
		t.Fatalf("expected wrapped blockquote, got %v", got)
	}
	joined := strings.Join(content, " ")
	for _, want := range []string{"very long", "blockquote", "multiple"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in wrapped blockquote: %v", want, content)
		}
	}
	for _, line := range content {
		if !strings.HasPrefix(line, "│ ") {
			t.Fatalf("wrapped line missing quote border: %q", line)
		}
	}
}

func TestMarkdownStrikethroughStrictSyntax(t *testing.T) {
	m := NewMarkdown("Use ~~strikethrough~~ here")
	joined := strings.Join(m.Render(80), "\n")
	plain := strings.Join(markdownPlainLines(m.Render(80)), " ")
	if !strings.Contains(joined, "\033[9m") {
		t.Fatalf("expected strikethrough styling in %q", joined)
	}
	if strings.Contains(plain, "~~strikethrough~~") {
		t.Fatalf("expected delimiters removed in %q", plain)
	}

	m = NewMarkdown("Use ~strikethrough~ literally")
	joined = strings.Join(m.Render(80), "\n")
	plain = strings.Join(markdownPlainLines(m.Render(80)), " ")
	if !strings.Contains(plain, "~strikethrough~") {
		t.Fatalf("expected single-tilde text literal in %q", plain)
	}
	if strings.Contains(joined, "\033[9m") {
		t.Fatalf("single-tilde text should not use strikethrough styling: %q", joined)
	}
}

func TestMarkdownTableSimple(t *testing.T) {
	m := NewMarkdown("| Name | Age |\n| --- | --- |\n| Alice | 30 |\n| Bob | 25 |")
	plain := markdownPlainLines(m.Render(80))
	joined := strings.Join(plain, "\n")
	for _, want := range []string{"Name", "Age", "Alice", "Bob", "│", "─"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in rendered table:\n%s", want, joined)
		}
	}
	dividerCount := 0
	for _, line := range plain {
		if strings.Contains(line, "┼") {
			dividerCount++
		}
	}
	if dividerCount != 2 {
		t.Fatalf("dividerCount = %d, want 2; lines=%v", dividerCount, plain)
	}
}

func TestMarkdownHTMLLikeTagsRenderAsText(t *testing.T) {
	m := NewMarkdown("This is text with <thinking>hidden content</thinking> that should be visible")
	joined := strings.Join(markdownPlainLines(m.Render(80)), " ")
	if !strings.Contains(joined, "hidden content") && !strings.Contains(joined, "<thinking>") {
		t.Fatalf("html-like tag content disappeared: %q", joined)
	}
}

func TestMarkdownHTMLInCodeBlockRemainsVisible(t *testing.T) {
	m := NewMarkdown("```html\n<div>Some HTML</div>\n```")
	joined := strings.Join(markdownPlainLines(m.Render(80)), "\n")
	if !strings.Contains(joined, "<div>") || !strings.Contains(joined, "</div>") {
		t.Fatalf("html code block content disappeared: %q", joined)
	}
}

func TestMarkdownTableWrapsWithinWidth(t *testing.T) {
	m := NewMarkdown("| Command | Description | Example |\n| --- | --- | --- |\n| npm install | Install all dependencies | npm install |\n| npm run build | Build the project | npm run build |")
	plain := markdownPlainLines(m.Render(50))
	joined := strings.Join(plain, " ")
	for _, line := range plain {
		if widthx.VisibleWidth(line) > 50 {
			t.Fatalf("line exceeds width 50: %q (%d)", line, widthx.VisibleWidth(line))
		}
	}
	for _, want := range []string{"Command", "Description", "npm install", "Install"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in wrapped table: %s", want, joined)
		}
	}
}

func TestMarkdownTableVaryingColumnWidths(t *testing.T) {
	m := NewMarkdown("| Short | Very long column header |\n| --- | --- |\n| A | This is a much longer cell content |\n| B | Short |")
	plain := markdownPlainLines(m.Render(80))
	joined := strings.Join(plain, "\n")
	for _, want := range []string{"Very long column header", "This is a much longer cell content", "Short"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in varying-width table:\n%s", want, joined)
		}
	}
}

func TestMarkdownTableLongCellWrapsToMultipleLines(t *testing.T) {
	m := NewMarkdown("| Header |\n| --- |\n| This is a very long cell content that should wrap |")
	plain := markdownPlainLines(m.Render(25))
	dataRows := 0
	for _, line := range plain {
		if strings.HasPrefix(line, "│") && !strings.Contains(line, "─") {
			dataRows++
		}
	}
	if dataRows <= 2 {
		t.Fatalf("expected wrapped data rows, got %d: %v", dataRows, plain)
	}
	joined := strings.Join(plain, " ")
	for _, want := range []string{"very long", "cell content", "should wrap"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in wrapped table content: %v", want, plain)
		}
	}
}

func TestMarkdownTableWrapsLongUnbrokenTokensWithinBorders(t *testing.T) {
	m := NewMarkdown("| Value |\n| --- |\n| prefix https://example.com/this/is/a/very/long/url/that/should/wrap |")
	plain := markdownPlainLines(m.Render(30))
	for _, line := range plain {
		if widthx.VisibleWidth(line) > 30 {
			t.Fatalf("line exceeds width 30: %q (%d)", line, widthx.VisibleWidth(line))
		}
	}
	tableLines := 0
	for _, line := range plain {
		if strings.HasPrefix(line, "│") {
			tableLines++
			if count := strings.Count(line, "│"); count != 2 {
				t.Fatalf("expected 2 borders, got %d: %q", count, line)
			}
		}
	}
	if tableLines == 0 {
		t.Fatalf("expected table rows, got %v", plain)
	}
	extracted := strings.NewReplacer("│", "", "├", "", "┤", "", "─", "", " ", "").Replace(strings.Join(plain, ""))
	for _, want := range []string{"prefix", "https://example.com/this/is/a/very/long/url/that/should/wrap"} {
		if !strings.Contains(extracted, want) {
			t.Fatalf("missing %q in extracted wrapped token text: %q", want, extracted)
		}
	}
}

func TestMarkdownTableInlineCodePreservesBorders(t *testing.T) {
	m := NewMarkdown("| Code |\n| --- |\n| `averyveryveryverylongidentifier` |")
	lines := m.Render(20)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, ActiveTheme().MDCode) {
		t.Fatalf("inline code should be styled in table cell: %q", joined)
	}
	plain := markdownPlainLines(lines)
	for _, line := range plain {
		if widthx.VisibleWidth(line) > 20 {
			t.Fatalf("line exceeds width 20: %q (%d)", line, widthx.VisibleWidth(line))
		}
		if strings.HasPrefix(line, "│") {
			count := strings.Count(line, "│")
			if count != 2 {
				t.Fatalf("expected 2 borders, got %d: %q", count, line)
			}
		}
	}
}

func TestMarkdownTableFitsNaturallyAtWideWidth(t *testing.T) {
	m := NewMarkdown("| A | B |\n| --- | --- |\n| 1 | 2 |")
	plain := markdownPlainLines(m.Render(80))
	joined := strings.Join(plain, "\n")
	if !strings.Contains(joined, "A") || !strings.Contains(joined, "B") || !strings.Contains(joined, "│") {
		t.Fatalf("missing natural-fit table header/borders:\n%s", joined)
	}
	if !strings.Contains(joined, "┼") {
		t.Fatalf("missing natural-fit table separator:\n%s", joined)
	}
	if !strings.Contains(joined, "1") || !strings.Contains(joined, "2") {
		t.Fatalf("missing natural-fit table data:\n%s", joined)
	}
}

func TestMarkdownTableHandlesExtremelyNarrowWidth(t *testing.T) {
	m := NewMarkdown("| A | B | C | D | E | F | G | H |\n| --- | --- | --- | --- | --- | --- | --- | --- |\n| body-a | body-b | body-c | body-d | body-e | body-f | body-g | body-h |")
	plain := markdownPlainLines(m.Render(30))
	if len(plain) == 0 {
		t.Fatal("expected output for narrow table")
	}
	joined := strings.Join(plain, "\n")
	for _, want := range []string{"| A | B | C | D", "body-a", "body-h"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("narrow table fallback dropped %q:\n%s", want, joined)
		}
	}
	for _, line := range plain {
		if widthx.VisibleWidth(line) > 30 {
			t.Fatalf("line exceeds width 30: %q (%d)", line, widthx.VisibleWidth(line))
		}
	}
}

func TestMarkdownTableDistributesConstrainedMinimumWidthsProportionally(t *testing.T) {
	m := NewMarkdown("| 1234567890 | aaaaa aaaaa aaaaa aaaaa |\n| --- | --- |\n| x | y |")
	plain := markdownPlainLines(m.Render(20))
	if len(plain) == 0 {
		t.Fatal("expected constrained table output")
	}
	wantTop := "┌─" + strings.Repeat("─", 9) + "─┬─" + strings.Repeat("─", 4) + "─┐"
	if plain[0] != wantTop {
		t.Fatalf("constrained table top border = %q, want %q (all lines: %q)", plain[0], wantTop, plain)
	}
}

func TestMarkdownTableDistributesConstrainedMinimumWidthsWithJavaScriptRounding(t *testing.T) {
	source := "| " + strings.Repeat("a", 2) + " | " + strings.Repeat("b", 20) + " | " + strings.Repeat("c", 27) + " |\n| --- | --- | --- |\n| x | y | z |"
	plain := markdownPlainLines(NewMarkdown(source).Render(36))
	if len(plain) == 0 {
		t.Fatal("expected constrained table output")
	}
	wantTop := "┌─" + strings.Repeat("─", 2) + "─┬─" + strings.Repeat("─", 11) + "─┬─" + strings.Repeat("─", 13) + "─┐"
	if plain[0] != wantTop {
		t.Fatalf("constrained table top border = %q, want JavaScript allocation %q", plain[0], wantTop)
	}
}

func TestMarkdownTableDistributesShrinkWidthsWithJavaScriptRounding(t *testing.T) {
	first := strings.TrimSpace(strings.Repeat("a ", 8))
	second := strings.TrimSpace(strings.Repeat("b ", 16))
	source := "| " + first + " | " + second + " |\n| --- | --- |\n| x | y |"
	plain := markdownPlainLines(NewMarkdown(source).Render(31))
	if len(plain) == 0 {
		t.Fatal("expected squeezed table output")
	}
	wantTop := "┌─" + strings.Repeat("─", 9) + "─┬─" + strings.Repeat("─", 15) + "─┐"
	if plain[0] != wantTop {
		t.Fatalf("squeezed table top border = %q, want JavaScript allocation %q", plain[0], wantTop)
	}
}

func TestMarkdownTableNoTrailingBlankLine(t *testing.T) {
	m := NewMarkdown("| Name |\n| --- |\n| Alice |")
	plain := markdownPlainLines(m.Render(80))
	if plain[len(plain)-1] == "" {
		t.Fatalf("table should not end with trailing blank line: %v", plain)
	}
}

func TestMarkdownListsAndTablesTogether(t *testing.T) {
	m := NewMarkdown("# Test Document\n\n- Item 1\n  - Nested item\n- Item 2\n\n| Col1 | Col2 |\n| --- | --- |\n| A | B |")
	plain := markdownPlainLines(m.Render(80))
	joined := strings.Join(plain, "\n")
	for _, want := range []string{"Test Document", "- Item 1", "  - Nested item", "Col1", "│"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in combined markdown/table render:\n%s", want, joined)
		}
	}
}

func TestMarkdownNestedListsPreserveMarkers(t *testing.T) {
	m := NewMarkdown("- Item 1\n  - Nested 1.1\n  - Nested 1.2\n- Item 2")
	got := markdownPlainLines(m.Render(80))
	joined := strings.Join(got, "\n")
	for _, want := range []string{"- Item 1", "  - Nested 1.1", "  - Nested 1.2", "- Item 2"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %v", want, got)
		}
	}
}

func TestMarkdownMixedOrderedAndNestedLists(t *testing.T) {
	m := NewMarkdown("1. First\n   1. Nested first\n   2. Nested second\n2. Second")
	got := markdownPlainLines(m.Render(80))
	joined := strings.Join(got, "\n")
	for _, want := range []string{"1. First", "   1. Nested first", "   2. Nested second", "2. Second"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %v", want, got)
		}
	}
}

func TestMarkdownLinkFallbackShowsParentheticalURL(t *testing.T) {
	oldCaps := GetCapabilities()
	SetCapabilities(TerminalCapabilities{Hyperlinks: false})
	defer SetCapabilities(oldCaps)

	m := NewMarkdown("[click here](https://example.com)")
	plain := strings.Join(markdownPlainLines(m.Render(80)), " ")
	if !strings.Contains(plain, "click here") || !strings.Contains(plain, "(https://example.com)") {
		t.Fatalf("fallback link rendering mismatch: %q", plain)
	}
}

func TestMarkdownLinkDoesNotDuplicateAutolinks(t *testing.T) {
	oldCaps := GetCapabilities()
	SetCapabilities(TerminalCapabilities{Hyperlinks: false})
	defer SetCapabilities(oldCaps)

	urlPlain := strings.Join(markdownPlainLines(NewMarkdown("Visit https://example.com for more").Render(80)), " ")
	if count := strings.Count(urlPlain, "https://example.com"); count != 1 {
		t.Fatalf("bare URL count = %d, want 1: %q", count, urlPlain)
	}
	emailPlain := strings.Join(markdownPlainLines(NewMarkdown("Contact user@example.com for help").Render(80)), " ")
	if !strings.Contains(emailPlain, "user@example.com") || strings.Contains(emailPlain, "mailto:") {
		t.Fatalf("email autolink mismatch: %q", emailPlain)
	}
}

func TestMarkdownLinkUsesOSC8WhenSupported(t *testing.T) {
	oldCaps := GetCapabilities()
	SetCapabilities(TerminalCapabilities{Hyperlinks: true})
	defer SetCapabilities(oldCaps)

	m := NewMarkdown("[click here](https://example.com)")
	joined := strings.Join(m.Render(80), "")
	if !strings.Contains(joined, "\x1b]8;;https://example.com\x1b\\") {
		t.Fatalf("missing OSC 8 open sequence: %q", joined)
	}
	if !strings.Contains(joined, "\x1b]8;;\x1b\\") {
		t.Fatalf("missing OSC 8 close sequence: %q", joined)
	}
}

func TestMarkdownRendersDeepHeadingsAndTildeFences(t *testing.T) {
	m := NewMarkdown("#### Four\n\n###### Six\n\n~~~go\nfmt.Println(\"ok\")\n~~~")
	lines := m.Render(80)
	plain := markdownPlainLines(lines)
	joined := strings.Join(plain, "\n")
	for _, want := range []string{"#### Four", "###### Six", "```go", "fmt.Println", "```"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("rendered markdown missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "~~~") {
		t.Fatalf("tilde fence rendered as literal text:\n%s", joined)
	}
	for _, heading := range []string{"#### Four", "###### Six"} {
		for i, line := range plain {
			if line == heading && !strings.Contains(lines[i], ActiveTheme().MDHeading) {
				t.Fatalf("heading %q is not styled: %q", heading, lines[i])
			}
		}
	}
}

func TestMarkdown_HeadingInlineCodeRestoresStyle(t *testing.T) {
	m := NewMarkdown("### Why `sourceInfo` should not be optional")
	joined := strings.Join(m.Render(80), "\n")

	// The active theme drives the actual SGR codes; we don't pin specific
	// colors. What we DO assert is structural: after the inline code's
	// scoped foreground reset, the heading SGR is re-applied before
	// "should not be optional".
	idx := strings.Index(joined, "should not be optional")
	if idx <= 0 {
		t.Fatalf("text after inline-code not found: %q", joined)
	}
	preceding := joined[max(0, idx-60):idx]
	if !strings.Contains(preceding, SGRFgReset) {
		t.Errorf("expected scoped foreground reset after inline code; preceding=%q", preceding)
	}
	if !strings.Contains(preceding, ActiveTheme().MDHeading) {
		t.Errorf("expected heading SGR re-applied after reset; preceding=%q", preceding)
	}
}

func TestMarkdown_H1HeadingInlineCodeKeepsUnderline(t *testing.T) {
	m := NewMarkdown("# Title with `code` inside")
	joined := strings.Join(m.Render(80), "\n")

	idx := strings.Index(joined, "inside")
	if idx <= 0 {
		t.Fatalf("text after inline-code not found: %q", joined)
	}
	preceding := joined[max(0, idx-60):idx]
	// H1 base style is "\x1b[1;4m" + headingColor, so look for the
	// underline SGR (either combined "1;4" or bare "4") AFTER the scoped
	// foreground reset.
	reset := strings.LastIndex(preceding, SGRFgReset)
	if reset == -1 {
		t.Fatalf("expected scoped foreground reset before 'inside'; preceding=%q", preceding)
	}
	after := preceding[reset:]
	if !strings.Contains(after, "1;4m") && !strings.Contains(after, "\x1b[4m") {
		t.Errorf("expected H1 underline re-applied after reset; after=%q", after)
	}
}

func TestMarkdownUsesScopedResetsForBgCompatibility(t *testing.T) {
	m := NewMarkdown("# H `code`\n\nPlain **bold** *italic* ~~gone~~ [link](https://example.com)\n\n```go\npackage main\n```")
	joined := strings.Join(m.Render(80), "\n")
	if strings.Contains(joined, SGRResetAll) {
		t.Fatalf("markdown renderer should avoid full SGR resets so bg-painted containers remain continuous; got %q", joined)
	}
	for _, want := range []string{SGRFgReset, SGRBoldDimReset, SGRItalicReset, SGRStrikeReset} {
		if !strings.Contains(joined, want) {
			t.Fatalf("markdown renderer missing scoped reset %q in %q", want, joined)
		}
	}
}

func TestMarkdown_NestedUnorderedList(t *testing.T) {
	src := "- Item 1\n  - Nested 1.1\n  - Nested 1.2\n- Item 2"
	lines := NewMarkdown(src).Render(80)
	plain := stripANSILines(lines)
	for _, want := range []string{"- Item 1", "  - Nested 1.1", "  - Nested 1.2", "- Item 2"} {
		if !anyLineContains(plain, want) {
			t.Errorf("nested list missing %q in:\n%s", want, strings.Join(plain, "\n"))
		}
	}
}

func TestMarkdown_DeeplyNestedUnorderedList(t *testing.T) {
	src := "- Level 1\n  - Level 2\n    - Level 3\n      - Level 4"
	lines := NewMarkdown(src).Render(80)
	plain := stripANSILines(lines)
	for _, want := range []string{"- Level 1", "  - Level 2", "    - Level 3", "      - Level 4"} {
		if !anyLineContains(plain, want) {
			t.Errorf("deeply nested list missing %q in:\n%s", want, strings.Join(plain, "\n"))
		}
	}
}

func TestMarkdown_OrderedNestedList(t *testing.T) {
	src := "1. First\n   1. Nested first\n   2. Nested second\n2. Second"
	lines := NewMarkdown(src).Render(80)
	plain := stripANSILines(lines)
	for _, want := range []string{"1. First", "1. Nested first", "2. Nested second", "2. Second"} {
		if !anyLineContains(plain, want) {
			t.Errorf("ordered nested missing %q in:\n%s", want, strings.Join(plain, "\n"))
		}
	}
}

func stripANSILines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = stripANSI(l)
	}
	return out
}

func anyLineContains(lines []string, needle string) bool {
	for _, l := range lines {
		if strings.Contains(l, needle) {
			return true
		}
	}
	return false
}
