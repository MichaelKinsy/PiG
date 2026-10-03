package durable

import "fmt"

// Ports packages/durable/src/entries.ts

// DefineEntry defines a typed entry kind whose Is guard matches by EntryRecord.Kind. It panics when kind is empty,
// where upstream throws a TypeError at definition time.
func DefineEntry[D any](kind string) Entry[D] {
	if kind == "" {
		panic("Entry kind must be a non-empty string")
	}
	return Entry[D]{Kind: kind}
}

// Is reports whether entry is present and has this kind.
func (token Entry[D]) Is(entry *EntryRecord) bool {
	return entry != nil && entry.Kind == token.Kind
}

// As returns entry with its Data decoded as D; nil when entry is absent or has another kind.
func (token Entry[D]) As(entry *EntryRecord) (*TypedEntry[D], error) {
	if !token.Is(entry) {
		return nil, nil
	}
	typed := &TypedEntry[D]{EntryRecord: *entry}
	if entry.Data != nil {
		data, err := FromJsonValue[D](entry.Data)
		if err != nil {
			return nil, fmt.Errorf("entry %d (%s) data: %w", entry.Id, entry.Kind, err)
		}
		typed.TypedData = data
	}
	return typed, nil
}

// Draft returns the untyped draft of a typed draft.
func (token Entry[D]) Draft(value TypedEntryDraft[D]) (EntryDraft, error) {
	draft := EntryDraft{Kind: token.Kind, Model: value.Model, Edits: value.Edits, Head: value.Head, HeadSelf: value.HeadSelf}
	if _, never := any(value.Data).(Never); !never {
		data, err := ToJsonValue(value.Data)
		if err != nil {
			return EntryDraft{}, err
		}
		draft.Data = data
	}
	return draft, nil
}

// TxEntry returns the entry when it is present and has the token's kind; nil otherwise.
func TxEntry[D any](tx Tx, token Entry[D], id EntryId) (*TypedEntry[D], error) {
	entry, err := tx.Entry(id)
	if err != nil {
		return nil, err
	}
	return token.As(entry)
}

// TxAppendEntry appends an entry of the token's kind.
func TxAppendEntry[D any](tx Tx, token Entry[D], conversationId ConversationId, value TypedEntryDraft[D]) (*TypedEntry[D], error) {
	draft, err := token.Draft(value)
	if err != nil {
		return nil, err
	}
	entry, err := tx.AppendEntry(conversationId, draft)
	if err != nil {
		return nil, err
	}
	return token.As(&entry)
}

// ToolResultEntryData is the data of a ToolResultEntry.
type ToolResultEntryData struct {
	Diagnostics []ToolDiagnostic `json:"diagnostics"`
}

// CompactionEntryData is the data of a CompactionEntry.
type CompactionEntryData struct {
	Reason CompactionReason `json:"reason"`
}

var (
	// UserEntry is user input: Model is [UserMessage]. Written by submissions.
	UserEntry = DefineEntry[Never]("pi.user")
	// AssistantEntry is a provider result with any stop reason: Model is [AssistantMessage]. Written by generation.
	AssistantEntry = DefineEntry[Never]("pi.assistant")
	// SystemEntry is a positional prompt and tool change: Model is [SystemMessage] with empty content.
	SystemEntry = DefineEntry[Never]("pi.system")
	// ToolResultEntry is a tool result: Model is [ToolResultMessage], whose content ends with the rendered diagnostics
	// block; Data holds the structured diagnostics, possibly none. Written by tool tasks, and by generation for calls
	// it did not offer.
	ToolResultEntry = DefineEntry[ToolResultEntryData]("pi.tool-result")
	// ResetEntry is the start of a new context: always head "self", with Model absent for a plain reset or
	// [UserMessage] carrying the handoff text. Written by Conversation.Reset and the handoff tool control.
	ResetEntry = DefineEntry[Never]("pi.reset")
	// CompactionEntry is a compaction summary: Model is [UserMessage] with the wrapped summary, Head the first kept
	// entry. Written by compaction tasks, directly or through a write submission.
	CompactionEntry = DefineEntry[CompactionEntryData]("pi.compaction")
)
