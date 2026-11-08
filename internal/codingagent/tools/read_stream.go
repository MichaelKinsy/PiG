package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/utf8stream"
)

// readStreamChunk is the size of the chunks a streamed read pulls, as the 64 KB chunks fs.createReadStream yields.
const readStreamChunk = 64 * 1024

// readChunkPool holds the chunk buffers of streamed reads, so a read of a small file does not allocate 64 KB for itself: the selected text is copied out of a chunk
// before the next read, and nothing holds the buffer once selectLinesFromStream returns.
var readChunkPool = sync.Pool{New: func() any {
	buf := make([]byte, readStreamChunk)
	return &buf
}}

// maxStreamedLine bounds the offset and limit a streamed read takes: larger values keep the whole-file path.
const maxStreamedLine = math.MaxInt32

// selectedLines is the lines a read shows with the file's line count; line counts follow text.split("\n") (read.ts SelectedLines).
type selectedLines struct {
	totalFileLines int
	truncation     TruncationResult
	// firstLineBytes is the size of the first selected line, for the notice when it alone exceeds the byte limit.
	firstLineBytes int
}

// isStreamableRange reports whether the range can be streamed (read.ts isStreamableRange). Other values (fractional, negative limit)
// keep the whole-file path, whose slicing defines what they select, for example limit -1 as all lines but the last.
func isStreamableRange(p readParams) bool {
	whole := func(v *float64, minimum float64) bool {
		return v == nil || (*v == math.Trunc(*v) && *v >= minimum && *v <= maxStreamedLine)
	}
	return whole(p.Offset, -maxStreamedLine) && whole(p.Limit, 0)
}

// readStreamedTextResult is the text branch of read.ts execute over a stream of the file's bytes: only the lines head truncation can
// show stay in memory, the rest are counted.
func readStreamedTextResult(ctx context.Context, p readParams, stream io.Reader) (agent.AgentToolResult, error) {
	startLine := 0
	if p.Offset != nil && *p.Offset != 0 {
		//portlint:allow numbers isStreamableRange bounds the offset to a whole number in int32 range
		startLine = int(math.Max(0, *p.Offset-1))
	}
	limit := -1
	if p.Limit != nil {
		//portlint:allow numbers isStreamableRange bounds the limit to a whole number in [0, int32 max]
		limit = int(*p.Limit)
	}
	selection, err := selectLinesFromStream(ctx, stream, startLine, limit)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	if startLine >= selection.totalFileLines {
		return agent.AgentToolResult{}, fmt.Errorf("Offset %s is beyond end of file (%d lines total)", jsNumber(*p.Offset), selection.totalFileLines)
	}
	// A user-specified limit is honored before truncation.
	userLimitedLines := 0
	if limit >= 0 {
		userLimitedLines = min(startLine+limit, selection.totalFileLines) - startLine
	}
	return readSelectionResult(p, float64(startLine), float64(userLimitedLines), selection.totalFileLines, selection.truncation, selection.firstLineBytes), nil
}

