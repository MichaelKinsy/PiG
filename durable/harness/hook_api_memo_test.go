package harness

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// packages/durable/src/harness/types.ts:604-608 HookApi.memo(name): a hook reads a durable memo of its task; it is absent until memoCandidate stores one, and the first
// stored candidate wins over later ones.
// Pi: packages/durable/src/harness/types.ts:608 (memo).
func TestHookApiMemoReadsDurableMemoOfTheHookTask(t *testing.T) {
	chat := openCompaction(t)
	var seenBefore, seenAfter, winner durable.JsonValue
	var presentBefore, presentAfter bool
	addHooks(t, chat.setup.Registry, CompactionTask, &CompactionHooks{BeforeCompact: func(ctx context.Context, _ CompactionRequest, api HookApi) (*CompactionDecision, error) {
		var err error
		if seenBefore, presentBefore, err = api.Memo(ctx, "pick"); err != nil {
			return nil, err
		}
		if _, err = api.MemoCandidate(ctx, "pick", "first"); err != nil {
			return nil, err
		}
		if winner, err = api.MemoCandidate(ctx, "pick", "second"); err != nil {
			return nil, err
		}
		if seenAfter, presentAfter, err = api.Memo(ctx, "pick"); err != nil {
			return nil, err
		}
		return &CompactionDecision{Summary: new("HOOK")}, nil
	}})
	chat.history(t)
	if outcome := chat.outcome(t, chat.compact(t, nil)); outcome.Status != durable.OutcomeCompleted {
		t.Fatalf("outcome = %s", outcome.Status)
	}
	if presentBefore || seenBefore != nil {
		t.Fatalf("memo before any candidate = %v/%v", seenBefore, presentBefore)
	}
	if winner != "first" || seenAfter != "first" || !presentAfter {
		t.Fatalf("memo after candidates = %v (winner %v, present %v), want the first candidate", seenAfter, winner, presentAfter)
	}
	chat.close(t)
}
