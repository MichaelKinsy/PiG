package tui

// Ports node_modules/marked@18.0.11/src/Lexer.ts:107-262 (blockTokens), node_modules/marked@18.0.11/src/Tokenizer.ts:76-361,520-606 (the block tokenizers) and the block rules of node_modules/marked@18.0.11/src/rules.ts:60-244, with the latexBlock extension of packages/tui/src/components/markdown.ts:127-139.
// Ports packages/tui/src/components/markdown.ts (its marked block lexer).

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/dlclark/regexp2"

	"github.com/MichaelKinsy/PiG/internal/jsstring"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// The block rules marked builds with edit() that markdown_marked_block.go does not hold (rules.ts:60-110, 164-176).
const (
	markedHeadingSource = `^ {0,3}(#{1,6})(?=\s|$)(.*)(?:\n+|$)`
	// markedLheadingSource is lheadingGfm: lheadingCore with its pieces replaced the way rules.ts:67-86 does.
	markedLheadingBullet = ` {0,3}(?:[*+-]|\d{1,9}[.)])`
	markedLheadingStops  = markedLheadingBullet + ` |(?: {4}| {0,3}\t)| {0,3}(?:` + "`{3,}|~{3,})" + `| {0,3}>| {0,3}#{1,6}(?:\s|$)| {0,3}<[^\n>]+>\n| {0,3}\|?(?:[:\- ]*\|)+[\:\- ]*\n`
	markedLheadingSource = `^(?!` + markedLheadingStops + `)((?:.|\n(?!\s*?\n|` + markedLheadingStops + `))+?)\n {0,3}(=+|-+) *(?:\n+|$)`
)

var (
	markedBlockHeading  = newAnchoredMarkedRegexp(markedHeadingSource, false)
	markedBlockLheading = newAnchoredMarkedRegexp(markedLheadingSource, false)
	// markedBlockquoteSetext is other.blockquoteSetextReplace (rules.ts:75); markedStripQuoteMarkers is other.blockquoteSetextReplace2 (rules.ts:76).
	markedBlockquoteSetext = newMarkedRegexp(`\n {0,3}((?:=+|-+) *)(?=\n|$)`)
)

// markedInlineEntry is one entry of Lexer.inlineQueue: the source of an inline token list, which the list tokenizer may still change.
type markedInlineEntry struct{ src string }

// markedBlock is a block token of Lexer.blockTokens.
type markedBlock struct {
	kind string
	raw  string
	text string
	// lang and depth are those of a code and a heading token.
	lang  string
	depth int
	// pending is that of a latexBlock token.
	pending bool
	// entry holds the inline source of a heading, paragraph or text token; it is stored with the token, which is the only holder of the token's list.
	entry      *markedInlineEntry
	entryStore markedInlineEntry
	// tokens are the children of a blockquote and of a list item.
	tokens []*markedBlock
	// ordered, start, loose and items are those of a list token; task, checked and loose of a list item.
	ordered, loose, task, checked bool
	start                         int
	items                         []*markedBlock
	// header and rows are the cells of a table token.
	header []*markedInlineEntry
	rows   [][]*markedInlineEntry
	// tag is that of a def token.
	tag string
	// offset is where the token starts in the source that was lexed.
	offset int
}

// markedBlockLexer is _Lexer for one document: its state.top flag and its inline queue.
type markedBlockLexer struct {
	state *markedInlineState
	top   bool
	queue []*markedInlineEntry
	// slabs hold the tokens that every document has most of (space, paragraph, text and heading tokens), so that lexing a streamed text again allocates none of them; slab and used are the next free token.
	slabs      [][]markedBlock
	slab, used int
	// topTokens is the token list of the last document, kept for the next.
	topTokens []*markedBlock
	// lists hold the token lists of list items and the item lists of lists, so that lexing a streamed text again allocates none of them; listSlab and listUsed are the next free entry.
	lists              [][]*markedBlock
	listSlab, listUsed int
	// chunk is the buffer that the strings of the document's tokens are copied into, so that a document costs a few allocations instead of one per string; sourceLen sizes the first chunk.
	chunk     strings.Builder
	sourceLen int
	// listRawBuf, itemRawBuf and itemTextBuf collect the pieces of the texts of a list and its items before they are copied to the chunk.
	listRawBuf, itemRawBuf, itemTextBuf []byte
	// runeBufs are the code points of the nested sources of list items and quotes, one buffer per nesting level; depth is the level being lexed.
	runeBufs [][]rune
	depth    int
}

// pig additive (D112): Pi's marked lexer keeps no state between renders and allocates each token; a lexer that reuses slabs of tokens lexes the same text to the same tokens.
const markedSlabSize = 16

// newBlock stores a token in the lexer's slabs.
func (l *markedBlockLexer) newBlock(token markedBlock) *markedBlock {
	for l.slab < len(l.slabs) && l.used == len(l.slabs[l.slab]) {
		l.slab, l.used = l.slab+1, 0
	}
	if l.slab == len(l.slabs) {
		l.slabs = append(l.slabs, make([]markedBlock, markedSlabSize))
	}
	slot := &l.slabs[l.slab][l.used]
	l.used++
	*slot = token
	return slot
}

// blockLexer is the lexer of the document the state belongs to, ready for a new source; its buffers are kept for the next one.
func (l *markedInlineState) blockLexer() *markedBlockLexer {
	lexer := &l.block
	lexer.state, lexer.top = l, true
	lexer.slab, lexer.used = 0, 0
	lexer.listSlab, lexer.listUsed, lexer.depth = 0, 0, 0
	// The strings of the last document may still be held (a code block's text is a key of the highlight memo), so its chunk is left to them.
	lexer.chunk = strings.Builder{}
	clear(lexer.queue)
	lexer.queue = lexer.queue[:0]
	return lexer
}

// pig additive (D112): Pi's marked lexer allocates each token list; a lexer that takes them from slabs it keeps lexes the same text to the same tokens.
// markedListSlabSize is the number of entries of one slab of token lists, and markedListCap the capacity of a token list taken from one.
const (
	markedListSlabSize = 256
	markedListCap      = 4
)

// tokenList is an empty token list with room for markedListCap tokens, taken from the lexer's slabs; a list that grows past it moves to the heap.
func (l *markedBlockLexer) tokenList() []*markedBlock {
	for l.listSlab < len(l.lists) && len(l.lists[l.listSlab])-l.listUsed < markedListCap {
		l.listSlab, l.listUsed = l.listSlab+1, 0
	}
	if l.listSlab == len(l.lists) {
		l.lists = append(l.lists, make([]*markedBlock, markedListSlabSize))
	}
	slab := l.lists[l.listSlab]
	start := l.listUsed
	l.listUsed += markedListCap
	return slab[start:start:l.listUsed]
}

// pig additive (D112): Pi allocates each string of a token; the lexer copies them into chunks, which changes how often memory is allocated and never a string. A chunk is never reused, because a string of the last document may still be held.
// markedChunkMin and markedChunkMax bound the size of the chunks that the lexer copies strings into.
const (
	markedChunkMin = 256
	markedChunkMax = 16 << 10
)

// bytesRoom makes the chunk able to take n more bytes: a chunk that is full is left to the strings in it, and a new one takes the next.
func (l *markedBlockLexer) bytesRoom(n int) {
	if l.chunk.Cap()-l.chunk.Len() >= n {
		return
	}
	l.chunk = strings.Builder{}
	l.chunk.Grow(max(n, min(max(2*l.sourceLen, markedChunkMin), markedChunkMax)))
}

