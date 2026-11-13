package mcp

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Ports packages/mcp/src/transports/streamable-http.ts (consumeSseStream).

// SSEEvent is one dispatched server-sent event.
type SSEEvent struct {
	Event string
	Data  string
	ID    string
}

// ConsumeSSEOptions configure [ConsumeSSEStream].
type ConsumeSSEOptions struct {
	// MaxEventBytes bounds one event. Zero selects DefaultMaxMessageBytes.
	MaxEventBytes int
	OnEvent       func(event SSEEvent)
	// OnID is called for every `id` field, including events without data (for
	// example resumption priming events).
	OnID func(id string)
	// OnRetry is called for every valid `retry` field, in milliseconds.
	OnRetry func(delayMs int)
}

type sseTooLargeError struct{ limit int }

func (e *sseTooLargeError) Error() string {
	return fmt.Sprintf("MCP SSE event exceeds %d bytes", e.limit)
}

// ConsumeSSEStream reads a server-sent event stream to its end. It fails as
// soon as an event grows past the limit, before the stream ends.
func ConsumeSSEStream(r io.Reader, options ConsumeSSEOptions) error {
	maxEventBytes := options.MaxEventBytes
	if maxEventBytes == 0 {
		maxEventBytes = DefaultMaxMessageBytes
	}
	var (
		eventName, eventID string
		dataLines          []string
		// dataBytes counts the pending event's data, including the "\n" joins, so
		// events streamed as many short `data:` lines without a terminating blank
		// line cannot grow without bound.
		dataBytes int
	)
	dispatch := func() {
		if len(dataLines) == 0 {
			eventName, eventID = "", ""
			return
		}
		data := strings.Join(dataLines, "\n")
		options.OnEvent(SSEEvent{Event: eventName, Data: data, ID: eventID})
		eventName, eventID = "", ""
		dataLines, dataBytes = nil, 0
	}
	processLine := func(line string) error {
		if line == "" {
			dispatch()
			return nil
		}
		if strings.HasPrefix(line, ":") {
			return nil
		}
		field, value, hasColon := strings.Cut(line, ":")
		if !hasColon {
			value = ""
		}
		value = strings.TrimPrefix(value, " ")
		switch {
		case field == "data":
			dataBytes += len(value)
			if len(dataLines) > 0 {
				dataBytes++
			}
			if dataBytes > maxEventBytes {
				return &sseTooLargeError{maxEventBytes}
			}
			dataLines = append(dataLines, value)
		case field == "event":
			eventName = value
		case field == "id" && !strings.Contains(value, "\x00"):
			eventID = value
			if options.OnID != nil {
				options.OnID(value)
			}
		case field == "retry" && isDigits(value):
			if options.OnRetry != nil {
				if delay, err := strconv.Atoi(value); err == nil {
					options.OnRetry(delay)
				}
			}
		}
		return nil
	}

	var lines sseLineSplitter
	chunk := make([]byte, 32*1024)
	for {
		n, readErr := r.Read(chunk)
		if n > 0 {
			lines.write(chunk[:n])
			for {
				line, ok := lines.next()
				if !ok {
					break
				}
				if err := processLine(string(line)); err != nil {
					return err
				}
			}
			if lines.pendingLen() > maxEventBytes {
				return &sseTooLargeError{maxEventBytes}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if rest := lines.pending(); len(rest) > 0 {
		if err := processLine(string(rest)); err != nil {
			return err
		}
	}
	dispatch()
	return nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// sseLineSplitter frames SSE lines, which end with CRLF, LF or a lone CR (the SSE spec; Pi b64e6badd). It is separate from the stdio framing, whose
// lines end at "\n" alone. A CR at the end of the buffered bytes ends its line at once, and skipLF drops the LF that may start the next write so a
// CRLF split across reads still counts as one line ending.
type sseLineSplitter struct {
	buf     []byte
	start   int  // first pending byte
	scanned int  // bytes before scanned hold no CR or LF
	skipLF  bool // the last line ended with a CR that ended the buffer
}

func (s *sseLineSplitter) write(p []byte) {
	if s.skipLF && len(p) > 0 {
		if p[0] == '\n' {
			p = p[1:]
		}
		s.skipLF = false
	}
	if s.start > 0 && s.start >= len(s.buf)-s.start {
		n := copy(s.buf, s.buf[s.start:])
		s.buf = s.buf[:n]
		s.scanned -= s.start
		s.start = 0
	}
	s.buf = append(s.buf, p...)
}

// next returns the next line without its terminator. The line aliases the buffer and is valid until the next write.
func (s *sseLineSplitter) next() ([]byte, bool) {
	i := bytes.IndexAny(s.buf[s.scanned:], "\r\n")
	if i < 0 {
		s.scanned = len(s.buf)
		return nil, false
	}
	end := s.scanned + i
	line := s.buf[s.start:end]
	if s.buf[end] == '\r' {
		if end+1 < len(s.buf) {
			if s.buf[end+1] == '\n' {
				end++
			}
		} else {
			s.skipLF = true
		}
	}
	s.start, s.scanned = end+1, end+1
	return line, true
}

func (s *sseLineSplitter) pendingLen() int { return len(s.buf) - s.start }

// pending returns the unterminated tail, or nil when there is none.
func (s *sseLineSplitter) pending() []byte {
	if s.start == len(s.buf) {
		return nil
	}
	return s.buf[s.start:]
}
