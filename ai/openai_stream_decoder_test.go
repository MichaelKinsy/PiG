package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestOpenAIStreamDecoderEOFAndDoneAreNotValues(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"", ": comment", "data: [DONE]\n\n", "data: [DONE]"} {
		decoder := newOpenAIStreamDecoder(strings.NewReader(input), nil, nil)
		if decoder.Next() || decoder.Err() != nil {
			t.Errorf("input %q yielded %#v, err %v", input, decoder.Event(), decoder.Err())
		}
	}
}

func TestOpenAIStreamDecoderAPIErrors(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ payload, message string }{
		{`null`, ""}, {`false`, ""}, {`0`, ""}, {`-0.0`, ""}, {`1e-400`, ""}, {`""`, ""},
		{`{"message":"bad"}`, "bad"}, {`"bad"`, `"bad"`}, {`[]`, `[]`}, {`{}`, `{}`},
		{`1e400`, `null`}, {`{"message":1e400}`, `null`},
		{`{"2":2,"1":1,"message":false}`, `{"1":1,"2":2,"message":false}`},
	} {
		input := `data: {"error":` + test.payload + "}\n\n"
		decoder := newOpenAIStreamDecoder(strings.NewReader(input), nil, nil)
		if test.message == "" {
			if !decoder.Next() || decoder.Err() != nil {
				t.Fatalf("falsey error %s was not yielded: %v", test.payload, decoder.Err())
			}
		} else if decoder.Next() || decoder.Err() == nil || decoder.Err().Error() != test.message {
			t.Fatalf("error %s = %v, want %q", test.payload, decoder.Err(), test.message)
		}
	}
}

func TestOpenAIStreamDecoderSSEAndLineSemantics(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		input string
		want  []serverSentEvent
	}{
		{"multiline", "event: message\ndata: {\ndata: \"x\":1}\n\n", []serverSentEvent{{Event: "message", Data: "{\n\"x\":1}", Raw: []string{"event: message", "data: {", "data: \"x\":1}"}}}},
		{"comments-raw", ": comment\n\nid: unused\nretry: 100\ndata:1\n\n", []serverSentEvent{{Data: "1", Raw: []string{": comment", "id: unused", "retry: 100", "data:1"}}}},
		{"last-event", "event: first\nevent: second\ndata: true\n\n", []serverSentEvent{{Event: "second", Data: "true", Raw: []string{"event: first", "event: second", "data: true"}}}},
		{"empty-event", "event:\ndata: null\n\n", []serverSentEvent{{Data: "null", Raw: []string{"event:", "data: null"}}}},
		{"trailing-flushed-at-eof", "data: 1\n\ndata: 2\n", []serverSentEvent{{Data: "1", Raw: []string{"data: 1"}}, {Data: "2", Raw: []string{"data: 2"}}}},
		{"crlf", "data: \"你好🐷\"\r\n\r\n", []serverSentEvent{{Data: "\"你好🐷\"", Raw: []string{"data: \"你好🐷\""}}}},
		{"cr", "data: 1\r\rdata: 2\r\r", []serverSentEvent{{Data: "1", Raw: []string{"data: 1"}}, {Data: "2", Raw: []string{"data: 2"}}}},
		{"mixed", "data: 1\r\ndata: 2\n\r\n", nil},
		{"bom-per-line", "\ufeffdata: 1\n\ufeff\n", []serverSentEvent{{Data: "1", Raw: []string{"data: 1"}}}},
		{"thread", "event: thread.run.failed\ndata: {\"error\":{\"message\":\"bad\"}}\n\n", []serverSentEvent{{Event: "thread.run.failed", Data: `{"event":"thread.run.failed","data":{"error":{"message":"bad"}}}`, Raw: []string{"event: thread.run.failed", `data: {"error":{"message":"bad"}}`}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "mixed" {
				decoder := newOpenAIStreamDecoder(strings.NewReader(test.input), nil, nil)
				if decoder.Next() || decoder.Err() == nil {
					t.Fatal("joined data fields must fail JSON.parse, not dispatch separate values")
				}
				return
			}
			// Every split point includes splits inside UTF-8, CRLF and the double-newline delimiter.
			for split := 0; split <= len(test.input); split++ {
				chunks := []string{test.input[:split], test.input[split:]}
				reader := &openAITestChunkReader{read: func() ([]byte, error) {
					if len(chunks) == 0 {
						return nil, io.EOF
					}
					chunk := chunks[0]
					chunks = chunks[1:]
					return []byte(chunk), nil
				}}
				decoder := newOpenAIStreamDecoder(reader, nil, nil)
				var got []serverSentEvent
				for decoder.Next() {
					got = append(got, retainOpenAIEvent(decoder.Event()))
				}
				if decoder.Err() != nil || !reflect.DeepEqual(got, test.want) {
					t.Fatalf("split %d: events %#v, want %#v; err %v", split, got, test.want, decoder.Err())
				}
			}
		})
	}
}

