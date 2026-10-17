package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// The openai-completions stream decoder at b02c2ceabc, before lw-sse (S29) changed its record path: a double-newline search that
// scanned the rest of the buffer for a CR on every record, a raw line array allocated per event, and json.Valid plus the "error"
// member probe on every record. It is kept verbatim (renamed) as the reference TestOpenAIStreamDecoderMatchesBaseDecoder holds the
// decoder to. The line decoder, the API error rules and the UTF-8 replacement did not change and are shared.

// referenceOpenAIStreamDecoder translates openai core/streaming.mjs's iterSSEChunks, _iterSSEMessages, and fromSSEResponse generators. It follows 7.19.0 (Pi 1.1.0): the exact "[DONE]" sentinel ends the stream without reading further, an event that lacks its blank line is flushed at EOF, an "error" event and a malformed record throw the SDK's errors, and each suspension point is one of 7.19's awaits, including the return and cleanup awaits of createAbortableSSESource. A body read's awaits belong to the reader: the native read and source.iterator.next's Promise.race over it, which iterSSEChunks's for-await awaits.
type referenceOpenAIStreamDecoder struct {
	messages   referenceOpenAISSEMessages
	beforeNext func()
	suspend    func()
	current    serverSentEvent
	err        error
	done       bool
	finished   bool
	// record holds the bytes of the record being parsed, reused across records up to openAIStreamRecordReuse bytes.
	record []byte
}

func newReferenceOpenAIStreamDecoder(reader io.Reader, beforeNext, suspend func()) *referenceOpenAIStreamDecoder {
	return &referenceOpenAIStreamDecoder{
		beforeNext: beforeNext,
		suspend:    suspend,
		messages: referenceOpenAISSEMessages{
			chunks:  referenceOpenAISSEChunks{reader: reader, suspend: suspend},
			suspend: suspend,
		},
	}
}

func (decoder *referenceOpenAIStreamDecoder) Next() bool {
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

func (decoder *referenceOpenAIStreamDecoder) parse(sse *serverSentEvent) error {
	// A record that is valid JSON is checked in place; the decode that names the syntax error runs only for one that is not.
	data := append(decoder.record[:0], sse.Data...) //nolint:gocritic // appendAssign: the capacity-capped reuse stores the buffer only when it is small; an oversized record is dropped
	if cap(data) <= openAIStreamRecordReuse {
		decoder.record = data
	}
	if !json.Valid(data) {
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

type referenceOpenAISSEChunks struct {
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

func (chunks *referenceOpenAISSEChunks) next() ([]byte, bool, error) {
	for !chunks.finished {
		if index := referenceBaseDoubleNewlineIndex(chunks.data[chunks.scanFrom:]); index >= 0 {
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

func (chunks *referenceOpenAISSEChunks) read() ([]byte, error) {
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
func (chunks *referenceOpenAISSEChunks) cancel() {
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

// referenceBaseDoubleNewlineIndex is internal/decoders/line.mjs findDoubleNewlineIndex (7.19): the end of the first two consecutive line endings, each LF, CRLF or a lone CR.
func referenceBaseDoubleNewlineIndex(data []byte) int {
	// A line ending starts only at a CR or an LF: cr and lf are the next of each at or after i, len(data) when none is left, each searched again only once passed.
	cr, lf := -1, -1
	for i := 0; i+1 < len(data); i++ {
		if cr < i {
			cr = nextByte(data, i, '\r')
		}
		if lf < i {
			lf = nextByte(data, i, '\n')
		}
		if i = min(cr, lf); i+1 >= len(data) {
			return -1
		}
		first := openAILineEndingLength(data, i)
		if second := openAILineEndingLength(data, i+first); second > 0 {
			return i + first + second
		}
	}
	return -1
}

type referenceOpenAISSEMessages struct {
	chunks   referenceOpenAISSEChunks
	lines    openAILineDecoder
	sse      referenceOpenAISSEDecoder
	suspend  func()
	pending  []string
	flushed  bool
	drained  bool // SSEDecoder.flush ran.
	finished bool
}

func (messages *referenceOpenAISSEMessages) next() (serverSentEvent, bool, error) {
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
func (messages *referenceOpenAISSEMessages) close(sentinel bool) {
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

type referenceOpenAISSEDecoder struct {
	event string
	data  []string
	raw   []string
}

func (decoder *referenceOpenAISSEDecoder) decode(line string) (serverSentEvent, bool) {
	line = strings.TrimSuffix(line, "\r")
	if line == "" {
		if decoder.event == "" && len(decoder.data) == 0 {
			return serverSentEvent{}, false
		}
		event := serverSentEvent{Event: decoder.event, Data: strings.Join(decoder.data, "\n"), Raw: decoder.raw}
		// The data lines are joined into the event; their slice is reused for the next event.
		clear(decoder.data)
		decoder.event, decoder.data, decoder.raw = "", decoder.data[:0], nil
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

func (decoder *referenceOpenAIStreamDecoder) Event() serverSentEvent { return decoder.current }
func (decoder *referenceOpenAIStreamDecoder) Err() error             { return decoder.err }
