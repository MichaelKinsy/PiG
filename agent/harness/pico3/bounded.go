package pico3

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

// Bounded collects bytes, retaining a prefix or suffix constrained by both a
// byte budget and a newline budget, and accounts for every discarded byte and
// newline.
type Bounded struct {
	bytes        []byte
	maxBytes     int
	maxLines     int
	retain       string
	DroppedBytes int
	DroppedLines int
	Total        int
}

// NewBounded creates a collector; retain is "head" or "tail".
func NewBounded(maxBytes, maxLines int, retain string) *Bounded {
	return &Bounded{maxBytes: max(0, maxBytes), maxLines: max(0, maxLines), retain: retain}
}

// Push adds one chunk.
func (bounded *Bounded) Push(chunk []byte) {
	bounded.Total += len(chunk)
	if len(chunk) == 0 {
		return
	}
	if bounded.maxBytes == 0 || bounded.maxLines == 0 {
		bounded.drop(chunk)
		return
	}
	if bounded.retain == "head" {
		bounded.pushHead(chunk)
		return
	}
	bounded.pushTail(chunk)
}

func (bounded *Bounded) pushHead(chunk []byte) {
	remainingBytes := bounded.maxBytes - len(bounded.bytes)
	remainingLines := bounded.maxLines - bytes.Count(bounded.bytes, []byte{'\n'})
	if remainingBytes <= 0 || remainingLines <= 0 {
		bounded.drop(chunk)
		return
	}
	take := min(len(chunk), remainingBytes)
	lines := 0
	for index := 0; index < take; index++ {
		if chunk[index] != '\n' {
			continue
		}
		lines++
		if lines == remainingLines {
			take = index + 1
			break
		}
	}
	bounded.bytes = append(bounded.bytes, chunk[:take]...)
	bounded.drop(chunk[take:])
}

func (bounded *Bounded) pushTail(chunk []byte) {
	incomingStart := tailStart(chunk, bounded.maxBytes, bounded.maxLines)
	bounded.drop(chunk[:incomingStart])
	combined := append(append([]byte{}, bounded.bytes...), chunk[incomingStart:]...)
	start := tailStart(combined, bounded.maxBytes, bounded.maxLines)
	bounded.drop(combined[:start])
	bounded.bytes = combined[start:]
}

func (bounded *Bounded) drop(dropped []byte) {
	bounded.DroppedBytes += len(dropped)
	bounded.DroppedLines += bytes.Count(dropped, []byte{'\n'})
}

// Dropped returns the discarded byte count.
func (bounded *Bounded) Dropped() int { return bounded.DroppedBytes }

// Text decodes the retained bytes as UTF-8, replacing invalid sequences.
func (bounded *Bounded) Text() string { return decodeUTF8Lossy(bounded.bytes) }

// decodeUTF8Lossy decodes like the WHATWG TextDecoder: each maximal invalid
// subpart becomes one U+FFFD.
func decodeUTF8Lossy(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	var out strings.Builder
	for index := 0; index < len(data); {
		r, size := utf8.DecodeRune(data[index:])
		if r != utf8.RuneError || size > 1 {
			out.WriteRune(r)
			index += size
			continue
		}
		index += maximalSubpart(data[index:])
		out.WriteRune(utf8.RuneError)
	}
	return out.String()
}

// maximalSubpart returns the length of the invalid prefix that one U+FFFD
// replaces.
func maximalSubpart(data []byte) int {
	need, low, high := leadShape(data[0])
	if need == 0 {
		return 1
	}
	length := 1
	for offset := 0; offset < need && length < len(data); offset++ {
		next := data[length]
		if offset == 0 && (next < low || next > high) {
			break
		}
		if offset > 0 && (next < 0x80 || next > 0xBF) {
			break
		}
		length++
	}
	return length
}

// leadShape returns the continuation count and the valid range of the first
// continuation byte for a UTF-8 lead byte; need is zero for an invalid lead.
func leadShape(lead byte) (need int, low, high byte) {
	switch {
	case lead >= 0xC2 && lead <= 0xDF:
		return 1, 0x80, 0xBF
	case lead == 0xE0:
		return 2, 0xA0, 0xBF
	case lead == 0xED:
		return 2, 0x80, 0x9F
	case lead >= 0xE1 && lead <= 0xEF:
		return 2, 0x80, 0xBF
	case lead == 0xF0:
		return 3, 0x90, 0xBF
	case lead >= 0xF1 && lead <= 0xF3:
		return 3, 0x80, 0xBF
	case lead == 0xF4:
		return 3, 0x80, 0x8F
	default:
		return 0, 0, 0
	}
}

func tailStart(data []byte, maxBytes, maxLines int) int {
	start := max(0, len(data)-maxBytes)
	excessLines := bytes.Count(data[start:], []byte{'\n'}) - maxLines
	for ; start < len(data) && excessLines > 0; start++ {
		if data[start] == '\n' {
			excessLines--
		}
	}
	return start
}
