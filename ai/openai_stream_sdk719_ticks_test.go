package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"
)

// Oracle: openai 7.19.0..7.59.0 core/streaming.mjs (Pi 1.1.0..1.6.0; fromSSEResponse and responses.create are unchanged in 7.59.0,
// while chat.completions.create wraps Stream[Symbol.asyncIterator] in lib/chat-completions/streaming.mjs normalizeChatCompletionStream
// adding an outer async function* for-await wrapper of +2 microtask ticks per chunk), traced by driving Stream.fromSSEResponse over a body async iterator whose next and
// return resolve at once, with synchronous labels and an independent queueMicrotask clock (scratch probe trace-openai-sse.mjs). The
// controller.abort labels of the trace are omitted: the Go decoder has no controller, and an abort changes nothing that the provider reads.
// The decoder leaves a body read's awaits to the reader: the native read, then createAbortableSSESource's Promise.race over it and the
// for-await of its async next, which this reader charges as the trace's three ticks. The end-to-end Azure oracle
// (coding/rpc33_azure_ticks_test.go) pins the production reader. Nothing awaits the native return on its own, so the decoder models
// every tick after it.
// readFailure stands for a body read that rejects with "wire failed".
const readFailure = "\x00read failure"

// readAbort stands for a pending body read that the request's abort rejects with an AbortError. 7.19 ends such a stream as done, not as an error.
// The probe aborts one microtask after the read starts, so its traces are "next@0 read@0 value@9 next@9 read@9 controller.abort@10 read.reject@10
// body.return@11 done@17" and "next@0 read@0 controller.abort@1 read.reject@1 body.return@2 done@8". Counted from the rejection, done
// comes 7 ticks later, as after a failed read; the rows below rebase the rejection onto the read, as the reader's three ticks do. The body is
// returned by the abort listener one microtask after the abort, so nothing awaits it and its tick is not observable to the provider; the
// Go decoder returns it through the read-failure cleanup instead, which also takes no await the provider sees.
const readAbort = "\x00read abort"

