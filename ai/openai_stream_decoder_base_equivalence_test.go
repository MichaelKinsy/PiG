package ai

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// lw-sse (S29) changed the openai-completions decoder's record path: a line-ending search bounded by the distance to the next line
// ending, the raw line array reused across events, and, for chat completions, the chunk decode in place of json.Valid and the
// "error" member probe. These tests hold it to the decoder at b02c2ceabc (openai_stream_decoder_reference_test.go) over random
// bodies and random splits: every record, every suspension tick, every body read and close in the same order, and the same error.

// openAISSEBodyLines are the lines a body is built from: chat chunks the chunk decode reads and ones it leaves to json.Unmarshal,
// escapes, multi-line data, event/id/retry fields, comments, a field without a colon, the sentinel, error and thread.* events, and
// lines that are not JSON.
var openAISSEBodyLines = []string{
	`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"gpt-x","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
	`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"caf\u00e9 \ud83d\udc37 \"q\"\n"}}]}`,
	strings.TrimSuffix(openAIToolDeltaRecord, "\n\n"),
	`data: {"id":"c","model":"m\u0031","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"write","arguments":"{\"path\":"}},{"index":1,"function":{"arguments":"` + strings.Repeat(`x\\y`, 90) + `"}}]}},{"index":1,"delta":{"role":"assistant"}}]}`,
	`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":null}`,
	`data: {"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":1}}`,
	`data: {"Model":"folded","choices":[]}`,
	`data: {"error":{"message":"bad","code":1}}`,
	`data: {"\u0065rror":{"message":"escaped key"}}`,
	`data: {"x":{"error":1},"choices":[]}`,
	`data: {"choices":[{"delta":{"content":"a"}}]} `,
	`data: {"a":`,
	`data: 1}`,
	`data:{"choices":[]}`,
	`data:`,
	`data: [DONE]`,
	`data: not json`,
	`data: 12`,
	`event: error`,
	`event: thread.run.step`,
	`event: message`,
	`event:`,
	`id: 7`,
	`retry: 100`,
	`: keepalive`,
	`data`,
	"data: \"\xff\"",
	"data: \"\xe4\xbd\"",
	"data: \"\xe4\xbd\xa0\"",
}

var openAISSEBodyEndings = []string{"\n", "\r", "\r\n"}

// randomOpenAISSEBody is a body of up to 30 lines and blank lines, each ended by an LF, a CR or a CRLF, some after a BOM.
func randomOpenAISSEBody(random *rand.Rand) []byte {
	var body []byte
	for range 1 + random.IntN(30) {
		if random.IntN(16) == 0 {
			body = append(body, "\ufeff"...)
		}
		if random.IntN(3) != 0 {
			body = append(body, openAISSEBodyLines[random.IntN(len(openAISSEBodyLines))]...)
		}
		body = append(body, openAISSEBodyEndings[random.IntN(len(openAISSEBodyEndings))]...)
		if random.IntN(3) == 0 {
			body = append(body, openAISSEBodyEndings[random.IntN(len(openAISSEBodyEndings))]...)
		}
	}
	if random.IntN(4) == 0 {
		// A body that ends inside a line or inside a UTF-8 sequence.
		body = append(body, "data: {\"x\":\"\xe4"[:random.IntN(14)]...)
	}
	return body
}

// randomOpenAISSESplit cuts body into body values of 1 to 64 bytes, or keeps it whole.
func randomOpenAISSESplit(random *rand.Rand, body []byte) [][]byte {
	if random.IntN(8) == 0 {
		return [][]byte{body}
	}
	var values [][]byte
	for rest := body; len(rest) > 0; {
		size := min(len(rest), 1+random.IntN(64))
		values = append(values, rest[:size])
		rest = rest[size:]
	}
	return values
}

// tracedOpenAIBody serves values as body values (ReadChunk, reusing one buffer) or as Read results, and traces each read and close.
type tracedOpenAIBody struct {
	values [][]byte
	buffer []byte
	trace  *strings.Builder
}

func (body *tracedOpenAIBody) next() []byte {
	fmt.Fprintf(body.trace, "read;")
	if len(body.values) == 0 {
		return nil
	}
	for i := range body.buffer {
		body.buffer[i] = '#'
	}
	body.buffer = append(body.buffer[:0], body.values[0]...)
	body.values = body.values[1:]
	return body.buffer
}

func (body *tracedOpenAIBody) Close() error {
	fmt.Fprintf(body.trace, "close;")
	return nil
}

type tracedOpenAIChunkBody struct{ tracedOpenAIBody }

func (body *tracedOpenAIChunkBody) Read([]byte) (int, error) { panic("chunk reader only") }

func (body *tracedOpenAIChunkBody) ReadChunk() ([]byte, error) {
	if value := body.next(); value != nil {
		return value, nil
	}
	return nil, io.EOF
}

type tracedOpenAIReadBody struct{ tracedOpenAIBody }

