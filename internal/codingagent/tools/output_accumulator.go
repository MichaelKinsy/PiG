// Streaming output accumulator for the shell tools.
//
// Mirrors upstream packages/coding-agent/src/core/tools/output-accumulator.ts.
package tools

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

	rawChunks                [][]byte
	tailText                 string
	tailBytes                int
	tailStartsAtLineBoundary bool
	totalRawBytes            int
	totalDecodedBytes        int
	completedLines           int
	totalLines               int
	currentLineBytes         int
	hasOpenLine              bool
	finished                 bool

	tempFilePath string
	tempFile     *os.File
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

// Append adds a raw output chunk.
func (a *OutputAccumulator) Append(data []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return
	}
	a.totalRawBytes += len(data)
	a.appendDecodedText(a.decoder.decode(data, true))
	if a.tempFile != nil || a.shouldUseTempFile() {
		a.ensureTempFile()
		if a.tempFile != nil {
			_, _ = a.tempFile.Write(data)
		}
	} else if len(data) > 0 {
		a.rawChunks = append(a.rawChunks, append([]byte(nil), data...))
	}
}

// Finish flushes the decoder; later appends are ignored.
func (a *OutputAccumulator) Finish() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return
	}
	a.finished = true
	a.appendDecodedText(a.decoder.decode(nil, false))
	if a.shouldUseTempFile() {
		a.ensureTempFile()
	}
}

// Snapshot mirrors upstream snapshot({ persistIfTruncated }).
func (a *OutputAccumulator) Snapshot(persistIfTruncated bool) OutputSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	tr := TruncateTail(a.getSnapshotText(), a.maxBytes, a.maxLines)
	truncated := a.totalLines > a.maxLines || a.totalDecodedBytes > a.maxBytes
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
	tr.MaxLines = a.maxLines
	tr.MaxBytes = a.maxBytes
	if persistIfTruncated && truncated {
		a.ensureTempFile()
	}
	return OutputSnapshot{Content: tr.Content, Truncation: tr, FullOutputPath: a.tempFilePath}
}

// CloseTempFile closes the full-output file, if one was opened.
func (a *OutputAccumulator) CloseTempFile() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tempFile == nil {
		return nil
	}
	f := a.tempFile
	a.tempFile = nil
	return f.Close()
}

// LastLineBytes mirrors upstream getLastLineBytes.
func (a *OutputAccumulator) LastLineBytes() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.currentLineBytes
}

func (a *OutputAccumulator) appendDecodedText(text string) {
	if text == "" {
		return
	}
	bytes := len(text)
	a.totalDecodedBytes += bytes
	a.tailText += text
	a.tailBytes += bytes
	if a.tailBytes > a.maxRollingBytes*2 {
		a.trimTail()
	}
	newlines := strings.Count(text, "\n")
	if newlines == 0 {
		a.currentLineBytes += bytes
		a.hasOpenLine = true
	} else {
		a.completedLines += newlines
		tail := text[strings.LastIndexByte(text, '\n')+1:]
		a.currentLineBytes = len(tail)
		a.hasOpenLine = tail != ""
	}
	a.totalLines = a.completedLines
	if a.hasOpenLine {
		a.totalLines++
	}
}

func (a *OutputAccumulator) trimTail() {
	buffer := a.tailText
	if len(buffer) <= a.maxRollingBytes {
		a.tailBytes = len(buffer)
		return
	}
	start := len(buffer) - a.maxRollingBytes
	for start < len(buffer) && buffer[start]&0xc0 == 0x80 {
		start++
	}
	if start != 0 {
		a.tailStartsAtLineBoundary = buffer[start-1] == '\n'
	}
	a.tailText = buffer[start:]
	a.tailBytes = len(a.tailText)
}

func (a *OutputAccumulator) getSnapshotText() string {
	if a.tailStartsAtLineBoundary {
		return a.tailText
	}
	if i := strings.IndexByte(a.tailText, '\n'); i >= 0 {
		return a.tailText[i+1:]
	}
	return a.tailText
}

func (a *OutputAccumulator) shouldUseTempFile() bool {
	return a.totalRawBytes > a.maxBytes || a.totalDecodedBytes > a.maxBytes || a.totalLines > a.maxLines
}

func (a *OutputAccumulator) ensureTempFile() {
	if a.tempFilePath != "" {
		return
	}
	a.tempFilePath = defaultTempFilePath(a.tempFilePrefix)
	// Upstream's write stream reports open failures asynchronously and keeps
	// the path; a failed create likewise only loses the file contents.
	f, err := os.Create(a.tempFilePath)
	if err == nil {
		a.tempFile = f
		for _, chunk := range a.rawChunks {
			_, _ = f.Write(chunk)
		}
	}
	a.rawChunks = nil
}

// defaultTempFilePath mirrors upstream defaultTempFilePath:
// <tmpdir>/<prefix>-<16 hex>.log.
func defaultTempFilePath(prefix string) string {
	var id [8]byte
	_, _ = rand.Read(id[:])
	return filepath.Join(os.TempDir(), prefix+"-"+hex.EncodeToString(id[:])+".log")
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

func (d *utf8StreamDecoder) decode(data []byte, stream bool) string {
	var b strings.Builder
	b.Grow(len(data))
	if d.lower == 0 {
		d.lower, d.upper = 0x80, 0xBF
	}
	emit := func(r rune) {
		if !d.started {
			d.started = true
			if d.stripBOM && r == '\uFEFF' {
				return
			}
		}
		b.WriteRune(r)
	}
	for i := 0; i < len(data); i++ {
		c := data[i]
		if d.bytesNeeded == 0 {
			switch {
			case c <= 0x7F:
				emit(rune(c))
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
				emit('�')
			}
			continue
		}
		if c < d.lower || c > d.upper {
			d.reset()
			emit('�')
			i-- // reprocess this byte as the start of a new sequence
			continue
		}
		d.lower, d.upper = 0x80, 0xBF
		d.codePoint = d.codePoint<<6 | rune(c&0x3F)
		d.bytesSeen++
		if d.bytesSeen == d.bytesNeeded {
			emit(d.codePoint)
			d.reset()
		}
	}
	if !stream && d.bytesNeeded != 0 {
		d.reset()
		emit('�')
	}
	return b.String()
}

func (d *utf8StreamDecoder) reset() {
	d.codePoint, d.bytesSeen, d.bytesNeeded = 0, 0, 0
	d.lower, d.upper = 0x80, 0xBF
}
