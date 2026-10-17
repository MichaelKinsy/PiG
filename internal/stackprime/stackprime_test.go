package stackprime

import (
	"testing"
	"unsafe"
)

var frameSum uintptr

// frame is one recursion level of known size, so a run of them uses a known part of the stack.
//
//go:noinline
func frame(depth int) uintptr {
	var pad [1 << 10]byte
	pad[depth%len(pad)] = byte(depth)
	if depth == 0 {
		return uintptr(pad[0])
	}
	return frame(depth-1) + uintptr(pad[depth%len(pad)])
}

// addressOfLocal returns where a local of the calling goroutine's stack lives. The runtime moves a stack when it grows, so
// the address changes when the stack was copied.
//
//go:noinline
func addressOfLocal() uintptr {
	var local int
	//nolint:gosec // G103: the address is compared and never converted back to a pointer; the test reads where the runtime put a local, which has no CLI input.
	return uintptr(unsafe.Pointer(&local))
}

// stackMoved reports whether the stack of a new goroutine moved while it ran depth one-KiB frames, after prime.
func stackMoved(prime func(), depth int) bool {
	moved := make(chan bool)
	go func() {
		prime()
		before := addressOfLocal()
		frameSum = frame(depth)
		after := addressOfLocal()
		moved <- before != after
	}()
	return <-moved
}

// A new goroutine starts with the runtime's smallest stack and copies it at each doubling. After Prime, a goroutine that
// reaches a call 12 KiB deep reaches it on the stack it has.
func TestPrimeSpansADeepCallWithoutAStackCopy(t *testing.T) {
	const depth = 12
	if !stackMoved(func() {}, depth) {
		t.Skip("a goroutine without Prime reaches the call without a stack copy, so the stack start size is already large enough to prove nothing")
	}
	if stackMoved(Prime, depth) {
		t.Fatalf("the stack of a goroutine that called Prime moved while it ran %d KiB of frames", depth)
	}
}
