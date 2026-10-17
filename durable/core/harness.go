// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"github.com/MichaelKinsy/PiG/durable/core/docs"
	"github.com/MichaelKinsy/PiG/durable/core/inbox"
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// usageDef is UsageDoc (src/harness/usage.ts): the conversation's usage ledger.
// Read-only: assigned once at initialization.
var usageDef = &session.DocDef{
	Kind:           "pi.usage",
	Version:        1,
	Scope:          session.ScopeConversation,
	History:        session.HistoryLatest,
	Fork:           session.ForkInitial,
	Initial:        func(any) *jv.Object { return turn.UsageInitial() },
	CheckpointWhen: func(*jv.Object, []delta.Op, int) bool { return true },
}

// conversationCreated is the built-in creation hook of HarnessImpl.conversationCreated (harness.ts), in every commit that
// creates or forks a conversation: empty pi.live, pi.inbox and pi.usage, a fresh pi.provider and the conversation's
// pi.agent. The host runs HarnessOptions.conversationCreated itself in the same transaction, after this hook.
func (h *handler) conversationCreated(tx *session.Tx, record *jv.Object) error {
	id, _ := record.Get("id")
	args := session.DocArgs{Owner: session.ID(id.(float64))}
	for _, def := range []*session.DocDef{turn.LiveDef, inbox.Def, usageDef, docs.ProviderDef(h.uuid)} {
		if _, err := tx.Doc(def, args); err != nil {
			return err
		}
	}
	return docs.CreateAgent(tx, record)
}
