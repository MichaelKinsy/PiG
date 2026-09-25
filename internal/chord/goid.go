package chord

import (
	"bytes"
	"runtime"
	"strconv"
)

// currentGoroutine returns the calling goroutine's ID. Upstream Chord is
// single-threaded, so "reentrant" means "on the current call stack"; the Go
// runtime detects that case by comparing the goroutine that owns a delivery
// or mutation with the caller, so reentrant calls keep upstream's synchronous
// (nested) semantics while calls from other goroutines are serialized.
func currentGoroutine() uint64 {
	var buf [64]byte
	stack := buf[:runtime.Stack(buf[:], false)]
	stack = bytes.TrimPrefix(stack, []byte("goroutine "))
	if end := bytes.IndexByte(stack, ' '); end >= 0 {
		stack = stack[:end]
	}
	id, err := strconv.ParseUint(string(stack), 10, 64)
	if err != nil {
		panic("chord: cannot identify the current goroutine")
	}
	return id
}
