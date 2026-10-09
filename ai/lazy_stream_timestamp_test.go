package ai

import (
	"context"
	"errors"
	"testing"
	"time"
)

// packages/ai/src/api/lazy.ts lazyStream (1.1.0): the error message of a failed setup carries the request start as its
// timestamp, not the failure time (createSetupErrorMessage(model, error, startedAt)). The setup lets the wall clock pass
// the millisecond it began in before it fails, so a message stamped at failure time cannot satisfy the bound.
func TestLazyStreamSetupErrorTimestampIsTheRequestStart(t *testing.T) {
	model := &Model{ID: "m", ProviderMeta: ProviderMetadata{API: APIOpenAIResponses, ProviderID: "p"}}
	for name, start := range map[string]func(context.Context, *Model, func(context.Context) (*AssistantMessageEventStream, error)) *AssistantMessageEventStream{"async setup": LazyStream, "synchronous setup": LazyStreamSync} {
		t.Run(name, func(t *testing.T) {
			var began, failed int64
			stream := start(t.Context(), model, func(context.Context) (*AssistantMessageEventStream, error) {
				began = time.Now().UnixMilli()
				for time.Now().UnixMilli() <= began {
				}
				failed = time.Now().UnixMilli()
				return nil, errors.New("setup failed")
			})
			result := stream.Result()
			if result.StopReason != StopReasonError || result.ErrorMessage != "setup failed" {
				t.Fatalf("result = %+v", result)
			}
			if result.Timestamp > began || result.Timestamp >= failed {
				t.Fatalf("timestamp = %d, want the request start (at most %d, when setup began), not the failure time (%d)", result.Timestamp, began, failed)
			}
		})
	}
}
