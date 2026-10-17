// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package rec

import "github.com/MichaelKinsy/PiG/durable/core/jv"

// Conversation is a conversation record (spec §2 ConversationRecord): `{id, parent?: {conversationId, at}, owner?:
// {conversationId, taskId}}`. It is immutable once written.
type Conversation struct{ V *jv.Object }

// NewConversation builds the record in Pi's key order (src/session/transaction.ts #stageConversation). parent and owner
// are absent when their conversation ID is 0.
func NewConversation(convID, parentConv, parentAt, ownerConv, ownerTask int64) *Conversation {
	o := jv.NewObjectCap(3)
	o.Set("id", id(convID))
	if parentConv != 0 {
		p := jv.NewObjectCap(2)
		p.Set("conversationId", id(parentConv))
		p.Set("at", id(parentAt))
		o.Set("parent", p)
	}
	if ownerConv != 0 {
		w := jv.NewObjectCap(2)
		w.Set("conversationId", id(ownerConv))
		w.Set("taskId", id(ownerTask))
		o.Set("owner", w)
	}
	return &Conversation{V: o}
}

// ParseConversation reads a stored conversation record.
func ParseConversation(b []byte) (*Conversation, error) {
	o, err := parseObject(b)
	if err != nil {
		return nil, err
	}
	return ConversationOf(o)
}

// ConversationOf reads a conversation record that is already a value: ParseConversation without the text, with the same checks.
func ConversationOf(o *jv.Object) (*Conversation, error) {
	if o == nil {
		return nil, ErrShape
	}
	if _, ok := idOf(o, "id"); !ok {
		return nil, ErrShape
	}
	return &Conversation{V: o}, nil
}

// Append appends the record's JSON.stringify.
func (c *Conversation) Append(dst []byte) []byte { return jv.Append(dst, c.V) }

// ID is the conversation ID.
func (c *Conversation) ID() int64 { n, _ := idOf(c.V, "id"); return n }

// Parent returns the fork source and the inclusive parent entry; ok is false for a root.
func (c *Conversation) Parent() (conv, at int64, ok bool) {
	p := objectOf(c.V, "parent")
	if p == nil {
		return 0, 0, false
	}
	conv, okC := idOf(p, "conversationId")
	at, okA := idOf(p, "at")
	return conv, at, okC && okA
}

// OwnerConversation and OwnerTask are the owner_conversation_id and owner_task_id column values
// (`write.value.owner?.conversationId ?? null`): ok is false where Pi binds NULL.
func (c *Conversation) OwnerConversation() (int64, bool) {
	if p := objectOf(c.V, "owner"); p != nil {
		return idOf(p, "conversationId")
	}
	return 0, false
}

// OwnerTask is the owner_task_id column value; see OwnerConversation.
func (c *Conversation) OwnerTask() (int64, bool) {
	if p := objectOf(c.V, "owner"); p != nil {
		return idOf(p, "taskId")
	}
	return 0, false
}
