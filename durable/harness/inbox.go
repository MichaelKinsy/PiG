// Ports packages/durable/src/harness/inbox.ts.

package harness

import (
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// InboxMode is the mode of a queued submission: user input for a run (steer or followUp) or a passive entry write.
type InboxMode string

const (
	InboxSteer    InboxMode = "steer"
	InboxFollowUp InboxMode = "followUp"
	InboxWrite    InboxMode = "write"
)

// InboxItem is a queued submission: user input for a run, with Content, or a passive entry write, with Entry, an
// EntryDraft stored as plain JSON (inbox.ts:8-11).
type InboxItem struct {
	Id   durable.SubmissionId `json:"id"`
	Mode InboxMode            `json:"mode"`
	// Content is the JSON representation of the user input; nil for writes.
	Content durable.JsonValue `json:"content,omitempty"`
	// Entry is the JSON representation of the entry draft; nil for user input.
	Entry durable.JsonObject `json:"entry,omitempty"`
}

// InboxState is the built-in queue of one conversation's submissions waiting for a boundary, in ID order.
type InboxState struct {
	Items []InboxItem `json:"items"`
}

// InboxDoc is the built-in pi.inbox document; a complete base is stored exactly while it is empty (inbox.ts:16-24).
var InboxDoc = durable.DefineDoc(durable.DocDefinition[InboxState]{
	CommonDocDefinition: durable.CommonDocDefinition[InboxState]{
		Kind:    "pi.inbox",
		Version: 1,
		CheckpointWhen: func(value InboxState, _ []durable.Op, _ durable.CheckpointInfo) bool {
			return len(value.Items) == 0
		},
	},
	DocumentSemantics: durable.DocumentSemantics{
		Scope:   durable.ScopeConversation,
		History: durable.HistoryLatest,
		Fork:    durable.ForkInitial,
	},
	Initial: func() InboxState { return InboxState{Items: []InboxItem{}} },
})

// QueueModes are the settings a boundary reads, on the Session line.
type QueueModes struct {
	SteeringMode durable.QueueMode
	FollowUpMode durable.QueueMode
}

// QueueModesOf returns the queue modes of resolved settings.
func QueueModesOf(settings durable.Settings) QueueModes {
	return QueueModes{SteeringMode: settings.SteeringMode, FollowUpMode: settings.FollowUpMode}
}

// Boundary is what a boundary reads before the commit's first table write, and the newest head it has seen so far.
type Boundary struct {
	ConversationId durable.ConversationId
	// Inbox is the pi.inbox draft of the boundary's commit.
	Inbox        *delta.Object
	SteeringMode durable.QueueMode
	FollowUpMode durable.QueueMode
	// Head is the start of the active range, the newest head marker's head; advanced by heads written in this
	// commit. Nil when no head marker exists.
	Head *durable.EntryId
}

// BoundaryResult holds the selected user items, in ID order, and whether a head "self" write (a reset) was placed.
type BoundaryResult struct {
	Users []durable.SubmissionId
	Reset bool
}

// BoundaryAt is where a boundary runs: after a tool round, or at the end of a run.
type BoundaryAt string

const (
	BoundaryPostTools BoundaryAt = "postTools"
	BoundaryFinal     BoundaryAt = "final"
)

// PrepareBoundary reads what a boundary needs. Table reads must precede the commit's first table write, so callers
// prepare the boundary at the start of their commit (inbox.ts:44-55).
func PrepareBoundary(tx durable.Tx, conversationId durable.ConversationId, modes QueueModes) (*Boundary, error) {
	marker, err := tx.LatestHeadMarker(conversationId)
	if err != nil {
		return nil, err
	}
	inbox, err := docDraft(tx, InboxDoc, conversationId)
	if err != nil {
		return nil, err
	}
	boundary := &Boundary{ConversationId: conversationId, Inbox: inbox, SteeringMode: modes.SteeringMode, FollowUpMode: modes.FollowUpMode}
	if marker != nil && marker.Head != nil {
		head := *marker.Head
		boundary.Head = &head
	}
	return boundary, nil
}

// ApplyBoundary places the queued items a boundary selects (spec §6, inbox.ts:65-108): every write, the first or all
// steers, and at final the first or all follow-ups. A selected reset turns a postTools boundary into final. Writes are
// placed first and user items after them, each in ID order, so user items queued before a reset run in the new
// context. A write whose head targets an entry before the active range, including a range started earlier in this
// commit, is stale. Selected and stale items are removed positionally.
func ApplyBoundary(tx durable.Tx, boundary *Boundary, at BoundaryAt, now float64) (BoundaryResult, error) {
	conversationId := boundary.ConversationId
	state, err := decodeDraft[InboxState](boundary.Inbox)
	if err != nil {
		return BoundaryResult{}, err
	}
	items := state.Items
	reset := false
	for _, item := range items {
		if item.Mode == InboxWrite && item.Entry["head"] == "self" {
			reset = true
			break
		}
	}
	final := at == BoundaryFinal || reset
	pick := func(mode InboxMode, queueMode durable.QueueMode) []int {
		var indexes []int
		for index, item := range items {
			if item.Mode == mode {
				indexes = append(indexes, index)
			}
		}
		if queueMode != durable.QueueAll && len(indexes) > 1 {
			indexes = indexes[:1]
		}
		return indexes
	}
	var writes []int
	for index, item := range items {
		if item.Mode == InboxWrite {
			writes = append(writes, index)
		}
	}
	users := pick(InboxSteer, boundary.SteeringMode)
	if final {
		users = append(users, pick(InboxFollowUp, boundary.FollowUpMode)...)
	}
	slices.Sort(users)

	// AppendEntry copies the drafts' values; the items are removed only afterwards.
	for _, index := range writes {
		item := items[index]
		var draft durable.EntryDraft
		if err := decodeJSON(item.Entry, &draft); err != nil {
			return BoundaryResult{}, err
		}
		if IsStale(boundary, draft) {
			if err := tx.SettleSubmission(item.Id, durable.SubmissionSettlement{Status: durable.SubmissionUnanswered, Reason: "stale"}); err != nil {
				return BoundaryResult{}, err
			}
			continue
		}
		entry, err := tx.AppendEntry(conversationId, draft)
		if err != nil {
			return BoundaryResult{}, err
		}
		if draft.HeadSelf {
			id := entry.Id
			boundary.Head = &id
		} else if draft.Head != nil {
			id := *draft.Head
			boundary.Head = &id
		}
		if err := tx.PlaceSubmission(item.Id, entry.Id); err != nil {
			return BoundaryResult{}, err
		}
	}
	placed := []durable.SubmissionId{}
	for _, index := range users {
		item := items[index]
		content, err := durable.DecodeUserContent(item.Content)
		if err != nil {
			return BoundaryResult{}, err
		}
		message := ai.UserMessage{Content: content, Timestamp: int64(now)}
		entry, err := durable.TxAppendEntry(tx, durable.UserEntry, conversationId, durable.TypedEntryDraft[durable.Never]{Model: []ai.Message{message}})
		if err != nil {
			return BoundaryResult{}, err
		}
		if err := tx.PlaceSubmission(item.Id, entry.Id); err != nil {
			return BoundaryResult{}, err
		}
		placed = append(placed, item.Id)
	}
	removed := append(slices.Clone(writes), users...)
	slices.SortFunc(removed, func(a, b int) int { return b - a })
	array := boundary.Inbox.Array("items")
	for _, index := range removed {
		if _, err := array.Splice(index, 1); err != nil {
			return BoundaryResult{}, err
		}
	}
	return BoundaryResult{Users: placed, Reset: reset}, nil
}

// IsStale reports whether a head write targets an entry before the active range, so placing it would bring back cut
// history (inbox.ts:111-113).
func IsStale(boundary *Boundary, entry durable.EntryDraft) bool {
	return !entry.HeadSelf && entry.Head != nil && boundary.Head != nil && *entry.Head < *boundary.Head
}

// RemoveInboxItem removes a withdrawn submission's item; the caller settles the submission.
func RemoveInboxItem(tx durable.Tx, conversationId durable.ConversationId, id durable.SubmissionId) error {
	items, err := inboxItems(tx, conversationId)
	if err != nil {
		return err
	}
	for index := range items.Len() {
		if items.Object(index).Get("id") == float64(id) {
			_, err := items.Splice(index, 1)
			return err
		}
	}
	return nil
}

// WithdrawQueuedInputs withdraws every queued input of a conversation, as Conversation.Abort and abort cascades do:
// each settles unanswered with aborted and leaves the inbox; queued writes stay for later placement
// (inbox.ts:127-136).
func WithdrawQueuedInputs(tx durable.Tx, conversationId durable.ConversationId) error {
	items, err := inboxItems(tx, conversationId)
	if err != nil {
		return err
	}
	for index := items.Len() - 1; index >= 0; index-- {
		item := items.Object(index)
		if item.Get("mode") == string(InboxWrite) {
			continue
		}
		id, _ := item.Get("id").(float64)
		if err := tx.SettleSubmission(durable.SubmissionId(id), durable.SubmissionSettlement{Status: durable.SubmissionUnanswered, Reason: "aborted"}); err != nil {
			return err
		}
		if _, err := items.Splice(index, 1); err != nil {
			return err
		}
	}
	return nil
}

// inboxItems returns the items draft of a conversation's pi.inbox.
func inboxItems(tx durable.Tx, conversationId durable.ConversationId) (*delta.Array, error) {
	inbox, err := docDraft(tx, InboxDoc, conversationId)
	if err != nil {
		return nil, err
	}
	items := inbox.Array("items")
	if items == nil {
		return nil, fmt.Errorf("pi.inbox of conversation %d has no items", conversationId)
	}
	return items, nil
}
