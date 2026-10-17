package tui

import (
	"reflect"
	"strings"
)

// Nested emphasis renders through ansiSpan once per level: each level replaces its close code in the whole inner string, so a run of n delimiters allocates O(n^3) bytes on the way to an O(n^2) result (marked's `*`×4000 gives 8 MB of output and 16 GB of garbage). This file computes the same bytes in one pass.
//
// Each ansiSpan level is a map on the atoms of its text: its close code becomes close+open and each newline becomes close+newline+open (a CR before the newline stays attached to it). Maps compose, so an atom emitted below k levels is the k maps applied innermost first. spanTable holds that composition for the levels above the current position as ropes, and one level changes only five entries. Emitting an atom then writes its rope: the cost is the output size.
//
// A context with a text style (a thinking block, a heading, a quote) adds two rules to the per-level path: each text segment is styled and the style's opening codes P are appended after every emphasis, code span and link, and each level then trims trailing copies of P from its text before decorating it. P holds only opening codes, so the maps leave it alone; the writer counts the trailing copies as pending and writes them only when something follows in the same level, which is the trim.
//
// Exactness holds while no close code can be formed across two chunks of text and the theme is the stock one; anything else returns to renderInlineTokensSequential, the per-level implementation.

type spanKind int

const (
	spanBold spanKind = iota
	spanItalic
	spanStrike
	spanKinds
)

// spanAtoms are the strings a level rewrites: the three close codes, then a newline and a CR-newline pair.
const (
	spanNewline = iota + int(spanKinds)
	spanCRNewline
	spanAtomCount
)

type spanCodes struct{ open, close string }

var spanCodeTable = [spanKinds]spanCodes{
	spanBold:   {"\x1b[1m", SGRBoldDimReset},
	spanItalic: {"\x1b[3m", SGRItalicReset},
	spanStrike: {"\x1b[9m", SGRStrikeReset},
}

// rope is an immutable string built from literals; a node with no children is a literal.
type rope struct {
	left, right *rope
	literal     string
	size        int
}

func (r *rope) isLiteral() bool { return r.left == nil }

func ropeOf(s string) *rope { return &rope{literal: s, size: len(s)} }

func ropeConcat(a, b *rope) *rope { return &rope{left: a, right: b, size: a.size + b.size} }

// spanWriter emits the rendering of one inline token list in two passes over the same tokens. The first only counts: it sizes the result and decides which levels have empty text. The second writes into a buffer of exactly that size.
type spanWriter struct {
	counting bool
	sb       strings.Builder
	// length is the bytes emitted so far in either pass. emitted only grows (a held CR counts when it is held, and again when written), so a level whose emitted count is unchanged had empty text.
	length, emitted int
	// table is the composed map for the current position: the rope each rewritten atom becomes.
	table [spanAtomCount]*rope
	depth int
	// holdCR is a CR not emitted yet, because a newline emitted next makes it part of a CR-newline pair.
	holdCR bool
	// prefix is the context's opening codes P; pending counts the copies of P at the end of the current level's text that nothing has followed yet.
	prefix  string
	pending int
	// emptyLevels has one entry per level in traversal order: true when its text was empty, so it emits neither open nor close code (markdownDecoration returns "" for empty text).
	emptyLevels []bool
	levelIndex  int
	// chunks holds the theme-produced strings (code spans, links) the first pass computed, in order, so the second pass reuses them.
	chunks     []string
	chunkIndex int
	stack      []*rope
	ok         bool
}

// spanIdentityTable maps every atom to itself, the table outside any level; spanOpenRopes are the open codes. Ropes are immutable, so every writer shares them.
var spanIdentityTable, spanOpenRopes = func() ([spanAtomCount]*rope, [spanKinds]*rope) {
	var table [spanAtomCount]*rope
	var open [spanKinds]*rope
	for kind := range spanKinds {
		table[kind] = ropeOf(spanCodeTable[kind].close)
		open[kind] = ropeOf(spanCodeTable[kind].open)
	}
	table[spanNewline] = ropeOf("\n")
	table[spanCRNewline] = ropeOf("\r\n")
	return table, open
}()

