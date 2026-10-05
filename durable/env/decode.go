package env

// Ports packages/durable/src/env/decode.ts
//
// Decoding that matches a TextDecoder's decode of the whole input when the input arrives in pieces. Node's streaming
// decoder with BOM handling can drop a U+FEFF that follows a chunk boundary, not only a leading byte-order mark, so
// these decoders keep BOM handling off and drop a leading mark themselves.

import (
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// RangeDecoder is a streaming UTF-8 decoder for a byte range, like a TextDecoder with ignoreBOM: a character split
// across two chunks is held back until its remaining bytes arrive, each maximal invalid subpart becomes one U+FFFD, and
// no byte-order mark is dropped. Callers that start at the beginning of a file skip a leading mark themselves.
type RangeDecoder struct {
	pending []byte
}

// NewRangeDecoder returns a decoder for a byte range (upstream rangeDecoder).
func NewRangeDecoder() *RangeDecoder { return &RangeDecoder{} }

// Decode returns the text of the complete characters in the held bytes plus chunk.
func (decoder *RangeDecoder) Decode(chunk []byte) string {
	data := chunk
	if len(decoder.pending) > 0 {
		data = append(append(make([]byte, 0, len(decoder.pending)+len(chunk)), decoder.pending...), chunk...)
	}
	held := incompleteTail(data)
	decoder.pending = append(decoder.pending[:0], data[len(data)-held:]...)
	return jsstring.FromUTF8(data[:len(data)-held])
}

// Flush returns the replacement character for a character the stream ended in the middle of, or "".
func (decoder *RangeDecoder) Flush() string {
	if len(decoder.pending) == 0 {
		return ""
	}
	decoder.pending = decoder.pending[:0]
	return string(utf8.RuneError)
}

// incompleteTail is the length of a trailing, still-valid prefix of a multi-byte character, or 0.
func incompleteTail(data []byte) int {
	for tail := 1; tail <= min(3, len(data)); tail++ {
		if suffix := data[len(data)-tail:]; utf8.RuneStart(suffix[0]) && !utf8.FullRune(suffix) {
			return tail
		}
	}
	return 0
}

// StartsWithBom reports whether decoding the whole input drops its first three bytes as a byte-order mark.
func StartsWithBom(firstBytes []byte) bool {
	return len(firstBytes) >= 3 && firstBytes[0] == 0xef && firstBytes[1] == 0xbb && firstBytes[2] == 0xbf
}

// StreamDecoder decodes one stream chunk by chunk exactly like decoding all of it at once.
type StreamDecoder struct {
	decoder RangeDecoder
	started bool
}

// NewStreamDecoder returns a decoder for one stream.
func NewStreamDecoder() *StreamDecoder { return &StreamDecoder{} }

// Decode returns the text for chunk, holding back an incomplete character.
func (decoder *StreamDecoder) Decode(chunk []byte) string {
	return decoder.start(decoder.decoder.Decode(chunk))
}

// End returns the text the end of the stream completes: a replacement character for an incomplete character. This is
// upstream's decode() without bytes.
func (decoder *StreamDecoder) End() string {
	return decoder.start(decoder.decoder.Flush())
}

func (decoder *StreamDecoder) start(text string) string {
	if decoder.started || text == "" {
		return text
	}
	decoder.started = true
	// U+FEFF encodes only as EF BB BF, so a leading U+FEFF is exactly a byte-order mark at the stream's start.
	return strings.TrimPrefix(text, "\ufeff")
}
