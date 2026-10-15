package tui

// Ports node_modules/marked@18.0.11/src/Tokenizer.ts:514-543,545-606 (html, def and table), node_modules/marked@18.0.11/src/helpers.ts:38-78,112-124 (splitCells, trimTrailingBlankLines) and the GFM block rules of node_modules/marked@18.0.11/src/rules.ts:106-244 that end a paragraph, define a link or open an HTML block or table.

import (
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// The block rule pieces as marked's edit() composes them (rules.ts:109-244). Literal tabs are the "\t" characters of marked's string-built sources.
const (
	markedHTMLTagNames       = `address|article|aside|base|basefont|blockquote|body|caption|center|col|colgroup|dd|details|dialog|dir|div|dl|dt|fieldset|figcaption|figure|footer|form|frame|frameset|h[1-6]|head|header|hr|html|iframe|legend|li|link|main|menu|menuitem|meta|nav|noframes|ol|optgroup|option|p|param|search|section|summary|table|tbody|td|tfoot|th|thead|title|tr|track|ul`
	markedBlockHrRule        = ` {0,3}((?:-[\t ]*){3,}|(?:_[ \t]*){3,}|(?:\*[ \t]*){3,})(?:\n+|$)`
	markedHeadingInterrupt   = ` {0,3}#{1,6}(?:\s|$)`
	markedBlockquoteStart    = ` {0,3}>`
	markedFencesInterrupt    = " {0,3}(?:`{3,}(?=[^`\\n]*(?:\\n|$))|~~~)[^\\n]*(?:\\n|$)"
	markedHTMLInterrupt      = `<\/?(?:` + markedHTMLTagNames + `)(?: +|\n|\/?>)|<(?:script|pre|style|textarea|!--)`
	markedGfmTableBody       = ` *([^\n ].*)\n {0,3}((?:\| *)?:?-+:? *(?:\| *:?-+:? *)*(?:\| *)?)(?:\n((?:(?! *\n|` + markedBlockHrRule + `|` + markedHeadingInterrupt + `|` + markedBlockquoteStart + "|(?: {4}| {0,3}\t)[^\\n]|" + markedFencesInterrupt + `| {0,3}(?:[*+-]|1[.)])[ \t]|` + markedHTMLInterrupt + `).*(?:\n|$))*)\n*|$)`
	markedParagraphInterrupt = markedBlockHrRule + `|` + markedHeadingInterrupt + `|` + markedBlockquoteStart + `|` + markedFencesInterrupt + `| {0,3}(?:[*+-]|1[.)])[ \t]+[^ \t\n]|` + markedHTMLInterrupt + `|` + markedGfmTableBody + `|[ \t]+\n`
	// markedQuoteInterrupt is the paragraph rule inside a blockquote (Ae's Me): the GFM interrupts without lheading and table, and a list item of any start number.
	markedQuoteInterrupt       = markedBlockHrRule + `|` + markedHeadingInterrupt + `|` + markedBlockquoteStart + `|` + markedFencesInterrupt + `| {0,3}(?:[*+-]|\d{1,9}[.)])(?:[ \t]|\n|$)|` + markedHTMLInterrupt + `|[ \t]+\n`
	markedBlockHTMLSource      = "^ {0,3}(?:<(script|pre|style|textarea)[\\s>][\\s\\S]*?(?:<\\/\\1>[^\\n]*\\n*|$)|<!--(?:-?>|[\\s\\S]*?(?:-->|$))[^\\n]*(\\n+|$)|<\\?[\\s\\S]*?(?:\\?>[^\\n]*\\n*|$)|<![A-Z][\\s\\S]*?(?:>[^\\n]*\\n*|$)|<!\\[CDATA\\[[\\s\\S]*?(?:\\]\\]>[^\\n]*\\n*|$)|<\\/?(" + markedHTMLTagNames + ")(?: +|\\n|\\/?>)[\\s\\S]*?(?:(?:\\n[ \t]*)+\\n|$)|<(?!script|pre|style|textarea)([a-z][\\w-]*)(?: +[a-zA-Z:_][\\w.:-]*(?: *= *\"[^\"\\n]*\"| *= *'[^'\\n]*'| *= *[^\\s\"'=<>`]+)?)*? *\\/?>(?=[ \\t]*(?:\\n|$))[\\s\\S]*?(?:(?:\\n[ \t]*)+\\n|$)|<\\/(?!script|pre|style|textarea)[a-z][\\w-]*\\s*>(?=[ \\t]*(?:\\n|$))[\\s\\S]*?(?:(?:\\n[ \t]*)+\\n|$))"
	markedBlockTableSource     = `^` + markedGfmTableBody
	markedBlockParagraphSource = `^([^\n]+(?:\n(?!` + markedParagraphInterrupt + `)[^\n]+)*)`
)

var (
	markedBlockHTML  = newAnchoredMarkedRegexp(markedBlockHTMLSource, true)
	markedBlockCode  = newAnchoredMarkedRegexp(`^((?: {4}| {0,3}\t)[^\n]+(?:\n(?:[ \t]*(?:\n|$))*)?)+`, false)
	markedBlockTable = newAnchoredMarkedRegexp(markedBlockTableSource, false)
	markedBlockHr    = newAnchoredMarkedRegexp(`^`+markedBlockHrRule, false)
	// markedBlockDef is the link reference definition rule (rules.ts:134-137).
	markedBlockDef = newAnchoredMarkedRegexp(`^ {0,3}\[((?!\s*\])(?:\\[\s\S]|[^\[\]\\])+)\]: *(?:\n[ \t]*)?([^<\s][^\s]*|<.*?>)(?:(?: +(?:\n[ \t]*)?| *\n[ \t]*)((?:"(?:\\"?|[^"\\])*"|'[^'\n]*(?:\n[^'\n]+)*\n?'|\([^()]*\))))? *(?:\n+|$)`, false)
	// markedParagraphInterruptAt is the lookahead of the GFM paragraph rule: a line it matches ends the paragraph before it.
	markedParagraphInterruptAt = newAnchoredMarkedRegexp(`^(?:`+markedParagraphInterrupt+`)`, false)
	markedQuoteInterruptAt     = newAnchoredMarkedRegexp(`^(?:`+markedQuoteInterrupt+`)`, false)
)

// markdownBlockSource gives block rules the remaining source from a line start, as marked's block lexer sees src. The rune form is built once per render on first use.
type markdownBlockSource struct {
	lines   []string
	runes   []rune
	offsets []int
	// lazy are the indexes of lines marked joined to the previous paragraph of a quote: a delimiter row among them starts no table under a header that is not one of them.
	lazy []int
	// indented are the lazy lines whose four leading spaces the quote removed: an indented line is no block start, so it never interrupts a paragraph.
	indented []int
	// breaks are the lazy lines that start a block of their own: they end the paragraph before them.
	breaks []int
}

func (s *markdownBlockSource) from(line int) []rune {
	if s.offsets == nil {
		s.offsets = make([]int, len(s.lines)+1)
		for i, l := range s.lines {
			s.runes = append(s.runes, []rune(l)...)
			s.runes = append(s.runes, '\n')
			s.offsets[i+1] = len(s.runes)
		}
		s.runes = s.runes[:len(s.runes)-1]
	}
	return s.runes[min(s.offsets[line], len(s.runes)):]
}

// markedLinesConsumed is the number of source lines a block token with this raw text covers when the next token starts on the following line.
func markedLinesConsumed(raw string) int {
	n := strings.Count(raw, "\n")
	if !strings.HasSuffix(raw, "\n") {
		n++
	}
	return n
}

// paragraphInterrupted reports whether lines[index] ends a paragraph under marked's GFM paragraph rule: a blank or whitespace-only line, a rule, ATX heading, blockquote, fence, non-empty list item starting at 1, HTML block start, or a table header and delimiter row.
func (s *markdownBlockSource) paragraphInterrupted(index int) bool {
	if slices.Contains(s.breaks, index) {
		return true
	}
	if slices.Contains(s.indented, index) {
		return false
	}
	line := s.lines[index]
	if line == "" {
		return true
	}
	if strings.Trim(line, " \t") == "" {
		// [ \t]+\n: a whitespace-only line ends the paragraph only when a newline follows it. A quote's lazy line is lexed apart from the quoted lines, so no newline follows the quoted line before it (`> q`, `>  `, `x` is one paragraph).
		return index+1 < len(s.lines) && !slices.Contains(s.lazy, index+1)
	}
	// Every other alternative needs one of these starts, or a delimiter row next for a table.
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) <= 3 && strings.ContainsRune("-_*#>`~+1", rune(trimmed[0])) || line[0] == '<' ||
		index+1 < len(s.lines) && markedDelimiterRowShape(s.lines[index+1]) {
		// No alternative reads past the following line: a table's rows may be empty and every other alternative ends at a newline or the end.
		window := line
		// A lazy line is lexed apart from the quoted lines before it, so a delimiter row on one interrupts the paragraph only as the delimiter of a table whose header is lazy too and has as many cells.
		if index+1 < len(s.lines) {
			if _, _, ok := s.table(index); ok || !slices.Contains(s.lazy, index+1) {
				window += "\n" + s.lines[index+1]
			}
		}
		return markedParagraphInterruptAt.exec([]rune(window)) != nil
	}
	return false
}

