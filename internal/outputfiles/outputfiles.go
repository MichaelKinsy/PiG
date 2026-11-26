// Package outputfiles creates the files pi writes so the model can use output
// it was not shown in full: the full text of truncated tool output, binary MCP
// resources and images shown by codemode scripts. Every output file is created
// here, in the OS temp directory.
//
// Ports packages/coding-agent/src/utils/output-files.ts
package outputfiles

import (
	"bufio"
	"crypto/rand"
	"sync"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// Mode is the permission of every output file. Output can carry private data,
// so only the user may read the files.
const Mode os.FileMode = 0o600

// newPath is a new, unused path: `<tmpdir>/<prefix>-<16 hex><extension>`, where tmpdir is Node's os.tmpdir().
// extension includes the dot.
func newPath(prefix, extension string) string {
	var id [8]byte
	// crypto/rand.Read never returns an error.
	_, _ = rand.Read(id[:])
	return filepath.Join(nodeTmpdir(runtime.GOOS, os.Getenv), prefix+"-"+hex.EncodeToString(id[:])+extension)
}

// create creates the file exclusively, so it never follows a link someone else
// placed at the path.
func create(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, Mode)
}

// WriteFile writes data to a new output file and returns its path. A failure
// returns an empty path and the error as Node reports it.
func WriteFile(prefix, extension string, data []byte) (string, error) {
	path := newPath(prefix, extension)
	f, err := create(path)
	if err != nil {
		return "", nodeerrno.FromPathError(err)
	}
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", nodeerrno.FromPathError(err)
	}
	return path, nil
}

// CreateStream opens a new output file for streamed output. Like Node's
// write stream, which reports an open failure asynchronously, it returns the
// path even when the file could not be created: the error accompanies a nil
// file and the caller keeps the path.
func CreateStream(prefix, extension string) (string, *os.File, error) {
	path := newPath(prefix, extension)
	f, err := create(path)
	return path, f, nodeerrno.FromPathError(err)
}

// StreamResult describes where streamed output ended up: a complete file, or
// why it could not be saved. Mirrors upstream OutputFileResult.
type StreamResult struct {
	Path  string
	Error string
}

// Saved reports whether the file has not failed.
func (r StreamResult) Saved() bool { return r.Error == "" && r.Path != "" }

// Failed reports whether opening, writing, flushing, or closing the file failed.
func (r StreamResult) Failed() bool { return r.Error != "" }

// Stream is an output file written while output streams in. Open, write, flush,
// and close failures never panic: the first failure drops and removes any
// partially written file (never removing a file that already existed before
// open), ignores later writes, and reports the error from Result() and Close().
//
// Mirrors upstream OutputFileStream (utils/output-files.ts).
type Stream struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	writer  *bufio.Writer
	opened  bool
	closed  bool
	failed  string
	removed bool
}

// OpenStream creates a new exclusive output file stream in the OS temp dir.
// Never returns an error; open failures are captured in Result() / Close().
func OpenStream(prefix, extension string) *Stream {
	path := newPath(prefix, extension)
	f, err := create(path)
	if err != nil {
		return &Stream{
			path:   path,
			closed: true,
			failed: nodeerrno.FromPathError(err).Error(),
		}
	}
	return &Stream{
		path:   path,
		file:   f,
		writer: bufio.NewWriter(f),
		opened: true,
	}
}

// Result returns the current state of the stream. The file is complete only
// once Close() returns.
func (s *Stream) Result() StreamResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != "" {
		return StreamResult{Error: s.failed}
	}
	return StreamResult{Path: s.path}
}

// Write appends data to the stream. Writes after a failure or after Close()
// are ignored.
func (s *Stream) Write(data []byte) {
	if len(data) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != "" || s.closed || s.writer == nil {
		return
	}
	if _, err := s.writer.Write(data); err != nil {
		s.failLocked(err)
	}
}

// WriteString appends text to the stream. Writes after a failure or after
// Close() are ignored.
func (s *Stream) WriteString(text string) {
	if text == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != "" || s.closed || s.writer == nil {
		return
	}
	if _, err := s.writer.WriteString(text); err != nil {
		s.failLocked(err)
	}
}

// Buffered returns the number of bytes currently buffered in memory before
// flushing to the underlying file.
func (s *Stream) Buffered() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writer == nil {
		return 0
	}
	return s.writer.Buffered()
}

// Flush flushes buffered data to the underlying file if still open and healthy.
func (s *Stream) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != "" || s.closed || s.writer == nil {
		return
	}
	if err := s.writer.Flush(); err != nil {
		s.failLocked(err)
	}
}

// Close flushes and closes the underlying file, removing any partial file on
// error, and returns the final StreamResult. Idempotent.
func (s *Stream) Close() StreamResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		if s.writer != nil && s.failed == "" {
			if err := s.writer.Flush(); err != nil {
				s.failLocked(err)
			}
		}
		if s.file != nil {
			err := s.file.Close()
			s.file = nil
			s.writer = nil
			if err != nil && s.failed == "" {
				s.failLocked(err)
			}
		}
	}
	if s.failed != "" {
		return StreamResult{Error: s.failed}
	}
	return StreamResult{Path: s.path}
}

func (s *Stream) failLocked(err error) {
	if s.failed == "" {
		s.failed = nodeerrno.FromPathError(err).Error()
	}
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
		s.writer = nil
	}
	if s.opened && !s.removed {
		s.removed = true
		_ = os.Remove(s.path)
	}
}
