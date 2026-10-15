package ai

import "strings"

// accumulatedString appends deltas to a growing string without copying the earlier bytes each time. The strings it returns share its buffer; the buffer only ever grows past the bytes already returned, so they stay immutable.
type accumulatedString struct{ buffer strings.Builder }

// append is current+delta. When current is not what the buffer holds, because the owner replaced it, the buffer restarts from current.
func (accumulated *accumulatedString) append(current, delta string) string {
	if accumulated.buffer.String() != current {
		accumulated.buffer.Reset()
		accumulated.buffer.Grow(len(current) + len(delta))
		accumulated.buffer.WriteString(current)
	}
	accumulated.buffer.WriteString(delta)
	return accumulated.buffer.String()
}
