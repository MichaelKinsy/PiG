package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// The decoder's framing and its "error" member probe replaced a copy per layer and a map decode of every record that holds an escape. These tests hold the replacements to the implementations they replaced, which are kept here verbatim as references: the same lines for every split of the same bytes, the same parse outcome for every record, and the same events when the reader reuses its buffer.

// referenceOpenAILineDecoder is the line decoder before in-place splitting: it copied every chunk into its buffer and each line into its own string.
type referenceOpenAILineDecoder struct {
	buffer        []byte
	searchFrom    int
	skipLeadingLF bool
}

func (decoder *referenceOpenAILineDecoder) decode(chunk []byte) []string {
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
	decoder.buffer = append(decoder.buffer, chunk...)
	var lines []string
	for {
		position := bytes.IndexAny(decoder.buffer[decoder.searchFrom:], "\r\n")
		if position < 0 {
			decoder.searchFrom = len(decoder.buffer)
			return lines
		}
		position += decoder.searchFrom
		lines = append(lines, decodeOpenAILine(decoder.buffer[:position]))
		carriage := decoder.buffer[position] == '\r'
		decoder.buffer = decoder.buffer[position+1:]
		decoder.searchFrom = 0
		if carriage {
			if len(decoder.buffer) > 0 && decoder.buffer[0] == '\n' {
				decoder.buffer = decoder.buffer[1:]
			} else if len(decoder.buffer) == 0 {
				decoder.skipLeadingLF = true
			}
		}
		if len(decoder.buffer) == 0 {
			decoder.buffer = nil
		}
	}
}

func (decoder *referenceOpenAILineDecoder) flush() []string {
	decoder.skipLeadingLF = false
	if len(decoder.buffer) == 0 {
		return nil
	}
	return decoder.decode([]byte{'\n'})
}

// referenceOpenAIParse is parse before the member probe: a record that holds "error" or an escape was decoded into a map of its members.
func referenceOpenAIParse(sse *serverSentEvent) error {
	data := json.RawMessage(sse.Data)
	if !json.Valid(data) {
		var invalid json.RawMessage
		err := json.Unmarshal(data, &invalid)
		return &openAIStreamDecodeError{SSE: *sse, Cause: err}
	}
	if strings.HasPrefix(sse.Event, "thread.") {
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
	if sse.Event == "error" {
		if value := bytes.TrimLeft(data, " \t\r\n"); len(value) > 0 && value[0] == '{' {
			if err := json.Unmarshal(data, &object); err != nil {
				return err
			}
		}
		apiError := object["error"]
		if len(apiError) == 0 || string(apiError) == "null" {
			apiError = data
		}
		err := openAIStreamAPIError(apiError)
		if err == nil {
			err = errors.New("(no status code or body)")
		}
		return &openAIStreamDecodeError{SSE: *sse, APIError: apiError, Cause: err}
	}
	if value := bytes.TrimLeft(data, " \t\r\n"); len(value) > 0 && value[0] == '{' && (bytes.Contains(data, []byte("error")) || bytes.IndexByte(data, '\\') >= 0) {
		if err := json.Unmarshal(data, &object); err != nil {
			return err
		}
	}
	if err := openAIStreamAPIError(object["error"]); err != nil {
		return &openAIStreamDecodeError{SSE: *sse, APIError: object["error"], Cause: err}
	}
	return nil
}

// openAILineAlphabet mixes every byte the line decoder treats specially: CR, LF, a BOM, invalid and multi-byte UTF-8.
var openAILineAlphabet = [][]byte{{'a'}, {'b'}, {' '}, {':'}, {'\r'}, {'\n'}, {'\r', '\n'}, {0xef, 0xbb, 0xbf}, {0xff}, {0xe4, 0xbd}, {0xe4, 0xbd, 0xa0}, []byte("data: {\"x\":1}")}

func TestOpenAILineDecoderMatchesReference(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 2))
	for range 3000 {
		var text []byte
		for range random.IntN(40) {
			text = append(text, openAILineAlphabet[random.IntN(len(openAILineAlphabet))]...)
		}
		var got, want []string
		decoder, reference := &openAILineDecoder{}, &referenceOpenAILineDecoder{}
		for rest := text; len(rest) > 0; {
			size := min(len(rest), 1+random.IntN(8))
			chunk := bytes.Clone(rest[:size])
			got = append(got, decoder.decode(chunk)...)
			// The decoder must not keep the chunk: its caller reuses it.
			for i := range chunk {
				chunk[i] = '#'
			}
			want = append(want, reference.decode(rest[:size])...)
			rest = rest[size:]
		}
		got = append(got, decoder.flush()...)
		want = append(want, reference.flush()...)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("lines of %q:\n got %q\nwant %q", text, got, want)
		}
	}
}

