package harness

import (
	"context"
	"slices"
	"sort"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/internal/entryscan"
)

// contextCacheConversations bounds how many conversations keep a derived context.
const contextCacheConversations = 16

// contextCache keeps the derived context of each conversation's latest read and extends it by the entries committed
// since, so a read costs the new entries plus a copy of the model messages instead of a derivation over the whole range.
// It serves exactly what DeriveContext derives; DeriveContext is its oracle. Views share their arrays with the cache and
// are read-only.
//
// A read the cache cannot extend falls back to DeriveContext and, when the range allows it, rebuilds the cached context:
//   - a different head marker or range start, or a store that cannot count its range;
//   - a changed number of entries in the cached range (an entry committed out of ID order), or a changed range;
//   - edits in the entries added since, which change contributions already derived;
//   - a read cut off before the cached tail, while the cached range carries edits.
//
// pig additive (D104): Pi derives the context over the whole range on every read; PiG extends the previous derivation.
type contextCache struct {
	mu       sync.Mutex
	contexts map[durable.ConversationId]*derivedContext
	used     []durable.ConversationId
}

func newContextCache() *contextCache {
	return &contextCache{contexts: map[durable.ConversationId]*derivedContext{}}
}

// derivedContext is the context of one conversation over [from, tail] for one head marker.
type derivedContext struct {
	head     *durable.EntryRecord
	from     int64
	tail     durable.EntryId
	count    int
	edits    map[durable.EntryId]durable.ContextEdit
	active   []durable.EntryRecord
	contrib  [][]ai.Message
	flat     []ai.Message
	flatEnd  []int
	assist   []int
	stable   []ai.Message
	stableAt []int
}

func (cache *contextCache) touch(id durable.ConversationId) {
	if index := slices.Index(cache.used, id); index >= 0 {
		cache.used = slices.Delete(cache.used, index, index+1)
	}
	cache.used = append(cache.used, id)
	for len(cache.used) > contextCacheConversations {
		delete(cache.contexts, cache.used[0])
		cache.used = cache.used[1:]
	}
}

// drop forgets the derived context of one conversation.
func (cache *contextCache) drop(id durable.ConversationId) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	delete(cache.contexts, id)
	if index := slices.Index(cache.used, id); index >= 0 {
		cache.used = slices.Delete(cache.used, index, index+1)
	}
}

// clear forgets every derived context.
func (cache *contextCache) clear() {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	clear(cache.contexts)
	cache.used = nil
}

// has reports whether a derived context of the conversation is kept.
func (cache *contextCache) has(id durable.ConversationId) bool {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.contexts[id] != nil
}

func rangeStart(head *durable.EntryRecord) int64 {
	if head != nil && head.Head != nil {
		return int64(*head.Head)
	}
	return -1 << 63
}

// derive returns what DeriveContext returns for bounds; the view shares memory with the cache and storage.
func (cache *contextCache) derive(ctx context.Context, storage durable.Storage, conversationId durable.ConversationId, bounds *ContextBounds) (durable.ContextView, error) {
	ranger, ok := storage.(entryscan.Ranger)
	if bounds == nil || !ok {
		return deriveContext(ctx, storage, conversationId, bounds)
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	from := rangeStart(bounds.Head)
	query := func(low, high int64) durable.EntryQuery {
		query := durable.EntryQuery{ConversationId: conversationId, MaxEntryId: new(durable.EntryId(high))}
		if low > -1<<63 {
			query.MinEntryId = new(durable.EntryId(low))
		}
		return query
	}
	current := cache.contexts[conversationId]
	if current != nil && sameHead(current.head, bounds.Head) && current.from == from {
		count, err := ranger.CountVisibleEntries(ctx, query(from, int64(current.tail)))
		if err != nil {
			return durable.ContextView{}, err
		}
		if count == current.count {
			switch {
			case bounds.Tail == current.tail:
				cache.touch(conversationId)
				return current.view(bounds.Head, len(current.active)), nil
			case bounds.Tail > current.tail:
				added, err := ranger.VisibleEntryRange(ctx, query(int64(current.tail)+1, int64(bounds.Tail)))
				if err != nil {
					return durable.ContextView{}, err
				}
				if current.extend(added) {
					current.tail = bounds.Tail
					cache.touch(conversationId)
					return current.view(bounds.Head, len(current.active)), nil
				}
			case len(current.edits) == 0:
				cache.touch(conversationId)
				return current.view(bounds.Head, current.activeUpTo(bounds.Tail)), nil
			default:
				return deriveContext(ctx, storage, conversationId, bounds)
			}
		}
	}
	if current != nil && bounds.Tail < current.tail {
		// A read cut off before the cached tail that the cache cannot answer must not replace a newer context.
		return deriveContext(ctx, storage, conversationId, bounds)
	}
	scanned, err := ranger.VisibleEntryRange(ctx, query(from, int64(bounds.Tail)))
	if err != nil {
		return durable.ContextView{}, err
	}
	rebuilt := newDerivedContext(bounds, from, scanned)
	cache.contexts[conversationId] = rebuilt
	cache.touch(conversationId)
	return rebuilt.view(bounds.Head, len(rebuilt.active)), nil
}

func sameHead(a, b *durable.EntryRecord) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Id == b.Id
}

