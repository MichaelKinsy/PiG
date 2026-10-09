// Package yaml12 ports the parts of eemeli/yaml 2.9.0 that Pi's frontmatter parser runs (lexer, parser, composer, core schema, error text), so frontmatter reads and fails as it does in Pi.
//
// Ports yaml 2.9.0 src/parse/lexer.ts. Copyright Eemeli Aro, ISC licence: permission to use, copy, modify, and/or distribute this software for any purpose with or without fee is hereby granted, provided that the above copyright notice and this permission notice appear in all copies.
//
// Positions are byte offsets into the source; LineCounter reports UTF-16 columns, as the JavaScript library does. The lexer reads a complete source, so the incremental-input states of the original are absent.
package yaml12

import "strings"

// Token sentinels of the CST token stream (cst.ts).
const (
	tokDocument = "\x02"
	tokFlowEnd  = "\x18"
	tokScalar   = "\x1f"
	bom         = "\ufeff"
)

const eof = -1

func isEmpty(ch int) bool {
	switch ch {
	case eof, ' ', '\n', '\r', '\t':
		return true
	}
	return false
}

const tagChars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-#;/?:@&=+$_.!~*'()"

func isHex(ch int) bool {
	return ch >= '0' && ch <= '9' || ch >= 'A' && ch <= 'F' || ch >= 'a' && ch <= 'f'
}

func isFlowIndicator(ch int) bool {
	return ch == ',' || ch == '[' || ch == ']' || ch == '{' || ch == '}'
}

func isNotAnchorChar(ch int) bool {
	return ch == eof || ch == ' ' || ch == ',' || ch == '[' || ch == ']' || ch == '{' || ch == '}' || ch == '\n' || ch == '\r' || ch == '\t'
}

type lexer struct {
	buffer            string
	blockScalarIndent int
	blockScalarKeep   bool
	flowKey           bool
	flowLevel         int
	indentNext        int
	indentValue       int
	pos               int
	out               []string
}

// lex returns the CST token stream of source.
func lex(source string) []string {
	l := &lexer{buffer: source, blockScalarIndent: -1}
	next := "stream"
	for next != "" && l.hasChars(1) {
		next = l.parseNext(next)
	}
	return l.out
}

func (l *lexer) at(i int) int {
	if i < 0 || i >= len(l.buffer) {
		return eof
	}
	return int(l.buffer[i])
}

func (l *lexer) charAt(n int) int { return l.at(l.pos + n) }

func (l *lexer) hasChars(n int) bool { return l.pos+n <= len(l.buffer) }

func (l *lexer) emit(s string) { l.out = append(l.out, s) }

func (l *lexer) atLineEnd() bool {
	i := l.pos
	ch := l.at(i)
	for ch == ' ' || ch == '\t' {
		i++
		ch = l.at(i)
	}
	if ch == eof || ch == '#' || ch == '\n' {
		return true
	}
	if ch == '\r' {
		return l.at(i+1) == '\n'
	}
	return false
}

func (l *lexer) continueScalar(offset int) int {
	ch := l.at(offset)
	if l.indentNext > 0 {
		indent := 0
		for ch == ' ' {
			indent++
			ch = l.at(indent + offset)
		}
		if ch == '\r' {
			next := l.at(indent + offset + 1)
			if next == '\n' {
				return offset + indent + 1
			}
		}
		if ch == '\n' || indent >= l.indentNext {
			return offset + indent
		}
		return -1
	}
	if ch == '-' || ch == '.' {
		dt := l.substr(offset, 3)
		if (dt == "---" || dt == "...") && isEmpty(l.at(offset+3)) {
			return -1
		}
	}
	return offset
}

func (l *lexer) substr(start, n int) string {
	if start >= len(l.buffer) {
		return ""
	}
	end := min(start+n, len(l.buffer))
	return l.buffer[start:end]
}

// getLine is the rest of the current line without its line break.
func (l *lexer) getLine() string {
	end := strings.IndexByte(l.buffer[l.pos:], '\n')
	if end == -1 {
		return l.buffer[l.pos:]
	}
	end += l.pos
	if end > 0 && l.buffer[end-1] == '\r' {
		end--
	}
	return l.buffer[l.pos:end]
}

func (l *lexer) parseNext(next string) string {
	switch next {
	case "stream":
		return l.parseStream()
	case "line-start":
		return l.parseLineStart()
	case "block-start":
		return l.parseBlockStart()
	case "doc":
		return l.parseDocument()
	case "flow":
		return l.parseFlowCollection()
	case "quoted-scalar":
		return l.parseQuotedScalar()
	case "block-scalar":
		return l.parseBlockScalar()
	case "plain-scalar":
		return l.parsePlainScalar()
	}
	return ""
}

