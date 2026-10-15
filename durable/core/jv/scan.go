package jv

import (
	"errors"
	"unicode/utf8"
)

// ErrNotArray reports JSON text that Parse accepts but whose top-level value is not an array. Read-only: assigned once at
// initialization.
var ErrNotArray = errors.New("JSON is not an array")

// Member is one member of a JSON object text: its key as Parse decodes it, and the raw JSON text of its value.
type Member struct {
	Key string
	Raw []byte // the value's text, without surrounding white space; it aliases the scanned text
}

// SplitArrayMembers reports, for each element of the array text in order, the members of the element when it is an object, and
// nil when it is another kind of value, without building the values. It accepts exactly the texts Parse accepts and fails with
// ErrSyntax on the others; a text that Parse accepts as another kind of value fails with ErrNotArray. A member's key is the key
// as Parse decodes it, and its text is valid JSON that Parse reads as its value. Members are reported in the order they are
// written, with a repeated key reported each time: Parse keeps the last value of a repeated key, so a caller that reads one key
// takes the last member that has it.
func SplitArrayMembers(text []byte) (members [][]Member, err error) {
	p := parser{s: text}
	p.ws()
	if p.i >= len(p.s) || p.s[p.i] != '[' {
		return nil, p.otherThan(ErrNotArray)
	}
	p.i++
	p.ws()
	if p.i < len(p.s) && p.s[p.i] == ']' {
		p.i++
		members = [][]Member{}
	} else {
		members = make([][]Member, 0, firstMembers)
	elements:
		for {
			p.ws()
			if p.i < len(p.s) && p.s[p.i] == '{' {
				m, err := p.members(1)
				if err != nil {
					return nil, err
				}
				members = append(members, m)
			} else {
				if err := p.skipValue(1); err != nil {
					return nil, err
				}
				members = append(members, nil)
			}
			p.ws()
			if p.i >= len(p.s) {
				return nil, ErrSyntax
			}
			switch p.s[p.i] {
			case ',':
				p.i++
			case ']':
				p.i++
				break elements
			default:
				return nil, ErrSyntax
			}
		}
	}
	p.ws()
	if p.i != len(p.s) {
		return nil, ErrSyntax
	}
	return members, nil
}

// members reads the members of the object at p.i, whose own depth is depth; an empty object has a non-nil, empty result.
func (p *parser) members(depth int) ([]Member, error) {
	if depth > maxDepth {
		return nil, ErrSyntax
	}
	p.i++
	p.ws()
	if p.i < len(p.s) && p.s[p.i] == '}' {
		p.i++
		return []Member{}, nil
	}
	out := make([]Member, 0, firstMembers)
	for {
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != '"' {
			return nil, ErrSyntax
		}
		key, err := p.str()
		if err != nil {
			return nil, err
		}
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			return nil, ErrSyntax
		}
		p.i++
		p.ws()
		start := p.i
		if err := p.skipValue(depth + 1); err != nil {
			return nil, err
		}
		out = append(out, Member{Key: key, Raw: p.s[start:p.i]})
		p.ws()
		if p.i >= len(p.s) {
			return nil, ErrSyntax
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case '}':
			p.i++
			return out, nil
		default:
			return nil, ErrSyntax
		}
	}
}

// MemberText returns the text of the value of the member named key of the object text, the last member of the name as Parse
// keeps it, or nil when there is none. text must be an object that Parse accepts, such as the text of a member that
// SplitArrayMembers reported: MemberText does not validate it, it only walks past each value. It does not panic on other text
// and returns whatever it reads.
func MemberText(text []byte, key string) []byte {
	p := parser{s: text}
	p.ws()
	if p.i >= len(p.s) || p.s[p.i] != '{' {
		return nil
	}
	p.i++
	var found []byte
	for {
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != '"' {
			return found
		}
		start := p.i
		p.skimStr()
		if p.i-1 < start+1 || p.s[p.i-1] != '"' {
			return found
		}
		name := p.s[start+1 : p.i-1]
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			return found
		}
		p.i++
		p.ws()
		vs := p.i
		p.skim()
		if keyIs(name, key) {
			found = p.s[vs:p.i]
		}
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != ',' {
			return found
		}
		p.i++
	}
}

// keyIs reports whether the member name, the text between a key's quotes, is key.
func keyIs(name []byte, key string) bool {
	for _, c := range name {
		if c == '\\' || c >= utf8.RuneSelf {
			// A name with an escape or an ill-formed UTF-8 sequence is decoded as Parse decodes it.
			p := parser{s: append(append([]byte{'"'}, name...), '"')}
			decoded, err := p.str()
			return err == nil && decoded == key
		}
	}
	return string(name) == key
}

