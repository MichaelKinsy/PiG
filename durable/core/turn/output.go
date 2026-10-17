// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import "strings"

// Bounded tool output (src/harness/output.ts): the retention limits of one call, the exact slice of a text within them,
// the sanitizer, and the running OutputBuffer a tool call reports into. Byte-level; a JavaScript string is its UTF-8 text.

// OutputLimits are the retention limits of one tool's output.
type OutputLimits struct {
	MaxBytes int
	MaxLines int
	// Retain is "head" or "tail".
	Retain string
}

// BoundedOutput is retained output and what the limits dropped.
type BoundedOutput struct {
	Text         string
	DroppedBytes int
	DroppedLines int
}

// OutputSlice is an exact slice of the input within the limits, and what it left out.
type OutputSlice struct {
	Text         string
	Bytes        int
	DroppedBytes int
	DroppedLines int
}

// SanitizeOutput removes control characters that break display and transcripts (U+0000-U+0008, U+000B-U+001F and
// U+FFF9-U+FFFB); tabs and newlines stay.
func SanitizeOutput(text string) string {
	clean := true
	for _, r := range text {
		if invalidOutput(r) {
			clean = false
			break
		}
	}
	if clean {
		return text
	}
	var b strings.Builder
	for _, r := range text {
		if !invalidOutput(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func invalidOutput(r rune) bool {
	return (r >= 0x00 && r <= 0x08) || (r >= 0x0b && r <= 0x1f) || (r >= 0xfff9 && r <= 0xfffb)
}

// BoundOutput is boundOutput: text bounded to whole lines within the limits, the first lines for head, the last lines for
// tail. The result is an exact slice, trailing newline included. A single line longer than MaxBytes is cut at the byte limit
// on a character boundary.
func BoundOutput(text string, limits OutputLimits) OutputSlice {
	var from, to int
	if limits.Retain == "head" {
		from, to = headRange(text, limits)
	} else {
		from, to = tailRange(text, limits)
	}
	kept := text[from:to]
	return OutputSlice{Text: kept, Bytes: len(kept), DroppedBytes: len(text) - len(kept), DroppedLines: lineCount(text) - lineCount(kept)}
}

func headRange(s string, limits OutputLimits) (int, int) {
	if limits.MaxLines == 0 || limits.MaxBytes == 0 {
		return 0, 0
	}
	end := len(s)
	lines := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '\n' {
			continue
		}
		lines++
		if lines == limits.MaxLines {
			end = i + 1
			break
		}
	}
	if end > limits.MaxBytes {
		newline := strings.LastIndexByte(s[:limits.MaxBytes], '\n')
		if newline == -1 {
			end = characterEnd(s, limits.MaxBytes)
		} else {
			end = newline + 1
		}
	}
	return 0, end
}

func tailRange(s string, limits OutputLimits) (int, int) {
	n := len(s)
	if limits.MaxLines == 0 || limits.MaxBytes == 0 {
		return n, n
	}
	// A trailing newline ends the last line rather than starting another.
	last := n - 1
	if n > 0 && s[n-1] == '\n' {
		last = n - 2
	}
	start := 0
	lines := 1
	index := -1
	if last >= 0 {
		index = strings.LastIndexByte(s[:last+1], '\n')
	}
	for index != -1 {
		if lines == limits.MaxLines {
			start = index + 1
			break
		}
		lines++
		if index == 0 {
			index = -1
		} else {
			index = strings.LastIndexByte(s[:index], '\n')
		}
	}
	if n-start > limits.MaxBytes {
		from := n - limits.MaxBytes
		newline := -1
		if from-1 >= 0 {
			if i := strings.IndexByte(s[from-1:], '\n'); i >= 0 {
				newline = from - 1 + i
			}
		} else if i := strings.IndexByte(s, '\n'); i >= 0 {
			newline = i
		}
		if newline != -1 && newline+1 < n {
			start = newline + 1
		} else {
			start = characterStart(s, from)
		}
	}
	return start, n
}

// characterEnd is the last character boundary at or before index.
func characterEnd(s string, index int) int {
	end := index
	for end > 0 && end < len(s) && s[end]&0xc0 == 0x80 {
		end--
	}
	return end
}

// characterStart is the first character boundary at or after index.
func characterStart(s string, index int) int {
	start := index
	for start < len(s) && s[start]&0xc0 == 0x80 {
		start++
	}
	return start
}

func lineCount(s string) int {
	if len(s) == 0 {
		return 0
	}
	n := strings.Count(s, "\n")
	if s[len(s)-1] != '\n' {
		n++
	}
	return n
}

// Skip is output omitted right before a chunk (ShellOutputSkip): its size, newlines and whether it ended with one.
type Skip struct {
	Bytes           int
	Newlines        int
	EndsWithNewline bool
}

type outChunk struct {
	text     string
	bytes    int
	newlines int
}

// OutputBuffer is the bounded running output of one tool call. Accepting a chunk costs time proportional to the chunk:
// head retention stops storing once the window is full, and tail retention drops stored text the window no longer needs when
// it snapshots. Counts of the whole stream are kept so the dropped totals stay exact.
type OutputBuffer struct {
	limits OutputLimits
	// decoder of byte chunks (WHATWG UTF-8 with replacement); a string chunk or a skip ends an incomplete character.
	decoder utf8Decoder
	started bool
	chunks  []outChunk

	storedBytes    int
	storedNewlines int
	full           bool
	totalBytes     int
	totalNewlines  int
	endsWithNL     bool
}

// NewOutputBuffer returns an empty buffer.
func NewOutputBuffer(limits OutputLimits) *OutputBuffer {
	return &OutputBuffer{limits: limits, endsWithNL: true}
}

// StoredBytes is the bytes currently held; bounded by the limits plus one chunk.
func (b *OutputBuffer) StoredBytes() int { return b.storedBytes }

// PushString accepts a string chunk; it reports whether anything was accepted. skipped is output omitted right before the
// chunk; only tail retention accepts it.
func (b *OutputBuffer) PushString(chunk string, skipped *Skip) bool {
	return b.push(chunk, true, skipped)
}

// PushBytes accepts a byte chunk, decoded as a stream.
func (b *OutputBuffer) PushBytes(chunk []byte, skipped *Skip) bool {
	return b.push(string(chunk), false, skipped)
}

func (b *OutputBuffer) push(chunk string, isString bool, skipped *Skip) bool {
	// Bytes of an incomplete character from an earlier byte chunk come first.
	pending := ""
	if isString || skipped != nil {
		pending = b.decoder.flush()
	}
	text := chunk
	if !isString {
		text = b.decoder.decode(chunk)
	}
	first := !b.started && pending == "" && skipped == nil
	if pending != "" || text != "" || skipped != nil {
		b.started = true
	}
	if first && !isString && strings.HasPrefix(text, "\ufeff") {
		text = text[len("\ufeff"):]
	}
	if skipped == nil {
		return b.accept(pending + text)
	}
	if b.limits.Retain != "tail" {
		panic("Skipped output requires tail retention")
	}
	b.accept(pending)
	b.skip(skipped)
	b.accept(text)
	return true
}

func (b *OutputBuffer) skip(s *Skip) {
	if s.Bytes == 0 {
		return
	}
	b.totalBytes += s.Bytes
	b.totalNewlines += s.Newlines
	b.endsWithNL = s.EndsWithNewline
	b.chunks = nil
	b.storedBytes = 0
	b.storedNewlines = 0
}

// End flushes an incomplete trailing character as a replacement character; call when the stream ends.
func (b *OutputBuffer) End() { b.accept(b.decoder.flush()) }

func (b *OutputBuffer) accept(text string) bool {
	if len(text) == 0 {
		return false
	}
	bytes := len(text)
	newlines := strings.Count(text, "\n")
	b.totalBytes += bytes
	b.totalNewlines += newlines
	b.endsWithNL = strings.HasSuffix(text, "\n")
	if b.full {
		return true
	}
	b.chunks = append(b.chunks, outChunk{text, bytes, newlines})
	b.storedBytes += bytes
	b.storedNewlines += newlines
	if b.limits.Retain == "head" {
		// Nothing past a full window is ever needed.
		b.full = b.storedBytes > b.limits.MaxBytes || b.storedNewlines >= b.limits.MaxLines
		return true
	}
	// Drop leading chunks while the rest still holds more than a window: more than MaxBytes bytes or MaxLines newlines,
	// plus one, so the window's line start can still be found. Each chunk is dropped once.
	for len(b.chunks) > 1 {
		first := b.chunks[0]
		bytesAfter := b.storedBytes - first.bytes
		newlinesAfter := b.storedNewlines - first.newlines
		if bytesAfter <= b.limits.MaxBytes+1 && newlinesAfter <= b.limits.MaxLines+1 {
			break
		}
		b.chunks = b.chunks[1:]
		b.storedBytes = bytesAfter
		b.storedNewlines = newlinesAfter
	}
	return true
}

// Snapshot returns the retained, sanitized output and what the limits dropped from the whole stream.
func (b *OutputBuffer) Snapshot() BoundedOutput {
	var stored string
	if len(b.chunks) == 1 {
		stored = b.chunks[0].text
	} else {
		var sb strings.Builder
		for _, c := range b.chunks {
			sb.WriteString(c.text)
		}
		stored = sb.String()
	}
	kept := BoundOutput(stored, b.limits)
	storedLines := lines(b.storedNewlines, stored == "" || strings.HasSuffix(stored, "\n"))
	keptLines := storedLines - kept.DroppedLines
	// Tail windows never reach back before this one, but finding a later window's first line needs what precedes it: keep
	// the shortest suffix longer than the window by a byte or a line, as accept does.
	if b.limits.Retain == "tail" || len(b.chunks) > 1 {
		text := stored
		bytes := b.storedBytes
		if b.limits.Retain == "tail" {
			text = tailMargin(stored, b.limits)
			bytes = len(text)
		}
		b.chunks = nil
		if text != "" {
			b.chunks = []outChunk{{text, bytes, strings.Count(text, "\n")}}
		}
		b.storedBytes = bytes
		b.storedNewlines = 0
		if len(b.chunks) == 1 {
			b.storedNewlines = b.chunks[0].newlines
		}
	}
	return BoundedOutput{
		Text:         SanitizeOutput(kept.Text),
		DroppedBytes: b.totalBytes - kept.Bytes,
		DroppedLines: lines(b.totalNewlines, b.endsWithNL) - keptLines,
	}
}

// tailMargin is the shortest suffix of text with more than MaxBytes bytes or more than MaxLines newlines, or all of it. The
// tail window of any text that ends with this suffix, followed by anything, is the same as of text followed by it.
func tailMargin(text string, limits OutputLimits) string {
	byteStart := 0
	if len(text) > limits.MaxBytes {
		byteStart = characterEnd(text, len(text)-limits.MaxBytes-1)
	}
	lineStart := 0
	newlines := 0
	for index := strings.LastIndexByte(text, '\n'); index != -1; {
		newlines++
		if newlines > limits.MaxLines {
			lineStart = index
			break
		}
		if index == 0 {
			break
		}
		index = strings.LastIndexByte(text[:index], '\n')
	}
	start := max(byteStart, lineStart)
	if start == 0 {
		return text
	}
	return decodeLossy(text[start:])
}

// lines is the lines of text with newlines newlines; a final unterminated line counts.
func lines(newlines int, terminated bool) int {
	if terminated {
		return newlines
	}
	return newlines + 1
}