// selectLinesFromStream selects lines from a file read in chunks (read.ts selectLinesFromStream). Only lines TruncateHead can show are
// stored; the rest are counted, so memory does not grow with the file. limit is negative for none. It stops reading, with
// "Operation aborted", when ctx is done.
func selectLinesFromStream(ctx context.Context, stream io.Reader, startLine, limit int) (selectedLines, error) {
	endLine := math.MaxInt
	if limit >= 0 {
		endLine = startLine + limit
	}
	var (
		decoder utf8stream.Decoder
		// stored holds the stored lines joined by "\n": no string per line. completedBytes is its size without the selected line
		// being read, and storedLines counts the finished lines in it.
		stored         strings.Builder
		completedBytes int
		storedLines    int
		storedPastCaps bool
		// line is the line being read, 0-based, and selectedCount the selected lines finished.
		line           int
		selectedCount  int
		selectedBytes  int
		firstLineBytes int
		lastLineEmpty  = true
		// scratch takes the decoded text of a chunk's selected lines when decoding changes it.
		scratch []byte
		// The selected line being read: whether it is stored, whether its separator is in stored, and its size.
		storing     = true
		lineOpen    bool
		lineBytes   int
		lineHasText bool
	)
	newline := []byte{'\n'}

	// addPiece takes the decoded text of the selected line being read up to a newline or the end of a chunk.
	addPiece := func(text []byte) {
		if len(text) != 0 {
			selectedBytes += len(text)
			lineHasText = true
		}
		if !storing {
			return
		}
		if !lineOpen {
			lineOpen = true
			if storedLines > 0 {
				stored.WriteByte('\n')
			}
		}
		// A line over the byte cap is never shown, and a first line over it is only measured: never store all of a 1 GB line.
		if lineBytes <= DefaultMaxBytes {
			stored.Write(text)
		}
		lineBytes += len(text)
	}
	// finishLine ends the selected line being read at a newline or the end of the file.
	finishLine := func() {
		if storing {
			if selectedCount == 0 {
				firstLineBytes = lineBytes
			}
			// Storing continues one line past the caps: TruncateHead ignores a trailing empty line, so the line after the cap must be
			// there for it to decide truncation as for the full text.
			if storedLines > DefaultMaxLines || completedBytes > DefaultMaxBytes {
				storedPastCaps = true
			}
			if !lineOpen && storedLines > 0 {
				stored.WriteByte('\n')
			}
			storedLines++
			completedBytes = stored.Len()
		}
		selectedCount++
		line++
		lastLineEmpty = !lineHasText
		storing = !storedPastCaps
		lineOpen = false
		lineBytes = 0
		lineHasText = false
	}
	// storeWholeLines stores, in one write, the lines of text that end in a newline and cannot reach a cap of the stored lines, which
	// is every line while storedLines stays within DefaultMaxLines and the stored text within DefaultMaxBytes. text starts a line. It
	// returns the bytes taken, which end in the newline that separates the last line from the next, already stored.
	storeWholeLines := func(text []byte) int {
		taken, lines, lastLine := 0, 0, 0
		separator := 0
		if storedLines > 0 {
			separator = 1
		}
		for storedLines+lines < DefaultMaxLines {
			next := bytes.IndexByte(text[taken:], '\n')
			// The stored text, without the newline that follows the last line, stays within the byte cap.
			if next == -1 || completedBytes+separator+taken+next > DefaultMaxBytes {
				break
			}
			if selectedCount == 0 && lines == 0 {
				firstLineBytes = next
			}
			lastLine = next
			taken += next + 1
			lines++
		}
		if lines == 0 {
			return 0
		}
		if separator == 1 {
			stored.WriteByte('\n')
		}
		stored.Write(text[:taken])
		lineOpen = true
		storedLines += lines
		completedBytes = stored.Len() - 1
		selectedCount += lines
		selectedBytes += taken
		line += lines
		lastLineEmpty = lastLine == 0
		return taken
	}
	// selectText reads decoded text of selected lines: the text holds a newline between the lines. Lines that no longer show are
	// counted without a pass per line, and the line the text ends in stays open.
	selectText := func(text []byte) {
		// Storing ends a line past the caps, so about DefaultMaxBytes is the most this chunk adds.
		if room := DefaultMaxBytes + 1 - stored.Len(); storing && room > 0 {
			stored.Grow(min(len(text), room))
		}
		for storing {
			if !lineOpen {
				if taken := storeWholeLines(text); taken > 0 {
					text = text[taken:]
					continue
				}
			}
			next := bytes.IndexByte(text, '\n')
			if next == -1 {
				addPiece(text)
				return
			}
			addPiece(text[:next])
			finishLine()
			selectedBytes++
			text = text[next+1:]
		}
		selectedBytes += len(text)
		lines := bytes.Count(text, newline)
		if lines == 0 {
			lineHasText = lineHasText || len(text) != 0
			return
		}
		last := bytes.LastIndexByte(text, '\n')
		if lines == 1 {
			lastLineEmpty = !lineHasText && last == 0
		} else {
			lastLineEmpty = last-bytes.LastIndexByte(text[:last], '\n') == 1
		}
		selectedCount += lines
		line += lines
		lineHasText = len(text) > last+1
	}
	// selectChunk reads one chunk of bytes. Only the bytes of the selected lines are decoded, in one pass: a newline flushes the
	// decoder, as an invalid sequence before it becomes U+FFFD before the newline.
	selectChunk := func(chunk []byte) {
		position := 0
		for ; line < startLine; line++ {
			next := bytes.IndexByte(chunk[position:], '\n')
			if next == -1 {
				return
			}
			position += next + 1
		}
		if line >= endLine {
			line += bytes.Count(chunk[position:], newline)
			return
		}
		selection, ends := chunk[position:], false
		if limit >= 0 {
			// The newline that ends the last selected line is the remaining-th one.
			offset := 0
			for remaining := endLine - line; remaining > 0; remaining-- {
				next := bytes.IndexByte(selection[offset:], '\n')
				if next == -1 {
					break
				}
				offset += next
				if remaining == 1 {
					selection, ends = selection[:offset], true
					break
				}
				offset++
			}
		}
		// Valid text is its own decoding. Otherwise decoding into scratch takes as many bytes as the text has for valid parts.
		text := selection
		if !decoder.Passthrough(selection) {
			scratch = decoder.AppendDecode(scratch[:0], selection, !ends)
			text = scratch
		}
		selectText(text)
		if ends {
			finishLine()
			line += bytes.Count(chunk[position+len(selection)+1:], newline)
		}
	}

	bufp := readChunkPool.Get().(*[]byte)
	defer readChunkPool.Put(bufp)
	buf := *bufp
	for {
		if ctx.Err() != nil {
			return selectedLines{}, errors.New("Operation aborted")
		}
		n, err := stream.Read(buf)
		if hook := afterReadChunkHook; hook != nil {
			hook()
		}
		if n > 0 {
			selectChunk(buf[:n])
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return selectedLines{}, errors.New(NodeFSError(err, "read", ""))
		}
	}
	totalFileLines := line + 1
	if line >= startLine && line < endLine {
		scratch = decoder.AppendDecode(scratch[:0], nil, false)
		selectText(scratch)
		finishLine()
	}
	truncation := TruncateHead(stored.String(), TruncationOptions{})
	// TruncateHead's counts for the joined selected lines, which drop a trailing empty line.
	truncation.TotalLines = 0
	if selectedBytes != 0 {
		truncation.TotalLines = selectedCount
		if lastLineEmpty {
			truncation.TotalLines--
		}
	}
	truncation.TotalBytes = selectedBytes
	return selectedLines{totalFileLines: totalFileLines, truncation: truncation, firstLineBytes: firstLineBytes}, nil
}

// afterReadChunkHook runs after a streamed read takes a chunk, while the file is open and partly read; tests use it to change the file
// mid-read.
var afterReadChunkHook func()
