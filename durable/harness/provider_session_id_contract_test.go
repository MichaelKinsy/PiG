package harness

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

type providerContextKey struct{}

// providerRuntimeStub is the part of a task runtime that ensureProviderSessionId reads, with its calls recorded in order.
type providerRuntimeStub struct {
	durable.TaskRuntime[any, any, any, any]
	stored      durable.JsonObject
	snapshotErr error
	commitErr   error
	signal      context.Context
	calls       []string
	contexts    []context.Context
}

func (runtime *providerRuntimeStub) ConversationId() durable.ConversationId { return 7 }
func (runtime *providerRuntimeStub) Signal() context.Context                { return runtime.signal }

func (runtime *providerRuntimeStub) SnapshotErased(ctx context.Context, _ durable.AnyDocToken, _ ...any) (durable.JsonObject, error) {
	runtime.calls, runtime.contexts = append(runtime.calls, "snapshot"), append(runtime.contexts, ctx)
	return runtime.stored, runtime.snapshotErr
}

// Commit never runs the change: the migration commit that would create the identity does not happen.
func (runtime *providerRuntimeStub) Commit(ctx context.Context, _ func(durable.Tx, durable.RunningTask[any, any, any]) (*durable.NextTaskState[any, any], error), _ ...durable.CommitOptions) error {
	runtime.calls, runtime.contexts = append(runtime.calls, "commit"), append(runtime.contexts, ctx)
	return runtime.commitErr
}

func newProviderRuntimeStub(sessions *providerSessionIds) *providerRuntimeStub {
	return &providerRuntimeStub{signal: withProviderSessions(context.Background(), sessions)}
}

// provider.ts ensureProviderSessionId reads the persisted identity with one awaited snapshot and writes only when it is absent, in one awaited commit; both reads
// and the write use the caller's signal; every failure is the failure of the call and records nothing; a commit that leaves no identity is the error
// `Conversation <id> has no provider session ID`.
func TestEnsureProviderSessionIdAwaitsTheSnapshotThenTheCommitAndFailsWithTheirFailures(t *testing.T) {
	ctx := context.WithValue(t.Context(), providerContextKey{}, "caller")

	t.Run("a stored identity is returned without a commit and recorded", func(t *testing.T) {
		sessions := newProviderSessionIds()
		runtime := newProviderRuntimeStub(sessions)
		runtime.stored = delta.JsonObjectOf("sessionId", "stored-id")
		id, err := ensureProviderSessionId[any, any, any, any](ctx, runtime)
		if err != nil || id != "stored-id" || !slices.Equal(runtime.calls, []string{"snapshot"}) {
			t.Fatalf("id = %q, err = %v, calls = %v", id, err, runtime.calls)
		}
		if got := sessions.drain(); !slices.Equal(got, []string{"stored-id"}) {
			t.Fatalf("recorded %v, want the stored identity", got)
		}
		if runtime.contexts[0].Value(providerContextKey{}) != "caller" {
			t.Fatal("the snapshot did not receive the caller's context")
		}
	})
	t.Run("an absent identity is committed after the snapshot, with the caller's context", func(t *testing.T) {
		sessions := newProviderSessionIds()
		runtime := newProviderRuntimeStub(sessions)
		_, err := ensureProviderSessionId[any, any, any, any](ctx, runtime)
		if err == nil || err.Error() != "Conversation 7 has no provider session ID" {
			t.Fatalf("err = %v, want Pi's error for a commit that left no identity", err)
		}
		if !slices.Equal(runtime.calls, []string{"snapshot", "commit"}) {
			t.Fatalf("calls = %v", runtime.calls)
		}
		for i, got := range runtime.contexts {
			if got.Value(providerContextKey{}) != "caller" {
				t.Errorf("call %d (%s) did not receive the caller's context", i, runtime.calls[i])
			}
		}
		if got := sessions.drain(); len(got) != 0 {
			t.Fatalf("recorded %v for a failed call", got)
		}
	})
	t.Run("a failing snapshot is the call's failure and no commit follows", func(t *testing.T) {
		sessions := newProviderSessionIds()
		runtime := newProviderRuntimeStub(sessions)
		runtime.snapshotErr = errors.New("snapshot failed")
		if _, err := ensureProviderSessionId[any, any, any, any](ctx, runtime); !errors.Is(err, runtime.snapshotErr) || !slices.Equal(runtime.calls, []string{"snapshot"}) {
			t.Fatalf("err = %v, calls = %v", err, runtime.calls)
		}
	})
	t.Run("a failing commit is the call's failure, not the missing-identity error", func(t *testing.T) {
		sessions := newProviderSessionIds()
		runtime := newProviderRuntimeStub(sessions)
		runtime.commitErr = errors.New("commit failed")
		if _, err := ensureProviderSessionId[any, any, any, any](ctx, runtime); !errors.Is(err, runtime.commitErr) {
			t.Fatalf("err = %v", err)
		}
		if got := sessions.drain(); len(got) != 0 {
			t.Fatalf("recorded %v for a failed call", got)
		}
	})
}