// copyRunes is jsstring.FromCodePoints(runes) in the lexer's chunk.
func (l *markedBlockLexer) copyRunes(runes []rune) string {
	if len(runes) == 0 {
		return ""
	}
	n := 0
	for _, r := range runes {
		n += jsstring.CodePointLen(r)
	}
	l.bytesRoom(n)
	start := l.chunk.Len()
	writeCodePoints(&l.chunk, runes)
	return l.chunk.String()[start:]
}

// copyBytes is string(b) in the lexer's chunk.
func (l *markedBlockLexer) copyBytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	l.bytesRoom(len(b))
	start := l.chunk.Len()
	l.chunk.Write(b)
	return l.chunk.String()[start:]
}

// withNewline is s + "\n" in the lexer's chunk.
func (l *markedBlockLexer) withNewline(s string) string {
	l.bytesRoom(len(s) + 1)
	start := l.chunk.Len()
	l.chunk.WriteString(s)
	l.chunk.WriteByte('\n')
	return l.chunk.String()[start:]
}

// pushRunes is the code points of text in the buffer of the next nesting level, which stays the lexer's until popRunes.
func (l *markedBlockLexer) pushRunes(text string) []rune {
	l.depth++
	for len(l.runeBufs) <= l.depth {
		l.runeBufs = append(l.runeBufs, nil)
	}
	buf := l.runeBufs[l.depth][:0]
	//portlint:allow utf16units the byte length of text bounds its number of code points, which is all the buffer's capacity needs
	if cap(buf) < len(text) {
		buf = make([]rune, 0, len(text))
	}
	buf = jsstring.AppendCodePoints(buf, text)
	l.runeBufs[l.depth] = buf
	return buf
}

func (l *markedBlockLexer) popRunes() { l.depth-- }

// matchText is the text a match covers, with its lone surrogates spelled as WTF-8.
func (l *markedBlockLexer) matchText(m *regexp2.Match) string { return l.copyRunes(m.Runes()) }

// group is a capture group's text, "" when the group did not participate.
func (l *markedBlockLexer) group(match *regexp2.Match, group int) string {
	g := match.GroupByNumber(group)
	if g == nil || len(g.Captures) == 0 {
		return ""
	}
	return l.copyRunes(g.Runes())
}

// blockRunes is src as code points in a buffer that the state keeps for the next source.
func (l *markedInlineState) blockRunes(src string) []rune {
	if n := len(src); cap(l.blockSource) < n {
		l.blockSource = make([]rune, 0, n)
	}
	l.blockSource = jsstring.AppendCodePoints(l.blockSource[:0], src)
	return l.blockSource
}

// lexRunes is Lexer.lex up to the inline queue: the block tokens of a source whose line endings and tabs are normalized. A token's offset is where it starts in src.
func (l *markedBlockLexer) lexRunes(src []rune, str string) []*markedBlock {
	tokens := l.topTokens[:0]
	l.sourceLen = len(src)
	l.blockTokens(src, str, &tokens, false)
	l.topTokens = tokens
	return tokens
}

// appendBlock adds a token that starts at offset at of the source being lexed.
func appendBlock(tokens *[]*markedBlock, token *markedBlock, at int) {
	token.offset = at
	*tokens = append(*tokens, token)
}

// inline is Lexer.inline: the token list of src is lexed after the block pass, in the order of the queue. It registers the entry stored with token, which holds the list.
func (l *markedBlockLexer) inline(token *markedBlock, src string) *markedBlock {
	token.entryStore.src = src
	token.entry = &token.entryStore
	l.queue = append(l.queue, token.entry)
	return token
}

// inlineCell is Lexer.inline for a table cell, which has no token of its own.
func (l *markedBlockLexer) inlineCell(src string) *markedInlineEntry {
	entry := &markedInlineEntry{src: src}
	l.queue = append(l.queue, entry)
	return entry
}

func (l *markedBlockLexer) lastEntry() *markedInlineEntry { return l.queue[len(l.queue)-1] }

func markedLast(tokens []*markedBlock) *markedBlock {
	if len(tokens) == 0 {
		return nil
	}
	return tokens[len(tokens)-1]
}

// markedJoinRaw is `raw += (raw.endsWith('\n') ? ” : '\n') + next`.
func markedJoinRaw(raw, next string) string {
	if strings.HasSuffix(raw, "\n") {
		return raw + next
	}
	return raw + "\n" + next
}

// blockTokens lexes src, the code points of str: when every character of str is one byte, tokens take their text from str without copying it.
func (l *markedBlockLexer) blockTokens(src []rune, str string, tokens *[]*markedBlock, lastParagraphClipped bool) {
	pos := 0
	latexNext := -1
	for pos < len(src) {
		rest := src[pos:]
		at := pos
		restStr := ""
		if len(str) == len(src) {
			//portlint:allow utf16units len(str) == len(src) means every character of str is one byte, so the byte offset pos is the code point and code unit offset
			restStr = str[pos:]
		}

		if token, n := markedLatexBlock(rest); token != nil {
			appendBlock(tokens, token, at)
			pos += n
			continue
		}

		// newline
		if n := markedSpaceLength(rest); n > 0 {
			pos += n
			if last := markedLast(*tokens); n == 1 && last != nil {
				// A single newline terminates the last line, so it moves there.
				last.raw = l.withNewline(last.raw)
			} else {
				appendBlock(tokens, l.newBlock(markedBlock{kind: "space", raw: l.rawText(rest, restStr, n)}), at)
			}
			continue
		}

		// code
		if token, n := markedCode(rest); token != nil {
			pos += n
			// An indented code block cannot interrupt a paragraph.
			if last := markedLast(*tokens); last != nil && (last.kind == "paragraph" || last.kind == "text") {
				last.raw = markedJoinRaw(last.raw, token.raw)
				last.text += "\n" + token.text
				l.lastEntry().src = last.text
			} else {
				appendBlock(tokens, token, at)
			}
			continue
		}

		// fences
		if token, n := l.fence(rest); token != nil {
			appendBlock(tokens, token, at)
			pos += n
			continue
		}

		// heading
		if token, n := l.heading(rest); token != nil {
			appendBlock(tokens, token, at)
			pos += n
			continue
		}

		// hr
		if token, n := markedHr(rest); token != nil {
			appendBlock(tokens, token, at)
			pos += n
			continue
		}

		// blockquote
		if token, n := l.blockquote(rest); token != nil {
			appendBlock(tokens, token, at)
			pos += n
			continue
		}

		// list
		if token, n := l.list(rest, restStr); token != nil {
			appendBlock(tokens, token, at)
			pos += n
			continue
		}

		// html
		if token, n := markedHTMLBlock(rest); token != nil {
			appendBlock(tokens, token, at)
			pos += n
			continue
		}

		// def
		if token, n := markedDef(rest); token != nil {
			pos += n
			if last := markedLast(*tokens); last != nil && (last.kind == "paragraph" || last.kind == "text") {
				last.raw = markedJoinRaw(last.raw, token.raw)
				last.text += "\n" + token.raw
				l.lastEntry().src = last.text
			} else if _, defined := l.state.links[token.tag]; !defined {
				l.state.defineLink(token.tag, token.text)
				appendBlock(tokens, token, at)
			}
			continue
		}

		// table (gfm)
		if token, n := l.table(rest); token != nil {
			appendBlock(tokens, token, at)
			pos += n
			continue
		}

		// lheading
		if token, n := l.lheading(rest); token != nil {
			appendBlock(tokens, token, at)
			pos += n
			continue
		}

		// top-level paragraph: the src is clipped to the start of a block extension, which keeps the paragraph from consuming it.
		cutSrc := rest
		if start := markedLatexStartAt(src, pos, &latexNext); start >= 0 {
			cutSrc = rest[:start+1]
		}
		if l.top {
			if token, n := l.paragraph(cutSrc, restStr); token != nil {
				if last := markedLast(*tokens); lastParagraphClipped && last != nil && last.kind == "paragraph" {
					last.raw = markedJoinRaw(last.raw, token.raw)
					last.text += "\n" + token.text
					l.queue = l.queue[:len(l.queue)-1]
					l.lastEntry().src = last.text
				} else {
					appendBlock(tokens, token, at)
				}
				lastParagraphClipped = len(cutSrc) != len(rest)
				pos += n
				continue
			}
		}

		// text
		if token, n := l.text(rest, restStr); token != nil {
			pos += n
			if last := markedLast(*tokens); last != nil && last.kind == "text" {
				last.raw = markedJoinRaw(last.raw, token.raw)
				last.text += "\n" + token.text
				l.queue = l.queue[:len(l.queue)-1]
				l.lastEntry().src = last.text
			} else {
				appendBlock(tokens, token, at)
			}
			continue
		}

		// Lexer.infiniteLoopError: no rule consumed the source. Every source starts with a line of text, a blank line or a block, so this is unreachable.
		break
	}
	l.top = true
}

