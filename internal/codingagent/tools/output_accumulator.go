// Streaming output accumulator for the shell tools.
//
// Mirrors upstream packages/coding-agent/src/core/tools/output-accumulator.ts.
package tools

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"strconv"
	"sync"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/outputfiles"
	"github.com/MichaelKinsy/PiG/internal/truncate"
)

// OutputSnapshot mirrors upstream OutputSnapshot.
type OutputSnapshot struct {
	Content        string
	Truncation     TruncationResult
	FullOutputPath string
}

// OutputAccumulator incrementally tracks streaming output with bounded
// memory. It decodes chunks with a streaming UTF-8 decoder (so a character
// split across reads survives), keeps only a decoded tail for snapshots, and
// writes the raw bytes to a temp file once the full output must be kept.
// Methods are safe for concurrent use.
type OutputAccumulator struct {
	mu sync.Mutex

	maxLines        int
	maxBytes        int
	maxRollingBytes int
	tempFilePrefix  string
	decoder         utf8StreamDecoder

	// raw holds the raw bytes until the full-output file exists. Append copies a chunk into it, since the caller's slice is only lent for the call.
	raw []byte
	// tail is the decoded rolling tail as UTF-8 bytes: a chunk is decoded straight onto its end, so appending costs the chunk, not a copy of the tail (a string concatenation copied up to 4 maxBytes per chunk), and a snapshot makes the one string it keeps.
	tail                     []byte
	tailStartsAtLineBoundary bool
	totalRawBytes            int
	totalDecodedBytes        int
	completedLines           int
	totalLines               int
	currentLineBytes         int
	lastCompletedLineBytes   int
	hasOpenLine              bool
	finished                 bool

	tempFilePath string
	tempFile     *os.File
	// tempWriter coalesces the raw chunks written to tempFile: a syscall per chunk was most of the cost of a small chunk. It is flushed whenever the file is read or its path is handed out (Snapshot, ReadFullOutput) and before the file closes, so the file holds every appended byte whenever anything can see it.
	tempWriter *bufio.Writer

	// snapshot is the last snapshot taken and snapshotBytes the decoded byte count it saw: a snapshot depends only on the decoded output and the file path,
	// so the final update and the tool result share one.
	snapshot      OutputSnapshot
	snapshotBytes int
	hasSnapshot   bool
}

// NewOutputAccumulator mirrors `new OutputAccumulator({ tempFilePrefix })`
// with the default line and byte limits. An empty prefix means "pi-output".
func NewOutputAccumulator(tempFilePrefix string) *OutputAccumulator {
	return newOutputAccumulator(DefaultMaxLinesUpstream, DefaultMaxBytesUpstream, tempFilePrefix)
}

func newOutputAccumulator(maxLines, maxBytes int, tempFilePrefix string) *OutputAccumulator {
	if tempFilePrefix == "" {
		tempFilePrefix = "pi-output"
	}
	return &OutputAccumulator{
		maxLines:                 maxLines,
		maxBytes:                 maxBytes,
		maxRollingBytes:          max(maxBytes*2, 1),
		tempFilePrefix:           tempFilePrefix,
		tailStartsAtLineBoundary: true,
		decoder:                  utf8StreamDecoder{stripBOM: true},
	}
}

// errAppendAfterFinish is the error output-accumulator.ts:66 throws.
var errAppendAfterFinish = errors.New("Cannot append to a finished output accumulator")

// Append adds a raw output chunk; once Finish has run it fails like upstream's append(), which throws. Append keeps no reference to data: the reader
// lends its read buffer for the call.
//
//perf:hot
func (a *OutputAccumulator) Append(data []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return errAppendAfterFinish
	}
	a.totalRawBytes += len(data)
	a.decodeOntoTail(data, true)
	if a.tempFile != nil || a.shouldUseTempFile() {
		a.ensureTempFile()
		if a.tempWriter != nil {
			_, _ = a.tempWriter.Write(data)
		}
	} else {
		a.raw = append(a.raw, data...)
	}
	return nil
}

// Finish flushes the decoder; later appends fail.
func (a *OutputAccumulator) Finish() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return
	}
	a.finished = true
	a.decodeOntoTail(nil, false)
	if a.shouldUseTempFile() {
		a.ensureTempFile()
	}
}

