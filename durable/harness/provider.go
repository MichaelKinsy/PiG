// Ports packages/durable/src/harness/provider.ts.

package harness

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// ProviderState is the stable provider-facing identity of one conversation (provider.ts:6-9).
type ProviderState struct {
	SessionId string `json:"sessionId"`
}

// ProviderDoc is the built-in provider state; every fork starts with a fresh identity instead of copying its parent, and every change stores a complete base (provider.ts:12-20).
var ProviderDoc = durable.DefineDoc(durable.DocDefinition[ProviderState]{
	CommonDocDefinition: durable.CommonDocDefinition[ProviderState]{
		Kind:    "pi.provider",
		Version: 1,
		CheckpointWhen: func(ProviderState, []durable.Op, durable.CheckpointInfo) bool {
			return true
		},

		Initial: newProviderState,
	},
	DocumentSemantics: durable.DocumentSemantics{
		Scope:   durable.ScopeConversation,
		History: durable.HistoryLatest,
		Fork:    durable.ForkInitial,
	},
})

// newProviderState draws a fresh UUIDv7. ai.UUIDv7 fails only when crypto/rand fails or 2^41 identities share one process, which Go's runtime and the clock rule out, so a failure is a broken invariant.
func newProviderState() ProviderState {
	id, err := ai.UUIDv7(nil)
	if err != nil {
		panic(fmt.Sprintf("pi.provider: UUIDv7: %v", err))
	}
	return ProviderState{SessionId: id}
}

// ensureProviderSessionId returns the persisted identity without writing in the normal path. A legacy conversation without pi.provider gets one migration commit whose tx.Doc creates the initial value before the provider request starts (provider.ts:22-39). Concurrent callers observe the committed winner.
func ensureProviderSessionId[I, S, R, H any](ctx context.Context, runtime durable.TaskRuntime[I, S, R, H]) (string, error) {
	existing, err := durable.Snapshot[ProviderState](ctx, runtime, ProviderDoc, runtime.ConversationId())
	if err != nil {
		return "", err
	}
	if existing != nil {
		return existing.SessionId, nil
	}
	var created string
	if err := runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[I, S, R]) (*durable.NextTaskState[S, R], error) {
		draft, err := docDraft(tx, ProviderDoc, runtime.ConversationId())
		if err != nil {
			return nil, err
		}
		created, _ = draft.Get("sessionId").(string)
		return nil, nil
	}); err != nil {
		return "", err
	}
	if created == "" {
		return "", fmt.Errorf("Conversation %d has no provider session ID", runtime.ConversationId())
	}
	return created, nil
}
