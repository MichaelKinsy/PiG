package ai

import (
	"context"
	"io"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// The Anthropic SSE reader appends each chunk to a line buffer and decodes well-formed UTF-8 without a per-rune loop. The references below are the decoder and line draining it replaced, kept verbatim; the differential tests require the same text for every split of the same bytes and the same events for every split of the same stream.

type referenceWebTextDecoder struct {
	pending []byte
	started bool
}

func (decoder *referenceWebTextDecoder) decode(data []byte, stream bool) string {
	data = append(decoder.pending, data...)
	decoder.pending = nil
	var text strings.Builder
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			if stream && !utf8.FullRune(data) {
				decoder.pending = append([]byte(nil), data...)
				break
			}
			text.WriteRune(utf8.RuneError)
			data = data[openAIInvalidUTF8Prefix(data):]
			decoder.started = true
			continue
		}
		wasStarted := decoder.started
		decoder.started = true
		data = data[size:]
		if !wasStarted && r == '\uFEFF' {
			continue
		}
		text.WriteRune(r)
	}
	return text.String()
}

// referenceAnthropicLines is the reader's line draining before the line buffer: the decoded text was concatenated onto a string and consumed from its start.
type referenceAnthropicLines struct {
	state  anthropicSSEState
	buffer string
	events []serverSentEvent
}

func (reader *referenceAnthropicLines) drain(final bool) {
	for {
		line, rest, ok := anthropicConsumeLine(reader.buffer, final)
		if !ok {
			break
		}
		reader.buffer = rest
		if event, ok := reader.state.decode(line); ok {
			reader.events = append(reader.events, event)
		}
	}
	if !final {
		return
	}
	if len(reader.buffer) > 0 {
		if event, ok := reader.state.decode(reader.buffer); ok {
			reader.events = append(reader.events, event)
		}
	}
	if event, ok := reader.state.flush(); ok {
		reader.events = append(reader.events, event)
	}
}

var webTextAlphabet = [][]byte{{'a'}, {'\r'}, {'\n'}, {'\r', '\n'}, {0xef, 0xbb, 0xbf}, {0xff}, {0xc3}, {0xc3, 0xa9}, {0xe4, 0xbd}, {0xe4, 0xbd, 0xa0}, {0xf0, 0x9f, 0x90}, {0xf0, 0x9f, 0x90, 0xb7}, {0xed, 0xa0, 0x80}, {0xf4, 0x90}, {0x80}}

func randomSplits(random *rand.Rand, data []byte, most int) [][]byte {
	var chunks [][]byte
	for len(data) > 0 {
		size := min(len(data), 1+random.IntN(most))
		chunks, data = append(chunks, data[:size]), data[size:]
	}
	return chunks
}

func TestWebTextDecoderMatchesReference(t *testing.T) {
	random := rand.New(rand.NewPCG(11, 12))
	for range 5000 {
		var data []byte
		for range random.IntN(16) {
			data = append(data, webTextAlphabet[random.IntN(len(webTextAlphabet))]...)
		}
		var got, want strings.Builder
		var appended []byte
		decoder, appender, reference := &webTextDecoder{}, &webTextDecoder{}, &referenceWebTextDecoder{}
		for _, chunk := range randomSplits(random, data, 6) {
			got.WriteString(decoder.decode(chunk, true))
			appended = appender.decodeTo(appended, chunk, true)
			want.WriteString(reference.decode(chunk, true))
		}
		got.WriteString(decoder.decode(nil, false))
		appended = appender.decodeTo(appended, nil, false)
		want.WriteString(reference.decode(nil, false))
		if got.String() != want.String() || string(appended) != want.String() {
			t.Fatalf("decoding % x: got %q (appending %q), want %q", data, got.String(), appended, want.String())
		}
	}
}

var anthropicSSEAlphabet = []string{"\n", "\r", "\r\n", "\n\n", "event: content_block_delta", "event: ping", "data: ", "data:{\"type\":\"x\"}", ": comment", "id", "\ufeff", "é", "\xff", strings.Repeat("y", 300)}

func TestAnthropicSSEReaderMatchesReferenceLines(t *testing.T) {
	random := rand.New(rand.NewPCG(13, 14))
	for range 3000 {
		var stream []byte
		for range random.IntN(24) {
			stream = append(stream, anthropicSSEAlphabet[random.IntN(len(anthropicSSEAlphabet))]...)
		}
		chunks := randomSplits(random, stream, 1+random.IntN(64))
		reference := &referenceAnthropicLines{}
		decoder := &referenceWebTextDecoder{}
		for _, chunk := range chunks {
			reference.buffer += decoder.decode(chunk, true)
			reference.drain(false)
		}
		reference.buffer += decoder.decode(nil, false)
		reference.drain(true)

		var body []byte
		for _, chunk := range chunks {
			body = append(body, chunk...)
		}
		reader := &anthropicSSEReader{ctx: context.Background(), source: &splitChunkSource{chunks: chunks}, suspend: func() {}}
		var got []serverSentEvent
		for {
			event, ok, err := reader.next()
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				break
			}
			got = append(got, event)
		}
		if !reflect.DeepEqual(got, reference.events) && (len(got) != 0 || len(reference.events) != 0) {
			t.Fatalf("stream %q in %d chunks:\n got %q\nwant %q", body, len(chunks), got, reference.events)
		}
	}
}

// splitChunkSource returns each chunk in a buffer it overwrites on the next read, then io.EOF.
type splitChunkSource struct {
	chunks [][]byte
	buffer []byte
}

func (source *splitChunkSource) ReadChunk() ([]byte, error) {
	for i := range source.buffer {
		source.buffer[i] = '#'
	}
	if len(source.chunks) == 0 {
		return nil, io.EOF
	}
	source.buffer = append(source.buffer[:0], source.chunks[0]...)
	source.chunks = source.chunks[1:]
	return source.buffer, nil
}