func newSpanWriter(counting bool, style inlineStyleContext) *spanWriter {
	return &spanWriter{counting: counting, ok: true, prefix: style.stylePrefix, table: spanIdentityTable}
}

func (w *spanWriter) flushCR() {
	if w.holdCR {
		w.holdCR = false
		w.emit("\r")
	}
}

func (w *spanWriter) emit(s string) {
	w.length += len(s)
	w.emitted += len(s)
	if !w.counting {
		w.sb.WriteString(s)
	}
}

// flushPending writes the pending copies of P: something now follows them in the level, so they are not trailing.
func (w *spanWriter) flushPending() {
	if w.pending == 0 {
		return
	}
	w.flushCR()
	for range w.pending {
		w.emit(w.prefix)
	}
	w.pending = 0
}

func (w *spanWriter) writeLiteral(s string) {
	if s == "" {
		return
	}
	w.flushPending()
	w.flushCR()
	w.emit(s)
}

func (w *spanWriter) writeRope(r *rope) {
	if w.counting {
		if r.size > 0 {
			w.flushPending()
			w.flushCR()
			w.length += r.size
			w.emitted += r.size
		}
		return
	}
	w.stack = append(w.stack[:0], r)
	for len(w.stack) > 0 {
		top := w.stack[len(w.stack)-1]
		w.stack = w.stack[:len(w.stack)-1]
		if top.isLiteral() {
			w.writeLiteral(top.literal)
			continue
		}
		w.stack = append(w.stack, top.right, top.left)
	}
}

// chunk returns the theme-produced string compute makes, computing it once across both passes.
func (w *spanWriter) chunk(compute func() string) string {
	if w.counting {
		s := compute()
		w.chunks = append(w.chunks, s)
		return s
	}
	s := w.chunks[w.chunkIndex]
	w.chunkIndex++
	return s
}

// writeNewline emits the newline atom, joined with a held CR. A chunk's own CR-newline pair leaves a held CR standing alone before it.
func (w *spanWriter) writeNewline(crBefore bool) {
	w.flushPending()
	if crBefore {
		w.flushCR()
	}
	if crBefore || w.holdCR {
		w.holdCR = false
		w.writeRope(w.table[spanCRNewline])
		return
	}
	w.writeRope(w.table[spanNewline])
}

// writeText emits one chunk. A chunk under a level must not contain ESC unless escapesAreCodes, which says it is theme output made of complete SGR codes: a partial code could join a neighbouring chunk into a close code, which only the per-level path reproduces.
func (w *spanWriter) writeText(s string, escapesAreCodes bool) {
	if s == "" {
		return
	}
	if w.depth > 0 && strings.IndexByte(s, '\x1b') >= 0 {
		if !escapesAreCodes || !onlyCompleteSGR(s) {
			w.ok = false
			return
		}
	}
	for len(s) > 0 {
		i := strings.IndexAny(s, "\n\x1b\r")
		if i < 0 {
			w.writeLiteral(s)
			return
		}
		w.writeLiteral(s[:i])
		s = s[i:]
		switch s[0] {
		case '\n':
			w.writeNewline(false)
			s = s[1:]
		case '\r':
			if len(s) > 1 && s[1] == '\n' {
				w.writeNewline(true)
				s = s[2:]
				continue
			}
			if len(s) == 1 {
				w.flushPending()
				w.flushCR()
				w.holdCR = true
				w.emitted++
				return
			}
			w.writeLiteral("\r")
			s = s[1:]
		default:
			kind := -1
			for k := range spanKinds {
				if strings.HasPrefix(s, spanCodeTable[k].close) {
					kind = int(k)
					break
				}
			}
			if kind >= 0 {
				w.writeRope(w.table[kind])
				s = s[len(spanCodeTable[kind].close):]
			} else {
				w.writeLiteral(s[:1])
				s = s[1:]
			}
		}
	}
}

