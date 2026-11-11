package tui

// Ports packages/tui/src/tui-main-screen.ts (BoundedTerminalWriter).

import (
	"encoding/binary"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

const maxRenderWriteChars = 1024 * 1024

// boundedTerminalWriter bounds each buffered render write in UTF-16 units, the upstream JavaScript string unit. A Unicode code point is never split across writes, and an unpaired surrogate (three WTF-8 bytes) counts as one unit.
type boundedTerminalWriter struct {
	buffer      strings.Builder
	bufferChars int
	write       func(string)
}

func (w *boundedTerminalWriter) append(value string) {
	for value != "" {
		capacity := maxRenderWriteChars - w.bufferChars
		if capacity == 0 {
			w.flush()
			continue
		}
		end, units := renderPrefixWithinUTF16(value, capacity)
		if end == 0 {
			w.flush()
			continue
		}
		w.buffer.WriteString(value[:end])
		w.bufferChars += units
		value = value[end:]
		if w.bufferChars == maxRenderWriteChars {
			w.flush()
		}
	}
}

func renderPrefixWithinUTF16(value string, capacity int) (end, units int) {
	for end < len(value) && units < capacity {
		if len(value)-end >= 8 && capacity-units >= 8 && binary.LittleEndian.Uint64([]byte(value[end:end+8]))&0x8080808080808080 == 0 {
			end += 8
			units += 8
			continue
		}
		size, cost := 1, 1
		if value[end] >= utf8.RuneSelf {
			r, n := jsstring.DecodeRuneInString(value[end:])
			size = n
			if r > 0xffff {
				cost = 2
			} else if n == 1 {
				size, cost = surrogateUnitsAt(value[end:])
			}
		}
		if units+cost > capacity {
			break
		}
		end += size
		units += cost
	}
	return end, units
}

// surrogateUnitsAt is the byte length and UTF-16 length of the UTF-16 surrogate that value starts with, which a PiG string carries as WTF-8
// (ED A0..BF 80..BF, three bytes of invalid UTF-8): one unit, as Pi's buffer.length counts it, and two (six bytes) for a high surrogate
// directly followed by a low one, which Pi never splits. Any other byte is one byte and one unit. A lone surrogate counted as three bytes of
// invalid UTF-8 could be cut between them, and each part would be written as its own U+FFFD where Pi writes one.
func surrogateUnitsAt(value string) (size, cost int) {
	if !wtf8SurrogateAt(value) {
		return 1, 1
	}
	//portlint:allow utf16units value starts with one three-byte WTF-8 surrogate: the second byte below 0xB0 makes it a high surrogate, and the surrogate after it (bytes 3..5) is low from 0xB0; these index the encoding, not JS offsets
	if value[1] < 0xb0 && wtf8SurrogateAt(value[3:]) && value[4] >= 0xb0 {
		return 6, 2
	}
	return 3, 1
}

// wtf8SurrogateAt reports whether value starts with the three bytes of a UTF-16 surrogate, U+D800 to U+DFFF.
func wtf8SurrogateAt(value string) bool {
	//portlint:allow utf16units this parses the WTF-8 encoding of one surrogate (ED, A0..BF, 80..BF), a three-byte sequence the writer counts as one UTF-16 unit, so the byte checks are the encoding, not JS offsets
	return len(value) >= 3 && value[0] == 0xed && value[1]&0xe0 == 0xa0 && value[2]&0xc0 == 0x80
}

func (w *boundedTerminalWriter) WriteString(value string) { w.append(value) }

// grow sizes the buffer for about n more bytes in one allocation, within the bound of one write.
func (w *boundedTerminalWriter) grow(n int) { w.buffer.Grow(min(n, maxRenderWriteChars-w.bufferChars)) }

// discard drops what was buffered and not written.
func (w *boundedTerminalWriter) discard() {
	w.buffer.Reset()
	w.bufferChars = 0
}

func (w *boundedTerminalWriter) flush() {
	if w.buffer.Len() == 0 {
		return
	}
	w.write(w.buffer.String())
	w.buffer.Reset()
	w.bufferChars = 0
}