func (l *lexer) parseStream() string {
	line := l.getLine()
	if strings.HasPrefix(line, bom) {
		l.pushBytes(len(bom))
		line = line[len(bom):]
	}
	if len(line) > 0 && line[0] == '%' {
		dirEnd := len(line)
		cs := strings.IndexByte(line, '#')
		for cs != -1 {
			ch := line[cs-1]
			if ch == ' ' || ch == '\t' {
				dirEnd = cs - 1
				break
			}
			rest := strings.IndexByte(line[cs+1:], '#')
			if rest == -1 {
				cs = -1
			} else {
				cs += 1 + rest
			}
		}
		for dirEnd > 0 && (line[dirEnd-1] == ' ' || line[dirEnd-1] == '\t') {
			dirEnd--
		}
		n := l.pushCount(dirEnd) + l.pushSpaces(true)
		l.pushCount(len(line) - n)
		l.pushNewline()
		return "stream"
	}
	if l.atLineEnd() {
		sp := l.pushSpaces(true)
		l.pushCount(len(line) - sp)
		l.pushNewline()
		return "stream"
	}
	l.emit(tokDocument)
	return l.parseLineStart()
}

func (l *lexer) parseLineStart() string {
	ch := l.charAt(0)
	if ch == '-' || ch == '.' {
		s := l.substr(l.pos, 3)
		if (s == "---" || s == "...") && isEmpty(l.charAt(3)) {
			l.pushCount(3)
			l.indentValue = 0
			l.indentNext = 0
			if s == "---" {
				return "doc"
			}
			return "stream"
		}
	}
	l.indentValue = l.pushSpaces(false)
	if l.indentNext > l.indentValue && !isEmpty(l.charAt(1)) {
		l.indentNext = l.indentValue
	}
	return l.parseBlockStart()
}

func (l *lexer) parseBlockStart() string {
	ch0, ch1 := l.charAt(0), l.charAt(1)
	if (ch0 == '-' || ch0 == '?' || ch0 == ':') && isEmpty(ch1) {
		n := l.pushCount(1) + l.pushSpaces(true)
		l.indentNext = l.indentValue + 1
		l.indentValue += n
		return "block-start"
	}
	return "doc"
}

func (l *lexer) parseDocument() string {
	l.pushSpaces(true)
	line := l.getLine()
	n := l.pushIndicators()
	ch := eof
	if n < len(line) {
		ch = int(line[n])
	}
	switch ch {
	case '#', eof:
		if ch == '#' {
			l.pushCount(len(line) - n)
		}
		l.pushNewline()
		return l.parseLineStart()
	case '{', '[':
		l.pushCount(1)
		l.flowKey = false
		l.flowLevel = 1
		return "flow"
	case '}', ']':
		l.pushCount(1)
		return "doc"
	case '*':
		l.pushUntil(isNotAnchorChar)
		return "doc"
	case '"', '\'':
		return l.parseQuotedScalar()
	case '|', '>':
		n += l.parseBlockScalarHeader()
		n += l.pushSpaces(true)
		l.pushCount(len(line) - n)
		l.pushNewline()
		return l.parseBlockScalar()
	}
	return l.parsePlainScalar()
}

func (l *lexer) parseFlowCollection() string {
	var nl, sp int
	indent := -1
	for {
		nl = l.pushNewline()
		if nl > 0 {
			sp = l.pushSpaces(false)
			indent = sp
			l.indentValue = sp
		} else {
			sp = 0
		}
		sp += l.pushSpaces(true)
		if nl+sp <= 0 {
			break
		}
	}
	line := l.getLine()
	lineAt := func(i int) int {
		if i < len(line) {
			return int(line[i])
		}
		return eof
	}
	if (indent != -1 && indent < l.indentNext && lineAt(0) != '#') ||
		(indent == 0 && (strings.HasPrefix(line, "---") || strings.HasPrefix(line, "...")) && isEmpty(lineAt(3))) {
		atFlowEndMarker := indent == l.indentNext-1 && l.flowLevel == 1 && (lineAt(0) == ']' || lineAt(0) == '}')
		if !atFlowEndMarker {
			l.flowLevel = 0
			l.emit(tokFlowEnd)
			return l.parseLineStart()
		}
	}
	n := 0
	for lineAt(n) == ',' {
		n += l.pushCount(1)
		n += l.pushSpaces(true)
		l.flowKey = false
	}
	n += l.pushIndicators()
	switch lineAt(n) {
	case eof:
		return "flow"
	case '#':
		l.pushCount(len(line) - n)
		return "flow"
	case '{', '[':
		l.pushCount(1)
		l.flowKey = false
		l.flowLevel++
		return "flow"
	case '}', ']':
		l.pushCount(1)
		l.flowKey = true
		l.flowLevel--
		if l.flowLevel > 0 {
			return "flow"
		}
		return "doc"
	case '*':
		l.pushUntil(isNotAnchorChar)
		return "flow"
	case '"', '\'':
		l.flowKey = true
		return l.parseQuotedScalar()
	case ':':
		next := l.charAt(1)
		if l.flowKey || isEmpty(next) || next == ',' {
			l.flowKey = false
			l.pushCount(1)
			l.pushSpaces(true)
			return "flow"
		}
	}
	l.flowKey = false
	return l.parsePlainScalar()
}

