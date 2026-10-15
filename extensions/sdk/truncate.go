package sdk

import (
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Output truncation helpers for tool results, mirroring Pi's exports of core/tools/truncate.ts (formatSize, truncateHead, truncateTail, truncateLine and the
// default limits). Truncation uses two independent limits, a line count and a UTF-8 byte count, and the one hit first wins.
const (
	// DefaultMaxLines is Pi's DEFAULT_MAX_LINES.
	DefaultMaxLines = 2000
	// DefaultMaxBytes is Pi's DEFAULT_MAX_BYTES (50KB).
	DefaultMaxBytes = 50 * 1024
	// GrepMaxLineLength is Pi's GREP_MAX_LINE_LENGTH: the characters a grep match line keeps before [TruncateLine] cuts it.
	GrepMaxLineLength = 500
)

// TruncationOptions are the limits [TruncateHead] and [TruncateTail] apply. A nil limit takes its default, like an omitted Pi option; a limit of 0 is used as given.
type TruncationOptions struct {
	MaxLines *int
	MaxBytes *int
}

// TruncationResult is Pi's TruncationResult.
type TruncationResult struct {
	// Content is the truncated content.
	Content string
	// Truncated reports whether truncation occurred.
	Truncated bool
	// TruncatedBy is the limit that was hit, "lines" or "bytes", and "" (Pi's null) when nothing was truncated.
	TruncatedBy string
	// TotalLines and TotalBytes describe the original content; OutputLines and OutputBytes the truncated output.
	TotalLines, TotalBytes, OutputLines, OutputBytes int
	// LastLinePartial reports that the last line was cut (the tail edge case where the final line alone exceeds the byte limit).
	LastLinePartial bool
	// FirstLineExceedsLimit reports that the first line alone exceeded the byte limit (head truncation).
	FirstLineExceedsLimit bool
	// MaxLines and MaxBytes are the limits that were applied.
	MaxLines, MaxBytes int
}

func (o TruncationOptions) limits() (maxLines, maxBytes int) {
	maxLines, maxBytes = DefaultMaxLines, DefaultMaxBytes
	if o.MaxLines != nil {
		maxLines = *o.MaxLines
	}
	if o.MaxBytes != nil {
		maxBytes = *o.MaxBytes
	}
	return maxLines, maxBytes
}

// countLines is len(splitLinesForCounting(content)) in Pi: a trailing newline terminates the last line instead of starting another.
func countLines(content string) int {
	if content == "" {
		return 0
	}
	lines := strings.Count(content, "\n")
	if content[len(content)-1] != '\n' {
		lines++
	}
	return lines
}

// FormatSize formats a byte count as Pi's formatSize does: "512B", "1.5KB" or "2.0MB", rounding the one decimal half up like JavaScript's toFixed.
func FormatSize(bytes int) string {
	switch {
	case bytes < 1024:
		return strconv.Itoa(bytes) + "B"
	case bytes < 1024*1024:
		return tenths(bytes, 1024) + "KB"
	default:
		return tenths(bytes, 1024*1024) + "MB"
	}
}

// tenths renders bytes/unit with one decimal. unit is a power of two, so the quotient is exact in a double and toFixed(1) rounds an exact tie up.
// Rounding only the remainder keeps every int in range: scaling the whole count by ten overflows near math.MaxInt64.
func tenths(bytes, unit int) string {
	whole, tenth := bytes/unit, (bytes%unit*10+unit/2)/unit
	if tenth == 10 {
		whole, tenth = whole+1, 0
	}
	return strconv.Itoa(whole) + "." + strconv.Itoa(tenth)
}

// TruncateHead keeps the first lines and bytes of content, which suits file reads. It never returns a partial line: when the first line alone exceeds the
// byte limit the content is empty and FirstLineExceedsLimit is set.
//
// The kept lines are contiguous, so the result is a prefix of content: the walk finds where it ends and nothing is split or joined. The module cannot
// import PiG's internal/truncate, which holds the same walk for the host.
func TruncateHead(content string, options TruncationOptions) TruncationResult {
	maxLines, maxBytes := options.limits()
	totalBytes, totalLines := len(content), countLines(content)
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{Content: content, TotalLines: totalLines, TotalBytes: totalBytes, OutputLines: totalLines, OutputBytes: totalBytes, MaxLines: maxLines, MaxBytes: maxBytes}
	}
	firstLine := strings.IndexByte(content, '\n')
	if firstLine < 0 {
		firstLine = len(content)
	}
	if totalLines > 0 && firstLine > maxBytes {
		return TruncationResult{Truncated: true, TruncatedBy: "bytes", TotalLines: totalLines, TotalBytes: totalBytes, FirstLineExceedsLimit: true, MaxLines: maxLines, MaxBytes: maxBytes}
	}
	last := len(content)
	if last > 0 && content[last-1] == '\n' {
		last--
	}
	count, end, byBytes := 0, 0, false
	for count < maxLines && totalLines > 0 {
		start, extra := 0, 0
		if count > 0 {
			if end == last {
				break
			}
			start, extra = end+1, 1
		}
		// The line fits if it has at most budget bytes, so the scan stops one byte past the budget.
		budget := maxBytes - end - extra
		if budget < 0 {
			byBytes = true
			break
		}
		scanEnd := last
		if budget < last-start {
			scanEnd = start + budget + 1
		}
		lineEnd := scanEnd
		if i := strings.IndexByte(content[start:scanEnd], '\n'); i >= 0 {
			lineEnd = start + i
		}
		if lineEnd-start > budget {
			byBytes = true
			break
		}
		end = lineEnd
		count++
	}
	truncatedBy := "lines"
	if byBytes && (count < maxLines || end > maxBytes) {
		truncatedBy = "bytes"
	}
	return TruncationResult{Content: content[:end], Truncated: true, TruncatedBy: truncatedBy, TotalLines: totalLines, TotalBytes: totalBytes, OutputLines: count, OutputBytes: end, MaxLines: maxLines, MaxBytes: maxBytes}
}

