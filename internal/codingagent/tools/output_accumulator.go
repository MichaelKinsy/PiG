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

	"github.com/MichaelKinsy/PiG/internal/outputfiles"
	"github.com/MichaelKinsy/PiG/internal/truncate"
	"github.com/MichaelKinsy/PiG/internal/utf8stream"
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
	decoder         utf8stream.Decoder

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
		decoder:                  utf8stream.Decoder{StripBOM: true},
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
	a.tail = a.decoder.AppendDecode(a.tail, data, stream)
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
		decoder := utf8stream.Decoder{StripBOM: true}
		return FullOutput{Content: decoder.Decode(a.raw, false)}, nil
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
		decoder := utf8stream.Decoder{StripBOM: true}
		return FullOutput{Content: decoder.Decode(data, false)}, nil
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
	headDecoder := utf8stream.Decoder{StripBOM: true}
	headText := headDecoder.Decode(head, true)
	tailStart := 0
	for tailStart < len(tail) && tail[tailStart]&0xc0 == 0x80 {
		tailStart++
	}
	tailDecoder := utf8stream.Decoder{StripBOM: true}
	tailText := tailDecoder.Decode(tail[tailStart:], false)
	omitted := size - int64(headBytes) - int64(tailBytes)
	return FullOutput{Content: headText + "\n\n[... " + strconv.FormatInt(omitted, 10) + " bytes omitted ...]\n\n" + tailText, Truncated: true}, nil
}