func (l *lexer) indexByteFrom(c byte, from int) int {
	if from > len(l.buffer) {
		return -1
	}
	i := strings.IndexByte(l.buffer[from:], c)
	if i == -1 {
		return -1
	}
	return i + from
}

func (l *lexer) parseQuotedScalar() string {
	quote := byte(l.charAt(0))
	end := l.indexByteFrom(quote, l.pos+1)
	if quote == '\'' {
		for end != -1 && l.at(end+1) == '\'' {
			end = l.indexByteFrom('\'', end+2)
		}
	} else {
		for end != -1 {
			n := 0
			for l.at(end-1-n) == '\\' {
				n++
			}
			if n%2 == 0 {
				break
			}
			end = l.indexByteFrom('"', end+1)
		}
	}
	qb := l.buffer
	if end != -1 {
		qb = l.buffer[:end]
	}
	nl := indexByteIn(qb, '\n', l.pos)
	if nl != -1 {
		for nl != -1 {
			cs := l.continueScalar(nl + 1)
			if cs == -1 {
				break
			}
			nl = indexByteIn(qb, '\n', cs)
		}
		if nl != -1 {
			if nl > 0 && qb[nl-1] == '\r' {
				end = nl - 2
			} else {
				end = nl - 1
			}
		}
	}
	if end == -1 {
		end = len(l.buffer)
	}
	l.pushToIndex(end+1, false)
	if l.flowLevel > 0 {
		return "flow"
	}
	return "doc"
}

func indexByteIn(s string, c byte, from int) int {
	if from < 0 {
		from = 0
	}
	if from > len(s) {
		return -1
	}
	i := strings.IndexByte(s[from:], c)
	if i == -1 {
		return -1
	}
	return i + from
}

func (l *lexer) parseBlockScalarHeader() int {
	l.blockScalarIndent = -1
	l.blockScalarKeep = false
	i := l.pos
	for {
		i++
		ch := l.at(i)
		switch {
		case ch == '+':
			l.blockScalarKeep = true
		case ch > '0' && ch <= '9':
			l.blockScalarIndent = ch - '0' - 1
		case ch != '-':
			return l.pushUntil(func(ch int) bool { return isEmpty(ch) || ch == '#' })
		}
	}
}

func (l *lexer) parseBlockScalar() string {
	nl := l.pos - 1
	indent := 0
loop:
	for i := l.pos; ; i++ {
		ch := l.at(i)
		if ch == eof {
			break
		}
		switch ch {
		case ' ':
			indent++
		case '\n':
			nl = i
			indent = 0
		case '\r':
			if l.at(i+1) == '\n' {
				break
			}
			break loop
		default:
			break loop
		}
	}
	if indent >= l.indentNext {
		if l.blockScalarIndent == -1 {
			l.indentNext = indent
		} else {
			base := l.indentNext
			if base == 0 {
				base = 1
			}
			l.indentNext = l.blockScalarIndent + base
		}
		for {
			cs := l.continueScalar(nl + 1)
			if cs == -1 {
				break
			}
			nl = l.indexByteFrom('\n', cs)
			if nl == -1 {
				break
			}
		}
		if nl == -1 {
			nl = len(l.buffer)
		}
	}
	i := nl + 1
	ch := l.at(i)
	for ch == ' ' {
		i++
		ch = l.at(i)
	}
	if ch == '\t' {
		for ch == '\t' || ch == ' ' || ch == '\r' || ch == '\n' {
			i++
			ch = l.at(i)
		}
		nl = i - 1
	} else if !l.blockScalarKeep {
		for {
			i := nl - 1
			ch := l.at(i)
			if ch == '\r' {
				i--
				ch = l.at(i)
			}
			lastChar := i
			for ch == ' ' {
				i--
				ch = l.at(i)
			}
			if ch == '\n' && i >= l.pos && i+1+indent > lastChar {
				nl = i
			} else {
				break
			}
		}
	}
	l.emit(tokScalar)
	l.pushToIndex(nl+1, true)
	return l.parseLineStart()
}