func markedRuneCount(s string) int { return jsstring.CodePointCount(s) }

// markedLineEnd is the index of the first "\n" of src from from, or len(src).
func markedLineEnd(src []rune, from int) int {
	for i := from; i < len(src); i++ {
		if src[i] == '\n' {
			return i
		}
	}
	return len(src)
}

// markedLeadingSpaces counts the spaces of src before the first other character.
func markedLeadingSpaces(src []rune) int {
	n := 0
	for n < len(src) && src[n] == ' ' {
		n++
	}
	return n
}

// markedSpaceLength is the length of rules.block.newline, /^(?:[ \t]*(?:\n|$))+/, on src.
func markedSpaceLength(src []rune) int {
	i := 0
	for {
		j := i
		for j < len(src) && (src[j] == ' ' || src[j] == '\t') {
			j++
		}
		switch {
		case j < len(src) && src[j] == '\n':
			i = j + 1
		case j == len(src):
			return j
		default:
			return i
		}
	}
}

// markedRtrim is rtrim(str, c).
func markedRtrim(s string, c byte) string {
	n := len(s)
	for n > 0 && s[n-1] == c {
		n--
	}
	return s[:n]
}

// markedCode is Tokenizer.code.
func markedCode(src []rune) (*markedBlock, int) {
	if len(src) < 4 || src[0] != ' ' || src[1] != ' ' || src[2] != ' ' || src[3] != ' ' {
		return nil, 0
	}
	match := markedBlockCode.exec(src)
	if match == nil {
		return nil, 0
	}
	raw := markedTrimTrailingBlankLines(matchString(match))
	// codeRemoveIndent, /^(?: {1,4}| {0,3}\t)/gm: the space alternative wins whenever a line starts with a space, and a line starts after "\n", "\r", U+2028 and U+2029.
	var text strings.Builder
	text.Grow(len(raw))
	atLineStart := true
	for i := 0; i < len(raw); {
		if atLineStart {
			spaces := 0
			for spaces < 4 && i+spaces < len(raw) && raw[i+spaces] == ' ' {
				spaces++
			}
			i += spaces
			if i == len(raw) {
				break
			}
		}
		r, size := jsstring.DecodeCodePoint(raw[i:])
		//portlint:allow utf16units size is the byte length of the code point DecodeCodePoint just decoded at i, so the copy writes that one code point with a lone surrogate's WTF-8 bytes intact
		text.WriteString(raw[i : i+size])
		i += size
		atLineStart = r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029'
	}
	return &markedBlock{kind: "code", raw: raw, text: text.String()}, markedRuneCount(raw)
}

// fence is Tokenizer.fences, whose rule is ^ {0,3}(`{3,}(?=[^`\n]*(?:\n|$))|~{3,})([^\n]*)(?:\n|$)(?:|([\s\S]*?)(?:\n|$))(?: {0,3}\1[~`]* *(?=\n|$)|$), matched without the pattern: the opening run takes every fence character, because a pattern that fails behind a shorter run fails behind the longer one as well; the rest of the opening line is the info string; the first line that closes the block (at most three spaces, a run of the opening character at least as long as the opening's, any more of the fence characters, spaces, and the end of the line) ends the match, and so does the end of the source.
func (l *markedBlockLexer) fence(src []rune) (*markedBlock, int) {
	if len(src) < 3 {
		return nil, 0
	}
	spaces := min(markedLeadingSpaces(src), 3)
	if spaces >= len(src) || src[spaces] != '`' && src[spaces] != '~' {
		return nil, 0
	}
	fence := src[spaces]
	run := 0
	for spaces+run < len(src) && src[spaces+run] == fence {
		run++
	}
	if run < 3 {
		return nil, 0
	}
	infoStart := spaces + run
	infoEnd := markedLineEnd(src, infoStart)
	// A backtick fence takes no backtick in its info string.
	if fence == '`' && slices.Contains(src[infoStart:infoEnd], '`') {
		return nil, 0
	}
	bodyStart := min(infoEnd+1, len(src))
	contentEnd, end := -1, markedFenceClose(src, bodyStart, fence, run)
	if end < 0 {
		// The first line that starts at the end of a line of the body and closes it; the end of the source always does.
		for at := bodyStart; ; at++ {
			if at < len(src) && src[at] != '\n' {
				continue
			}
			if end = markedFenceClose(src, min(at+1, len(src)), fence, run); end >= 0 {
				contentEnd = at
				break
			}
		}
	} else {
		contentEnd = bodyStart
	}
	raw := l.copyRunes(src[:end])
	// The info string and the content are parts of raw: a source of one-byte characters has their offsets as byte offsets.
	offset := func(n int) int {
		if len(raw) == end {
			return n
		}
		return markedByteLength(src[:n])
	}
	lang := ""
	//portlint:allow utf16units offset gives the byte offsets in raw of the code point indexes infoStart and infoEnd, so the slice is the info string
	if info := raw[offset(infoStart):offset(infoEnd)]; info != "" {
		lang = markedUnescapePunctuation(widthx.JSTrim(info))
	}
	// The body is a group of the match only when a line of it was read; a fence that closes on its first body line has no content, which is what an absent group reads as.
	//portlint:allow utf16units offset gives the byte offsets in raw of the code point indexes bodyStart and contentEnd, so the slice is that part of the fence's text
	content := raw[offset(bodyStart):offset(contentEnd)]
	token := l.newBlock(markedBlock{kind: "code", raw: raw, lang: lang, text: markedIndentCodeCompensation(raw, content)})
	return token, end
}

