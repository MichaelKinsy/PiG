package ai

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// accumulatedString appends deltas to a growing string without copying the earlier bytes each time. The strings it returns share its buffer; the buffer only ever grows past the bytes already returned, so they stay immutable.
type accumulatedString struct{ buffer strings.Builder }

// append is current+delta as JavaScript's `+=` joins them. When current is not what the buffer holds, because the owner replaced it, the buffer
// restarts from current. A high surrogate that ends current and a low surrogate that starts delta are one character: a server that escapes
// everything outside ASCII can split a character over two deltas, and the joined text holds it in its four bytes, not as two halves.
func (accumulated *accumulatedString) append(current, delta string) string {
	if accumulated.buffer.String() != current {
		accumulated.buffer.Reset()
		accumulated.buffer.Grow(len(current) + len(delta))
		accumulated.buffer.WriteString(current)
	}
	if jsstring.JoinsSurrogates(current, delta) {
		joined := jsstring.Concat(current, delta)
		accumulated.buffer.Reset()
		accumulated.buffer.WriteString(joined)
		return accumulated.buffer.String()
	}
	accumulated.buffer.WriteString(delta)
	return accumulated.buffer.String()
}
