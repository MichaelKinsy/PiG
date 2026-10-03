package node

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// utf8StreamDecoder decodes a byte stream like Node's TextDecoder in streaming
// mode: a character split across two chunks is held back until its remaining
// bytes arrive, each maximal invalid subpart becomes one U+FFFD, and a byte
// order mark at the start of the stream is dropped.
type utf8StreamDecoder struct {
	pending []byte
	// started is set once the decoder has produced text; only the start of a
	// stream may drop a byte order mark.
	started bool
}

// decode returns the text of the complete characters in pending plus chunk.
func (decoder *utf8StreamDecoder) decode(chunk []byte) string {
	data := append(slices.Clone(decoder.pending), chunk...)
	held := incompleteTail(data)
	decoder.pending = append([]byte(nil), data[len(data)-held:]...)
	text := jsstring.FromUTF8(data[:len(data)-held])
	if !decoder.started && text != "" {
		decoder.started = true
		text = strings.TrimPrefix(text, "\ufeff")
	}
	return text
}

// flush returns the replacement character for a character the stream ended in
// the middle of, or "".
func (decoder *utf8StreamDecoder) flush() string {
	if len(decoder.pending) == 0 {
		return ""
	}
	decoder.pending = nil
	return string(utf8.RuneError)
}

// incompleteTail is the length of a trailing, still-valid prefix of a
// multi-byte character, or 0.
func incompleteTail(data []byte) int {
	for tail := 1; tail <= min(3, len(data)); tail++ {
		if suffix := data[len(data)-tail:]; utf8.RuneStart(suffix[0]) && !utf8.FullRune(suffix) {
			return tail
		}
	}
	return 0
}
