package codingagent

import (
	"testing"
	"time"
)

// BenchmarkSuspendLifecycle measures the owned timer and callback lifecycle, not terminal repaint or Session history.
func BenchmarkSuspendLifecycle(b *testing.B) {
	b.ReportAllocs()
	mode := &InteractiveMode{}
	for b.Loop() {
		var onContinue func() error
		ops := suspendOperations{
			keepAlive:       func(interval time.Duration) func() { return time.NewTicker(interval).Stop },
			ignoreInterrupt: func() func() { mode.suspended.Store(true); return func() { mode.suspended.Store(false) } },
			continued:       func(continued func() error, _ func()) func() { onContinue = continued; return func() {} },
			stop:            func() {}, start: func() error { return nil }, requestRender: func() {}, kill: func(int) error { return nil },
		}
		if err := suspendTerminal(b.Context(), "unix", nil, ops); err != nil {
			b.Fatal(err)
		}
		if err := onContinue(); err != nil {
			b.Fatal(err)
		}
	}
}
