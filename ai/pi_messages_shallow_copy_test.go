package ai

import (
	"context"
	"testing"
	"time"
)

// pi-messages.ts:195-205 (`done`): `Object.assign(partial, { stopReason, usage: event.usage, responseId })` assigns a new usage object to the
// partial. agent-loop.ts:408-416 emits `{ ...partialMessage }` at `start`, so that copy keeps the usage object the partial had then, while
// its content array stays shared with the partial. A consumer that reads the copy after `done` sees zero usage and the tool call.
func TestPiMessagesDoneReplacesUsageRetainedByEarlierShallowCopy(t *testing.T) {
	oracle := loadPiMessagesOracleFile(t)
	for _, delivery := range []string{"buffered", "pending"} {
		t.Run(delivery, func(t *testing.T) {
			baseURL := servePiMessagesFixture(t, oracle.Bodies["tool"], delivery)
			provider := NewPiMessagesProvider(PiMessagesConfig{BaseURL: baseURL, APIKey: "k", Model: "strict", ProviderID: "p"})
			defer func() { _ = provider.Close() }()
			ctx, stop := context.WithTimeout(t.Context(), 20*time.Second)
			defer stop()
			_, executor := withContinuationExecutor(ctx)
			ctx = context.WithValue(ctx, continuationExecutorKey{}, executor)
			var copied *AssistantMessage
			var final *AssistantMessage
			executor.run(func(turn *continuationTurn) {
				stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("probe"), Timestamp: 1}}}), StreamOptions{})
				if err != nil {
					t.Error(err)
					return
				}
				for event := range stream.Events(context.WithValue(ctx, continuationTurnKey{}, turn)) {
					if start, ok := event.(StartEvent); ok {
						copied = start.Partial.ShallowCopy()
					}
				}
				final = stream.Result()
			})
			if copied == nil || final == nil || final.Usage.Input != 10 {
				t.Fatalf("copied=%v final=%+v", copied, final)
			}
			if got := copied.ObserveUsage(); got != (Usage{}) {
				t.Fatalf("shallow copy taken at start observes usage %+v after done; Pi keeps the usage object the partial had at the copy", got)
			}
			if got := copied.Observe().Usage; got != (Usage{}) {
				t.Fatalf("Observe of the start copy reports usage %+v after done, want zero", got)
			}
			if got := copied.Observe().StopReason; got != StopReasonPending {
				t.Fatalf("start copy stopReason = %q, want pending", got)
			}
		})
	}
}