// SDK internal/utils/bytes.mjs uses a non-streaming TextDecoder for each decoded line.
func TestOpenAIStreamDecoderUTF8Replacement(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ input, want string }{
		{"\xef\xbb\xbfhello", "hello"},
		{"\xe2\x82", "�"},
		{"\xe2\x82x", "�x"},
		{"\xff\xfe", "��"},
		{"\xed\xa0\x80", "���"},
		{"\xf0\x90\x80", "�"},
		{"\xe0\x80\x80", "���"},
	} {
		if got := decodeOpenAILine([]byte(test.input)); got != test.want {
			t.Errorf("%x decoded as %q, want %q", test.input, got, test.want)
		}
	}
}

func TestOpenAIStreamDecoderErrorsAndIteratorClose(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, data, event, ending string
		readErr                   error
		wantClose                 bool
		wantAPI                   string
		wantSyntax                bool
	}{
		{name: "syntax", data: "nope", wantClose: true, wantSyntax: true},
		{name: "event-only", event: "message", wantClose: true, wantSyntax: true},
		{name: "event-only-cr", event: "message", ending: "\r\r", wantClose: true, wantSyntax: true},
		{name: "syntax-cr", data: "nope", ending: "\r\r", wantClose: true, wantSyntax: true},
		{name: "syntax-tail-chunk", data: "nope", ending: "\n\r\n", wantClose: true, wantSyntax: true},
		{name: "API", data: `{"error":{"message":"bad","code":"code"}}`, wantClose: true, wantAPI: `{"message":"bad","code":"code"}`},
		{name: "transport", readErr: errors.New("wire failed"), wantClose: true},
		{name: "abort", readErr: context.Canceled, wantClose: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reads, closes := 0, 0
			reader := &openAITestChunkReader{read: func() ([]byte, error) {
				reads++
				if test.readErr != nil {
					return nil, test.readErr
				}
				if reads > 1 {
					if test.ending == "" {
						t.Fatal("JSON error must not request more body values")
					}
					return nil, io.EOF
				}
				ending := test.ending
				if ending == "" {
					ending = "\n\n"
				}
				if test.event != "" {
					return []byte("event: " + test.event + ending), nil
				}
				return []byte("data: " + test.data + ending), nil
			}, close: func() error {
				closes++
				return errors.New("cleanup error must not replace the parse/API error")
			}}
			// The suspension points of these cases are pinned by TestOpenAIStreamDecoderFollowsOpenAI719SuspensionPoints.
			decoder := newOpenAIStreamDecoder(reader, nil, nil)
			if decoder.Next() || (closes == 1) != test.wantClose {
				t.Fatalf("closes=%d, want the body returned: %v", closes, test.wantClose)
			}
			var decodeErr *openAIStreamDecodeError
			switch {
			case test.wantSyntax || test.wantAPI != "":
				if !errors.As(decoder.Err(), &decodeErr) || decodeErr.SSE.Data != test.data || string(decodeErr.APIError) != test.wantAPI {
					t.Fatalf("lost error payload: %#v", decoder.Err())
				}
				var syntax *json.SyntaxError
				if errors.As(decodeErr, &syntax) != test.wantSyntax {
					t.Fatalf("wrong error kind: %#v", decodeErr)
				}
			case test.name == "abort":
				if decoder.Err() != nil {
					t.Fatalf("decoder propagated AbortError instead of returning: %v", decoder.Err())
				}
			case !errors.Is(decoder.Err(), test.readErr):
				t.Fatalf("transport cause changed: %v", decoder.Err())
			}
		})
	}
}