// markedFenceClose is the index after the closing line of a fence of run characters fence that starts at index at, or -1 when the line does not close it: at most three spaces, run fence characters, any more of "`" and "~", spaces, and then the end of the line or of the source; the end of the source closes the fence as well.
func markedFenceClose(src []rune, at int, fence rune, run int) int {
	i := at
	for i < len(src) && i-at < 3 && src[i] == ' ' {
		i++
	}
	if i+run <= len(src) {
		n := 0
		for n < run && src[i+n] == fence {
			n++
		}
		if n == run {
			i += run
			for i < len(src) && (src[i] == '~' || src[i] == '`') {
				i++
			}
			for i < len(src) && src[i] == ' ' {
				i++
			}
			if i == len(src) || src[i] == '\n' {
				return i
			}
		}
	}
	if at == len(src) {
		return at
	}
	return -1
}

// markedByteLength is the length of runes in WTF-8.
func markedByteLength(runes []rune) int {
	n := 0
	for _, r := range runes {
		n += jsstring.CodePointLen(r)
	}
	return n
}

// markedIndentCodeCompensation is Tokenizer.ts indentCodeCompensation: a fence indented by n spaces removes n whitespace characters from each code line that starts with at least as many.
func markedIndentCodeCompensation(raw, text string) string {
	// /^(\s+)(?:```)/: the run of whitespace before a backtick fence.
	indent := 0
	for indent < len(raw) && raw[indent] == ' ' {
		indent++
	}
	if indent == 0 || !strings.HasPrefix(raw[indent:], "```") {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		runes := jsstring.CodePoints(line)
		lead := 0
		for lead < len(runes) && jsstring.IsSpace(runes[lead]) {
			lead++
		}
		if lead >= indent {
			lines[i] = jsstring.FromCodePoints(runes[indent:])
		}
	}
	return strings.Join(lines, "\n")
}

// heading is Tokenizer.heading.
func (l *markedBlockLexer) heading(src []rune) (*markedBlock, int) {
	if len(src) == 0 {
		return nil, 0
	}
	spaces := min(markedLeadingSpaces(src), 3)
	if spaces >= len(src) || src[spaces] != '#' {
		return nil, 0
	}
	match := markedBlockHeading.exec(src)
	if match == nil {
		return nil, 0
	}
	hashes := match.GroupByNumber(1).Length
	body := l.group(match, 2)
	text := widthx.JSTrim(body)
	// remove trailing #s
	if strings.HasSuffix(text, "#") {
		trimmed := markedRtrim(text, '#')
		// CommonMark requires a space before the trailing #s.
		if trimmed == "" || strings.HasSuffix(trimmed, " ") {
			text = widthx.JSTrim(trimmed)
		}
	}
	raw := markedRtrim(l.matchText(match), '\n')
	return l.inline(l.newBlock(markedBlock{kind: "heading", raw: raw, depth: hashes, text: text}), text), markedRuneCount(raw)
}

// markedHr is Tokenizer.hr.
func markedHr(src []rune) (*markedBlock, int) {
	spaces := min(markedLeadingSpaces(src), 3)
	if spaces >= len(src) || src[spaces] != '-' && src[spaces] != '_' && src[spaces] != '*' {
		return nil, 0
	}
	match := markedBlockHr.exec(src)
	if match == nil {
		return nil, 0
	}
	raw := markedRtrim(matchString(match), '\n')
	return &markedBlock{kind: "hr", raw: raw}, markedRuneCount(raw)
}

// markedHTMLBlock is Tokenizer.html.
func markedHTMLBlock(src []rune) (*markedBlock, int) {
	spaces := min(markedLeadingSpaces(src), 3)
	if spaces >= len(src) || src[spaces] != '<' {
		return nil, 0
	}
	match := markedBlockHTML.exec(src)
	if match == nil {
		return nil, 0
	}
	raw := markedTrimTrailingBlankLines(matchString(match))
	return &markedBlock{kind: "html", raw: raw, text: raw}, markedRuneCount(raw)
}

// markedDef is Tokenizer.def: the token's text is the destination, and its tag the normalized label.
func markedDef(src []rune) (*markedBlock, int) {
	if spaces := markedLeadingSpaces(src); spaces > 3 || spaces >= len(src) || src[spaces] != '[' {
		return nil, 0
	}
	if !markedDefCandidate(jsstring.FromCodePoints(src[:markedLineEnd(src, 0)])) {
		return nil, 0
	}
	match := markedBlockDef.exec(src)
	if match == nil {
		return nil, 0
	}
	label, _ := markedGroup(match, 1)
	destination, _ := markedGroup(match, 2)
	// hrefBrackets, /^<(.*)>$/; the destination rule admits no line terminator for "." to reject.
	//portlint:allow utf16units Tokenizer.ts hrefBrackets (/^<(.*)>$/): the first and last bytes tested are ASCII "<" and ">", so the trim removes one unit at each end
	if len(destination) >= 2 && destination[0] == '<' && destination[len(destination)-1] == '>' {
		destination = destination[1 : len(destination)-1]
	}
	raw := markedRtrim(matchString(match), '\n')
	return &markedBlock{kind: "def", raw: raw, tag: markedLinkLabel(label), text: markedUnescapePunctuation(destination)}, markedRuneCount(raw)
}

// markedDelimiterRowAt reports whether the line that starts at index could be the delimiter row of a table: only spaces, pipes, colons and at least one dash.
func markedDelimiterRowAt(src []rune, index int) bool {
	if index >= len(src) {
		return false
	}
	dash := false
	for i := index; i < len(src) && src[i] != '\n'; i++ {
		switch src[i] {
		case '-':
			dash = true
		case ' ', '|', ':':
		default:
			return false
		}
	}
	return dash
}

// table is Tokenizer.table.
func (l *markedBlockLexer) table(src []rune) (*markedBlock, int) {
	if !markedDelimiterRowAt(src, markedLineEnd(src, 0)+1) {
		return nil, 0
	}
	match := markedBlockTable.exec(src)
	if match == nil {
		return nil, 0
	}
	header := l.group(match, 1)
	delimiter := l.group(match, 2)
	cells := l.group(match, 3)
	// A delimiter row without a pipe or colon is a setext underline.
	if !strings.ContainsAny(delimiter, ":|") {
		return nil, 0
	}
	headers := markedSplitCells(header, 0)
	// tableAlignChars, /^\||\| *$/g, removes the outer pipes before the delimiter row splits into columns.
	aligns := strings.TrimPrefix(delimiter, "|")
	if trimmed := strings.TrimRight(aligns, " "); strings.HasSuffix(trimmed, "|") {
		aligns = trimmed[:len(trimmed)-1]
	}
	var rowSources []string
	if widthx.JSTrim(cells) != "" {
		// tableRowBlankLine, /\n[ \t]*$/, drops a final whitespace-only line.
		if last := strings.LastIndexByte(cells, '\n'); last >= 0 && strings.Trim(cells[last+1:], " \t") == "" {
			cells = cells[:last]
		}
		rowSources = strings.Split(cells, "\n")
	}
	raw := markedRtrim(l.matchText(match), '\n')
	// header and align columns must be equal, rows can be different.
	if len(headers) != strings.Count(aligns, "|")+1 {
		return nil, 0
	}
	token := &markedBlock{kind: "table", raw: raw}
	for _, cell := range headers {
		token.header = append(token.header, l.inlineCell(cell))
	}
	for _, row := range rowSources {
		var entries []*markedInlineEntry
		for _, cell := range markedSplitCells(row, len(token.header)) {
			entries = append(entries, l.inlineCell(cell))
		}
		token.rows = append(token.rows, entries)
	}
	return token, markedRuneCount(raw)
}

