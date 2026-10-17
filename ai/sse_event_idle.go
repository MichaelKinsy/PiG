package ai

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sync"
	"time"
)

// sseIdleTransport ends a text/event-stream response that makes no semantic progress for the configured HTTP idle timeout. The byte-level idle
// deadline (idleTimeoutConn) never fires while a gateway keeps sending comment lines or pings, so a stalled generation hung forever.
// pig divergence (D131): Pi measures idleness on bytes only (pi-mono #10761, #10762); reasoning events keep keepalives counting.
type sseIdleTransport struct{ base http.RoundTripper }

func (t *sseIdleTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil || response.Body == nil {
		return response, err
	}
	timeout := configuredHTTPIdleTimeout()
	if timeout <= 0 {
		return response, nil
	}
	if mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type")); mediaType != "text/event-stream" {
		return response, nil
	}
	body := &sseIdleBody{ReadCloser: response.Body, timeout: timeout}
	body.timer = time.AfterFunc(timeout, body.expire)
	response.Body = body
	return response, nil
}

func (t *sseIdleTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// sseIdleBody restarts its timer whenever a data line of an event other than ping arrives. Comment lines and ping events are keepalives, except while the
// stream is reasoning: a model that thinks silently behind a gateway's keepalives has not stalled, so after a reasoning or thinking event the keepalives
// count as progress until an event that is not about reasoning arrives (owner sign-off 2026-10-10).
type sseIdleBody struct {
	io.ReadCloser
	timeout time.Duration
	timer   *time.Timer

	mu      sync.Mutex
	expired bool

	line []byte
	ping bool
	// eventReasoning reports that the current event's name says reasoning, as OpenAI's response.reasoning_summary_text.delta does.
	eventReasoning bool
	// reasoning reports that the last event with data was a reasoning or thinking event.
	reasoning bool
	// afterCR reports that the previous byte was a CR, so a LF right after it ends no further line (CRLF is one line end).
	afterCR bool
	// A line longer than the sniff window is classified when the window first fills; kind holds that, payload is where a data line's data starts in the
	// window, and lineReasoning reports that an earlier window of the data line held a reasoning marker.
	kind          sseLineKind
	payload       int
	lineReasoning bool
}

type sseLineKind uint8

const (
	sseLineUnknown sseLineKind = iota
	sseLineComment
	sseLineEvent
	sseLineData
	sseLineOther
)

type sseIdleError struct{ timeout time.Duration }

func (e *sseIdleError) Error() string {
	return fmt.Sprintf("SSE Event Timeout Error: no event for %dms", e.timeout.Milliseconds())
}

func (b *sseIdleBody) expire() {
	b.mu.Lock()
	b.expired = true
	b.mu.Unlock()
	_ = b.ReadCloser.Close()
}

func (b *sseIdleBody) Read(buffer []byte) (int, error) {
	count, err := b.ReadCloser.Read(buffer)
	b.mu.Lock()
	expired := b.expired
	b.mu.Unlock()
	if expired && err != nil {
		return count, &sseIdleError{timeout: b.timeout}
	}
	if count > 0 && b.scan(buffer[:count]) {
		b.timer.Reset(b.timeout)
	}
	if err != nil {
		b.timer.Stop()
	}
	return count, err
}

func (b *sseIdleBody) Close() error {
	b.timer.Stop()
	return b.ReadCloser.Close()
}

// scan feeds bytes through a line tracker and reports whether a data line of a non-ping event completed. Line ends are found with a byte search, and a line that starts and ends in data is sniffed in place; only the part of a line that continues into the next read is kept.
func (b *sseIdleBody) scan(data []byte) (progress bool) {
	// cr and lf are the next CR and LF at or after i, len(data) when none is left; each is searched again only once passed.
	cr, lf := -1, -1
	for i := 0; i < len(data); {
		if b.afterCR {
			// CRLF is one line end.
			b.afterCR = false
			if data[i] == '\n' {
				i++
				continue
			}
		}
		if cr < i {
			cr = nextByte(data, i, '\r')
		}
		if lf < i {
			lf = nextByte(data, i, '\n')
		}
		end := min(cr, lf)
		window, kept := b.extend(data[i:end])
		if end == len(data) {
			if !kept {
				b.line = append(b.line[:0], window...)
			}
			break
		}
		b.afterCR = data[end] == '\r'
		if b.endLine(window) {
			progress = true
		}
		i = end + 1
	}
	return progress
}

// extend adds span, the next bytes of the current line, to the sniff window, sniffing each window that fills. It returns the window that is still open: b.line when the line began in an earlier read (kept), otherwise the tail of span itself.
func (b *sseIdleBody) extend(span []byte) (window []byte, kept bool) {
	if len(b.line) > 0 {
		for len(span) > 0 {
			n := min(sseLineSniffBytes-len(b.line), len(span))
			b.line = append(b.line, span[:n]...)
			span = span[n:]
			if len(b.line) == sseLineSniffBytes {
				b.slide(b.line)
				b.line = append(b.line[:0], b.line[len(b.line)-sseSniffOverlap:]...)
			}
		}
		return b.line, true
	}
	for len(span) >= sseLineSniffBytes {
		b.slide(span[:sseLineSniffBytes])
		span = span[sseLineSniffBytes-sseSniffOverlap:]
	}
	return span, false
}

// classify sets the kind of the line from its start; an event line also sets the event's ping and reasoning state.
func (b *sseIdleBody) classify(line []byte) {
	switch {
	case line[0] == ':':
		b.kind = sseLineComment
	case hasSSEField(line, "event:"):
		b.kind = sseLineEvent
		name := bytes.TrimPrefix(line[len("event:"):], []byte(" "))
		b.ping = string(name) == "ping"
		b.eventReasoning = bytes.Contains(name, []byte("reasoning")) || bytes.Contains(name, []byte("thinking"))
	case hasSSEField(line, "data:"):
		b.kind, b.payload = sseLineData, len("data:")
	default:
		b.kind = sseLineOther
	}
}

// slide sniffs a full window of a long line; the next window starts sseSniffOverlap bytes before this one ends, so a marker that crosses the cut is found in it.
func (b *sseIdleBody) slide(window []byte) {
	if b.kind == sseLineUnknown {
		b.classify(window)
	}
	if b.kind == sseLineData && !b.lineReasoning {
		data := window[b.payload:]
		b.lineReasoning = isReasoningEventData(data, len(data)-sseSniffOverlap)
	}
	b.payload = 0
}

// endLine handles a completed line, of which window is the last sniff window, and reports whether it was a data line of a non-ping event, or a keepalive while the stream is reasoning.
func (b *sseIdleBody) endLine(window []byte) bool {
	if b.kind == sseLineUnknown {
		if len(window) == 0 {
			b.ping, b.eventReasoning = false, false
			return false
		}
		b.classify(window)
	}
	kind, data, lineReasoning := b.kind, window[b.payload:], b.lineReasoning
	b.line, b.kind, b.payload, b.lineReasoning = b.line[:0], sseLineUnknown, 0, false
	switch kind {
	case sseLineComment:
		return b.reasoning
	case sseLineData:
		if b.ping {
			return b.reasoning
		}
		b.reasoning = b.eventReasoning || lineReasoning || isReasoningEventData(data, len(data))
		return true
	}
	return false
}

// sseLineSniffBytes is how much of a line the idle tracker keeps at once. A longer line is sniffed window by window, so a marker after a long value, as
// Gemini writes "thought": true after the thought's text, is still found.
// pig divergence (D131): the reasoning sniff window is PiG's; Pi measures idleness on bytes only.
const sseLineSniffBytes = 1024

// sseSniffOverlap is how many bytes of a full window the next one keeps: more than the longest marker with its colon, spaces, value prefix and the byte
// that tells an empty value.
// pig divergence (D131): the reasoning sniff window is PiG's; Pi measures idleness on bytes only.
const sseSniffOverlap = 64

func hasSSEField(line []byte, field string) bool {
	return len(line) >= len(field) && string(line[:len(field)]) == field
}

// reasoningEventMarkers are the JSON members that mark a streamed event as reasoning or thinking: Anthropic's thinking blocks and deltas, OpenAI's reasoning
// items, OpenRouter, DeepSeek and Mistral reasoning text, and Gemini thought parts. Each is a key with the starts of its value that mark reasoning. A
// reasoning text or detail member counts only when it is not empty: Pi takes the first non-empty reasoning string and each reasoning detail
// (openai-completions.ts:611-619, 670-680), and an endpoint may send "reasoning_content":"" or "reasoning_details":[] with every text delta.
var reasoningEventMarkers = [...]struct {
	key    []byte
	values [][]byte
}{
	{[]byte(`"type"`), [][]byte{[]byte(`"thinking`), []byte(`"signature_delta"`), []byte(`"redacted_thinking"`), []byte(`"reasoning`)}},
	{[]byte(`"reasoning"`), [][]byte{[]byte(`"`)}},
	{[]byte(`"reasoning_content"`), [][]byte{[]byte(`"`)}},
	{[]byte(`"reasoning_text"`), [][]byte{[]byte(`"`)}},
	{[]byte(`"reasoning_details"`), [][]byte{[]byte(`[`)}},
	{[]byte(`"thought"`), [][]byte{[]byte(`true`)}},
}

// reasoningKeySuffixes holds the last four bytes of each marker's key. They are distinct, so one comparison per marker rules out a colon that no key
// precedes.
var reasoningKeySuffixes = func() (suffixes [len(reasoningEventMarkers)]uint32) {
	for k, marker := range reasoningEventMarkers {
		suffixes[k] = binary.LittleEndian.Uint32(marker.key[len(marker.key)-4:])
	}
	return suffixes
}()

// isReasoningEventData reports whether data holds, with its key starting before limit, a marker's quoted key followed by optional spaces, a colon, optional
// spaces and one of the marker's value starts, as a JSON writer with or without spaces emits it. One pass visits each colon and matches the keys that end
// before its spaces. An occurrence of a key counts as a left-to-right search for that key takes it: a search resumes after the occurrence it took, so of
// occurrences that overlap (a key's closing quote opening the next) only every other one is taken.
func isReasoningEventData(data []byte, limit int) bool {
	limit = min(limit, len(data))
	for colon := 0; colon < len(data); colon++ {
		next := bytes.IndexByte(data[colon:], ':')
		if next < 0 {
			return false
		}
		colon += next
		end := colon
		for end > 0 && data[end-1] == ' ' {
			end--
		}
		if end < 4 {
			continue
		}
		suffix := binary.LittleEndian.Uint32(data[end-4 : end])
		for k := range reasoningEventMarkers {
			marker := &reasoningEventMarkers[k]
			start := end - len(marker.key)
			if reasoningKeySuffixes[k] != suffix || start < 0 || start >= limit || !bytes.Equal(data[start:end], marker.key) || !takenOccurrence(data, start, marker.key) {
				continue
			}
			for _, value := range marker.values {
				if jsonMemberValueStarts(data[end:], value) {
					return true
				}
			}
		}
	}
	return false
}

// takenOccurrence reports whether a left-to-right search for key, resuming after each occurrence it takes, takes the occurrence at start. Only an occurrence
// that ends where this one starts (sharing its quote) is skipped over: walking back the chain of such occurrences, this one is taken when the chain before
// it is even.
func takenOccurrence(data []byte, start int, key []byte) bool {
	taken := true
	for at := start - (len(key) - 1); at >= 0 && bytes.Equal(data[at:at+len(key)], key); at -= len(key) - 1 {
		taken = !taken
	}
	return taken
}

// jsonMemberValueStarts reports whether rest, the bytes after a member's key, holds a colon, optional spaces and valuePrefix, and the value is not empty.
func jsonMemberValueStarts(rest, valuePrefix []byte) bool {
	rest = bytes.TrimLeft(rest, " ")
	if len(rest) == 0 || rest[0] != ':' {
		return false
	}
	value, ok := bytes.CutPrefix(bytes.TrimLeft(rest[1:], " "), valuePrefix)
	return ok && !emptyJSONValueStart(valuePrefix, value)
}

// emptyJSONValueStart reports whether the rest of a value whose prefix opened a string or an array closes it at once, as "" and [] do. A value the line cut
// off is not known to be empty.
func emptyJSONValueStart(valuePrefix, rest []byte) bool {
	var closing byte
	switch string(valuePrefix) {
	case `"`:
		closing = '"'
	case `[`:
		closing = ']'
	default:
		return false
	}
	rest = bytes.TrimLeft(rest, " ")
	return len(rest) > 0 && rest[0] == closing
}
