// Ports packages/durable/src/harness/transcript.ts.

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"sync"

	"github.com/MichaelKinsy/PiG/durable"
)

// TranscriptItem is one row of a client's transcript: a TranscriptEntry, a TranscriptGap, or a TranscriptEarlier. Kind is the
// discriminator of Pi's TranscriptItem union ("entry", "gap" or "earlier"), which each variant also writes as its JSON "kind".
type TranscriptItem interface {
	transcriptItem()
	Kind() string
}

// TranscriptEntry is an entry the client has.
type TranscriptEntry struct{ Entry durable.EntryRecord }

// TranscriptGap says the entries strictly between After and UpTo are missing: render "load more" and call FillGap.
type TranscriptGap struct{ After, UpTo durable.EntryId }

// TranscriptEarlier says the transcript continues above: render "load earlier" and call LoadEarlier. It is always the first
// item.
type TranscriptEarlier struct{}

func (TranscriptEntry) transcriptItem()   {}
func (TranscriptGap) transcriptItem()     {}
func (TranscriptEarlier) transcriptItem() {}

// Kind is "entry".
func (TranscriptEntry) Kind() string { return "entry" }

// Kind is "gap".
func (TranscriptGap) Kind() string { return "gap" }

// Kind is "earlier".
func (TranscriptEarlier) Kind() string { return "earlier" }

// MarshalJSON writes Pi's { kind: "entry", entry }.
func (item TranscriptEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct { //perf:ok one wire object per transcript item the client reads; the discriminator is Pi's { kind, entry } shape (transcript.ts), not a re-encode of a value the outer encoder compacts again
		Kind  string              `json:"kind"`
		Entry durable.EntryRecord `json:"entry"`
	}{item.Kind(), item.Entry})
}

// MarshalJSON writes Pi's { kind: "gap", after, upTo }.
func (item TranscriptGap) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct { //perf:ok one two-id wire object per gap of a transcript; Pi's { kind, after, upTo } shape
		Kind  string          `json:"kind"`
		After durable.EntryId `json:"after"`
		UpTo  durable.EntryId `json:"upTo"`
	}{item.Kind(), item.After, item.UpTo})
}

// MarshalJSON writes Pi's { kind: "earlier" }.
func (item TranscriptEarlier) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct { //perf:ok a one-field wire object, at most once per transcript; Pi's { kind: "earlier" } shape
		Kind string `json:"kind"`
	}{item.Kind()})
}

// TranscriptView is the conversation view as a renderer sees it: Items, the client's whole transcript, instead of the window.
type TranscriptView struct {
	Conversation durable.ConversationRecord `json:"conversation"`
	Docs         ViewDocs                   `json:"docs"`
	Items        []TranscriptItem           `json:"items"`
}

// EntryLoader pages a conversation's entries, as Conversation.Entries does without a cursor; query.ConversationId is
// ignored.
type EntryLoader func(ctx context.Context, query durable.EntryQuery, limit int) (durable.Page[durable.EntryRecord, durable.Cursor], error)

// ConversationTranscriptOptions are the options of NewConversationTranscript.
type ConversationTranscriptOptions struct {
	// PageSize is the number of entries LoadEarlier and FillGap load per call; default 50.
	PageSize *int
}

// transcriptLoad is a load that concurrent callers share: they wait for it and take its result.
type transcriptLoad struct {
	done chan struct{}
	view *TranscriptView
	err  error
}