// quoteLazyInterrupted reports whether lines[index] ends a blockquote's paragraph, so that it is not a lazy line of the quote: the paragraph rule without table or lheading.
func (s *markdownBlockSource) quoteLazyInterrupted(index int) bool {
	line := s.lines[index]
	if line == "" {
		return true
	}
	if strings.Trim(line, " \t") == "" {
		return index+1 < len(s.lines)
	}
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) <= 3 && strings.ContainsRune("-_*#>`~+0123456789", rune(trimmed[0])) || line[0] == '<' {
		window := line
		if index+1 < len(s.lines) {
			window += "\n" + s.lines[index+1]
		}
		return markedQuoteInterruptAt.exec([]rune(window)) != nil
	}
	return false
}

// isMarkedHr is the GFM hr rule, /^ {0,3}((?:-[\t ]*){3,}|(?:_[ \t]*){3,}|(?:\*[ \t]*){3,})(?:\n+|$)/, on one line without a regexp; the rule never reads past the line's newline.
func isMarkedHr(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	if trimmed == "" || len(line)-len(trimmed) > 3 {
		return false
	}
	c := trimmed[0]
	if c != '-' && c != '_' && c != '*' {
		return false
	}
	n := 0
	for i := range len(trimmed) {
		switch trimmed[i] {
		case c:
			n++
		case ' ', '\t':
		default:
			return false
		}
	}
	return n >= 3
}

