package tui

// Ports node_modules/marked@18.0.11/src/Lexer.ts:340-531 (inlineTokens), node_modules/marked@18.0.11/src/Tokenizer.ts:14-50,649-1001 (outputLink and the inline tokenizers) and the GFM inline rules of node_modules/marked@18.0.11/src/rules.ts:283-534, the lexer packages/tui/package.json pins for packages/tui/src/components/markdown.ts.

import (
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/dlclark/regexp2"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// markedRegexp is one marked rule compiled on first use from the JavaScript source marked 18.0.11 builds (Lexer.rules.inline.gfm / Lexer.rules.block.gfm). The source is kept verbatim so it can be compared with marked's; translateMarkedPattern supplies JavaScript's meaning for the constructs regexp2 reads differently.
type markedRegexp struct {
	once       sync.Once
	source     string
	ignoreCase bool
	anchored   bool
	re         *regexp2.Regexp
}

func newMarkedRegexp(source string) *markedRegexp { return &markedRegexp{source: source} }

// newAnchoredMarkedRegexp is a rule whose every alternative begins with ^; the explicit outer anchor lets regexp2 reject a non-match without scanning the rest of the input.
func newAnchoredMarkedRegexp(source string, ignoreCase bool) *markedRegexp {
	return &markedRegexp{source: source, ignoreCase: ignoreCase, anchored: true}
}

func (r *markedRegexp) compiled() *regexp2.Regexp {
	r.once.Do(func() {
		pattern := translateMarkedPattern(r.source)
		if r.anchored {
			pattern = `^(?:` + pattern + `)`
		}
		options := regexp2.RegexOptions(regexp2.None)
		if r.ignoreCase {
			options |= regexp2.IgnoreCase
		}
		r.re = regexp2.MustCompile(pattern, options)
	})
	return r.re
}

// exec is RegExp.prototype.exec from index 0. regexp2 reports an error only for a match timeout, and these rules set none.
func (r *markedRegexp) exec(src []rune) *regexp2.Match {
	match, err := r.compiled().FindRunesMatch(src)
	if err != nil {
		return nil
	}
	return match
}

// next continues a global (g flag) scan after match, as exec does from lastIndex.
func (r *markedRegexp) next(match *regexp2.Match) *regexp2.Match {
	next, err := r.compiled().FindNextMatch(match)
	if err != nil {
		return nil
	}
	return next
}

// markedGroup is a capture group's text; ok is false when the group did not participate (undefined in JavaScript).
func markedGroup(match *regexp2.Match, group int) (text string, ok bool) {
	g := match.GroupByNumber(group)
	if g == nil || len(g.Captures) == 0 {
		return "", false
	}
	return g.String(), true
}

// markedJSWhitespace is the set JavaScript's \s matches (WhiteSpace and LineTerminator); regexp2's \s follows .NET instead.
const markedJSWhitespace = `\t\n\v\f\r \u00A0\u1680\u2000-\u200A\u2028\u2029\u202F\u205F\u3000\uFEFF`

// translateMarkedPattern rewrites a JavaScript pattern without the m flag into regexp2's default syntax: \s, \w, \d and \b take their JavaScript (ASCII word, JavaScript whitespace) meaning, . excludes every JavaScript line terminator, and $ matches only at the end of the input. Code points are the matching unit, as in a u-flag pattern; marked's non-u rules match surrogate halves only as part of character runs that a code point equally satisfies.
func translateMarkedPattern(source string) string {
	var out strings.Builder
	src := []rune(source)
	for i := 0; i < len(src); i++ {
		switch c := src[i]; c {
		case '\\':
			i++
			switch e := src[i]; e {
			case 's':
				out.WriteString(`[` + markedJSWhitespace + `]`)
			case 'S':
				out.WriteString(`[^` + markedJSWhitespace + `]`)
			case 'w':
				out.WriteString(`[A-Za-z0-9_]`)
			case 'W':
				out.WriteString(`[^A-Za-z0-9_]`)
			case 'd':
				out.WriteString(`[0-9]`)
			case 'D':
				out.WriteString(`[^0-9]`)
			case 'b':
				out.WriteString(`(?:(?<=[A-Za-z0-9_])(?![A-Za-z0-9_])|(?<![A-Za-z0-9_])(?=[A-Za-z0-9_]))`)
			default:
				out.WriteRune('\\')
				out.WriteRune(e)
			}
		case '.':
			out.WriteString(`[^\n\r\u2028\u2029]`)
		case '$':
			out.WriteString(`\z`)
		case '[':
			end := i + 1
			if end < len(src) && src[end] == '^' {
				end++
			}
			for ; src[end] != ']'; end++ {
				if src[end] == '\\' {
					end++
				}
			}
			body := src[i+1 : end]
			if string(body) == `\s\S` {
				out.WriteString(`[\s\S]`)
				i = end
				continue
			}
			out.WriteRune('[')
			for k := 0; k < len(body); k++ {
				if body[k] != '\\' {
					out.WriteRune(body[k])
					continue
				}
				k++
				switch e := body[k]; e {
				case 's':
					out.WriteString(markedJSWhitespace)
				case 'w':
					out.WriteString(`A-Za-z0-9_`)
				case 'd':
					out.WriteString(`0-9`)
				case 'S', 'W', 'D':
					panic("marked pattern uses an unsupported negated class escape inside a class: " + source)
				default:
					out.WriteRune('\\')
					out.WriteRune(e)
				}
			}
			out.WriteRune(']')
			i = end
		default:
			out.WriteRune(c)
		}
	}
	return out.String()
}

// The GFM inline rules, verbatim from marked 18.0.11 (src/rules.ts:283-534 as composed by edit()).
var (
	markedInlineEscape  = newAnchoredMarkedRegexp(`^\\([!"#$%&'()*+,\-./:;<=>?@\[\]\\^_`+"`"+`{|}~])`, false)
	markedInlineTag     = newAnchoredMarkedRegexp(`^<!--(?:-?>|[\s\S]*?-->)|^<\/[a-zA-Z][\w:-]*\s*>|^<[a-zA-Z][\w-]*(?:\s+[a-zA-Z:_][\w.:-]*(?:\s*=\s*"[^"]*"|\s*=\s*'[^']*'|\s*=\s*[^\s"'=<>`+"`"+`]+)?)*?\s*\/?>|^<\?[\s\S]*?\?>|^<![a-zA-Z]+\s[\s\S]*?>|^<!\[CDATA\[[\s\S]*?\]\]>`, false)
	markedInlineLink    = newAnchoredMarkedRegexp(`^!?\[((?:\[(?:\\[\s\S]|[^\[\]\\])*\]|\\[\s\S]|`+"`+(?!`)[^`]*?`+(?!`)|``"+`+(?=\])|[^\[\]\\`+"`"+`])*?)\]\(\s*(<(?:\\.|[^\n<>\\])+>|[^ \t\n\x00-\x1f]+|(?=\)))(?:(?:[ \t]+(?:\n[ \t]*)?|\n[ \t]*)("(?:\\"?|[^"\\])*"|'(?:\\'?|[^'\\])*'|\((?:\\\)?|[^)\\])*\)))?\s*\)`, false)
	markedInlineReflink = newAnchoredMarkedRegexp(`^!?\[((?:\[(?:\\[\s\S]|[^\[\]\\])*\]|\\[\s\S]|`+"`+(?!`)[^`]*?`+(?!`)|``"+`+(?=\])|[^\[\]\\`+"`"+`])*?)\]\[((?!\s*\])(?:\\[\s\S]|[^\[\]\\])+)\]`, false)
	markedInlineNolink  = newAnchoredMarkedRegexp(`^!?\[((?!\s*\])(?:\\[\s\S]|[^\[\]\\])+)\](?:\[\])?`, false)
	// markedReflinkSearch is reflinkSearch (rules.ts:461-464), reflink|nolink(?!\(), with the g flag.
	markedReflinkSearch     = newMarkedRegexp(`!?\[((?:\[(?:\\[\s\S]|[^\[\]\\])*\]|\\[\s\S]|` + "`+(?!`)[^`]*?`+(?!`)|``" + `+(?=\])|[^\[\]\\` + "`" + `])*?)\]\[((?!\s*\])(?:\\[\s\S]|[^\[\]\\])+)\]|!?\[((?!\s*\])(?:\\[\s\S]|[^\[\]\\])+)\](?:\[\])?(?!\()`)
	markedEmStrongLDelim    = newAnchoredMarkedRegexp(`^(?:\*+(?:((?!\*)(?!~)[\p{P}\p{S}])|([^\s*]))?)|^_+(?:((?!_)(?!~)[\p{P}\p{S}])|([^\s_]))?`, false)
	markedEmStrongRDelimAst = newMarkedRegexp(`^[^_*]*?__[^_*]*?\*[^_*]*?(?=__)|[^*]+(?=[^*])|(?!\*)(?!~)[\p{P}\p{S}](\*+)(?=[\s]|$)|(?:[^\s\p{P}\p{S}]|~)(\*+)(?!\*)(?=(?!~)[\s\p{P}\p{S}]|$)|(?!\*)(?!~)[\s\p{P}\p{S}](\*+)(?=(?:[^\s\p{P}\p{S}]|~))|[\s](\*+)(?!\*)(?=(?!~)[\p{P}\p{S}])|(?!\*)(?!~)[\p{P}\p{S}](\*+)(?!\*)(?=(?!~)[\p{P}\p{S}])|(?:[^\s\p{P}\p{S}]|~)(\*+)(?=(?:[^\s\p{P}\p{S}]|~))`)
	markedEmStrongRDelimUnd = newMarkedRegexp(`^[^_*]*?\*\*[^_*]*?_[^_*]*?(?=\*\*)|[^_]+(?=[^_])|(?!_)[\p{P}\p{S}](_+)(?=[\s]|$)|[^\s\p{P}\p{S}](_+)(?!_)(?=[\s\p{P}\p{S}]|$)|(?!_)[\s\p{P}\p{S}](_+)(?=[^\s\p{P}\p{S}])|[\s](_+)(?!_)(?=[\p{P}\p{S}])|(?!_)[\p{P}\p{S}](_+)(?!_)(?=[\p{P}\p{S}])`)
	markedInlineCode        = newAnchoredMarkedRegexp("^(`+)([^`]|[^`][\\s\\S]*?[^`])\\1(?!`)", false)
	markedInlineBr          = newAnchoredMarkedRegexp(`^( {2,}|\\)\n(?!\s*$)`, false)
	markedInlineAutolink    = newAnchoredMarkedRegexp(`^<([a-zA-Z][a-zA-Z0-9+.-]{1,31}:[^\s\x00-\x1f<>]*|[a-zA-Z0-9.!#$%&'*+/=?_`+"`"+`{|}~-]+(@)[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+(?![-_]))>`, false)
	markedInlineURL         = newAnchoredMarkedRegexp(`^((?:[hH][tT][tT][pP][sS]?|[fF][tT][pP]):\/\/|www\.)(?:[a-zA-Z0-9\-]+\.?)+[^\s<]*|^[A-Za-z0-9._+-]+(@)[a-zA-Z0-9-_]+(?:\.[a-zA-Z0-9-_]*[a-zA-Z0-9])+(?![-_])`, false)
	markedInlineBackpedal   = newMarkedRegexp(`(?:[^?!.,:;*_'"~()&]+|\([^)]*\)|&(?![a-zA-Z0-9]+;$)|[?!.,:;*_'"~)]+(?!$))+`)
	markedInlineText        = newAnchoredMarkedRegexp("^(`+|~+|[^`~])(?:(?=[`~])|(?= {2,}\\n)|(?=[a-zA-Z0-9.!#$%&'*+\\/=?_`{\\|}~-]+@)|[\\s\\S]*?(?:(?=[\\\\<!\\[`*~_]|\\b_|[hH][tT][tT][pP][sS]?|[fF][tT][pP]:\\/\\/|www\\.|$)|[^ ](?= {2,}\\n)|[^a-zA-Z0-9.!#$%&'*+\\/=?_`{\\|}~-](?=[a-zA-Z0-9.!#$%&'*+\\/=?_`{\\|}~-]+@)))", false)
	markedAnyPunctuation    = newMarkedRegexp(`\\([\p{P}\p{S}])`)
	markedBlockSkip         = newMarkedRegexp(`\[(?:[^\[\]` + "`" + `]|(?<a>` + "`+" + `)[^` + "`" + `]+\k<a>(?!` + "`" + `))*?\]\((?:\\[\s\S]|[^\\\(\)]|\((?:\\[\s\S]|[^\\\(\)])*\))*\)|(?<!` + "`" + `)()(?<b>` + "`+" + `)[^` + "`" + `]+\k<b>(?!` + "`" + `)|<(?! )[^<>]*?>`)
	// packages/tui/src/components/markdown.ts:7 STRICT_STRIKETHROUGH_REGEX replaces marked's del rule.
	markedStrictStrikethrough = newAnchoredMarkedRegexp(`^(~~)(?=[^\s~])((?:\\.|[^\\])*?(?:\\.|[^\s~\\]))\1(?=[^~]|$)`, false)
)

type markedTokenKind uint8

const (
	markedText markedTokenKind = iota
	markedEscape
	markedHTML
	markedLink
	markedImage
	markedStrong
	markedEm
	markedCodespan
	markedBr
	markedDel
	markedLatex
)

// markedToken is one inline token. raw is the source it consumed; tokens holds the children of links, emphasis and strikethrough.
type markedToken struct {
	kind   markedTokenKind
	raw    string
	text   string
	href   string
	tokens []markedToken
	latex  *latexToken
}

// markedInlineState is the lexer state that marked keeps for a whole document: an inline <a> tag suppresses GFM URL autolinks until its </a>, including in later blocks, and the link reference definitions (Lexer.tokens.links) resolve references anywhere in the document.
type markedInlineState struct {
	inLink, inRawBlock, linkEmitted bool
	// links maps a definition's tag to its href; the first definition of a tag wins.
	links map[string]string
	// lexedBracket records that inline text with a "[" was lexed; relex that a definition arrived after it, so a reference may have been lexed before its definition.
	lexedBracket, relex bool
	// The buffers below are reused by the next lexMarkedInline call, so its caller renders the tokens before lexing again. runes is the code-point form of the source; frames holds, per inlineTokens nesting depth, the token list being built and the masked source; arena holds the finished token lists.
	runes  []rune
	frames []markedLexFrame
	depth  int
	arena  []markedToken
}

type markedLexFrame struct {
	tokens []markedToken
	masked []rune
}

// markedInlineStates recycles lexer states across renders, whose buffers would otherwise grow from empty in every render.
var markedInlineStates = sync.Pool{New: func() any { return &markedInlineState{} }}

// startDocument clears the lexer state and definitions for a new document; the buffers are kept.
func (l *markedInlineState) startDocument() {
	l.restartWithLinks()
	clear(l.links)
}

// restartWithLinks clears the lexer state for lexing the document again with the definitions already found. marked's block pass registers every definition before any inline text is lexed (Lexer.ts:91-99); a single rendering pass lexes text as it goes, so a document whose definition follows a reference is rendered twice.
func (l *markedInlineState) restartWithLinks() {
	l.inLink, l.inRawBlock, l.linkEmitted = false, false, false
	l.lexedBracket, l.relex = false, false
}

// defineLink registers a definition as Lexer.ts:217-222 does: a tag keeps its first definition.
func (l *markedInlineState) defineLink(tag, href string) {
	if _, ok := l.links[tag]; ok {
		return
	}
	if l.links == nil {
		l.links = map[string]string{}
	}
	l.links[tag] = href
	l.relex = l.relex || l.lexedBracket
}

// markedPrevNone is the empty prevChar.
const markedPrevNone rune = -1

// markedPrevChar is JavaScript's raw.slice(-1): the last UTF-16 code unit, a lone low surrogate after an astral character.
func markedPrevChar(r rune) rune {
	if r > 0xFFFF {
		_, low := utf16.EncodeRune(r)
		return low
	}
	return r
}

func markedIsJSWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00A0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// markedPunctuation is rules.ts punctuation, /^((?![*_])[\s\p{P}\p{S}])/u, applied to prevChar.
func markedPunctuation(r rune) bool {
	return r != '*' && r != '_' && (markedIsJSWhitespace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r))
}