// onlyCompleteSGR reports whether every ESC in s starts a complete `ESC [ digits/semicolons m` code.
func onlyCompleteSGR(s string) bool {
	for {
		i := strings.IndexByte(s, '\x1b')
		if i < 0 {
			return true
		}
		s = s[i+1:]
		if len(s) == 0 || s[0] != '[' {
			return false
		}
		j := 1
		for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == ';') {
			j++
		}
		if j >= len(s) || s[j] != 'm' {
			return false
		}
		s = s[j+1:]
	}
}

// spanLevel records what pop restores.
type spanLevel struct {
	saved                                [spanAtomCount]*rope
	startLength, startEmitted, afterOpen int
	holdBefore                           bool
	parentPending                        int
	kind                                 spanKind
	index                                int
	empty                                bool
}

func (w *spanWriter) push(kind spanKind) spanLevel {
	level := spanLevel{saved: w.table, kind: kind, startLength: w.length, startEmitted: w.emitted, holdBefore: w.holdCR, parentPending: w.pending, index: w.levelIndex}
	w.levelIndex++
	if !w.counting {
		level.empty = w.emptyLevels[level.index]
	} else {
		w.emptyLevels = append(w.emptyLevels, false)
	}
	codes := spanCodeTable[kind]
	if !level.empty {
		w.writeLiteral(codes.open)
	}
	level.afterOpen = w.emitted
	w.pending = 0
	open := spanOpenRopes[kind]
	closeRope := w.table[kind]
	w.table[spanNewline] = ropeConcat(ropeConcat(closeRope, w.table[spanNewline]), open)
	w.table[spanCRNewline] = ropeConcat(ropeConcat(closeRope, w.table[spanCRNewline]), open)
	w.table[kind] = ropeConcat(closeRope, open)
	w.depth++
	return level
}

func (w *spanWriter) pop(level spanLevel) {
	w.depth--
	w.table = level.saved
	// The level's trailing copies of P are trimmed before it is decorated.
	w.pending = 0
	if level.empty {
		w.pending = level.parentPending
		return
	}
	if w.counting && w.emitted == level.afterOpen {
		// Nothing followed the open code: the text was empty, so the level is dropped, along with a CR its open code released.
		w.emptyLevels[level.index] = true
		w.length, w.emitted = level.startLength, level.startEmitted
		w.holdCR = level.holdBefore
		w.pending = level.parentPending
		return
	}
	w.writeRope(w.table[level.kind])
}

// stockSpanTheme reports whether every emphasis function is the stock one, whose codes spanCodeTable lists.
func stockSpanTheme(theme *MarkdownTheme) bool {
	return funcIdentity(theme.Bold) == stockSpanFuncs[spanBold] && funcIdentity(theme.Italic) == stockSpanFuncs[spanItalic] && funcIdentity(theme.Strikethrough) == stockSpanFuncs[spanStrike]
}

func funcIdentity(f func(string) string) uintptr {
	if f == nil {
		return 0
	}
	return reflect.ValueOf(f).Pointer()
}

// stockSpanFuncs are the code addresses of the built-in themes' emphasis functions (GetMarkdownTheme, MarkdownThemeFor). They are top-level functions, so the address does not depend on which theme builder ran or where the compiler inlined it.
var stockSpanFuncs = [spanKinds]uintptr{spanBold: funcIdentity(markdownBold), spanItalic: funcIdentity(markdownItalic), spanStrike: funcIdentity(markdownStrikethrough)}

// spanPrefixUsable reports whether prefix can be tracked as pending copies: complete SGR codes that no level rewrites, so that it appears unchanged wherever it is written.
func spanPrefixUsable(prefix string) bool {
	if !onlyCompleteSGR(prefix) {
		return false
	}
	for kind := range spanKinds {
		if strings.Contains(prefix, spanCodeTable[kind].close) {
			return false
		}
	}
	return true
}

