package jsonstringify

import (
	"bytes"
	"slices"
	"unicode/utf8"
)

const (
	// maxFastDepth bounds the nesting the scan follows; deeper text takes the full rewrite.
	maxFastDepth = 32
	// maxLinearObjectKeys is the most keys of one object checked for repeats one against another; a larger object sorts its keys once.
	maxLinearObjectKeys = 24
	// maxInlineKeys is how many keys of the objects being scanned the scanner holds without allocating.
	maxInlineKeys = 96
	// maxExactDigits is the most significant digits a decimal can carry and still print as itself: every such decimal maps to its own double, and the shortest form that parses back to it is the decimal without trailing zeros.
	maxExactDigits = 15
)

type span struct{ start, end int }

// scanner decides, in one pass over validated JSON text, whether JSON.stringify(JSON.parse(text)) is text itself. It answers false whenever it cannot prove that: the full rewrite then decides. It allocates only for text holding more than maxInlineKeys keys at once on its open objects, or an object of more than maxLinearObjectKeys keys.
type scanner struct {
	text     []byte
	inline   [maxInlineKeys]span
	overflow []span
	sorted   []span
	nkeys    int
}

// IsCanonical reports whether Canonicalize(text) would return text unchanged, as JSON.stringify(JSON.parse(text)) writes it; a false result does not say it would change. The text must be trimmed.
//
// What the rewrite changes, and so what the scan refuses: whitespace between tokens; string escapes JSON.stringify does not write (\/, an uppercase-hex or non-control non-surrogate \u escape, \u000a for \n, a surrogate pair spelled as two escapes); invalid UTF-8; numbers that are not already the shortest ECMAScript form (-0, a trailing zero or exponent, more than 15 significant digits, below 1e-6); integer-like object keys, which JSON.stringify writes first; repeated object keys, where the last wins; and anything that is not a complete JSON value.
func IsCanonical(text []byte) bool {
	if len(text) == 0 || !utf8.Valid(text) {
		return false
	}
	state := scanner{text: text}
	end := state.value(0, 0)
	return end == len(text)
}

func (state *scanner) value(i, depth int) int {
	if i >= len(state.text) || depth > maxFastDepth {
		return -1
	}
	switch c := state.text[i]; {
	case c == '{':
		return state.object(i+1, depth)
	case c == '[':
		return state.array(i+1, depth)
	case c == '"':
		return state.str(i)
	case c == 't':
		return state.literal(i, "true")
	case c == 'f':
		return state.literal(i, "false")
	case c == 'n':
		return state.literal(i, "null")
	case c == '-' || c >= '0' && c <= '9':
		return state.number(i)
	}
	return -1
}

func (state *scanner) literal(i int, word string) int {
	if i+len(word) > len(state.text) || string(state.text[i:i+len(word)]) != word {
		return -1
	}
	return i + len(word)
}

func (state *scanner) array(i, depth int) int {
	if i < len(state.text) && state.text[i] == ']' {
		return i + 1
	}
	for {
		if i = state.value(i, depth+1); i < 0 || i >= len(state.text) {
			return -1
		}
		switch state.text[i] {
		case ',':
			i++
		case ']':
			return i + 1
		default:
			return -1
		}
	}
}

func (state *scanner) object(i, depth int) int {
	if i < len(state.text) && state.text[i] == '}' {
		return i + 1
	}
	base := state.nkeys
	defer func() { state.nkeys = base }()
	for {
		if i >= len(state.text) || state.text[i] != '"' {
			return -1
		}
		end := state.str(i)
		if end < 0 {
			return -1
		}
		name := state.text[i+1 : end-1]
		if integerLike(name) {
			return -1
		}
		// A repeated key among the first members is found here; one among more members is found by sorting them at the end.
		if state.nkeys-base < maxLinearObjectKeys {
			for k := base; k < state.nkeys; k++ {
				if earlier := state.key(k); bytes.Equal(state.text[earlier.start:earlier.end], name) {
					return -1
				}
			}
		}
		state.push(span{i + 1, end - 1})
		if end >= len(state.text) || state.text[end] != ':' {
			return -1
		}
		if i = state.value(end+1, depth+1); i < 0 || i >= len(state.text) {
			return -1
		}
		switch state.text[i] {
		case ',':
			i++
		case '}':
			if state.nkeys-base > maxLinearObjectKeys && state.repeatedKey(base) {
				return -1
			}
			return i + 1
		default:
			return -1
		}
	}
}

// push adds a key to the stack of the keys of the objects being scanned: the first maxInlineKeys in the scanner itself, the rest on the heap.
func (state *scanner) push(key span) {
	if state.nkeys < maxInlineKeys {
		state.inline[state.nkeys] = key
	} else {
		if state.overflow == nil {
			state.overflow = make([]span, 0, 4*maxInlineKeys)
		}
		state.overflow = append(state.overflow[:state.nkeys-maxInlineKeys], key)
	}
	state.nkeys++
}

