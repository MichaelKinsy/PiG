package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The block lexer reads its source as code points and rebuilds the text of a token from them (every regular expression match is a string of
// code points), so a lone UTF-16 surrogate, which a PiG string spells as three invalid WTF-8 bytes, would come back as three U+FFFD. Pi's
// marked works on UTF-16 units and keeps the unit; the rows are wrapped and clipped with it nonprinting and encoded when the terminal write
// runs, so a replacement character, one cell wide, moves a row's wrap. A text with a lone surrogate is therefore lexed with each unit
// standing in as one code point of plane 16's private use area, U+10F800 (unit U+D800) to U+10FFFF (unit U+DFFF), and the units are put
// back into the tokens before they render.
const loneSurrogateStandIn = 0x10F800

// hideLoneSurrogates is text with each lone surrogate replaced by its stand-in, and whether there was any. A text that already holds a
// stand-in is left as it is: it is not a text the replacement can be undone on.
func hideLoneSurrogates(text string) (string, bool) {
	if utf8.ValidString(text) {
		return text, false
	}
	var out strings.Builder
	out.Grow(len(text))
	found := false
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		switch {
		case r >= loneSurrogateStandIn:
			return text, false
		case r == utf8.RuneError && size == 1:
			if unit, ok := loneSurrogateAt(text[i:]); ok {
				out.WriteRune(rune(loneSurrogateStandIn + int(unit-0xD800)))
				i += 3
				found = true
				continue
			}
			out.WriteString(text[i : i+size])
		default:
			out.WriteString(text[i : i+size])
		}
		i += size
	}
	return out.String(), found
}

// loneSurrogateAt is the UTF-16 unit of the three WTF-8 bytes at the start of text, when they spell a surrogate.
func loneSurrogateAt(text string) (uint16, bool) {
	if len(text) < 3 || text[0] != 0xED || text[1] < 0xA0 || text[1] > 0xBF || text[2] < 0x80 || text[2] > 0xBF {
		return 0, false
	}
	return 0xD000 | uint16(text[1]&0x3F)<<6 | uint16(text[2]&0x3F), true
}

// showLoneSurrogates is text with every stand-in replaced by the surrogate it stands for.
func showLoneSurrogates(text string) string {
	if !strings.ContainsFunc(text, func(r rune) bool { return r >= loneSurrogateStandIn }) {
		return text
	}
	var out strings.Builder
	out.Grow(len(text))
	for _, r := range text {
		if r >= loneSurrogateStandIn {
			out.WriteString(jsstring.FromUTF16([]uint16{uint16(0xD800 + int(r) - loneSurrogateStandIn)}))
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// showLoneSurrogatesIn restores the surrogates in the text of every token of tokens and of the tokens inside them.
func showLoneSurrogatesIn(tokens []*markedBlock) {
	entry := func(e *markedInlineEntry) {
		if e != nil {
			e.src = showLoneSurrogates(e.src)
		}
	}
	for _, token := range tokens {
		token.raw, token.text, token.lang, token.tag = showLoneSurrogates(token.raw), showLoneSurrogates(token.text), showLoneSurrogates(token.lang), showLoneSurrogates(token.tag)
		entry(token.entry)
		for _, cell := range token.header {
			entry(cell)
		}
		for _, row := range token.rows {
			for _, cell := range row {
				entry(cell)
			}
		}
		showLoneSurrogatesIn(token.tokens)
		showLoneSurrogatesIn(token.items)
	}
}