func (body *tracedOpenAIReadBody) Read(buffer []byte) (int, error) {
	if len(body.values) > 0 && len(body.values[0]) > len(buffer) {
		body.values = append([][]byte{body.values[0][:len(buffer)], body.values[0][len(buffer):]}, body.values[1:]...)
	}
	value := body.next()
	if value == nil {
		return 0, io.EOF
	}
	return copy(buffer, value), nil
}

// traceOpenAIDecoder runs a decoder to its end and traces what an observer of the provider sees: each beforeNext, suspension tick,
// body read and close in order, each record with its raw lines, and the error. With chunks, it also checks each chunk the decoder
// decoded against a fresh decode of the record, and that the decoder decoded every record a fresh decode reads.
func traceOpenAIDecoder(t *testing.T, decoder providerSSEDecoder, chunks func() (oaiChunk, bool), trace *strings.Builder) string {
	for decoder.Next() {
		event := decoder.Event()
		fmt.Fprintf(trace, "record %q %q %q;", event.Event, event.Data, event.Raw)
		if chunks == nil {
			continue
		}
		got, ok := chunks()
		want, wantOK := parseOpenAIChunkFast(event.Data)
		if event.Event == "error" || strings.HasPrefix(event.Event, "thread.") {
			wantOK = false
		}
		if ok != wantOK || ok && !reflect.DeepEqual(got, want) {
			t.Fatalf("record %q: chunk %v %#v, want %v %#v", event.Data, ok, got, wantOK, want)
		}
	}
	fmt.Fprintf(trace, "end %q", errorText(decoder.Err()))
	return trace.String()
}

func TestOpenAIStreamDecoderMatchesBaseDecoder(t *testing.T) {
	random := rand.New(rand.NewPCG(29, 2910))
	for run := range 6000 {
		body := randomOpenAISSEBody(random)
		values := randomOpenAISSESplit(random, body)
		chunkReader := run%2 == 0
		decodeChunks := run%3 != 0
		newBody := func(trace *strings.Builder) io.Reader {
			traced := tracedOpenAIBody{values: append([][]byte(nil), values...), trace: trace}
			if chunkReader {
				return &tracedOpenAIChunkBody{traced}
			}
			return &tracedOpenAIReadBody{traced}
		}
		var gotTrace, wantTrace strings.Builder
		decoder := newOpenAIStreamDecoder(newBody(&gotTrace), func() { gotTrace.WriteString("next;") }, func() { gotTrace.WriteString("tick;") })
		var chunks func() (oaiChunk, bool)
		if decodeChunks {
			decoder.decodeChunks()
			chunks = decoder.Chunk
		}
		got := traceOpenAIDecoder(t, decoder, chunks, &gotTrace)
		reference := newReferenceOpenAIStreamDecoder(newBody(&wantTrace), func() { wantTrace.WriteString("next;") }, func() { wantTrace.WriteString("tick;") })
		want := traceOpenAIDecoder(t, reference, nil, &wantTrace)
		if got != want {
			t.Fatalf("body %q in %d values (chunk reader %v, chunks %v):\n got %s\nwant %s", body, len(values), chunkReader, decodeChunks, got, want)
		}
	}
}

// One chunk decoder reused across records decodes each as a fresh one does, and a chunk read in place of json.Unmarshal is the one
// json.Unmarshal reads (checkChunkFast).
func TestOpenAIChunkDecoderReuseMatchesFreshDecode(t *testing.T) {
	random := rand.New(rand.NewPCG(9, 10))
	var decoder openAIChunkDecoder
	seeds := chunkSeeds()
	for _, line := range openAISSEBodyLines {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			seeds = append(seeds, data)
		}
	}
	for range 20000 {
		data := seeds[random.IntN(len(seeds))]
		got, ok := decoder.decode(data)
		want, wantOK := parseOpenAIChunkFast(data)
		if ok != wantOK || ok && !reflect.DeepEqual(got, want) {
			t.Fatalf("%q: reused %v %#v, fresh %v %#v", data, ok, got, wantOK, want)
		}
		checkChunkFast(t, data)
	}
}

// openAIChunkResponseModel is parseSSE's response model expression at b02c2ceabc.
func TestOpenAIChunkResponseModelMatchesBase(t *testing.T) {
	base := func(raw json.RawMessage, configured string) string {
		var responseModel string
		if inner, ok := simpleJSONString(raw); ok {
			responseModel = string(inner)
		} else if json.Unmarshal(raw, &responseModel) != nil {
			responseModel = ""
		}
		if responseModel == configured {
			responseModel = ""
		}
		return responseModel
	}
	for _, raw := range []string{"", `"gpt-x"`, `"other"`, `""`, `"\u0067pt-x"`, `"m\u0031"`, `"a\"b"`, `null`, `1`, `{"a":1}`, `["gpt-x"]`, "\"\xff\"", `"`} {
		for _, configured := range []string{"gpt-x", "", "m1"} {
			var model json.RawMessage
			if raw != "" {
				model = json.RawMessage(raw)
			}
			if got, want := openAIChunkResponseModel(model, configured), base(model, configured); got != want {
				t.Errorf("model %q configured %q: %q, want %q", raw, configured, got, want)
			}
		}
	}
}