// newDerivedContext derives the context of scanned, the visible entries of the range, oldest first.
func newDerivedContext(bounds *ContextBounds, from int64, scanned []durable.EntryRecord) *derivedContext {
	context := &derivedContext{head: bounds.Head, from: from, tail: bounds.Tail, count: len(scanned), edits: map[durable.EntryId]durable.ContextEdit{}}
	for index := range scanned {
		for _, edit := range scanned[index].Edits {
			context.edits[edit.Target] = edit
		}
	}
	if bounds.Head != nil {
		context.appendActive(*bounds.Head)
	}
	for index := range scanned {
		if bounds.Head == nil || scanned[index].Head == nil {
			context.appendActive(scanned[index])
		}
	}
	return context
}

// extend adds the entries committed after the tail. It reports false, leaving the context unchanged, when the entries
// carry edits or a head marker: either changes what is already derived.
func (context *derivedContext) extend(added []durable.EntryRecord) bool {
	for index := range added {
		if len(added[index].Edits) > 0 || added[index].Head != nil {
			return false
		}
	}
	for index := range added {
		context.appendActive(added[index])
	}
	context.count += len(added)
	return true
}

func (context *derivedContext) appendActive(entry durable.EntryRecord) {
	kept := contribution(&entry, context.edits)
	context.active = append(context.active, entry)
	context.contrib = append(context.contrib, kept)
	for _, message := range kept {
		if _, isAssistant := message.(ai.AssistantMessage); isAssistant {
			context.addAssistant(len(context.flat))
		}
		context.flat = append(context.flat, message)
	}
	context.flatEnd = append(context.flatEnd, len(context.flat))
}

// addAssistant records an assistant at flat[position] and settles the segment before it: tool results reach only the
// assistant before the next one, so everything up to a later assistant is final.
func (context *derivedContext) addAssistant(position int) {
	settled := 0
	if len(context.assist) > 0 {
		settled = context.assist[len(context.assist)-1]
	}
	context.stable = append(context.stable, OrderToolResults(context.flat[settled:position])...)
	context.stableAt = append(context.stableAt, len(context.stable))
	context.assist = append(context.assist, position)
}

// contribution is one entry's model messages after edits and excluded stop reasons.
func contribution(entry *durable.EntryRecord, edits map[durable.EntryId]durable.ContextEdit) []ai.Message {
	edit, edited := edits[entry.Id]
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

// activeUpTo is how many active entries have an ID at or below tail; the head marker always counts.
func (context *derivedContext) activeUpTo(tail durable.EntryId) int {
	start := 0
	if context.head != nil {
		start = 1
	}
	return start + sort.Search(len(context.active)-start, func(i int) bool { return context.active[start+i].Id > tail })
}

// view is the context of the first count active entries. Its slices are capped, so appending to one never writes into
// the cache.
func (context *derivedContext) view(head *durable.EntryRecord, count int) durable.ContextView {
	flatLen := 0
	if count > 0 {
		flatLen = context.flatEnd[count-1]
	}
	messages := make([]ai.Message, 0, flatLen)
	settled := 0
	if last := sort.Search(len(context.assist), func(i int) bool { return context.assist[i] >= flatLen }) - 1; last >= 0 {
		messages = append(messages, context.stable[:context.stableAt[last]]...)
		settled = context.assist[last]
	}
	messages = append(messages, OrderToolResults(context.flat[settled:flatLen])...)
	messages = leadWithSystem(messages)
	return durable.ContextView{
		Head:          head,
		Entries:       context.active[:count:count],
		Contributions: context.contrib[:count:count],
		Messages:      messages,
	}
}