// lexMarkedInline is Lexer.inlineTokens for one inline source with the document's lexer state. The tokens are valid until the next call with the same state.
func lexMarkedInline(src string, state *markedInlineState) []markedToken {
	if !utf8.ValidString(src) {
		// Token text is sliced from src at the byte length of the code points the rules consume, so invalid bytes take the U+FFFD form the rules see.
		src = string([]rune(src))
	}
	if n := utf8.RuneCountInString(src); cap(state.runes) < n {
		state.runes = make([]rune, 0, n)
	}
	state.runes = state.runes[:0]
	for _, r := range src {
		state.runes = append(state.runes, r)
	}
	if state.arena == nil {
		// Start with room for one token per eight characters of the first source instead of growing from empty.
		state.arena = make([]markedToken, 0, max(16, len(src)/8))
		state.frames = make([]markedLexFrame, 0, 4)
	}
	state.arena = state.arena[:0]
	state.lexedBracket = state.lexedBracket || strings.Contains(src, "[")
	return state.inlineTokens(state.runes, src)
}

// markedByteLen is the UTF-8 length of runes taken from a valid string.
func markedByteLen(runes []rune) int {
	n := 0
	for _, r := range runes {
		n += utf8.RuneLen(r)
	}
	return n
}

// inlineTokens ports Lexer.ts:340-531 with pi-tui's inline latex extension (markdown.ts:133-143) as the only extension. src and str are the same source as code points and as UTF-8; every token's raw is a prefix of str, so text is sliced from str rather than copied. Adjacent text tokens merge as marked merges them; a merged run is one contiguous slice of str.
func (l *markedInlineState) inlineTokens(src []rune, str string) []markedToken {
	depth := l.depth
	if depth == len(l.frames) {
		l.frames = append(l.frames, markedLexFrame{})
	}
	l.depth++
	tokens := l.frames[depth].tokens[:0]
	full, fullStr := src, str
	ascii := len(full) == len(fullStr)
	var masked []rune
	maskedSrc := func() []rune {
		if masked == nil {
			masked = full
			// Lexer.ts:345-367 masks references only when the document defines links (otherwise every replacement keeps its match).
			reflinks := len(l.links) > 0 && strings.Contains(fullStr, "[")
			if reflinks || strings.ContainsAny(fullStr, markedMaskedChars) {
				l.frames[depth].masked = append(l.frames[depth].masked[:0], full...)
				masked = l.frames[depth].masked
				if reflinks {
					l.maskReflinks(masked)
				}
				markedMaskSource(masked)
			}
		}
		return masked
	}
	textStart := -1
	flushText := func(at int) {
		if textStart >= 0 {
			text := fullStr[textStart:at]
			tokens = append(tokens, markedToken{kind: markedText, raw: text, text: text})
			textStart = -1
		}
	}
	keepPrevChar := false
	prevChar := markedPrevNone
	latexStart := -1
	reach := markedBracketReachOf(fullStr)
	for len(src) > 0 {
		if !keepPrevChar {
			prevChar = markedPrevNone
		}
		keepPrevChar = false
		offset := len(full) - len(src)
		at := len(fullStr) - len(str)
		if tok, ok := l.nextInlineToken(src, str, maskedSrc, prevChar, reach.at(at)); ok {
			length := len(tok.raw)
			if !ascii {
				length = utf8.RuneCountInString(tok.raw)
			}
			if tok.kind == markedText {
				// An unresolved reference keeps its first character as text without tracking prevChar.
				if textStart < 0 {
					textStart = at
				}
			} else {
				flushText(at)
				tokens = append(tokens, tok)
			}
			src, str = src[length:], str[len(tok.raw):]
			continue
		}
		// The latex extension's start() clips inline text before the next "$", "\(" or "\[" after the first character.
		cutSrc := src
		if latexStart < offset+1 {
			latexStart = markedNextLatexStart(full, offset+1)
		}
		if latexStart < len(full) {
			cutSrc = src[:latexStart-offset]
		}
		length := markedInlineTextLength(cutSrc)
		if length == 0 {
			break
		}
		if last := src[length-1]; last != '_' {
			prevChar = markedPrevChar(last)
		}
		keepPrevChar = true
		if textStart < 0 {
			textStart = at
		}
		byteLength := length
		if !ascii {
			byteLength = markedByteLen(src[:length])
		}
		src, str = src[length:], str[byteLength:]
	}
	flushText(len(fullStr) - len(str))
	l.frames[depth].tokens = tokens[:0]
	l.depth--
	start := len(l.arena)
	l.arena = append(l.arena, tokens...)
	return l.arena[start:len(l.arena):len(l.arena)]
}

