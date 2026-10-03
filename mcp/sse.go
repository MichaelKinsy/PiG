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
	processLine := func(raw string) error {
		line := strings.TrimSuffix(raw, "\r")
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

	var buffered []byte
	chunk := make([]byte, 32*1024)
	for {
		n, readErr := r.Read(chunk)
		if n > 0 {
			buffered = append(buffered, chunk[:n]...)
			for {
				newline := bytes.IndexByte(buffered, '\n')
				if newline < 0 {
					break
				}
				if err := processLine(string(buffered[:newline])); err != nil {
					return err
				}
				buffered = buffered[newline+1:]
			}
			if len(buffered) > maxEventBytes {
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
	if len(buffered) > 0 {
		if err := processLine(string(buffered)); err != nil {
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
