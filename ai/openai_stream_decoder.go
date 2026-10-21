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

// openAIStreamDecoder translates openai core/streaming.mjs's iterSSEChunks, _iterSSEMessages, and fromSSEResponse generators. It follows 7.19.0 (Pi 1.1.0): the exact "[DONE]" sentinel ends the stream without reading further, an event that lacks its blank line is flushed at EOF, an "error" event and a malformed record throw the SDK's errors, and each suspension point is one of 7.19's awaits, including the return and cleanup awaits of createAbortableSSESource. A body read's awaits belong to the reader: the native read and source.iterator.next's Promise.race over it, which iterSSEChunks's for-await awaits.
type openAIStreamDecoder struct {
	messages   openAISSEMessages
	beforeNext func()
	suspend    func()
	current    serverSentEvent
	err        error
	done       bool
	finished   bool
	// record holds the bytes of the record being parsed, reused across records up to openAIStreamRecordReuse bytes.
	record []byte
	// chunks, when set, decodes each record as a chat completion chunk while parse checks it; chunk is the last one it decoded, when chunkOK.
	chunks  *openAIChunkDecoder
	chunk   oaiChunk
	chunkOK bool
}

// openAIStreamRecordReuse is the largest record buffer the decoder keeps for the next record.
const openAIStreamRecordReuse = 64 << 10

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

// Error is the SDK's text: a record that is not JSON throws SyntaxError("Error reading response: malformed server-sent event JSON.") (openai core/streaming.ts fromSSEResponse); an error member is the APIError message.
func (err *openAIStreamDecodeError) Error() string {
	if _, syntax := errors.AsType[*json.SyntaxError](err.Cause); syntax && err.APIError == nil {
		return "Error reading response: malformed server-sent event JSON."
	}
	return err.Cause.Error()
}
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

//perf:hot
func (decoder *openAIStreamDecoder) Next() bool {
	if decoder.beforeNext != nil {
		decoder.beforeNext()
	}
	// The provider awaits the JSON iterator's next promise, including completion and rejection.
	defer openAIStreamSuspend(decoder.suspend)
	if decoder.finished {
		return false
	}
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
	if sse.Data == "[DONE]" {
		// 7.19 breaks at the sentinel; the loop's AsyncIteratorClose returns _iterSSEMessages without aborting the request, so the body is not read to its end.
		decoder.messages.close(true)
		openAIStreamSuspend(decoder.suspend) // AsyncIteratorClose awaits _iterSSEMessages.return().
		decoder.done = true
		decoder.finished = true
		return false
	}
	if err := decoder.parse(&sse); err != nil {
		decoder.messages.close(false)
		openAIStreamSuspend(decoder.suspend) // AsyncIteratorClose awaits _iterSSEMessages.return().
		decoder.err = err
		decoder.finished = true
		return false
	}
	decoder.current = sse
	openAIStreamSuspend(decoder.suspend) // AsyncGeneratorYield awaits the JSON value.
	return true
}

// Event is the current record. Its Raw lines are the decoder's array, which the next Next overwrites.
func (decoder *openAIStreamDecoder) Event() serverSentEvent { return decoder.current }
func (decoder *openAIStreamDecoder) Err() error             { return decoder.err }

// decodeChunks makes the decoder decode each record as a chat completion chunk (parseOpenAIChunkFast) as it checks it. A record the chunk decode reads is valid JSON without a top-level "error" member, which is everything parse checks for an event that is neither "error" nor "thread.*", so json.Valid and the member probe are skipped for it.
func (decoder *openAIStreamDecoder) decodeChunks() { decoder.chunks = &openAIChunkDecoder{} }

// Chunk is the current record's chunk and true when decodeChunks is on and the chunk decode read the record; the caller decodes the other records itself. The chunk's slices are valid until the next Next.
func (decoder *openAIStreamDecoder) Chunk() (oaiChunk, bool) { return decoder.chunk, decoder.chunkOK }

// ChunkParsesAsValue is validJSONParsesAsValue of the current record when Chunk answers true, which the chunk decode read off while it scanned the record.
func (decoder *openAIStreamDecoder) ChunkParsesAsValue() bool {
	return decoder.chunkOK && decoder.chunks.parsesAsValue
}