func TestOpenAIStreamDecoderFollowsOpenAI719SuspensionPoints(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		chunks []string
		values []string
		err    string
		trace  []string
	}{
		{"empty", nil, nil, "", []string{"next@0", "read@0", "done@7"}},
		{"buffered", []string{"data: 1\n\ndata: 2\n\n"}, []string{"1", "2"}, "", []string{"next@0", "read@0", "value@9", "next@9", "value@15", "next@15", "read@15", "done@22"}},
		{"split", []string{"data: 1\n", "\ndata: 2\n\n"}, []string{"1", "2"}, "", []string{"next@0", "read@0", "read@3", "value@12", "next@12", "value@18", "next@18", "read@18", "done@25"}},
		{"comment", []string{": comment\n\ndata: 1\n\n"}, []string{"1"}, "", []string{"next@0", "read@0", "value@11", "next@11", "read@11", "done@18"}},
		{"empty-frame", []string{"\n\ndata: 1\n\n"}, []string{"1"}, "", []string{"next@0", "read@0", "value@11", "next@11", "read@11", "done@18"}},
		{"bare-tail", []string{"data: 1"}, []string{"1"}, "", []string{"next@0", "read@0", "read@3", "value@13", "next@13", "done@16"}},
		{"one-newline-tail", []string{"data: 1\n"}, []string{"1"}, "", []string{"next@0", "read@0", "read@3", "value@13", "next@13", "done@16"}},
		{"cr-tail", []string{"data: 1\r\r"}, []string{"1"}, "", []string{"next@0", "read@0", "value@9", "next@9", "read@9", "done@16"}},
		{"done-and-after", []string{"data: [DONE]suffix\n\ndata: invalid\n\n"}, nil, "Error reading response: malformed server-sent event JSON.", []string{"next@0", "read@0", "body.return@8", "error@14"}},
		{"done-then-read", []string{"data: [DONE]\n\n", "data: 2\n\n"}, nil, "", []string{"next@0", "read@0", "body.return@9", "done@17"}},
		{"done-exact-after", []string{"data: [DONE]\n\ndata: invalid\n\n"}, nil, "", []string{"next@0", "read@0", "body.return@9", "done@17"}},
		{"invalid-json", []string{"data: nope\n\n"}, nil, "Error reading response: malformed server-sent event JSON.", []string{"next@0", "read@0", "body.return@8", "error@14"}},
		{"event-error", []string{"event: error\ndata: {\"message\":\"normal\"}\n\n"}, nil, "normal", []string{"next@0", "read@0", "body.return@8", "error@14"}},
		{"tail-then-eof-value", []string{"data: 1\n\ndata: 2"}, []string{"1", "2"}, "", []string{"next@0", "read@0", "value@9", "next@9", "read@9", "value@19", "next@19", "done@22"}},
		{"event-only-tail", []string{"event: message\n"}, nil, "Error reading response: malformed server-sent event JSON.", []string{"next@0", "read@0", "read@3", "error@15"}},
		{"comment-tail", []string{": c"}, nil, "", []string{"next@0", "read@0", "read@3", "done@12"}},
		{"data-empty-tail", []string{"data:"}, nil, "Error reading response: malformed server-sent event JSON.", []string{"next@0", "read@0", "read@3", "error@15"}},
		{"syntax-cr", []string{"data: nope\r\r"}, nil, "Error reading response: malformed server-sent event JSON.", []string{"next@0", "read@0", "body.return@8", "error@14"}},
		{"event-only-cr", []string{"event: message\r\r"}, nil, "Error reading response: malformed server-sent event JSON.", []string{"next@0", "read@0", "body.return@8", "error@14"}},
		{"syntax-tail-chunk", []string{"data: nope\n\r\n"}, nil, "Error reading response: malformed server-sent event JSON.", []string{"next@0", "read@0", "body.return@8", "error@14"}},
		{"api", []string{"data: {\"error\":{\"message\":\"bad\",\"code\":\"code\"}}\n\n"}, nil, "bad", []string{"next@0", "read@0", "body.return@8", "error@14"}},
		{"read-error", []string{"data: 1\n\n", readFailure}, []string{"1"}, "wire failed", []string{"next@0", "read@0", "value@9", "next@9", "read@9", "body.return@13", "error@16"}},
		{"read-error-first", []string{readFailure}, nil, "wire failed", []string{"next@0", "read@0", "body.return@4", "error@7"}},
		{"abort-pending", []string{"data: 1\n\n", readAbort}, []string{"1"}, "", []string{"next@0", "read@0", "value@9", "next@9", "read@9", "body.return@13", "done@16"}},
		{"abort-pending-first", []string{readAbort}, nil, "", []string{"next@0", "read@0", "body.return@4", "done@7"}},
		{"crlf-split", []string{"data: 1\r", "\n\r\n"}, []string{"1"}, "", []string{"next@0", "read@0", "read@3", "value@12", "next@12", "read@12", "done@19"}},
		{"lf-crlf", []string{"data: 1\n\r\ndata: 2\n\n"}, []string{"1", "2"}, "", []string{"next@0", "read@0", "value@9", "next@9", "value@15", "next@15", "read@15", "done@22"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var trace []string
			var values []string
			tick := 0
			mark := func(label string) { trace = append(trace, fmt.Sprintf("%s@%d", label, tick)) }
			index := 0
			reader := &openAITestChunkReader{read: func() ([]byte, error) {
				mark("read")
				tick += 3
				if index == len(test.chunks) {
					return nil, io.EOF
				}
				chunk := test.chunks[index]
				index++
				if chunk == readFailure {
					return nil, errors.New("wire failed")
				}
				if chunk == readAbort {
					return nil, context.Canceled
				}
				return []byte(chunk), nil
			}, close: func() error {
				mark("body.return")
				return nil
			}}
			decoder := newOpenAIStreamDecoder(reader, func() { mark("next") }, func() { tick++ })
			for decoder.Next() {
				mark("value")
				values = append(values, decoder.Event().Data)
			}
			errText := ""
			if decoder.Err() != nil {
				errText = decoder.Err().Error()
				mark("error")
			} else {
				mark("done")
			}
			if !reflect.DeepEqual(values, test.values) || errText != test.err || !reflect.DeepEqual(trace, test.trace) {
				t.Errorf("values = %q, want %q\nerr = %q, want %q\ntrace = %q\nwant    %q", values, test.values, errText, test.err, trace, test.trace)
			}
			before := tick
			if decoder.Next() || tick != before+1 {
				t.Error("a finished JSON iterator adds only its caller's await")
			}
		})
	}
}