// Snapshot mirrors upstream snapshot({ persistIfTruncated }). It copies only the kept text, and a snapshot of output that has not changed since the last
// one returns that one.
func (a *OutputAccumulator) Snapshot(persistIfTruncated bool) OutputSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	truncated := a.totalLines > a.maxLines || a.totalDecodedBytes > a.maxBytes
	if persistIfTruncated && truncated {
		a.ensureTempFile()
	}
	a.flushTempFile()
	if a.hasSnapshot && a.snapshotBytes == a.totalDecodedBytes && a.snapshot.FullOutputPath == a.tempFilePath {
		return a.snapshot
	}
	tr := truncationResult(truncate.TruncateTailBytes(a.snapshotText(), a.maxBytes, a.maxLines), a.maxBytes, a.maxLines)
	truncatedBy := ""
	if truncated {
		truncatedBy = tr.TruncatedBy
		if truncatedBy == "" {
			truncatedBy = "lines"
			if a.totalDecodedBytes > a.maxBytes {
				truncatedBy = "bytes"
			}
		}
	}
	tr.Truncated = truncated
	tr.TruncatedBy = truncatedBy
	tr.TotalLines = a.totalLines
	tr.TotalBytes = a.totalDecodedBytes
	a.snapshot = OutputSnapshot{Content: tr.Content, Truncation: tr, FullOutputPath: a.tempFilePath}
	a.snapshotBytes, a.hasSnapshot = a.totalDecodedBytes, true
	return a.snapshot
}

// CloseTempFile closes the full-output file, if one was opened.
func (a *OutputAccumulator) CloseTempFile() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tempFile == nil {
		return nil
	}
	f := a.tempFile
	flushErr := a.tempWriter.Flush()
	a.tempFile, a.tempWriter = nil, nil
	if err := f.Close(); err != nil {
		return err
	}
	return flushErr
}

// LastLineBytes mirrors upstream getLastLineBytes.
//
// pig divergence (D153): the size is that of the last line the output counts: the open line, or, when the output
// ends with a newline, the line that newline ended. Upstream returns the bytes after the final newline, so the
// notice for a long last line that ends in a newline reads "line is 0B" (Pi #10015).
func (a *OutputAccumulator) LastLineBytes() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.hasOpenLine {
		return a.currentLineBytes
	}
	return a.lastCompletedLineBytes
}

// decodeOntoTail decodes data onto the end of the tail and counts what it added.
func (a *OutputAccumulator) decodeOntoTail(data []byte, stream bool) {
	start := len(a.tail)
	a.tail = a.decoder.appendDecode(a.tail, data, stream)
	text := a.tail[start:]
	size := len(text)
	if size == 0 {
		return
	}
	a.totalDecodedBytes += size
	newlines := bytes.Count(text, newline)
	if newlines == 0 {
		a.currentLineBytes += size
		a.hasOpenLine = true
	} else {
		a.completedLines += newlines
		last := bytes.LastIndexByte(text, '\n')
		if newlines == 1 {
			a.lastCompletedLineBytes = a.currentLineBytes + last
		} else {
			a.lastCompletedLineBytes = last - 1 - bytes.LastIndexByte(text[:last], '\n')
		}
		a.currentLineBytes = size - last - 1
		a.hasOpenLine = a.currentLineBytes != 0
	}
	a.totalLines = a.completedLines
	if a.hasOpenLine {
		a.totalLines++
	}
	if len(a.tail) > a.maxRollingBytes*2 {
		a.trimTail()
	}
}

var newline = []byte{'\n'}

func (a *OutputAccumulator) trimTail() {
	buffer := a.tail
	if len(buffer) <= a.maxRollingBytes {
		return
	}
	start := len(buffer) - a.maxRollingBytes
	for start < len(buffer) && buffer[start]&0xc0 == 0x80 {
		start++
	}
	if start != 0 {
		a.tailStartsAtLineBoundary = buffer[start-1] == '\n'
	}
	a.tail = buffer[:copy(buffer, buffer[start:])]
}

// snapshotText is the tail a snapshot truncates: it starts at a line boundary once the tail has been trimmed. The slice aliases the tail.
func (a *OutputAccumulator) snapshotText() []byte {
	tail := a.tail
	if !a.tailStartsAtLineBoundary {
		if i := bytes.IndexByte(tail, '\n'); i >= 0 {
			tail = tail[i+1:]
		}
	}
	return tail
}