func (state *scanner) key(k int) span {
	if k < maxInlineKeys {
		return state.inline[k]
	}
	return state.overflow[k-maxInlineKeys]
}

// repeatedKey reports whether two keys of the object whose keys start at base are equal, by sorting a copy of them.
func (state *scanner) repeatedKey(base int) bool {
	sorted := slices.Grow(state.sorted[:0], state.nkeys-base)
	for k := base; k < state.nkeys; k++ {
		sorted = append(sorted, state.key(k))
	}
	text := state.text
	slices.SortFunc(sorted, func(a, b span) int { return bytes.Compare(text[a.start:a.end], text[b.start:b.end]) })
	state.sorted = sorted
	for k := 1; k < len(sorted); k++ {
		if bytes.Equal(text[sorted[k-1].start:sorted[k-1].end], text[sorted[k].start:sorted[k].end]) {
			return true
		}
	}
	return false
}

// integerLike is true for a key the rewrite might treat as an array index: only digits, at most ten of them. It over-approximates (leading zeros and values past 2^32-1 are refused too), which only sends such objects to the full rewrite.
func integerLike(name []byte) bool {
	if len(name) == 0 || len(name) > 10 {
		return false
	}
	for _, c := range name {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// str scans one string token starting at its opening quote and returns the index after the closing quote, or -1. A byte below 0x20 cannot appear raw, and the only escapes accepted are those JSON.stringify writes: \", \\, \b, \f, \n, \r, \t, a lowercase \u00xx for any other control character, and a lowercase \ud800-\udfff escape for a lone surrogate (never a high one directly followed by a low one, which stringify writes as the character they form).
func (state *scanner) str(i int) int {
	text := state.text
	highEnd := -1 // index just past a high-surrogate escape
	for i++; i < len(text); i++ {
		switch c := text[i]; {
		case c == '"':
			return i + 1
		case c == '\\':
			i++
			if i >= len(text) {
				return -1
			}
			switch text[i] {
			case '"', '\\', 'n', 'r', 't', 'b', 'f':
			case 'u':
				if i+4 >= len(text) {
					return -1
				}
				value := 0
				for _, h := range text[i+1 : i+5] {
					switch {
					case h >= '0' && h <= '9':
						value = value<<4 | int(h-'0')
					case h >= 'a' && h <= 'f':
						value = value<<4 | int(h-'a'+10)
					default:
						return -1
					}
				}
				switch {
				case value < 0x20:
					switch value {
					case 8, 9, 10, 12, 13: // written as \b \t \n \f \r
						return -1
					}
				case value >= 0xd800 && value <= 0xdbff:
					highEnd = i + 5
				case value >= 0xdc00 && value <= 0xdfff:
					if highEnd == i-1 { // a pair is written as the character it forms
						return -1
					}
				default:
					return -1
				}
				i += 4
			default:
				return -1
			}
		case c < 0x20:
			return -1
		}
	}
	return -1
}

// number accepts -?int[.frac] already in shortest form: no leading zeros, no exponent, a fraction without a trailing zero, at most maxExactDigits significant digits, and at least 1e-6 in magnitude (ECMAScript switches to exponent form below that). Zero is only the token "0".
func (state *scanner) number(i int) int {
	text := state.text
	negative := text[i] == '-'
	if negative {
		i++
	}
	if i >= len(text) || text[i] < '0' || text[i] > '9' {
		return -1
	}
	intStart := i
	if text[i] == '0' {
		i++
	} else {
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
		}
	}
	intDigits := i - intStart
	integerIsZero := intDigits == 1 && text[intStart] == '0'
	fracDigits, leadingZeros := 0, 0
	if i < len(text) && text[i] == '.' {
		i++
		fracStart := i
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
		}
		fracDigits = i - fracStart
		if fracDigits == 0 || text[i-1] == '0' {
			return -1
		}
		for leadingZeros < fracDigits && text[fracStart+leadingZeros] == '0' {
			leadingZeros++
		}
	}
	if i < len(text) {
		switch text[i] {
		case ',', ']', '}':
		default:
			return -1
		}
	}
	if integerIsZero && fracDigits == 0 {
		return skipIf(!negative, i)
	}
	significant := intDigits + fracDigits
	if integerIsZero {
		significant = fracDigits - leadingZeros
		if leadingZeros > 5 {
			return -1
		}
	}
	return skipIf(significant <= maxExactDigits, i)
}

func skipIf(ok bool, i int) int {
	if ok {
		return i
	}
	return -1
}