// wait returns the load's result, or ctx's cause when the caller gives up first.
func (load *transcriptLoad) wait(ctx context.Context) (*TranscriptView, error) {
	select {
	case <-load.done:
		return load.view, load.err
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// ConversationTranscript is what a client has of one conversation: every entry it got from a view or fetched, and the gaps
// between (spec §9.3). A layer over any ConversationView, wherever it comes from: call Merge with every value of the view; it
// returns the view with the client's whole transcript as Items instead of the window. Entries never change once committed, so
// the transcript only grows; a window that dropped its oldest entries keeps them here.
//
// A window whose Before lies past the newest entry the client has does not continue it, after a stall, an overflow, or a
// reconnect: the entries in between become a gap. Nothing counts entries or computes ranges from entry IDs, which have holes;
// only exclusive bounds are turned into the inclusive bounds of EntryQuery. After a drop, a new watch with Resume as its
// window continues the transcript.
//
// A merge costs O(items) and never re-sorts or re-fetches; a merge, LoadEarlier, or FillGap that adds nothing returns the
// previous view, whose Items are the previous items. Concurrent calls of LoadEarlier, or of FillGap for the same gap, share
// one load, so a placeholder that stays visible does not fetch twice. It is safe for concurrent use.
type ConversationTranscript struct {
	load     EntryLoader
	pageSize int

	mu sync.Mutex
	// entries holds every entry, in ID order, as items.
	entries []TranscriptEntry
	ids     map[durable.EntryId]struct{}
	// gaps are in ID order; each is an immutable item, replaced when it shrinks.
	gaps []TranscriptGap
	// started is whether anything was merged yet.
	started bool
	// low is the exclusive lower bound of the known range: entries at or below it are not loaded; -Inf at the start.
	low float64
	// tail is the inclusive upper bound of the known range: the newest entry, or a later empty window's Before.
	tail float64
	// items are the rows; itemsRevision changes with every assignment, so a result is reused only for the same items.
	items         []TranscriptItem
	itemsRevision uint64
	// last is the latest merged view's conversation and documents.
	last     *transcriptSource
	view     *TranscriptView
	viewItem uint64
	earlier  *transcriptLoad
	filling  map[string]*transcriptLoad
}

type transcriptSource struct {
	conversation durable.ConversationRecord
	docs         ViewDocs
}

// NewConversationTranscript returns an empty transcript that loads through load; it fails for a page size below 1.
func NewConversationTranscript(load EntryLoader, options *ConversationTranscriptOptions) (*ConversationTranscript, error) {
	pageSize := 50
	if options != nil && options.PageSize != nil {
		pageSize = *options.PageSize
	}
	if pageSize < 1 {
		return nil, fmt.Errorf("Page size must be an integer >= 1, got %d", pageSize)
	}
	return &ConversationTranscript{
		load:     load,
		pageSize: pageSize,
		ids:      map[durable.EntryId]struct{}{},
		low:      math.Inf(-1),
		tail:     math.Inf(-1),
		filling:  map[string]*transcriptLoad{},
	}, nil
}

// Newest is the ID the transcript reaches: its newest entry, or the Before of a later window that was empty. It reports false
// before the first merge and for an empty transcript.
func (transcript *ConversationTranscript) Newest() (durable.EntryId, bool) {
	transcript.mu.Lock()
	defer transcript.mu.Unlock()
	return transcript.newest()
}

func (transcript *ConversationTranscript) newest() (durable.EntryId, bool) {
	if math.IsInf(transcript.tail, -1) {
		return 0, false
	}
	return durable.EntryId(transcript.tail), true
}

// Resume is the window that resumes this transcript after a drop: From right after Newest, so a new watch returns only what was
// missed when it fits its window, otherwise its newest entries and a gap; an empty window for an empty transcript; nil before the
// first merge. conversation.Watch(ctx, transcript.Resume()).
func (transcript *ConversationTranscript) Resume() *ConversationWindow {
	transcript.mu.Lock()
	defer transcript.mu.Unlock()
	if !transcript.started {
		return nil
	}
	newest, present := transcript.newest()
	if !present {
		return &ConversationWindow{}
	}
	return &ConversationWindow{From: new(newest + 1)}
}

// Merge takes one value of the view: its window's entries join the transcript.
func (transcript *ConversationTranscript) Merge(view ConversationView) *TranscriptView {
	transcript.mu.Lock()
	defer transcript.mu.Unlock()
	transcript.last = &transcriptSource{conversation: view.Conversation, docs: view.Docs}
	hi := math.Inf(-1)
	if len(view.Entries) > 0 {
		hi = float64(view.Entries[len(view.Entries)-1].Id)
	} else if view.Before != nil {
		hi = float64(*view.Before)
	}
	lo := math.Inf(-1)
	if view.Before != nil {
		lo = float64(*view.Before)
	}
	transcript.cover(lo, hi, view.Entries)
	return transcript.result()
}

var errNothingMerged = errors.New("ConversationTranscript: merge a view first")

// LoadEarlier loads the page before the oldest entry held. Without anything earlier, it returns the current view.
func (transcript *ConversationTranscript) LoadEarlier(ctx context.Context) (*TranscriptView, error) {
	transcript.mu.Lock()
	if transcript.last == nil {
		transcript.mu.Unlock()
		return nil, errNothingMerged
	}
	if math.IsInf(transcript.low, -1) {
		view := transcript.result()
		transcript.mu.Unlock()
		return view, nil
	}
	if running := transcript.earlier; running != nil {
		transcript.mu.Unlock()
		return running.wait(ctx)
	}
	low := transcript.low
	loading := &transcriptLoad{done: make(chan struct{})}
	transcript.earlier = loading
	transcript.mu.Unlock()

	query := durable.EntryQuery{MaxEntryId: new(durable.EntryId(low)), Order: new(durable.ScanDescending)}
	page, err := transcript.load(ctx, query, transcript.pageSize)
	transcript.mu.Lock()
	if err == nil {
		items := slices.Clone(page.Items)
		slices.Reverse(items)
		// No next page, or an empty one: the transcript starts here.
		if page.Next == nil || len(items) == 0 {
			transcript.cover(math.Inf(-1), low, items)
		} else {
			transcript.cover(float64(items[0].Id)-1, low, items)
		}
		loading.view = transcript.result()
	}
	loading.err = err
	if transcript.earlier == loading {
		transcript.earlier = nil
	}
	transcript.mu.Unlock()
	close(loading.done)
	return loading.view, loading.err
}

// FillGap loads the next page of gap, a gap item of the view's Items, shrinking it from its start, or removing it once nothing is
// missing. A gap that has shrunk since loads the gap it became; one that is no longer there returns the current view.
func (transcript *ConversationTranscript) FillGap(ctx context.Context, gap TranscriptGap) (*TranscriptView, error) {
	transcript.mu.Lock()
	current, found := transcript.currentGap(gap)
	if !found {
		if transcript.last == nil {
			transcript.mu.Unlock()
			return nil, errNothingMerged
		}
		view := transcript.result()
		transcript.mu.Unlock()
		return view, nil
	}
	// Keyed by the gap the call resolved to, so a stale descriptor and the current one share the load.
	key := strconv.FormatInt(int64(current.After), 10) + ":" + strconv.FormatInt(int64(current.UpTo), 10)
	if running := transcript.filling[key]; running != nil {
		transcript.mu.Unlock()
		return running.wait(ctx)
	}
	filling := &transcriptLoad{done: make(chan struct{})}
	transcript.filling[key] = filling
	transcript.mu.Unlock()

	query := durable.EntryQuery{
		MinEntryId: new(current.After + 1),
		MaxEntryId: new(current.UpTo - 1),
		Order:      new(durable.ScanAscending),
	}
	page, err := transcript.load(ctx, query, transcript.pageSize)
	transcript.mu.Lock()
	if err == nil {
		// No next page, or an empty one: nothing else is missing.
		if page.Next == nil || len(page.Items) == 0 {
			transcript.cover(float64(current.After), float64(current.UpTo-1), page.Items)
		} else {
			transcript.cover(float64(current.After), float64(page.Items[len(page.Items)-1].Id), page.Items)
		}
		filling.view = transcript.result()
	}
	filling.err = err
	if transcript.filling[key] == filling {
		delete(transcript.filling, key)
	}
	transcript.mu.Unlock()
	close(filling.done)
	return filling.view, filling.err
}

// currentGap is gap itself when it is still there, else the gap it shrank into, which overlaps it.
func (transcript *ConversationTranscript) currentGap(gap TranscriptGap) (TranscriptGap, bool) {
	if index := slices.Index(transcript.gaps, gap); index >= 0 {
		return transcript.gaps[index], true
	}
	for _, each := range transcript.gaps {
		if each.After < gap.UpTo && gap.After < each.UpTo {
			return each, true
		}
	}
	return TranscriptGap{}, false
}

// cover records that every entry with an ID in (lo, hi] is known, added being those entries in ID order: it holds the new ones,
// shrinks the gaps they fill, extends the known range, and opens a gap where the range does not continue it.
func (transcript *ConversationTranscript) cover(lo, hi float64, added []durable.EntryRecord) {
	var fresh []durable.EntryRecord
	for _, entry := range added {
		if _, known := transcript.ids[entry.Id]; !known {
			fresh = append(fresh, entry)
		}
	}
	for _, entry := range fresh {
		transcript.ids[entry.Id] = struct{}{}
	}
	held := transcript.entries
	// New entries past every held one: the items grow at their end.
	appended := len(fresh) == 0 || len(held) == 0 || fresh[0].Id > held[len(held)-1].Entry.Id
	items := make([]TranscriptEntry, len(fresh))
	for index, entry := range fresh {
		items[index] = TranscriptEntry{Entry: entry}
	}
	if len(items) > 0 {
		if appended {
			transcript.entries = slices.Concat(held, items)
		} else {
			transcript.entries = mergeById(held, items)
		}
	}
	rebuild := false
	switch {
	case !transcript.started || (len(held) == 0 && len(transcript.gaps) == 0 && lo >= transcript.tail):
		// The first range, or one after an empty transcript: it is all the client knows.
		rebuild = !transcript.started || lo != transcript.low
		transcript.started = true
		transcript.low = lo
		transcript.tail = math.Max(transcript.tail, hi)
	case lo > transcript.tail:
		// Past the newest known ID: what lies between is missing.
		upTo := lo + 1
		if len(added) > 0 {
			upTo = float64(added[0].Id)
		}
		transcript.gaps = append(slices.Clone(transcript.gaps), gapOf(transcript.tail, upTo))
		transcript.tail = hi
		rebuild = true
	case !math.IsInf(hi, -1) && hi < transcript.low:
		// Below the oldest known ID: what lies between is missing.
		upTo := transcript.low + 1
		if index := slices.IndexFunc(held, func(item TranscriptEntry) bool { return float64(item.Entry.Id) > hi }); index >= 0 {
			upTo = float64(held[index].Entry.Id)
		}
		transcript.gaps = slices.Concat([]TranscriptGap{gapOf(hi, upTo)}, transcript.gaps)
		transcript.low = lo
		rebuild = true
	default:
		if gaps, changed := transcript.subtract(lo, hi); changed {
			transcript.gaps = gaps
			rebuild = true
		}
		if lo < transcript.low {
			transcript.low = lo
			rebuild = true
		}
		if hi > transcript.tail {
			transcript.tail = hi
		}
	}
	switch {
	case rebuild || !appended:
		transcript.items = transcript.build()
		transcript.itemsRevision++
	case len(items) > 0:
		grown := make([]TranscriptItem, 0, len(transcript.items)+len(items))
		grown = append(grown, transcript.items...)
		for _, item := range items {
			grown = append(grown, item)
		}
		transcript.items = grown
		transcript.itemsRevision++
	}
}

// subtract returns the gaps without (lo, hi] and whether any changed, as when the range is empty it is not.
func (transcript *ConversationTranscript) subtract(lo, hi float64) ([]TranscriptGap, bool) {
	if hi <= lo {
		return transcript.gaps, false
	}
	changed := false
	var gaps []TranscriptGap
	for _, gap := range transcript.gaps {
		// Missing: IDs in (after, upTo); known now: IDs in (lo, hi].
		if hi <= float64(gap.After) || lo+1 >= float64(gap.UpTo) {
			gaps = append(gaps, gap)
			continue
		}
		changed = true
		if lo > float64(gap.After) {
			firstKnown := lo + 1
			if index := slices.IndexFunc(transcript.entries, func(item TranscriptEntry) bool { return float64(item.Entry.Id) > lo }); index >= 0 {
				firstKnown = float64(transcript.entries[index].Entry.Id)
			}
			gaps = append(gaps, gapOf(float64(gap.After), math.Min(float64(gap.UpTo), firstKnown)))
		}
		if hi+1 < float64(gap.UpTo) {
			gaps = append(gaps, gapOf(hi, float64(gap.UpTo)))
		}
	}
	if !changed {
		return transcript.gaps, false
	}
	return gaps, true
}

// build returns the items in ID order: "earlier" until the start is known, then entries with each gap before the entry after it.
func (transcript *ConversationTranscript) build() []TranscriptItem {
	items := make([]TranscriptItem, 0, len(transcript.entries)+len(transcript.gaps)+1)
	if !math.IsInf(transcript.low, -1) {
		items = append(items, TranscriptEarlier{})
	}
	next := 0
	for _, item := range transcript.entries {
		for next < len(transcript.gaps) && transcript.gaps[next].After < item.Entry.Id {
			items = append(items, transcript.gaps[next])
			next++
		}
		items = append(items, item)
	}
	for ; next < len(transcript.gaps); next++ {
		items = append(items, transcript.gaps[next])
	}
	return items
}

// result returns the view with the current items; the previous one when nothing changed.
func (transcript *ConversationTranscript) result() *TranscriptView {
	last := transcript.last
	previous := transcript.view
	if previous != nil && transcript.viewItem == transcript.itemsRevision && previous.Docs == last.docs && previous.Conversation == last.conversation {
		return previous
	}
	transcript.view = &TranscriptView{Conversation: last.conversation, Docs: last.docs, Items: transcript.items}
	transcript.viewItem = transcript.itemsRevision
	return transcript.view
}

func gapOf(after, upTo float64) TranscriptGap {
	return TranscriptGap{After: durable.EntryId(after), UpTo: durable.EntryId(upTo)}
}

// mergeById merges two ID-ordered item lists in one pass.
func mergeById(left, right []TranscriptEntry) []TranscriptEntry {
	merged := make([]TranscriptEntry, 0, len(left)+len(right))
	i, j := 0, 0
	for i < len(left) && j < len(right) {
		if left[i].Entry.Id < right[j].Entry.Id {
			merged = append(merged, left[i])
			i++
		} else {
			merged = append(merged, right[j])
			j++
		}
	}
	merged = append(merged, left[i:]...)
	return append(merged, right[j:]...)
}