// startsHTMLBlock reports whether Tokenizer.html matches at the line.
func (s *markdownBlockSource) startsHTMLBlock(index int) bool {
	_, _, ok := s.htmlBlock(index)
	return ok
}

// htmlBlock ports Tokenizer.html: the raw HTML with its trailing blank lines trimmed, and the index of the line after it.
func (s *markdownBlockSource) htmlBlock(index int) (raw string, next int, ok bool) {
	if !strings.HasPrefix(strings.TrimLeft(s.lines[index], " "), "<") {
		return "", index, false
	}
	match := markedBlockHTML.exec(s.from(index))
	if match == nil {
		return "", index, false
	}
	raw = markedTrimTrailingBlankLines(match.String())
	return raw, index + markedLinesConsumed(raw), true
}

// def ports Tokenizer.def (Tokenizer.ts:529-543): the normalized label, the destination without its angle brackets and escapes, and the index of the line after the definition, whose raw text (without trailing newlines) covers whole lines. pi-tui renders a link's href and never its title, so the title is not returned.
func (s *markdownBlockSource) def(index int) (tag, href string, next int, ok bool) {
	if !markedDefCandidate(s.lines[index]) {
		return "", "", index, false
	}
	match := markedBlockDef.exec(s.from(index))
	if match == nil {
		return "", "", index, false
	}
	label, _ := markedGroup(match, 1)
	destination, _ := markedGroup(match, 2)
	// hrefBrackets, /^<(.*)>$/; the destination rule admits no line terminator for "." to reject.
	if len(destination) >= 2 && destination[0] == '<' && destination[len(destination)-1] == '>' {
		destination = destination[1 : len(destination)-1]
	}
	raw := strings.TrimRight(match.String(), "\n")
	return markedLinkLabel(label), markedUnescapePunctuation(destination), index + markedLinesConsumed(raw), true
}

