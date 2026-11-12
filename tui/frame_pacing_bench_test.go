// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package tui

import (
	"sync/atomic"
	"testing"
	"time"
)

// BenchmarkScheduleRender is the scheduler's question and its timer under Pi's constant and under the adaptive policy with every condition met.
func BenchmarkScheduleRender(b *testing.B) {
	for _, policy := range []string{"pi", "adaptive"} {
		b.Run(policy, func(b *testing.B) {
			at := time.Unix(1_000_000, 0)
			ui := NewTuiAltScreenWithOutput(discardFrames{}, 40, 10, TuiAltScreenOptions{})
			ui.now = func() time.Time { return at }
			ui.afterFunc = func(time.Duration, func()) stoppableTimer { return noopTimer{} }
			ui.terminal = &pacingTerminal{Terminal: ui.Terminal(), writerActive: true}
			var gate atomic.Bool
			gate.Store(true)
			if policy == "adaptive" {
				ui.SetFramePacing(&FramePacing{terminalTrusted: true, allowed: &gate})
			}
			ui.lastRenderAt, ui.lastFrameCost = at.Add(-time.Millisecond), time.Millisecond
			b.ReportAllocs()
			for b.Loop() {
				ui.mu.Lock()
				ui.renderTimer, ui.renderRequested = nil, true
				ui.scheduleRenderLocked()
				ui.mu.Unlock()
			}
		})
	}
}

type discardFrames struct{}

func (discardFrames) Write(data []byte) (int, error) { return len(data), nil }
