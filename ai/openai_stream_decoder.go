package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// openAIStreamDecoder translates OpenAI 6.40.0 core/streaming.mjs's iterSSEChunks, _iterSSEMessages, and fromSSEResponse generators. It does not model the native body iterator's awaits.
type openAIStreamDecoder struct {
	messages   openAISSEMessages
	beforeNext func()
	suspend    func()
	current    serverSentEvent
	err        error
	done       bool
	finished   bool
}

// openAIStreamChunkReader preserves a native body iterator value regardless of its size. The returned bytes remain owned by the reader until its next call.
type openAIStreamChunkReader interface {
	ReadChunk() ([]byte, error)
}

// openAIStreamDecodeError retains the SSE record for the provider's SyntaxError diagnostics or APIError formatting.
type openAIStreamDecodeError struct {
	SSE      serverSentEvent
	APIError json.RawMessage
	Cause    error
}

func (err *openAIStreamDecodeError) Error() string { return err.Cause.Error() }
func (err *openAIStreamDecodeError) Unwrap() error { return err.Cause }

func newOpenAIStreamDecoder(reader io.Reader, beforeNext, suspend func()) *openAIStreamDecoder {
	return &openAIStreamDecoder{
		beforeNext: beforeNext,
		suspend:    suspend,
		messages: openAISSEMessages{
			chunks:  openAISSEChunks{reader: reader, suspend: suspend},
			suspend: suspend,
		},
	}
}

func (decoder *openAIStreamDecoder) Next() bool {
	if decoder.beforeNext != nil {
		decoder.beforeNext()
	}
	// The provider awaits the JSON iterator's next promise, including completion and rejection.
	defer openAIStreamSuspend(decoder.suspend)
	if decoder.finished {
		return false
	}
	for {
		sse, ok, err := decoder.messages.next()
		openAIStreamSuspend(decoder.suspend) // fromSSEResponse awaits _iterSSEMessages.next().
		if !ok {
			decoder.finished = true
			if err == nil {
				decoder.done = true
			}
			if !errors.Is(err, context.Canceled) && (err == nil || !strings.Contains(err.Error(), "FetchRequestCanceledException")) {
				decoder.err = err
			}
			return false
		}
		if decoder.done {
			continue
		}
		if strings.HasPrefix(sse.Data, "[DONE]") {
			decoder.done = true
			continue
		}
		if err := decoder.parse(&sse); err != nil {
			decoder.messages.close()
			openAIStreamSuspend(decoder.suspend) // AsyncIteratorClose awaits _iterSSEMessages.return().
			decoder.err = err
			decoder.finished = true
			return false
		}
		decoder.current = sse
		openAIStreamSuspend(decoder.suspend) // AsyncGeneratorYield awaits the JSON value.
		return true
	}
}

func (decoder *openAIStreamDecoder) Event() serverSentEvent { return decoder.current }
func (decoder *openAIStreamDecoder) Err() error             { return decoder.err }

func (decoder *openAIStreamDecoder) parse(sse *serverSentEvent) error {
	var data json.RawMessage
	if err := json.Unmarshal([]byte(sse.Data), &data); err != nil {
		return &openAIStreamDecodeError{SSE: *sse, Cause: err}
	}
	if strings.HasPrefix(sse.Event, "thread.") {
		// The SDK's event == "error" check inside this branch is unreachable: the branch requires thread.*.
		wrapped, err := json.Marshal(struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}{sse.Event, data})
		if err != nil {
			return err
		}
		sse.Data = string(wrapped)
		return nil
	}
	var object map[string]json.RawMessage
	if len(data) > 0 && data[0] == '{' {
		if err := json.Unmarshal(data, &object); err != nil {
			return err
		}
	}
	if err := openAIStreamAPIError(object["error"]); err != nil {
		return &openAIStreamDecodeError{SSE: *sse, APIError: object["error"], Cause: err}
	}
	return nil
}

// openAIStreamAPIError mirrors core/error.mjs APIError.makeMessage for an error discovered in a JSON stream record.
func openAIStreamAPIError(raw json.RawMessage) error {
	if !openAIStreamTruthy(raw) {
		return nil
	}
	value := raw
	if len(raw) > 0 && raw[0] == '{' {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		if message := object["message"]; openAIStreamTruthy(message) {
			var text string
			if json.Unmarshal(message, &text) == nil {
				return errors.New(text)
			}
			value = message
		}
	}
	encoded, err := jsonstringify.Canonicalize(value)
	if err != nil {
		return err
	}
	return errors.New(string(encoded))
}