// markedSetextUnderlineAt reports whether the line that starts at index is a setext underline, /^ {0,3}(=+|-+) *$/.
func markedSetextUnderlineAt(src []rune, index int) bool {
	i := index
	for i < len(src) && i-index < 3 && src[i] == ' ' {
		i++
	}
	if i >= len(src) || src[i] != '=' && src[i] != '-' {
		return false
	}
	c := src[i]
	for i < len(src) && src[i] == c {
		i++
	}
	for i < len(src) && src[i] == ' ' {
		i++
	}
	return i == len(src) || src[i] == '\n'
}

// markedSetextStopAt reports that the line src[start:end] ends the text of a setext heading for certain: it is blank, indented as code, or opens a fence, a quote, an ATX heading or a list item (the lookahead of lheadingCore, without its HTML and table alternatives).
func markedSetextStopAt(src []rune, start, end int) bool {
	line := src[start:end]
	spaces := markedLeadingSpaces(line)
	if spaces >= 4 {
		return true
	}
	if spaces == len(line) {
		return end < len(src)
	}
	switch c := line[spaces]; {
	case c == '>':
		return true
	case c == '`' || c == '~':
		return spaces+2 < len(line) && line[spaces+1] == c && line[spaces+2] == c
	case c == '#':
		hashes := 0
		for spaces+hashes < len(line) && line[spaces+hashes] == '#' {
			hashes++
		}
		return hashes <= 6 && (spaces+hashes == len(line) || jsstring.IsSpace(line[spaces+hashes]))
	case c == '*' || c == '+' || c == '-':
		return spaces+1 < len(line) && line[spaces+1] == ' '
	case c >= '0' && c <= '9':
		digits := 0
		for spaces+digits < len(line) && line[spaces+digits] >= '0' && line[spaces+digits] <= '9' {
			digits++
		}
		return digits <= 9 && spaces+digits+1 < len(line) && (line[spaces+digits] == '.' || line[spaces+digits] == ')') && line[spaces+digits+1] == ' '
	}
	return false
}

// lheading is Tokenizer.lheading.
func (l *markedBlockLexer) lheading(src []rune) (*markedBlock, int) {
	// The rule needs an underline line before the first blank line.
	found := false
	for i := markedLineEnd(src, 0); i < len(src); {
		start := i + 1
		end := markedLineEnd(src, start)
		if markedSetextUnderlineAt(src, start) {
			found = true
			break
		}
		if markedSetextStopAt(src, start, end) {
			break
		}
		i = end
	}
	if !found {
		return nil, 0
	}
	match := markedBlockLheading.exec(src)
	if match == nil {
		return nil, 0
	}
	body := l.group(match, 1)
	underline := l.group(match, 2)
	text := widthx.JSTrim(body)
	depth := 2
	if underline[0] == '=' {
		depth = 1
	}
	raw := markedRtrim(l.matchText(match), '\n')
	return l.inline(&markedBlock{kind: "heading", raw: raw, depth: depth, text: text}, text), markedRuneCount(raw)
}

// markedParagraphStarts are the characters a line may open with, after at most three spaces, to end a paragraph other than through an HTML tag, a table or a line of spaces; markedQuoteStarts are the same inside a blockquote, where a list item of any number ends it.
const (
	markedParagraphStarts = "-_*#>`~+1"
	markedQuoteStarts     = "-_*#>`~+0123456789"
)

// markedParagraphInterrupted reports whether the paragraph rule's lookahead, (?!hr|heading|lheading|blockquote|fences|list|html|table|[ \t]+\n), matches at the start of src; quote selects the rule inside a blockquote, which has no table alternative and takes a list item of any number.
func markedParagraphInterrupted(src []rune, quote bool) bool {
	spaces := markedLeadingSpaces(src)
	// [ \t]+\n: a line of spaces and tabs followed by a newline.
	i := 0
	for i < len(src) && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	if i > 0 && i < len(src) && src[i] == '\n' {
		return true
	}
	starts := markedParagraphStarts
	if quote {
		starts = markedQuoteStarts
	}
	candidate := src[0] == '<'
	if !candidate && spaces <= 3 && spaces < len(src) {
		candidate = strings.ContainsRune(starts, src[spaces])
	}
	if !quote && !candidate {
		candidate = markedDelimiterRowAt(src, markedLineEnd(src, 0)+1)
	}
	if !candidate {
		return false
	}
	if quote {
		return markedQuoteInterruptAt.exec(src) != nil
	}
	return markedParagraphInterruptAt.exec(src) != nil
}

// paragraph is Tokenizer.paragraph, whose rule is ^([^\n]+(?:\n(?!interrupt)[^\n]+)*).
func (l *markedBlockLexer) paragraph(src []rune, str string) (*markedBlock, int) {
	end := markedLineEnd(src, 0)
	if end == 0 {
		return nil, 0
	}
	for end < len(src) {
		start := end + 1
		lineEnd := markedLineEnd(src, start)
		if lineEnd == start || markedParagraphInterrupted(src[start:], false) {
			break
		}
		end = lineEnd
	}
	text := l.rawText(src, str, end)
	return l.inline(l.newBlock(markedBlock{kind: "paragraph", raw: text, text: text}), text), end
}

// text is Tokenizer.text.
func (l *markedBlockLexer) text(src []rune, str string) (*markedBlock, int) {
	end := markedLineEnd(src, 0)
	if end == 0 {
		return nil, 0
	}
	text := l.rawText(src, str, end)
	return l.inline(l.newBlock(markedBlock{kind: "text", raw: text, text: text}), text), end
}

// rawText is string(src[:n]), taken from str, the same text, when there is one.
func (l *markedBlockLexer) rawText(src []rune, str string, n int) string {
	if str != "" {
		return str[:n]
	}
	return l.copyRunes(src[:n])
}

// markedLatexStartAt is the start() of the latexBlock extension on src[pos:].slice(1): the index in that slice of the first display-math start at its beginning or behind a line break, or -1. The cut is src.substring(0, start+1). It serves the successive positions of one lexing: next remembers the first display-math start behind a line break after the last position (-1 before the first call), so the search reads each line of the text once.
func markedLatexStartAt(src []rune, pos int, next *int) int {
	if pos+1 >= len(src) {
		return -1
	}
	// At the beginning of the slice, unless its first character is outside the BMP: slice(1) then leaves a lone low surrogate, which no start matches.
	if src[pos] <= 0xFFFF && markedLatexOpener(src[pos+1:]) {
		return 0
	}
	if *next < pos+2 {
		*next = len(src) + 1
		for q := pos + 2; q < len(src); q++ {
			if src[q-1] == '\n' && markedLatexOpener(src[q:]) {
				*next = q
				break
			}
		}
	}
	if *next > len(src) {
		return -1
	}
	return *next - pos - 1
}

// markedLatexOpener is / {0,3}(?:\$\$|\\\[)/ at the start of src.
func markedLatexOpener(src []rune) bool {
	spaces := min(markedLeadingSpaces(src), 3)
	if spaces+1 >= len(src) {
		return false
	}
	return src[spaces] == '$' && src[spaces+1] == '$' || src[spaces] == '\\' && src[spaces+1] == '['
}

// markedLatexBlock is the tokenizer of the latexBlock extension.
func markedLatexBlock(src []rune) (*markedBlock, int) {
	if !markedLatexOpener(src) {
		return nil, 0
	}
	token, ok := tokenizeBlockLatex(jsstring.FromCodePoints(src))
	if !ok {
		return nil, 0
	}
	return &markedBlock{kind: "latexBlock", raw: token.raw, text: token.text, pending: token.pending}, markedRuneCount(token.raw)
}

