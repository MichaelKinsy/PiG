package codingagent

import (
	"context"
	"os"
	"testing"
	"time"
)

// BenchmarkKeystrokeToPaint times one key from the terminal pipe to its paint through the real reader and owner loop:
// typed keys alternate with Backspace so the editor stays one character long.
func BenchmarkKeystrokeToPaint(b *testing.B) {
	m := statusBorderMode(b, false)
	m.isIdle = true
	m.tuiInst.Add(m.editor)
	m.tuiInst.SetFocus(m.editor)
	probe := &paintProbe{frames: make(chan struct{}, 1)}
	m.tuiInst.Add(probe)
	m.installRenderDispatcher()
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = readEnd.Close() }()
	defer func() { _ = writeEnd.Close() }()
	ctx, cancel := context.WithCancel(b.Context())
	m.backgroundCtx = ctx
	until, done := make(chan struct{}), make(chan error, 1)
	go func() { done <- m.inputLoopUntil(ctx, readEnd, until) }()
	defer func() {
		close(until)
		if err := <-done; err != nil {
			b.Errorf("input loop: %v", err)
		}
		cancel()
		if err := m.stopTerminalInput(); err != nil {
			b.Errorf("stop input: %v", err)
		}
	}()
	keys := [2]string{"a", "\x7f"}
	typeKey := func(key string) {
		if _, err := writeEnd.WriteString(key); err != nil {
			b.Fatal(err)
		}
		select {
		case <-probe.frames:
		case <-time.After(5 * time.Second):
			b.Fatal("the keystroke never painted")
		}
	}
	// A run of -benchtime=5x has no warm-up run before it, so the first keystroke's one-time work (parsing each key identifier it is compared
	// with, compiling the regexps of a key sequence, filling pools) fell in the measured iterations, divided by five: 858 allocs/op at 1x, 197 at 5x,
	// 74 at 20x and 41 at 100x. Both keys are typed once before the timer starts, so allocs/op is what a keystroke costs, whichever benchmark ran first.
	for _, key := range keys {
		typeKey(key)
	}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		typeKey(keys[i&1])
		i++
	}
}
