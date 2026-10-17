// Package stackprime grows the calling goroutine's stack in one step.
package stackprime

// bytes is the frame Prime reserves.
const bytes = 16 << 10

// Prime grows the stack of the calling goroutine to hold bytes more than it uses now. A goroutine that starts with the
// runtime's smallest stack and reaches a deep call copies its whole stack at each doubling, and each copy walks every frame
// it has so far; one growth at the top, where the stack holds a frame or two, copies almost nothing.
//
//go:noinline
func Prime() {
	var pad [bytes]byte
	sink(&pad)
}

//go:noinline
func sink(*[bytes]byte) {}