// nextInlineToken tries Lexer.ts:403-489 in order before inline text: the latex extension, escape, tag, link, reflink, emStrong, codespan, br, del, autolink and, outside a link, the GFM URL. The leading-character checks and brackets only skip rules whose pattern cannot match.
func (l *markedInlineState) nextInlineToken(src []rune, str string, maskedSrc func() []rune, prevChar rune, brackets markedBracketRules) (markedToken, bool) {
	if src[0] == '$' || src[0] == '\\' && len(src) > 1 && (src[1] == '(' || src[1] == '[') {
		if tok, ok := tokenizeInlineLatex(str); ok {
			return markedToken{kind: markedLatex, raw: tok.raw, latex: &tok}, true
		}
	}
	if tok, ok := markedEscapeToken(src, str); ok {
		return tok, true
	}
	if tok, ok := l.tag(src); ok {
		return tok, true
	}
	if tok, ok := l.link(src, str, brackets); ok {
		return tok, true
	}
	if tok, ok := l.reflink(src, str, brackets); ok {
		return tok, true
	}
	if src[0] == '*' || src[0] == '_' {
		if tok, ok := l.emStrong(src, str, maskedSrc, prevChar); ok {
			return tok, true
		}
	}
	if tok, ok := markedTokenizeCodespan(src, str); ok {
		return tok, true
	}
	if tok, ok := markedBreak(src, str); ok {
		return tok, true
	}
	if tok, ok := l.del(src, str); ok {
		return tok, true
	}
	if tok, ok := markedAutolink(src); ok {
		return tok, true
	}
	if !l.inLink {
		if tok, ok := markedURL(src); ok {
			return tok, true
		}
	}
	return markedToken{}, false
}