// lastSGRCode is the final complete code of a string made of complete codes.
func lastSGRCode(s string) string {
	i := strings.LastIndexByte(s, '\x1b')
	if i < 0 {
		return ""
	}
	return s[i:]
}

// renderInlineTokensStacked renders tokens without a copy per nesting level. It reports false when the context or content needs the per-level path.
func (m *Markdown) renderInlineTokensStacked(tokens []markedToken, style inlineStyleContext) (string, bool) {
	if style.applyText == nil || style.identity && style.stylePrefix != "" {
		return "", false
	}
	if style.stylePrefix != "" && !spanPrefixUsable(style.stylePrefix) {
		return "", false
	}
	theme := m.markdownTheme()
	if !stockSpanTheme(&theme) {
		return "", false
	}
	counted := newSpanWriter(true, style)
	m.writeInlineTokens(counted, tokens, style, &theme)
	if !counted.ok {
		return "", false
	}
	counted.pending = 0
	counted.flushCR()
	w := newSpanWriter(false, style)
	w.emptyLevels, w.chunks = counted.emptyLevels, counted.chunks
	w.sb.Grow(counted.length)
	m.writeInlineTokens(w, tokens, style, &theme)
	w.pending = 0
	w.flushCR()
	return w.sb.String(), true
}

// writeStyledText renders a text token under a text style: each line is styled separately, as the per-level path does.
func (m *Markdown) writeStyledText(w *spanWriter, text string, style inlineStyleContext) {
	if style.identity {
		w.writeText(text, false)
		return
	}
	for i, segment := range strings.Split(text, "\n") {
		if i > 0 {
			w.writeText("\n", false)
		}
		w.writeThemed(func() string { return style.applyText(segment) })
	}
}

// writeThemed writes a string a theme function produced, computing it once across both passes.
func (w *spanWriter) writeThemed(compute func() string) {
	chunk := w.chunk(compute)
	// A chunk ending in P's last code could join the copies of P after it and be trimmed with them.
	if w.prefix != "" && strings.HasSuffix(chunk, lastSGRCode(w.prefix)) {
		w.ok = false
		return
	}
	w.writeText(chunk, true)
}

// endToken appends P after a styled token, as the per-level path does.
func (w *spanWriter) endToken() {
	if w.prefix != "" {
		w.pending++
	}
}

func (m *Markdown) writeInlineTokens(w *spanWriter, tokens []markedToken, style inlineStyleContext, theme *MarkdownTheme) {
	for _, token := range tokens {
		if !w.ok {
			return
		}
		switch token.kind {
		case markedLatex:
			text := token.latex.raw
			if m.latexEnabled() {
				text = renderInlineLatex(*token.latex)
			}
			m.writeStyledText(w, text, style)
		case markedEscape:
			if m.options.PreserveBackslashEscapes {
				m.writeStyledText(w, token.raw, style)
			} else {
				m.writeStyledText(w, token.text, style)
			}
		case markedText, markedImage:
			m.writeStyledText(w, token.text, style)
		case markedHTML:
			m.writeStyledText(w, token.raw, style)
		case markedStrong, markedEm, markedDel:
			var kind spanKind
			switch token.kind {
			case markedStrong:
				kind = spanBold
			case markedEm:
				kind = spanItalic
			default:
				kind = spanStrike
			}
			level := w.push(kind)
			m.writeInlineTokens(w, token.tokens, style, theme)
			w.pop(level)
			w.endToken()
		case markedCodespan:
			w.writeThemed(func() string { return theme.Code(token.text) })
			w.endToken()
		case markedLink:
			w.writeThemed(func() string {
				return m.styleMarkdownLink(m.renderInlineTokens(token.tokens, style), token.text, token.href)
			})
			w.endToken()
		case markedBr:
			w.writeText("\n", false)
		}
	}
}
