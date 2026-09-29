package tui

// Ports packages/tui/src/tui-main-screen.ts (BoundedTerminalWriter).

import (
	"encoding/binary"
	"strings"
	"unicode/utf8"
)

const maxRenderWriteChars = 1024 * 1024

// boundedTerminalWriter bounds each buffered render write in UTF-16 units, the upstream JavaScript string unit. A Unicode code point is never split across writes.
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
			r, n := utf8.DecodeRuneInString(value[end:])
			size = n
			if r > 0xffff {
				cost = 2
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

func (w *boundedTerminalWriter) WriteString(value string) { w.append(value) }
func (w *boundedTerminalWriter) flush() {
	if w.buffer.Len() == 0 {
		return
	}
	w.write(w.buffer.String())
	w.buffer.Reset()
	w.bufferChars = 0
}
