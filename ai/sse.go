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

type sseDecoder struct {
	scanner *bufio.Scanner
	event   string
	data    []string
	raw     []string
	current serverSentEvent
	flushed bool
}

func newSSEDecoder(reader io.Reader) *sseDecoder {
	scanner := bufio.NewScanner(reader)
	scanner.Split(splitSSELines)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSETokenSize)
	return &sseDecoder{scanner: scanner}
}

func (decoder *sseDecoder) Next() bool {
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

// splitSSELines accepts LF, CRLF, and lone CR line endings. Waiting for the
// byte after a trailing CR avoids turning a split CRLF into two line breaks.
func splitSSELines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if index := bytes.IndexAny(data, "\r\n"); index >= 0 {
		if data[index] == '\r' && index+1 == len(data) && !atEOF {
			return 0, nil, nil
		}
		advance = index + 1
		if data[index] == '\r' && index+1 < len(data) && data[index+1] == '\n' {
			advance++
		}
		return advance, data[:index], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
