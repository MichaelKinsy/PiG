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
	view, err := readContext(ctx, session, storage, conversationId, at)
	return ownedView(storage, view), err
}

// readContext is ReadContext; the view may share memory with storage, so callers must not modify it.
func readContext(ctx context.Context, session lineReader, storage durable.Storage, conversationId durable.ConversationId, at *durable.EntryId) (durable.ContextView, error) {
	bounds, err := captureOnLine(ctx, session, storage, conversationId, at)
	if err != nil {
		return durable.ContextView{}, err
	}
	return deriveContext(ctx, storage, conversationId, bounds)
}

func captureOnLine(ctx context.Context, session lineReader, storage durable.Storage, conversationId durable.ConversationId, at *durable.EntryId) (*ContextBounds, error) {
	captured, err := session.ReadOnLine(ctx, func() (any, error) {
		return CaptureContextBounds(ctx, storage, conversationId, at)
	})
	if err != nil {
		return nil, err
	}
	return captured.(*ContextBounds), nil
}

// contextRange is one context read: the visible entries it scanned, from the head marker's head, or transcript start,
// through bounds.Tail, oldest first, and the view derived from them. Entries at or below the tail never change, so a
// later read with the same head marker extends both. A range outlives the read and is shared by the views derived from
// it, so nothing modifies it after it is built.
type contextRange struct {
	bounds  ContextBounds
	entries []durable.EntryRecord
	view    durable.ContextView
	// edited holds the targets of the edits in entries.
	edited map[durable.EntryId]struct{}
	// settled is view.Messages from the contributed messages before the last assistant message. Tool results are
	// ordered within the messages up to the next assistant message, so later entries cannot change these.
	settled []ai.Message
	// open is the contributed messages from the last assistant message on, before tool result ordering.
	open []ai.Message
	// baseline is the baseline's system message; see findBaseline.
	baseline baseline
	// firstSystem is the id of the first active entry that contributes a system message, 0 when none does.
	firstSystem durable.EntryId
}

// baseline is the harness baseline among a range's entries. resolved is false while a later entry may be one; id is
// the baseline entry, 0 when the range cannot have one, e.g. its first pi.system entry has no system message.
type baseline struct {
	resolved bool
	id       durable.EntryId
}

// readContextFrom is readContext reusing previous, an earlier range of the same conversation: with the same head
// marker, only entries after its tail are scanned. It returns the view and the range to pass to the next read; the
// range is nil when the conversation has no entry. The view has its own arrays but shares the entries and messages
// with the range, so callers must not modify them.
func readContextFrom(ctx context.Context, session lineReader, storage durable.Storage, conversationId durable.ConversationId, at *durable.EntryId, previous *contextRange) (durable.ContextView, *contextRange, error) {
	bounds, err := captureOnLine(ctx, session, storage, conversationId, at)
	if err != nil {
		return durable.ContextView{}, nil, err
	}
	if bounds == nil {
		return emptyView(), nil, nil
	}
	var next *contextRange
	switch {
	case previous == nil || !sameHead(previous.bounds.Head, bounds.Head):
		scanned, err := scanRange(ctx, storage, conversationId, bounds)
		if err != nil {
			return durable.ContextView{}, nil, err
		}
		next = deriveRange(*bounds, scanned)
	case bounds.Tail == previous.bounds.Tail:
		next = previous
	case bounds.Tail < previous.bounds.Tail:
		next = deriveRange(*bounds, slices.DeleteFunc(slices.Clone(previous.entries), func(entry durable.EntryRecord) bool { return entry.Id > bounds.Tail }))
	default:
		added, err := scanEntries(ctx, storage, conversationId, durable.EntryQuery{MinEntryId: new(previous.bounds.Tail + 1), MaxEntryId: new(bounds.Tail)})
		if err != nil {
			return durable.ContextView{}, nil, err
		}
		next = extendRange(previous, *bounds, added)
	}
	view := next.view
	view.Entries = slices.Clone(view.Entries)
	view.Contributions = slices.Clone(view.Contributions)
	view.Messages = slices.Clone(view.Messages)
	return view, next, nil
}