// markedStripQuoteMarkers is s.replace(/^ {0,3}>[ \t]?/gm, ”).
func markedStripQuoteMarkers(s string) string {
	var out strings.Builder
	lineStart := true
	for i := 0; i < len(s); {
		if lineStart {
			j := 0
			for j < 3 && i+j < len(s) && s[i+j] == ' ' {
				j++
			}
			if i+j < len(s) && s[i+j] == '>' {
				i += j + 1
				if i < len(s) && (s[i] == ' ' || s[i] == '\t') {
					i++
				}
				lineStart = false
				// The multiline anchor matches after the next line terminator only.
				continue
			}
		}
		r, size := jsstring.DecodeCodePoint(s[i:])
		//portlint:allow utf16units size is the byte length of the code point DecodeCodePoint just decoded at i, so the copy writes that one code point with a lone surrogate's WTF-8 bytes intact
		out.WriteString(s[i : i+size])
		i += size
		lineStart = r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029'
	}
	return out.String()
}

// markedQuoteMatch is rules.block.blockquote, ^( {0,3}> ?(paragraph|[^\n]*)(?:\n|$))+, on src: the length of the match, or 0.
func markedQuoteMatch(src []rune) int {
	pos := 0
	for {
		spaces := markedLeadingSpaces(src[pos:])
		if spaces > 3 || pos+spaces >= len(src) || src[pos+spaces] != '>' {
			return pos
		}
		at := pos + spaces + 1
		if at < len(src) && src[at] == ' ' {
			at++
		}
		// paragraph: a line and the lazy lines the paragraph rule of a blockquote takes after it; otherwise the rest of the line.
		end := markedLineEnd(src, at)
		if end > at {
			for end < len(src) {
				start := end + 1
				lineEnd := markedLineEnd(src, start)
				if lineEnd == start || markedParagraphInterrupted(src[start:], true) {
					break
				}
				end = lineEnd
			}
		}
		if end < len(src) {
			end++
		}
		pos = end
		if pos >= len(src) {
			return pos
		}
	}
}

// blockquote is Tokenizer.blockquote.
func (l *markedBlockLexer) blockquote(src []rune) (*markedBlock, int) {
	spaces := min(markedLeadingSpaces(src), 3)
	if spaces >= len(src) || src[spaces] != '>' {
		return nil, 0
	}
	matched := markedQuoteMatch(src)
	if matched == 0 {
		return nil, 0
	}
	token := l.blockquoteOf(jsstring.FromCodePoints(src[:matched]))
	return token, markedConsumed(src, token.raw)
}

// markedConsumed is the number of code points of src that src.substring(raw.length) removes: a blockquote's raw is spliced from pieces of different lengths, so its length in UTF-16 code units need not be that of the text it matched. A cut inside a surrogate pair leaves the low surrogate at the start of src.
func markedConsumed(src []rune, raw string) int {
	units := jsstring.Length(raw)
	index, counted := 0, 0
	for index < len(src) && counted < units {
		if src[index] > 0xFFFF {
			counted += 2
		} else {
			counted++
		}
		index++
	}
	if counted > units {
		_, low := utf16.EncodeRune(src[index-1])
		src[index-1] = low
		index--
	}
	return index
}

// blockquoteOf is the body of Tokenizer.blockquote for the text cap[0] of the rule.
func (l *markedBlockLexer) blockquoteOf(match string) *markedBlock {
	lines := strings.Split(markedRtrim(match, '\n'), "\n")
	raw, text := "", ""
	var tokens []*markedBlock

	for len(lines) > 0 {
		inBlockquote := false
		var currentLines []string

		i := 0
		for ; i < len(lines); i++ {
			// get lines up to a continuation
			switch {
			case markedQuoteStart(lines[i]):
				currentLines = append(currentLines, lines[i])
				inBlockquote = true
				continue
			case !inBlockquote:
				currentLines = append(currentLines, lines[i])
				continue
			}
			break
		}
		lines = lines[i:]

		currentRaw := strings.Join(currentLines, "\n")
		// precede setext continuation with 4 spaces so it isn't a setext
		currentText := markedStripQuoteMarkers(markedBlockquoteSetextReplace(currentRaw))
		if raw != "" {
			raw += "\n" + currentRaw
		} else {
			raw = currentRaw
		}
		if text != "" {
			text += "\n" + currentText
		} else {
			text = currentText
		}

		// parse blockquote lines as top level tokens; merge paragraphs if this is a continuation
		top := l.top
		l.top = true
		l.blockTokens(l.pushRunes(currentText), currentText, &tokens, true)
		l.popRunes()
		l.top = top

		// if there is no continuation then we are done
		if len(lines) == 0 {
			break
		}

		last := markedLast(tokens)
		switch {
		case last != nil && last.kind == "code":
			// blockquote continuation cannot be preceded by a code block
			return &markedBlock{kind: "blockquote", raw: raw, tokens: tokens, text: text}
		case last != nil && last.kind == "blockquote":
			// include continuation in nested blockquote
			continuation := strings.Join(lines, "\n")
			newText := last.raw + "\n" + markedStripQuoteMarkers(continuation)
			newToken, _ := l.blockquote(l.pushRunes(newText))
			l.popRunes()
			tokens[len(tokens)-1] = newToken

			raw = raw + "\n" + continuation
			text = markedWithoutTail(text, last.text) + newToken.text
			return &markedBlock{kind: "blockquote", raw: raw, tokens: tokens, text: text}
		case last != nil && last.kind == "list":
			// include continuation in nested list
			newText := last.raw + "\n" + strings.Join(lines, "\n")
			newToken, _ := l.list(l.pushRunes(newText), "")
			l.popRunes()
			tokens[len(tokens)-1] = newToken
			raw = markedWithoutTail(raw, last.raw) + newToken.raw
			text = markedWithoutTail(text, last.raw) + newToken.raw
			lines = strings.Split(markedSubstring(newText, newToken.raw), "\n")
		}
	}
	return &markedBlock{kind: "blockquote", raw: raw, tokens: tokens, text: text}
}

// markedWithoutTail is s.substring(0, s.length - tail.length): lengths count UTF-16 code units, and tail is not necessarily the end of s.
func markedWithoutTail(s, tail string) string {
	return jsstring.Slice(s, 0, max(0, jsstring.Length(s)-jsstring.Length(tail)))
}

// markedSubstring is text.substring(raw.length).
func markedSubstring(text, raw string) string {
	return jsstring.Slice(text, jsstring.Length(raw))
}

// markedQuoteStart is other.blockquoteStart, /^ {0,3}>/.
func markedQuoteStart(line string) bool {
	spaces := 0
	for spaces < len(line) && line[spaces] == ' ' {
		spaces++
	}
	return spaces <= 3 && spaces < len(line) && line[spaces] == '>'
}

// markedBlockquoteSetextReplace is s.replace(blockquoteSetextReplace, '\n    $1'): a lazy line that is only a setext underline is indented so that it stays text.
func markedBlockquoteSetextReplace(s string) string {
	if !strings.ContainsAny(s, "=-") {
		return s
	}
	out, err := markedBlockquoteSetext.compiled().Replace(s, "\n    $1", -1, -1)
	if err != nil {
		return s
	}
	return out
}

