// Package truncate holds the line and byte walks of Pi's truncateHead and truncateTail (packages/coding-agent/src/core/tools/truncate.ts), shared by
// the tools' result truncation, the bash-execution block's context limit and durable's truncateHead, which import cycles keep in different packages.
//
// Both walks cut once. truncate.ts splits its input into lines and joins the kept ones with "\n", but the lines it keeps are contiguous, so the joined
// text is a substring of the input: the head ends where its last kept line ends, the tail starts where its first kept line starts and ends before a
// trailing newline. The cuts find those offsets by scanning newlines from one end, read at most maxBytes+1 bytes of the input, and return substrings, so
// a result allocates nothing.
package truncate

import (
	"bytes"
	"strings"
)

// The default limits of truncateHead and truncateTail.
const (
	DefaultMaxBytes = 50 * 1024 // upstream: packages/coding-agent/src/core/tools/truncate.ts:DEFAULT_MAX_BYTES
	DefaultMaxLines = 2_000     // upstream: packages/coding-agent/src/core/tools/truncate.ts:DEFAULT_MAX_LINES
)

// Result is truncateHead's or truncateTail's result: the kept content and what was cut.
type Result struct {
	Content               string
	Truncated             bool
	TruncatedBy           string // "lines" | "bytes" | ""
	TotalLines            int
	TotalBytes            int
	OutputLines           int
	OutputBytes           int
	LastLinePartial       bool
	FirstLineExceedsLimit bool
}

// CountLines is len(splitLinesForCounting(content)) (truncate.ts): the "\n"-separated lines, a trailing newline ending the last line rather than starting
// an empty one.
func CountLines(content string) int {
	if content == "" {
		return 0
	}
	lines := strings.Count(content, "\n")
	if content[len(content)-1] != '\n' {
		lines++
	}
	return lines
}

// HeadCut is truncateHead's walk over content's lines: it keeps whole lines from the start while they fit in maxLines lines and maxBytes bytes, counting
// a joining newline before every line but the first. The kept text is content[:End]. ByBytes reports that a line's bytes stopped the walk.
type HeadCut struct {
	End, Lines int
	ByBytes    bool
}

// CutHead walks content as truncateHead does; see HeadCut.
func CutHead(content string, maxBytes, maxLines int) HeadCut {
	if content == "" {
		return HeadCut{}
	}
	last := len(content)
	if content[last-1] == '\n' {
		last--
	}
	var cut HeadCut
	for cut.Lines < maxLines {
		start, extra := 0, 0
		if cut.Lines > 0 {
			if cut.End == last {
				break
			}
			start, extra = cut.End+1, 1
		}
		// The line fits if it has at most budget bytes, so the scan stops one byte past the budget.
		budget := maxBytes - cut.End - extra
		if budget < 0 {
			cut.ByBytes = true
			break
		}
		scanEnd := last
		if budget < last-start {
			scanEnd = start + budget + 1
		}
		end := scanEnd
		if i := strings.IndexByte(content[start:scanEnd], '\n'); i >= 0 {
			end = start + i
		}
		if end-start > budget {
			cut.ByBytes = true
			break
		}
		cut.End = end
		cut.Lines++
	}
	return cut
}

// FirstLineBytes is the byte length of content's first counted line (truncateHead's lines[0]), 0 for empty content.
func FirstLineBytes(content string) int {
	if i := strings.IndexByte(content, '\n'); i >= 0 {
		return i
	}
	return len(content)
}

// TailCut is truncateTail's walk over content's lines: it keeps whole lines from the end while they fit in maxLines lines and maxBytes bytes. When the
// last line alone exceeds maxBytes its end is kept, cut forward to a UTF-8 character boundary, and Partial is set. The kept text is content[Start:End];
// End excludes a trailing newline. ByBytes reports that a line's bytes stopped the walk.
type TailCut struct {
	Start, End, Lines int
	ByBytes, Partial  bool
}

