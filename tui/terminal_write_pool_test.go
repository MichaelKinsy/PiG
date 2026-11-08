package tui

import (
	"strings"
	"testing"
)

// poolProbeWriter has no WriteString, so writeString copies into a pooled buffer for it.
type poolProbeWriter struct{}

func (poolProbeWriter) Write(p []byte) (int, error) { return len(p), nil }

// One large write does not leave its byte copy in terminalWriteBuffers (golang/go#23199): the copy grows to the write's size, and a pool that kept it would hold that
// memory until the garbage collector emptied the pool. Every Get after a large write must see a buffer within the cap (the race detector makes a Put drop at random, so a
// single pass could miss an uncapped Put).
func TestWriteStringKeepsNoLargeCopyInThePool(t *testing.T) {
	large := strings.Repeat("x", 4*maxPooledTerminalWrite)
	for range 64 {
		if n, err := writeString(poolProbeWriter{}, large); err != nil || n != len(large) {
			t.Fatalf("writeString = %d, %v, want %d, nil", n, err, len(large))
		}
		held := terminalWriteBuffers.Get().(*[]byte)
		if got := cap(*held); got > maxPooledTerminalWrite {
			t.Fatalf("a pooled copy has capacity %d, over the %d cap", got, maxPooledTerminalWrite)
		}
		terminalWriteBuffers.Put(held)
	}
}
