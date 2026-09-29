package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

// Oracle: exact openai6.40.0 core/streaming.mjs, with synchronous labels and an independent queueMicrotask clock. Native body awaits belong to the reader and contribute one tick here, not in the decoder.
// Retained trace: rpc33-observation-h1-followup/pipeline-probe.json. The tests compare the read/next boundaries, not a fixed delay per provider record.
func TestOpenAIStreamDecoderGeneratorBoundaries(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		chunks []string
		values []string
		trace  []string
	}{
		{"empty", nil, nil, []string{"next@0", "read@0", "done@4"}},
		{"buffered", []string{"data: 1\n\ndata: 2\n\n"}, []string{"1", "2"}, []string{"next@0", "read@0", "value@7", "next@7", "value@13", "next@13", "read@13", "done@17"}},
		{"split", []string{"data: 1\n", "\ndata: 2\n\n"}, []string{"1", "2"}, []string{"next@0", "read@0", "read@1", "value@8", "next@8", "value@14", "next@14", "read@14", "done@18"}},
		{"comment", []string{": comment\n\ndata: 1\n\n"}, []string{"1"}, []string{"next@0", "read@0", "value@9", "next@9", "read@9", "done@13"}},
		{"empty-frame", []string{"\n\ndata: 1\n\n"}, []string{"1"}, []string{"next@0", "read@0", "value@9", "next@9", "read@9", "done@13"}},
		{"bare-tail", []string{"data: 1"}, nil, []string{"next@0", "read@0", "read@1", "done@7"}},
		{"one-newline-tail", []string{"data: 1\n"}, nil, []string{"next@0", "read@0", "read@1", "done@7"}},
		{"cr-tail", []string{"data: 1\r\r"}, []string{"1"}, []string{"next@0", "read@0", "read@3", "value@9", "next@9", "done@11"}},
		{"done-and-after", []string{"data: [DONE]suffix\n\ndata: invalid\n\n"}, nil, []string{"next@0", "read@0", "read@9", "done@13"}},
		{"done-then-read", []string{"data: [DONE]\n\n", "data: 2\n\n"}, nil, []string{"next@0", "read@0", "read@5", "read@10", "done@14"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var trace, values []string
			tick := 0
			mark := func(label string) { trace = append(trace, fmt.Sprintf("%s@%d", label, tick)) }
			index := 0
			reader := &openAITestChunkReader{read: func() ([]byte, error) {
				mark("read")
				tick++
				if index == len(test.chunks) {
					return nil, io.EOF
				}
				chunk := test.chunks[index]
				index++
				return []byte(chunk), nil
			}}
			decoder := newOpenAIStreamDecoder(reader, func() { mark("next") }, func() { tick++ })
			for decoder.Next() {
				mark("value")
				values = append(values, decoder.Event().Data)
			}
			mark("done")
			if decoder.Err() != nil {
				t.Fatal(decoder.Err())
			}
			if !reflect.DeepEqual(values, test.values) || !reflect.DeepEqual(trace, test.trace) {
				t.Fatalf("values = %#v, want %#v\ntrace = %#v, want %#v", values, test.values, trace, test.trace)
			}
			before := tick
			if decoder.Next() || tick != before+1 {
				t.Fatal("completed JSON iterator adds only its caller await")
			}
		})
	}
}

func TestOpenAIStreamDecoderEOFAndDoneAreNotValues(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"data: 1", "data: 1\n", "event: message\n", "data: [DONE]\n\n", "data: [DONE]suffix\n\ndata: invalid\n\n"} {
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
		{"trailing-no-dispatch", "data: 1\n\ndata: 2\n", []serverSentEvent{{Data: "1", Raw: []string{"data: 1"}}}},
		{"crlf", "data: \"你好🐷\"\r\n\r\n", []serverSentEvent{{Data: "\"你好🐷\"", Raw: []string{"data: \"你好🐷\""}}}},
		{"cr", "data: 1\r\rdata: 2\r\r", []serverSentEvent{{Data: "1", Raw: []string{"data: 1"}}, {Data: "2", Raw: []string{"data: 2"}}}},
		{"mixed", "data: 1\r\ndata: 2\n\r\n", nil},
		{"bom-per-line", "\ufeffdata: 1\n\ufeff\n", []serverSentEvent{{Data: "1", Raw: []string{"data: 1"}}}},
		{"thread", "event: thread.run.failed\ndata: {\"error\":{\"message\":\"bad\"}}\n\n", []serverSentEvent{{Event: "thread.run.failed", Data: `{"event":"thread.run.failed","data":{"error":{"message":"bad"}}}`, Raw: []string{"event: thread.run.failed", `data: {"error":{"message":"bad"}}`}}}},
		{"ordinary-error-event", "event: error\ndata: {\"message\":\"normal\"}\n\n", []serverSentEvent{{Event: "error", Data: `{"message":"normal"}`, Raw: []string{"event: error", `data: {"message":"normal"}`}}}},
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
					got = append(got, decoder.Event())
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
		wantTick                  int
		wantClose                 bool
		wantAPI                   string
		wantSyntax                bool
	}{
		{name: "syntax", data: "nope", wantTick: 11, wantClose: true, wantSyntax: true},
		{name: "event-only", event: "message", wantTick: 11, wantClose: true, wantSyntax: true},
		{name: "event-only-cr", event: "message", ending: "\r\r", wantTick: 10, wantSyntax: true},
		{name: "syntax-cr", data: "nope", ending: "\r\r", wantTick: 10, wantSyntax: true},
		{name: "syntax-tail-chunk", data: "nope", ending: "\n\r\n", wantTick: 11, wantSyntax: true},
		{name: "API", data: `{"error":{"message":"bad","code":"code"}}`, wantTick: 11, wantClose: true, wantAPI: `{"message":"bad","code":"code"}`},
		{name: "transport", readErr: errors.New("wire failed"), wantTick: 4},
		{name: "abort", readErr: context.Canceled, wantTick: 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			tick, reads, closes := 0, 0, 0
			reader := &openAITestChunkReader{read: func() ([]byte, error) {
				tick++
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
				if tick != 7 {
					t.Errorf("body.return at tick %d, want 7 from Node trace", tick)
				}
				tick++ // Native body iterator return await, not an SDK generator hop.
				closes++
				return errors.New("cleanup error must not replace the parse/API error")
			}}
			decoder := newOpenAIStreamDecoder(reader, nil, func() { tick++ })
			if decoder.Next() || tick != test.wantTick || (closes == 1) != test.wantClose {
				t.Fatalf("tick=%d want %d, closes=%d", tick, test.wantTick, closes)
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

func TestOpenAIStreamDecoderDoneWaitsForEOF(t *testing.T) {
	t.Parallel()
	readingTail := make(chan struct{})
	release := make(chan struct{})
	result := make(chan bool, 1)
	calls := 0
	reader := &openAITestChunkReader{read: func() ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte("data: [DONE]suffix\n\n"), nil
		}
		close(readingTail)
		<-release
		return nil, io.EOF
	}}
	decoder := newOpenAIStreamDecoder(reader, nil, nil)
	go func() { result <- decoder.Next() }()
	select {
	case got := <-result:
		close(release)
		t.Fatalf("sentinel prematurely completed/yielded: %t", got)
	case <-readingTail:
		close(release)
		if <-result || decoder.Err() != nil {
			t.Fatalf("EOF after DONE: %v", decoder.Err())
		}
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
