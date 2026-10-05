package env

// Ports packages/durable/src/env/line-scan.ts

import (
	"bytes"
	"errors"
)

const newline = 0x0a

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER.
const maxSafeInteger = 1<<53 - 1

// ErrInvalidLineRange is the error of a line range LineScanner refuses (upstream's RangeError).
var ErrInvalidLineRange = errors.New("Invalid line range")

// LineScanner computes a LineScan from a file's bytes fed in order, so an environment can scan any size in bounded
// memory. Decoded sizes use the same streaming WHATWG decoder as decoding the whole file at once.
type LineScanner struct {
	startLine int64
	// endLine is meaningful only when hasEnd; without it the selection reaches the end of the file.
	endLine int64
	hasEnd  bool

	position        int64
	newlines        int64
	lineStart       int64
	start           int64
	hasStart        bool
	end             int64
	hasSelectionEnd bool
	firstLineEnd    int64
	hasFirstEnd     bool
	lastLineStart   int64
	hasLastStart    bool
	selectedBytes   int64
	firstBytes      int64
	selection       *RangeDecoder
	firstLine       *RangeDecoder
	// head holds the first bytes until it is known whether they are a byte-order mark.
	head    []byte
	headSet bool
	bom     bool
}

// NewLineScanner returns a scanner for lines [startLine, endLine), 0-based. startLine must be a non-negative safe
// integer and endLine, when not nil, a safe integer above startLine.
func NewLineScanner(startLine int64, endLine *int64) (*LineScanner, error) {
	if startLine < 0 || startLine > maxSafeInteger {
		return nil, ErrInvalidLineRange
	}
	scanner := &LineScanner{startLine: startLine, head: make([]byte, 0, 3), headSet: true}
	if endLine != nil {
		if *endLine <= startLine || *endLine > maxSafeInteger {
			return nil, ErrInvalidLineRange
		}
		scanner.endLine, scanner.hasEnd = *endLine, true
	}
	if startLine == 0 {
		scanner.begin(0)
	}
	return scanner, nil
}

// decodedBytes is the UTF-8 byte length of decoded text.
func decodedBytes(text string) int64 { return int64(len(text)) }

// Push feeds the next bytes of the file.
func (scanner *LineScanner) Push(chunk []byte) {
	if scanner.headSet {
		take := min(3-len(scanner.head), len(chunk))
		scanner.head = append(scanner.head, chunk[:take]...)
		if len(scanner.head) < 3 {
			return
		}
		scanner.releaseHead()
		chunk = chunk[take:]
	}
	scanner.process(chunk)
}

func (scanner *LineScanner) releaseHead() {
	head := scanner.head
	scanner.head, scanner.headSet = nil, false
	scanner.bom = StartsWithBom(head)
	scanner.process(head)
}

// isLastSelected reports whether line is the last selected line.
func (scanner *LineScanner) isLastSelected(line int64) bool {
	return scanner.hasEnd && line == scanner.endLine-1
}

func (scanner *LineScanner) process(chunk []byte) {
	base := scanner.position
	from := 0
	for offset := 0; ; {
		found := bytes.IndexByte(chunk[offset:], newline)
		if found < 0 {
			break
		}
		index := offset + found
		// The newline ends line scanner.newlines. It belongs to the selection between selected lines only.
		scanner.feed(chunk, base, from, index)
		line := scanner.newlines
		position := base + int64(index)
		if line == scanner.startLine {
			scanner.endFirstLine(position)
		}
		if scanner.isLastSelected(line) {
			scanner.endSelection(position)
		}
		scanner.feed(chunk, base, index, index+1)
		from = index + 1
		scanner.newlines++
		scanner.lineStart = position + 1
		if scanner.newlines == scanner.startLine {
			scanner.begin(scanner.lineStart)
		}
		if scanner.isLastSelected(scanner.newlines) {
			scanner.lastLineStart, scanner.hasLastStart = scanner.lineStart, true
		}
		offset = index + 1
	}
	scanner.feed(chunk, base, from, len(chunk))
	scanner.position += int64(len(chunk))
}

// Finish returns the scan of the bytes pushed so far, taking them as the whole file.
func (scanner *LineScanner) Finish() LineScan {
	if scanner.headSet {
		scanner.releaseHead()
	}
	size := scanner.position
	if !scanner.hasStart {
		return LineScan{Newlines: scanner.newlines, Start: size, End: size, FirstLineEnd: size, LastLineStart: size}
	}
	if !scanner.hasFirstEnd {
		scanner.endFirstLine(size)
	}
	if !scanner.hasSelectionEnd {
		scanner.endSelection(size)
	}
	// A selection that reaches past the last line ends with the last line.
	lastLineStart := scanner.lineStart
	if scanner.hasLastStart {
		lastLineStart = scanner.lastLineStart
	}
	return LineScan{
		Newlines:       scanner.newlines,
		Start:          scanner.start,
		End:            scanner.end,
		FirstLineEnd:   scanner.firstLineEnd,
		LastLineStart:  lastLineStart,
		SelectedBytes:  scanner.selectedBytes,
		FirstLineBytes: scanner.firstBytes,
	}
}

func (scanner *LineScanner) begin(start int64) {
	scanner.start, scanner.hasStart = start, true
	if scanner.isLastSelected(scanner.startLine) {
		scanner.lastLineStart, scanner.hasLastStart = start, true
	}
	scanner.selection = NewRangeDecoder()
	scanner.firstLine = NewRangeDecoder()
}

func (scanner *LineScanner) endFirstLine(position int64) {
	scanner.firstLineEnd, scanner.hasFirstEnd = position, true
	if scanner.firstLine != nil {
		scanner.firstBytes += decodedBytes(scanner.firstLine.Flush())
	}
	scanner.firstLine = nil
}

func (scanner *LineScanner) endSelection(position int64) {
	scanner.end, scanner.hasSelectionEnd = position, true
	if scanner.selection != nil {
		scanner.selectedBytes += decodedBytes(scanner.selection.Flush())
	}
	scanner.selection = nil
}

// feed gives chunk[from:to], which starts at file offset base, to the decoders of the ranges still open.
func (scanner *LineScanner) feed(chunk []byte, base int64, from, to int) {
	// Decoding the whole file drops a leading byte-order mark.
	if scanner.bom && base+int64(from) < 3 {
		from = int(min(int64(to), 3-base))
	}
	if to <= from {
		return
	}
	bytes := chunk[from:to]
	if scanner.selection != nil {
		scanner.selectedBytes += decodedBytes(scanner.selection.Decode(bytes))
	}
	if scanner.firstLine != nil {
		scanner.firstBytes += decodedBytes(scanner.firstLine.Decode(bytes))
	}
}
