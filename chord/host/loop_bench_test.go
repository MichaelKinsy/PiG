package host

import (
	"context"
	"testing"
	"time"
)

// idleGoroutines starts n goroutines parked on a channel for the benchmark's lifetime: internal/entered's goroutine dump
// covers every goroutine, so its cost grows with them.
func idleGoroutines(b *testing.B, n int) {
	b.Helper()
	stop := make(chan struct{})
	for range n {
		go func() { <-stop }()
	}
	b.Cleanup(func() { close(stop) })
}

func benchmarkGoEntered(b *testing.B, fn func(host *HostImpl, release <-chan struct{})) {
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	idleGoroutines(b, 1000)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		release := make(chan struct{})
		entered := host.goEntered(func() { fn(host, release) })
		<-entered
		close(release)
	}
	b.StopTimer()
	host.waitTasks()
}

// fn returns at once.
func BenchmarkGoEnteredReturns(b *testing.B) {
	benchmarkGoEntered(b, func(*HostImpl, <-chan struct{}) {})
}

// fn calls one host wait: it awaits a promise settled after it entered.
func BenchmarkGoEnteredHostWait(b *testing.B) {
	benchmarkGoEntered(b, func(host *HostImpl, release <-chan struct{}) {
		promise, resolve, _ := newPromise[struct{}](&host.loop)
		go func() {
			<-release
			resolve(struct{}{})
		}()
		_, _ = promise.Await(context.Background())
	})
}

// fn sleeps 5 ms: a wait outside the host, found by the goroutine state.
func BenchmarkGoEnteredSleep5ms(b *testing.B) {
	benchmarkGoEntered(b, func(*HostImpl, <-chan struct{}) { time.Sleep(5 * time.Millisecond) })
}
