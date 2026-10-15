package ai

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

// afterFuncCountingContext counts the context.AfterFunc registrations an iterator arms on it that are still armed: context.AfterFunc calls a context's own AfterFunc method.
type afterFuncCountingContext struct {
	context.Context
	armed chan struct{}
	live  atomic.Int64
}

// Value hides the cancel context's private key, so context.AfterFunc registers through AfterFunc below instead of on the cancel context underneath.
func (ctx *afterFuncCountingContext) Value(key any) any {
	if _, private := key.(*int); private {
		return nil
	}
	return ctx.Context.Value(key)
}

func (ctx *afterFuncCountingContext) AfterFunc(f func()) func() bool {
	stop := context.AfterFunc(ctx.Context, f)
	ctx.live.Add(1)
	ctx.armed <- struct{}{}
	var once sync.Once
	return func() bool {
		once.Do(func() { ctx.live.Add(-1) })
		return stop()
	}
}

// Every next() that waits on a cancelable context is cancelable, and a long stream read under one context keeps no registration per event until that context ends: the iteration registers its cancellation once and releases it when it ends.
func TestContinuationIteratorDisarmsDeliveredWaits(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		const events = 64
		executor := &continuationExecutor{}
		base, cancel := context.WithCancel(context.WithValue(context.Background(), continuationExecutorKey{}, executor))
		defer cancel()
		stream := NewAssistantMessageEventStream()
		ctx := &afterFuncCountingContext{armed: make(chan struct{}, events+2)}
		partial := lazyProbeMessage(StopReasonPending)
		// Each event is pushed once the iterator waits for it, so every delivery goes through a cancelable wait.
		go func() {
			for range events {
				waitForStreamWaiters(t, stream, 1)
				_ = stream.Push(TextDeltaEvent{ContentIndex: 0, Delta: "d", Partial: partial})
			}
			waitForStreamWaiters(t, stream, 1)
			_ = stream.Push(DoneEvent{Reason: StopReasonStop, Message: lazyProbeMessage(StopReasonStop)})
			stream.End()
		}()
		delivered := 0
		executor.run(func(turn *continuationTurn) {
			ctx.Context = context.WithValue(base, continuationTurnKey{}, turn)
			for range stream.events(ctx, false) {
				delivered++
			}
		})
		if delivered != events+1 {
			t.Fatalf("delivered %d events, want %d", delivered, events+1)
		}
		if registered := len(ctx.armed); registered != 1 {
			t.Fatalf("the iteration armed %d cancellations on its context, want 1", registered)
		}
		if live := ctx.live.Load(); live != 0 {
			t.Fatalf("%d delivered waits left their cancellation armed on the iterator's context", live)
		}
	})
}
