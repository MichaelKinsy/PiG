package ai

import (
	"bytes"
	"runtime"
	"strconv"
)

// goroutineID identifies the calling goroutine. A stream observation records its consumer's identity because Result cannot otherwise tell the goroutine that runs the consumer callback, which yields it as `await stream.result()` does, from a goroutine that only waits.
func goroutineID() uint64 {
	var buf [32]byte
	header := buf[:runtime.Stack(buf[:], false)]
	header, _ = bytes.CutPrefix(header, []byte("goroutine "))
	digits, _, _ := bytes.Cut(header, []byte(" "))
	id, err := strconv.ParseUint(string(digits), 10, 64)
	if err != nil {
		panic("ai: unrecognized goroutine header " + strconv.Quote(string(header)))
	}
	return id
}
