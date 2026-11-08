package terminals

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports apps/p2/plugins/terminals/plain.ts. Terminal output for the agent (terminal host): a bounded ring of each
// terminal's raw output at absolute offsets, and output turned into plain text the way a terminal would show it line by
// line, without escape sequences.

// OutputRing keeps the last `capacity` bytes of a terminal's output, addressed by absolute offsets since it started.
type OutputRing struct {
	buffer []byte
	// end is the bytes written in total: the absolute offset after the newest byte.
	end int64
}

// NewOutputRing is a ring of capacity bytes.
func NewOutputRing(capacity int) *OutputRing { return &OutputRing{buffer: make([]byte, capacity)} }

// End is the offset after the newest byte.
func (ring *OutputRing) End() int64 { return ring.end }

// Start is the offset of the oldest byte still kept.
func (ring *OutputRing) Start() int64 { return max(0, ring.end-int64(len(ring.buffer))) }

// Append adds output.
func (ring *OutputRing) Append(bytes []byte) {
	capacity := int64(len(ring.buffer))
	// Only the newest `capacity` bytes of a large chunk can survive.
	kept := bytes
	if int64(len(bytes)) > capacity {
		kept = bytes[int64(len(bytes))-capacity:]
	}
	lost := int64(len(bytes) - len(kept))
	at := (ring.end + lost) % capacity
	first := min(int64(len(kept)), capacity-at)
	copy(ring.buffer[at:], kept[:first])
	if first < int64(len(kept)) {
		copy(ring.buffer, kept[first:])
	}
	ring.end += int64(len(bytes))
}

// Since is the bytes from `from` to the end; skipped counts those after `from` the ring no longer has.
func (ring *OutputRing) Since(from int64) (bytes []byte, skipped int64) {
	begin := min(max(from, ring.Start()), ring.end)
	skipped = max(0, ring.Start()-from)
	length := ring.end - begin
	capacity := int64(len(ring.buffer))
	at := begin % capacity
	bytes = make([]byte, length)
	first := min(length, capacity-at)
	copy(bytes, ring.buffer[at:at+first])
	if first < length {
		copy(bytes[first:], ring.buffer[:length-first])
	}
	return bytes, skipped
}

// decodeOutput is `new TextDecoder().decode(bytes)`: each maximal ill-formed subpart one replacement character, a
// leading byte order mark dropped.
func decodeOutput(bytes []byte) string {
	text := jsstring.FromUTF8(bytes)
	return strings.TrimPrefix(text, "\ufeff")
}

const (
	esc = '\x1b'
	bel = '\x07'
)

// PlainText is terminal output as plain text: escape sequences removed (CSI, OSC, DCS, SOS, PM, APC, two- and
// three-byte escapes, C1 controls), and each line as it would end up on screen: a carriage return starts the line over
// (so a progress bar keeps its final state), a backspace steps back, erase-in-line clears. Tabs and line feeds stay;
// other control characters go. Trailing spaces of finished lines are trimmed.
func PlainText(input string) string {
	var lines []string
	var line []rune
	column := 0
	put := func(char rune) {
		if column < len(line) {
			line[column] = char
		} else {
			line = append(line, char)
		}
		column++
	}
	endLine := func() {
		lines = append(lines, jsstring.TrimEnd(string(line)))
		line = nil
		column = 0
	}
	chars := []rune(input)
	for index := 0; index < len(chars); index++ {
		char := chars[index]
		switch {
		case char == esc:
			index = skipEscape(chars, index, func(final rune, params string) {
				// Erase in line: 0 (or none) to the end, 1 to the start, 2 the whole line.
				if final != 'K' {
					return
				}
				switch params {
				case "", "0":
					line = line[:column]
				case "1":
					for at := 0; at < column && at < len(line); at++ {
						line[at] = ' '
					}
				case "2":
					line = make([]rune, column)
					for at := range line {
						line[at] = ' '
					}
				}
			}, false)
		case char == '\n':
			endLine()
		case char == '\r':
			column = 0
		case char == '\b':
			column = max(0, column-1)
		case char == '\t':
			put(char)
		case char < 0x20 || char == 0x7f:
		case char >= 0x80 && char < 0xa0:
			// C1 controls: a CSI, OSC or string introducer starts a sequence like its escape form; others carry nothing.
			if char == 0x9b || char == 0x9d || char == 0x90 || char == 0x98 || char == 0x9e || char == 0x9f {
				chars[index] = 0x40 + (char - 0x80)
				index = skipEscape(chars, index-1, func(rune, string) {}, true)
			}
		default:
			put(char)
		}
	}
	// The last line is where the cursor is (a prompt): its trailing spaces stay.
	lines = append(lines, string(line))
	return strings.Join(lines, "\n")
}

// skipEscape skips the escape sequence starting at chars[index] (ESC) and returns the index of its last character; onCsi
// hears each CSI's final character and parameters. introduced: the ESC is implied (a C1 control at index + 1).
func skipEscape(chars []rune, index int, onCsi func(final rune, params string), introduced bool) int {
	if index+1 >= len(chars) {
		return index
	}
	kind := chars[index+1]
	if kind == '[' {
		at := index + 2
		var params []rune
		for ; at < len(chars); at++ {
			if chars[at] >= 0x40 && chars[at] <= 0x7e {
				onCsi(chars[at], string(params))
				return at
			}
			params = append(params, chars[at])
		}
		return len(chars) - 1
	}
	if kind == ']' || kind == 'P' || kind == 'X' || kind == '^' || kind == '_' {
		// A string ends with ST (ESC \ or 0x9c); OSC also with BEL.
		for at := index + 2; at < len(chars); at++ {
			char := chars[at]
			if char == 0x9c || (kind == ']' && char == bel) {
				return at
			}
			if char == esc && at+1 < len(chars) && chars[at+1] == '\\' {
				return at + 1
			}
		}
		return len(chars) - 1
	}
	if introduced {
		return index + 1
	}
	// ESC with intermediates (charset selection `ESC ( B`) then a final character; or ESC and one character.
	at := index + 1
	for at < len(chars) {
		if chars[at] < 0x20 || chars[at] > 0x2f {
			break
		}
		at++
	}
	return min(at, len(chars)-1)
}
