// SPDX-License-Identifier: MIT

package history

import (
	"slices"
	"strconv"
)

// jsonError is the error a scanner or encoder returns. It is comparable so callers can test it with ==.
type jsonError string

func (e jsonError) Error() string { return "history: " + string(e) }

// Errors the JSON scanner and encoder return.
const (
	// ErrSyntax reports text that JSON.parse would reject.
	ErrSyntax = jsonError("invalid JSON")
	// ErrNonFinite reports a number outside the finite float64 range, which Pi's copyJson rejects.
	ErrNonFinite = jsonError("value contains a non-finite number and is not strict JSON")
	// ErrShape reports a value of the wrong JSON type where a record field has a fixed type.
	ErrShape = jsonError("unexpected JSON shape")
)

// scanner is a structural JSON tokenizer over one byte slice. It validates what it skips, so a value it accepts is a
// value JSON.parse accepts, and it never builds a decoded value.
type scanner struct {
	b     []byte
	i     int
	stack []byte
	// buf backs stack for the common shallow value, so scanning it allocates nothing. A scanner must not be copied
	// after its first skip.
	buf [32]byte
}

func newScanner(b []byte) scanner { return scanner{b: b} }

func (s *scanner) ws() {
	for s.i < len(s.b) {
		switch s.b[s.i] {
		case ' ', '\t', '\n', '\r':
			s.i++
		default:
			return
		}
	}
}

// peek returns the next non-space byte without consuming it, or 0 at the end.
func (s *scanner) peek() byte {
	s.ws()
	if s.i >= len(s.b) {
		return 0
	}
	return s.b[s.i]
}

// atEnd reports whether only whitespace remains.
func (s *scanner) atEnd() bool {
	s.ws()
	return s.i >= len(s.b)
}

// rawString scans a string token and returns its bytes between the quotes, escapes still in place.
func (s *scanner) rawString() ([]byte, error) {
	if s.peek() != '"' {
		return nil, ErrSyntax
	}
	s.i++
	start := s.i
	for s.i < len(s.b) {
		c := s.b[s.i]
		switch {
		case c == '"':
			raw := s.b[start:s.i]
			s.i++
			return raw, nil
		case c == '\\':
			s.i++
			if s.i >= len(s.b) {
				return nil, ErrSyntax
			}
			switch s.b[s.i] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				s.i++
			case 'u':
				if s.i+4 >= len(s.b) {
					return nil, ErrSyntax
				}
				for k := 1; k <= 4; k++ {
					if hexVal(s.b[s.i+k]) < 0 {
						return nil, ErrSyntax
					}
				}
				s.i += 5
			default:
				return nil, ErrSyntax
			}
		case c < 0x20:
			return nil, ErrSyntax
		default:
			s.i++
		}
	}
	return nil, ErrSyntax
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// number scans a number token and returns its text.
func (s *scanner) number() ([]byte, error) {
	s.ws()
	start := s.i
	b := s.b
	i := s.i
	if i < len(b) && b[i] == '-' {
		i++
	}
	if i >= len(b) {
		return nil, ErrSyntax
	}
	switch {
	case b[i] == '0':
		i++
	case b[i] >= '1' && b[i] <= '9':
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
		}
	default:
		return nil, ErrSyntax
	}
	if i < len(b) && b[i] == '.' {
		i++
		d := i
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
		}
		if i == d {
			return nil, ErrSyntax
		}
	}
	if i < len(b) && (b[i] == 'e' || b[i] == 'E') {
		i++
		if i < len(b) && (b[i] == '+' || b[i] == '-') {
			i++
		}
		d := i
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
		}
		if i == d {
			return nil, ErrSyntax
		}
	}
	s.i = i
	return b[start:i], nil
}

func (s *scanner) literal(lit string) error {
	if s.i+len(lit) > len(s.b) || string(s.b[s.i:s.i+len(lit)]) != lit {
		return ErrSyntax
	}
	s.i += len(lit)
	return nil
}

// maxDepth bounds container nesting. JSON.parse has no limit of its own; a bound keeps scanner memory flat. Pi's
// stored records nest a dozen levels at most.
const maxDepth = 1 << 12

