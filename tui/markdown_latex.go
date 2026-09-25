package tui

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/latex"
)

// LaTeX math rendering inside markdown. Faithful port of the latex tokenizer
// extensions and render seams in upstream pi's tui/src/components/markdown.ts:
// inline `$...$`, `\(...\)`, `\[...\]` and block `$$...$$`, `\[...\]`, with the
// same $-disambiguation heuristics, escape handling, and pending (unclosed,
// streaming) behavior. Rendering delegates to internal/latex (RenderLatex).

var (
	reMdPendingDollarMath = regexp.MustCompile(`\\[A-Za-z]+|[_^=+*/<>()[\]|±≤≥≠≈∈→⇒∞∫∑√-]`)
	reMdInlineDollarSpace = regexp.MustCompile(`^\$\s`)
	reMdTrailingSpace     = regexp.MustCompile(`\s$`)
	reMdLeadingDigit      = regexp.MustCompile(`^\d`)
	reMdEnvVarInner       = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*(?:[^A-Za-z0-9_\s])?$`)
	reMdIdentAfter        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)
	reMdBlockDollar       = regexp.MustCompile(`(?s)^ {0,3}\$\$[ \t]*(?:\n)?(.*?)\$\$[ \t]*(?:\n|$)`)
	reMdBlockBracket      = regexp.MustCompile(`(?s)^ {0,3}\\\[[ \t]*(?:\n)?(.*?)\\\][ \t]*(?:\n|$)`)
	reMdPendingBracket    = regexp.MustCompile(`(?s)^ {0,3}\\\[[ \t]*(?:\n)?(.*)$`)
	reMdPendingDollar     = regexp.MustCompile(`(?s)^ {0,3}\$\$[ \t]*(?:\n)?(.*)$`)
)

// latexToken mirrors the upstream LatexToken shape.
type latexToken struct {
	raw     string
	text    string
	pending bool
}

func mdIsEscaped(src []rune, index int) bool {
	backslashes := 0
	for p := index - 1; p >= 0 && src[p] == '\\'; p-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func runeIndexOfStr(src []rune, sub string, from int) int {
	subR := []rune(sub)
	if from < 0 {
		from = 0
	}
	for i := from; i+len(subR) <= len(src); i++ {
		match := true
		for j := range subR {
			if src[i+j] != subR[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func mdFindClosingDelimiter(src []rune, closing string, start int) int {
	idx := runeIndexOfStr(src, closing, start)
	for idx >= 0 && mdIsEscaped(src, idx) {
		idx = runeIndexOfStr(src, closing, idx+utf8.RuneCountInString(closing))
	}
	return idx
}

// tokenizeInlineLatex ports markdown.ts tokenizeInlineLatex: source begins at a
// candidate opener; returns the token and true when an inline latex span (or a
// pending unclosed one) is recognized.
func tokenizeInlineLatex(source string) (latexToken, bool) {
	src := []rune(source)
	var opening, closing string
	switch {
	case strings.HasPrefix(source, "$$"):
		opening, closing = "$$", "$$"
	case strings.HasPrefix(source, "\\("):
		opening, closing = "\\(", "\\)"
	case strings.HasPrefix(source, "\\["):
		opening, closing = "\\[", "\\]"
	case strings.HasPrefix(source, "$") && !reMdInlineDollarSpace.MatchString(source):
		opening, closing = "$", "$"
	default:
		return latexToken{}, false
	}
	openLen := utf8.RuneCountInString(opening)

	closingIndex := mdFindClosingDelimiter(src, closing, openLen)
	if closingIndex >= 0 && opening == "$" {
		inner := string(src[openLen:closingIndex])
		after := string(src[closingIndex+1:])
		if reMdTrailingSpace.MatchString(inner) ||
			reMdLeadingDigit.MatchString(after) ||
			(reMdEnvVarInner.MatchString(inner) && reMdIdentAfter.MatchString(after)) ||
			strings.Contains(inner, "`") {
			return latexToken{}, false
		}
	}

	if closingIndex < 0 {
		pendingSource := string(src[openLen:])
		if strings.HasPrefix(opening, "\\") || looksLikePendingDollarMath(pendingSource) {
			return latexToken{raw: source, text: pendingSource, pending: true}, true
		}
		return latexToken{}, false
	}

	text := string(src[openLen:closingIndex])
	if text == "" || strings.Contains(text, "\n") {
		return latexToken{}, false
	}
	raw := string(src[:closingIndex+utf8.RuneCountInString(closing)])
	return latexToken{raw: raw, text: text}, true
}

func looksLikePendingDollarMath(source string) bool {
	return reMdPendingDollarMath.MatchString(source)
}

// tokenizeBlockLatex ports markdown.ts tokenizeBlockLatex over the remaining
// source (joined lines). Returns the block token and true on a match.
func tokenizeBlockLatex(source string) (latexToken, bool) {
	if m := reMdBlockDollar.FindStringSubmatch(source); m != nil && strings.TrimSpace(m[1]) != "" {
		return latexToken{raw: m[0], text: strings.TrimSpace(m[1])}, true
	}
	if m := reMdBlockBracket.FindStringSubmatch(source); m != nil && strings.TrimSpace(m[1]) != "" {
		return latexToken{raw: m[0], text: strings.TrimSpace(m[1])}, true
	}
	if m := reMdPendingBracket.FindStringSubmatch(source); m != nil {
		return latexToken{raw: m[0], text: m[1], pending: true}, true
	}
	if m := reMdPendingDollar.FindStringSubmatch(source); m != nil && m[1] != "" && looksLikePendingDollarMath(m[1]) {
		return latexToken{raw: m[0], text: m[1], pending: true}, true
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
		return strings.TrimSpace(tok.raw)
	}
	if rendered, ok := latex.RenderLatex(tok.text, latex.RenderLatexOptions{Display: true}); ok {
		return rendered
	}
	return strings.TrimSpace(tok.raw)
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
