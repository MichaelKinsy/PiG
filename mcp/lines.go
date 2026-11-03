package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsonparse"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/mcp/src/transports/lines.ts.

const (
	defaultMaxStderrBytes = 64 * 1024
	stdoutReadChunkBytes  = 64 * 1024
)

// jsonRPCLineReaderOptions configure a [jsonRPCLineReader]. Zero limits mean the defaults.
type jsonRPCLineReaderOptions struct {
	maxMessageBytes int
	maxStderrBytes  int
	// onStderr receives every stderr chunk, decoded as UTF-8.
	onStderr  func(chunk string)
	onMessage func(message JSONRPCMessage)
	onError   func(err error)
}

// jsonRPCLineReader reads a program's output as MCP's stdio framing: newline-delimited JSON-RPC messages on stdout,
// and a bounded tail of stderr for diagnostics. StdioTransport and PipeTransport share it.
//
// Go mechanic: the stdout and stderr sides have their own locks, because the two streams are read from different
// goroutines. A caller must not run readStdout and end concurrently with themselves.
type jsonRPCLineReader struct {
	options jsonRPCLineReaderOptions

	stdoutMu sync.Mutex
	lines    lineSplitter

	stderrMu sync.Mutex
	stderr   []byte
}

func newJSONRPCLineReader(options jsonRPCLineReaderOptions) *jsonRPCLineReader {
	return &jsonRPCLineReader{options: options}
}

// stderrTail is the last maxStderrBytes of stderr.
func (r *jsonRPCLineReader) stderrTail() string {
	r.stderrMu.Lock()
	defer r.stderrMu.Unlock()
	return jsstring.FromUTF8(r.stderr)
}

// readStdout takes the next stdout bytes and reports every complete message, or the error parsing it. A chunk that
// leaves a partial line longer than the limit is dropped with an error.
func (r *jsonRPCLineReader) readStdout(chunk []byte) {
	maxBytes := r.options.maxMessageBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxMessageBytes
	}
	r.stdoutMu.Lock()
	defer r.stdoutMu.Unlock()
	r.lines.write(chunk)
	for {
		line, ok := r.lines.next()
		if !ok {
			if r.lines.pendingLen() > maxBytes {
				r.options.onError(fmt.Errorf("MCP stdio message exceeds %d bytes", maxBytes))
				r.lines.reset()
			}
			return
		}
		if len(line) > maxBytes {
			r.options.onError(fmt.Errorf("MCP stdio message exceeds %d bytes", maxBytes))
			continue
		}
		line = bytes.TrimSuffix(line, []byte{'\r'})
		// bytes.TrimFunc decodes runes as strings.TrimFunc does, so this is
		// jsstring.Trim(text) == "" without copying the line.
		if len(bytes.TrimFunc(line, jsstring.IsSpace)) == 0 {
			continue
		}
		// The line aliases the splitter's buffer; parsing copies what it keeps.
		message, err := parseWireMessage(line)
		if err != nil {
			r.options.onError(err)
			continue
		}
		r.options.onMessage(message)
	}
}

// readStderr takes the next stderr bytes into the tail and passes them to onStderr.
func (r *jsonRPCLineReader) readStderr(chunk []byte) {
	maxBytes := r.options.maxStderrBytes
	if maxBytes == 0 {
		maxBytes = defaultMaxStderrBytes
	}
	r.stderrMu.Lock()
	r.stderr = append(r.stderr, chunk...)
	if len(r.stderr) > maxBytes {
		r.stderr = r.stderr[len(r.stderr)-maxBytes:]
	}
	r.stderrMu.Unlock()
	if r.options.onStderr != nil {
		r.options.onStderr(jsstring.FromUTF8(chunk))
	}
}

// end ends stdout: a message without its newline is reported as an error and dropped.
func (r *jsonRPCLineReader) end() {
	r.stdoutMu.Lock()
	defer r.stdoutMu.Unlock()
	rest := r.lines.pending()
	r.lines.reset()
	if jsstring.Trim(string(rest)) != "" {
		r.options.onError(errors.New("MCP stdio server closed with an incomplete JSON-RPC message"))
	}
}

// copyStdout reads r to its end into readStdout.
func (r *jsonRPCLineReader) copyStdout(src io.Reader) {
	buf := make([]byte, stdoutReadChunkBytes)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			r.readStdout(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// copyStderr reads src to its end into readStderr.
func (r *jsonRPCLineReader) copyStderr(src io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			r.readStderr(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// lineSplitter frames "\n"-terminated lines from appended reads. It remembers
// how far the pending bytes were searched, so a line spanning many reads is
// searched once, and it compacts consumed bytes only when they outweigh the
// pending ones, so copying stays linear too.
type lineSplitter struct {
	buf     []byte
	start   int // first pending byte
	scanned int // bytes before scanned hold no '\n'
}

func (s *lineSplitter) write(p []byte) {
	if s.start > 0 && s.start >= len(s.buf)-s.start {
		n := copy(s.buf, s.buf[s.start:])
		s.buf = s.buf[:n]
		s.scanned -= s.start
		s.start = 0
	}
	s.buf = append(s.buf, p...)
}

// next returns the next line without its "\n". The line aliases the buffer
// and is valid until the next write.
func (s *lineSplitter) next() ([]byte, bool) {
	i := bytes.IndexByte(s.buf[s.scanned:], '\n')
	if i < 0 {
		s.scanned = len(s.buf)
		return nil, false
	}
	end := s.scanned + i
	line := s.buf[s.start:end]
	s.start, s.scanned = end+1, end+1
	return line, true
}

func (s *lineSplitter) pendingLen() int { return len(s.buf) - s.start }

// pending returns the unterminated tail, or nil when there is none.
func (s *lineSplitter) pending() []byte {
	if s.start == len(s.buf) {
		return nil
	}
	return s.buf[s.start:]
}

func (s *lineSplitter) reset() { s.buf, s.start, s.scanned = s.buf[:0], 0, 0 }

// parseWireMessage parses JSON text and validates it as a JSON-RPC message.
// A syntax error carries the message JSON.parse throws.
func parseWireMessage(text []byte) (JSONRPCMessage, error) {
	if !json.Valid(text) {
		if err := jsonparse.Validate(text); err != nil {
			return JSONRPCMessage{}, err
		}
	}
	return ParseJSONRPCMessage(text)
}