func sameHead(a, b *durable.EntryRecord) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Id == b.Id
}

func emptyView() durable.ContextView {
	return durable.ContextView{Entries: []durable.EntryRecord{}, Contributions: [][]ai.Message{}, Messages: []ai.Message{}}
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
		return emptyView(), nil
	}
	scanned, err := scanRange(ctx, storage, conversationId, bounds)
	if err != nil {
		return durable.ContextView{}, err
	}
	return deriveRange(*bounds, scanned).view, nil
}

// deriveRange derives the context view from the entries scanned within bounds.
func deriveRange(bounds ContextBounds, scanned []durable.EntryRecord) *contextRange {
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
	for index := range entries {
		edit, edited := edits[entries[index].Id]
		kept := contribute(&entries[index], edit, edited)
		contributions = append(contributions, kept)
		flat = append(flat, kept...)
	}
	settled, open := settle(nil, flat)
	edited := make(map[durable.EntryId]struct{}, len(edits))
	for target := range edits {
		edited[target] = struct{}{}
	}
	view := durable.ContextView{Head: bounds.Head, Entries: entries, Contributions: contributions, Messages: append(append([]ai.Message{}, settled...), OrderToolResults(open)...)}
	base := findBaseline(bounds.Head, entries)
	first := firstSystemEntry(entries, contributions)
	view.Messages = leadWithSystem(view.Messages, leadsBaseline(base, first, edited))
	return &contextRange{bounds: bounds, entries: scanned, view: view, edited: edited, settled: settled, open: open, baseline: base, firstSystem: first}
}

// extendRange is previous extended by the entries added after its tail under the same head marker: only their
// contributions and the open messages are derived. An added edit can change an earlier entry, so it derives the whole
// range again.
func extendRange(previous *contextRange, bounds ContextBounds, added []durable.EntryRecord) *contextRange {
	entries := slices.Concat(previous.entries, added)
	for index := range added {
		if _, edited := previous.edited[added[index].Id]; edited || len(added[index].Edits) > 0 || added[index].Head != nil {
			return deriveRange(bounds, entries)
		}
	}
	contributions := make([][]ai.Message, 0, len(added))
	flat := slices.Clone(previous.open)
	for index := range added {
		kept := contribute(&added[index], durable.ContextEdit{}, false)
		contributions = append(contributions, kept)
		flat = append(flat, kept...)
	}
	settled, open := settle(previous.settled, flat)
	base := previous.baseline
	if !base.resolved {
		base = findBaseline(bounds.Head, added)
	}
	first := previous.firstSystem
	if first == 0 {
		first = firstSystemEntry(added, contributions)
	}
	view := durable.ContextView{
		Head:          bounds.Head,
		Entries:       slices.Concat(previous.view.Entries, added),
		Contributions: slices.Concat(previous.view.Contributions, contributions),
		Messages:      leadWithSystem(append(append([]ai.Message{}, settled...), OrderToolResults(open)...), leadsBaseline(base, first, previous.edited)),
	}
	return &contextRange{bounds: bounds, entries: entries, view: view, edited: previous.edited, settled: settled, open: open, baseline: base, firstSystem: first}
}

