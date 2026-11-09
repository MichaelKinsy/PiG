package tools

// Ports packages/coding-agent/src/core/tools/grep.ts

import (
	"bytes"
	"encoding/base64"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// grepEvent is one rg "match" or "context" event that carries a path and a line number. path and lineText point into the line the
// event was parsed from or into the parser's buffers, and hold until the next parse.
type grepEvent struct {
	isMatch    bool
	path       []byte
	lineNumber int
	lineText   []byte
}

// grepEventParser reads rg --json lines without allocating per line: the strings of an event are slices of the line, and those that
// need unescaping or decoding are built in buffers the parser reuses.
type grepEventParser struct {
	pathBuf, textBuf []byte
}

// parse reads a "match" or "context" event that carries a path and a line number; upstream skips every other line, and a line that is
// not one JSON object, as the cut-off last line of a killed rg is not, is skipped (grep.ts: JSON.parse in a try/catch).
func (p *grepEventParser) parse(line []byte) (grepEvent, bool) {
	s := grepScanner{b: line}
	var (
		eventType          grepJSONString
		path, lines        grepRGText
		lineNumber         []byte
		hasLineNumber, ok  bool
		isMatch, isContext bool
	)
	s.skipSpace()
	if !s.object(func(key []byte) bool {
		switch string(key) {
		case "type":
			eventType, ok = s.str()
			return ok
		case "data":
			path, lines, lineNumber, hasLineNumber = grepRGText{}, grepRGText{}, nil, false
			return s.object(func(key []byte) bool {
				switch string(key) {
				case "path":
					path, ok = s.rgText()
					return ok
				case "lines":
					lines, ok = s.rgText()
					return ok
				case "line_number":
					lineNumber, hasLineNumber, ok = s.number()
					return ok
				}
				return s.skipValue()
			})
		}
		return s.skipValue()
	}) {
		return grepEvent{}, false
	}
	s.skipSpace()
	if s.i != len(s.b) {
		return grepEvent{}, false
	}
	typeText := p.unquote(&p.textBuf, eventType)
	isMatch, isContext = string(typeText) == "match", string(typeText) == "context"
	if !isMatch && !isContext {
		return grepEvent{}, false
	}
	filePath := p.text(&p.pathBuf, path)
	if len(filePath) == 0 || !hasLineNumber {
		return grepEvent{}, false
	}
	number, ok := parseLineNumber(lineNumber)
	if !ok {
		return grepEvent{}, false
	}
	// The textBuf of the type is free again: the line text is built after it.
	return grepEvent{isMatch: isMatch, path: filePath, lineNumber: number, lineText: p.text(&p.textBuf, lines)}, true
}

// parseLineNumber is int(JSON.parse(text)) of a JSON number: rg writes a plain integer, which takes no float parse.
func parseLineNumber(text []byte) (int, bool) {
	if len(text) <= 15 {
		n := 0
		for _, c := range text {
			if c < '0' || c > '9' {
				n = -1
				break
			}
			n = n*10 + int(c-'0')
		}
		if n >= 0 {
			return n, true
		}
	}
	number, err := strconv.ParseFloat(string(text), 64)
	if err != nil {
		return 0, false
	}
	//portlint:allow numbers ripgrep reports a line number as a small positive integer, so the conversion cannot leave the int range
	return int(number), true
}

// grepJSONString is a JSON string as it is written: the bytes between the quotes, and whether they hold an escape.
type grepJSONString struct {
	raw     []byte
	escaped bool
	present bool
}

// grepRGText is a ripgrep JSON text field, {"text": ...} for valid UTF-8 and {"bytes": base64} for anything else.
type grepRGText struct {
	text, bytes grepJSONString
}

// unquote returns the text of s: its bytes when it holds no escape, else the unescaped text built in buf.
func (p *grepEventParser) unquote(buf *[]byte, s grepJSONString) []byte {
	if !s.escaped {
		return s.raw
	}
	*buf = unescapeJSONString((*buf)[:0], s.raw)
	return *buf
}

// text is upstream decodeRgText: the text, or the decoded bytes (invalid UTF-8 becomes U+FFFD, as Buffer.toString does).
func (p *grepEventParser) text(buf *[]byte, t grepRGText) []byte {
	if t.text.present {
		return p.unquote(buf, t.text)
	}
	if t.bytes.present {
		encoded := p.unquote(buf, t.bytes)
		if data, err := base64.StdEncoding.DecodeString(string(encoded)); err == nil {
			return []byte(decodeText(data))
		}
	}
	return nil
}

// unescapeJSONString appends the text of the JSON string whose bytes between the quotes are raw to dst. A surrogate half that does not
// pair becomes U+FFFD, as encoding/json decodes it.
func unescapeJSONString(dst, raw []byte) []byte {
	for i := 0; i < len(raw); {
		c := raw[i]
		if c != '\\' {
			next := bytes.IndexByte(raw[i:], '\\')
			if next == -1 {
				return append(dst, raw[i:]...)
			}
			dst = append(dst, raw[i:i+next]...)
			i += next
			continue
		}
		// The scanner accepted the string, so an escape is complete.
		switch e := raw[i+1]; e {
		case 'b':
			dst = append(dst, '\b')
		case 'f':
			dst = append(dst, '\f')
		case 'n':
			dst = append(dst, '\n')
		case 'r':
			dst = append(dst, '\r')
		case 't':
			dst = append(dst, '\t')
		case 'u':
			r := rune(hex4(raw[i+2:]))
			i += 6
			if utf16.IsSurrogate(r) {
				if r < 0xDC00 && i+6 <= len(raw) && raw[i] == '\\' && raw[i+1] == 'u' {
					if low := rune(hex4(raw[i+2:])); utf16.IsSurrogate(low) && low >= 0xDC00 {
						dst = utf8.AppendRune(dst, utf16.DecodeRune(r, low))
						i += 6
						continue
					}
				}
				r = utf8.RuneError
			}
			dst = utf8.AppendRune(dst, r)
			continue
		default:
			dst = append(dst, e)
		}
		i += 2
	}
	return dst
}

// hex4 is the value of the four hex digits that start b, which the scanner has checked.
func hex4(b []byte) uint32 {
	var v uint32
	for _, c := range b[:4] {
		switch {
		case c >= 'a':
			c = c - 'a' + 10
		case c >= 'A':
			c = c - 'A' + 10
		default:
			c -= '0'
		}
		v = v<<4 | uint32(c)
	}
	return v
}

// grepScanner walks one JSON text, checking its syntax as it goes: a value it skips is as well formed as encoding/json requires.
type grepScanner struct {
	b []byte
	i int
}

func (s *grepScanner) skipSpace() {
	for s.i < len(s.b) {
		switch s.b[s.i] {
		case ' ', '\t', '\n', '\r':
			s.i++
		default:
			return
		}
	}
}

// object reads an object, calling member after each name with the scanner at the member's value; member reads that value.
func (s *grepScanner) object(member func(key []byte) bool) bool {
	if s.i >= len(s.b) || s.b[s.i] != '{' {
		return false
	}
	s.i++
	s.skipSpace()
	if s.i < len(s.b) && s.b[s.i] == '}' {
		s.i++
		return true
	}
	for {
		s.skipSpace()
		name, ok := s.quoted()
		if !ok {
			return false
		}
		key := name.raw
		if name.escaped {
			// Rare: rg writes plain names, but an escaped one still names the same member.
			key = unescapeJSONString(nil, key)
		}
		s.skipSpace()
		if s.i >= len(s.b) || s.b[s.i] != ':' {
			return false
		}
		s.i++
		s.skipSpace()
		if !member(key) {
			return false
		}
		s.skipSpace()
		if s.i >= len(s.b) {
			return false
		}
		switch s.b[s.i] {
		case ',':
			s.i++
		case '}':
			s.i++
			return true
		default:
			return false
		}
	}
}

// str reads a string, which is the zero value when the value is null: an absent field, as a null decodes into a nil pointer.
func (s *grepScanner) str() (grepJSONString, bool) {
	if s.i < len(s.b) && s.b[s.i] == 'n' {
		return grepJSONString{}, s.literal("null")
	}
	return s.quoted()
}

// quoted reads a string.
func (s *grepScanner) quoted() (grepJSONString, bool) {
	if s.i >= len(s.b) || s.b[s.i] != '"' {
		return grepJSONString{}, false
	}
	s.i++
	start, escaped := s.i, false
	for s.i < len(s.b) {
		switch c := s.b[s.i]; {
		case c == '"':
			raw := s.b[start:s.i]
			s.i++
			return grepJSONString{raw: raw, escaped: escaped, present: true}, true
		case c < 0x20:
			return grepJSONString{}, false
		case c == '\\':
			escaped = true
			if s.i+1 >= len(s.b) {
				return grepJSONString{}, false
			}
			switch s.b[s.i+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				s.i += 2
			case 'u':
				if s.i+6 > len(s.b) || !isHex4(s.b[s.i+2:s.i+6]) {
					return grepJSONString{}, false
				}
				s.i += 6
			default:
				return grepJSONString{}, false
			}
		default:
			s.i++
		}
	}
	return grepJSONString{}, false
}

func isHex4(b []byte) bool {
	for _, c := range b[:4] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// rgText reads a ripgrep text field: an object whose "text" and "bytes" strings are the field's, or null.
func (s *grepScanner) rgText() (grepRGText, bool) {
	var t grepRGText
	if s.i < len(s.b) && s.b[s.i] == 'n' {
		return t, s.literal("null")
	}
	ok := s.object(func(key []byte) bool {
		var field bool
		switch string(key) {
		case "text":
			t.text, field = s.str()
			return field
		case "bytes":
			t.bytes, field = s.str()
			return field
		}
		return s.skipValue()
	})
	return t, ok
}

// number reads a number, or null, which is an absent field.
func (s *grepScanner) number() (text []byte, present, ok bool) {
	if s.i < len(s.b) && s.b[s.i] == 'n' {
		return nil, false, s.literal("null")
	}
	start := s.i
	if !s.skipNumber() {
		return nil, false, false
	}
	return s.b[start:s.i], true, true
}

func (s *grepScanner) literal(word string) bool {
	if len(s.b)-s.i < len(word) || string(s.b[s.i:s.i+len(word)]) != word {
		return false
	}
	s.i += len(word)
	return true
}

func (s *grepScanner) skipNumber() bool {
	digits := func() bool {
		start := s.i
		for s.i < len(s.b) && s.b[s.i] >= '0' && s.b[s.i] <= '9' {
			s.i++
		}
		return s.i > start
	}
	if s.i < len(s.b) && s.b[s.i] == '-' {
		s.i++
	}
	switch {
	case s.i < len(s.b) && s.b[s.i] == '0':
		s.i++
	case !digits():
		return false
	}
	if s.i < len(s.b) && s.b[s.i] == '.' {
		s.i++
		if !digits() {
			return false
		}
	}
	if s.i < len(s.b) && (s.b[s.i] == 'e' || s.b[s.i] == 'E') {
		s.i++
		if s.i < len(s.b) && (s.b[s.i] == '+' || s.b[s.i] == '-') {
			s.i++
		}
		if !digits() {
			return false
		}
	}
	return true
}

// skipValue skips one value, checking its syntax. It keeps the open containers on a stack, not in recursion, so a deeply nested value
// costs memory in proportion to its text and cannot exhaust the call stack.
func (s *grepScanner) skipValue() bool {
	var small [16]byte
	open := small[:0]
	for {
		s.skipSpace()
		if s.i >= len(s.b) {
			return false
		}
		switch s.b[s.i] {
		case '{':
			s.i++
			s.skipSpace()
			if s.i < len(s.b) && s.b[s.i] == '}' {
				s.i++
				break
			}
			open = append(open, '{')
			if !s.name() {
				return false
			}
			continue
		case '[':
			s.i++
			s.skipSpace()
			if s.i < len(s.b) && s.b[s.i] == ']' {
				s.i++
				break
			}
			open = append(open, '[')
			continue
		case '"':
			if _, ok := s.quoted(); !ok {
				return false
			}
		case 't':
			if !s.literal("true") {
				return false
			}
		case 'f':
			if !s.literal("false") {
				return false
			}
		case 'n':
			if !s.literal("null") {
				return false
			}
		default:
			if !s.skipNumber() {
				return false
			}
		}
		// A value ended: close the containers it ends, or go on to the next member or element.
		more := false
		for !more {
			if len(open) == 0 {
				return true
			}
			s.skipSpace()
			if s.i >= len(s.b) {
				return false
			}
			top := open[len(open)-1]
			switch c := s.b[s.i]; {
			case c == ',':
				s.i++
				s.skipSpace()
				if top == '{' && !s.name() {
					return false
				}
				more = true
			case c == '}' && top == '{', c == ']' && top == '[':
				s.i++
				open = open[:len(open)-1]
			default:
				return false
			}
		}
	}
}

// name reads an object member's name and the colon after it.
func (s *grepScanner) name() bool {
	if _, ok := s.quoted(); !ok {
		return false
	}
	s.skipSpace()
	if s.i >= len(s.b) || s.b[s.i] != ':' {
		return false
	}
	s.i++
	return true
}