// skimStr advances over the string at p.i, which is valid, to just past its closing quote.
func (p *parser) skimStr() {
	p.i++
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case '"':
			p.i++
			return
		case '\\':
			p.i++
		}
		p.i++
	}
	p.i = min(p.i, len(p.s))
}

// skim advances over the valid value at p.i.
func (p *parser) skim() {
	if p.i >= len(p.s) {
		return
	}
	switch p.s[p.i] {
	case '"':
		p.skimStr()
	case '{', '[':
		depth := 0
		for p.i < len(p.s) {
			switch p.s[p.i] {
			case '"':
				p.skimStr()
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					p.i++
					return
				}
			}
			p.i++
		}
	default:
		for p.i < len(p.s) {
			switch p.s[p.i] {
			case ',', '}', ']', ' ', '\t', '\n', '\r':
				return
			}
			p.i++
		}
	}
}

// IsNonEmptyArray reports whether text, the text of a JSON value that Parse accepts, is an array with at least one element.
func IsNonEmptyArray(text []byte) bool {
	if len(text) == 0 || text[0] != '[' {
		return false
	}
	p := parser{s: text, i: 1}
	p.ws()
	return p.i < len(p.s) && p.s[p.i] != ']'
}

// otherThan is the error of a text whose top-level value is not the kind the caller asked for: ErrSyntax when Parse rejects the
// text, else kind.
func (p *parser) otherThan(kind error) error {
	p.i = 0
	p.ws()
	if err := p.skipValue(0); err != nil {
		return err
	}
	p.ws()
	if p.i != len(p.s) {
		return ErrSyntax
	}
	return kind
}

// skipValue advances over one JSON value with the grammar and the depth bound of value, and builds nothing.
func (p *parser) skipValue(depth int) error {
	if depth > maxDepth || p.i >= len(p.s) {
		return ErrSyntax
	}
	switch c := p.s[p.i]; {
	case c == '{':
		return p.skipObject(depth)
	case c == '[':
		return p.skipArray(depth)
	case c == '"':
		return p.skipStr()
	case c == 't':
		return p.skipLit("true")
	case c == 'f':
		return p.skipLit("false")
	case c == 'n':
		return p.skipLit("null")
	case c == '-' || (c >= '0' && c <= '9'):
		return p.scanNumber()
	}
	return ErrSyntax
}

func (p *parser) skipLit(word string) error {
	if len(p.s)-p.i < len(word) || string(p.s[p.i:p.i+len(word)]) != word {
		return ErrSyntax
	}
	p.i += len(word)
	return nil
}

// skipStr advances over one string. str accepts every byte but a quote, a backslash and a control character as itself or, for
// ill-formed UTF-8, as U+FFFD, and fails only on a control character, an unknown or short escape, and a missing closing quote.
func (p *parser) skipStr() error {
	p.i++ // opening quote
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == '"':
			p.i++
			return nil
		case c < 0x20:
			return ErrSyntax
		case c != '\\':
			p.i++
			continue
		}
		p.i++
		if p.i >= len(p.s) {
			return ErrSyntax
		}
		e := p.s[p.i]
		p.i++
		switch e {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		case 'u':
			if _, ok := p.hex4(); !ok {
				return ErrSyntax
			}
		default:
			return ErrSyntax
		}
	}
	return ErrSyntax
}

func (p *parser) skipArray(depth int) error {
	p.i++
	p.ws()
	if p.i < len(p.s) && p.s[p.i] == ']' {
		p.i++
		return nil
	}
	for {
		p.ws()
		if err := p.skipValue(depth + 1); err != nil {
			return err
		}
		p.ws()
		if p.i >= len(p.s) {
			return ErrSyntax
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case ']':
			p.i++
			return nil
		default:
			return ErrSyntax
		}
	}
}

func (p *parser) skipObject(depth int) error {
	p.i++
	p.ws()
	if p.i < len(p.s) && p.s[p.i] == '}' {
		p.i++
		return nil
	}
	for {
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != '"' {
			return ErrSyntax
		}
		if err := p.skipStr(); err != nil {
			return err
		}
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			return ErrSyntax
		}
		p.i++
		p.ws()
		if err := p.skipValue(depth + 1); err != nil {
			return err
		}
		p.ws()
		if p.i >= len(p.s) {
			return ErrSyntax
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case '}':
			p.i++
			return nil
		default:
			return ErrSyntax
		}
	}
}