// TestOpenAIChatCompletionsNormalizeWrapperTicks pins the +2 ticks/chunk and +1 tick on
// completion/error added by openai 7.59.0's normalizeChatCompletionStream outer async generator
// wrapper (lib/chat-completions/streaming.mjs:12-22) when wrapNormalizeChatCompletionStream() is
// active for chat.completions.create, while Stream.fromSSEResponse (used by responses.create)
// stays unchanged at 7.19.0..7.59.0 ticks.
// Measured against openai 7.59.0 on Node v24.22.2:
//   responses-empty:    start@0 done@7
//   completions-empty:  start@0 done@8
//   responses-eof:      start@0 chunk@9  chunk@15 done@22
//   completions-eof:    start@0 chunk@11 chunk@19 done@27
//   responses-done:     start@0 chunk@9  chunk@15 done@29
//   completions-done:   start@0 chunk@11 chunk@19 done@34
//   responses-badjson:  start@0 chunk@9  error@20
//   completions-badjson:start@0 chunk@11 error@23
//   responses-readfail: start@0 chunk@9  error@16
//   completions-readfail:start@0 chunk@11 error@19
func TestOpenAIChatCompletionsNormalizeWrapperTicks(t *testing.T) {
	t.Parallel()
	const oneChunk = `data: {"id":"1","choices":[{"index":0,"delta":{"content":"a"}}]}` + "\n\n"
	const twoChunksEOF = oneChunk + `data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"
	const twoChunksDone = twoChunksEOF + "data: [DONE]\n\n"
	const badJSON = oneChunk + "data: nope\n\n"

	for _, tc := range []struct {
		name           string
		chunks         []string
		chatCompletion bool
		wantErr        string
		want           []string
	}{
		{"responses-empty", nil, false, "", []string{"start@0", "done@7"}},
		{"completions-empty", nil, true, "", []string{"start@0", "done@8"}},
		{"responses-eof", []string{twoChunksEOF}, false, "", []string{"start@0", "chunk@9", "chunk@15", "done@22"}},
		{"completions-eof", []string{twoChunksEOF}, true, "", []string{"start@0", "chunk@11", "chunk@19", "done@27"}},
		{"responses-done", []string{twoChunksDone}, false, "", []string{"start@0", "chunk@9", "chunk@15", "done@29"}},
		{"completions-done", []string{twoChunksDone}, true, "", []string{"start@0", "chunk@11", "chunk@19", "done@34"}},
		{"responses-badjson", []string{badJSON}, false, "Error reading response: malformed server-sent event JSON.", []string{"start@0", "chunk@9", "error@20"}},
		{"completions-badjson", []string{badJSON}, true, "Error reading response: malformed server-sent event JSON.", []string{"start@0", "chunk@11", "error@23"}},
		{"responses-readfail", []string{oneChunk, readFailure}, false, "wire failed", []string{"start@0", "chunk@9", "error@16"}},
		{"completions-readfail", []string{oneChunk, readFailure}, true, "wire failed", []string{"start@0", "chunk@11", "error@19"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tick := 0
			var trace []string
			mark := func(label string) { trace = append(trace, fmt.Sprintf("%s@%d", label, tick)) }
			index := 0
			reader := &openAITestChunkReader{
				read: func() ([]byte, error) {
					tick += 3
					if index == len(tc.chunks) {
						return nil, io.EOF
					}
					chunk := tc.chunks[index]
					index++
					if chunk == readFailure {
						return nil, errors.New("wire failed")
					}
					return []byte(chunk), nil
				},
				close: func() error { return nil },
			}
			decoder := newOpenAIStreamDecoder(reader, nil, func() { tick++ })
			if tc.chatCompletion {
				decoder.decodeChunks()
				decoder.wrapNormalizeChatCompletionStream()
			}
			mark("start")
			for decoder.Next() {
				mark("chunk")
			}
			errText := ""
			if decoder.Err() != nil {
				errText = decoder.Err().Error()
				mark("error")
			} else {
				mark("done")
			}
			if errText != tc.wantErr {
				t.Fatalf("err = %q, want %q", errText, tc.wantErr)
			}
			if !reflect.DeepEqual(trace, tc.want) {
				t.Fatalf("trace = %v, want %v", trace, tc.want)
			}
		})
	}
}
