package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"github.com/MichaelKinsy/PiG/internal/latex"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// LaTeX math rendering inside markdown. Faithful port of the latex tokenizer
// extensions and render seams in upstream pi's tui/src/components/markdown.ts:
// inline `$...$`, `\(...\)`, `\[...\]` and block `$$...$$`, `\[...\]`, with the
// same $-disambiguation heuristics, escape handling, and pending (unclosed,
// streaming) behavior. Rendering delegates to internal/latex (RenderLatex).

// markdownJSSpace is the inside of JavaScript's \s class; Go's \s is ASCII-only and lacks \v and the Unicode spaces.
const markdownJSSpace = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

var (
	reMdPendingDollarMath = lazyregexp.New(`\\[A-Za-z]+|[_^=+*/<>()[\]|±≤≥≠≈∈→⇒∞∫∑√-]`)
	reMdInlineDollarSpace = lazyregexp.New(`^\$[` + markdownJSSpace + `]`)
	reMdTrailingSpace     = lazyregexp.New(`[` + markdownJSSpace + `]$`)
	reMdEnvVarInner       = lazyregexp.New(`^[A-Z_][A-Z0-9_]*(?:[^A-Za-z0-9_` + markdownJSSpace + `])?$`)
	reMdBlockDollar       = lazyregexp.New(`(?s)^ {0,3}\$\$[ \t]*(?:\n)?(.*?)\$\$[ \t]*(?:\n|$)`)
	reMdBlockBracket      = lazyregexp.New(`(?s)^ {0,3}\\\[[ \t]*(?:\n)?(.*?)\\\][ \t]*(?:\n|$)`)
	// markdown.ts TABLE_DELIMITER_ROW_REGEX (2ed3118b, #11159). No multiline flag: $ is the end of the line it tests.
	reMdTableDelimiterRow = lazyregexp.New(`^ {0,3}\|?[ \t]*:?-+:?[ \t]*(?:\|[ \t]*:?-+:?[ \t]*)*\|?[ \t]*$`)
)

// latexToken mirrors the upstream LatexToken shape.
type latexToken struct {
	raw     string
	text    string
	pending bool
}

// mdIsEscaped reports whether the byte at index follows an odd number of backslashes.
func mdIsEscaped(src string, index int) bool {
	backslashes := 0
	for p := index - 1; p >= 0 && src[p] == '\\'; p-- {
		backslashes++
	}
	return backslashes%2 == 1
}

// mdFindClosingDelimiter returns the byte index of the first unescaped closing at or after start, or -1. The source stays a string:
// the opener's cost is the distance to its closing delimiter, not the length of the rest of the paragraph, as Pi's
// String.indexOf over a slice that shares the source's storage is.
func mdFindClosingDelimiter(src, closing string, start int) int {
	for from := start; from <= len(src); {
		i := strings.Index(src[from:], closing)
		if i < 0 {
			return -1
		}
		idx := from + i
		if !mdIsEscaped(src, idx) {
			return idx
		}
		from = idx + len(closing)
	}
	return -1
}

// tokenizeInlineLatex ports markdown.ts tokenizeInlineLatex: source begins at a
// candidate opener; returns the token and true when an inline latex span (or a
// pending unclosed one) is recognized. Every index is a byte index of source, which is valid UTF-8, and no step
// copies more than the span it recognizes.
func tokenizeInlineLatex(source string) (latexToken, bool) {
	var opening, closing string
	switch {
	case strings.HasPrefix(source, "$$"):
		opening, closing = "$$", "$$"
	case strings.HasPrefix(source, "\\("):
		opening, closing = "\\(", "\\)"
	case strings.HasPrefix(source, "\\["):
		opening, closing = "\\[", "\\]"
	case strings.HasPrefix(source, "$") && !reMdInlineDollarSpace.MatchString(source[:min(len(source), utf8.UTFMax)]):
		opening, closing = "$", "$"
	default:
		return latexToken{}, false
	}
	openLen := len(opening)

	closingIndex := mdFindClosingDelimiter(source, closing, openLen)
	if closingIndex >= 0 && opening == "$" {
		inner := source[openLen:closingIndex]
		after := source[closingIndex+1:]
		if reMdTrailingSpace.MatchString(inner) ||
			mdStartsWithDigit(after) ||
			(reMdEnvVarInner.MatchString(inner) && mdStartsWithIdentifier(after)) ||
			strings.Contains(inner, "`") {
			return latexToken{}, false
		}
	}

	if closingIndex < 0 {
		pendingSource := source[openLen:]
		if strings.HasPrefix(opening, "\\") || looksLikePendingDollarMath(pendingSource) {
			return latexToken{raw: source, text: pendingSource, pending: true}, true
		}
		return latexToken{}, false
	}

	text := source[openLen:closingIndex]
	if text == "" || strings.Contains(text, "\n") {
		return latexToken{}, false
	}
	return latexToken{raw: source[:closingIndex+len(closing)], text: text}, true
}

// mdStartsWithDigit is /^\d/: JavaScript's \d is ASCII.
func mdStartsWithDigit(s string) bool { return s != "" && s[0] >= '0' && s[0] <= '9' }