func (l *lexer) parsePlainScalar() string {
	inFlow := l.flowLevel > 0
	end := l.pos - 1
	i := l.pos - 1
loop:
	for {
		i++
		ch := l.at(i)
		if ch == eof {
			break
		}
		switch {
		case ch == ':':
			next := l.at(i + 1)
			if isEmpty(next) || (inFlow && isFlowIndicator(next)) {
				break loop
			}
			end = i
		case isEmpty(ch):
			next := l.at(i + 1)
			if ch == '\r' {
				if next == '\n' {
					i++
					ch = '\n'
					next = l.at(i + 1)
				} else {
					end = i
				}
			}
			if next == '#' || (inFlow && isFlowIndicator(next)) {
				break loop
			}
			if ch == '\n' {
				cs := l.continueScalar(i + 1)
				if cs == -1 {
					break loop
				}
				i = max(i, cs-2)
			}
		default:
			if inFlow && isFlowIndicator(ch) {
				break loop
			}
			end = i
		}
	}
	l.emit(tokScalar)
	l.pushToIndex(end+1, true)
	if inFlow {
		return "flow"
	}
	return "doc"
}

// pushBytes emits n source bytes.
func (l *lexer) pushBytes(n int) int {
	l.emit(l.buffer[l.pos : l.pos+n])
	l.pos += n
	return n
}

func (l *lexer) pushCount(n int) int {
	if n > 0 {
		n = min(n, len(l.buffer)-l.pos)
		l.emit(l.buffer[l.pos : l.pos+n])
		l.pos += n
		return n
	}
	return 0
}

func (l *lexer) pushToIndex(i int, allowEmpty bool) int {
	i = min(i, len(l.buffer))
	if i > l.pos {
		s := l.buffer[l.pos:i]
		l.emit(s)
		l.pos += len(s)
		return len(s)
	}
	if allowEmpty {
		l.emit("")
	}
	return 0
}

func (l *lexer) pushIndicators() int {
	n := 0
	for {
		switch l.charAt(0) {
		case '!':
			n += l.pushTag()
			n += l.pushSpaces(true)
			continue
		case '&':
			n += l.pushUntil(isNotAnchorChar)
			n += l.pushSpaces(true)
			continue
		case '-', '?', ':':
			inFlow := l.flowLevel > 0
			ch1 := l.charAt(1)
			if isEmpty(ch1) || (inFlow && isFlowIndicator(ch1)) {
				if !inFlow {
					l.indentNext = l.indentValue + 1
				} else if l.flowKey {
					l.flowKey = false
				}
				n += l.pushCount(1)
				n += l.pushSpaces(true)
				continue
			}
		}
		break
	}
	return n
}

func (l *lexer) pushTag() int {
	if l.charAt(1) == '<' {
		i := l.pos + 2
		ch := l.at(i)
		for !isEmpty(ch) && ch != '>' {
			i++
			ch = l.at(i)
		}
		if ch == '>' {
			return l.pushToIndex(i+1, false)
		}
		return l.pushToIndex(i, false)
	}
	i := l.pos + 1
	ch := l.at(i)
	for ch != eof {
		switch {
		case strings.IndexByte(tagChars, byte(ch)) >= 0:
			i++
		case ch == '%' && isHex(l.at(i+1)) && isHex(l.at(i+2)):
			i += 3
		default:
			return l.pushToIndex(i, false)
		}
		ch = l.at(i)
	}
	return l.pushToIndex(i, false)
}

func (l *lexer) pushNewline() int {
	ch := l.at(l.pos)
	if ch == '\n' {
		return l.pushCount(1)
	}
	if ch == '\r' && l.charAt(1) == '\n' {
		return l.pushCount(2)
	}
	return 0
}

func (l *lexer) pushSpaces(allowTabs bool) int {
	i := l.pos
	for l.at(i) == ' ' || (allowTabs && l.at(i) == '\t') {
		i++
	}
	n := i - l.pos
	if n > 0 {
		l.emit(l.buffer[l.pos:i])
		l.pos = i
	}
	return n
}

func (l *lexer) pushUntil(test func(int) bool) int {
	i := l.pos
	ch := l.at(i)
	for !test(ch) {
		i++
		ch = l.at(i)
	}
	return l.pushToIndex(i, false)
}
