// Package jsonvalid reports whether a byte slice is one valid JSON text, with the verdict of the encoding/json scanner that session files were first checked with: the RFC 8259 grammar, no control character in a string, any \u escape (a lone surrogate included), bytes of any encoding inside a string, and nesting of at most 10,000 levels. It checks in a single pass without the scanner's per-byte state machine.
package jsonvalid

// maxNestingDepth is encoding/json's limit: the 10,001st open bracket makes a text invalid.
const maxNestingDepth = 10000

// special marks the bytes a string scan stops at: the closing quote, the escape introducer and the control characters.
var special = func() (t [256]bool) {
	t['"'], t['\\'] = true, true
	for c := range 0x20 {
		t[c] = true
	}
	return t
}()

// Valid reports whether data is exactly one JSON value surrounded by optional white space (space, tab, carriage return, line feed).
func Valid(data []byte) bool {
	i := skipSpace(data, 0)
	var stack stack
	for {
		if i >= len(data) {
			return false
		}
		switch c := data[i]; {
		case c == '{':
			if !stack.push(true) {
				return false
			}
			i = skipSpace(data, i+1)
			if i < len(data) && data[i] == '}' {
				stack.pop()
				i++
				break
			}
			if i = key(data, i); i < 0 {
				return false
			}
			continue
		case c == '[':
			if !stack.push(false) {
				return false
			}
			i = skipSpace(data, i+1)
			if i < len(data) && data[i] == ']' {
				stack.pop()
				i++
				break
			}
			continue
		case c == '"':
			if i = skipString(data, i+1); i < 0 {
				return false
			}
		case c == '-' || '0' <= c && c <= '9':
			if i = skipNumber(data, i); i < 0 {
				return false
			}
		case c == 't':
			if i = skipLiteral(data, i, "true"); i < 0 {
				return false
			}
		case c == 'f':
			if i = skipLiteral(data, i, "false"); i < 0 {
				return false
			}
		case c == 'n':
			if i = skipLiteral(data, i, "null"); i < 0 {
				return false
			}
		default:
			return false
		}
		// A value ended: close the containers it ends.
		for {
			i = skipSpace(data, i)
			if stack.len == 0 {
				return i == len(data)
			}
			if i >= len(data) {
				return false
			}
			object := stack.top()
			switch c := data[i]; {
			case c == ',':
				i = skipSpace(data, i+1)
				if object {
					if i = key(data, i); i < 0 {
						return false
					}
				}
			case object && c == '}', !object && c == ']':
				stack.pop()
				i++
				continue
			default:
				return false
			}
			break
		}
	}
}

// key reads the string key of an object member at data[i] and the colon after it, and returns the index of the member's value.
func key(data []byte, i int) int {
	if i >= len(data) || data[i] != '"' {
		return -1
	}
	if i = skipString(data, i+1); i < 0 {
		return -1
	}
	if i = skipSpace(data, i); i >= len(data) || data[i] != ':' {
		return -1
	}
	return skipSpace(data, i+1)
}

func skipSpace(data []byte, i int) int {
	for i < len(data) {
		switch data[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

// skipString returns the index after the string whose contents start at data[i], or -1 when it is not terminated, holds a control character or has a bad escape.
func skipString(data []byte, i int) int {
	n := len(data)
	for {
		for i < n && !special[data[i]] {
			i++
		}
		if i >= n {
			return -1
		}
		switch c := data[i]; {
		case c == '"':
			return i + 1
		case c != '\\' || i+1 >= n:
			return -1
		case simpleEscape[data[i+1]]:
			i += 2
		case data[i+1] == 'u' && i+6 <= n && isHex(data[i+2]) && isHex(data[i+3]) && isHex(data[i+4]) && isHex(data[i+5]):
			i += 6
		default:
			return -1
		}
	}
}

// simpleEscape marks the characters that may follow a backslash on their own.
var simpleEscape = func() (t [256]bool) {
	for _, c := range []byte(`"\\/bfnrt`) {
		t[c] = true
	}
	return t
}()

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// skipNumber returns the index after the number at data[i]: -?(0|[1-9][0-9]*)(.[0-9]+)?([eE][+-]?[0-9]+)?, or -1.
func skipNumber(data []byte, i int) int {
	if data[i] == '-' {
		if i++; i >= len(data) {
			return -1
		}
	}
	switch c := data[i]; {
	case c == '0':
		i++
	case '1' <= c && c <= '9':
		for i++; i < len(data) && isDigit(data[i]); i++ {
		}
	default:
		return -1
	}
	if i < len(data) && data[i] == '.' {
		if i++; i >= len(data) || !isDigit(data[i]) {
			return -1
		}
		for i++; i < len(data) && isDigit(data[i]); i++ {
		}
	}
	if i < len(data) && (data[i] == 'e' || data[i] == 'E') {
		if i++; i < len(data) && (data[i] == '+' || data[i] == '-') {
			i++
		}
		if i >= len(data) || !isDigit(data[i]) {
			return -1
		}
		for i++; i < len(data) && isDigit(data[i]); i++ {
		}
	}
	return i
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

func skipLiteral(data []byte, i int, word string) int {
	if len(data)-i < len(word) || string(data[i:i+len(word)]) != word {
		return -1
	}
	return i + len(word)
}

// stack records, for each open container, whether it is an object. It keeps 64 levels in a word-sized array and grows past that.
type stack struct {
	len   int
	small [8]uint64
	large []uint64
}

func (s *stack) push(object bool) bool {
	if s.len >= maxNestingDepth {
		return false
	}
	word, bit := s.len/64, uint(s.len%64)
	if word >= len(s.small) {
		for len(s.large) <= word-len(s.small) {
			s.large = append(s.large, 0)
		}
		s.set(&s.large[word-len(s.small)], bit, object)
	} else {
		s.set(&s.small[word], bit, object)
	}
	s.len++
	return true
}

func (s *stack) set(w *uint64, bit uint, object bool) {
	if object {
		*w |= 1 << bit
	} else {
		*w &^= 1 << bit
	}
}

func (s *stack) top() bool {
	n := s.len - 1
	word, bit := n/64, uint(n%64)
	if word >= len(s.small) {
		return s.large[word-len(s.small)]>>bit&1 == 1
	}
	return s.small[word]>>bit&1 == 1
}

func (s *stack) pop() { s.len-- }
