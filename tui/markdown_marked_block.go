package tui

// Ports node_modules/marked@18.0.11/src/Tokenizer.ts:514-543,545-606 (html, def and table), node_modules/marked@18.0.11/src/helpers.ts:38-78,112-124 (splitCells, trimTrailingBlankLines) and the GFM block rules of node_modules/marked@18.0.11/src/rules.ts:106-244 that end a paragraph, define a link or open an HTML block or table.
// Ports packages/tui/src/components/markdown.ts (its marked block lexer rules).

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"

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
	label = jsstring.ToLower(label)
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

// markedBlankLine is marked's blank line, /^[ \t]*$/: only spaces and tabs, so a line holding U+00A0, U+3000, U+FEFF or another Unicode space is text.
func markedBlankLine(line string) bool { return strings.Trim(line, " \t") == "" }
