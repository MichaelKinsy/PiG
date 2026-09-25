// Tail/head truncation helpers for tool outputs.
//
// Mirrors upstream packages/coding-agent/src/core/tools/truncate.ts
// byte-for-byte:
//
//   - DEFAULT_MAX_BYTES = 50 KB
//   - DEFAULT_MAX_LINES = 2000
//   - GREP_MAX_LINE_LENGTH = 500 chars per match line
//
// truncateTail keeps the LAST N lines/bytes (bash output: errors and
// final results live there). truncateHead keeps the FIRST N (file
// reads: beginning matters).
//
// "Whichever is hit first" applies to both: line cap or byte cap.
//
// For the bash tail-truncation edge case where the LAST line alone
// exceeds maxBytes, we return that line truncated from its end with
// LastLinePartial = true so the renderer can show
// "[Showing last <bytes> of line N (line is <bytes>). ...]"

package tools

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// splitLinesForCounting splits content into lines for truncation counting,
// treating a trailing newline as a line terminator rather than an empty
// final line. Mirrors upstream splitLinesForCounting (truncate.ts v0.75.5).
func splitLinesForCounting(content string) []string {
	if len(content) == 0 {
		return nil
	}
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Mirrors upstream defaults exactly.
// These supersede the older 200_000 figure that lived in tools.go.
const (
	DefaultMaxBytesUpstream   = 50 * 1024 // 50 KB: upstream DEFAULT_MAX_BYTES
	DefaultMaxLinesUpstream   = 2000      // upstream DEFAULT_MAX_LINES
	GrepMaxLineLengthUpstream = 500       // upstream GREP_MAX_LINE_LENGTH
)

// TruncationResult captures every fact a renderer needs to format the
// "[Showing lines X-Y of Z. Full output: <path>]" warning row.
//
// Layout matches upstream TruncationResult:
//
//	{ content, truncated, truncatedBy, totalLines, totalBytes,
//	  outputLines, outputBytes, lastLinePartial, firstLineExceedsLimit,
//	  maxLines, maxBytes }
type TruncationResult struct {
	Content     string
	Truncated   bool
	TruncatedBy string // "lines" | "bytes" | ""
	TotalLines  int
	TotalBytes  int
	OutputLines int
	OutputBytes int
	// LastLinePartial: the last line of the original output is the
	// only one that fit (and was truncated from its end). Bash-only
	// edge case.
	LastLinePartial bool
	// FirstLineExceedsLimit: head-truncation case where the first line
	// alone exceeds maxBytes. We return empty content and let the
	// caller decide.
	FirstLineExceedsLimit bool
	MaxLines              int
	MaxBytes              int
}

// TruncateTail keeps the last lines/bytes of content. Used for bash
// output (errors and final results live at the end).
//
// maxBytes and maxLines are used as given, like upstream's options (only an
// omitted option falls back to a default there); callers pass the defaults.
func TruncateTail(content string, maxBytes, maxLines int) TruncationResult {
	totalBytes := len(content)
	lines := splitLinesForCounting(content)
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content:     content,
			Truncated:   false,
			TotalLines:  totalLines,
			TotalBytes:  totalBytes,
			OutputLines: totalLines,
			OutputBytes: totalBytes,
			MaxLines:    maxLines,
			MaxBytes:    maxBytes,
		}
	}

	// Walk backwards collecting lines that still fit.
	var collected []string
	outputBytes := 0
	truncatedBy := "lines"
	lastLinePartial := false
	for i := len(lines) - 1; i >= 0 && len(collected) < maxLines; i-- {
		line := lines[i]
		// +1 for the joining newline, except for the very first line
		// added (which has no preceding newline in the joined output).
		extra := 0
		if len(collected) > 0 {
			extra = 1
		}
		lineBytes := len(line) + extra
		if outputBytes+lineBytes > maxBytes {
			truncatedBy = "bytes"
			if len(collected) == 0 {
				// Edge: this single line is bigger than maxBytes.
				// Take its END (last `maxBytes` bytes), respecting
				// UTF-8 boundaries.
				partial := tailNBytesUTF8(line, maxBytes)
				collected = append(collected, partial)
				outputBytes = len(partial)
				lastLinePartial = true
			}
			break
		}
		// Prepend (we're walking backwards).
		collected = append([]string{line}, collected...)
		outputBytes += lineBytes
	}
	if len(collected) >= maxLines && outputBytes <= maxBytes {
		truncatedBy = "lines"
	}
	out := strings.Join(collected, "\n")
	return TruncationResult{
		Content:         out,
		Truncated:       true,
		TruncatedBy:     truncatedBy,
		TotalLines:      totalLines,
		TotalBytes:      totalBytes,
		OutputLines:     len(collected),
		OutputBytes:     len(out),
		LastLinePartial: lastLinePartial,
		MaxLines:        maxLines,
		MaxBytes:        maxBytes,
	}
}

