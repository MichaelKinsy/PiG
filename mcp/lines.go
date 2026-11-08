package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsonparse"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/mcp/src/transports/lines.ts (JsonRpcLineReader), shared by the stdio and pipe transports (Pi c158fa2ee, f6278a472).

const defaultMaxStderrBytes = 64 * 1024

// lineReader reads a program's output as MCP's stdio framing: newline-delimited JSON-RPC messages on stdout, reported through events, and a
// bounded tail of stderr for diagnostics. readStdout and end are called one at a time by the transport; readStderr and stderrTail may run
// concurrently with them.
//
// Go mechanic: Pi's reader takes constructor callbacks (onMessage, onError, onStderr); Go's transports already own a [TransportEvents], so the
// reader emits to it directly.
type lineReader struct {
	events          *TransportEvents
	maxMessageBytes int // 0 is [DefaultMaxMessageBytes]
	maxStderrBytes  int // 0 is 64 KiB
	// onStderr receives every stderr chunk, decoded as UTF-8.
	onStderr func(chunk string)

	// splitter holds the bytes of the incomplete message received so far; it scans only new bytes, so a large message arriving in many chunks
	// is searched once (f6278a472).
	splitter lineSplitter

	stderrMu sync.Mutex
	stderr   []byte
}

// readStdout takes the next stdout bytes and reports every complete message, or the error parsing it. The chunk is copied, so the caller may reuse
// it.
func (r *lineReader) readStdout(chunk []byte) {
	maxBytes := r.maxMessageBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxMessageBytes
	}
	r.splitter.write(chunk)
	for {
		line, ok := r.splitter.next()
		if !ok {
			if r.splitter.pendingLen() > maxBytes {
				r.events.EmitError(fmt.Errorf("MCP stdio message exceeds %d bytes", maxBytes))
				r.splitter.reset()
			}
			return
		}
		if len(line) > maxBytes {
			r.events.EmitError(fmt.Errorf("MCP stdio message exceeds %d bytes", maxBytes))
			continue
		}
		line = bytes.TrimSuffix(line, []byte{'\r'})
		// bytes.TrimFunc decodes runes as strings.TrimFunc does, so this is jsstring.Trim(text) == "" without copying the line.
		if len(bytes.TrimFunc(line, jsstring.IsSpace)) == 0 {
			continue
		}
		// The line aliases the splitter's buffer; parsing copies what it keeps.
		message, err := parseWireMessage(line)
		if err != nil {
			r.events.EmitError(err)
			continue
		}
		r.events.EmitMessage(message)
	}
}

// readStderr takes the next stderr bytes into the tail and passes them to onStderr.
func (r *lineReader) readStderr(chunk []byte) {
	maxBytes := r.maxStderrBytes
	if maxBytes == 0 {
		maxBytes = defaultMaxStderrBytes
	}
	r.stderrMu.Lock()
	r.stderr = append(r.stderr, chunk...)
	if len(r.stderr) > maxBytes {
		r.stderr = append(r.stderr[:0], r.stderr[len(r.stderr)-maxBytes:]...)
	}
	r.stderrMu.Unlock()
	if r.onStderr != nil {
		r.onStderr(jsstring.FromUTF8(chunk))
	}
}

// stderrTail is the last maxStderrBytes of stderr.
func (r *lineReader) stderrTail() string {
	r.stderrMu.Lock()
	defer r.stderrMu.Unlock()
	return jsstring.FromUTF8(r.stderr)
}

// end ends stdout: a message without its newline is reported as an error and dropped.
func (r *lineReader) end() {
	rest := r.splitter.pending()
	if jsstring.Trim(string(rest)) != "" {
		r.events.EmitError(errors.New("MCP stdio server closed with an incomplete JSON-RPC message"))
	}
	r.splitter.reset()
}

// lineSplitter frames "\n"-terminated lines from appended reads. It remembers how far the pending bytes were searched, so a line spanning many
// reads is searched once, and it compacts consumed bytes only when they outweigh the pending ones, so copying stays linear too.
type lineSplitter struct {
	buf     []byte
	start   int // first pending byte
	scanned int // bytes before scanned hold no '\n'
}

// splitterKeepBytes is the largest buffer the splitter keeps while idle: one oversized message must not pin its memory for the transport's life.
const splitterKeepBytes = 64 * 1024

func (s *lineSplitter) write(p []byte) {
	if s.start == len(s.buf) {
		s.reset()
	}
	if s.start > 0 && s.start >= len(s.buf)-s.start {
		n := copy(s.buf, s.buf[s.start:])
		s.buf = s.buf[:n]
		s.scanned -= s.start
		s.start = 0
	}
	s.buf = append(s.buf, p...)
}

// next returns the next line without its "\n". The line aliases the buffer and is valid until the next write.
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

func (s *lineSplitter) reset() {
	if cap(s.buf) > splitterKeepBytes {
		s.buf = nil
	}
	s.buf, s.start, s.scanned = s.buf[:0], 0, 0
}

// parseWireMessage parses JSON text and validates it as a JSON-RPC message. A syntax error carries the message JSON.parse throws.
func parseWireMessage(text []byte) (JSONRPCMessage, error) {
	if !json.Valid(text) {
		if err := jsonparse.Validate(text); err != nil {
			return JSONRPCMessage{}, err
		}
	}
	return ParseJSONRPCMessage(text)
}
