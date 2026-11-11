package delta

import (
	"sync"
	"unsafe"
	"weak"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Appending to a text costs the appended bytes, as it does for the V8 ropes the upstream appenders (appendText, PendingOps) rely on. A Go string is flat, so concatenation copies the whole text: a stream of n appends then copies n*n/2 bytes. A textTails remembers the buffers behind the strings it concatenated, without holding them: only the strings that point into a buffer keep it alive, so a text nobody holds any more costs nothing. A string that is a prefix of such a buffer, as every string handed out of it is, is immutable and shares the buffer's bytes. Appending to the string that spans the whole written part of a buffer writes after that part in place, which no string handed out so far can see.

// maxTextTails bounds the buffers a textTails remembers. At the bound it drops the ones that were collected and, if none was, all of them; each text that goes on growing copies once and is remembered again.
const maxTextTails = 64

// textTail is a buffer whose first length bytes are written and never change; capacity is its allocated size.
type textTail struct {
	first    weak.Pointer[byte]
	length   int
	capacity int
	// alias is the key of the entry that sends the strings of the buffer this one replaced to it, or 0.
	alias uintptr
}

// textTailRef is the entry for strings that start at head: the buffer of tail itself, or one it replaced when it ran out of room, whose first bytes are the same.
type textTailRef struct {
	head weak.Pointer[byte]
	tail *textTail
}

// textTails concatenates JavaScript strings, sharing the bytes of the strings it made. The zero value is ready; it is not safe for concurrent use.
type textTails struct {
	tails map[uintptr]textTailRef // by the address of the first byte of the buffer the strings start in
}

// find returns the entry of the strings that start at head and the first byte of its buffer, or nil when none is remembered or the buffer was collected.
func (tails *textTails) find(head *byte) (*textTail, *byte) {
	key := uintptr(unsafe.Pointer(head)) //nolint:gosec // G103: the address is only a map key; it is never converted back to a pointer
	ref, remembered := tails.tails[key]
	if !remembered {
		return nil, nil
	}
	// A buffer collected since, whose address a later string reuses, no longer matches its weak pointer.
	if ref.head.Value() == head {
		if first := ref.tail.first.Value(); first != nil {
			return ref.tail, first
		}
	}
	delete(tails.tails, key)
	return nil, nil
}

// concat is jsstring.Concat(a, b). The result may share bytes with a, and with strings an earlier concat returned.
func (tails *textTails) concat(a, b string) string {
	if len(a) == 0 || len(b) == 0 || jsstring.JoinsSurrogates(a, b) {
		return jsstring.Concat(a, b)
	}
	head := unsafe.StringData(a) //nolint:gosec // G103: a's address finds the buffer this textTails made it in; the pointer is compared and its address kept only as a map key
	tail, first := tails.find(head)
	size := len(a) + len(b)
	if tail != nil && len(a) <= tail.length {
		buf := unsafe.Slice(first, tail.capacity) //nolint:gosec // G103: the buffer is capacity bytes long and alive, as first is a strong pointer to it
		// Another change appended the same text to a: the string it made is the same bytes.
		if size <= tail.length && string(buf[len(a):size]) == b {
			return unsafe.String(first, size) //nolint:gosec // G103: the first size bytes of the buffer are written and never change
		}
		if len(a) == tail.length && size <= tail.capacity {
			copy(buf[len(a):size], b)
			tail.length = size
			return unsafe.String(first, size) //nolint:gosec // G103: as above
		}
	}
	// The first copy of a text is exact. A text that is appended to again leaves room for it to double, so n appends copy O(n) bytes in all.
	full := tail != nil && len(a) == tail.length
	capacity := size
	if full {
		capacity = 2 * size
	}
	buf := append(append(make([]byte, 0, capacity), a...), b...)
	if len(tails.tails) >= maxTextTails {
		tails.sweep()
		full = false
	}
	if tails.tails == nil {
		tails.tails = map[uintptr]textTailRef{}
	}
	start := unsafe.SliceData(buf) //nolint:gosec // G103: the address of the buffer's first byte is the head of the strings made from it
	grown := &textTail{first: weak.Make(start), length: size, capacity: capacity}
	tails.tails[uintptr(unsafe.Pointer(start))] = textTailRef{head: grown.first, tail: grown} //nolint:gosec // G103: the address is only a map key
	if full {
		// The full buffer takes no more writes. Its strings find the grown one, where whoever extends the same text a second time finds the bytes already there.
		if tail.alias != 0 {
			delete(tails.tails, tail.alias)
			tail.alias = 0
		}
		key := uintptr(unsafe.Pointer(head)) //nolint:gosec // G103: the address is only a map key; it is never converted back to a pointer
		tails.tails[key] = textTailRef{head: tail.first, tail: grown}
		grown.alias = key
	}
	return unsafe.String(start, size) //nolint:gosec // G103: the first size bytes of the buffer are written and never change
}

// sweep forgets the buffers that were collected, and all of them when every one is still alive.
func (tails *textTails) sweep() {
	for key, ref := range tails.tails {
		if ref.head.Value() == nil || ref.tail.first.Value() == nil {
			delete(tails.tails, key)
		}
	}
	if len(tails.tails) >= maxTextTails {
		clear(tails.tails)
	}
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