// TruncateTail keeps the last lines and bytes of content, which suits command output where errors and final results come last. When the last line alone
// exceeds the byte limit it keeps the end of that line and sets LastLinePartial.
//
// The kept lines are contiguous, so the result is a substring of content that ends before a trailing newline: the walk scans back from the end for its
// start and nothing is split or joined.
func TruncateTail(content string, options TruncationOptions) TruncationResult {
	maxLines, maxBytes := options.limits()
	totalBytes, totalLines := len(content), countLines(content)
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{Content: content, TotalLines: totalLines, TotalBytes: totalBytes, OutputLines: totalLines, OutputBytes: totalBytes, MaxLines: maxLines, MaxBytes: maxBytes}
	}
	end := len(content)
	if end > 0 && content[end-1] == '\n' {
		end--
	}
	count, start, byBytes, partial := 0, end, false, false
	for count < maxLines && totalLines > 0 {
		lineEnd, extra := end, 0
		if count > 0 {
			if start == 0 {
				break
			}
			lineEnd, extra = start-1, 1
		}
		// The line fits if it has at most budget bytes, so the scan stops one byte past the budget.
		budget := maxBytes - (end - start) - extra
		floor := 0
		if budget < lineEnd {
			floor = lineEnd - budget - 1
		}
		lineStart := lineEnd
		for lineStart > floor && content[lineStart-1] != '\n' {
			lineStart--
		}
		if lineEnd-lineStart > budget {
			byBytes = true
			if count == 0 {
				// The last line alone is too long: keep its end, moved forward to the start of a UTF-8 character. A negative limit starts past the end, where
				// Buffer.slice returns an empty buffer.
				start = lineEnd - maxBytes
				if maxBytes < 0 {
					start = lineEnd
				}
				for start < lineEnd && !utf8.RuneStart(content[start]) {
					start++
				}
				count, partial = 1, true
			}
			break
		}
		start = lineStart
		count++
	}
	output := content[start:end]
	truncatedBy := "lines"
	if byBytes && (count < maxLines || len(output) > maxBytes) {
		truncatedBy = "bytes"
	}
	return TruncationResult{Content: output, Truncated: true, TruncatedBy: truncatedBy, TotalLines: totalLines, TotalBytes: totalBytes, OutputLines: count, OutputBytes: len(output), LastLinePartial: partial, MaxLines: maxLines, MaxBytes: maxBytes}
}

// TruncateLine cuts a line longer than maxChars UTF-16 code units (a JavaScript string's length) to that many units plus "... [truncated]" and reports
// whether it cut. Pass GrepMaxLineLength for Pi's default. A cut inside a surrogate pair leaves U+FFFD where JavaScript keeps a lone surrogate. A negative
// maxChars counts back from the end of the line, as String.prototype.slice does.
func TruncateLine(line string, maxChars int) (text string, wasTruncated bool) {
	units := 0
	for _, r := range line {
		units += utf16.RuneLen(r)
	}
	if units <= maxChars {
		return line, false
	}
	end := maxChars
	if end < 0 {
		end = max(units+maxChars, 0)
	}
	var b strings.Builder
	used := 0
	for _, r := range line {
		n := utf16.RuneLen(r)
		if used+n > end {
			if used < end {
				b.WriteRune(utf8.RuneError)
			}
			break
		}
		b.WriteRune(r)
		used += n
	}
	return b.String() + "... [truncated]", true
}
