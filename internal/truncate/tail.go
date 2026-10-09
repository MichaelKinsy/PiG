// Package truncate holds the line and byte walk of Pi's truncateTail
// (packages/coding-agent/src/core/tools/truncate.ts), shared by the bash tool's
// result truncation and the bash-execution block's context limit, which import
// cycles keep in different packages.
package truncate

import (
	"slices"
	"strings"
)

// The default limits of truncateHead and truncateTail.
const (
	DefaultMaxBytes = 50 * 1024 // upstream: packages/coding-agent/src/core/tools/truncate.ts:DEFAULT_MAX_BYTES
	DefaultMaxLines = 2_000     // upstream: packages/coding-agent/src/core/tools/truncate.ts:DEFAULT_MAX_LINES
)

// Tail is truncateTail's result: the kept content and what was cut.
type Tail struct {
	Content         string
	Truncated       bool
	TruncatedBy     string // "lines" | "bytes" | ""
	TotalLines      int
	TotalBytes      int
	OutputLines     int
	OutputBytes     int
	LastLinePartial bool
}

// SplitLinesForCounting splits content into lines for truncation counting,
// treating a trailing newline as a line terminator rather than an empty final
// line (truncate.ts splitLinesForCounting).
func SplitLinesForCounting(content string) []string {
	if len(content) == 0 {
		return nil
	}
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// TruncateTail keeps the last maxLines lines and maxBytes bytes of content,
// whichever limit is hit first (truncate.ts truncateTail). When the last line
// alone exceeds maxBytes, its end is kept and LastLinePartial is set.
func TruncateTail(content string, maxBytes, maxLines int) Tail {
	totalBytes := len(content)
	lines := SplitLinesForCounting(content)
	totalLines := len(lines)
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return Tail{Content: content, TotalLines: totalLines, TotalBytes: totalBytes, OutputLines: totalLines, OutputBytes: totalBytes}
	}

	// Walk backwards collecting lines that still fit, newest first; reversed
	// once below. Prepending each line was quadratic in the kept lines, and
	// streaming snapshots run this on every throttled update.
	var collected []string
	outputBytes := 0
	truncatedBy := "lines"
	lastLinePartial := false
	for i := len(lines) - 1; i >= 0 && len(collected) < maxLines; i-- {
		line := lines[i]
		// +1 for the joining newline, except for the first line added.
		extra := 0
		if len(collected) > 0 {
			extra = 1
		}
		lineBytes := len(line) + extra
		if outputBytes+lineBytes > maxBytes {
			truncatedBy = "bytes"
			if len(collected) == 0 {
				// This single line is bigger than maxBytes: keep its end, on a UTF-8 boundary.
				partial := tailNBytesUTF8(line, maxBytes)
				collected = append(collected, partial)
				outputBytes = len(partial)
				lastLinePartial = true
			}
			break
		}
		collected = append(collected, line)
		outputBytes += lineBytes
	}
	slices.Reverse(collected)
	if len(collected) >= maxLines && outputBytes <= maxBytes {
		truncatedBy = "lines"
	}
	out := strings.Join(collected, "\n")
	return Tail{
		Content:         out,
		Truncated:       true,
		TruncatedBy:     truncatedBy,
		TotalLines:      totalLines,
		TotalBytes:      totalBytes,
		OutputLines:     len(collected),
		OutputBytes:     len(out),
		LastLinePartial: lastLinePartial,
	}
}

// tailNBytesUTF8 returns the last n bytes of s, advanced forward to the next
// UTF-8 character boundary so it never starts inside a rune.
func tailNBytesUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	start := len(s) - n
	for start < len(s) && (s[start]&0xC0) == 0x80 {
		start++
	}
	return s[start:]
}
