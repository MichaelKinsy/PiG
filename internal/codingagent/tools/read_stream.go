package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/utf8stream"
)

// readStreamChunk is the size of the chunks a streamed read pulls, as the 64 KB chunks fs.createReadStream yields.
const readStreamChunk = 64 * 1024

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
		decoder        utf8stream.Decoder
		storedLines    []string
		storedBytes    int
		storedPastCaps bool
		line           int
		selectedCount  int
		selectedBytes  int
		firstLineBytes int
		lastLineEmpty  = true
		// The selected line being read: its text while it is stored, and its size.
		storing     = true
		pieces      []string
		lineBytes   int
		lineHasText bool
	)

	appendToLine := func(piece string) {
		lineHasText = true
		if !storing {
			return
		}
		// A line over the byte cap is never shown, and a first line over it is only measured: never store all of a 1 GB line.
		if lineBytes <= DefaultMaxBytes {
			pieces = append(pieces, piece)
		}
		lineBytes += len(piece)
	}
	finishLine := func() {
		if storing {
			if selectedCount == 0 {
				firstLineBytes = lineBytes
			}
			// Storing continues one line past the caps: TruncateHead ignores a trailing empty line, so the line after the cap must be
			// there for it to decide truncation as for the full text.
			if len(storedLines) > DefaultMaxLines || storedBytes > DefaultMaxBytes {
				storedPastCaps = true
			}
			text := strings.Join(pieces, "")
			storedBytes += len(text)
			if len(storedLines) > 0 {
				storedBytes++
			}
			storedLines = append(storedLines, text)
		}
		selectedCount++
		lastLineEmpty = !lineHasText
		storing = !storedPastCaps
		pieces = pieces[:0]
		lineBytes = 0
		lineHasText = false
	}
	// selectChunk reads the lines of one chunk of bytes. Only the lines of the selection are decoded: a line that ends the chunk is
	// decoded as a stream, and a newline flushes the decoder, as an invalid sequence before it becomes U+FFFD before the newline.
	selectChunk := func(chunk []byte) {
		position := 0
		for {
			selected := line >= startLine && line < endLine
			newline := bytes.IndexByte(chunk[position:], '\n')
			end := len(chunk)
			if newline != -1 {
				newline += position
				end = newline
			}
			if selected && (end > position || newline != -1) {
				text := decoder.Decode(chunk[position:end], newline == -1)
				if text != "" {
					selectedBytes += len(text)
					appendToLine(text)
				}
			}
			if newline == -1 {
				return
			}
			if selected {
				finishLine()
				// The joined selected lines hold a newline between them.
				if line+1 < endLine {
					selectedBytes++
				}
			}
			line++
			position = newline + 1
		}
	}

	buf := make([]byte, readStreamChunk)
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
	if line >= startLine && line < endLine {
		if text := decoder.Decode(nil, false); text != "" {
			selectedBytes += len(text)
			appendToLine(text)
		}
		finishLine()
	}
	truncation := TruncateHead(strings.Join(storedLines, "\n"), TruncationOptions{})
	// TruncateHead's counts for the joined selected lines, which drop a trailing empty line.
	truncation.TotalLines = 0
	if selectedBytes != 0 {
		truncation.TotalLines = selectedCount
		if lastLineEmpty {
			truncation.TotalLines--
		}
	}
	truncation.TotalBytes = selectedBytes
	return selectedLines{totalFileLines: line + 1, truncation: truncation, firstLineBytes: firstLineBytes}, nil
}

// afterReadChunkHook runs after a streamed read takes a chunk, while the file is open and partly read; tests use it to change the file
// mid-read.
var afterReadChunkHook func()