// skip scans one complete value, validating it, and returns its start and end offsets.
func (s *scanner) skip() (int, int, error) {
	s.ws()
	start := s.i
	if s.stack == nil {
		s.stack = s.buf[:0]
	}
	s.stack = s.stack[:0]
	for {
		// A value starts here.
		s.ws()
		if s.i >= len(s.b) {
			return 0, 0, ErrSyntax
		}
		switch c := s.b[s.i]; {
		case c == '{':
			s.i++
			if s.peek() == '}' {
				s.i++
				break
			}
			if len(s.stack) >= maxDepth {
				return 0, 0, ErrSyntax
			}
			s.stack = append(s.stack, '{')
			if err := s.keyColon(); err != nil {
				return 0, 0, err
			}
			continue
		case c == '[':
			s.i++
			if s.peek() == ']' {
				s.i++
				break
			}
			if len(s.stack) >= maxDepth {
				return 0, 0, ErrSyntax
			}
			s.stack = append(s.stack, '[')
			continue
		case c == '"':
			if _, err := s.rawString(); err != nil {
				return 0, 0, err
			}
		case c == '-' || (c >= '0' && c <= '9'):
			if _, err := s.number(); err != nil {
				return 0, 0, err
			}
		case c == 't':
			if err := s.literal("true"); err != nil {
				return 0, 0, err
			}
		case c == 'f':
			if err := s.literal("false"); err != nil {
				return 0, 0, err
			}
		case c == 'n':
			if err := s.literal("null"); err != nil {
				return 0, 0, err
			}
		default:
			return 0, 0, ErrSyntax
		}
		// A value just ended: close containers until one continues.
		for {
			if len(s.stack) == 0 {
				return start, s.i, nil
			}
			c := s.peek()
			top := s.stack[len(s.stack)-1]
			if c == ',' {
				s.i++
				if top == '{' {
					if err := s.keyColon(); err != nil {
						return 0, 0, err
					}
				}
				break
			}
			if (top == '{' && c == '}') || (top == '[' && c == ']') {
				s.i++
				s.stack = s.stack[:len(s.stack)-1]
				continue
			}
			return 0, 0, ErrSyntax
		}
	}
}

func (s *scanner) keyColon() error {
	if _, err := s.rawString(); err != nil {
		return err
	}
	if s.peek() != ':' {
		return ErrSyntax
	}
	s.i++
	return nil
}

// objIter walks the fields of one object. Call next until it returns false, then read err.
type objIter struct {
	s     *scanner
	first bool
	done  bool
	err   error
	// key is the current key's raw bytes (escapes in place); vs and ve bound the current value.
	key    []byte
	vs, ve int
}

// object begins iterating the object at the scanner's position.
func (s *scanner) object() (objIter, error) {
	if s.peek() != '{' {
		return objIter{}, ErrShape
	}
	s.i++
	return objIter{s: s, first: true}, nil
}

func (it *objIter) next() bool {
	if it.done {
		return false
	}
	s := it.s
	c := s.peek()
	if it.first && c == '}' {
		s.i++
		it.done = true
		return false
	}
	if !it.first {
		switch c {
		case '}':
			s.i++
			it.done = true
			return false
		case ',':
			s.i++
		default:
			it.fail()
			return false
		}
	}
	it.first = false
	key, err := s.rawString()
	if err != nil {
		it.err = err
		it.done = true
		return false
	}
	if s.peek() != ':' {
		it.fail()
		return false
	}
	s.i++
	vs, ve, err := s.skip()
	if err != nil {
		it.err = err
		it.done = true
		return false
	}
	it.key, it.vs, it.ve = key, vs, ve
	return true
}

func (it *objIter) fail() {
	it.err = ErrSyntax
	it.done = true
}

// keyIs reports whether the current key decodes to lit. lit is plain ASCII without escapes.
func (it *objIter) keyIs(lit string) bool { return keyIs(it.key, lit) }

func (it *objIter) value() []byte { return it.s.b[it.vs:it.ve] }

