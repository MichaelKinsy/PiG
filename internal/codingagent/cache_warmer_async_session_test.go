package codingagent

// pi: packages/coding-agent/src/core/cache-warmer.ts

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// An extension decision that throws falls back to Pi's own decision (cache-warmer.ts:236-244): the refresh is still
// sent, its usage is recorded, and the next refresh is scheduled.
func TestCacheWarmingExtensionDecisionFailureFallsBackToPisDecisionAndKeepsWarming(t *testing.T) {
	models := newCacheWarmingModels(t)
	synctest.Test(t, func(t *testing.T) {
		f := newFakeWarmRuntime(t, withDecide(func(CacheWarmingDecisionEvent) CacheWarmingAction {
			panic("extension decision failed")
		}))
		f.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		advance(270 * time.Second)
		record := f.snapshot()
		if len(record.calls) != 1 || len(record.appended) != 1 {
			t.Fatalf("after a failed decision: calls %d appended %d, want 1 and 1", len(record.calls), len(record.appended))
		}
		if record.appended[0].Note != "" {
			t.Fatalf("usage note = %q, want no extension override", record.appended[0].Note)
		}
		advance(300 * time.Second)
		if f.callCount() != 2 {
			t.Fatalf("calls after the next interval = %d, want 2 (the run is rescheduled)", f.callCount())
		}
	})
}

// A refresh request that fails (stream function error, panic, or a panic while building the result) records no usage
// and does not end the run: the next refresh is still scheduled (cache-warmer.ts:260-300 catch).
func TestCacheWarmingRequestFailuresRecordNoUsageAndStillReschedule(t *testing.T) {
	models := newCacheWarmingModels(t)
	cases := []struct {
		name string
		fail func(call int) (*ai.AssistantMessageEventStream, error)
	}{
		{"stream function error", func(int) (*ai.AssistantMessageEventStream, error) { return nil, errors.New("request failed") }},
		{"stream function panic", func(int) (*ai.AssistantMessageEventStream, error) { panic("request panicked") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newFakeWarmRuntime(t)
				calls := 0
				var stream agent.StreamFn = func(ctx context.Context, model *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
					calls++
					return tc.fail(calls)
				}
				f.warmer.Close()
				f.warmer.Wait()
				f.warmer = NewCacheWarmer(stream, f, func() CacheWarmingMode { return "idle" }, func(_ context.Context, event CacheWarmingDecisionEvent) (CacheWarmingAction, error) {
					return event.Action, nil
				})
				f.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
				advance(270 * time.Second)
				if calls != 1 || len(f.snapshot().appended) != 0 {
					t.Fatalf("after the failed request: calls %d appended %d, want 1 and 0", calls, len(f.snapshot().appended))
				}
				advance(300 * time.Second)
				if calls != 2 {
					t.Fatalf("calls after the next interval = %d, want 2 (a failed request still reschedules)", calls)
				}
				if len(f.snapshot().appended) != 0 {
					t.Fatalf("a failed request recorded usage: %+v", f.snapshot().appended)
				}
			})
		})
	}
}

// A panic while the result is produced is caught like a request failure.
func TestCacheWarmingResultPanicRecordsNoUsageAndStillReschedules(t *testing.T) {
	models := newCacheWarmingModels(t)
	synctest.Test(t, func(t *testing.T) {
		f := newFakeWarmRuntime(t, withResult(func(*ai.Model) *ai.AssistantMessageEventStream { panic("result panicked") }))
		f.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		advance(270 * time.Second)
		advance(300 * time.Second)
		if f.callCount() != 2 || len(f.snapshot().appended) != 0 {
			t.Fatalf("calls %d appended %d, want 2 and 0", f.callCount(), len(f.snapshot().appended))
		}
	})
}
