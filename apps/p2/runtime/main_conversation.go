package runtime

import (
	"context"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/durable"
)

// Ports apps/p2/src/runtime/main-conversation.ts: the session's top-level conversations (the main conversation and its
// `/tree` branches) and which one is current.

// MainBranch is a top-level conversation and its label, what the sidebar and `/tree` call it. A nil label shows the
// session title: conversation 1's default (it has no fork point), so it follows a session rename until the branch is
// renamed.
type MainBranch struct {
	Conversation int     `json:"conversation"`
	Label        *string `json:"label"`
}

// MainState is `p2.main`: the current main conversation and the top-level conversations in creation order.
type MainState struct {
	Conversation int          `json:"conversation"`
	Branches     []MainBranch `json:"branches"`
}

// MainDoc is the session's top-level conversations (`docs/tree.md`): conversation 1 and the branches `/tree` created, in
// creation order, each with its label, and the current main conversation among them, the one the session shows and
// follows. Session-scoped, so a fork never copies it. An explicit list, not "conversations no task owns": older sessions
// have subagents created without an owner.
var MainDoc = durable.DefineDoc(durable.DocDefinition[MainState]{
	CommonDocDefinition: durable.CommonDocDefinition[MainState]{
		Kind: "p2.main",
		// 2: stored labels. Version 1 listed IDs only: their labels start nil, and the tree worker fills them at start.
		Version: 2,
		Initial: func() MainState {
			return MainState{Conversation: int(durable.ROOT_CONVERSATION_ID), Branches: []MainBranch{{Conversation: int(durable.ROOT_CONVERSATION_ID)}}}
		},
		Migrate: func(value durable.JsonObject, fromVersion int) (MainState, error) {
			if fromVersion != 1 {
				return MainState{}, fmt.Errorf("Unknown p2.main version %d", fromVersion)
			}
			current, _ := value.Value("conversation").(float64)
			state := MainState{Conversation: int(current)}
			branches, _ := value.Value("branches").([]any)
			for _, branch := range branches {
				id, _ := branch.(float64)
				state.Branches = append(state.Branches, MainBranch{Conversation: int(id)})
			}
			return state, nil
		},
		CheckpointWhen: func(MainState, []durable.Op, durable.CheckpointInfo) bool { return true },
	},
	DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeSession},
})

// MainConversation is the current main conversation's ID.
func MainConversation(ctx context.Context, read durable.DocumentReader) (durable.ConversationId, error) {
	state, err := durable.Snapshot(ctx, read, MainDoc)
	if err != nil || state == nil {
		return durable.ROOT_CONVERSATION_ID, err
	}
	return durable.ConversationId(state.Conversation), nil
}

// TopLevelConversations are the top-level conversations' IDs: conversation 1, then the `/tree` branches.
func TopLevelConversations(ctx context.Context, read durable.DocumentReader) ([]durable.ConversationId, error) {
	state, err := durable.Snapshot(ctx, read, MainDoc)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return []durable.ConversationId{durable.ROOT_CONVERSATION_ID}, nil
	}
	ids := make([]durable.ConversationId, len(state.Branches))
	for i, branch := range state.Branches {
		ids[i] = durable.ConversationId(branch.Conversation)
	}
	return ids, nil
}

// IsTopLevel is whether conversation id is top-level (conversation 1 or a `/tree` branch), not a subagent or one-shot
// agent.
func IsTopLevel(ctx context.Context, read durable.DocumentReader, id durable.ConversationId) (bool, error) {
	ids, err := TopLevelConversations(ctx, read)
	return slices.Contains(ids, id), err
}

// AgentConversation is the conversation holding an agent's own documents (its artifacts list, voice and chat mode):
// conversation 1 for the main agent, whichever top-level conversation it works in, so a `/tree` switch keeps them; a
// subagent's or one-shot agent's own conversation otherwise.
func AgentConversation(ctx context.Context, read durable.DocumentReader, id durable.ConversationId) (durable.ConversationId, error) {
	top, err := IsTopLevel(ctx, read, id)
	if err != nil || top {
		return durable.ROOT_CONVERSATION_ID, err
	}
	return id, nil
}

// TopLevelRecords are the top-level conversations' records (their fork points), in ID order; records are table reads, in a
// commit.
func TopLevelRecords(ctx context.Context, session durable.Session) ([]durable.ConversationRecord, error) {
	ids, err := TopLevelConversations(ctx, session)
	if err != nil {
		return nil, err
	}
	records, err := session.Commit(ctx, func(tx durable.Tx) (any, error) {
		var records []durable.ConversationRecord
		for _, id := range ids {
			record, err := tx.Conversation(id)
			if err != nil {
				return nil, err
			}
			if record != nil {
				records = append(records, *record)
			}
		}
		slices.SortFunc(records, func(a, b durable.ConversationRecord) int { return int(a.Id) - int(b.Id) })
		return records, nil
	})
	if err != nil {
		return nil, err
	}
	return records.([]durable.ConversationRecord), nil
}