// markedNextLatexStart is the first index at or after from where "$", "\(" or "\[" begins, len(src) when there is none.
func markedNextLatexStart(src []rune, from int) int {
	for i := from; i < len(src); i++ {
		if src[i] == '$' || src[i] == '\\' && i+1 < len(src) && (src[i+1] == '(' || src[i+1] == '[') {
			return i
		}
	}
	return len(src)
}

// markedMaskedChars are the characters a mask starts with; a source without them needs no mask.
const markedMaskedChars = "\\[`<"

// markedMaskSource builds Lexer.ts maskedSrc (lines 342-380) in place without link definitions: escaped punctuation (anyPunctuation) becomes "+", and links, code spans and HTML tags (blockSkip) become "[aaa]" of the same length, so emphasis delimiters inside them cannot match.
func markedMaskSource(masked []rune) []rune {
	// anyPunctuation, /\\([\p{P}\p{S}])/gu: matches never overlap, so a masked pair is skipped whole.
	for i := 0; i+1 < len(masked); i++ {
		if masked[i] == '\\' && (unicode.IsPunct(masked[i+1]) || unicode.IsSymbol(masked[i+1])) {
			masked[i], masked[i+1] = '+', '+'
			i++
		}
	}
	// blockSkip is matched against the source as it was before this replacement; only the code alternative's lookbehind reads behind the scan position, so it is given the original character.
	prev := markedPrevNone
	for i := 0; i < len(masked); {
		end := markedBlockSkipAt(masked, i, prev)
		if end < 0 {
			prev = masked[i]
			i++
			continue
		}
		prev = masked[end-1]
		masked[i] = '['
		for k := i + 1; k < end-1; k++ {
			masked[k] = 'a'
		}
		masked[end-1] = ']'
		i = end
	}
	return masked
}

// markedBlockSkipAt evaluates marked's blockSkip rule (rules.ts:307-312) at i without a regexp and returns the match end, or -1:
//
//	\[(?:[^\[\]`]|(?<a>`+)[^`]+\k<a>(?!`))*?\]\((?:\\[\s\S]|[^\\\(\)]|\((?:\\[\s\S]|[^\\\(\)])*\))*\)|(?<!`)()(?<b>`+)[^`]+\k<b>(?!`)|<(?! )[^<>]*?>
//
// The alternatives start with different characters. Each is deterministic: a code span item can only take its whole backtick run (a shorter run leaves a backtick for [^`]+) and only close on a run of the same length; no label item consumes "]" and no destination item starts with ")", so backtracking never finds another end. prev is the character before i in the unmasked source, markedPrevNone at the start.
func markedBlockSkipAt(m []rune, i int, prev rune) int {
	// codeSpanEnd matches `+[^`]+\k(?!`) at j and returns its end.
	codeSpanEnd := func(j int) int {
		open := j
		for j < len(m) && m[j] == '`' {
			j++
		}
		n := j - open
		content := j
		for j < len(m) && m[j] != '`' {
			j++
		}
		if j == content || j == len(m) {
			return -1
		}
		close := j
		for j < len(m) && m[j] == '`' {
			j++
		}
		if j-close != n {
			return -1
		}
		return j
	}
	switch m[i] {
	case '[':
		j := i + 1
		for {
			if j == len(m) || m[j] == '[' {
				return -1
			}
			if m[j] == ']' {
				break
			}
			if m[j] == '`' {
				if j = codeSpanEnd(j); j < 0 {
					return -1
				}
				continue
			}
			j++
		}
		if j+1 == len(m) || m[j+1] != '(' {
			return -1
		}
		for k := j + 2; k < len(m); {
			switch m[k] {
			case ')':
				return k + 1
			case '\\':
				if k+1 == len(m) {
					return -1
				}
				k += 2
			case '(':
				q := k + 1
				for q < len(m) && m[q] != ')' {
					if m[q] == '(' {
						return -1
					}
					if m[q] == '\\' {
						q++
					}
					q++
				}
				if q >= len(m) {
					return -1
				}
				k = q + 1
			default:
				k++
			}
		}
		return -1
	case '`':
		if prev == '`' {
			return -1
		}
		return codeSpanEnd(i)
	case '<':
		if i+1 < len(m) && m[i+1] == ' ' {
			return -1
		}
		for j := i + 1; j < len(m); j++ {
			switch m[j] {
			case '>':
				return j + 1
			case '<':
				return -1
			}
		}
	}
	return -1
}

// markedSlice is the text of src[from:to] cut from str, the UTF-8 form of src.
func markedSlice(src []rune, str string, from, to int) string {
	if len(src) == len(str) {
		return str[from:to]
	}
	start := markedByteLen(src[:from])
	return str[start : start+markedByteLen(src[from:to])]
}

// markedGroupSlice is a capture group's text cut from str; ok is false when the group did not participate (undefined in JavaScript).
func markedGroupSlice(match *regexp2.Match, group int, src []rune, str string) (text string, ok bool) {
	g := match.GroupByNumber(group)
	if g == nil || len(g.Captures) == 0 {
		return "", false
	}
	return markedSlice(src, str, g.Index, g.Index+g.Length), true
}