// referenceOpenAIDoubleNewlineIndex is findDoubleNewlineIndex before the byte search: it tested every position.
func referenceOpenAIDoubleNewlineIndex(data []byte) int {
	for i := 0; i+1 < len(data); i++ {
		if first := openAILineEndingLength(data, i); first > 0 {
			if second := openAILineEndingLength(data, i+first); second > 0 {
				return i + first + second
			}
		}
	}
	return -1
}

func TestOpenAIDoubleNewlineIndexMatchesReference(t *testing.T) {
	random := rand.New(rand.NewPCG(5, 6))
	for range 20000 {
		var data []byte
		for range random.IntN(24) {
			data = append(data, openAILineAlphabet[random.IntN(len(openAILineAlphabet))]...)
		}
		if got, want := openAIDoubleNewlineIndex(data), referenceOpenAIDoubleNewlineIndex(data); got != want {
			t.Fatalf("double newline in %q at %d, want %d", data, got, want)
		}
	}
}

// Records far longer than the first line-ending search span, with line endings on either side of each span boundary.
func TestOpenAIDoubleNewlineIndexMatchesReferenceAcrossSpans(t *testing.T) {
	random := rand.New(rand.NewPCG(7, 8))
	endings := []string{"\n", "\r", "\r\n"}
	for range 4000 {
		data := bytes.Repeat([]byte{'x'}, random.IntN(3*openAILineEndingFirstSpan+600))
		for range random.IntN(8) {
			if len(data) == 0 {
				break
			}
			// Half of the endings come in pairs, which delimit a record.
			ending := endings[random.IntN(len(endings))]
			if random.IntN(2) == 0 {
				ending += endings[random.IntN(len(endings))]
			}
			copy(data[random.IntN(len(data)):], ending)
		}
		if got, want := openAIDoubleNewlineIndex(data), referenceOpenAIDoubleNewlineIndex(data); got != want {
			t.Fatalf("double newline in %d bytes at %d, want %d", len(data), got, want)
		}
	}
}

