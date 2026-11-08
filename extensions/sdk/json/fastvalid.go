package json

import "math/bits"

// fvMaxNestingDepth is encoding/json's limit: the 10,001st open bracket makes a text invalid.
const fvMaxNestingDepth = 10000

// fvStop is the index of the first byte at or after i that ends a run of plain string bytes (a quote, a backslash or a control character), or len(data). It reads eight bytes at a time;
// only the lowest mark of a word is exact, and that is the one it uses.
func fvStop(data []byte, i int) int {
	for ; i+8 <= len(data); i += 8 {
		w := load8(data[i : i+8])
		if stops := ((w-swarLSB*0x20)&^w | swarZero(w^(swarLSB*'"')) | swarZero(w^(swarLSB*'\\'))) & swarMSB; stops != 0 {
			return i + bits.TrailingZeros64(stops)>>3
		}
	}
	for ; i < len(data); i++ {
		if c := data[i]; c < 0x20 || c == '"' || c == '\\' {
			return i
		}
	}
	return i
}

// validFast reports whether data is exactly one JSON value surrounded by optional white space (space, tab, carriage return, line feed), with the verdict of the scanner's checkValid under its default nesting limit: the RFC 8259 grammar, no control character in a string, any \u escape, bytes of any encoding inside a string, at most 10,000 levels. It checks in one pass without the scanner's per-byte state machine. A false verdict does not say checkValid rejects the text (Unmarshal then asks checkValid, which also words the error); a true one must, and fastvalid_test.go holds it to that. pig additive (lw-json): a copy of internal/jsonvalid, which the SDK module cannot import.
func validFast(data []byte) bool {
	i := fvSkipSpace(data, 0)
	var st fvStack
	for {
		if i >= len(data) {
			return false
		}
		switch c := data[i]; {
		case c == '{':
			if !st.push(true) {
				return false
			}
			i = fvSkipSpace(data, i+1)
			if i < len(data) && data[i] == '}' {
				st.pop()
				i++
				break
			}
			if i = fvKey(data, i); i < 0 {
				return false
			}
			continue
		case c == '[':
			if !st.push(false) {
				return false
			}
			i = fvSkipSpace(data, i+1)
			if i < len(data) && data[i] == ']' {
				st.pop()
				i++
				break
			}
			continue
		case c == '"':
			if i = fvSkipString(data, i+1); i < 0 {
				return false
			}
		case c == '-' || '0' <= c && c <= '9':
			if i = fvSkipNumber(data, i); i < 0 {
				return false
			}
		case c == 't':
			if i = fvSkipLiteral(data, i, "true"); i < 0 {
				return false
			}
		case c == 'f':
			if i = fvSkipLiteral(data, i, "false"); i < 0 {
				return false
			}
		case c == 'n':
			if i = fvSkipLiteral(data, i, "null"); i < 0 {
				return false
			}
		default:
			return false
		}
		// A value ended: close the containers it ends.
		for {
			i = fvSkipSpace(data, i)
			if st.len == 0 {
				return i == len(data)
			}
			if i >= len(data) {
				return false
			}
			object := st.top()
			switch c := data[i]; {
			case c == ',':
				i = fvSkipSpace(data, i+1)
				if object {
					if i = fvKey(data, i); i < 0 {
						return false
					}
				}
			case object && c == '}', !object && c == ']':
				st.pop()
				i++
				continue
			default:
				return false
			}
			break
		}
	}
}

// fvKey reads the string fvKey of an object member at data[i] and the colon after it, and returns the index of the member's value.
func fvKey(data []byte, i int) int {
	if i >= len(data) || data[i] != '"' {
		return -1
	}
	if i = fvSkipString(data, i+1); i < 0 {
		return -1
	}
	if i = fvSkipSpace(data, i); i >= len(data) || data[i] != ':' {
		return -1
	}
	return fvSkipSpace(data, i+1)
}

func fvSkipSpace(data []byte, i int) int {
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

// fvSkipString returns the index after the string whose contents start at data[i], or -1 when it is not terminated, holds a control character or has a bad escape.
func fvSkipString(data []byte, i int) int {
	n := len(data)
	for {
		i = fvStop(data, i)
		if i >= n {
			return -1
		}
		switch c := data[i]; {
		case c == '"':
			return i + 1
		case c != '\\' || i+1 >= n:
			return -1
		case fvSimpleEscape[data[i+1]]:
			i += 2
		case data[i+1] == 'u' && i+6 <= n && fvIsHex(data[i+2]) && fvIsHex(data[i+3]) && fvIsHex(data[i+4]) && fvIsHex(data[i+5]):
			i += 6
		default:
			return -1
		}
	}
}

// fvSimpleEscape marks the characters that may follow a backslash on their own.
var fvSimpleEscape = func() (t [256]bool) {
	for _, c := range []byte(`"\\/bfnrt`) {
		t[c] = true
	}
	return t
}()

func fvIsHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// fvSkipNumber returns the index after the number at data[i]: -?(0|[1-9][0-9]*)(.[0-9]+)?([eE][+-]?[0-9]+)?, or -1.
func fvSkipNumber(data []byte, i int) int {
	if data[i] == '-' {
		if i++; i >= len(data) {
			return -1
		}
	}
	switch c := data[i]; {
	case c == '0':
		i++
	case '1' <= c && c <= '9':
		for i++; i < len(data) && fvIsDigit(data[i]); i++ {
		}
	default:
		return -1
	}
	if i < len(data) && data[i] == '.' {
		if i++; i >= len(data) || !fvIsDigit(data[i]) {
			return -1
		}
		for i++; i < len(data) && fvIsDigit(data[i]); i++ {
		}
	}
	if i < len(data) && (data[i] == 'e' || data[i] == 'E') {
		if i++; i < len(data) && (data[i] == '+' || data[i] == '-') {
			i++
		}
		if i >= len(data) || !fvIsDigit(data[i]) {
			return -1
		}
		for i++; i < len(data) && fvIsDigit(data[i]); i++ {
		}
	}
	return i
}

func fvIsDigit(c byte) bool { return '0' <= c && c <= '9' }

func fvSkipLiteral(data []byte, i int, word string) int {
	if len(data)-i < len(word) || string(data[i:i+len(word)]) != word {
		return -1
	}
	return i + len(word)
}

// fvStack records, for each open container, whether it is an object. It keeps 64 levels in a word-sized array and grows past that.
type fvStack struct {
	len   int
	small [8]uint64
	large []uint64
}

func (s *fvStack) push(object bool) bool {
	if s.len >= fvMaxNestingDepth {
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

func (s *fvStack) set(w *uint64, bit uint, object bool) {
	if object {
		*w |= 1 << bit
	} else {
		*w &^= 1 << bit
	}
}

func (s *fvStack) top() bool {
	n := s.len - 1
	word, bit := n/64, uint(n%64)
	if word >= len(s.small) {
		return s.large[word-len(s.small)]>>bit&1 == 1
	}
	return s.small[word]>>bit&1 == 1
}

func (s *fvStack) pop() { s.len-- }
