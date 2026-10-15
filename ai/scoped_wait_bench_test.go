//go:build unix

package ai

import (
	"context"
	"syscall"
	"testing"
	"time"
)

// The RPC and JSON print listeners wait once per Agent event, so a 10k-delta response pays each wait 10k times.
//
// waiting is the path a live stream takes: the producer pushes from another goroutine, one event after the consumer finished the previous one, so every next() finds the queue empty and parks (the second promise, the eventWaiter, its delivery channel and AfterFunc, and the cross-goroutine wake). The producer's own pacing (one buffered channel hand-off per event) is part of the measured cost and stays constant across fixes.
// queued is the fast path of a consumer slower than its producer: every event is pushed before the consumer starts.
//
// Both report cpu-ns/op, the process CPU from getrusage: wall time hides the CPU a wake-up burns on another thread.
func benchmarkScopedWait(b *testing.B, wait func(*StreamObservation)) {
	b.Run("waiting", func(b *testing.B) {
		b.ReportAllocs()
		executor := &continuationExecutor{}
		ctx := context.WithValue(b.Context(), continuationExecutorKey{}, executor)
		stream := NewAssistantMessageEventStream()
		partial := lazyProbeMessage(StopReasonPending)
		consumed := make(chan struct{}, 1)
		n := b.N
		cpu := startProcessCPU(b)
		b.ResetTimer()
		go func() {
			for range n {
				_ = stream.Push(TextDeltaEvent{ContentIndex: 0, Delta: "d", Partial: partial})
				<-consumed
			}
			_ = stream.Push(DoneEvent{Reason: StopReasonStop, Message: lazyProbeMessage(StopReasonStop)})
			stream.End()
		}()
		executor.run(func(turn *continuationTurn) {
			ctx := context.WithValue(ctx, continuationTurnKey{}, turn)
			for observation := range stream.ObserveEvents(ctx) {
				wait(observation)
				consumed <- struct{}{}
			}
		})
		b.StopTimer()
		cpu.report(b, n)
	})
	b.Run("queued", func(b *testing.B) {
		b.ReportAllocs()
		executor := &continuationExecutor{}
		ctx := context.WithValue(b.Context(), continuationExecutorKey{}, executor)
		stream := NewAssistantMessageEventStream()
		partial := lazyProbeMessage(StopReasonPending)
		for range b.N {
			_ = stream.Push(TextDeltaEvent{ContentIndex: 0, Delta: "d", Partial: partial})
		}
		_ = stream.Push(DoneEvent{Reason: StopReasonStop, Message: lazyProbeMessage(StopReasonStop)})
		stream.End()
		cpu := startProcessCPU(b)
		b.ResetTimer()
		executor.run(func(turn *continuationTurn) {
			ctx := context.WithValue(ctx, continuationTurnKey{}, turn)
			for observation := range stream.ObserveEvents(ctx) {
				wait(observation)
			}
		})
		b.StopTimer()
		cpu.report(b, b.N)
	})
}

// processCPUStart is the process CPU (user plus system, getrusage RUSAGE_SELF) when a measurement began.
type processCPUStart time.Duration

func readProcessCPU(b *testing.B) time.Duration {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		b.Fatal(err)
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}

func startProcessCPU(b *testing.B) processCPUStart { return processCPUStart(readProcessCPU(b)) }

func (start processCPUStart) report(b *testing.B, n int) {
	b.ReportMetric(float64((readProcessCPU(b)-time.Duration(start)).Nanoseconds())/float64(n), "cpu-ns/op")
}

func BenchmarkObservationYield(b *testing.B) {
	benchmarkScopedWait(b, func(observation *StreamObservation) { observation.Yield() })
}

func BenchmarkObservationAwaitTick(b *testing.B) {
	benchmarkScopedWait(b, func(observation *StreamObservation) { _ = observation.AwaitTick(func() error { return nil }) })
}

func BenchmarkObservationAwaitExternal(b *testing.B) {
	benchmarkScopedWait(b, func(observation *StreamObservation) { _ = observation.AwaitExternal(func() error { return nil }) })
}
