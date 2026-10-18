// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Package inbox is the Session-side submission queue of src/harness/inbox.ts and submissions.ts: the built-in pi.inbox
// document, the boundaries that place queued items, and admission, abort and withdrawal of submissions. It runs inside a
// session.Tx and holds no state.
package inbox

import (
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// Queue modes of the settings (src/harness/types.ts QueueMode).
const (
	ModeOne = "one-at-a-time"
	ModeAll = "all"
)

// Def is InboxDoc: one conversation's submissions waiting for a boundary, in ID order. Its value is {items}; an item is
// {id, mode: "steer"|"followUp", content, entry?} (entry: the {kind, data?} an input is placed as when not pi.user) or
// {id, mode: "write", entry}.
// Read-only: assigned once at initialization.
var Def = &session.DocDef{
	Kind:    "pi.inbox",
	Version: 1,
	Scope:   session.ScopeConversation,
	History: session.HistoryLatest,
	Fork:    session.ForkInitial,
	Initial: func(any) *jv.Object {
		value := jv.NewObject()
		value.Set("items", []any{})
		return value
	},
	CheckpointWhen: func(value *jv.Object, _ []delta.Op, _ int) bool {
		items, _ := value.Get("items")
		list, _ := items.([]any)
		return len(list) == 0
	},
}

// QueueModes are the settings a boundary reads: steeringMode and followUpMode.
type QueueModes struct{ Steering, FollowUp string }

// Boundary is what a boundary reads before the commit's first table write, and the newest head it has seen so far.
type Boundary struct {
	ConversationID session.ID
	Inbox          *delta.Obj
	Modes          QueueModes
	// Head is the start of the active range, the newest head marker's head; heads written in this commit advance it.
	Head    session.ID
	HasHead bool
}

// Result is the selected user items in ID order, and whether a head "self" write (a reset) was placed.
type Result struct {
	Users []session.ID
	Reset bool
}

// Prepare is prepareBoundary: table reads must precede the commit's first table write, so callers prepare the boundary at
// the start of their commit.
func Prepare(tx *session.Tx, conversationID session.ID, modes QueueModes) (*Boundary, error) {
	marker, err := tx.LatestHeadMarker(conversationID)
	if err != nil {
		return nil, err
	}
	b := &Boundary{ConversationID: conversationID, Modes: modes}
	if marker != nil {
		if head, ok := marker.Get("head"); ok {
			if n, isNum := head.(float64); isNum {
				b.Head, b.HasHead = session.ID(n), true
			}
		}
	}
	if b.Inbox, err = tx.Doc(Def, session.DocArgs{Owner: conversationID}); err != nil {
		return nil, err
	}
	return b, nil
}

func itemID(item *delta.Obj) session.ID {
	id, _ := item.Get("id").(float64)
	return session.ID(id)
}

func itemMode(item *delta.Obj) string {
	mode, _ := item.Get("mode").(string)
	return mode
}

func queued(inbox *delta.Obj) *delta.Arr { return inbox.Arr("items") }

// Apply is applyBoundary: it places the queued items a boundary selects (spec section 6): every write, the first or all
// steers, and at final the first or all follow-ups. A selected reset turns a postTools boundary into final. Writes are
// placed first and user items after them, each in ID order, so user items queued before a reset run in the new context. A
// write whose head targets an entry before the active range, including a range started earlier in this commit, is stale.
// Selected and stale items are removed positionally, after the entries are appended.
func Apply(tx *session.Tx, b *Boundary, final bool, now float64) (Result, error) {
	items := queued(b.Inbox)
	n := items.Len()
	reset := HasQueuedReset(items)
	var writes []int
	for i := range n {
		if itemMode(items.Obj(i)) == "write" {
			writes = append(writes, i)
		}
	}
	final = final || reset
	pick := func(mode, queueMode string) []int {
		var indexes []int
		for i := range n {
			if itemMode(items.Obj(i)) == mode {
				indexes = append(indexes, i)
			}
		}
		if queueMode != ModeAll && len(indexes) > 1 {
			indexes = indexes[:1]
		}
		return indexes
	}
	users := pick("steer", b.Modes.Steering)
	if final {
		users = append(users, pick("followUp", b.Modes.FollowUp)...)
	}
	slices.Sort(users)

	for _, index := range writes {
		item := items.Obj(index)
		draft := item.Obj("entry").Snapshot()
		if b.IsStale(draft) {
			if err := tx.SettleSubmission(itemID(item), Unanswered("stale")); err != nil {
				return Result{}, err
			}
			continue
		}
		entry, err := tx.AppendEntry(b.ConversationID, draft)
		if err != nil {
			return Result{}, err
		}
		entryID, _ := entry.Get("id")
		if head, ok := draft.Get("head"); ok {
			if head == "self" {
				b.Head, b.HasHead = session.ID(entryID.(float64)), true
			} else if n, isNum := head.(float64); isNum {
				b.Head, b.HasHead = session.ID(n), true
			}
		}
		if err := tx.PlaceSubmission(itemID(item), session.ID(entryID.(float64))); err != nil {
			return Result{}, err
		}
	}
	placed := make([]session.ID, 0, len(users))
	for _, index := range users {
		item := items.Obj(index)
		entry, err := tx.AppendEntry(b.ConversationID, InputDraft(item.Get("content"), item.Obj("entry"), now))
		if err != nil {
			return Result{}, err
		}
		entryID, _ := entry.Get("id")
		if err := tx.PlaceSubmission(itemID(item), session.ID(entryID.(float64))); err != nil {
			return Result{}, err
		}
		placed = append(placed, itemID(item))
	}
	removed := append(slices.Clone(writes), users...)
	slices.SortFunc(removed, func(a, b int) int { return b - a })
	for _, index := range removed {
		if _, err := items.Splice(index, 1); err != nil {
			return Result{}, err
		}
	}
	return Result{Users: placed, Reset: reset}, nil
}

// IsStale is isStale: whether a head write targets an entry before the active range, so placing it would bring back cut
// history.
func (b *Boundary) IsStale(entry *jv.Object) bool {
	head, ok := entry.Get("head")
	n, isNum := head.(float64)
	return ok && isNum && b.HasHead && session.ID(n) < b.Head
}

// UserDraft is the pi.user entry draft of one user input: model is [UserMessage].
func UserDraft(content any, now float64) *jv.Object {
	message := jv.NewObject()
	message.Set("role", "user")
	message.Set("content", jv.Clone(content))
	message.Set("timestamp", now)
	// A typed appendEntry spreads the draft, then sets kind ({...value, kind}, session/transaction.ts appendEntry).
	draft := jv.NewObject()
	draft.Set("model", []any{message})
	draft.Set("kind", "pi.user")
	return draft
}

// InputDraft is the entry draft an input is placed as (appendInput): the pi.user draft, or the input's entry {kind, data?}
// with the user message of content as its model.
func InputDraft(content any, entry *delta.Obj, now float64) *jv.Object {
	if entry == nil {
		return UserDraft(content, now)
	}
	return withInputModel(PickInputEntry(entry.Snapshot()), UserDraft(content, now))
}

// withInputModel is {...picked, model}: the picked kind and data, then the user draft's model.
func withInputModel(picked, user *jv.Object) *jv.Object {
	model, _ := user.Get("model")
	picked.Set("model", model)
	return picked
}

// PickInputEntry is pickInputEntry: the kind and data of an input's entry as strict JSON, without other members an untyped
// caller might pass.
func PickInputEntry(entry *jv.Object) *jv.Object {
	picked := jv.NewObject()
	kind, _ := entry.Get("kind")
	picked.Set("kind", jv.Clone(kind))
	if data, has := entry.Get("data"); has {
		picked.Set("data", jv.Clone(data))
	}
	return picked
}

// IsDisplayOnly is isDisplayOnly: whether a write has no model, head or edits (a present member counts, even if empty), so
// it changes nothing the model sees and needs no boundary.
func IsDisplayOnly(entry *jv.Object) bool {
	return !entry.Has("model") && !entry.Has("head") && !entry.Has("edits")
}

// HasQueuedReset is hasQueuedReset: whether a reset (a head "self" write) is queued; entries appended before it leave the
// active range when it lands.
func HasQueuedReset(items *delta.Arr) bool {
	for i := range items.Len() {
		item := items.Obj(i)
		if itemMode(item) != "write" {
			continue
		}
		if entry := item.Obj("entry"); entry != nil && entry.Get("head") == "self" {
			return true
		}
	}
	return false
}

// Unanswered is the settlement {status: "unanswered", reason}.
func Unanswered(reason string) *jv.Object {
	settlement := jv.NewObject()
	settlement.Set("status", "unanswered")
	settlement.Set("reason", reason)
	return settlement
}

// RemoveItem is removeInboxItem: a withdrawn submission's item leaves the queue; the caller settles the submission.
func RemoveItem(tx *session.Tx, conversationID, id session.ID) error {
	inbox, err := tx.Doc(Def, session.DocArgs{Owner: conversationID})
	if err != nil {
		return err
	}
	items := queued(inbox)
	for i := range items.Len() {
		if itemID(items.Obj(i)) == id {
			_, err := items.Splice(i, 1)
			return err
		}
	}
	return nil
}

// WithdrawQueued is withdrawQueuedInputs, used by Conversation.abort() and abort cascades: each queued input settles
// unanswered with reason aborted and leaves the inbox; queued writes stay for later placement.
func WithdrawQueued(tx *session.Tx, conversationID session.ID) error {
	return WithdrawQueuedFor(tx, conversationID, "aborted")
}

// WithdrawQueuedFor is withdrawQueuedInputs with a reason: each queued input settles unanswered with reason and leaves the
// inbox; queued writes stay for later placement.
func WithdrawQueuedFor(tx *session.Tx, conversationID session.ID, reason string) error {
	inbox, err := tx.Doc(Def, session.DocArgs{Owner: conversationID})
	if err != nil {
		return err
	}
	items := queued(inbox)
	for i := items.Len() - 1; i >= 0; i-- {
		item := items.Obj(i)
		if itemMode(item) == "write" {
			continue
		}
		if err := tx.SettleSubmission(itemID(item), Unanswered(reason)); err != nil {
			return err
		}
		if _, err := items.Splice(i, 1); err != nil {
			return err
		}
	}
	return nil
}