func openAIStreamTruthy(raw json.RawMessage) bool {
	value := bytes.TrimSpace(raw)
	if len(value) > 0 && (value[0] == '-' || value[0] >= '0' && value[0] <= '9') {
		// JSON.parse accepts overflow to Infinity, which is truthy even though JSON.stringify subsequently emits null.
		number, _ := strconv.ParseFloat(string(value), 64)
		return number != 0
	}
	return jsonValueTruthy(value)
}

func openAIStreamSuspend(suspend func()) {
	if suspend != nil {
		suspend()
	}
}

type openAISSEChunks struct {
	reader     io.Reader
	suspend    func()
	data       []byte
	scanFrom   int
	readBuffer []byte
	pendingErr error
	bodyEnded  bool
	finished   bool
	yielded    bool
}

func (chunks *openAISSEChunks) next() ([]byte, bool, error) {
	chunks.yielded = false // Resumption after yield runs synchronously until the next await/yield.
	for !chunks.finished {
		if index := openAIDoubleNewlineIndex(chunks.data[chunks.scanFrom:]); index >= 0 {
			index += chunks.scanFrom
			chunk := chunks.data[:index]
			chunks.data = chunks.data[index:]
			chunks.scanFrom = 0
			chunks.yielded = true
			openAIStreamSuspend(chunks.suspend)
			return chunk, true, nil
		}
		// A delimiter can straddle the next body value by at most three bytes (CR LF CR LF).
		chunks.scanFrom = max(0, len(chunks.data)-3)
		if chunks.bodyEnded {
			if len(chunks.data) > 0 {
				chunk := chunks.data
				chunks.data = nil
				chunks.scanFrom = 0
				chunks.yielded = true
				openAIStreamSuspend(chunks.suspend)
				return chunk, true, nil
			}
			chunks.finished = true
			return nil, false, nil
		}
		data, err := chunks.read()
		if err != nil {
			chunks.bodyEnded = true
			if !errors.Is(err, io.EOF) {
				chunks.data = nil
				chunks.finished = true
				return nil, false, err
			}
		}
		chunks.data = append(chunks.data, data...)
	}
	return nil, false, nil
}

func (chunks *openAISSEChunks) read() ([]byte, error) {
	if chunks.pendingErr != nil {
		err := chunks.pendingErr
		chunks.pendingErr = nil
		return nil, err
	}
	var data []byte
	var err error
	if reader, ok := chunks.reader.(openAIStreamChunkReader); ok {
		data, err = reader.ReadChunk()
	} else {
		if chunks.readBuffer == nil {
			chunks.readBuffer = make([]byte, 4096)
		}
		var n int
		n, err = chunks.reader.Read(chunks.readBuffer)
		data = chunks.readBuffer[:n]
	}
	if len(data) > 0 && err != nil {
		chunks.pendingErr = err
		err = nil
	}
	return data, err
}

func (chunks *openAISSEChunks) close() {
	if chunks.finished {
		return
	}
	if chunks.yielded {
		openAIStreamSuspend(chunks.suspend) // AsyncGeneratorAwaitReturn awaits the return value before resuming a suspended yield.
	}
	if !chunks.bodyEnded {
		if closer, ok := chunks.reader.(io.Closer); ok {
			// AsyncIteratorClose preserves the pending JSON/API exception over an iterator-return error. Native return/close awaits belong to the body adapter.
			_ = closer.Close()
		}
	}
	chunks.data, chunks.readBuffer = nil, nil
	chunks.finished = true
}

// openAIDoubleNewlineIndex matches internal/decoders/line.mjs findDoubleNewlineIndex, not every possible SSE line-ending pair.
func openAIDoubleNewlineIndex(data []byte) int {
	for i := 0; i+1 < len(data); i++ {
		if data[i] == '\n' && data[i+1] == '\n' || data[i] == '\r' && data[i+1] == '\r' {
			return i + 2
		}
		if data[i] == '\r' && data[i+1] == '\n' && i+3 < len(data) && data[i+2] == '\r' && data[i+3] == '\n' {
			return i + 4
		}
	}
	return -1
}

type openAISSEMessages struct {
	chunks   openAISSEChunks
	lines    openAILineDecoder
	sse      openAISSEDecoder
	suspend  func()
	pending  []string
	flushed  bool
	finished bool
	yielded  bool
}

