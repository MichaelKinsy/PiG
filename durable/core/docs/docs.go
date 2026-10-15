// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Package docs holds the built-in conversation documents of src/harness/agent.ts and provider.ts that no machine owns:
// pi.agent and pi.provider, and the conversation-creation step for pi.agent.
package docs

import (
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// AgentDef is AgentDoc: the conversation's agent state, rewindable so forks start from the agent at their fork entry. Its
// value holds the model, thinkingLevel, extensions, tools, instructions and cwd that are set.
// Read-only: assigned once at initialization.
var AgentDef = &session.DocDef{
	Kind:           "pi.agent",
	Version:        1,
	Scope:          session.ScopeConversation,
	History:        session.HistoryRewindable,
	Fork:           session.ForkAsOf,
	Initial:        func(any) *jv.Object { return jv.NewObject() },
	CheckpointWhen: func(*jv.Object, []delta.Op, int) bool { return true },
}

// ProviderDef is ProviderDoc: the stable provider-facing identity of one conversation, {sessionId}. A fork starts with a
// fresh identity instead of copying its parent's. newID is the Session's UUIDv7 source.
func ProviderDef(newID func() string) *session.DocDef {
	return &session.DocDef{
		Kind:    "pi.provider",
		Version: 1,
		Scope:   session.ScopeConversation,
		History: session.HistoryLatest,
		Fork:    session.ForkInitial,
		Initial: func(any) *jv.Object {
			value := jv.NewObject()
			value.Set("sessionId", newID())
			return value
		},
		CheckpointWhen: func(*jv.Object, []delta.Op, int) bool { return true },
	}
}

// CreateAgent is createAgent, the built-in part of every commit that creates or forks a conversation, for pi.agent: a fork
// keeps its asOf copy; a new task-owned conversation copies the stored agent of its owner task's conversation; a new
// ownerless one starts empty.
func CreateAgent(tx *session.Tx, conversation *jv.Object) error {
	if _, forked := conversation.Get("parent"); forked {
		return nil
	}
	id, _ := conversation.Get("id")
	agent, err := tx.Doc(AgentDef, session.DocArgs{Owner: session.ID(id.(float64))})
	if err != nil {
		return err
	}
	ownerValue, _ := conversation.Get("owner")
	owner, ok := ownerValue.(*jv.Object)
	if !ok {
		return nil
	}
	ownerConversation, _ := owner.Get("conversationId")
	from, err := tx.Doc(AgentDef, session.DocArgs{Owner: session.ID(ownerConversation.(float64))})
	if err != nil {
		return err
	}
	state := from.Snapshot()
	for _, key := range state.Keys() {
		value, _ := state.Get(key)
		if err := agent.Set(key, jv.Clone(value)); err != nil {
			return err
		}
	}
	return nil
}
