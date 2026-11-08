package contract

import (
	"context"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Registry is the session's subagent registry (empty when absent), its agents' key order in Order.
func Registry(ctx context.Context, read durable.DocumentReader) (SubagentState, error) {
	state, err := durable.Snapshot(ctx, read, SubagentsDoc, durable.ROOT_CONVERSATION_ID)
	if err != nil || state == nil {
		return SubagentState{Agents: map[string]Subagent{}, Reporters: map[string]durable.TaskId{}}, err
	}
	raw, err := read.SnapshotErased(ctx, SubagentsDoc, durable.ROOT_CONVERSATION_ID)
	if err != nil {
		return SubagentState{}, err
	}
	if agents, ok := raw.Value("agents").(*delta.JsonObject); ok {
		for key := range agents.All() {
			state.Order = append(state.Order, key)
		}
	}
	return *state, nil
}

// OwnLocation is a conversation's own location (a subagent spawned with a cwd, or a one-shot agent started by a
// conversation with one), or nil: it follows the session.
func OwnLocation(ctx context.Context, read durable.DocumentReader, conversation durable.ConversationId) (*p2runtime.Location, error) {
	if conversation == durable.ROOT_CONVERSATION_ID {
		return nil, nil
	}
	registry, err := Registry(ctx, read)
	if err != nil {
		return nil, err
	}
	for _, agent := range registry.Agents {
		if agent.ConversationId == conversation && agent.Location != nil {
			return LocationOf(agent.Location), nil
		}
	}
	inherited, err := durable.Snapshot(ctx, read, InheritedLocationDoc, conversation)
	if err != nil || inherited == nil {
		return nil, err
	}
	return LocationOf(inherited.Location), nil
}

// LocationOf is a stored location as a runtime Location; nil for none.
func LocationOf(location *LocationValue) *p2runtime.Location {
	if location == nil {
		return nil
	}
	return &p2runtime.Location{Cwd: location.Cwd, Env: location.Env}
}

// PlaceSubagent gives the subagent working in conversation its own location, in tx (a granted `location_move`): its
// registry entry and its agent cwd. False when the conversation is not a subagent (one-shot agents are not).
func PlaceSubagent(tx durable.Tx, conversation durable.ConversationId, location p2runtime.Location) (bool, error) {
	state, err := durable.TxDoc[SubagentState](tx, SubagentsDoc, durable.ROOT_CONVERSATION_ID)
	if err != nil {
		return false, err
	}
	agents := state.Object("agents")
	for _, name := range agents.Keys() {
		agent := agents.Object(name)
		if id, _ := agent.Get("conversationId").(float64); durable.ConversationId(id) != conversation {
			continue
		}
		var env any
		if location.Env != nil {
			env = *location.Env
		}
		if err := agent.Set("location", delta.JsonObjectOf("cwd", location.Cwd, "env", env)); err != nil {
			return false, err
		}
		return true, harness.Configure(tx, conversation, harness.AgentChange{Cwd: harness.SetTo(location.Cwd)})
	}
	return false, nil
}