// markedEscapeToken ports Tokenizer.ts:649-658 with the escape rule, /^\\([!"#$%&'()*+,\-./:;<=>?@\[\]\\^_`{|}~])/, evaluated without a regexp.
func markedEscapeToken(src []rune, str string) (markedToken, bool) {
	if src[0] != '\\' || len(src) < 2 || src[1] > 0x7F || !strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[]\\^_`{|}~", src[1]) {
		return markedToken{}, false
	}
	return markedToken{kind: markedEscape, raw: str[:2], text: str[1:2]}, true
}

// tag ports Tokenizer.ts:660-683 and the startATag/endATag/startPreScriptTag/endPreScriptTag rules of rules.ts:72-75.
func (l *markedInlineState) tag(src []rune) (markedToken, bool) {
	if src[0] != '<' {
		return markedToken{}, false
	}
	match := markedInlineTag.exec(src)
	if match == nil {
		return markedToken{}, false
	}
	raw := match.String()
	lower := strings.ToLower(raw)
	if !l.inLink && strings.HasPrefix(lower, "<a ") {
		l.inLink = true
	} else if l.inLink && strings.HasPrefix(lower, "</a>") {
		l.inLink = false
	}
	if !l.inRawBlock && markedRawBlockTag(lower, "<") {
		l.inRawBlock = true
	} else if l.inRawBlock && markedRawBlockTag(lower, "</") {
		l.inRawBlock = false
	}
	return markedToken{kind: markedHTML, raw: raw, text: raw}, true
}

// markedRawBlockTag is /^<(pre|code|kbd|script)(\s|>)/i (or its closing form) over a lower-cased tag.
func markedRawBlockTag(lower, open string) bool {
	rest, ok := strings.CutPrefix(lower, open)
	if !ok {
		return false
	}
	for _, name := range []string{"pre", "code", "kbd", "script"} {
		if after, ok := strings.CutPrefix(rest, name); ok && after != "" {
			r, _ := utf8.DecodeRuneInString(after)
			if r == '>' || markedIsJSWhitespace(r) {
				return true
			}
		}
	}
	return false
}

// link ports Tokenizer.ts:685-744 without the pedantic branches.
func (l *markedInlineState) link(src []rune, str string, brackets markedBracketRules) (markedToken, bool) {
	if src[0] != '[' && src[0] != '!' || !brackets.link {
		return markedToken{}, false
	}
	match := markedInlineLink.exec(src)
	if match == nil {
		return markedToken{}, false
	}
	raw := markedSlice(src, str, 0, match.Length)
	labelGroup := match.GroupByNumber(1)
	labelRunes := src[labelGroup.Index : labelGroup.Index+labelGroup.Length]
	label := markedSlice(src, str, labelGroup.Index, labelGroup.Index+labelGroup.Length)
	destination, _ := markedGroupSlice(match, 2, src, str)
	trimmedURL := widthx.JSTrim(destination)
	if strings.HasPrefix(trimmedURL, "<") {
		if !strings.HasSuffix(trimmedURL, ">") {
			return markedToken{}, false
		}
		inner := trimmedURL[:len(trimmedURL)-1]
		slashes := len(inner) - len(strings.TrimRight(inner, `\`))
		if (slashes+1)%2 == 0 {
			return markedToken{}, false
		}
	} else {
		destinationRunes := []rune(destination)
		lastParenIndex := markedFindClosingBracket(destinationRunes, '(', ')')
		if lastParenIndex == -2 {
			return markedToken{}, false
		}
		if lastParenIndex > -1 {
			start := 4
			if src[0] == '!' {
				start = 5
			}
			linkLen := start + len(labelRunes) + lastParenIndex
			destination = string(destinationRunes[:lastParenIndex])
			raw = widthx.JSTrim(markedSlice(src, str, 0, linkLen))
		}
	}
	href := widthx.JSTrim(destination)
	if strings.HasPrefix(href, "<") {
		_, first := utf8.DecodeRuneInString(href)
		_, last := utf8.DecodeLastRuneInString(href)
		href = href[first : len(href)-last]
	}
	if href != "" {
		href = markedUnescapePunctuation(href)
	}
	return l.outputLink(labelRunes, label, raw, href)
}

// outputLink ports Tokenizer.ts:14-50: a link whose text already holds a link is not a link. labelRunes is label as code points.
func (l *markedInlineState) outputLink(labelRunes []rune, label, raw, href string) (markedToken, bool) {
	text := markedUnescapeBrackets(label)
	textRunes := labelRunes
	if text != label {
		textRunes = []rune(text)
	}
	isImage := strings.HasPrefix(raw, "!")
	l.inLink = true
	outerLinkEmitted := l.linkEmitted
	outerInRawBlock := l.inRawBlock
	l.linkEmitted = false
	tokens := l.inlineTokens(textRunes, text)
	textHasLink := l.linkEmitted
	l.linkEmitted = outerLinkEmitted
	l.inLink = false
	if !isImage {
		if textHasLink {
			l.inRawBlock = outerInRawBlock
			return markedToken{}, false
		}
		l.linkEmitted = true
	}
	kind := markedLink
	if isImage {
		kind = markedImage
	}
	return markedToken{kind: kind, raw: raw, text: text, href: href, tokens: tokens}, true
}

// markedUnescapeBrackets is rules.ts outputLinkReplace, /\\([\[\]])/g → "$1".
func markedUnescapeBrackets(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	return strings.NewReplacer(`\[`, `[`, `\]`, `]`).Replace(s)
}

// markedUnescapePunctuation is s.replace(anyPunctuation, "$1") with anyPunctuation, /\\([\p{P}\p{S}])/gu, evaluated without a regexp.
func markedUnescapePunctuation(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\\' {
			if r, size := utf8.DecodeRuneInString(s[i+1:]); size > 0 && (unicode.IsPunct(r) || unicode.IsSymbol(r)) {
				out.WriteString(s[i+1 : i+1+size])
				i += 1 + size
				continue
			}
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

// markedFindClosingBracket ports helpers.ts:126-149.
func markedFindClosingBracket(s []rune, open, close rune) int {
	if !slices.Contains(s, close) {
		return -1
	}
	level := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case open:
			level++
		case close:
			level--
			if level < 0 {
				return i
			}
		}
	}
	if level > 0 {
		return -2
	}
	return -1
}

// markedBracketReach is, for one inline source, the last byte offset at which each bracket rule can still match: the last "]" (nolink), the last "](" with a ")" after it (link) and the last "][" (reflink). Each rule needs that sequence at or after its start, so one starting later can only fail, the link and reflink regexps after backtracking through the rest of the source.
type markedBracketReach struct{ nolink, link, reflink int }

func markedBracketReachOf(str string) markedBracketReach {
	reach := markedBracketReach{nolink: strings.LastIndexByte(str, ']'), link: -1, reflink: -1}
	if reach.nolink < 0 {
		return reach
	}
	if paren := strings.LastIndexByte(str, ')'); paren > 0 {
		reach.link = strings.LastIndex(str[:paren], "](")
	}
	reach.reflink = strings.LastIndex(str, "][")
	return reach
}

// markedBracketRules says which bracket rules can match at a source offset.
type markedBracketRules struct{ nolink, link, reflink bool }

func (r markedBracketReach) at(offset int) markedBracketRules {
	return markedBracketRules{nolink: r.nolink >= offset, link: r.link >= offset, reflink: r.reflink >= offset}
}

// reflink ports Tokenizer.ts:746-762: a reference ([text][label]) or shortcut ([label] or [label][]) whose label names a definition is a link to it; any other keeps only its first character as text, and lexing resumes after it.
func (l *markedInlineState) reflink(src []rune, str string, brackets markedBracketRules) (markedToken, bool) {
	if src[0] != '[' && src[0] != '!' || !brackets.nolink {
		return markedToken{}, false
	}
	var match *regexp2.Match
	if brackets.reflink {
		match = markedInlineReflink.exec(src)
	}
	if match == nil {
		if match = markedInlineNolink.exec(src); match == nil {
			return markedToken{}, false
		}
	}
	text := markedToken{kind: markedText, raw: str[:1], text: str[:1]}
	if len(l.links) == 0 {
		return text, true
	}
	labelGroup := match.GroupByNumber(1)
	label := markedSlice(src, str, labelGroup.Index, labelGroup.Index+labelGroup.Length)
	// linkString is cap[2] || cap[1]; nolink has no second group.
	key, ok := markedGroupSlice(match, 2, src, str)
	if !ok || key == "" {
		key = label
	}
	href, defined := l.links[markedLinkLabel(key)]
	if !defined {
		return text, true
	}
	return l.outputLink(src[labelGroup.Index:labelGroup.Index+labelGroup.Length], label, markedSlice(src, str, 0, match.Length), href)
}

// maskReflinks applies the reference masking of Lexer.ts:345-367 to masked in place: every reflinkSearch match whose last bracket pair holds a defined tag (compared unnormalized, as Object.hasOwn reads it) becomes "[aaa]". A reference whose text holds a link keeps its text, with the references inside it masked the same way, and masks only its label.
func (l *markedInlineState) maskReflinks(masked []rune) {
	type span struct{ start, end int }
	var spans []span
	for m := markedReflinkSearch.exec(masked); m != nil; m = markedReflinkSearch.next(m) {
		spans = append(spans, span{m.Index, m.Index + m.Length})
	}
	// The matches do not overlap, so each is still unmasked when it is replaced.
	for _, s := range spans {
		match := masked[s.start:s.end]
		refStart := markedLastIndexRune(match, '[')
		if _, ok := l.links[string(match[refStart+1:len(match)-1])]; !ok {
			continue
		}
		maskFrom := 1
		if refStart > 1 && match[0] != '!' && l.linkInText(match[1:refStart-1]) {
			l.maskReflinks(match[1 : refStart-1])
			maskFrom = refStart + 1
		}
		match[0] = '['
		for i := maskFrom; i < len(match)-1; i++ {
			match[i] = 'a'
		}
	}
}

// markedLastIndexRune is the index of the last r in s, -1 when there is none.
func markedLastIndexRune(s []rune, r rune) int {
	for i, v := range slices.Backward(s) {
		if v == r {
			return i
		}
	}
	return -1
}

// linkInText ports Lexer.ts:303-334: whether link text already holds a link, an inline link that blockSkip finds (not an image) or a reference to a defined tag whose own text holds none.
func (l *markedInlineState) linkInText(text []rune) bool {
	if !slices.Contains(text, '[') {
		return false
	}
	prev := markedPrevNone
	for i := 0; i < len(text); {
		end := markedBlockSkipAt(text, i, prev)
		if end < 0 {
			prev = text[i]
			i++
			continue
		}
		if markedInlineLink.exec(text[i:end]) != nil && (i == 0 || text[i-1] != '!') {
			return true
		}
		prev = text[end-1]
		i = end
	}
	for m := markedReflinkSearch.exec(text); m != nil; m = markedReflinkSearch.next(m) {
		match := text[m.Index : m.Index+m.Length]
		refStart := markedLastIndexRune(match, '[')
		if match[0] == '!' {
			continue
		}
		if _, ok := l.links[string(match[refStart+1:len(match)-1])]; !ok {
			continue
		}
		if refStart > 1 && l.linkInText(match[1:refStart-1]) {
			continue
		}
		return true
	}
	return false
}

// emStrong ports Tokenizer.ts:764-843: a delimiter run opens when it is left-flanking (and, for "_", not intraword), then closing runs are counted on the masked source with CommonMark's rule of 3.
func (l *markedInlineState) emStrong(src []rune, str string, maskedSrc func() []rune, prevChar rune) (markedToken, bool) {
	run, next := markedLeftDelimiter(src)
	if next == markedDelimNone {
		return markedToken{}, false
	}
	// "_" cannot open between two alphanumerics; \p{L}\p{N} include non-English letters and numbers.
	if src[0] == '_' && next == markedDelimOther && prevChar != markedPrevNone && (unicode.IsLetter(prevChar) || unicode.IsNumber(prevChar)) {
		return markedToken{}, false
	}
	// A run followed by punctuation is left-flanking only after whitespace, punctuation or the start.
	if next == markedDelimPunctuation && prevChar != markedPrevNone && !markedPunctuation(prevChar) {
		return markedToken{}, false
	}
	lLength := run
	delimTotal, midDelimTotal := lLength, 0
	delimChar := src[0]
	// A mid-run opener must only pair with a delimiter that can only close.
	midRun := prevChar == delimChar
	masked := maskedSrc()
	masked = masked[len(masked)-len(src)+lLength:]
	for pos := 0; ; {
		index, end, group, rLength, ok := markedRightDelimiterMatch(masked, pos, delimChar)
		if !ok {
			break
		}
		pos = end
		if group == 0 {
			continue
		}
		if group == 3 || group == 4 {
			delimTotal += rLength
			continue
		} else if group == 5 || group == 6 {
			if lLength%3 != 0 && (lLength+rLength)%3 == 0 {
				midDelimTotal += rLength
				continue
			}
			if midRun {
				break
			}
		}
		delimTotal -= rLength
		if delimTotal > 0 {
			continue
		}
		rLength = min(rLength, rLength+delimTotal+midDelimTotal)
		raw := src[:lLength+index+1+rLength]
		rawStr := str[:markedByteLen(raw)]
		// Both ends of raw are ASCII delimiters.
		if min(lLength, rLength)%2 == 1 {
			text := rawStr[1 : len(rawStr)-1]
			return markedToken{kind: markedEm, raw: rawStr, text: text, tokens: l.inlineTokens(raw[1:len(raw)-1], text)}, true
		}
		text := rawStr[2 : len(rawStr)-2]
		return markedToken{kind: markedStrong, raw: rawStr, text: text, tokens: l.inlineTokens(raw[2:len(raw)-2], text)}, true
	}
	return markedToken{}, false
}

type markedDelimNext uint8

const (
	markedDelimNone markedDelimNext = iota
	// markedDelimPunctuation is emStrongLDelim group 1 or 3: Unicode punctuation or a symbol other than "~".
	markedDelimPunctuation
	// markedDelimOther is group 2 or 4: any other character that is not whitespace.
	markedDelimOther
)

// markedLeftDelimiter evaluates the GFM emStrongLDelim rule, /^(?:\*+(?:((?!\*)(?!~)[\p{P}\p{S}])|([^\s*]))?)|^_+(?:((?!_)(?!~)[\p{P}\p{S}])|([^\s_]))?/u, without a regexp: the length of the leading "*" or "_" run and which group the character after it matched. The greedy run leaves a different character next, so neither group can see the delimiter itself.
func markedLeftDelimiter(src []rune) (run int, next markedDelimNext) {
	delim := src[0]
	if delim != '*' && delim != '_' {
		return 0, markedDelimNone
	}
	for run < len(src) && src[run] == delim {
		run++
	}
	if run == len(src) {
		return run, markedDelimNone
	}
	switch c := src[run]; {
	case c != '~' && (unicode.IsPunct(c) || unicode.IsSymbol(c)):
		return run, markedDelimPunctuation
	case !markedIsJSWhitespace(c):
		return run, markedDelimOther
	}
	return run, markedDelimNone
}

// markedRightDelimiterMatch evaluates one global exec of marked's closing-delimiter rule from pos: emStrongRDelimAst (GFM, delim "*") or emStrongRDelimUnd (delim "_"), rules.ts:332-383. It returns the match start and end, the capturing group (1-6) holding the delimiter run and the run's length; group 0 is a skip match (the orphan-delimiter and consume-to-delimiter alternatives). At each position the alternatives are tried in order; every run alternative takes the whole run, because a shorter run leaves the delimiter itself next, which none of the lookaheads accept.
func markedRightDelimiterMatch(m []rune, pos int, delim rune) (index, end, group, length int, ok bool) {
	gfm := delim == '*'
	other := '_'
	if !gfm {
		other = '*'
	}
	ps := func(c rune) bool { return unicode.IsPunct(c) || unicode.IsSymbol(c) }
	// punct, punctSpace and notPunctSpace; the GFM "*" rule lets "~" act as a letter for strikethrough.
	punct := func(c rune) bool { return ps(c) && (!gfm || c != '~') }
	punctSpace := func(c rune) bool { return (markedIsJSWhitespace(c) || ps(c)) && (!gfm || c != '~') }
	notPunctSpace := func(c rune) bool { return !markedIsJSWhitespace(c) && !ps(c) || gfm && c == '~' }
	notDelimOrOther := func(i int) int {
		for i < len(m) && m[i] != delim && m[i] != other {
			i++
		}
		return i
	}
	for p := pos; p < len(m); p++ {
		if p == 0 {
			// ^[^_*]*?OO[^_*]*?D[^_*]*?(?=OO): skip an orphan delimiter inside the other character's strong span ("__abc*abc__" for "*").
			i := notDelimOrOther(0)
			if i+1 < len(m) && m[i] == other && m[i+1] == other {
				j := notDelimOrOther(i + 2)
				if j < len(m) && m[j] == delim {
					k := notDelimOrOther(j + 1)
					if k+1 < len(m) && m[k] == other && m[k+1] == other {
						return 0, k, 0, 0, true
					}
				}
			}
		}
		c := m[p]
		if c != delim {
			// [^D]+(?=[^D]): consume to the character before the next delimiter.
			run := p
			for run < len(m) && m[run] != delim {
				run++
			}
			if run-p >= 2 {
				return p, run - 1, 0, 0, true
			}
		}
		if p+1 >= len(m) || m[p+1] != delim || c == delim {
			continue
		}
		runEnd := p + 1
		for runEnd < len(m) && m[runEnd] == delim {
			runEnd++
		}
		length = runEnd - p - 1
		atEnd := runEnd == len(m)
		var after rune
		if !atEnd {
			after = m[runEnd]
		}
		switch {
		case punct(c) && (atEnd || markedIsJSWhitespace(after)):
			group = 1 // #*** can only close
		case notPunctSpace(c) && (atEnd || punctSpace(after)):
			group = 2 // a***# and a*** can only close
		case punctSpace(c) && !atEnd && notPunctSpace(after):
			group = 3 // #***a and ***a can only open
		case markedIsJSWhitespace(c) && !atEnd && punct(after):
			group = 4 // ***# can only open
		case punct(c) && !atEnd && punct(after):
			group = 5 // #***# can open or close
		case gfm && notPunctSpace(c) && !atEnd && notPunctSpace(after):
			group = 6 // a***a can open or close
		default:
			continue
		}
		return p, runEnd, group, length, true
	}
	return 0, 0, 0, 0, false
}

// markedEmailChar is the class [a-zA-Z0-9.!#$%&'*+\/=?_`{\|}~-] of the GFM text rule's email lookaheads.
func markedEmailChar(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(".!#$%&'*+/=?_`{|}~-", c)
}

