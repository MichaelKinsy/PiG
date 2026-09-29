// Package jsonparse reports ECMAScript JSON.parse syntax errors for rejected JSON text.
package jsonparse

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
)

// Validate returns the error JSON.parse throws for input, with V8's message, or nil when input parses.
func Validate(input []byte) error {
	if json.Valid(input) {
		return nil
	}
	if message := SyntaxError(input); message != nil {
		return errors.New(string(utf16.Decode(message)))
	}
	return nil
}

// SyntaxError returns the message of the SyntaxError V8's JSON.parse throws for input, in UTF-16 units, or nil when
// input parses. Callers own validation and call it only for rejected JSON; positions and excerpts count UTF-16 units.
func SyntaxError(input []byte) []uint16 {
	p := parser{text: utf16.Encode([]rune(string(input)))}
	if message := p.value(); message != nil {
		return message
	}
	p.space()
	if p.pos < len(p.text) {
		return p.at("Unexpected non-whitespace character after JSON")
	}
	return nil
}

type parser struct {
	text []uint16
	pos  int
}

func (p *parser) char() uint16 {
	if p.pos == len(p.text) {
		return 0
	}
	return p.text[p.pos]
}
func (p *parser) space() {
	for p.pos < len(p.text) && strings.ContainsRune(" \t\r\n", rune(p.char())) {
		p.pos++
	}
}
func (p *parser) at(message string) []uint16 {
	line, column := 1, 1
	for i, c := range p.text[:p.pos] {
		if c == '\r' || c == '\n' {
			if c != '\n' || i == 0 || p.text[i-1] != '\r' {
				line++
			}
			column = 1
		} else {
			column++
		}
	}
	if !strings.HasSuffix(message, "JSON") {
		message += " in JSON"
	}
	return utf16.Encode([]rune(fmt.Sprintf("%s at position %d (line %d column %d)", message, p.pos, line, column)))
}
func (p *parser) unexpected() []uint16 {
	if p.pos == len(p.text) {
		return utf16.Encode([]rune("Unexpected end of JSON input"))
	}
	source := string(utf16.Decode(p.text))
	switch source {
	case "undefined", "NaN", "Infinity", "[object Object]":
		return utf16.Encode([]rune(fmt.Sprintf("\"%s\" is not valid JSON", source)))
	}
	start, end := 0, len(p.text)
	if len(p.text) > 20 {
		start, end = max(0, p.pos-10), min(len(p.text), p.pos+10)
	}
	message := utf16.Encode([]rune("Unexpected token '"))
	message = append(message, p.text[p.pos], '\'', ',', ' ')
	if len(p.text) > 20 && p.pos >= 10 {
		message = append(message, '.', '.', '.')
	}
	message = append(message, '"')
	message = append(message, p.text[start:end]...)
	message = append(message, '"')
	if end < len(p.text) {
		message = append(message, '.', '.', '.')
	}
	return append(message, utf16.Encode([]rune(" is not valid JSON"))...)
}
func (p *parser) value() []uint16 {
	p.space()
	switch p.char() {
	case '{':
		p.pos++
		p.space()
		if p.char() == '}' {
			p.pos++
			return nil
		}
		keyError := "Expected property name or '}'"
		for {
			if p.char() != '"' {
				return p.at(keyError)
			}
			if err := p.stringValue(); err != nil {
				return err
			}
			p.space()
			if p.char() != ':' {
				return p.at("Expected ':' after property name")
			}
			p.pos++
			if err := p.value(); err != nil {
				return err
			}
			p.space()
			if p.char() == '}' {
				p.pos++
				return nil
			}
			if p.char() != ',' {
				return p.at("Expected ',' or '}' after property value")
			}
			p.pos++
			p.space()
			keyError = "Expected double-quoted property name"
		}
	case '[':
		p.pos++
		p.space()
		if p.char() == ']' {
			p.pos++
			return nil
		}
		for {
			if err := p.value(); err != nil {
				return err
			}
			p.space()
			if p.char() == ']' {
				p.pos++
				return nil
			}
			if p.char() != ',' {
				return p.at("Expected ',' or ']' after array element")
			}
			p.pos++
		}
	case '"':
		return p.stringValue()
	case 't', 'f', 'n':
		literal := map[uint16]string{'t': "true", 'f': "false", 'n': "null"}[p.char()]
		for _, c := range literal {
			if p.char() != uint16(c) {
				return p.unexpected()
			}
			p.pos++
		}
		return nil
	default:
		if p.char() == '-' || p.digit() {
			return p.number()
		}
		return p.unexpected()
	}
}
func (p *parser) digit() bool { return p.char() >= '0' && p.char() <= '9' }
func (p *parser) number() []uint16 {
	if p.char() == '-' {
		p.pos++
		if !p.digit() {
			return p.at("No number after minus sign")
		}
	}
	if p.char() == '0' {
		p.pos++
		if p.digit() {
			return p.at("Unexpected number")
		}
	} else {
		for p.digit() {
			p.pos++
		}
	}
	if p.char() == '.' {
		p.pos++
		if !p.digit() {
			return p.at("Unterminated fractional number")
		}
		for p.digit() {
			p.pos++
		}
	}
	if p.char() == 'e' || p.char() == 'E' {
		p.pos++
		if p.char() == '+' || p.char() == '-' {
			p.pos++
		}
		if !p.digit() {
			return p.at("Exponent part is missing a number")
		}
		for p.digit() {
			p.pos++
		}
	}
	return nil
}
func (p *parser) stringValue() []uint16 {
	p.pos++
	for p.pos < len(p.text) {
		c := p.char()
		if c == '"' {
			p.pos++
			return nil
		}
		if c < 0x20 {
			return p.at("Bad control character in string literal")
		}
		p.pos++
		if c != '\\' {
			continue
		}
		if p.pos == len(p.text) {
			return p.unexpected()
		}
		c = p.char()
		if c == 'u' {
			p.pos++
			for range 4 {
				c = p.char()
				if !p.digit() && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
					return p.at("Bad Unicode escape")
				}
				p.pos++
			}
		} else {
			if !strings.ContainsRune(`"\/bfnrt`, rune(c)) {
				return p.at("Bad escaped character")
			}
			p.pos++
		}
	}
	return p.at("Unterminated string")
}