func keyIs(raw []byte, lit string) bool {
	if !hasEscape(raw) {
		return string(raw) == lit
	}
	var buf [64]byte
	return string(unescape(buf[:0], raw)) == lit
}

func hasEscape(raw []byte) bool {
	return slices.Contains(raw, '\\')
}

// arrIter walks the elements of one array.
type arrIter struct {
	s      *scanner
	first  bool
	done   bool
	err    error
	vs, ve int
}

// array begins iterating the array at the scanner's position.
func (s *scanner) array() (arrIter, error) {
	if s.peek() != '[' {
		return arrIter{}, ErrShape
	}
	s.i++
	return arrIter{s: s, first: true}, nil
}

func (it *arrIter) next() bool {
	if it.done {
		return false
	}
	s := it.s
	c := s.peek()
	if it.first && c == ']' {
		s.i++
		it.done = true
		return false
	}
	if !it.first {
		switch c {
		case ']':
			s.i++
			it.done = true
			return false
		case ',':
			s.i++
		default:
			it.err = ErrSyntax
			it.done = true
			return false
		}
	}
	it.first = false
	vs, ve, err := s.skip()
	if err != nil {
		it.err = err
		it.done = true
		return false
	}
	it.vs, it.ve = vs, ve
	return true
}

// valueKind classifies a value by its first byte: '{', '[', '"', 'n' (number), 't', 'f' or 'l' (null).
func valueKind(v []byte) byte {
	if len(v) == 0 {
		return 0
	}
	switch c := v[0]; c {
	case '{', '[', '"', 't', 'f':
		return c
	case 'n':
		return 'l'
	default:
		return 'n'
	}
}

func parseFloat(b []byte) (float64, error) { return strconv.ParseFloat(string(b), 64) }

// unescape appends the decoded string of raw (no quotes) to dst as WTF-8: UTF-8, except that a lone UTF-16
// surrogate is encoded as the three bytes of its code point, so a JavaScript string round-trips exactly.
func unescape(dst, raw []byte) []byte {
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c != '\\' {
			dst = append(dst, c)
			continue
		}
		i++
		switch raw[i] {
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
			u := hex4(raw[i+1:])
			i += 4
			if u >= 0xD800 && u <= 0xDBFF && i+6 < len(raw) && raw[i+1] == '\\' && raw[i+2] == 'u' {
				if lo := hex4(raw[i+3:]); lo >= 0xDC00 && lo <= 0xDFFF {
					dst = appendRune(dst, 0x10000+((u-0xD800)<<10)+(lo-0xDC00))
					i += 6
					continue
				}
			}
			dst = appendRune(dst, u)
		default: // " \ /
			dst = append(dst, raw[i])
		}
	}
	return dst
}

func hex4(b []byte) rune {
	return rune(hexVal(b[0]))<<12 | rune(hexVal(b[1]))<<8 | rune(hexVal(b[2]))<<4 | rune(hexVal(b[3]))
}

// appendRune appends r as UTF-8; a surrogate code point is appended as its three-byte WTF-8 form.
func appendRune(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte((r>>6)&0x3F), 0x80|byte(r&0x3F))
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte((r>>12)&0x3F), 0x80|byte((r>>6)&0x3F), 0x80|byte(r&0x3F))
	}
}

// unitsOfRaw is the JavaScript string length (UTF-16 code units) of the string whose raw JSON bytes are raw.
func unitsOfRaw(raw []byte) int {
	n := 0
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c == '\\':
			if i+1 < len(raw) && raw[i+1] == 'u' {
				i += 5
			} else {
				i++
			}
			n++
		case c < 0x80:
			n++
		case c >= 0xF0:
			n += 2
		case c >= 0xC0:
			n++
		}
	}
	return n
}

// unitsOf is the UTF-16 length of a WTF-8 string.
func unitsOf(w []byte) int {
	n := 0
	for _, c := range w {
		switch {
		case c < 0x80:
			n++
		case c >= 0xF0:
			n += 2
		case c >= 0xC0:
			n++
		}
	}
	return n
}