// markedBulletEnd is the index after the list marker that starts src at index from ([*+-] or one to nine digits and . or )), and whether there is one with a space, a tab, a newline or the end after it.
func markedBulletEnd(src []rune, from int) (int, bool) {
	i := from
	if i >= len(src) {
		return 0, false
	}
	if c := src[i]; c == '*' || c == '+' || c == '-' {
		i++
	} else {
		for i < len(src) && i-from < 9 && src[i] >= '0' && src[i] <= '9' {
			i++
		}
		if i == from || i >= len(src) || src[i] != '.' && src[i] != ')' {
			return 0, false
		}
		i++
	}
	if i < len(src) && src[i] != ' ' && src[i] != '\t' && src[i] != '\n' {
		return 0, false
	}
	return i, true
}

// markedIndented is the line after at most n leading spaces, and whether it has at most n.
func markedIndented(line string, n int) (string, bool) {
	spaces := 0
	for spaces < len(line) && line[spaces] == ' ' {
		spaces++
	}
	return line[spaces:], spaces <= n
}

// markedItemBegin is the rule that other.nextBulletRegex(indent) builds, on one line: another bullet of any kind within the indent, with a space, a tab or the end of the line after it.
func markedItemBegin(line string, n int) bool {
	rest, ok := markedIndented(line, n)
	if !ok || rest == "" {
		return false
	}
	end := 1
	if c := rest[0]; c != '*' && c != '+' && c != '-' {
		for end < len(rest) && end <= 9 && rest[end-1] >= '0' && rest[end-1] <= '9' {
			end++
		}
		if end == 1 && (rest[0] < '0' || rest[0] > '9') || rest[end-1] != '.' && rest[end-1] != ')' {
			return false
		}
		if end < 2 {
			return false
		}
	}
	return end == len(rest) || rest[end] == ' ' || rest[end] == '\t'
}

// markedHrBegin is other.hrRegex(indent) on one line: three or more of one of - _ * with spaces between, within the indent.
func markedHrBegin(line string, n int) bool {
	rest, ok := markedIndented(line, n)
	if !ok || rest == "" || rest[0] != '-' && rest[0] != '_' && rest[0] != '*' {
		return false
	}
	count := 0
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case rest[0]:
			count++
		case ' ':
		default:
			return false
		}
	}
	return count >= 3
}

// markedFencesBegin is other.fencesBeginRegex(indent), /^ {0,n}(?:```|~~~)/.
func markedFencesBegin(line string, n int) bool {
	rest, ok := markedIndented(line, n)
	return ok && (strings.HasPrefix(rest, "```") || strings.HasPrefix(rest, "~~~"))
}

// markedHeadingBegin is other.headingBeginRegex(indent), /^ {0,n}#/.
func markedHeadingBegin(line string, n int) bool {
	rest, ok := markedIndented(line, n)
	return ok && strings.HasPrefix(rest, "#")
}

// markedBlockquoteBegin is other.blockquoteBeginRegex(indent), /^ {0,n}>/.
func markedBlockquoteBegin(line string, n int) bool {
	rest, ok := markedIndented(line, n)
	return ok && strings.HasPrefix(rest, ">")
}

// markedHTMLBeginAt is other.htmlBeginRegex(indent), /^ {0,n}<(?:[a-z].*>|!--)/i, where . excludes the line terminators.
func markedHTMLBeginAt(line string, n int) bool {
	rest, ok := markedIndented(line, n)
	//portlint:allow utf16units the match needs rest[0] to be the ASCII '<' and rest[1] a further byte: a one-unit rest is a single byte, and any other first byte fails either way
	if !ok || len(rest) < 2 || rest[0] != '<' {
		return false
	}
	if strings.HasPrefix(rest[1:], "!--") {
		return true
	}
	if c := rest[1] | 0x20; c < 'a' || c > 'z' {
		return false
	}
	//portlint:allow utf16units rest[1] matched an ASCII letter, so rest[2:] starts on a character boundary; the loop only looks for the ASCII '>' and the line terminators
	for _, r := range rest[2:] {
		switch r {
		case '>':
			return true
		case '\r', '\u2028', '\u2029':
			return false
		}
	}
	return false
}

// markedTaskPrefix is the length of other.listReplaceTask, /^\[[ xX]\] +/, on s, and whether it is a task (other.listIsTask, /^\[[ xX]\] +\S/) as well.
func markedTaskPrefix(s string) (length int, isTask bool) {
	//portlint:allow utf16units the rule is ASCII bytes only ("[", one of " xX", "]", " "): a string shorter than four bytes or with another byte there does not match, whatever its UTF-16 length
	if len(s) < 4 || s[0] != '[' || s[1] != ' ' && s[1] != 'x' && s[1] != 'X' || s[2] != ']' || s[3] != ' ' {
		return 0, false
	}
	length = 3
	for length < len(s) && s[length] == ' ' {
		length++
	}
	if length >= len(s) {
		return length, false
	}
	r, _ := jsstring.DecodeCodePoint(s[length:])
	return length, !jsstring.IsSpace(r)
}

// markedEndsWithBlankLine is other.doubleBlankLine, /\n[ \t]*\n[ \t]*$/, on raw.
func markedEndsWithBlankLine(raw string) bool {
	trimmed := strings.TrimRight(raw, " \t")
	if !strings.HasSuffix(trimmed, "\n") {
		return false
	}
	return strings.HasSuffix(strings.TrimRight(trimmed[:len(trimmed)-1], " \t"), "\n")
}

// markedFirstNonSpace is line.search(/[^ ]/): the index of the first character that is not a space, or -1.
func markedFirstNonSpace(line string) int {
	index := 0
	for _, r := range line {
		if r != ' ' {
			return index
		}
		index++
	}
	return -1
}

// markedSliceUnits is line.slice(from): from counts UTF-16 code units.
func markedSliceUnits(line string, from int) string { return jsstring.Slice(line, from) }

