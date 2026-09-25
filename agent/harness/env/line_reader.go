package env

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

const textLineChunkBytes = 64 * 1024

// nodeTextLineReader is a strict LF reader. It splits raw bytes on "\n" and
// decodes each line, which equals decoding the stream first: neither a valid
// UTF-8 sequence nor a maximal invalid subpart contains 0x0A.
type nodeTextLineReader struct {
	mu         sync.Mutex
	file       *os.File
	path       string
	chunk      []byte
	byteOffset int64
	buffered   []byte
	ended      bool
	closed     bool
}

func (reader *nodeTextLineReader) ReadLine(ctx context.Context) (*harness.TextLine, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if err := abortedFileError(ctx, reader.path); err != nil {
		return nil, err
	}
	if reader.closed {
		return nil, &harness.FileError{Code: harness.FileErrorInvalid, Message: "Text line reader is closed", Path: reader.path}
	}
	for {
		if line, ok := reader.bufferedLine(); ok {
			return line, nil
		}
		if reader.ended {
			return nil, nil
		}
		if err := reader.fill(ctx); err != nil {
			return nil, err
		}
	}
}

// bufferedLine returns the next complete line, or the unterminated remainder
// at end of file.
func (reader *nodeTextLineReader) bufferedLine() (*harness.TextLine, bool) {
	if newline := bytes.IndexByte(reader.buffered, '\n'); newline != -1 {
		text := decodeUTF8(reader.buffered[:newline])
		reader.buffered = reader.buffered[newline+1:]
		return &harness.TextLine{Text: text, Terminated: true}, true
	}
	if reader.ended && len(reader.buffered) > 0 {
		text := decodeUTF8(reader.buffered)
		reader.buffered = nil
		return &harness.TextLine{Text: text}, true
	}
	return nil, false
}

// fill reads the next chunk at an explicit offset so an aborted read can be
// retried without skipping bytes.
func (reader *nodeTextLineReader) fill(ctx context.Context) error {
	bytesRead, err := reader.file.ReadAt(reader.chunk, reader.byteOffset)
	if err != nil && !errors.Is(err, io.EOF) {
		return toFileError(err, fsCall{syscall: "read", path: reader.path})
	}
	if abortErr := abortedFileError(ctx, reader.path); abortErr != nil {
		return abortErr
	}
	reader.byteOffset += int64(bytesRead)
	if bytesRead == 0 {
		reader.ended = true
		return nil
	}
	reader.buffered = append(reader.buffered, reader.chunk[:bytesRead]...)
	return nil
}

func (reader *nodeTextLineReader) Close(context.Context) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.closed {
		return
	}
	reader.closed = true
	reader.buffered = nil
	_ = reader.file.Close()
}

// decodeUTF8 decodes bytes like Node's UTF-8 TextDecoder: each maximal
// invalid subpart becomes one U+FFFD.
func decodeUTF8(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	var out strings.Builder
	out.Grow(len(data))
	for index := 0; index < len(data); {
		r, size := utf8.DecodeRune(data[index:])
		if r != utf8.RuneError || size > 1 {
			out.Write(data[index : index+size])
			index += size
			continue
		}
		out.WriteRune(utf8.RuneError)
		index += invalidSubpartLength(data[index:])
	}
	return out.String()
}

// invalidSubpartLength is the length of the longest incomplete-but-valid
// sequence prefix at the start of data, or 1.
func invalidSubpartLength(data []byte) int {
	length := 1
	for candidate := 2; candidate <= min(3, len(data)); candidate++ {
		if !utf8.FullRune(data[:candidate]) {
			length = candidate
		}
	}
	return length
}
