package tools

import (
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

// Add appends one line (it may itself contain newlines).
func (h *headJoin) Add(line string) {
	if h.parts > 0 {
		h.write("\n", 1)
	}
	h.parts++
	h.write(line, strings.Count(line, "\n"))
}

// AddLength appends a line of n bytes without newlines whose text Result cannot return: it is only called once full.
func (h *headJoin) AddLength(n int) {
	if h.parts > 0 {
		h.count("\n", 1)
	}
	h.parts++
	// firstLineBytes need not grow: without a newline yet, the first line already fills the prefix past maxBytes.
	h.countBytes(n, 0, false)
}

func (h *headJoin) write(s string, newlines int) {
	if room := h.maxBytes + 1 - h.prefix.Len(); room > 0 {
		h.prefix.WriteString(s[:min(len(s), room)])
	}
	h.count(s, newlines)
}

func (h *headJoin) count(s string, newlines int) {
	if h.newlines == 0 {
		if at := strings.IndexByte(s, '\n'); at >= 0 {
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

// truncatedLineBytes is len(TruncateLine(line, maxChars).Text) and its WasTruncated, without building the text.
func truncatedLineBytes(line string, maxChars int) (int, bool) {
	if maxChars <= 0 {
		maxChars = GrepMaxLineLengthUpstream
	}
	if jsLength(line) <= maxChars {
		return len(line), false
	}
	n, units := 0, 0
	for _, r := range line {
		width := utf16.RuneLen(r)
		if units+width > maxChars {
			if units < maxChars {
				n += utf8.RuneLen(utf8.RuneError)
			}
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
