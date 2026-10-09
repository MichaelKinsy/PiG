package sqlite

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"sync"

	"github.com/MichaelKinsy/PiG/durable"
)

// DefaultWorkingSetBytes is the default bound on the decoded entries a SqliteStorage retains.
const DefaultWorkingSetBytes = 64 << 20

// decodedSizeFactor estimates the decoded size of an entry as a multiple of its stored JSON text.
const decodedSizeFactor = 4

// noEntries is the floor of a window that holds nothing yet.
const noEntries = math.MaxInt64

// anyEntry is the lower bound of a query without MinEntryId.
const anyEntry = math.MinInt64

// pig additive (D104): Pi re-reads and re-decodes the stored transcript for every context read; PiG retains decoded entries.
// retainedWindow holds the newest entries of one conversation: every stored entry of the conversation with an ID at or
// above floor is in records, oldest first. Entries are immutable once committed, so a window only grows at its new end,
// grows or loses entries at its old end, or is replaced; a slice handed to a reader is never written again.
type retainedWindow struct {
	floor   int64
	records []durable.EntryRecord
	sizes   []int64
	bytes   int64
	used    uint64
}

// workingSet is the bounded set of decoded entries a SqliteStorage serves context reads from. Commit writes through
// it; reads fill it from the entries table. A change another connection makes to the database file resets it.
//
// mu orders the set before the database: Commit and the reads take it first and then call the database.
type workingSet struct {
	mu            sync.Mutex
	budget        int64
	bytes         int64
	tick          uint64
	version       int64
	versionKnown  bool
	windows       map[durable.ConversationId]*retainedWindow
	conversations map[durable.ConversationId]*durable.ConversationRecord
}

func newWorkingSet(budget int64) *workingSet {
	return &workingSet{
		budget:        budget,
		windows:       map[durable.ConversationId]*retainedWindow{},
		conversations: map[durable.ConversationId]*durable.ConversationRecord{},
	}
}

// reset drops everything retained. The caller holds mu.
func (set *workingSet) reset() {
	clear(set.windows)
	clear(set.conversations)
	set.bytes = 0
	set.versionKnown = false
}

// release drops everything retained.
func (set *workingSet) release() {
	set.mu.Lock()
	defer set.mu.Unlock()
	set.reset()
}

// checkVersion resets the set when another connection changed the database since the set was last consistent. The
// caller holds mu.
func (set *workingSet) checkVersion(db SqliteDatabase) error {
	row, err := db.Get("PRAGMA data_version")
	if err != nil {
		return err
	}
	version, err := rowInt(row, "data_version")
	if err != nil {
		return err
	}
	if set.versionKnown && set.version != version {
		set.reset()
	}
	set.version, set.versionKnown = version, true
	return nil
}

func (set *workingSet) conversation(db SqliteDatabase, id durable.ConversationId) (*durable.ConversationRecord, error) {
	if record := set.conversations[id]; record != nil {
		return record, nil
	}
	record, err := getRecord[durable.ConversationRecord](db, "SELECT record FROM conversations WHERE id = ?", int64(id))
	if err != nil || record == nil {
		return nil, err
	}
	set.conversations[id] = record
	return record, nil
}

type decodedEntry struct {
	record durable.EntryRecord
	size   int64
}

func entrySize(textBytes int) int64 { return int64(textBytes)*decodedSizeFactor + 256 }

func decodeEntry(text []byte) (decodedEntry, error) {
	// EntryRecord's decoder validates the text itself, so the generic decoder's extra scan is skipped.
	var record durable.EntryRecord
	if err := record.UnmarshalJSON(text); err != nil {
		return decodedEntry{}, err
	}
	return decodedEntry{record: record, size: entrySize(len(text))}, nil
}