// TruncateHead keeps the FIRST lines/bytes of content. Used for grep
// output, find results, etc.: anywhere we want to see the
// beginning. Mirrors upstream `truncateHead` at
// `.upstream/current/packages/coding-agent/src/core/tools/truncate.ts:67-149`.
//
// Never returns partial lines: if the first line alone exceeds
// maxBytes, the result is empty content with `FirstLineExceedsLimit`
// set so the caller can emit the upstream `[First line exceeds N
// limit]` warning.
func TruncateHead(content string, maxBytes, maxLines int) TruncationResult {
	totalBytes := len(content)
	lines := splitLinesForCounting(content)
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content:     content,
			Truncated:   false,
			TotalLines:  totalLines,
			TotalBytes:  totalBytes,
			OutputLines: totalLines,
			OutputBytes: totalBytes,
			MaxLines:    maxLines,
			MaxBytes:    maxBytes,
		}
	}

	// First-line-exceeds-limit edge (matches upstream :85-99).
	if len(lines) > 0 && len(lines[0]) > maxBytes {
		return TruncationResult{
			Content:               "",
			Truncated:             true,
			TruncatedBy:           "bytes",
			TotalLines:            totalLines,
			TotalBytes:            totalBytes,
			OutputLines:           0,
			OutputBytes:           0,
			FirstLineExceedsLimit: true,
			MaxLines:              maxLines,
			MaxBytes:              maxBytes,
		}
	}

	var collected []string
	outputBytes := 0
	truncatedBy := "lines"
	for i := 0; i < len(lines) && len(collected) < maxLines; i++ {
		line := lines[i]
		extra := 0
		if i > 0 {
			extra = 1 // joining newline
		}
		lineBytes := len(line) + extra
		if outputBytes+lineBytes > maxBytes {
			truncatedBy = "bytes"
			break
		}
		collected = append(collected, line)
		outputBytes += lineBytes
	}
	if len(collected) >= maxLines && outputBytes <= maxBytes {
		truncatedBy = "lines"
	}
	out := strings.Join(collected, "\n")
	return TruncationResult{
		Content:     out,
		Truncated:   true,
		TruncatedBy: truncatedBy,
		TotalLines:  totalLines,
		TotalBytes:  totalBytes,
		OutputLines: len(collected),
		OutputBytes: len(out),
		MaxLines:    maxLines,
		MaxBytes:    maxBytes,
	}
}

// FormatTruncationWarning returns the upstream-format `[Truncated:
// ...]` warning string for a TruncationResult, or "" if not
// truncated. Mirrors upstream's render-layer formatting in
// `read.ts:108-116` and `bash.ts:247-263`. Producers that don't
// have a separate render layer (pig grep / find / etc.) append
// the warning directly to the LLM-visible content.
func FormatTruncationWarning(tr TruncationResult) string {
	if !tr.Truncated {
		return ""
	}
	if tr.FirstLineExceedsLimit {
		return "[First line exceeds " + FormatSize(tr.MaxBytes) + " limit]"
	}
	if tr.TruncatedBy == "lines" {
		return "[Truncated: showing " + itoa(tr.OutputLines) + " of " + itoa(tr.TotalLines) + " lines (" + itoa(tr.MaxLines) + " line limit)]"
	}
	return "[Truncated: " + itoa(tr.OutputLines) + " lines shown (" + FormatSize(tr.MaxBytes) + " limit)]"
}

// tailNBytesUTF8 returns the last n bytes of s, advanced forward to
// the next UTF-8 character boundary so we don't return invalid runes.
func tailNBytesUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	start := len(s) - n
	// 0xC0 mask: 10xxxxxx is a continuation byte; advance past it.
	for start < len(s) && (s[start]&0xC0) == 0x80 {
		start++
	}
	return s[start:]
}

// FormatSize renders a byte count as "123B" / "12.3KB" / "1.2MB".
// Mirrors upstream `formatSize`.
func FormatSize(bytes int) string {
	switch {
	case bytes < 1024:
		return itoa(bytes) + "B"
	case bytes < 1024*1024:
		return fmtFloat(float64(bytes)/1024.0, "KB")
	default:
		return fmtFloat(float64(bytes)/(1024.0*1024.0), "MB")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func fmtFloat(f float64, suffix string) string {
	// One decimal, banker-truncated to match upstream `.toFixed(1)`.
	scaled := int64(f*10 + 0.5)
	whole := scaled / 10
	frac := scaled % 10
	return itoa(int(whole)) + "." + string('0'+byte(frac)) + suffix
}

// TruncateLine mirrors upstream truncateLine: a line longer than maxChars
// UTF-16 code units (JavaScript string length) is cut to maxChars units plus
// "... [truncated]". A cut inside a surrogate pair leaves U+FFFD, as the
// lone surrogate JavaScript would keep serializes to.
func TruncateLine(line string, maxChars int) (string, bool) {
	if maxChars <= 0 {
		maxChars = GrepMaxLineLengthUpstream
	}
	if jsLength(line) <= maxChars {
		return line, false
	}
	var b strings.Builder
	units := 0
	for _, r := range line {
		n := utf16.RuneLen(r)
		if units+n > maxChars {
			if units < maxChars {
				b.WriteRune(utf8.RuneError)
			}
			break
		}
		b.WriteRune(r)
		units += n
	}
	return b.String() + "... [truncated]", true
}
