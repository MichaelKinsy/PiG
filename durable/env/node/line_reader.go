package node

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

const textLineChunkBytes = 64 * 1024

var utf8ByteOrderMark = []byte{0xef, 0xbb, 0xbf}

// readFileAt reads a chunk at an explicit offset; tests replace it to hold a read
// pending.
var readFileAt = (*os.File).ReadAt

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

func (reader *nodeTextLineReader) ReadLine(ctx context.Context) (*durableenv.TextLine, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if err := abortedFileError(ctx, reader.path); err != nil {
		return nil, err
	}
	if reader.closed {
		return nil, &durableenv.FileError{Code: durableenv.FileErrorInvalid, Message: "Text line reader is closed", Path: reader.path}
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
func (reader *nodeTextLineReader) bufferedLine() (*durableenv.TextLine, bool) {
	if newline := bytes.IndexByte(reader.buffered, '\n'); newline != -1 {
		text := jsstring.FromUTF8(reader.buffered[:newline])
		reader.buffered = reader.buffered[newline+1:]
		return &durableenv.TextLine{Text: text, Terminated: true}, true
	}
	if reader.ended && len(reader.buffered) > 0 {
		text := jsstring.FromUTF8(reader.buffered)
		reader.buffered = nil
		return &durableenv.TextLine{Text: text}, true
	}
	return nil, false
}

// fill reads the next chunk at an explicit offset so an aborted read can be
// retried without skipping bytes.
func (reader *nodeTextLineReader) fill(ctx context.Context) error {
	bytesRead, err := readFileAt(reader.file, reader.chunk, reader.byteOffset)
	if err != nil && !errors.Is(err, io.EOF) {
		return toFileError(err, fsCall{syscall: "read", path: reader.path})
	}
	if abortErr := abortedFileError(ctx, reader.path); abortErr != nil {
		return abortErr
	}
	atStart := reader.byteOffset == 0
	reader.byteOffset += int64(bytesRead)
	if bytesRead == 0 {
		reader.ended = true
		return nil
	}
	chunk := reader.chunk[:bytesRead]
	if atStart {
		// A TextDecoder drops a byte order mark at the start of its stream.
		chunk = bytes.TrimPrefix(chunk, utf8ByteOrderMark)
	}
	reader.buffered = append(reader.buffered, chunk...)
	return nil
}

func (reader *nodeTextLineReader) Close(context.Context) error {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.closed {
		return nil
	}
	reader.closed = true
	reader.buffered = nil
	closeQuietly(reader.file)
	return nil
}
