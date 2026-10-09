// Ports packages/durable/src/harness/context.ts.

package harness

import (
	"context"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/internal/detach"
	"github.com/MichaelKinsy/PiG/durable/internal/entryscan"
)

const contextScanPageSize = 256

const missingResultText = "Tool result unavailable: history ends before this call completed."

// excludedStopReason reports the assistant stop reasons that never enter model context (context.ts:9).
func excludedStopReason(reason ai.StopReason) bool {
	return reason == "aborted" || reason == "error" || reason == "deferred"
}

// ContextBounds are the head marker and newest visible entry that fix one committed context range.
type ContextBounds struct {
	// Head is the newest visible head marker at or below Tail; nil when none.
	Head *durable.EntryRecord
	Tail durable.EntryId
}

// CaptureContextBounds captures the bounds of the current context, or of the context cut off at the visible entry at, with two O(1) reads; nil when the conversation has no entry. Run it on the Session line; entries at or below the tail are immutable, so DeriveContext can then scan them off the line.
func CaptureContextBounds(ctx context.Context, storage durable.Storage, conversationId durable.ConversationId, at *durable.EntryId) (*ContextBounds, error) {
	var tail durable.EntryId
	if at == nil {
		page, err := storage.ScanEntries(ctx, durable.EntryQuery{ConversationId: conversationId}, 1, nil)
		if err != nil {
			return nil, err
		}
		if len(page.Items) == 0 {
			return nil, nil
		}
		tail = page.Items[0].Id
	} else {
		visible, err := storage.VisibleEntry(ctx, conversationId, *at)
		if err != nil {
			return nil, err
		}
		if visible == nil {
			return nil, fmt.Errorf("Entry %d is not visible from conversation %d", *at, conversationId)
		}
		tail = *at
	}
	head, err := storage.FindLatestHeadMarker(ctx, conversationId, &tail)
	if err != nil {
		return nil, err
	}
	return &ContextBounds{Head: head, Tail: tail}, nil
}

// lineReader runs read-only jobs on the Session line.
type lineReader interface {
	ReadOnLine(ctx context.Context, job func() (any, error)) (any, error)
}

// ReadContext returns the committed context of one conversation: bounds captured on the Session line, entries derived off it. The view is the caller's own.
func ReadContext(ctx context.Context, session lineReader, storage durable.Storage, conversationId durable.ConversationId, at *durable.EntryId) (durable.ContextView, error) {
	view, err := readContext(ctx, session, storage, conversationId, at, nil)
	return ownedView(storage, view), err
}

// readContext is ReadContext, deriving through cache when it is not nil. The view may share memory with storage and
// the cache: callers must not modify it.
func readContext(ctx context.Context, session lineReader, storage durable.Storage, conversationId durable.ConversationId, at *durable.EntryId, cache *contextCache) (durable.ContextView, error) {
	captured, err := session.ReadOnLine(ctx, func() (any, error) {
		return CaptureContextBounds(ctx, storage, conversationId, at)
	})
	if err != nil {
		return durable.ContextView{}, err
	}
	if cache != nil {
		return cache.derive(ctx, storage, conversationId, captured.(*ContextBounds))
	}
	return deriveContext(ctx, storage, conversationId, captured.(*ContextBounds))
}

// sharesEntries reports whether storage serves context reads from decoded entries it retains, so what they derive
// shares memory with it.
func sharesEntries(storage durable.Storage) bool {
	_, ok := storage.(entryscan.Ranger)
	return ok
}

// ownedView is view, deep-copied when it may share memory with storage.
func ownedView(storage durable.Storage, view durable.ContextView) durable.ContextView {
	if !sharesEntries(storage) {
		return view
	}
	return detach.ContextView(view)
}