func TestOpenAIStreamDecoderLargeBodyValue(t *testing.T) {
	t.Parallel()
	text := strings.Repeat("🐷", 32768)
	input := "data: \"" + text + "\"\n\n"
	for _, reader := range []io.Reader{strings.NewReader(input), &openAITestChunkReader{read: func() ([]byte, error) {
		value := input
		input = ""
		if value == "" {
			return nil, io.EOF
		}
		return []byte(value), nil
	}}} {
		decoder := newOpenAIStreamDecoder(reader, nil, nil)
		if !decoder.Next() || decoder.Event().Data != "\""+text+"\"" || decoder.Next() || decoder.Err() != nil {
			t.Fatalf("large value changed or truncated: %v", decoder.Err())
		}
	}
}

func BenchmarkOpenAIStreamDecoder(b *testing.B) {
	for _, test := range []struct {
		name, input string
		values      int
	}{
		{"buffered", strings.Repeat("data: {\"delta\":\"text\"}\n\n", 256), 256},
		{"long-line", "data: \"" + strings.Repeat("你好🐷", 32768) + "\"\n\n", 1},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(test.input)))
			for b.Loop() {
				decoder := newOpenAIStreamDecoder(strings.NewReader(test.input), nil, nil)
				values := 0
				for decoder.Next() {
					values++
				}
				if values != test.values || decoder.Err() != nil {
					b.Fatalf("values=%d error=%v", values, decoder.Err())
				}
			}
		})
	}
}

// openAIToolDeltaRecord is a Chat Completions tool-call argument delta as a large write streams it: the argument text carries escapes on almost every record.
const openAIToolDeltaRecord = `data: {"id":"chatcmpl-9","object":"chat.completion.chunk","created":1760000000,"model":"gpt-x","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"line of text\n\t\"quoted\""}}]},"finish_reason":null}]}` + "\n\n"

type openAITestChunkReader struct {
	read  func() ([]byte, error)
	close func() error
}

func (reader *openAITestChunkReader) Read([]byte) (int, error) {
	panic("native chunk path must not split body values into parser buffer reads")
}
func (reader *openAITestChunkReader) ReadChunk() ([]byte, error) { return reader.read() }
func (reader *openAITestChunkReader) Close() error {
	if reader.close != nil {
		return reader.close()
	}
	return nil
}

// 7.19.0 fromSSEResponse: an event named error throws an APIError built from `data?.error ?? data`, whatever the record holds.
func TestOpenAIStreamDecoderErrorEvent(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ payload, message string }{
		{`{"message":"normal"}`, "normal"}, {`{"error":{"message":"inner"},"message":"outer"}`, "inner"}, {`{"error":null,"message":"outer"}`, "outer"},
		{`null`, "(no status code or body)"}, {`0`, "(no status code or body)"}, {`"text"`, `"text"`}, {`{"error":0}`, "(no status code or body)"}, {`[1]`, "[1]"},
	} {
		decoder := newOpenAIStreamDecoder(strings.NewReader("event: error\ndata: "+test.payload+"\n\n"), nil, nil)
		if decoder.Next() || decoder.Err() == nil || decoder.Err().Error() != test.message {
			t.Errorf("event error %s = %v, want %q", test.payload, decoder.Err(), test.message)
		}
	}
}

// openai 7.19.0 fromSSEResponse breaks out of its loop at the [DONE] sentinel and the body is closed: the stream ends at once, without waiting for the server to close the connection.
func TestOpenAIStreamDecoderDoneEndsTheStream(t *testing.T) {
	t.Parallel()
	calls := 0
	reader := &openAITestChunkReader{read: func() ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte("data: [DONE]\n\n"), nil
		}
		t.Error("the body was read after the sentinel")
		return nil, io.EOF
	}}
	decoder := newOpenAIStreamDecoder(reader, nil, nil)
	if decoder.Next() || decoder.Err() != nil {
		t.Fatalf("[DONE] yielded or failed: %v", decoder.Err())
	}
	if decoder.Next() || calls != 1 {
		t.Fatalf("after the sentinel: %d reads", calls)
	}
}

// retainOpenAIEvent is an event that outlives the decoder's next record: Raw is the decoder's array, which the next record overwrites.
func retainOpenAIEvent(event serverSentEvent) serverSentEvent {
	event.Raw = slices.Clone(event.Raw)
	return event
}
