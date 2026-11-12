package uiview

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// pig additive (D614): canonical hashing. Canonical is the JSON Canonicalization Scheme of RFC 8785: no whitespace,
// members sorted by the UTF-16 code units of their names, numbers and strings written as ECMAScript's JSON.stringify
// writes them. The number and string forms come from internal/jsonstringify, the one JSON.stringify implementation of
// the repository; this file adds the input rules of the RFC and the member order.

// Canonical returns the RFC 8785 form of one JSON value. As the RFC requires of its input (I-JSON, RFC 7493), it
// rejects a duplicate member name, a lone surrogate, invalid UTF-8 and a number that is not a finite double.
func Canonical(raw []byte) ([]byte, error) {
	if err := checkIJSON(raw); err != nil {
		return nil, err
	}
	text, err := jsonstringify.Canonicalize(raw)
	if err != nil {
		return nil, err
	}
	c := canon{src: text}
	out, err := c.value(make([]byte, 0, len(text)))
	if err != nil {
		return nil, err
	}
	if c.pos != len(text) {
		return nil, errors.New("trailing data")
	}
	return out, nil
}

// Digest returns the lower-case hexadecimal SHA-256 of the canonical form of raw.
func Digest(raw []byte) (string, error) {
	text, err := Canonical(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(text)
	return hex.EncodeToString(sum[:]), nil
}

// checkIJSON reads raw as exactly one JSON value. The decoder rejects malformed text, invalid UTF-8, an unpaired
// surrogate escape and a repeated member name; a number that overflows a double is rejected here.
func checkIJSON(raw []byte) error {
	dec := jsontext.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.ReadToken()
		if err != nil {
			return err
		}
		if tok.Kind() == '0' {
			if _, err := strconv.ParseFloat(tok.String(), 64); errors.Is(err, strconv.ErrRange) {
				return fmt.Errorf("number %s is outside a double", tok.String())
			}
		}
		if dec.StackDepth() == 0 {
			break
		}
	}
	if _, err := dec.ReadToken(); err != io.EOF {
		return errors.New("trailing data")
	}
	return nil
}

// canon reorders the members of JSON text that JSON.stringify produced: compact, with every string in its one
// serialized form.
type canon struct {
	src []byte
	pos int
}

type canonMember struct {
	name []uint16
	text []byte
}

func (c *canon) value(dst []byte) ([]byte, error) {
	if c.pos >= len(c.src) {
		return nil, io.ErrUnexpectedEOF
	}
	switch c.src[c.pos] {
	case '{':
		c.pos++
		var members []canonMember
		for c.pos < len(c.src) && c.src[c.pos] != '}' {
			if c.src[c.pos] == ',' {
				c.pos++
			}
			start := c.pos
			name, err := c.str()
			if err != nil {
				return nil, err
			}
			text := append(slices.Clone(c.src[start:c.pos]), ':')
			c.pos++ // the colon
			if text, err = c.value(text); err != nil {
				return nil, err
			}
			members = append(members, canonMember{name, text})
		}
		c.pos++
		slices.SortFunc(members, func(a, b canonMember) int { return slices.Compare(a.name, b.name) })
		dst = append(dst, '{')
		for i, m := range members {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = append(dst, m.text...)
		}
		return append(dst, '}'), nil
	case '[':
		c.pos++
		dst = append(dst, '[')
		for c.pos < len(c.src) && c.src[c.pos] != ']' {
			if c.src[c.pos] == ',' {
				c.pos++
				dst = append(dst, ',')
			}
			var err error
			if dst, err = c.value(dst); err != nil {
				return nil, err
			}
		}
		c.pos++
		return append(dst, ']'), nil
	case '"':
		start := c.pos
		if _, err := c.str(); err != nil {
			return nil, err
		}
		return append(dst, c.src[start:c.pos]...), nil
	}
	start := c.pos
	for c.pos < len(c.src) && c.src[c.pos] != ',' && c.src[c.pos] != ']' && c.src[c.pos] != '}' {
		c.pos++
	}
	return append(dst, c.src[start:c.pos]...), nil
}

// str reads the string token at pos and returns its UTF-16 code units, the sort key of a member name. An escaped
// surrogate is a lone one, because JSON.stringify writes a pair as the character itself.
func (c *canon) str() ([]uint16, error) {
	var units []uint16
	i := c.pos + 1
	for i < len(c.src) {
		b := c.src[i]
		switch {
		case b == '"':
			c.pos = i + 1
			return units, nil
		case b == '\\':
			if i+1 >= len(c.src) {
				return nil, io.ErrUnexpectedEOF
			}
			i++
			switch c.src[i] {
			case 'u':
				if i+4 >= len(c.src) {
					return nil, io.ErrUnexpectedEOF
				}
				n, err := strconv.ParseUint(string(c.src[i+1:i+5]), 16, 16)
				if err != nil {
					return nil, err
				}
				if n >= 0xD800 && n <= 0xDFFF {
					return nil, fmt.Errorf("lone surrogate U+%04X", n)
				}
				units = append(units, uint16(n))
				i += 5
				continue
			case 'b':
				units = append(units, '\b')
			case 'f':
				units = append(units, '\f')
			case 'n':
				units = append(units, '\n')
			case 'r':
				units = append(units, '\r')
			case 't':
				units = append(units, '\t')
			default:
				units = append(units, uint16(c.src[i]))
			}
			i++
		default:
			r, size := utf8.DecodeRune(c.src[i:])
			units = utf16.AppendRune(units, r)
			i += size
		}
	}
	return nil, io.ErrUnexpectedEOF
}