// loadEntries decodes the entries of one conversation with from <= id <= to, oldest first.
func loadEntries(db SqliteDatabase, id durable.ConversationId, from, to int64) ([]decodedEntry, error) {
	const query = "SELECT record FROM entries WHERE conversation_id = ? AND id >= ? AND id <= ? ORDER BY id"
	if reader, ok := db.(TextReader); ok {
		var loaded []decodedEntry
		err := reader.EachText(query, []SqliteValue{int64(id), from, to}, func(text []byte) error {
			entry, err := decodeEntry(text)
			loaded = append(loaded, entry)
			return err
		})
		return loaded, err
	}
	rows, err := db.All(query, int64(id), from, to)
	if err != nil {
		return nil, err
	}
	loaded := make([]decodedEntry, len(rows))
	for index, row := range rows {
		text, err := rowText(row, "record")
		if err != nil {
			return nil, err
		}
		if loaded[index], err = decodeEntry([]byte(text)); err != nil {
			return nil, err
		}
	}
	return loaded, nil
}

// span returns the retained entries with from <= id <= to. The result is capped, so appending to it never writes into
// the window.
func (window *retainedWindow) span(from, to int64) []durable.EntryRecord {
	start := sort.Search(len(window.records), func(i int) bool { return int64(window.records[i].Id) >= from })
	end := sort.Search(len(window.records), func(i int) bool { return int64(window.records[i].Id) > to })
	if start >= end {
		return nil
	}
	return window.records[start:end:end]
}

// prepend adds entries older than every retained one.
func (set *workingSet) prepend(window *retainedWindow, older []decodedEntry) {
	records := make([]durable.EntryRecord, 0, len(older)+len(window.records))
	sizes := make([]int64, 0, len(older)+len(window.records))
	for _, entry := range older {
		records = append(records, entry.record)
		sizes = append(sizes, entry.size)
		window.bytes += entry.size
		set.bytes += entry.size
	}
	window.records = append(records, window.records...)
	window.sizes = append(sizes, window.sizes...)
}

// segment returns the entries of one conversation with from <= id <= to, oldest first. It extends the conversation's
// window down to from when the window does not reach that far; a range that lies wholly below a populated window is read
// without being retained.
func (set *workingSet) segment(db SqliteDatabase, id durable.ConversationId, from, to int64) ([]durable.EntryRecord, error) {
	window := set.windows[id]
	if window == nil {
		window = &retainedWindow{floor: noEntries}
		set.windows[id] = window
	}
	set.tick++
	window.used = set.tick
	if window.floor > from {
		if window.floor != noEntries && to < window.floor {
			loaded, err := loadEntries(db, id, from, to)
			if err != nil {
				return nil, err
			}
			records := make([]durable.EntryRecord, len(loaded))
			for index := range loaded {
				records[index] = loaded[index].record
			}
			return records, nil
		}
		loaded, err := loadEntries(db, id, from, window.floor-1)
		if err != nil {
			return nil, err
		}
		set.prepend(window, loaded)
		window.floor = from
	}
	return window.span(from, to), nil
}

// trim drops entries from the old end of the least recently used windows until the set fits its budget.
func (set *workingSet) trim() {
	for set.bytes > set.budget {
		var oldest *retainedWindow
		for _, window := range set.windows {
			if len(window.records) > 0 && (oldest == nil || window.used < oldest.used) {
				oldest = window
			}
		}
		if oldest == nil {
			return
		}
		drop := 0
		for drop < len(oldest.records) && set.bytes > set.budget {
			set.bytes -= oldest.sizes[drop]
			oldest.bytes -= oldest.sizes[drop]
			drop++
		}
		last := int64(oldest.records[drop-1].Id)
		if drop == len(oldest.records) {
			oldest.floor = last + 1
		} else {
			oldest.floor = int64(oldest.records[drop].Id)
		}
		oldest.records = slices.Clone(oldest.records[drop:])
		oldest.sizes = slices.Clone(oldest.sizes[drop:])
	}
}