func (decoder *openAIStreamDecoder) parse(sse *serverSentEvent) error {
	decoder.chunkOK = false
	if decoder.chunks != nil && sse.Event != "error" && !strings.HasPrefix(sse.Event, "thread.") {
		if decoder.chunk, decoder.chunkOK = decoder.chunks.decode(sse.Data); decoder.chunkOK {
			return nil
		}
	}
	// A record that is valid JSON is checked in place; the decode that names the syntax error runs only for one that is not.
	data := append(decoder.record[:0], sse.Data...) //nolint:gocritic // appendAssign: the capacity-capped reuse stores the buffer only when it is small; an oversized record is dropped
	if cap(data) <= openAIStreamRecordReuse {
		decoder.record = data
	}
	if !jsonValidRecord(data) {
		var invalid json.RawMessage
		err := json.Unmarshal(data, &invalid)
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
	var apiError json.RawMessage
	if sse.Event == "error" {
		// fromSSEResponse throws new APIError(undefined, data?.error ?? data) for an error event before any other check.
		if value := bytes.TrimLeft(data, " \t\r\n"); len(value) > 0 && value[0] == '{' {
			member, err := openAIStreamErrorMember(data)
			if err != nil {
				return err
			}
			apiError = member
		}
		if len(apiError) == 0 || string(apiError) == "null" {
			apiError = json.RawMessage(sse.Data)
		}
		err := openAIStreamAPIError(apiError)
		if err == nil {
			err = errors.New("(no status code or body)")
		}
		return &openAIStreamDecodeError{SSE: *sse, APIError: apiError, Cause: err}
	}
	// Only the "error" member is read. The members are decoded only when the object has a top-level member named "error", found by a walk that unescapes nothing but member names (an escape could spell the key); the record is valid JSON, so decoding them cannot fail. JSON whitespace may precede the object, as JSON.parse in the SDK's stream accepts.
	if value := bytes.TrimLeft(data, " \t\r\n"); len(value) > 0 && value[0] == '{' && (bytes.Contains(data, []byte("error")) || bytes.IndexByte(data, '\\') >= 0) && jsonObjectHasMember(sse.Data, "error") {
		member, err := openAIStreamErrorMember(data)
		if err != nil {
			return err
		}
		apiError = member
	}
	if err := openAIStreamAPIError(apiError); err != nil {
		return &openAIStreamDecodeError{SSE: *sse, APIError: apiError, Cause: err}
	}
	return nil
}

// openAIStreamErrorMember is the "error" member of the JSON object data, decoded as the SDK's `data?.error` reads it; nil when there is none. The member is a copy, so data may be reused.
func openAIStreamErrorMember(data []byte) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	return object["error"], nil
}

// jsonObjectHasMember reports whether the JSON object data, already validated, has a top-level member named name. A member name matches as a map key of encoding/json matches: exactly, after unescaping, so the answer is whether decoding data into a map would set name. It reads member values only to skip them.
func jsonObjectHasMember(data, name string) bool {
	i := anthropicSkipSpace(data, 0)
	if i >= len(data) || data[i] != '{' {
		return false
	}
	for i = anthropicSkipSpace(data, i+1); i < len(data) && data[i] == '"'; {
		end, escaped := anthropicStringEnd(data, i)
		key := data[i+1 : end-1]
		if escaped {
			var decoded string
			if json.Unmarshal([]byte(data[i:end]), &decoded) != nil {
				return true // not reached for valid JSON; the caller's decode decides
			}
			key = decoded
		}
		if key == name {
			return true
		}
		i = anthropicSkipSpace(data, end)
		if i >= len(data) || data[i] != ':' {
			return true // not reached for valid JSON
		}
		i = anthropicSkipSpace(data, anthropicValueEnd(data, anthropicSkipSpace(data, i+1)))
		if i >= len(data) || data[i] != ',' {
			return false
		}
		i = anthropicSkipSpace(data, i+1)
	}
	return false
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
	reader  io.Reader
	suspend func()
	data    []byte
	// owned reports that data is the splitter's own buffer. Otherwise data is what the reader returned, which the reader may reuse on its next call: a body value that holds whole events is split in place and never copied.
	owned      bool
	scanFrom   int
	readBuffer []byte
	pendingErr error
	bodyEnded  bool
	closed     bool // The body reported its end; createAbortableSSESource's closed.
	canceled   bool
	finished   bool
}