func (messages *openAISSEMessages) next() (serverSentEvent, bool, error) {
	messages.yielded = false
	for !messages.finished {
		for len(messages.pending) > 0 {
			line := messages.pending[0]
			messages.pending[0] = ""
			messages.pending = messages.pending[1:]
			if event, ok := messages.sse.decode(line); ok {
				messages.yielded = true
				openAIStreamSuspend(messages.suspend)
				return event, true, nil
			}
		}
		if messages.flushed {
			messages.finished = true
			return serverSentEvent{}, false, nil
		}
		chunk, ok, err := messages.chunks.next()
		openAIStreamSuspend(messages.suspend) // _iterSSEMessages awaits iterSSEChunks.next().
		if err != nil {
			messages.finished = true
			return serverSentEvent{}, false, err
		}
		if ok {
			messages.pending = messages.lines.decode(chunk)
		} else {
			messages.flushed = true
			messages.pending = messages.lines.flush()
		}
	}
	return serverSentEvent{}, false, nil
}

func (messages *openAISSEMessages) close() {
	if messages.finished {
		return
	}
	if messages.yielded {
		openAIStreamSuspend(messages.suspend)
	}
	if !messages.chunks.finished {
		messages.chunks.close()
		openAIStreamSuspend(messages.suspend) // AsyncIteratorClose awaits iterSSEChunks.return().
	}
	messages.pending = nil
	messages.finished = true
}

type openAISSEDecoder struct {
	event string
	data  []string
	raw   []string
}

func (decoder *openAISSEDecoder) decode(line string) (serverSentEvent, bool) {
	line = strings.TrimSuffix(line, "\r")
	if line == "" {
		if decoder.event == "" && len(decoder.data) == 0 {
			return serverSentEvent{}, false
		}
		event := serverSentEvent{Event: decoder.event, Data: strings.Join(decoder.data, "\n"), Raw: decoder.raw}
		decoder.event, decoder.data, decoder.raw = "", nil, nil
		return event, true
	}
	decoder.raw = append(decoder.raw, line)
	if strings.HasPrefix(line, ":") {
		return serverSentEvent{}, false
	}
	field, value, _ := strings.Cut(line, ":")
	value = strings.TrimPrefix(value, " ")
	switch field {
	case "event":
		decoder.event = value
	case "data":
		decoder.data = append(decoder.data, value)
	}
	return serverSentEvent{}, false
}

type openAILineDecoder struct {
	buffer []byte
	cr     int // One past a pending CR; zero means no pending CR.
}

func (decoder *openAILineDecoder) decode(chunk []byte) []string {
	decoder.buffer = append(decoder.buffer, chunk...)
	var lines []string
	for {
		position := bytes.IndexAny(decoder.buffer[decoder.cr:], "\r\n")
		if position < 0 {
			return lines
		}
		position += decoder.cr
		carriage := decoder.buffer[position] == '\r'
		index := position + 1
		if carriage && decoder.cr == 0 {
			decoder.cr = index
			continue
		}
		if decoder.cr != 0 && (index != decoder.cr+1 || carriage) {
			lines = append(lines, decodeOpenAILine(decoder.buffer[:decoder.cr-1]))
			decoder.buffer = decoder.buffer[decoder.cr:]
			decoder.cr = 0
			continue
		}
		end := position
		if decoder.cr != 0 {
			end--
		}
		lines = append(lines, decodeOpenAILine(decoder.buffer[:end]))
		decoder.buffer = decoder.buffer[index:]
		decoder.cr = 0
	}
}

func (decoder *openAILineDecoder) flush() []string {
	if len(decoder.buffer) == 0 {
		return nil
	}
	return decoder.decode([]byte{'\n'})
}

// decodeOpenAILine follows a non-streaming TextDecoder.decode: strip the initial BOM and replace each malformed UTF-8 subsequence.
func decodeOpenAILine(data []byte) string {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if utf8.Valid(data) {
		return string(data)
	}
	var decoded strings.Builder
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			size = openAIInvalidUTF8Prefix(data)
		}
		decoded.WriteRune(r)
		data = data[size:]
	}
	return decoded.String()
}

func openAIInvalidUTF8Prefix(data []byte) int {
	first := data[0]
	length := 0
	switch {
	case first >= 0xc2 && first <= 0xdf:
		length = 2
	case first >= 0xe0 && first <= 0xef:
		length = 3
	case first >= 0xf0 && first <= 0xf4:
		length = 4
	default:
		return 1
	}
	for i := 1; i < length && i < len(data); i++ {
		b := data[i]
		if b < 0x80 || b > 0xbf || i == 1 && (first == 0xe0 && b < 0xa0 || first == 0xed && b > 0x9f || first == 0xf0 && b < 0x90 || first == 0xf4 && b > 0x8f) {
			return i
		}
	}
	return min(length, len(data))
}
