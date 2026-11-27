package ai

import (
	"strings"
)

// ServerSentEvent is one decoded server-sent event: its optional event name,
// its data lines joined by "\n", and its raw lines for diagnostics.
// Ports packages/ai/src/utils/sse.ts ServerSentEvent.
type ServerSentEvent struct {
	Event *string
	Data  string
	Raw   []string
}

// SseDecoder decodes server-sent events incrementally from a UTF-8 byte stream.
// Lines end with LF, CRLF (including split across chunks), or lone CR; a blank
// line terminates an event. Only newly arrived text is scanned so large events
// split across many chunks cost time linear in their size.
// Ports packages/ai/src/utils/sse.ts SseDecoder.
type SseDecoder struct {
	decoder       webTextDecoder
	pendingLine   string
	skipLeadingLF bool
	event         *string
	data          []string
	raw           []string
}

// NewSseDecoder constructs a streaming SseDecoder.
func NewSseDecoder() *SseDecoder {
	return &SseDecoder{}
}

// Push feeds the next UTF-8 bytes and returns every event they complete.
func (d *SseDecoder) Push(bytes []byte) []ServerSentEvent {
	text := string(d.decoder.decodeTo(nil, bytes, true))
	return d.decodeText(text)
}

// End flushes the decoder at EOF and returns any remaining events, including a
// trailing event not followed by a blank line.
func (d *SseDecoder) End() []ServerSentEvent {
	text := string(d.decoder.decodeTo(nil, nil, false))
	events := d.decodeText(text)
	if d.pendingLine != "" {
		if ev, ok := d.decodeLine(d.pendingLine); ok {
			events = append(events, ev)
		}
	}
	d.pendingLine = ""
	d.skipLeadingLF = false
	if last, ok := d.flush(); ok {
		events = append(events, last)
	}
	return events
}

func (d *SseDecoder) decodeText(text string) []ServerSentEvent {
	var events []ServerSentEvent
	start := 0
	if d.skipLeadingLF && strings.HasPrefix(text, "\n") {
		start = 1
	}
	if len(text) > 0 {
		d.skipLeadingLF = false
	}
	for i := start; i < len(text); i++ {
		ch := text[i]
		if ch != '\r' && ch != '\n' {
			continue
		}
		if ev, ok := d.decodeLine(d.pendingLine + text[start:i]); ok {
			events = append(events, ev)
		}
		d.pendingLine = ""
		if ch == '\r' {
			if i+1 == len(text) {
				d.skipLeadingLF = true
			} else if text[i+1] == '\n' {
				i++
			}
		}
		start = i + 1
	}
	d.pendingLine += text[start:]
	return events
}

func (d *SseDecoder) decodeLine(line string) (ServerSentEvent, bool) {
	if line == "" {
		return d.flush()
	}
	d.raw = append(d.raw, line)
	if strings.HasPrefix(line, ":") {
		return ServerSentEvent{}, false
	}
	field, value, found := strings.Cut(line, ":")
	if !found {
		value = ""
	} else {
		value = strings.TrimPrefix(value, " ")
	}
	switch field {
	case "event":
		v := value
		d.event = &v
	case "data":
		d.data = append(d.data, value)
	}
	return ServerSentEvent{}, false
}

func (d *SseDecoder) flush() (ServerSentEvent, bool) {
	if (d.event == nil || *d.event == "") && len(d.data) == 0 {
		d.event = nil
		d.raw = nil
		return ServerSentEvent{}, false
	}
	ev := ServerSentEvent{
		Event: d.event,
		Data:  strings.Join(d.data, "\n"),
		Raw:   append([]string(nil), d.raw...),
	}
	d.event = nil
	d.data = nil
	d.raw = nil
	return ev, true
}
