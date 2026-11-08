package tui

import (
	"errors"
	"unicode/utf16"
)

// Ports packages/chord/src/node/tui/filter.ts. The terminal's output filter (slice 11 §4; p2's `tui-filter.ts`): the only path
// from the sandbox to the real terminal. A frame's text and the escape sequences a terminal UI needs to draw pass (cursor
// moves, erasing, colours, the alternate screen, bracketed paste, synchronized output, mouse reporting); every other sequence
// is dropped, so sandboxed code cannot set the clipboard (OSC 52), retitle or reconfigure the terminal, or smuggle a sequence
// across frames: an unterminated one ends with its frame.
//
// Forced Go difference. Pi walks the frame's UTF-16 code units; so does this (a sequence's introducer that is half of a
// surrogate pair is consumed alone, as there), and the kept units are written as text, a lone surrogate as U+FFFD, as Node
// writes it.

// MaxFrame is the longest frame the sandbox may write.
const MaxFrame = 64 * 1024

const esc = 0x1b

// modes are the DEC private modes output may set: cursor visibility, bracketed paste, sync, alternate screen, autowrap, mouse.
var modes = map[string]bool{"25": true, "2004": true, "2026": true, "1049": true, "7": true, "1000": true, "1002": true, "1006": true, "1004": true}

// ErrFrameTooLarge is a frame over MaxFrame.
var ErrFrameTooLarge = errors.New("terminal: a frame over 64 KiB")

// exceedsUnits is whether s is longer than limit UTF-16 code units (JavaScript's `length`, which Pi's limits count). A string of
// at most limit bytes has at most limit units (a unit is never more than a byte of its UTF-8 form, as an astral character's two
// units are four bytes), so only a longer one is counted.
func exceedsUnits(s string, limit int) bool {
	if len(s) <= limit {
		return false
	}
	units := 0
	for _, r := range s {
		units++
		if r >= 0x10000 {
			units++
		}
		if units > limit {
			return true
		}
	}
	return false
}

// FilterOutput is output with only the allowed sequences; it fails for a frame over MaxFrame.
func FilterOutput(output string) (string, error) {
	if exceedsUnits(output, MaxFrame) {
		return "", ErrFrameTooLarge
	}
	units := utf16.Encode([]rune(output))
	kept := make([]uint16, 0, len(units))
	for index := 0; index < len(units); index++ {
		code := units[index]
		if code == esc || (code >= 0x80 && code <= 0x9f) {
			next, sequence := escapeAt(units, index)
			index = next
			kept = append(kept, sequence...)
			continue
		}
		if code == 9 || code == 10 || code == 13 || (code >= 0x20 && code != 0x7f) {
			kept = append(kept, code)
		}
	}
	return string(utf16.Decode(kept)), nil
}

func isDigit(unit uint16) bool { return unit >= '0' && unit <= '9' }

// escapeAt is the escape at start: the index of its last character, and the sequence kept (none when dropped).
func escapeAt(output []uint16, start int) (int, []uint16) {
	index := start
	code := output[index]
	var introducer uint16
	if code == esc {
		index++
		if index < len(output) {
			introducer = output[index]
		}
	} else {
		introducer = output[index]
	}
	if introducer == '[' || code == 0x9b {
		body := index + 1
		for index+1 < len(output) && output[index+1] >= 0x20 && output[index+1] <= 0x3f {
			index++
		}
		index++
		var final uint16
		if index < len(output) {
			final = output[index]
		}
		text := output[min(body, len(output)):min(index, len(output))]
		if code == esc && final != 0 && allowedCsi(text, final) {
			sequence := append([]uint16{esc, '['}, text...)
			return index, append(sequence, final)
		}
		return index, nil
	}
	if (introducer != 0 && (introducer == ']' || introducer == 'P' || introducer == 'X' || introducer == '^' || introducer == '_')) ||
		code == 0x90 || code == 0x98 || code == 0x9d || code == 0x9e || code == 0x9f {
		// A control string (OSC, DCS, ...): dropped up to its terminator, or the end of the frame.
		for index++; index < len(output); index++ {
			if output[index] == 0x9c || output[index] == 7 {
				break
			}
			if output[index] == esc && index+1 < len(output) && output[index+1] == '\\' {
				return index + 1, nil
			}
		}
		return index, nil
	}
	// Any other ESC sequence with its intermediates (charset selection, ...): dropped.
	for index < len(output) && output[index] >= 0x20 && output[index] <= 0x2f {
		index++
	}
	return index, nil
}

// hasFiveDigits is whether body holds a run of five digits (a number no terminal needs).
func hasFiveDigits(body []uint16) bool {
	run := 0
	for _, unit := range body {
		if isDigit(unit) {
			if run++; run >= 5 {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}

// allowedCsi is whether the CSI sequence with parameters body and final may reach the terminal.
func allowedCsi(body []uint16, final uint16) bool {
	if len(body) > 128 {
		return false
	}
	text := string(utf16.Decode(body))
	if final == 'h' || final == 'l' {
		return len(text) > 0 && text[0] == '?' && modes[text[1:]]
	}
	if final == 'm' {
		for _, unit := range body {
			if !isDigit(unit) && unit != ';' && unit != ':' {
				return false
			}
		}
		return !hasFiveDigits(body)
	}
	for _, unit := range body {
		if !isDigit(unit) && unit != ';' {
			return false
		}
	}
	if hasFiveDigits(body) {
		return false
	}
	semicolons := 0
	for _, unit := range body {
		if unit == ';' {
			semicolons++
		}
	}
	switch final {
	case 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'f', 'd':
		limit := 0
		if final == 'H' || final == 'f' {
			limit = 1
		}
		return semicolons <= limit
	case 'J', 'K':
		return text == "" || (len(text) == 1 && text[0] >= '0' && text[0] <= '2')
	case 'r':
		return rowsRegion(text)
	case 's', 'u':
		return text == ""
	}
	return false
}

// rowsRegion is `^(?:\d{1,4};\d{1,4})?$`.
func rowsRegion(text string) bool {
	if text == "" {
		return true
	}
	semicolon := -1
	for i := range len(text) {
		if text[i] == ';' {
			semicolon = i
		}
	}
	if semicolon < 1 || semicolon > 4 || len(text)-semicolon-1 < 1 || len(text)-semicolon-1 > 4 {
		return false
	}
	for i := range len(text) {
		if i != semicolon && (text[i] < '0' || text[i] > '9') {
			return false
		}
	}
	return true
}
