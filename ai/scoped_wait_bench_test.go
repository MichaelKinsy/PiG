package ai

import (
	"context"
	"testing"
)

// The RPC and JSON print listeners wait once per Agent event, so a 10k-delta response pays each wait 10k times.
func benchmarkScopedWait(b *testing.B, wait func(*StreamObservation)) {
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
	b.ResetTimer()
	executor.run(func(turn *continuationTurn) {
		ctx := context.WithValue(ctx, continuationTurnKey{}, turn)
		for observation := range stream.ObserveEvents(ctx) {
			wait(observation)
		}
	})
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