// mdStartsWithIdentifier is /^[A-Za-z_][A-Za-z0-9_]*/ matching: its first character decides.
func mdStartsWithIdentifier(s string) bool {
	return s != "" && (s[0] == '_' || s[0] >= 'a' && s[0] <= 'z' || s[0] >= 'A' && s[0] <= 'Z')
}

func looksLikePendingDollarMath(source string) bool {
	return reMdPendingDollarMath.MatchString(source)
}

// containsTableStart ports markdown.ts containsTableStart (2ed3118b, #11159): a GFM table starts with a header row
// followed by a delimiter row such as "|---|---|" or "| --- |". The header needs a "|" only when the delimiter has
// none: a pipe-free delimiter without a pipe in the header is a thematic break, not a table.
func containsTableStart(source string) bool {
	// Walks the lines in place and stops at the first table start: the unclosed-math fallbacks run once per display-math
	// opener, and a split of the whole rest of the document on every opener made a long reply quadratic.
	line, rest, ok := strings.Cut(source, "\n")
	for ok {
		var next string
		next, rest, ok = strings.Cut(rest, "\n")
		if (strings.Contains(next, "|") || strings.Contains(line, "|")) && reMdTableDelimiterRow.MatchString(next) {
			return true
		}
		line = next
	}
	return false
}

// mdPendingBody is reMdPendingDollar and reMdPendingBracket without a regexp: it returns what `(?s)^ {0,3}<opener>[ \t]*(?:\n)?(.*)$`
// captures, the whole rest of the document. The regexp read every byte of the rest on every opener that
// containsTableStart then refuses.
func mdPendingBody(source, opener string) (string, bool) {
	spaces := 0
	for spaces < len(source) && spaces <= 3 && source[spaces] == ' ' {
		spaces++
	}
	if spaces > 3 || !strings.HasPrefix(source[spaces:], opener) {
		return "", false
	}
	rest := source[spaces+len(opener):]
	rest = strings.TrimLeft(rest, " \t")
	return strings.TrimPrefix(rest, "\n"), true
}

// tokenizeBlockLatex ports markdown.ts tokenizeBlockLatex over the remaining
// source (joined lines). Returns the block token and true on a match.
// Display math never spans a table (2ed3118b, #11159): without the containsTableStart guards an unclosed "$$" or
// "\[" pairs with a later opener or stays pending, swallowing the table.
func tokenizeBlockLatex(source string) (latexToken, bool) {
	// Every form below starts with up to three spaces and then "$$" or "\[". Most blocks start with neither, and each
	// regexp attempt on a long remaining document pays for its length before it fails.
	spaces := 0
	for spaces < len(source) && spaces <= 3 && source[spaces] == ' ' {
		spaces++
	}
	if spaces > 3 || !strings.HasPrefix(source[spaces:], "$$") && !strings.HasPrefix(source[spaces:], `\[`) {
		return latexToken{}, false
	}
	if m := reMdBlockDollar.FindStringSubmatch(source); m != nil && m[1] != "" && !containsTableStart(m[1]) {
		return latexToken{raw: m[0], text: widthx.JSTrim(m[1])}, true
	}
	if m := reMdBlockBracket.FindStringSubmatch(source); m != nil && m[1] != "" && !containsTableStart(m[1]) {
		return latexToken{raw: m[0], text: widthx.JSTrim(m[1])}, true
	}
	if body, ok := mdPendingBody(source, `\[`); ok && body != "" && !containsTableStart(body) {
		return latexToken{raw: source, text: body, pending: true}, true
	}
	if body, ok := mdPendingBody(source, "$$"); ok && body != "" && looksLikePendingDollarMath(body) && !containsTableStart(body) {
		return latexToken{raw: source, text: body, pending: true}, true
	}
	return latexToken{}, false
}

// renderInlineLatex mirrors the markdown.ts "latex" render case: unchanged raw
// when pending, else RenderLatex(text) with the raw as fallback.
func renderInlineLatex(tok latexToken) string {
	if tok.pending {
		return tok.raw
	}
	if rendered, ok := latex.RenderLatex(tok.text, latex.RenderLatexOptions{}); ok {
		return rendered
	}
	return tok.raw
}

// renderBlockLatex mirrors the "latexBlock" render case: raw.trim() when
// pending, else RenderLatex(text, display) with raw.trim() as fallback.
func renderBlockLatex(tok latexToken) string {
	if tok.pending {
		return widthx.JSTrim(tok.raw)
	}
	if rendered, ok := latex.RenderLatex(tok.text, latex.RenderLatexOptions{Display: true}); ok {
		return rendered
	}
	return widthx.JSTrim(tok.raw)
}

// blockLatexStart reports whether a line begins a block latex token
// (≤3 leading spaces then `$$` or `\[`), matching the upstream start hint.
func blockLatexStart(line string) bool {
	trimmed := line
	for i := 0; i < 3 && strings.HasPrefix(trimmed, " "); i++ {
		trimmed = trimmed[1:]
	}
	return strings.HasPrefix(trimmed, "$$") || strings.HasPrefix(trimmed, "\\[")
}