// DeriveContext derives the active transcript and model context of one conversation within captured bounds.
//
// H is the newest visible head marker; the range runs from H.Head (or transcript start) through the tail. Per target, the newest edit in the range wins. Context entries are H followed by the range's non-head entries. The view is the caller's own.
func DeriveContext(ctx context.Context, storage durable.Storage, conversationId durable.ConversationId, bounds *ContextBounds) (durable.ContextView, error) {
	view, err := deriveContext(ctx, storage, conversationId, bounds)
	return ownedView(storage, view), err
}

// deriveContext is DeriveContext; the view may share memory with storage, so callers must not modify it.
func deriveContext(ctx context.Context, storage durable.Storage, conversationId durable.ConversationId, bounds *ContextBounds) (durable.ContextView, error) {
	if bounds == nil {
		return durable.ContextView{Entries: []durable.EntryRecord{}, Contributions: [][]ai.Message{}, Messages: []ai.Message{}}, nil
	}
	scanned, err := scanRange(ctx, storage, conversationId, bounds)
	if err != nil {
		return durable.ContextView{}, err
	}
	// Edits of every entry in the range count, including older head markers that selectActive drops.
	edits := map[durable.EntryId]durable.ContextEdit{}
	for _, entry := range scanned {
		for _, edit := range entry.Edits {
			edits[edit.Target] = edit
		}
	}
	entries := selectActive(bounds.Head, scanned)
	contributions := make([][]ai.Message, 0, len(entries))
	var flat []ai.Message
	for _, entry := range entries {
		edit, edited := edits[entry.Id]
		if edited && edit.Action == durable.EditOmit {
			contributions = append(contributions, []ai.Message{})
			continue
		}
		contributed := entry.Model
		if edited && edit.Action == durable.EditReplace {
			contributed = edit.Messages
		}
		kept := []ai.Message{}
		for _, message := range contributed {
			if assistant, ok := message.(ai.AssistantMessage); ok && excludedStopReason(assistant.StopReason) {
				continue
			}
			kept = append(kept, message)
		}
		contributions = append(contributions, kept)
		flat = append(flat, kept...)
	}
	return durable.ContextView{Head: bounds.Head, Entries: entries, Contributions: contributions, Messages: leadWithSystem(OrderToolResults(flat))}, nil
}

// ActiveEntries returns the raw active entries within captured bounds, without deriving model context. The entries are
// the caller's own.
func ActiveEntries(ctx context.Context, storage durable.Storage, conversationId durable.ConversationId, bounds *ContextBounds) ([]durable.EntryRecord, error) {
	entries, err := activeEntries(ctx, storage, conversationId, bounds)
	if err != nil || !sharesEntries(storage) {
		return entries, err
	}
	owned := make([]durable.EntryRecord, len(entries))
	for index := range entries {
		owned[index] = detach.Entry(entries[index])
	}
	return owned, nil
}

// activeEntries is ActiveEntries; the entries may share memory with storage, so callers must not modify them.
func activeEntries(ctx context.Context, storage durable.Storage, conversationId durable.ConversationId, bounds *ContextBounds) ([]durable.EntryRecord, error) {
	if bounds == nil {
		return []durable.EntryRecord{}, nil
	}
	scanned, err := scanRange(ctx, storage, conversationId, bounds)
	if err != nil {
		return nil, err
	}
	return selectActive(bounds.Head, scanned), nil
}

// scanRange returns the visible entries from the head marker's head, or transcript start, through the tail, oldest first.
func scanRange(ctx context.Context, storage durable.Storage, conversationId durable.ConversationId, bounds *ContextBounds) ([]durable.EntryRecord, error) {
	query := durable.EntryQuery{ConversationId: conversationId, MaxEntryId: new(bounds.Tail)}
	if bounds.Head != nil && bounds.Head.Head != nil {
		query.MinEntryId = new(*bounds.Head.Head)
	}
	// A storage that retains decoded entries serves the whole range at once; its entries are shared, so nothing below modifies one.
	if ranger, ok := storage.(entryscan.Ranger); ok {
		return ranger.VisibleEntryRange(ctx, query)
	}
	scanned, err := ScanAll(func(cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
		return storage.ScanEntries(ctx, query, contextScanPageSize, cursor)
	})
	if err != nil {
		return nil, err
	}
	for left, right := 0, len(scanned)-1; left < right; left, right = left+1, right-1 {
		scanned[left], scanned[right] = scanned[right], scanned[left]
	}
	return scanned, nil
}

