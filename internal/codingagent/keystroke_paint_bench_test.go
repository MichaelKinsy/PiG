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
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		if _, err := writeEnd.WriteString(keys[i&1]); err != nil {
			b.Fatal(err)
		}
		i++
		select {
		case <-probe.frames:
		case <-time.After(5 * time.Second):
			b.Fatal("the keystroke never painted")
		}
	}
}
