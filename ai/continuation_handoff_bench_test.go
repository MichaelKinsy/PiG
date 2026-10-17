//go:build unix

package ai

import (
	"iter"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// handOffWork is the producer's own work between two hand-offs, about 2 us, roughly what a provider spends on one streamed record; a hand-off to a parked goroutine costs its CPU on other threads (wakep, spinning Ms), which wall time hides.
const handOffWork = 1000

var handOffSink uint64

func handOffSpin() {
	x := handOffSink
	for i := range handOffWork {
		x = x*6364136223846793005 + uint64(i)
	}
	handOffSink = x
}

// processCPUTime is the user plus system CPU time of the whole process (getrusage RUSAGE_SELF).
func processCPUTime(b *testing.B) time.Duration {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		b.Fatal(err)
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}

// reportCPU reports the process CPU per operation beside the wall time.
func reportCPU(b *testing.B, run func(n int)) {
	b.ReportAllocs()
	start := processCPUTime(b)
	b.ResetTimer()
	run(b.N)
	b.StopTimer()
	b.ReportMetric(float64((processCPUTime(b)-start).Nanoseconds())/float64(b.N), "cpu-ns/op")
}

// BenchmarkTurnHandOff is one hand-off of execution between two turns and back, by the mechanism the executor used before (a cap-1 chan bool permit per turn, as continuationTurn.permit was) and the one it uses now (an iter.Pull coroutine switch).
func BenchmarkTurnHandOff(b *testing.B) {
	b.Run("baton", func(b *testing.B) {
		reportCPU(b, func(n int) {
			toConsumer, toProducer := make(chan bool, 1), make(chan bool, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				for range n {
					<-toConsumer
					toProducer <- true
				}
			}()
			for range n {
				handOffSpin()
				toConsumer <- true
				<-toProducer
			}
			<-done
		})
	})
	b.Run("coroutine", func(b *testing.B) {
		reportCPU(b, func(n int) {
			next, stop := iter.Pull(func(yield func(struct{}) bool) {
				for range n {
					handOffSpin()
					if !yield(struct{}{}) {
						return
					}
				}
			})
			defer stop()
			for {
				if _, ok := next(); !ok {
					return
				}
			}
		})
	})
}

// BenchmarkContinuationHandOff streams events through the executor from a provider turn that awaits once after each push to a consumer iterating on another goroutine; an op is one event.
func BenchmarkContinuationHandOff(b *testing.B) {
	partial := testAssistant(StopReasonPending)
	delta := TextDeltaEvent{Delta: "x", Partial: partial}
	reportCPU(b, func(n int) {
		executor := &continuationExecutor{}
		stream := NewAssistantMessageEventStream()
		stream.executor = executor
		producer := executor.newTurn()
		go producer.run(func(turn *continuationTurn) {
			if err := stream.Push(StartEvent{Partial: partial}); err != nil {
				b.Error(err)
			}
			for range n {
				handOffSpin()
				if err := stream.Push(delta); err != nil {
					b.Error(err)
					return
				}
				suspendContinuation(turn)
			}
			if err := stream.Push(DoneEvent{Reason: StopReasonStop, Message: testAssistant(StopReasonStop)}); err != nil {
				b.Error(err)
			}
		})
		for range stream.events(b.Context(), false) {
		}
	})
	runtime.KeepAlive(partial)
}