//perf:hot
func (chunks *openAISSEChunks) next() ([]byte, bool, error) {
	for !chunks.finished {
		if index := openAIDoubleNewlineIndex(chunks.data[chunks.scanFrom:]); index >= 0 {
			index += chunks.scanFrom
			chunk := chunks.data[:index]
			chunks.data = chunks.data[index:]
			chunks.scanFrom = 0
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
				openAIStreamSuspend(chunks.suspend)
				return chunk, true, nil
			}
			chunks.finished = true
			return nil, false, nil
		}
		if len(chunks.data) > 0 && !chunks.owned {
			// The bytes left of the reader's last value must outlive its next call; the copy has room for the value that completes them.
			owned := make([]byte, len(chunks.data), max(2*len(chunks.data), 4096))
			copy(owned, chunks.data)
			chunks.data, chunks.owned = owned, true
		}
		data, err := chunks.read()
		if err != nil {
			chunks.bodyEnded = true
			chunks.closed = errors.Is(err, io.EOF)
			if !chunks.closed {
				chunks.data = nil
				chunks.finished = true
				return nil, false, err
			}
		}
		if len(chunks.data) == 0 {
			chunks.data, chunks.owned = data, false
		} else {
			chunks.data = append(chunks.data, data...)
		}
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

// cancel is createAbortableSSESource's cancel: the first call returns the body, which the SDK never reads past. A body that reached its end is not returned; one whose read failed is.
func (chunks *openAISSEChunks) cancel() {
	if !chunks.closed && !chunks.canceled {
		if closer, ok := chunks.reader.(io.Closer); ok {
			// AsyncIteratorClose preserves the pending JSON/API exception over an iterator-return error.
			_ = closer.Close()
		}
	}
	chunks.canceled = true
	chunks.data, chunks.readBuffer = nil, nil
	chunks.finished = true
}

// openAIDoubleNewlineIndex is internal/decoders/line.mjs findDoubleNewlineIndex (7.19): the end of the first two consecutive line endings, each LF, CRLF or a lone CR.
func openAIDoubleNewlineIndex(data []byte) int {
	// A line ending starts only at a CR or an LF.
	for i := 0; i+1 < len(data); i++ {
		if i = nextLineEnding(data, i); i+1 >= len(data) {
			return -1
		}
		first := openAILineEndingLength(data, i)
		if second := openAILineEndingLength(data, i+first); second > 0 {
			return i + first + second
		}
	}
	return -1
}

// openAILineEndingFirstSpan is the length of the first span nextLineEnding searches; each further span is twice the last. The span lengths change how far a search reads ahead, not what it finds.
const openAILineEndingFirstSpan = 256

// nextLineEnding is the index of the first CR or LF in data at or after from, or len(data). It searches spans that grow from openAILineEndingFirstSpan, so finding a line ending costs about the distance to it: a buffer that holds many records is not searched to its end for a CR on every record.
func nextLineEnding(data []byte, from int) int {
	for length := openAILineEndingFirstSpan; from < len(data); length *= 2 {
		span := data[from:min(len(data), from+length)]
		if lf := bytes.IndexByte(span, '\n'); lf >= 0 {
			span = span[:lf]
			if cr := bytes.IndexByte(span, '\r'); cr >= 0 {
				return from + cr
			}
			return from + lf
		}
		if cr := bytes.IndexByte(span, '\r'); cr >= 0 {
			return from + cr
		}
		from += len(span)
	}
	return len(data)
}

// nextByte is the index of the first c in data at or after from, or len(data).
func nextByte(data []byte, from int, c byte) int {
	if index := bytes.IndexByte(data[from:], c); index >= 0 {
		return from + index
	}
	return len(data)
}

func openAILineEndingLength(data []byte, index int) int {
	if index >= len(data) {
		return 0
	}
	switch data[index] {
	case '\n':
		return 1
	case '\r':
		if index+1 < len(data) && data[index+1] == '\n' {
			return 2
		}
		return 1
	}
	return 0
}

type openAISSEMessages struct {
	chunks   openAISSEChunks
	lines    openAILineDecoder
	sse      openAISSEDecoder
	suspend  func()
	pending  []string
	flushed  bool
	drained  bool // SSEDecoder.flush ran.
	finished bool
}

//perf:hot
func (messages *openAISSEMessages) next() (serverSentEvent, bool, error) {
	for !messages.finished {
		for len(messages.pending) > 0 {
			line := messages.pending[0]
			messages.pending[0] = ""
			messages.pending = messages.pending[1:]
			if event, ok := messages.sse.decode(line); ok {
				openAIStreamSuspend(messages.suspend)
				return event, true, nil
			}
		}
		if messages.flushed {
			if !messages.drained {
				messages.drained = true
				// SSEDecoder.flush delivers an event whose terminating blank line the stream omitted.
				if event, ok := messages.sse.decode(""); ok {
					openAIStreamSuspend(messages.suspend)
					return event, true, nil
				}
			}
			messages.finished = true
			openAIStreamSuspend(messages.suspend) // The finally clause awaits source.cleanup(false); the body ended, so cleanup awaits nothing.
			return serverSentEvent{}, false, nil
		}
		chunk, ok, err := messages.chunks.next()
		openAIStreamSuspend(messages.suspend) // _iterSSEMessages awaits iterSSEChunks.next().
		if err != nil {
			// The catch marks the failure; the finally clause's source.cleanup(true) cancels the body without awaiting it, and is awaited.
			messages.finished = true
			messages.chunks.cancel()
			openAIStreamSuspend(messages.suspend)
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

// close is fromSSEResponse's AsyncIteratorClose of _iterSSEMessages, which is suspended at a yield. At the sentinel the request stays open: the body is returned through source.return(), whose Promise.race and the cleanup's await of that cancellation are awaited. On a malformed or error record the patched return aborts the controller first, and the abort listener returns the body one microtask later, so nothing awaits it.
func (messages *openAISSEMessages) close(sentinel bool) {
	if messages.finished {
		return
	}
	openAIStreamSuspend(messages.suspend) // AsyncGeneratorAwaitReturn: the return resumes the yield one tick later.
	if messages.chunks.finished {
		// The body ended, so neither the abort nor the cleanup returns it.
		openAIStreamSuspend(messages.suspend) // await source.cleanup(failed)
		messages.pending = nil
		messages.finished = true
		return
	}
	if sentinel {
		openAIStreamSuspend(messages.suspend) // iterSSEChunks.return() resumes its yield.
		messages.chunks.cancel()              // source.iterator.return() calls cancel().
		openAIStreamSuspend(messages.suspend) // Promise.race([cancellation, aborted]) settles.
		openAIStreamSuspend(messages.suspend) // return() resumes from awaiting the race.
		openAIStreamSuspend(messages.suspend) // iterSSEChunks's AsyncIteratorClose awaits return().
		openAIStreamSuspend(messages.suspend) // _iterSSEMessages's AsyncIteratorClose awaits iterSSEChunks.return().
		openAIStreamSuspend(messages.suspend) // cleanup awaits the pending cancellation.
		openAIStreamSuspend(messages.suspend) // await source.cleanup(false)
	} else {
		messages.chunks.cancel()              // The abort listener's microtask cancels the body.
		openAIStreamSuspend(messages.suspend) // iterSSEChunks.return() resumes its yield.
		openAIStreamSuspend(messages.suspend) // iterSSEChunks's AsyncIteratorClose awaits source.iterator.return(), which awaits nothing once aborted.
		openAIStreamSuspend(messages.suspend) // _iterSSEMessages's AsyncIteratorClose awaits iterSSEChunks.return().
		openAIStreamSuspend(messages.suspend) // await source.cleanup(false), which awaits nothing once aborted.
	}
	messages.pending = nil
	messages.finished = true
}

type openAISSEDecoder struct {
	event string
	data  []string
	// raw holds the lines of the pending event. A dispatched event's Raw is this array, which the next event's lines overwrite.
	raw []string
}

// openAIRawLinesReuse is the most raw lines the decoder keeps an array for after an event.
const openAIRawLinesReuse = 1024

func (decoder *openAISSEDecoder) decode(line string) (serverSentEvent, bool) {
	line = strings.TrimSuffix(line, "\r")
	if line == "" {
		if decoder.event == "" && len(decoder.data) == 0 {
			return serverSentEvent{}, false
		}
		raw := decoder.raw[:len(decoder.raw):len(decoder.raw)]
		event := serverSentEvent{Event: decoder.event, Data: strings.Join(decoder.data, "\n"), Raw: raw}
		// The data lines are joined into the event; their slice is reused for the next event, and so is the raw lines' array.
		clear(decoder.data)
		decoder.event, decoder.data, decoder.raw = "", decoder.data[:0], decoder.raw[:0]
		if cap(decoder.raw) > openAIRawLinesReuse {
			decoder.raw = nil
		}
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

// openAILineDecoder is internal/decoders/line.mjs LineDecoder (7.19): a CR, LF or CRLF ends a line, and a CR at the end of the buffered bytes ends its line at once; an LF that starts the next chunk is then its continuation.
type openAILineDecoder struct {
	// buffer holds the bytes after the last line end, which no line end follows yet.
	buffer        []byte
	searchFrom    int
	skipLeadingLF bool
	// lines is the slice decode returns, reused: the caller takes every line before it decodes again.
	lines []string
}

// openAILineBufferReuse is the largest partial-line buffer the line decoder keeps once its line ends.
const openAILineBufferReuse = 64 << 10

// decode returns the lines that chunk completes. The bytes up to the last line end become one string, and each line is a substring of it; only the bytes after the last line end are copied for the next chunk.
func (decoder *openAILineDecoder) decode(chunk []byte) []string {
	if len(chunk) == 0 {
		return nil
	}
	if decoder.skipLeadingLF {
		decoder.skipLeadingLF = false
		if chunk[0] == '\n' {
			chunk = chunk[1:]
		}
		if len(chunk) == 0 {
			return nil
		}
	}
	text, from := chunk, 0
	if len(decoder.buffer) > 0 {
		decoder.buffer = append(decoder.buffer, chunk...)
		text, from = decoder.buffer, decoder.searchFrom
	}
	last := bytes.LastIndexAny(text[from:], "\r\n")
	if last < 0 {
		if len(decoder.buffer) == 0 {
			decoder.buffer = append(decoder.buffer, chunk...)
		}
		decoder.searchFrom = len(decoder.buffer)
		return nil
	}
	consumed := from + last + 1
	if text[consumed-1] == '\r' && consumed == len(text) {
		decoder.skipLeadingLF = true
	}
	valid := utf8.Valid(text[:consumed])
	segment := string(text[:consumed])
	if remainder := text[consumed:]; len(remainder) > 0 || cap(decoder.buffer) <= openAILineBufferReuse {
		decoder.buffer = append(decoder.buffer[:0], remainder...)
	} else {
		decoder.buffer = nil
	}
	decoder.searchFrom = len(decoder.buffer)
	lines := decoder.lines[:0]
	// cr and lf are the next CR and LF at or after start, len(segment) when none is left; each is searched again only once passed.
	cr, lf := -1, -1
	for start := 0; start < len(segment); {
		if cr < start {
			cr = nextStringByte(segment, start, '\r')
		}
		if lf < start {
			lf = nextStringByte(segment, start, '\n')
		}
		end := min(cr, lf)
		line := segment[start:end]
		if valid {
			line = strings.TrimPrefix(line, "\ufeff")
		} else {
			line = decodeOpenAILine([]byte(line))
		}
		lines = append(lines, line)
		start = end + 1
		if segment[end] == '\r' && start < len(segment) && segment[start] == '\n' {
			start++
		}
	}
	decoder.lines = lines
	return lines
}

// nextStringByte is the index of the first c in s at or after from, or len(s).
func nextStringByte(s string, from int, c byte) int {
	if index := strings.IndexByte(s[from:], c); index >= 0 {
		return from + index
	}
	return len(s)
}

func (decoder *openAILineDecoder) flush() []string {
	decoder.skipLeadingLF = false
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
