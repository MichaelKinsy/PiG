// Package goroutineid identifies the calling goroutine for synchronous reentry checks. Upstream JavaScript is
// single-threaded, so "reentrant" means "on the current call stack"; Go code compares the goroutine that owns a running
// callback with the caller's, keeping upstream's nested synchronous semantics for reentrant calls while calls from
// other goroutines serialize. The ID is never a wire, persisted or application identity.
package goroutineid

import (
	"bytes"
	"runtime"
	"strconv"
)

// Current returns the calling goroutine's runtime ID, which is never zero.
//
// Where the runtime's goroutine record can be read (fast_gc.go), a call loads the ID from it once the offset is proved. The first
// calls prove where the ID lives in the record on the goroutines that make them, and return the stack header's ID meanwhile. The
// proof stands only if exactly one word equals the stack header's ID on 17 distinct goroutines (the runtime never reuses an ID): a
// runtime that moved, renamed or stopped storing the field leaves no such word, or none that stays equal across goroutines, and
// Current then formats the header, as it does on every other platform. A changed layout therefore costs speed and never returns
// another goroutine's or a wrong ID. The header's cost grows with the depth of the stack.
func Current() uint64 { return current() }

// stackID parses the ID from the header line runtime.Stack writes for the calling goroutine.
func stackID() uint64 {
	var buf [64]byte
	header := buf[:runtime.Stack(buf[:], false)]
	header, _ = bytes.CutPrefix(header, []byte("goroutine "))
	digits, _, _ := bytes.Cut(header, []byte(" "))
	id, err := strconv.ParseUint(string(digits), 10, 64)
	if err != nil {
		panic("goroutineid: unrecognized goroutine header " + strconv.Quote(string(header)))
	}
	return id
}
