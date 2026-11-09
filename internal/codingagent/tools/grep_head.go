package tools

// Ports packages/coding-agent/src/core/tools/grep.ts

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// headJoin collects the lines of an output that is joined with "\n" and then cut by TruncateHead, keeping only the bytes the head can return. grep formats limit x (2*context+1) lines before it truncates to 50KB (grep.ts:176-282), so materializing them all held memory proportional to the context for an output of fixed size (#9276). Result returns exactly what TruncateHead returns for the joined text.
type headJoin struct {
	maxBytes, maxLines int
	// prefix is the joined text up to maxBytes+1 bytes: every line TruncateHead can keep ends inside it.
	prefix strings.Builder
	parts  int
	// totalBytes, newlines, firstLineBytes and endsWithNewline describe the whole joined text.
	totalBytes      int
	newlines        int
	firstLineBytes  int
	endsWithNewline bool
}

func newHeadJoin(options TruncationOptions) *headJoin {
	maxBytes, maxLines := options.limits()
	return &headJoin{maxBytes: maxBytes, maxLines: maxLines}
}

// full reports whether the prefix holds everything Result can return, so a later line only counts.
func (h *headJoin) full() bool { return h.prefix.Len() > h.maxBytes }

// Add appends one line (it may itself contain newlines); line is copied.
func (h *headJoin) Add(line []byte) {
	if h.parts > 0 {
		h.write(headJoinNewline, 1)
	}
	h.parts++
	h.write(line, bytes.Count(line, headJoinNewline))
}

// AddLength appends a line of n bytes without newlines whose text Result cannot return: it is only called once full.
func (h *headJoin) AddLength(n int) {
	if h.parts > 0 {
		h.count(headJoinNewline, 1)
	}
	h.parts++
	// firstLineBytes need not grow: without a newline yet, the first line already fills the prefix past maxBytes.
	h.countBytes(n, 0, false)
}

// headJoinSpanGrowth is the prefix size from which it grows to its cap in one step.
const headJoinSpanGrowth = 8 * 1024

// headJoinNewline is the separator of the joined lines.
var headJoinNewline = []byte{'\n'}

func (h *headJoin) write(s []byte, newlines int) {
	if room := h.maxBytes + 1 - h.prefix.Len(); room > 0 {
		n := min(len(s), room)
		if h.prefix.Cap()-h.prefix.Len() < n {
			// An output that has grown this far is likely to fill the prefix: grow it once to the cap, not by doubling up to it.
			if h.prefix.Len() >= headJoinSpanGrowth {
				n = room
			}
			h.prefix.Grow(n)
		}
		h.prefix.Write(s[:min(len(s), room)])
	}
	h.count(s, newlines)
}

func (h *headJoin) count(s []byte, newlines int) {
	if h.newlines == 0 {
		if at := bytes.IndexByte(s, '\n'); at >= 0 {
			h.firstLineBytes += at
		} else {
			h.firstLineBytes += len(s)
		}
	}
	h.countBytes(len(s), newlines, len(s) > 0 && s[len(s)-1] == '\n')
}

func (h *headJoin) countBytes(n, newlines int, endsWithNewline bool) {
	h.totalBytes += n
	h.newlines += newlines
	if n > 0 {
		h.endsWithNewline = endsWithNewline
	}
}

// Result is TruncateHead of the joined text.
func (h *headJoin) Result() TruncationResult {
	maxBytes, maxLines := h.maxBytes, h.maxLines
	totalLines := 0
	if h.totalBytes > 0 {
		totalLines = h.newlines + 1
		if h.endsWithNewline {
			totalLines--
		}
	}
	prefix := h.prefix.String()
	if totalLines <= maxLines && h.totalBytes <= maxBytes {
		return TruncationResult{Content: prefix, TotalLines: totalLines, TotalBytes: h.totalBytes, OutputLines: totalLines, OutputBytes: h.totalBytes, MaxLines: maxLines, MaxBytes: maxBytes}
	}
	if totalLines > 0 && h.firstLineBytes > maxBytes {
		return TruncationResult{Truncated: true, TruncatedBy: "bytes", TotalLines: totalLines, TotalBytes: h.totalBytes, FirstLineExceedsLimit: true, MaxLines: maxLines, MaxBytes: maxBytes}
	}
	collected, outputBytes, truncatedBy := 0, 0, "lines"
	rest := prefix
	for collected < maxLines && collected < totalLines {
		line, more := rest, false
		if at := strings.IndexByte(rest, '\n'); at >= 0 {
			line, rest, more = rest[:at], rest[at+1:], true
		} else {
			rest = ""
		}
		extra := 0
		if collected > 0 {
			extra = 1
		}
		// A line the prefix cut off ends past maxBytes, and a line after the prefix starts past it: neither fits.
		complete := more || len(prefix) == h.totalBytes
		if !complete || outputBytes+len(line)+extra > maxBytes {
			truncatedBy = "bytes"
			break
		}
		collected++
		outputBytes += len(line) + extra
	}
	if collected >= maxLines && outputBytes <= maxBytes {
		truncatedBy = "lines"
	}
	return TruncationResult{Content: prefix[:outputBytes], Truncated: true, TruncatedBy: truncatedBy, TotalLines: totalLines, TotalBytes: h.totalBytes, OutputLines: collected, OutputBytes: outputBytes, MaxLines: maxLines, MaxBytes: maxBytes}
}

// truncatedLineBytes is len(TruncateLine(string(line), maxChars).Text) and its WasTruncated, without building the text.
func truncatedLineBytes(line []byte, maxChars int) (int, bool) {
	if maxChars <= 0 {
		maxChars = GrepMaxLineLengthUpstream
	}
	if jsLengthBytes(line) <= maxChars {
		return len(line), false
	}
	n, units := 0, 0
	for rest := line; len(rest) > 0; {
		r, size := utf8.DecodeRune(rest)
		rest = rest[size:]
		width := utf16.RuneLen(r)
		if units+width > maxChars {
			// pig divergence (D252): a pair the cut would split is dropped whole.
			break
		}
		n += utf8.RuneLen(r)
		units += width
	}
	return n + len(truncatedLineSuffix), true
}

// decimalBytes is the length of an integer line number as jsNumber writes it.
func decimalBytes(n int) int {
	var buf [24]byte
	return len(strconv.AppendInt(buf[:0], int64(n), 10))
}

// jsLengthBytes is jsLength(string(s)) without the copy.
func jsLengthBytes(s []byte) int {
	n := 0
	for len(s) > 0 {
		if s[0] < utf8.RuneSelf {
			n++
			s = s[1:]
			continue
		}
		r, size := utf8.DecodeRune(s)
		n += utf16.RuneLen(r)
		s = s[size:]
	}
	return n
}