// markedEmailAhead is the lookahead (?=[a-zA-Z0-9.!#$%&'*+\/=?_`{\|}~-]+@) at i.
func markedEmailAhead(src []rune, i int) bool {
	start := i
	for i < len(src) && markedEmailChar(src[i]) {
		i++
	}
	return i > start && i < len(src) && src[i] == '@'
}

// markedHardBreakAhead is the lookahead (?= {2,}\n) at i.
func markedHardBreakAhead(src []rune, i int) bool {
	start := i
	for i < len(src) && src[i] == ' ' {
		i++
	}
	return i-start >= 2 && i < len(src) && src[i] == '\n'
}

// markedHasPrefix reports whether src[i:] starts with prefix; with fold, ASCII letters compare case-insensitively as [hH][tT]... does.
func markedHasPrefix(src []rune, i int, prefix string, fold bool) bool {
	if len(src)-i < len(prefix) {
		return false
	}
	for k := range len(prefix) {
		c := src[i+k]
		if fold && c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != rune(prefix[k]) {
			return false
		}
	}
	return true
}

// markedInlineTextLength evaluates the GFM inline text rule (rules.ts:531) without a regexp and returns the matched length, 0 only for empty input:
//
//	^(`+|~+|[^`~])(?:(?=[`~])|(?= {2,}\n)|(?=EMAIL+@)|[\s\S]*?(?:(?=[\\<!\[`*~_]|\b_|[hH][tT][tT][pP][sS]?|[fF][tT][pP]:\/\/|www\.|$)|[^ ](?= {2,}\n)|[^EMAIL](?=EMAIL+@)))
//
// The lazy [\s\S]*? stops at the first position where one of the three closing alternatives matches, tried in order. \b_ is subsumed by the "_" in the class, and the unparenthesized scheme alternation makes any "http" stop the run.
func markedInlineTextLength(src []rune) int {
	if len(src) == 0 {
		return 0
	}
	p := 1
	if c := src[0]; c == '`' || c == '~' {
		for p < len(src) && src[p] == c {
			p++
		}
	}
	if p < len(src) && (src[p] == '`' || src[p] == '~') || markedHardBreakAhead(src, p) || markedEmailAhead(src, p) {
		return p
	}
	for q := p; ; q++ {
		if q == len(src) || strings.ContainsRune("\\<![`*~_", src[q]) || markedHasPrefix(src, q, "http", true) || markedHasPrefix(src, q, "ftp://", true) || markedHasPrefix(src, q, "www.", false) {
			return q
		}
		if src[q] != ' ' && markedHardBreakAhead(src, q+1) {
			return q + 1
		}
		if !markedEmailChar(src[q]) && markedEmailAhead(src, q+1) {
			return q + 1
		}
	}
}

