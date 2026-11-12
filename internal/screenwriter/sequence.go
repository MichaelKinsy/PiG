// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package screenwriter

import (
	"encoding/binary"
	"math/bits"
	"strings"
	"unsafe"
)

const (
	lowBits  = 0x0101010101010101
	highBits = 0x8080808080808080
)

// bytesOf is s as bytes, not to be written to: for reading it eight bytes at a time.
func bytesOf(s string) []byte { return unsafe.Slice(unsafe.StringData(s), len(s)) } //nolint:gosec // G103: the string's own bytes, read and never written.

// controls marks, with its high bit, the lowest byte of x that is a control character (below 0x20, or 0x7f); the bytes above that one
// may be marked wrongly.
func controls(x uint64) uint64 {
	del := x ^ lowBits*0x7f
	return ((x-lowBits*0x20)&^x | (del-lowBits)&^del) & highBits
}

// textEnd is the first index from i on at which data holds a control character, len(data) when there is none.
func textEnd(data string, i int) int {
	for b := bytesOf(data); i+8 <= len(b); i += 8 {
		if m := controls(binary.LittleEndian.Uint64(b[i:])); m != 0 {
			return i + bits.TrailingZeros64(m)/8
		}
	}
	for i < len(data) && data[i] >= 0x20 && data[i] != 0x7f {
		i++
	}
	return i
}

// asciiEnd is the first index from i on at which data holds a byte that is not printable ASCII, len(data) when there is none.
func asciiEnd(data string, i int) int {
	for b := bytesOf(data); i+8 <= len(b); i += 8 {
		x := binary.LittleEndian.Uint64(b[i:])
		if m := controls(x) | x&highBits; m != 0 {
			return i + bits.TrailingZeros64(m)/8
		}
	}
	for i < len(data) && data[i]-0x20 < 0x5f {
		i++
	}
	return i
}

// The kinds of token a write is made of.
const (
	tokenText   = iota // printable bytes up to the next control byte
	tokenC0            // one control byte other than ESC
	tokenCSI           // ESC [ parameters intermediates final
	tokenOSC           // ESC ] ... BEL or ST
	tokenString        // DCS, APC, PM or SOS up to ST
	tokenEscape        // ESC intermediates final
	tokenBad           // a sequence that does not end in the write
)

// nextToken is the kind and the end of the token that starts at data[i].
func nextToken(data string, i int) (kind, end int) {
	c := data[i]
	if c != 0x1b {
		if c < 0x20 || c == 0x7f {
			return tokenC0, i + 1
		}
		return tokenText, textEnd(data, i+1)
	}
	if i+1 >= len(data) {
		return tokenBad, len(data)
	}
	switch data[i+1] {
	case '[':
		j := i + 2
		for j < len(data) && data[j] >= 0x30 && data[j] <= 0x3f {
			j++
		}
		for j < len(data) && data[j] >= 0x20 && data[j] <= 0x2f {
			j++
		}
		if j < len(data) && data[j] >= 0x40 && data[j] <= 0x7e {
			return tokenCSI, j + 1
		}
		return tokenBad, len(data)
	case ']':
		for j := i + 2; j < len(data); j++ {
			if data[j] == 0x07 {
				return tokenOSC, j + 1
			}
			if data[j] == 0x1b {
				if j+1 < len(data) && data[j+1] == '\\' {
					return tokenOSC, j + 2
				}
				return tokenBad, len(data)
			}
		}
		return tokenBad, len(data)
	case 'P', '_', '^', 'X':
		for j := i + 2; j+1 < len(data); j++ {
			if data[j] == 0x1b && data[j+1] == '\\' {
				return tokenString, j + 2
			}
		}
		return tokenBad, len(data)
	}
	j := i + 1
	for j < len(data) && data[j] >= 0x20 && data[j] <= 0x2f {
		j++
	}
	if j < len(data) && data[j] >= 0x30 && data[j] <= 0x7e {
		return tokenEscape, j + 1
	}
	return tokenBad, len(data)
}