func (a *OutputAccumulator) shouldUseTempFile() bool {
	return a.totalRawBytes > a.maxBytes || a.totalDecodedBytes > a.maxBytes || a.totalLines > a.maxLines
}

// flushTempFile writes the buffered raw chunks to the full-output file.
func (a *OutputAccumulator) flushTempFile() {
	if a.tempWriter != nil {
		_ = a.tempWriter.Flush()
	}
}

func (a *OutputAccumulator) ensureTempFile() {
	if a.tempFilePath != "" {
		return
	}
	// Upstream's write stream reports open failures asynchronously and keeps
	// the path; a failed create likewise only loses the file contents.
	path, f, err := outputfiles.CreateStream(a.tempFilePrefix, ".log")
	a.tempFilePath = path
	if err == nil {
		a.tempFile = f
		a.tempWriter = bufio.NewWriter(f)
		_, _ = a.tempWriter.Write(a.raw)
	}
	a.raw = nil
}

// utf8StreamDecoder mirrors the WHATWG UTF-8 decoder behind TextDecoder with
// { stream: true }: an incomplete trailing sequence waits for the next chunk,
// and each maximal invalid subpart becomes one U+FFFD.
//
// With stripBOM set it also mirrors TextDecoder's default ignoreBOM: false,
// dropping a U+FEFF that is the stream's first code point, even when its
// bytes arrive split across chunks. Buffer.toString (file reads) keeps the
// BOM, so those callers leave stripBOM unset.
type utf8StreamDecoder struct {
	stripBOM    bool
	started     bool
	codePoint   rune
	bytesSeen   int
	bytesNeeded int
	lower       byte
	upper       byte
}

// decode returns the decoding of data. Well-formed UTF-8 decodes to itself, so it is copied once; anything else runs the decoder.
func (d *utf8StreamDecoder) decode(data []byte, stream bool) string {
	if d.bytesNeeded == 0 && utf8.Valid(data) {
		if !d.started && len(data) > 0 {
			d.started = true
			if d.stripBOM && bytes.HasPrefix(data, utf8BOM) {
				data = data[len(utf8BOM):]
			}
		}
		return string(data)
	}
	return string(d.appendDecode(make([]byte, 0, len(data)+utf8.UTFMax), data, stream))
}

var utf8BOM = []byte("\uFEFF")

// appendDecode appends the decoding of data to dst. Between characters, the well-formed prefix of a chunk that ends at a character boundary is appended
// as it is; the decoder walks only what is left (an incomplete last character, or a chunk with an invalid sequence).
//
//perf:hot
func (d *utf8StreamDecoder) appendDecode(dst, data []byte, stream bool) []byte {
	if d.lower == 0 {
		d.lower, d.upper = 0x80, 0xBF
	}
	if d.started && d.bytesNeeded == 0 {
		if whole := len(data) - incompleteSuffix(data); utf8.Valid(data[:whole]) {
			dst = append(dst, data[:whole]...)
			data = data[whole:]
		}
	}
	for i := 0; i < len(data); i++ {
		c := data[i]
		if d.bytesNeeded == 0 {
			if d.started && c <= 0x7F {
				// A run of ASCII after the first code point is copied whole: no BOM check applies and each byte is its own code point.
				run := i + 1
				for run < len(data) && data[run] <= 0x7F {
					run++
				}
				dst = append(dst, data[i:run]...)
				i = run - 1
				continue
			}
			switch {
			case c <= 0x7F:
				dst = d.emit(dst, rune(c))
			case c >= 0xC2 && c <= 0xDF:
				d.bytesNeeded, d.codePoint = 1, rune(c&0x1F)
			case c >= 0xE0 && c <= 0xEF:
				switch c {
				case 0xE0:
					d.lower = 0xA0
				case 0xED:
					d.upper = 0x9F
				}
				d.bytesNeeded, d.codePoint = 2, rune(c&0x0F)
			case c >= 0xF0 && c <= 0xF4:
				switch c {
				case 0xF0:
					d.lower = 0x90
				case 0xF4:
					d.upper = 0x8F
				}
				d.bytesNeeded, d.codePoint = 3, rune(c&0x07)
			default:
				dst = d.emit(dst, '�')
			}
			continue
		}
		if c < d.lower || c > d.upper {
			d.reset()
			dst = d.emit(dst, '�')
			i-- // reprocess this byte as the start of a new sequence
			continue
		}
		d.lower, d.upper = 0x80, 0xBF
		d.codePoint = d.codePoint<<6 | rune(c&0x3F)
		d.bytesSeen++
		if d.bytesSeen == d.bytesNeeded {
			dst = d.emit(dst, d.codePoint)
			d.reset()
		}
	}
	if !stream && d.bytesNeeded != 0 {
		d.reset()
		dst = d.emit(dst, '�')
	}
	return dst
}

