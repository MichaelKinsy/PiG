package delta

import (
	"sync"
	"unsafe"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Appending to a text costs the appended bytes, as it does for the V8 ropes the upstream appenders (appendText, PendingOps) rely on. A Go string is flat, so concatenation copies the whole text: a stream of n appends then copies n*n/2 bytes. A textTails keeps the buffers behind the strings it concatenated. A string that is a prefix of such a buffer, as every string handed out of it is, is immutable and shares the buffer's bytes. Appending to the string that spans the whole written part of a buffer writes after that part in place, which no string handed out so far can see.

// maxTextTails bounds the buffers a textTails keeps; at the bound it forgets all of them, and each text that goes on growing copies once and is kept again.
const maxTextTails = 64

// textTail is a buffer whose first len(buf) bytes are written and never change.
type textTail struct {
	buf []byte
	// alias is the first byte of the buffer this one replaced when it ran out of room, whose strings find this one: the first bytes are the same.
	alias *byte
}

// textTails concatenates JavaScript strings, sharing the bytes of the strings it made. The zero value is ready; it is not safe for concurrent use.
type textTails struct {
	tails map[*byte]*textTail // by the address of the first byte of each buffer
}

// concat is jsstring.Concat(a, b). The result may share bytes with a, and with strings an earlier concat returned.
func (tails *textTails) concat(a, b string) string {
	if len(a) == 0 || len(b) == 0 || jsstring.JoinsSurrogates(a, b) {
		return jsstring.Concat(a, b)
	}
	head := unsafe.StringData(a) //nolint:gosec // G103: a's address identifies a buffer this textTails made; the pointer is compared and kept only as a map key
	tail := tails.tails[head]
	size := len(a) + len(b)
	if tail != nil && len(a) <= len(tail.buf) {
		first := unsafe.SliceData(tail.buf) //nolint:gosec // G103: the first size bytes of the buffer are written and never change
		// Another change appended the same text to a: the string it made is the same bytes.
		if size <= len(tail.buf) && string(tail.buf[len(a):size]) == b {
			return unsafe.String(first, size) //nolint:gosec // G103: as above
		}
		if len(a) == len(tail.buf) && size <= cap(tail.buf) {
			tail.buf = append(tail.buf, b...)
			return unsafe.String(first, size) //nolint:gosec // G103: as above
		}
	}
	// The first copy of a text is exact. A text that is appended to again leaves room for it to double, so n appends copy O(n) bytes in all.
	full := tail != nil && len(a) == len(tail.buf)
	capacity := size
	if full {
		capacity = 2 * size
	}
	buf := make([]byte, 0, capacity)
	buf = append(append(buf, a...), b...)
	if len(tails.tails) >= maxTextTails {
		clear(tails.tails)
		full = false
	}
	if tails.tails == nil {
		tails.tails = map[*byte]*textTail{}
	}
	grown := &textTail{buf: buf}
	first := unsafe.SliceData(buf) //nolint:gosec // G103: the address of the buffer's first byte is only a map key and the head of the strings made from it
	tails.tails[first] = grown
	if full {
		// The full buffer takes no more writes. Its strings find the grown one, where whoever extends the same text a second time finds the bytes already there.
		if tail.alias != nil {
			delete(tails.tails, tail.alias)
			tail.alias = nil
		}
		tails.tails[head] = grown
		grown.alias = head
	}
	return unsafe.String(first, size) //nolint:gosec // G103: the first size bytes of the buffer are written and never change
}

// reset forgets every buffer.
func (tails *textTails) reset() { tails.tails = nil }

// syncTextTails is a textTails that concurrent changes of one tracker share.
type syncTextTails struct {
	mu    sync.Mutex
	tails textTails
}

func (shared *syncTextTails) concat(a, b string) string {
	shared.mu.Lock()
	defer shared.mu.Unlock()
	return shared.tails.concat(a, b)
}