// CutTail walks content as truncateTail does; see TailCut. It reads only the bytes it keeps plus the one before them, so it takes the accumulators'
// []byte tails as well as strings.
func CutTail[T ~string | ~[]byte](content T, maxBytes, maxLines int) TailCut {
	if len(content) == 0 {
		return TailCut{}
	}
	end := len(content)
	if content[end-1] == '\n' {
		end--
	}
	cut := TailCut{Start: end, End: end}
	for cut.Lines < maxLines {
		lineEnd, extra := end, 0
		if cut.Lines > 0 {
			if cut.Start == 0 {
				break
			}
			lineEnd, extra = cut.Start-1, 1
		}
		// The line fits if it has at most budget bytes, so the scan stops one byte past the budget: a longer line is cut or ends the walk whatever its
		// length.
		budget := maxBytes - (end - cut.Start) - extra
		floor := 0
		if budget < lineEnd {
			floor = lineEnd - budget - 1
		}
		lineStart := lineEnd
		for lineStart > floor && content[lineStart-1] != '\n' {
			lineStart--
		}
		if lineEnd-lineStart > budget {
			cut.ByBytes = true
			if cut.Lines == 0 {
				// The last line alone is too long: keep its end. A negative limit starts past the end, where Buffer.slice returns nothing.
				start := lineEnd - maxBytes
				if maxBytes < 0 {
					start = lineEnd
				}
				for start < lineEnd && content[start]&0xC0 == 0x80 {
					start++
				}
				cut.Start, cut.Lines, cut.Partial = start, 1, true
			}
			break
		}
		cut.Start = lineStart
		cut.Lines++
	}
	return cut
}

// TruncateHead keeps the first maxLines lines and maxBytes bytes of content, whichever limit is hit first, and never a partial line: when the first line
// alone exceeds maxBytes the content is empty and FirstLineExceedsLimit is set (truncate.ts truncateHead).
func TruncateHead(content string, maxBytes, maxLines int) Result {
	totalBytes, totalLines := len(content), CountLines(content)
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return Result{Content: content, TotalLines: totalLines, TotalBytes: totalBytes, OutputLines: totalLines, OutputBytes: totalBytes}
	}
	if totalLines > 0 && FirstLineBytes(content) > maxBytes {
		return Result{Truncated: true, TruncatedBy: "bytes", TotalLines: totalLines, TotalBytes: totalBytes, FirstLineExceedsLimit: true}
	}
	cut := CutHead(content, maxBytes, maxLines)
	truncatedBy := "lines"
	if cut.ByBytes && (cut.Lines < maxLines || cut.End > maxBytes) {
		truncatedBy = "bytes"
	}
	return Result{
		Content:     content[:cut.End],
		Truncated:   true,
		TruncatedBy: truncatedBy,
		TotalLines:  totalLines,
		TotalBytes:  totalBytes,
		OutputLines: cut.Lines,
		OutputBytes: cut.End,
	}
}

// TruncateTail keeps the last maxLines lines and maxBytes bytes of content, whichever limit is hit first (truncate.ts truncateTail). When the last line
// alone exceeds maxBytes, its end is kept and LastLinePartial is set.
func TruncateTail(content string, maxBytes, maxLines int) Result {
	totalBytes, totalLines := len(content), CountLines(content)
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return Result{Content: content, TotalLines: totalLines, TotalBytes: totalBytes, OutputLines: totalLines, OutputBytes: totalBytes}
	}
	cut := CutTail(content, maxBytes, maxLines)
	return tailResult(content[cut.Start:cut.End], cut, maxBytes, maxLines, totalBytes, totalLines)
}

// TruncateTailBytes is TruncateTail(string(content)), copying only the kept text.
func TruncateTailBytes(content []byte, maxBytes, maxLines int) Result {
	totalBytes, totalLines := len(content), 0
	if totalBytes > 0 {
		totalLines = bytes.Count(content, newline)
		if content[totalBytes-1] != '\n' {
			totalLines++
		}
	}
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return Result{Content: string(content), TotalLines: totalLines, TotalBytes: totalBytes, OutputLines: totalLines, OutputBytes: totalBytes}
	}
	cut := CutTail(content, maxBytes, maxLines)
	return tailResult(string(content[cut.Start:cut.End]), cut, maxBytes, maxLines, totalBytes, totalLines)
}

var newline = []byte{'\n'}

// tailResult is truncateTail's truncated result for the kept text of cut.
func tailResult(kept string, cut TailCut, maxBytes, maxLines, totalBytes, totalLines int) Result {
	// truncate.ts sets "lines" again after the walk when it kept maxLines lines within maxBytes, which a one-line limit reaches after a partial line.
	truncatedBy := "lines"
	if cut.ByBytes && (cut.Lines < maxLines || len(kept) > maxBytes) {
		truncatedBy = "bytes"
	}
	return Result{
		Content:         kept,
		Truncated:       true,
		TruncatedBy:     truncatedBy,
		TotalLines:      totalLines,
		TotalBytes:      totalBytes,
		OutputLines:     cut.Lines,
		OutputBytes:     len(kept),
		LastLinePartial: cut.Partial,
	}
}
