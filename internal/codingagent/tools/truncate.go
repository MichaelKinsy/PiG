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

	"github.com/MichaelKinsy/PiG/internal/truncate"
	"github.com/MichaelKinsy/PiG/tui"
)

// Mirrors upstream defaults exactly.
// These supersede the older 200_000 figure that lived in tools.go.
const (
	DefaultMaxBytesUpstream   = 50 * 1024 // 50 KB: upstream DEFAULT_MAX_BYTES
	DefaultMaxLinesUpstream   = 2000      // upstream DEFAULT_MAX_LINES
	GrepMaxLineLengthUpstream = 500       // upstream GREP_MAX_LINE_LENGTH
)

// TruncationResult is upstream TruncationResult (truncate.ts). Its definition lives in the tui package, which BashExecutionComponent.SetComplete
// takes, and this package imports tui.
type TruncationResult = tui.TruncationResult

// shareTruncatedContent points tr.Content at the start of output, the text a tool returns, when output begins with it. Pi's tools return
// `truncation.content + notice` and keep truncation in details (read.ts, ls.ts, find.ts, grep.ts, bash.ts formatOutput); V8 makes that sum
// a cons string over truncation.content, so the details and the text hold the kept output once. Go concatenation copies it, so the
// details would hold a second copy for as long as the session and the tool card keep the result.
func shareTruncatedContent(tr *TruncationResult, output string) {
	if tr != nil && strings.HasPrefix(output, tr.Content) {
		tr.Content = output[:len(tr.Content)]
	}
}

// TruncationOptions is upstream TruncationOptions (truncate.ts:40): the limits truncateHead and truncateTail apply. A nil
// limit takes its default, like an omitted upstream option; a limit of 0 is used as given.
type TruncationOptions struct {
	// MaxLines defaults to DefaultMaxLines.
	MaxLines *int
	// MaxBytes defaults to DefaultMaxBytes.
	MaxBytes *int
}

// limits resolves the options to the line and byte limits to apply (truncate.ts:79-80).
func (options TruncationOptions) limits() (maxBytes, maxLines int) {
	maxBytes, maxLines = DefaultMaxBytes, DefaultMaxLines
	if options.MaxBytes != nil {
		maxBytes = *options.MaxBytes
	}
	if options.MaxLines != nil {
		maxLines = *options.MaxLines
	}
	return maxBytes, maxLines
}

// truncationLimits is TruncationOptions with both limits set.
func truncationLimits(maxBytes, maxLines int) TruncationOptions {
	return TruncationOptions{MaxBytes: &maxBytes, MaxLines: &maxLines}
}

// TruncateTail keeps the last lines/bytes of content. Used for bash
// output (errors and final results live at the end).
func TruncateTail(content string, options TruncationOptions) TruncationResult {
	maxBytes, maxLines := options.limits()
	return truncationResult(truncate.TruncateTail(content, maxBytes, maxLines), maxBytes, maxLines)
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
func TruncateHead(content string, options TruncationOptions) TruncationResult {
	maxBytes, maxLines := options.limits()
	return truncationResult(truncate.TruncateHead(content, maxBytes, maxLines), maxBytes, maxLines)
}

// truncationResult is a shared truncation result with the limits that produced it.
func truncationResult(r truncate.Result, maxBytes, maxLines int) TruncationResult {
	return TruncationResult{
		Content:               r.Content,
		Truncated:             r.Truncated,
		TruncatedBy:           r.TruncatedBy,
		TotalLines:            r.TotalLines,
		TotalBytes:            r.TotalBytes,
		OutputLines:           r.OutputLines,
		OutputBytes:           r.OutputBytes,
		LastLinePartial:       r.LastLinePartial,
		FirstLineExceedsLimit: r.FirstLineExceedsLimit,
		MaxLines:              maxLines,
		MaxBytes:              maxBytes,
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
	//portlint:allow fusedmuladd f is bytes/2^10 or bytes/2^20 (formatSize), so f*10 is exact and fusion cannot move the rounding
	scaled := int64(f*10 + 0.5)
	whole := scaled / 10
	frac := scaled % 10
	return itoa(int(whole)) + "." + string('0'+byte(frac)) + suffix
}

// TruncateLineResult is upstream's `{ text: string; wasTruncated: boolean }`.
type TruncateLineResult struct {
	Text         string
	WasTruncated bool
}

// TruncateLine mirrors upstream truncateLine: a line longer than maxChars
// UTF-16 code units (JavaScript string length) is cut to maxChars units plus
// "... [truncated]". A cut inside a surrogate pair leaves U+FFFD, as the
// lone surrogate JavaScript would keep serializes to.
func TruncateLine(line string, maxChars int) TruncateLineResult {
	if maxChars <= 0 {
		maxChars = GrepMaxLineLengthUpstream
	}
	if jsLength(line) <= maxChars {
		return TruncateLineResult{Text: line}
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
	return TruncateLineResult{Text: b.String() + truncatedLineSuffix, WasTruncated: true}
}

// truncatedLineSuffix ends a line TruncateLine shortened (truncate.ts truncateLine).
const truncatedLineSuffix = "... [truncated]"

// MiddleTruncationResult mirrors upstream MiddleTruncationResult.
type MiddleTruncationResult struct {
	// Content is the start and end of the input with a `…N chars truncated…`
	// marker between them.
	Content      string
	Truncated    bool
	RemovedChars int
	TotalBytes   int
	TotalLines   int
}

// TruncateMiddle mirrors upstream truncateMiddle: keep the start and the end
// of content, half of maxBytes each (the tail takes the odd byte), and replace
// the middle with a `…N chars truncated…` marker, like Codex does for tool
// output. It cuts only at character boundaries.
func TruncateMiddle(content string, maxBytes int) MiddleTruncationResult {
	totalLines := truncate.CountLines(content)
	if len(content) <= maxBytes {
		return MiddleTruncationResult{Content: content, TotalBytes: len(content), TotalLines: totalLines}
	}
	// Continuation bytes (10xxxxxx) are not character starts.
	isBoundary := func(index int) bool { return index >= len(content) || content[index]&0xc0 != 0x80 }
	headEnd := maxBytes / 2
	for headEnd > 0 && !isBoundary(headEnd) {
		headEnd--
	}
	tailStart := len(content) - (maxBytes - maxBytes/2)
	for tailStart < len(content) && !isBoundary(tailStart) {
		tailStart++
	}
	removedChars := utf8.RuneCountInString(content[headEnd:tailStart])
	return MiddleTruncationResult{
		Content:      content[:headEnd] + "…" + itoa(removedChars) + " chars truncated…" + content[tailStart:],
		Truncated:    true,
		RemovedChars: removedChars,
		TotalBytes:   len(content),
		TotalLines:   totalLines,
	}
}