// lineEnd is where the line a frame wrote from body[i] on ends: at the first control character, or the first sequence that is not an
// SGR sequence or a hyperlink.
func lineEnd(body string, i int) int {
	for {
		if i = textEnd(body, i); i+2 >= len(body) || body[i] != 0x1b {
			return i
		}
		switch body[i+1] {
		case '[':
			if body[i+2] == '>' || body[i+2] == '?' {
				return i
			}
			j := i + 2
			for j < len(body) && body[j]-0x30 < 0x10 {
				j++
			}
			for j < len(body) && body[j]-0x20 < 0x10 {
				j++
			}
			if j == len(body) || body[j] != 'm' {
				return i
			}
			i = j + 1
		case ']':
			kind, end := nextToken(body, i)
			if kind != tokenOSC || !strings.HasPrefix(body[i+2:], "8;") {
				return i
			}
			i = end
		default:
			return i
		}
	}
}

// twoParams reads the two parameters of a cursor position or a scroll region, each 1 when left out; ok is false for any other form.
func twoParams(params string) (a, b int, ok bool) {
	a, b = 1, 1
	n, digits, field := 0, false, 0
	for i := 0; i <= len(params); i++ {
		if i < len(params) && params[i] >= '0' && params[i] <= '9' {
			n = n*10 + int(params[i]-'0')
			digits = true
			if n > 1<<15 {
				return 0, 0, false
			}
			continue
		}
		if i < len(params) && params[i] != ';' {
			return 0, 0, false
		}
		if digits && n > 0 {
			if field == 0 {
				a = n
			} else {
				b = n
			}
		}
		field++
		if field > 2 {
			return 0, 0, false
		}
		n, digits = 0, false
	}
	return a, b, true
}

// pen is what a cell is drawn with, counted from the default pen.
type pen struct {
	fg, bg uint32 // 0 default; kind<<24 | value: kind 1 a basic color's SGR code, 2 an indexed color, 3 red<<16|green<<8|blue
	attrs  uint16
}

const (
	attrBold uint16 = 1 << iota
	attrDim
	attrItalic
	attrUnderline
	attrBlink
	attrInverse
	attrHidden
	attrStrike
	attrOverline
)

// applySGR applies the parameters of one SGR sequence and reports false for a form outside the set every terminal reads alike: the
// semicolon forms of 0-9, 22-29, 30-39, 40-49, 53, 55, 90-97, 100-107, 38;5;n, 38;2;r;g;b and the same two for 48. A colon form, an
// underline color or style (58, 4:n) and a double underline (21) are not in it.
func (p *pen) applySGR(params string) bool {
	if params == "" || params == "0" {
		*p = pen{}
		return true
	}
	var list [12]int
	n, value := 0, 0
	for i := 0; i <= len(params); i++ {
		if i < len(params) && params[i] >= '0' && params[i] <= '9' {
			value = value*10 + int(params[i]-'0')
			if value > 1<<16 {
				return false
			}
			continue
		}
		if i < len(params) && params[i] != ';' {
			return false
		}
		if n == len(list) {
			return false
		}
		list[n] = value
		n++
		value = 0
	}
	for i := 0; i < n; i++ {
		v := list[i]
		switch {
		case v == 0:
			*p = pen{}
		case v == 1:
			p.attrs |= attrBold
		case v == 2:
			p.attrs |= attrDim
		case v == 3:
			p.attrs |= attrItalic
		case v == 4:
			p.attrs |= attrUnderline
		case v == 5:
			p.attrs |= attrBlink
		case v == 7:
			p.attrs |= attrInverse
		case v == 8:
			p.attrs |= attrHidden
		case v == 9:
			p.attrs |= attrStrike
		case v == 22:
			p.attrs &^= attrBold | attrDim
		case v == 23:
			p.attrs &^= attrItalic
		case v == 24:
			p.attrs &^= attrUnderline
		case v == 25:
			p.attrs &^= attrBlink
		case v == 27:
			p.attrs &^= attrInverse
		case v == 28:
			p.attrs &^= attrHidden
		case v == 29:
			p.attrs &^= attrStrike
		case v == 53:
			p.attrs |= attrOverline
		case v == 55:
			p.attrs &^= attrOverline
		case v >= 30 && v <= 37 || v >= 90 && v <= 97:
			p.fg = 1<<24 | uint32(v)
		case v == 39:
			p.fg = 0
		case v >= 40 && v <= 47 || v >= 100 && v <= 107:
			p.bg = 1<<24 | uint32(v)
		case v == 49:
			p.bg = 0
		case v == 38 || v == 48:
			var color uint32
			switch {
			case i+2 < n && list[i+1] == 5 && list[i+2] <= 255:
				color = 2<<24 | uint32(list[i+2])
				i += 2
			case i+4 < n && list[i+1] == 2 && list[i+2] <= 255 && list[i+3] <= 255 && list[i+4] <= 255:
				color = 3<<24 | uint32(list[i+2])<<16 | uint32(list[i+3])<<8 | uint32(list[i+4])
				i += 4
			default:
				return false
			}
			if v == 38 {
				p.fg = color
			} else {
				p.bg = color
			}
		default:
			return false
		}
	}
	return true
}