// markedDefCandidate rejects, without the regexp, a line that cannot start a definition: one not opening with "[" after at most three spaces, or one whose label visibly ends without "]:". The label ends at the first "[" or "]" unless a backslash escapes it, and may continue on the next line.
func markedDefCandidate(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || !strings.HasPrefix(trimmed, "[") {
		return false
	}
	end := strings.IndexAny(trimmed[1:], `[]\`)
	if end < 0 {
		return true
	}
	switch trimmed[1+end] {
	case '[':
		return false
	case ']':
		return strings.HasPrefix(trimmed[2+end:], ":")
	}
	return true
}

// markedLinkLabel is a definition tag or reference key as marked forms it: lower-cased (String.prototype.toLowerCase) with every whitespace run replaced by one space (rules.ts multipleSpaceGlobal).
func markedLinkLabel(label string) string {
	label = cases.Lower(language.Und).String(label)
	var out strings.Builder
	space := false
	for _, r := range label {
		if jsstring.IsSpace(r) {
			space = true
			continue
		}
		if space {
			out.WriteByte(' ')
			space = false
		}
		out.WriteRune(r)
	}
	if space {
		out.WriteByte(' ')
	}
	return out.String()
}

// indentedCode ports Tokenizer.code (Tokenizer.ts:102-116): the code lines with their indentation removed, and the index of the line after the block.
func (s *markdownBlockSource) indentedCode(index int) (code []string, next int, ok bool) {
	line := s.lines[index]
	if !strings.HasPrefix(line, "    ") && !strings.HasPrefix(strings.TrimLeft(line, " "), "\t") {
		return nil, index, false
	}
	// The newline rule, /^(?:[ \t]*(?:\n|$))+/, runs before the code rule: a whitespace-only line is a space token.
	if strings.Trim(line, " \t") == "" {
		return nil, index, false
	}
	match := markedBlockCode.exec(s.from(index))
	if match == nil {
		return nil, index, false
	}
	raw := markedTrimTrailingBlankLines(match.String())
	code = strings.Split(raw, "\n")
	for i, l := range code {
		// codeRemoveIndent, /^(?: {1,4}| {0,3}\t)/gm: the space alternative wins whenever a line starts with a space.
		if spaces := len(l) - len(strings.TrimLeft(l, " ")); spaces > 0 {
			code[i] = l[min(spaces, 4):]
		} else {
			code[i] = strings.TrimPrefix(l, "\t")
		}
	}
	return code, index + markedLinesConsumed(raw), true
}

// markedTrimTrailingBlankLines ports helpers.ts:112-124: a single trailing blank line stays.
func markedTrimTrailingBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	end := len(lines) - 1
	for end >= 0 && strings.Trim(lines[end], " \t") == "" {
		end--
	}
	if len(lines)-end <= 2 {
		return s
	}
	return strings.Join(lines[:end+1], "\n")
}

// markdownTable is a GFM table token: header and row cell sources, and raw for the narrow-width fallback.
type markdownTable struct {
	header []string
	rows   [][]string
	raw    string
	// foldsNewline is whether marked's lexer appends one newline to raw (a one-newline space token after the table).
	foldsNewline bool
}

// table ports Tokenizer.table without alignment, which pi-tui does not render.
func (s *markdownBlockSource) table(index int) (table markdownTable, next int, ok bool) {
	if index+1 >= len(s.lines) || !markedDelimiterRowShape(s.lines[index+1]) || slices.Contains(s.lazy, index+1) && !slices.Contains(s.lazy, index) {
		return markdownTable{}, index, false
	}
	match := markedBlockTable.exec(s.from(index))
	if match == nil {
		return markdownTable{}, index, false
	}
	header, _ := markedGroup(match, 1)
	delimiter, _ := markedGroup(match, 2)
	cells, _ := markedGroup(match, 3)
	// A delimiter row without a pipe or colon is a setext underline.
	if !strings.ContainsAny(delimiter, ":|") {
		return markdownTable{}, index, false
	}
	table.header = markedSplitCells(header, 0)
	// tableAlignChars, /^\||\| *$/g, removes the outer pipes before the delimiter row splits into columns.
	aligns := strings.TrimPrefix(delimiter, "|")
	if trimmed := strings.TrimRight(aligns, " "); strings.HasSuffix(trimmed, "|") {
		aligns = trimmed[:len(trimmed)-1]
	}
	if len(table.header) != strings.Count(aligns, "|")+1 {
		return markdownTable{}, index, false
	}
	if widthx.JSTrim(cells) != "" {
		// tableRowBlankLine, /\n[ \t]*$/, drops a final whitespace-only line.
		if last := strings.LastIndexByte(cells, '\n'); last >= 0 && strings.Trim(cells[last+1:], " \t") == "" {
			cells = cells[:last]
		}
		for row := range strings.SplitSeq(cells, "\n") {
			table.rows = append(table.rows, markedSplitCells(row, len(table.header)))
		}
	}
	table.raw = strings.TrimRight(match.String(), "\n")
	next = index + strings.Count(table.raw, "\n") + 1
	// Lexer.ts adds a space token of one newline to the previous token's raw: the single newline after the table, when the text ends there or the next line has content.
	table.foldsNewline = next < len(s.lines) && (strings.Trim(s.lines[next], " \t") != "" || next == len(s.lines)-1)
	return table, next, true
}

// markedDelimiterRowShape is a cheap necessary condition for the table delimiter row: only spaces, pipes, colons and at least one dash.
func markedDelimiterRowShape(line string) bool {
	return strings.Contains(line, "-") && strings.Trim(line, " |:-") == ""
}

// markedSplitCells ports helpers.ts:38-78: unescaped pipes split cells, an empty first or last cell without its pipe is dropped, and count pads or truncates.
func markedSplitCells(row string, count int) []string {
	var spaced strings.Builder
	for i := range len(row) {
		if row[i] != '|' {
			spaced.WriteByte(row[i])
			continue
		}
		escaped := false
		for k := i - 1; k >= 0 && row[k] == '\\'; k-- {
			escaped = !escaped
		}
		if escaped {
			spaced.WriteByte('|')
		} else {
			spaced.WriteString(" |")
		}
	}
	cells := strings.Split(spaced.String(), " |")
	if widthx.JSTrim(cells[0]) == "" {
		cells = cells[1:]
	}
	if len(cells) > 0 && widthx.JSTrim(cells[len(cells)-1]) == "" {
		cells = cells[:len(cells)-1]
	}
	if count > 0 {
		if len(cells) > count {
			cells = cells[:count]
		}
		for len(cells) < count {
			cells = append(cells, "")
		}
	}
	for i := range cells {
		cells[i] = strings.ReplaceAll(widthx.JSTrim(cells[i]), `\|`, "|")
	}
	return cells
}

// markdownQuoteEndsInCode reports that the last token of a quote's quoted lines is a code token, which a lazy line cannot continue: marked's blockquote tokenizer breaks out when the last token is code (`if (lastToken?.type === 'code') break`), and the lazy line starts a block after the quote.
func markdownQuoteEndsInCode(quoted []string) bool {
	const (
		other = iota
		paragraph
		list
		code
	)
	kind := other
	var fence byte
	var fenceLen int
	inFence := false
	for _, line := range quoted {
		if inFence {
			inFence = !isMarkdownFenceClose(line, fence, fenceLen)
			continue
		}
		if strings.Trim(line, " \t") == "" {
			kind = other
			continue
		}
		indent := markdownIndent(line)
		switch {
		case kind == list && indent >= 2:
		case indent >= 4 && kind != paragraph:
			kind = code
		case isBlockquoteLine(line):
			kind = other
		default:
			if f, n, _, ok := parseMarkdownFenceOpen(line); ok {
				fence, fenceLen, inFence, kind = f, n, true, code
			} else if _, _, _, ok := parseMarkdownListItem(line); ok {
				kind = list
			} else if kind != paragraph {
				kind = paragraph
			}
		}
	}
	return kind == code
}

// markdownTextEndsAt reports that no token follows the lines before index: the rest is empty or the one newline that ends the text (a blank line before the end is a space token).
func markdownTextEndsAt(lines []string, index int) bool {
	if len(lines) >= 2 && lines[len(lines)-1] == "" && lines[len(lines)-2] == "" {
		return false
	}
	return index >= len(lines) || index == len(lines)-1 && lines[index] == ""
}

// markedBlankLine is marked's blank line, /^[ \t]*$/: only spaces and tabs, so a line holding U+00A0, U+3000, U+FEFF or another Unicode space is text.
func markedBlankLine(line string) bool { return strings.Trim(line, " \t") == "" }