// segments returns the retained segments of query's visible range, newest segment first. The caller holds mu.
func (set *workingSet) segments(db SqliteDatabase, query durable.EntryQuery) ([][]durable.EntryRecord, error) {
	if err := set.checkVersion(db); err != nil {
		return nil, err
	}
	from, upper := int64(anyEntry), int64(math.MaxInt64)
	if query.MinEntryId != nil {
		from = int64(*query.MinEntryId)
	}
	if query.MaxEntryId != nil {
		upper = int64(*query.MaxEntryId)
	}
	conversation, err := set.conversation(db, query.ConversationId)
	if err != nil {
		return nil, err
	}
	if conversation == nil {
		return nil, fmt.Errorf("Unknown conversation: %d", query.ConversationId)
	}
	// Segments run from the queried conversation up its ancestry, so they are newest first.
	var segments [][]durable.EntryRecord
	for {
		segment, err := set.segment(db, conversation.Id, from, upper)
		if err != nil {
			return nil, err
		}
		segments = append(segments, segment)
		if conversation.Parent == nil {
			break
		}
		upper = min(upper, int64(conversation.Parent.At))
		if upper < from {
			break
		}
		conversation, err = set.conversation(db, conversation.Parent.ConversationId)
		if err != nil {
			return nil, err
		}
		if conversation == nil {
			return nil, fmt.Errorf("Fork parent conversation is missing")
		}
	}
	set.trim()
	return segments, nil
}

// visibleRange is entryscan.Ranger for one conversation. The caller holds mu.
func (set *workingSet) visibleRange(db SqliteDatabase, query durable.EntryQuery) ([]durable.EntryRecord, error) {
	segments, err := set.segments(db, query)
	if err != nil {
		return nil, err
	}
	if len(segments) == 1 {
		return segments[0], nil
	}
	total := 0
	for _, segment := range segments {
		total += len(segment)
	}
	joined := make([]durable.EntryRecord, 0, total)
	for _, segment := range slices.Backward(segments) {
		joined = append(joined, segment...)
	}
	return joined, nil
}

// committedEntry is an entry a Commit stored: its conversation and its stored JSON text.
type committedEntry struct {
	conversationId durable.ConversationId
	text           string
}

// retainCommitted writes a Commit's entries through to the windows that exist. The caller holds mu. An entry that cannot
// be decoded resets the set, so the next read decodes from the table and reports the failure.
func (set *workingSet) retainCommitted(entries []committedEntry) {
	for _, committed := range entries {
		window := set.windows[committed.conversationId]
		if window == nil {
			continue
		}
		entry, err := decodeEntry([]byte(committed.text))
		if err != nil {
			set.reset()
			return
		}
		set.retainEntry(window, entry)
	}
	set.trim()
}

// retainEntry adds a committed entry to its conversation's window. An entry below the window's floor stays in the table
// only.
func (set *workingSet) retainEntry(window *retainedWindow, entry decodedEntry) {
	id := int64(entry.record.Id)
	if id < window.floor {
		return
	}
	window.bytes += entry.size
	set.bytes += entry.size
	end := len(window.records)
	if end == 0 || int64(window.records[end-1].Id) < id {
		window.records = append(window.records, entry.record)
		window.sizes = append(window.sizes, entry.size)
		return
	}
	position := sort.Search(end, func(i int) bool { return int64(window.records[i].Id) >= id })
	records := make([]durable.EntryRecord, 0, end+1)
	records = append(append(append(records, window.records[:position]...), entry.record), window.records[position:]...)
	sizes := make([]int64, 0, end+1)
	sizes = append(append(append(sizes, window.sizes[:position]...), entry.size), window.sizes[position:]...)
	window.records, window.sizes = records, sizes
}

// VisibleEntryRange returns the visible entries within query's bounds, oldest first, from the working set. The result is
// shared with the storage and read-only.
func (storage *SqliteStorage) VisibleEntryRange(_ context.Context, query durable.EntryQuery) ([]durable.EntryRecord, error) {
	var found []durable.EntryRecord
	err := storage.admitRead(func() error {
		storage.retained.mu.Lock()
		defer storage.retained.mu.Unlock()
		var err error
		found, err = storage.retained.visibleRange(storage.db, query)
		return err
	})
	return found, err
}

// CountVisibleEntries returns how many entries VisibleEntryRange returns for query.
func (storage *SqliteStorage) CountVisibleEntries(_ context.Context, query durable.EntryQuery) (int, error) {
	count := 0
	err := storage.admitRead(func() error {
		storage.retained.mu.Lock()
		defer storage.retained.mu.Unlock()
		segments, err := storage.retained.segments(storage.db, query)
		for _, segment := range segments {
			count += len(segment)
		}
		return err
	})
	return count, err
}