// neutralMode reports whether mode is a private mode that changes nothing the writer keeps of the screen: cursor keys, autowrap (no
// row the writer writes ends past the row the frame wrote; outside reports what a write says of it, and frames are translated only
// while it is known to be off), cursor blink, mouse reporting and its encodings, focus
// reports, bracketed paste, synchronized updates, grapheme clustering reports, color-scheme reports and in-band resize.
func neutralMode(mode string) bool {
	switch mode {
	case "1", "7", "12", "1000", "1002", "1003", "1004", "1005", "1006", "1015", "1016", "2004", "2026", "2027", "2031", "2048":
		return true
	}
	return false
}

// neutralOSC are the operating-system commands that change nothing the writer keeps: titles, the palette and the default colors (a
// cell keeps the color number it was written with), notifications and progress, the clipboard, program status, shell-integration marks
// and the working directory.
var neutralOSC = [...]string{"0;", "1;", "2;", "4;", "7;", "9;", "10;", "11;", "12;", "52;", "133;", "7501;"}

// outside classifies a write that is not a frame. screen is whether the alternate screen is shown after it (shown before it), cursor is
// +1 or -1 when it shows or hides the cursor, and forget is whether it holds anything that may change the cells, the pen, the cursor's
// position or the scroll region: everything that is not in the tables above, a query, a keyboard-protocol sequence or a keypad mode.
// wrap is what the write leaves known of autowrap: +1 when it is off after it (CSI ? 7 l came last), -1 when it is not known to be
// off (autowrap turned on; a switch to or from an alternate screen, modes 47, 1047 and 1049; saved private modes restored; a reset
// of the terminal, DECSTR or RIS), 0 when the write says nothing of it. Only those sequences change autowrap, and outside reads each
// of them wherever it stands in a write. What it does not recognise is passed through whole and is none of them: it makes the
// writer forget the rows, and leaves what is known of autowrap as it was.
func outside(data string, shown bool) (screen bool, cursor int, forget bool, wrap int) {
	screen = shown
	for i := 0; i < len(data); {
		kind, end := nextToken(data, i)
		seq := data[i:end]
		i = end
		switch kind {
		case tokenCSI:
			final := seq[len(seq)-1]
			switch {
			case len(seq) > 3 && seq[2] == '?' && (final == 'h' || final == 'l'):
				for mode := range strings.SplitSeq(seq[3:len(seq)-1], ";") {
					switch {
					case mode == "1049":
						screen = final == 'h'
						forget = true
						wrap = -1
					case mode == "47" || mode == "1047":
						forget = true
						wrap = -1
					case mode == "7":
						wrap = -1
						if final == 'l' {
							wrap = 1
						}
					case mode == "25":
						cursor = -1
						if final == 'h' {
							cursor = 1
						}
					case !neutralMode(mode):
						forget = true
					}
				}
			case len(seq) > 3 && (seq[2] == '>' || seq[2] == '<' || seq[2] == '=' || seq[2] == '?') && (final == 'u' || final == 'm' || final == 'c' || final == 'q' || final == 'p' || final == 'n'):
				// The kitty keyboard protocol, modifyOtherKeys, device attributes, the terminal's name and mode and status queries.
			case len(seq) > 2 && seq[2] == '?' && final == 'r', seq == "\x1b[!p":
				// Saved private modes restored, and the soft reset: autowrap may be on after either.
				forget = true
				wrap = -1
			case final == 'c' || final == 'n' || final == 't' || final == 'q':
				// Device attributes, status reports, window reports and the cursor's shape.
			default:
				forget = true
			}
		case tokenOSC:
			neutral := false
			for _, prefix := range neutralOSC {
				neutral = neutral || strings.HasPrefix(seq[2:], prefix)
			}
			forget = forget || !neutral
		case tokenC0:
			forget = forget || seq != "\x07"
		case tokenEscape:
			forget = forget || seq != "\x1b=" && seq != "\x1b>"
			if seq == "\x1bc" {
				wrap = -1
			}
		default:
			forget = true
		}
	}
	return screen, cursor, forget, wrap
}