// list is Tokenizer.list.
func (l *markedBlockLexer) list(src []rune, str string) (*markedBlock, int) {
	spaces := markedLeadingSpaces(src)
	if spaces > 3 {
		return nil, 0
	}
	end, ok := markedBulletEnd(src, spaces)
	if !ok {
		return nil, 0
	}
	bull := src[spaces:end] // the bullet rule matched ASCII
	ordered := len(bull) > 1
	list := l.newBlock(markedBlock{kind: "list", ordered: ordered, items: l.tokenList()})
	if ordered {
		//portlint:allow numbers bull is one to nine ASCII digits and a . or ) (markedBulletEnd), so Atoi on the digits is parseInt and cannot fail
		list.start, _ = strconv.Atoi(string(bull[:len(bull)-1]))
	}

	// Get next list item. The text of an item, and of the list, is collected in buffers that are kept for the next list and copied to the chunk once.
	endsWithBlankLine := false
	pos := 0
	listRaw := l.listRawBuf[:0]
	raw, contents := l.itemRawBuf[:0], l.itemTextBuf[:0]
	// Check if current bullet point can start a new List Item.
	for pos < len(src) {
		rest := src[pos:]
		bulletLength, itemEnd, ok := markedListItem(rest, bull)
		if !ok {
			break
		}
		if _, isHr := markedHr(rest); isHr != 0 {
			// End list if bullet was actually HR.
			break
		}

		raw = appendCodePoints(raw[:0], rest[:itemEnd])
		pos += itemEnd

		line := l.lineAt(src, str, pos-itemEnd+bulletLength)
		nextLine := l.lineAt(src, str, pos)
		nextLinePos := pos
		blankLine := widthx.JSTrim(line) == ""
		endEarly := false

		indent := 0
		contents = contents[:0]
		if blankLine {
			indent = bulletLength + 1
		} else {
			// Find first non-space char.
			indent = markedFirstNonSpace(line)
			if indent > 4 {
				// Treat indented code blocks (> 4 spaces) as having only 1 indent.
				indent = 1
			}
			contents = append(contents, markedSliceUnits(line, indent)...)
			indent += bulletLength
		}

		if blankLine && strings.Trim(nextLine, " \t") == "" {
			// Items begin with at most one blank line.
			raw = append(append(raw, nextLine...), '\n')
			pos = min(len(src), pos+markedRuneCount(nextLine)+1)
			endEarly = true
		}

		if !endEarly {
			begin := max(0, min(3, indent-1))
			// Check if following lines should be included in List Item.
			for pos < len(src) {
				rawLine := nextLine
				if pos != nextLinePos {
					rawLine = l.lineAt(src, str, pos)
				}
				nextLine = rawLine

				if markedFencesBegin(nextLine, begin) || markedHeadingBegin(nextLine, begin) || markedHTMLBeginAt(nextLine, begin) || markedBlockquoteBegin(nextLine, begin) || markedItemBegin(nextLine, begin) || markedHrBegin(nextLine, begin) {
					break
				}

				if markedFirstNonSpace(nextLine) >= indent || widthx.JSTrim(nextLine) == "" {
					// Dedent if possible.
					contents = append(append(contents, '\n'), markedSliceUnits(nextLine, indent)...)
				} else {
					// not enough indentation
					if blankLine {
						break
					}
					// paragraph continuation unless last line was a different block level element
					if markedFirstNonSpace(line) >= 4 {
						// indented code block
						break
					}
					if markedFencesBegin(line, begin) || markedHeadingBegin(line, begin) || markedHrBegin(line, begin) {
						break
					}
					contents = append(append(contents, '\n'), nextLine...)
				}

				blankLine = widthx.JSTrim(nextLine) == ""
				raw = append(append(raw, rawLine...), '\n')
				pos = min(len(src), pos+markedRuneCount(rawLine)+1)
				line = markedSliceUnits(nextLine, indent)
			}
		}

		itemRaw := l.copyBytes(raw)
		if !list.loose {
			// If the previous item ended with a blank line, the list is loose.
			if endsWithBlankLine {
				list.loose = true
			} else if markedEndsWithBlankLine(itemRaw) {
				endsWithBlankLine = true
			}
		}

		itemContents := l.copyBytes(contents)
		_, task := markedTaskPrefix(itemContents)
		list.items = append(list.items, l.newBlock(markedBlock{kind: "list_item", raw: itemRaw, task: task, text: itemContents}))
		listRaw = append(listRaw, itemRaw...)
	}
	l.listRawBuf, l.itemRawBuf, l.itemTextBuf = listRaw, raw, contents

	// Do not consume newlines at end of final item.
	if len(list.items) == 0 {
		// not a list since there were no items
		return nil, 0
	}
	lastItem := list.items[len(list.items)-1]
	lastItem.raw = widthx.JSTrimEnd(lastItem.raw)
	lastItem.text = widthx.JSTrimEnd(lastItem.text)
	list.raw = widthx.JSTrimEnd(l.copyBytes(listRaw))

	// Item child tokens handled here at end because we needed to have the final item to trim it first. First pass: tokenize items and finalize list.loose from spacers before placing checkboxes.
	for _, item := range list.items {
		l.top = false
		tokens := l.tokenList()
		l.blockTokens(l.pushRunes(item.text), item.text, &tokens, false)
		l.popRunes()
		item.tokens = tokens

		if !list.loose {
			// Check if list should be loose.
			for _, token := range tokens {
				if token.kind == "space" && strings.Count(token.raw, "\n") >= 2 {
					list.loose = true
					break
				}
			}
		}
	}

	// Second pass: place task checkboxes using the final list.loose.
	for _, item := range list.items {
		var first *markedBlock
		if len(item.tokens) > 0 {
			first = item.tokens[0]
		}
		if item.task && first != nil && (first.kind == "text" || first.kind == "paragraph") {
			// Remove checkbox markdown from item tokens.
			item.text = markedTrimTask(item.text)
			first.raw = markedTrimTask(first.raw)
			first.text = markedTrimTask(first.text)
			for _, entry := range slices.Backward(l.queue) {
				if _, isTask := markedTaskPrefix(entry.src); isTask {
					entry.src = markedTrimTask(entry.src)
					break
				}
			}
			// The checkbox token renders nothing; the item keeps its state.
			item.checked = markedCheckboxChecked(item.raw)
		} else if item.task {
			item.task = false
		}
	}

	// Set all items to loose if list is loose.
	if list.loose {
		for _, item := range list.items {
			item.loose = true
			for _, token := range item.tokens {
				if token.kind == "text" {
					token.kind = "paragraph"
				}
			}
		}
	}
	return list, markedRuneCount(list.raw)
}

// markedTrimTask is s.replace(other.listReplaceTask, ”).
func markedTrimTask(s string) string {
	if length, _ := markedTaskPrefix(s); length > 0 {
		return s[length:]
	}
	return s
}

// markedCheckboxChecked is whether the first checkbox of raw, other.listTaskCheckbox /\[[ xX]\]/, is not an empty one.
func markedCheckboxChecked(raw string) bool {
	for i := 0; i+2 < len(raw); i++ {
		if raw[i] == '[' && raw[i+2] == ']' && (raw[i+1] == ' ' || raw[i+1] == 'x' || raw[i+1] == 'X') {
			return raw[i+1] != ' '
		}
	}
	return false
}

// lineAt is the text of src from index from to its next newline, taken from str, the same text, when there is one, and copied to the lexer's chunk when there is not.
func (l *markedBlockLexer) lineAt(src []rune, str string, from int) string {
	end := markedLineEnd(src, from)
	if str != "" {
		return str[from:end]
	}
	return l.copyRunes(src[from:end])
}

// appendCodePoints appends the text of runes to dst, a lone surrogate spelled as WTF-8.
func appendCodePoints(dst []byte, runes []rune) []byte {
	for _, r := range runes {
		dst = jsstring.AppendCodePoint(dst, r)
	}
	return dst
}

// markedListItem is the item rule other.listItemRegex(bull) builds, ^( {0,3}bull)((?:[\t ][^\n]*)?(?:\n|$)), on src: the length of its first group, the length of the whole match, and whether it matches.
func markedListItem(src []rune, bull []rune) (bulletLength, length int, ok bool) {
	spaces := markedLeadingSpaces(src)
	if spaces > 3 {
		return 0, 0, false
	}
	end, ok := markedBulletEnd(src, spaces)
	if !ok {
		return 0, 0, false
	}
	marker := src[spaces:end] // the bullet rule matched ASCII
	if len(bull) == 1 && (len(marker) != 1 || marker[0] != bull[0]) || len(bull) > 1 && (len(marker) < 2 || marker[len(marker)-1] != bull[len(bull)-1]) {
		return 0, 0, false
	}
	lineEnd := markedLineEnd(src, end)
	return end, lineEnd + min(1, len(src)-lineEnd), true
}

// writeCodePoints writes the text of runes to b, a lone surrogate spelled as WTF-8, without building a string.
func writeCodePoints(b *strings.Builder, runes []rune) {
	for _, r := range runes {
		if r >= 0xD800 && r <= 0xDFFF {
			b.WriteString(jsstring.CodePointString(r))
			continue
		}
		b.WriteRune(r)
	}
}