// selectActive returns the head marker followed by the range's non-head entries, or the whole range without a marker.
func selectActive(head *durable.EntryRecord, scanned []durable.EntryRecord) []durable.EntryRecord {
	if head == nil {
		if scanned == nil {
			return []durable.EntryRecord{}
		}
		return scanned
	}
	active := []durable.EntryRecord{*head}
	for _, entry := range scanned {
		if entry.Head == nil {
			active = append(active, entry)
		}
	}
	return active
}

// leadWithSystem moves a system message that only user messages precede to the front. A run's input is committed before
// generation renders the system prompt, so a transcript, or the range after a compaction or reset, starts with user
// messages followed by the baseline system message. Providers treat only a leading system message as the initial prompt
// and tool set; without it, a later tool change rewrites the request's tool list and invalidates the whole prompt cache.
// The result is a new slice; messages is not modified.
func leadWithSystem(messages []ai.Message) []ai.Message {
	index := slices.IndexFunc(messages, func(message ai.Message) bool {
		_, isUser := message.(ai.UserMessage)
		return !isUser
	})
	if index <= 0 {
		return messages
	}
	if _, isSystem := messages[index].(ai.SystemMessage); !isSystem {
		return messages
	}
	led := make([]ai.Message, 0, len(messages))
	led = append(led, messages[index])
	led = append(led, messages[:index]...)
	return append(led, messages[index+1:]...)
}

// OrderToolResults places each assistant's tool results directly after it in call order. Results are taken from the messages before the next assistant; a missing result is synthesized and unmatched results are dropped.
func OrderToolResults(messages []ai.Message) []ai.Message {
	ordered := []ai.Message{}
	for index, message := range messages {
		if _, ok := message.(ai.ToolResultMessage); ok {
			continue
		}
		ordered = append(ordered, message)
		assistant, ok := message.(ai.AssistantMessage)
		if !ok {
			continue
		}
		var calls []ai.ToolCall
		for _, block := range assistant.Content {
			if call, isCall := block.(ai.ToolCall); isCall {
				calls = append(calls, call)
			}
		}
		if len(calls) == 0 {
			continue
		}
		results := map[string]int{}
		for next := index + 1; next < len(messages); next++ {
			if _, isAssistant := messages[next].(ai.AssistantMessage); isAssistant {
				break
			}
			if candidate, isResult := messages[next].(ai.ToolResultMessage); isResult {
				if _, seen := results[candidate.ToolCallID]; !seen {
					results[candidate.ToolCallID] = next
				}
			}
		}
		for _, call := range calls {
			if resultIndex, found := results[call.ID]; found {
				ordered = append(ordered, messages[resultIndex])
			} else {
				ordered = append(ordered, missingResult(call, assistant.Timestamp))
			}
		}
	}
	return ordered
}

func missingResult(call ai.ToolCall, timestamp int64) ai.ToolResultMessage {
	return ai.ToolResultMessage{
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    []ai.ToolResultMessageContent{ai.TextContent{Text: missingResultText}},
		IsError:    true,
		Details:    map[string]any{"reason": "missing_result"},
		Timestamp:  timestamp,
	}
}

// sharedContextKey marks a context whose Context reads may return views that share memory with the Harness. The
// generation handlers, which only read a view, set it; every other reader gets a copy.
type sharedContextKey struct{}

func sharedReads(ctx context.Context) context.Context {
	return context.WithValue(ctx, sharedContextKey{}, true)
}
