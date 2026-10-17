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
func Current() uint64 {
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