// emit appends a decoded code point, dropping a BOM that starts the stream when stripBOM is set.
func (d *utf8StreamDecoder) emit(dst []byte, r rune) []byte {
	if !d.started {
		d.started = true
		if d.stripBOM && r == '\uFEFF' {
			return dst
		}
	}
	return utf8.AppendRune(dst, r)
}

// incompleteSuffix is the length of the multi-byte character data ends inside of, 0 when data ends at a character boundary. A malformed ending counts
// as complete: the caller then finds the data malformed and decodes it in full.
func incompleteSuffix(data []byte) int {
	for back := 1; back <= utf8.UTFMax-1 && back <= len(data); back++ {
		c := data[len(data)-back]
		switch {
		case c < 0x80:
			return 0
		case c >= 0xC0:
			if back < sequenceLength(c) {
				return back
			}
			return 0
		}
	}
	return 0
}

// sequenceLength is the byte length a UTF-8 lead byte announces.
func sequenceLength(lead byte) int {
	switch {
	case lead >= 0xF0:
		return 4
	case lead >= 0xE0:
		return 3
	default:
		return 2
	}
}

func (d *utf8StreamDecoder) reset() {
	d.codePoint, d.bytesSeen, d.bytesNeeded = 0, 0, 0
	d.lower, d.upper = 0x80, 0xBF
}

// FullOutput mirrors upstream FullOutput (output-accumulator.ts): the complete
// output for callers that can take more than the display snapshot.
type FullOutput struct {
	Content string
	// Truncated reports whether Content omits part of the output.
	Truncated bool
}

// ReadFullOutput mirrors upstream readFullOutput: the complete output, for
// callers that can take more than the display snapshot. Call it after Finish
// and CloseTempFile. Output longer than maxBytes raw bytes keeps its first and
// last maxBytes/2 bytes around an omission marker. Both halves are cut at
// character boundaries: the head drops an incomplete trailing sequence and the
// tail skips leading continuation bytes. A full-output file that cannot be
// opened or read returns that error, as upstream's promise rejects.
func (a *OutputAccumulator) ReadFullOutput(maxBytes int) (FullOutput, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.flushTempFile()
	if a.tempFilePath == "" {
		decoder := utf8StreamDecoder{stripBOM: true}
		return FullOutput{Content: decoder.decode(a.raw, false)}, nil
	}
	file, err := os.Open(a.tempFilePath)
	if err != nil {
		return FullOutput{}, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return FullOutput{}, err
	}
	size := info.Size()
	if size <= int64(maxBytes) {
		data, err := io.ReadAll(file)
		if err != nil {
			return FullOutput{}, err
		}
		decoder := utf8StreamDecoder{stripBOM: true}
		return FullOutput{Content: decoder.decode(data, false)}, nil
	}
	headBytes := maxBytes / 2
	tailBytes := maxBytes - headBytes
	head := make([]byte, headBytes)
	tail := make([]byte, tailBytes)
	if _, err := file.ReadAt(head, 0); err != nil {
		return FullOutput{}, err
	}
	if _, err := file.ReadAt(tail, size-int64(tailBytes)); err != nil {
		return FullOutput{}, err
	}
	headDecoder := utf8StreamDecoder{stripBOM: true}
	headText := headDecoder.decode(head, true)
	tailStart := 0
	for tailStart < len(tail) && tail[tailStart]&0xc0 == 0x80 {
		tailStart++
	}
	tailDecoder := utf8StreamDecoder{stripBOM: true}
	tailText := tailDecoder.decode(tail[tailStart:], false)
	omitted := size - int64(headBytes) - int64(tailBytes)
	return FullOutput{Content: headText + "\n\n[... " + strconv.FormatInt(omitted, 10) + " bytes omitted ...]\n\n" + tailText, Truncated: true}, nil
}
