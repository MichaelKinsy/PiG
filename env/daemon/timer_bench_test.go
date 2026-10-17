package daemon

import (
	"testing"
	"time"
)

// One pass of a wait loop whose event is ready before the timeout: the daemon's output wait, exec kill retry and terminal idle grace loops all take this path once per pass. The first benchmark is what the loops did before waitTimer (a time.After per pass), the second is what they do now.
func BenchmarkWaitPassTimeAfter(b *testing.B) {
	ready := make(chan struct{}, 1)
	b.ReportAllocs()
	for b.Loop() {
		ready <- struct{}{}
		select {
		case <-ready:
		case <-time.After(time.Second):
			b.Fatal("timed out")
		}
	}
}

func BenchmarkWaitPassWaitTimer(b *testing.B) {
	ready := make(chan struct{}, 1)
	var timer waitTimer
	defer timer.stop()
	b.ReportAllocs()
	for b.Loop() {
		ready <- struct{}{}
		select {
		case <-ready:
		case <-timer.arm(time.Second):
			b.Fatal("timed out")
		}
	}
}
