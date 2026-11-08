// SPDX-License-Identifier: MIT

package sdk

import (
	"encoding/base64"
	"errors"
	"io"
	"strconv"
	"sync"
)

// readStreamChunk is the most a readStream call returns: the 64 KB chunks fs.createReadStream yields.
const readStreamChunk = 64 * 1024

// streamArg is the argument of operations.readStream and operations.closeStream.
type streamArg struct {
	Stream string `json:"stream"`
}

// readStreams holds the streams ReadOperations.CreateReadStream opened for the host. The host reads one chunk per operations.readStream call and ends the stream
// with operations.closeStream, whether the read finished or was aborted.
type readStreams struct {
	mu      sync.Mutex
	last    int
	streams map[string]*openStream
}

// openStream is one open stream and the chunk buffer its readStream calls share; the host asks for the next chunk after it has the last one.
type openStream struct {
	io.ReadCloser
	buf []byte
}

func (r *readStreams) add(stream io.ReadCloser) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.streams == nil {
		r.streams = map[string]*openStream{}
	}
	r.last++
	id := strconv.Itoa(r.last)
	r.streams[id] = &openStream{ReadCloser: stream}
	return id
}

func (r *readStreams) lookup(id string) (*openStream, error) {
	if r == nil {
		return nil, errors.New("no read stream is open")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	stream, ok := r.streams[id]
	if !ok {
		return nil, errors.New("read stream " + id + " is not open")
	}
	return stream, nil
}

// next returns the next chunk of the stream, base64-encoded, or "" at its end. A chunk is never empty: a stream that reads nothing without an error is read again.
func (r *readStreams) next(id string) (string, error) {
	stream, err := r.lookup(id)
	if err != nil {
		return "", err
	}
	if stream.buf == nil {
		stream.buf = make([]byte, readStreamChunk)
	}
	for {
		n, err := stream.Read(stream.buf)
		if n > 0 {
			return base64.StdEncoding.EncodeToString(stream.buf[:n]), nil
		}
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
	}
}

func (r *readStreams) close(id string) error {
	stream, err := r.lookup(id)
	if err != nil {
		return err
	}
	r.mu.Lock()
	delete(r.streams, id)
	r.mu.Unlock()
	return stream.Close()
}
