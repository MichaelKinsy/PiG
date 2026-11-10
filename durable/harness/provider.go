// Ports packages/durable/src/harness/provider.ts.

package harness

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// ProviderState is the stable provider-facing identity of one conversation (provider.ts:6-9).
type ProviderState struct {
	SessionId string `json:"sessionId"`
}

// ProviderDoc is the built-in provider state; every fork starts with a fresh identity instead of copying its parent, and every change stores a complete base (provider.ts:12-20).
var ProviderDoc = durable.DefineDoc(durable.DocDefinition[ProviderState]{
	Kind:    "pi.provider",
	Version: 1,
	CheckpointWhenRaw: func(durable.JsonObject, []durable.Op, durable.CheckpointInfo) bool {
		return true
	},

	Initial:           newProviderState,
	DocumentSemantics: durable.NewLatestConversationSemantics(durable.ForkInitial).DocumentSemantics,
})

// newProviderState draws a fresh UUIDv7. ai.UUIDv7 fails only when crypto/rand fails or 2^41 identities share one process, which Go's runtime and the clock rule out, so a failure is a broken invariant.
func newProviderState() ProviderState {
	id, err := ai.UUIDv7(nil)
	if err != nil {
		panic(fmt.Sprintf("pi.provider: UUIDv7: %v", err))
	}
	return ProviderState{SessionId: id}
}

// providerSessionIds is the set of provider session IDs the task runtimes of one Harness resolved (scheduler.ts providerSessionIds), in the order
// they were first recorded. Invocations run concurrently, so it is guarded.
type providerSessionIds struct {
	mu  sync.Mutex
	ids *orderedSet[string]
}

func newProviderSessionIds() *providerSessionIds { return &providerSessionIds{ids: newOrderedSet[string]()} }

func (sessions *providerSessionIds) add(sessionId string) {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	sessions.ids.Add(sessionId)
}

// drain returns the recorded IDs and forgets them (Harness.beforeClose reads the set, then clears it).
func (sessions *providerSessionIds) drain() []string {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	drained := sessions.ids.Values()
	sessions.ids.Clear()
	return drained
}

// providerSessionsKey carries a Harness's providerSessionIds on the context of its task invocations.
type providerSessionsKey struct{}

// withProviderSessions makes sessions the set that every task runtime whose Signal derives from ctx records into: provider.ts providerSessionsByRuntime, a
// WeakMap from a runtime to its Harness's set. A Go runtime reaches a handler wrapped by its task definition's types, so the association rides the
// runtime's Signal, which lives exactly as long as the invocation, as a WeakMap entry lives as long as the runtime.
func withProviderSessions(ctx context.Context, sessions *providerSessionIds) context.Context {
	return context.WithValue(ctx, providerSessionsKey{}, sessions)
}

// ensureProviderSessionId returns and records the persisted provider identity without writing in the normal path. A legacy conversation without pi.provider gets one migration commit whose tx.Doc creates the initial value before the provider request starts (provider.ts:26-48). Concurrent callers observe the committed winner.
func ensureProviderSessionId[I, S, R, H any](ctx context.Context, runtime durable.TaskRuntime[I, S, R, H]) (string, error) {
	existing, err := durable.Snapshot[ProviderState](ctx, runtime, ProviderDoc, runtime.ConversationId())
	if err != nil {
		return "", err
	}
	var sessionId string
	if existing != nil {
		sessionId = existing.SessionId
	} else {
		if err := runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[I, S, R]) (*durable.NextTaskState[S, R], error) {
			draft, err := docDraft(tx, ProviderDoc, runtime.ConversationId())
			if err != nil {
				return nil, err
			}
			sessionId, _ = draft.Get("sessionId").(string)
			return nil, nil
		}); err != nil {
			return "", err
		}
		if sessionId == "" {
			return "", fmt.Errorf("Conversation %d has no provider session ID", runtime.ConversationId())
		}
	}
	sessions, ok := runtime.Signal().Value(providerSessionsKey{}).(*providerSessionIds)
	if !ok {
		return "", errors.New("task runtime has no Harness provider session set")
	}
	sessions.add(sessionId)
	return sessionId, nil
}