// markedInlineCodeMatch evaluates the inline code rule, /^(`+)([^`]|[^`][\s\S]*?[^`])\1(?!`)/, without a regexp: the length of the opening run and the end of the match. Only the whole opening run can be group 1 (a shorter one leaves a backtick for [^`]), and the lazy content ends at the first later run of exactly that length; a longer run fails (?!`) at its start and leaves a backtick before every later position.
func markedInlineCodeMatch(src []rune) (open, end int, ok bool) {
	for open < len(src) && src[open] == '`' {
		open++
	}
	if open == 0 {
		return 0, 0, false
	}
	for j := open; j < len(src); {
		if src[j] != '`' {
			j++
			continue
		}
		run := j
		for j < len(src) && src[j] == '`' {
			j++
		}
		if j-run == open {
			return open, j, true
		}
	}
	return 0, 0, false
}

// markedTokenizeCodespan ports Tokenizer.ts:845-860.
func markedTokenizeCodespan(src []rune, str string) (markedToken, bool) {
	if src[0] != '`' {
		return markedToken{}, false
	}
	open, end, ok := markedInlineCodeMatch(src)
	if !ok {
		return markedToken{}, false
	}
	raw := markedSlice(src, str, 0, end)
	// The backtick runs are ASCII.
	text := strings.ReplaceAll(raw[open:len(raw)-open], "\n", " ")
	if strings.ContainsFunc(text, func(r rune) bool { return r != ' ' }) && strings.HasPrefix(text, " ") && strings.HasSuffix(text, " ") {
		text = text[1 : len(text)-1]
	}
	return markedToken{kind: markedCodespan, raw: raw, text: text}, true
}

