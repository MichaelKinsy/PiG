package json

// pig additive (D19): the validity pass of every wire frame and every Unmarshal.
//
// checkValid steps a state machine one byte at a time through an indirect call. A frame the host sends is mostly string bytes (message
// text, base64 images), which this file crosses eight bytes at a time. validate accepts exactly the documents checkValid accepts; on any
// document it does not accept it runs checkValid, so a malformed document produces the same *SyntaxError (message and offset) as before.

// validate is checkValid: it returns nil for the documents checkValid accepts and checkValid's error for the rest.
func validate(data []byte, scan *scanner) error {
	limit := scan.maxDepth
	if limit == 0 {
		limit = maxNestingDepth
	}
	ok, stack := validDocument(data, limit, scan.parseState[:0])
	scan.parseState = stack[:0]
	if ok {
		return nil
	}
	return checkValid(data, scan)
}

// validDocument reports whether data is one JSON value nested at most limit levels, followed by nothing but white space. It never reports a
// document valid that checkValid rejects, and never reports one invalid that checkValid accepts (FuzzValidateMatchesCheckValid). stack is
// the open containers, one parseObjectValue or parseArrayValue each; it is returned so the caller keeps its capacity.
func validDocument(data []byte, limit int, stack []int) (bool, []int) {
	n := len(data)
	i := 0
	for {
		// A value starts at data[i] after white space.
		i = skipSpace(data, i)
		if i >= n {
			return false, stack
		}
		switch c := data[i]; {
		case c == '{':
			if len(stack) >= limit {
				return false, stack
			}
			i = skipSpace(data, i+1)
			if i < n && data[i] == '}' {
				i++
				break
			}
			stack = append(stack, parseObjectValue)
			var ok bool
			if i, ok = objectKey(data, i); !ok {
				return false, stack
			}
			continue
		case c == '[':
			if len(stack) >= limit {
				return false, stack
			}
			i = skipSpace(data, i+1)
			if i < n && data[i] == ']' {
				i++
				break
			}
			stack = append(stack, parseArrayValue)
			continue
		case c == '"':
			var ok bool
			if i, ok = stringEnd(data, i+1); !ok {
				return false, stack
			}
		case c == '-' || c >= '0' && c <= '9':
			var ok bool
			if i, ok = numberEnd(data, i); !ok {
				return false, stack
			}
		case c == 't':
			if !hasLiteralAt(data, i, "true") {
				return false, stack
			}
			i += 4
		case c == 'f':
			if !hasLiteralAt(data, i, "false") {
				return false, stack
			}
			i += 5
		case c == 'n':
			if !hasLiteralAt(data, i, "null") {
				return false, stack
			}
			i += 4
		default:
			return false, stack
		}
		// A value ended before data[i]: the open containers say what may follow.
	closing:
		for {
			if len(stack) == 0 {
				return skipSpace(data, i) == n, stack
			}
			i = skipSpace(data, i)
			if i >= n {
				return false, stack
			}
			switch data[i] {
			case ',':
				i++
				if stack[len(stack)-1] == parseObjectValue {
					i = skipSpace(data, i)
					var ok bool
					if i, ok = objectKey(data, i); !ok {
						return false, stack
					}
				}
				break closing
			case ']':
				if stack[len(stack)-1] != parseArrayValue {
					return false, stack
				}
				stack = stack[:len(stack)-1]
				i++
			case '}':
				if stack[len(stack)-1] != parseObjectValue {
					return false, stack
				}
				stack = stack[:len(stack)-1]
				i++
			default:
				return false, stack
			}
		}
	}
}

// skipSpace is the index of the first byte at or after i that is not JSON white space.
func skipSpace(data []byte, i int) int {
	for i < len(data) && isSpace(data[i]) {
		i++
	}
	return i
}

// objectKey reads the member name and colon that start at data[i] and returns the index after the colon, where the member's value starts
// after white space.
func objectKey(data []byte, i int) (int, bool) {
	if i >= len(data) || data[i] != '"' {
		return i, false
	}
	i, ok := stringEnd(data, i+1)
	if !ok {
		return i, false
	}
	i = skipSpace(data, i)
	if i >= len(data) || data[i] != ':' {
		return i, false
	}
	return i + 1, true
}

// stringEnd is the index after the closing quote of the string whose content starts at data[i], or false when the string is not valid
// JSON: a byte below 0x20 or a bad escape in it, or no closing quote.
func stringEnd(data []byte, i int) (int, bool) {
	for {
		i = stringContentEnd(data, i)
		if i >= len(data) {
			return i, false
		}
		switch data[i] {
		case '"':
			return i + 1, true
		case '\\':
			if i+1 >= len(data) {
				return i, false
			}
			switch data[i+1] {
			case 'b', 'f', 'n', 'r', 't', '\\', '/', '"':
				i += 2
			case 'u':
				if i+6 > len(data) || !isHex(data[i+2]) || !isHex(data[i+3]) || !isHex(data[i+4]) || !isHex(data[i+5]) {
					return i, false
				}
				i += 6
			default:
				return i, false
			}
		default: // a byte below 0x20
			return i, false
		}
	}
}

func isHex(c byte) bool { return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' }

// numberEnd is the index after the number that starts at data[i] (a '-' or a digit), or false when what starts there is not a JSON number.
// The grammar is -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?; what follows the number is the caller's to judge.
func numberEnd(data []byte, i int) (int, bool) {
	n := len(data)
	if data[i] == '-' {
		i++
		if i >= n {
			return i, false
		}
	}
	switch c := data[i]; {
	case c == '0':
		i++
	case c >= '1' && c <= '9':
		for i++; i < n && data[i] >= '0' && data[i] <= '9'; i++ {
		}
	default:
		return i, false
	}
	if i < n && data[i] == '.' {
		i++
		if i >= n || data[i] < '0' || data[i] > '9' {
			return i, false
		}
		for i++; i < n && data[i] >= '0' && data[i] <= '9'; i++ {
		}
	}
	if i < n && (data[i] == 'e' || data[i] == 'E') {
		i++
		if i < n && (data[i] == '+' || data[i] == '-') {
			i++
		}
		if i >= n || data[i] < '0' || data[i] > '9' {
			return i, false
		}
		for i++; i < n && data[i] >= '0' && data[i] <= '9'; i++ {
		}
	}
	return i, true
}

// hasLiteralAt reports whether data[i:] starts with literal.
func hasLiteralAt(data []byte, i int, literal string) bool {
	return len(data)-i >= len(literal) && string(data[i:i+len(literal)]) == literal
}