func TestOpenAIStreamParseMatchesMapDecodeRule(t *testing.T) {
	records := []string{
		`{"choices":[{"delta":{"content":"a\nb"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{\"error\":1}"}}]}}]}`,
		`{"error":{"message":"bad"}}`,
		` {"error":{"message":"bad","code":1}} `,
		`{"\u0065rror":{"message":"escaped key"}}`,
		`{"err\u006fr":"escaped key, string"}`,
		`{"error":null}`,
		`{"error":0}`,
		`{"error":-0.0}`,
		`{"error":1e999}`,
		`{"error":""}`,
		`{"error":false}`,
		`{"error":[1,2]}`,
		`{"error":{"message":""}}`,
		`{"error":{"message":7}}`,
		`{"error":1,"error":null}`,
		`{"error":null,"error":{"message":"last wins"}}`,
		`{"x":{"error":{"message":"nested"}}}`,
		`{"x":["error",{"error":1}],"y":"\"error\""}`,
		`{"Error":{"message":"case differs"}}`,
		`{"errors":{"message":"longer key"}}`,
		`{"a":1,"b":true,"c":null,"d":[],"e":{},"f":"\\","error":"tail"}`,
		`{ "a" : 1 , "error" : { "message" : "spaced" } }`,
		"{\n\t\"a\": \"x\\ty\",\r\n\t\"error\": {\"message\": \"crlf\"}\n}",
		`{"\u00e9rror":1}`,
		`{"a\\":"b","error\\":1}`,
		`[{"error":1}]`,
		`"error"`,
		`"\u0065"`,
		`12`,
		`null`,
		`{}`,
		`{"error":{"message":"x"`,
		`not json`,
		``,
	}
	for _, event := range []string{"", "message", "error", "thread.run.failed", "response.output_text.delta"} {
		for _, record := range records {
			got := serverSentEvent{Event: event, Data: record}
			want := got
			decoder := &openAIStreamDecoder{}
			gotErr, wantErr := decoder.parse(&got), referenceOpenAIParse(&want)
			if got.Data != want.Data || errorText(gotErr) != errorText(wantErr) || !bytes.Equal(apiErrorOf(gotErr), apiErrorOf(wantErr)) {
				t.Errorf("event %q record %q:\n got data %q error %q apiError %q\nwant data %q error %q apiError %q", event, record, got.Data, errorText(gotErr), apiErrorOf(gotErr), want.Data, errorText(wantErr), apiErrorOf(wantErr))
			}
		}
	}
}

func apiErrorOf(err error) json.RawMessage {
	if decodeErr, ok := errors.AsType[*openAIStreamDecodeError](err); ok {
		return decodeErr.APIError
	}
	return nil
}

// reusingChunkReader returns each body value in one buffer that it overwrites on the next call, as the reader's contract allows.
type reusingChunkReader struct {
	values [][]byte
	buffer []byte
}

func (reader *reusingChunkReader) Read([]byte) (int, error) { panic("chunk reader only") }

func (reader *reusingChunkReader) ReadChunk() ([]byte, error) {
	for i := range reader.buffer {
		reader.buffer[i] = '#'
	}
	if len(reader.values) == 0 {
		return nil, io.EOF
	}
	reader.buffer = append(reader.buffer[:0], reader.values[0]...)
	reader.values = reader.values[1:]
	return reader.buffer, nil
}

func decodeAllOpenAIEvents(reader io.Reader) ([]serverSentEvent, string) {
	decoder := newOpenAIStreamDecoder(reader, nil, nil)
	var events []serverSentEvent
	for decoder.Next() {
		events = append(events, retainOpenAIEvent(decoder.Event()))
	}
	return events, errorText(decoder.Err())
}

func TestOpenAIStreamDecoderReusedReaderBufferMatchesWholeBody(t *testing.T) {
	random := rand.New(rand.NewPCG(3, 4))
	events := []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"hi\\n\"}}]}\n\n",
		"event: message\r\ndata: {\"a\":\r\ndata: 1}\r\n\r\n",
		": keepalive\n\n",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"arguments\":\"" + strings.Repeat("x\\\"y", 700) + "\"}}]}}]}\n\n",
		"\ufeffdata: \"你好🐷\"\r\r",
		"data: {\"x\":\"\xff\"}\n\n",
		"event: thread.run.step\ndata: {\"id\":1}\n\n",
	}
	for range 400 {
		var body []byte
		for range 1 + random.IntN(12) {
			body = append(body, events[random.IntN(len(events))]...)
		}
		if random.IntN(2) == 0 {
			body = append(body, "data: [DONE]\n\n"...)
		}
		var values [][]byte
		for rest := body; len(rest) > 0; {
			size := min(len(rest), 1+random.IntN(300))
			values = append(values, rest[:size])
			rest = rest[size:]
		}
		got, gotErr := decodeAllOpenAIEvents(&reusingChunkReader{values: values})
		want, wantErr := decodeAllOpenAIEvents(bytes.NewReader(body))
		if !reflect.DeepEqual(got, want) || gotErr != wantErr {
			t.Fatalf("body %q split %d ways:\n got %q %s\nwant %q %s", body, len(values), got, gotErr, want, wantErr)
		}
	}
}