// markedBreakLength evaluates the br rule, /^( {2,}|\\)\n(?!\s*$)/, without a regexp: the match length, 0 when it does not match.
func markedBreakLength(src []rune) int {
	i := 0
	switch src[0] {
	case '\\':
		i = 1
	case ' ':
		for i < len(src) && src[i] == ' ' {
			i++
		}
		if i < 2 {
			return 0
		}
	default:
		return 0
	}
	if i == len(src) || src[i] != '\n' {
		return 0
	}
	i++
	for _, c := range src[i:] {
		if !markedIsJSWhitespace(c) {
			return i
		}
	}
	return 0
}

// markedBreak ports Tokenizer.ts:862-870.
func markedBreak(src []rune, str string) (markedToken, bool) {
	n := markedBreakLength(src)
	if n == 0 {
		return markedToken{}, false
	}
	// The rule matches ASCII only.
	return markedToken{kind: markedBr, raw: str[:n]}, true
}

// markedIsJSLineTerminator reports whether JavaScript's "." excludes r.
func markedIsJSLineTerminator(r rune) bool {
	return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029
}

// markedStrictStrikethroughMatch evaluates pi-tui's STRICT_STRIKETHROUGH_REGEX, /^(~~)(?=[^\s~])((?:\\.|[^\\])*?(?:\\.|[^\s~\\]))\1(?=[^~]|$)/, without a regexp: the end of group 2 and of the match. The content splits into units deterministically (a backslash and the next character, or one other character); the lazy loop ends at the first unit that may close the content and is followed by "~~" and no third "~".
func markedStrictStrikethroughMatch(src []rune) (textEnd, end int, ok bool) {
	if len(src) < 3 || src[0] != '~' || src[1] != '~' || src[2] == '~' || markedIsJSWhitespace(src[2]) {
		return 0, 0, false
	}
	for b := 2; b < len(src); {
		unitEnd := b + 1
		closes := !markedIsJSWhitespace(src[b]) && src[b] != '~'
		if src[b] == '\\' {
			if b+1 == len(src) || markedIsJSLineTerminator(src[b+1]) {
				return 0, 0, false
			}
			unitEnd, closes = b+2, true
		}
		if closes && unitEnd+1 < len(src) && src[unitEnd] == '~' && src[unitEnd+1] == '~' && (unitEnd+2 == len(src) || src[unitEnd+2] != '~') {
			return unitEnd, unitEnd + 2, true
		}
		b = unitEnd
	}
	return 0, 0, false
}

// del is pi-tui's StrictStrikethroughTokenizer.del (markdown.ts:9-24), which replaces marked's single- and double-tilde rule.
func (l *markedInlineState) del(src []rune, str string) (markedToken, bool) {
	if src[0] != '~' {
		return markedToken{}, false
	}
	textEnd, end, ok := markedStrictStrikethroughMatch(src)
	if !ok {
		return markedToken{}, false
	}
	raw := markedSlice(src, str, 0, end)
	// The "~~" delimiters are ASCII.
	text := raw[2 : len(raw)-2]
	return markedToken{kind: markedDel, raw: raw, text: text, tokens: l.inlineTokens(src[2:textEnd], text)}, true
}

// markedAutolink ports Tokenizer.ts:925-951.
func markedAutolink(src []rune) (markedToken, bool) {
	if src[0] != '<' {
		return markedToken{}, false
	}
	match := markedInlineAutolink.exec(src)
	if match == nil {
		return markedToken{}, false
	}
	text, _ := markedGroup(match, 1)
	href := text
	if at, _ := markedGroup(match, 2); at == "@" {
		href = "mailto:" + text
	}
	return markedToken{kind: markedLink, raw: match.String(), text: text, href: href, tokens: []markedToken{{kind: markedText, raw: text, text: text}}}, true
}

// markedURL ports Tokenizer.ts:953-988, the GFM extended autolink with its trailing-punctuation backpedal.
func markedURL(src []rune) (markedToken, bool) {
	if !markedHasPrefix(src, 0, "http://", true) && !markedHasPrefix(src, 0, "https://", true) && !markedHasPrefix(src, 0, "ftp://", true) && !markedHasPrefix(src, 0, "www.", false) {
		// Otherwise only the email branch, ^[A-Za-z0-9._+-]+(@), can match.
		i := 0
		for i < len(src) && (src[i] >= 'a' && src[i] <= 'z' || src[i] >= 'A' && src[i] <= 'Z' || src[i] >= '0' && src[i] <= '9' || strings.ContainsRune("._+-", src[i])) {
			i++
		}
		if i == 0 || i == len(src) || src[i] != '@' {
			return markedToken{}, false
		}
	}
	match := markedInlineURL.exec(src)
	if match == nil {
		return markedToken{}, false
	}
	text := match.String()
	href := ""
	if at, _ := markedGroup(match, 2); at == "@" {
		href = "mailto:" + text
	} else {
		for {
			previous := text
			text = ""
			if m := markedInlineBackpedal.exec([]rune(previous)); m != nil {
				text = m.String()
			}
			if text == previous {
				break
			}
		}
		href = text
		if scheme, _ := markedGroup(match, 1); scheme == "www." {
			href = "http://" + text
		}
	}
	return markedToken{kind: markedLink, raw: text, text: text, href: href, tokens: []markedToken{{kind: markedText, raw: text, text: text}}}, true
}

// renderInlineTokens ports markdown.ts renderInlineTokens (lines 644-751).
func (m *Markdown) renderInlineTokens(tokens []markedToken, style inlineStyleContext) string {
	var resolved *MarkdownTheme
	theme := func() *MarkdownTheme {
		if resolved == nil {
			t := m.markdownTheme()
			resolved = &t
		}
		return resolved
	}
	var out strings.Builder
	applyText := func(text string) {
		for i, segment := range strings.Split(text, "\n") {
			if i > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(style.applyText(segment))
		}
	}
	for _, token := range tokens {
		switch token.kind {
		case markedLatex:
			text := token.latex.raw
			if m.latexEnabled() {
				text = renderInlineLatex(*token.latex)
			}
			applyText(text)
		case markedEscape:
			if m.options.PreserveBackslashEscapes {
				applyText(token.raw)
			} else {
				applyText(token.text)
			}
		case markedText, markedImage:
			applyText(token.text)
		case markedHTML:
			applyText(token.raw)
		case markedStrong:
			out.WriteString(theme().Bold(m.renderInlineTokens(token.tokens, style)))
			out.WriteString(style.stylePrefix)
		case markedEm:
			out.WriteString(theme().Italic(m.renderInlineTokens(token.tokens, style)))
			out.WriteString(style.stylePrefix)
		case markedCodespan:
			out.WriteString(theme().Code(token.text))
			out.WriteString(style.stylePrefix)
		case markedLink:
			out.WriteString(m.styleMarkdownLink(m.renderInlineTokens(token.tokens, style), token.text, token.href))
			out.WriteString(style.stylePrefix)
		case markedBr:
			out.WriteByte('\n')
		case markedDel:
			out.WriteString(theme().Strikethrough(m.renderInlineTokens(token.tokens, style)))
			out.WriteString(style.stylePrefix)
		}
	}
	result := out.String()
	for style.stylePrefix != "" && strings.HasSuffix(result, style.stylePrefix) {
		result = strings.TrimSuffix(result, style.stylePrefix)
	}
	return result
}
