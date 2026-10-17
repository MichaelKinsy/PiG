package ai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// serverSentEvent is one event dispatched by an SSE stream. Data contains all
// data fields joined with newlines, as required by the event-stream format.
type serverSentEvent struct {
	Event string
	Data  string
	Raw   []string
}

type providerSSEDecoder interface {
	Next() bool
	Event() serverSentEvent
	Err() error
}

type sseDecoder struct {
	beforeNext func()
	scanner    *bufio.Scanner
	event      string
	data       []string
	raw        []string
	current    serverSentEvent
	flushed    bool
}

func newSSEDecoder(reader io.Reader) *sseDecoder {
	scanner := bufio.NewScanner(reader)
	scanner.Split(newSSELineSplitter().split)
	scanner.Buffer(make([]byte, 0, min(64*1024, sseLineLimit)), sseLineLimit)
	return &sseDecoder{scanner: scanner}
}

// sseLineLimit bounds one SSE line. Pi's readers have no cap of their own, but every one of them holds the line in a JavaScript string, which Node
// cannot make longer than buffer.constants.MAX_STRING_LENGTH (2^29-24 on 64-bit V8): the OpenAI SDK's LineDecoder then throws "Cannot create a string
// longer than 0x1fffffe8 characters" and a string buffer throws "RangeError: Invalid string length", ending the stream. Go strings have no such limit.
// pig divergence (D134): a longer line ends the stream with bufio.ErrTooLong at the same bound, so a server that never ends a line cannot exhaust memory.
var sseLineLimit = 1<<29 - 24

func (decoder *sseDecoder) Next() bool {
	if decoder.beforeNext != nil {
		decoder.beforeNext()
	}
	for decoder.scanner.Scan() {
		line := decoder.scanner.Text()
		if line == "" {
			if decoder.flush() {
				return true
			}
			continue
		}

		decoder.raw = append(decoder.raw, line)
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			value = ""
		} else {
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "event":
			decoder.event = value
		case "data":
			decoder.data = append(decoder.data, value)
		}
	}
	if decoder.flushed {
		return false
	}
	decoder.flushed = true
	if decoder.scanner.Err() != nil {
		return false
	}
	return decoder.flush()
}

func (decoder *sseDecoder) Event() serverSentEvent {
	return decoder.current
}

func (decoder *sseDecoder) Err() error {
	return decoder.scanner.Err()
}

func (decoder *sseDecoder) flush() bool {
	if decoder.event == "" && len(decoder.data) == 0 {
		return false
	}
	decoder.current = serverSentEvent{
		Event: decoder.event,
		Data:  strings.Join(decoder.data, "\n"),
		Raw:   append([]string(nil), decoder.raw...),
	}
	decoder.event = ""
	decoder.data = decoder.data[:0]
	decoder.raw = decoder.raw[:0]
	return true
}

func openAIStreamErrorMessage(raw json.RawMessage) (string, bool) {
	if !jsonValueTruthy(raw) {
		return "", false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil {
		if message, ok := object["message"]; ok && jsonValueTruthy(message) {
			var text string
			if json.Unmarshal(message, &text) == nil {
				return text, true
			}
			return compactJSON(message), true
		}
	}
	return compactJSON(raw), true
}

func jsonValueTruthy(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("false")) {
		return false
	}
	switch trimmed[0] {
	case '"':
		var value string
		return json.Unmarshal(trimmed, &value) == nil && value != ""
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		var value float64
		return json.Unmarshal(trimmed, &value) == nil && value != 0
	default:
		return true
	}
}

func compactJSON(raw []byte) string {
	var compact bytes.Buffer
	if json.Compact(&compact, raw) == nil {
		return compact.String()
	}
	return string(raw)
}

// sseLineSplitter is the bufio.SplitFunc of an SSE stream. It accepts LF, CRLF, and lone CR line endings, and waiting for the byte after a trailing CR avoids turning a split CRLF into two line breaks.
// bufio.Scanner calls the function again with the same unconsumed bytes plus whatever the last read added, so it remembers how much of them it has already searched: a line that arrives in many reads is
// searched once, not from its start after every read. A line ending is found with one IndexByte for LF and one for CR in the bytes before it, which is much faster than a search for either of two bytes.
type sseLineSplitter struct {
	// scanned is how many leading bytes of the pending data hold no line ending.
	scanned int
}

func newSSELineSplitter() *sseLineSplitter { return &sseLineSplitter{} }

func (s *sseLineSplitter) split(data []byte, atEOF bool) (advance int, token []byte, err error) {
	start := min(s.scanned, len(data))
	end := len(data)
	index := -1
	if newline := bytes.IndexByte(data[start:], '\n'); newline >= 0 {
		end = start + newline
		index = end
	}
	if carriage := bytes.IndexByte(data[start:end], '\r'); carriage >= 0 {
		index = start + carriage
	}
	if index >= 0 {
		if data[index] == '\r' && index+1 == len(data) && !atEOF {
			s.scanned = index
			return 0, nil, nil
		}
		s.scanned = 0
		advance = index + 1
		if data[index] == '\r' && index+1 < len(data) && data[index+1] == '\n' {
			advance++
		}
		return advance, data[:index], nil
	}
	if atEOF && len(data) > 0 {
		s.scanned = 0
		return len(data), data, nil
	}
	s.scanned = len(data)
	return 0, nil, nil
}