// contribute is one active entry's model messages after its edit and the excluded stop reasons, before tool result
// ordering.
func contribute(entry *durable.EntryRecord, edit durable.ContextEdit, edited bool) []ai.Message {
	if edited && edit.Action == durable.EditOmit {
		return []ai.Message{}
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
	return kept
}

// settle moves the ordered messages before the last assistant message of open to settled. OrderToolResults of a
// sequence equals the concatenation over its parts when each later part starts with an assistant message.
func settle(settled, open []ai.Message) ([]ai.Message, []ai.Message) {
	last := -1
	for index, o := range slices.Backward(open) {
		if _, ok := o.(ai.AssistantMessage); ok {
			last = index
			break
		}
	}
	if last <= 0 {
		return settled, open
	}
	return slices.Concat(settled, OrderToolResults(open[:last])), slices.Clone(open[last:])
}

// findBaseline finds the baseline among entries, oldest first: the first pi.system entry after the head marker, or of
// the transcript without one, when generation wrote it with every section and tool in effect. After a head marker it
// always does. Without one it does when it saw no system message before, which holds unless an earlier entry carries
// one, even if an edit omits it later. An edit that targets the baseline contributes other messages, or none, so none
// leads; a later entry does not take its place.
func findBaseline(head *durable.EntryRecord, entries []durable.EntryRecord) baseline {
	for index := range entries {
		entry := &entries[index]
		if head != nil && entry.Id <= head.Id {
			continue
		}
		if entry.Kind == "pi.system" {
			for _, message := range entry.Model {
				if _, ok := message.(ai.SystemMessage); ok {
					return baseline{resolved: true, id: entry.Id}
				}
			}
			return baseline{resolved: true}
		}
		if head == nil && carriesSystemMessage(entry) {
			return baseline{resolved: true}
		}
	}
	return baseline{}
}

func isSystemMessage(message ai.Message) bool {
	_, ok := message.(ai.SystemMessage)
	return ok
}

// carriesSystemMessage reports whether an entry's own messages, or the replacement messages of its edits, include a
// system message.
func carriesSystemMessage(entry *durable.EntryRecord) bool {
	if slices.ContainsFunc(entry.Model, isSystemMessage) {
		return true
	}
	return slices.ContainsFunc(entry.Edits, func(edit durable.ContextEdit) bool {
		return edit.Action == durable.EditReplace && slices.ContainsFunc(edit.Messages, isSystemMessage)
	})
}

// firstSystemEntry is the id of the first of entries whose contribution holds a system message, 0 when none does.
func firstSystemEntry(entries []durable.EntryRecord, contributions [][]ai.Message) durable.EntryId {
	for index := range entries {
		if slices.ContainsFunc(contributions[index], isSystemMessage) {
			return entries[index].Id
		}
	}
	return 0
}

// leadsBaseline reports whether the first system message of the context is the baseline's own, unedited message.
func leadsBaseline(base baseline, first durable.EntryId, edited map[durable.EntryId]struct{}) bool {
	if first == 0 || base.id != first {
		return false
	}
	_, isEdited := edited[first]
	return !isEdited
}

// leadWithSystem moves the first system message to the front when only user messages precede it, or when it is the
// baseline's. A run's input is committed before generation renders the system prompt, so a transcript starts with the
// input followed by the baseline, and the range after a compaction with the kept history, assistant messages included.
// Providers treat only a leading system message as the initial prompt and tool set; without it, a later tool change
// rewrites the request's tool list and invalidates the whole prompt cache. Only the first system message moves, so
// replaying system messages in order gives the same sections and tools. Any other system message after an assistant
// message stays where it was committed.
func leadWithSystem(messages []ai.Message, baseline bool) []ai.Message {
	index := slices.IndexFunc(messages, isSystemMessage)
	if index <= 0 {
		return messages
	}
	if !baseline && slices.ContainsFunc(messages[:index], func(message ai.Message) bool {
		_, user := message.(ai.UserMessage)
		return !user
	}) {
		return messages
	}
	led := make([]ai.Message, 0, len(messages))
	led = append(led, messages[index])
	led = append(led, messages[:index]...)
	return append(led, messages[index+1:]...)
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
	query := durable.EntryQuery{MaxEntryId: new(bounds.Tail)}
	if bounds.Head != nil && bounds.Head.Head != nil {
		query.MinEntryId = new(*bounds.Head.Head)
	}
	return scanEntries(ctx, storage, conversationId, query)
}

// scanEntries returns the visible entries within query's ID bounds, oldest first.
func scanEntries(ctx context.Context, storage durable.Storage, conversationId durable.ConversationId, query durable.EntryQuery) ([]durable.EntryRecord, error) {
	query.ConversationId = conversationId
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
