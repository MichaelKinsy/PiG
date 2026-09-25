package session

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/agent/harness/utils"
)

type storedList struct {
	address  StoredAddressBase
	elements []ListElement[any]
}

func physicalKey(namespace, key string) string { return namespace + "\x00" + key }

// InMemoryStorageState is the complete materialized session state shared by
// MemoryStorage and JSONL storage. It is unsuitable for database backends and
// long sessions that may not fit in memory. It is not safe for concurrent use;
// its owner serializes access.
type InMemoryStorageState struct {
	entries      map[string]Entry
	entriesBySeq []Entry
	scalarValues map[string]StoredValue[any]
	listValues   map[string]*storedList
	usage        map[string]UsageRow
	stats        SessionStats
	nextSeq      int64
}

// NewInMemoryStorageState returns empty state whose next sequence is 1.
func NewInMemoryStorageState() *InMemoryStorageState {
	return &InMemoryStorageState{
		entries:      map[string]Entry{},
		scalarValues: map[string]StoredValue[any]{},
		listValues:   map[string]*storedList{},
		usage:        map[string]UsageRow{},
		stats:        SessionStats{Usage: utils.EmptyUsage()},
		nextSeq:      1,
	}
}

// PrepareCommit sequences and validates writes at timestamp without applying
// them.
func (state *InMemoryStorageState) PrepareCommit(writes []Write, timestamp int64) (PreparedCommit, error) {
	prepared, err := PrepareStorageCommit(writes, state.nextSeq, timestamp)
	if err != nil {
		return PreparedCommit{}, err
	}
	if err := state.ValidateCommitted(prepared.Writes); err != nil {
		return PreparedCommit{}, err
	}
	return prepared, nil
}

// ValidateCommitted validates sequenced writes against current state.
func (state *InMemoryStorageState) ValidateCommitted(writes []CommittedWrite) error {
	return ValidateCommittedWrites(writes, state.nextSeq, CommitValidationState{
		HasEntryOrUsageID: func(id string) bool {
			_, entry := state.entries[id]
			_, usage := state.usage[id]
			return entry || usage
		},
		HasEntryID: func(id string) bool {
			_, ok := state.entries[id]
			return ok
		},
	})
}

// ApplyValidated applies writes already accepted by ValidateCommitted and
// returns the post-apply totals.
func (state *InMemoryStorageState) ApplyValidated(writes []CommittedWrite) SessionStats {
	for _, write := range writes {
		switch value := write.(type) {
		case CommittedEntryWrite:
			state.entries[value.Entry.ID] = value.Entry
			state.entriesBySeq = append(state.entriesBySeq, value.Entry)
			if value.Entry.Type == EntryTypeMessage {
				state.stats.MessageCount++
			}
		case CommittedUsageWrite:
			state.usage[value.Row.ID] = value.Row
			state.stats.Usage = utils.AddUsage(state.stats.Usage, value.Row.Usage)
		case CommittedValueDeleteWrite:
			delete(state.scalarValues, physicalKey(value.Namespace, value.Key))
		case CommittedListDeleteWrite:
			delete(state.listValues, physicalKey(value.Namespace, value.Key))
		default:
			state.applyValueSetOrListAppend(write)
		}
		state.nextSeq = write.CommittedSeq() + 1
	}
	return state.stats
}

// CreateFork constructs destination state directly from this state.
func (state *InMemoryStorageState) CreateFork(options ForkOptions) (*InMemoryStorageState, error) {
	plan, entryIDs, err := state.selectForkPlan(options)
	if err != nil {
		return nil, err
	}
	isEntryCopied := func(entryID string) bool { return plan.Scope == ForkScopeTree || entryIDs[entryID] }
	destination := NewInMemoryStorageState()
	for _, entry := range state.entriesBySeq {
		if !isEntryCopied(entry.ID) {
			continue
		}
		destination.entries[entry.ID] = entry
		destination.entriesBySeq = append(destination.entriesBySeq, entry)
		if entry.Type == EntryTypeMessage {
			destination.stats.MessageCount++
		}
	}
	for _, write := range state.currentStateWrites() {
		projected, ok, err := ProjectForkCurrentStateWrite(write, plan, isEntryCopied)
		if err != nil {
			return nil, err
		}
		if ok {
			destination.applyValueSetOrListAppend(projected)
		}
	}
	destination.nextSeq = state.nextSeq
	return destination, nil
}

// currentStateWrites lists every current scalar row and surviving list
// element in address order, list elements in sequence order.
func (state *InMemoryStorageState) currentStateWrites() []CommittedWrite {
	var writes []CommittedWrite
	for _, key := range sortedKeys(state.scalarValues) {
		stored := state.scalarValues[key]
		writes = append(writes, CommittedValueSetWrite{Seq: stored.Seq, Namespace: stored.Address.Namespace, Key: stored.Address.Key, Value: stored.Value})
	}
	for _, key := range sortedKeys(state.listValues) {
		stored := state.listValues[key]
		for _, element := range stored.elements {
			writes = append(writes, CommittedListAppendWrite{Seq: element.Seq, Namespace: stored.address.Namespace, Key: stored.address.Key, Value: element.Value})
		}
	}
	return writes
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func (state *InMemoryStorageState) selectForkPlan(options ForkOptions) (ForkCurrentStatePlan, map[string]bool, error) {
	if options.Scope == ForkScopeTree {
		return ForkCurrentStatePlan{Scope: ForkScopeTree}, nil, nil
	}
	entryIDs := map[string]bool{}
	source := BranchForkSource{
		GetParent: func(entryID string) (*string, bool) {
			entry, ok := state.entries[entryID]
			return entry.ParentID, ok
		},
		SelectEntry: func(entryID string) { entryIDs[entryID] = true },
	}
	if tip := state.GetValue(BranchTip(options.Branch).StoredAddressBase); tip != nil {
		value, err := convertStored[*string](tip.Value)
		if err != nil {
			return ForkCurrentStatePlan{}, nil, err
		}
		source.Tip, source.HasTip = value, true
	}
	plan, err := SelectBranchFork(options, source)
	if err != nil {
		return ForkCurrentStatePlan{}, nil, err
	}
	if state.GetValue(LaneConfig(options.Branch).StoredAddressBase) == nil || state.GetValue(LaneStateValue(options.Branch).StoredAddressBase) == nil {
		return ForkCurrentStatePlan{}, nil, fmt.Errorf("Source branch %s is not a configured AgentLane", jsonQuote(options.Branch))
	}
	return plan, entryIDs, nil
}

func (state *InMemoryStorageState) applyValueSetOrListAppend(write CommittedWrite) {
	switch value := write.(type) {
	case CommittedValueSetWrite:
		state.scalarValues[physicalKey(value.Namespace, value.Key)] = StoredValue[any]{
			Address: MustValue[any](value.Namespace, value.Key), Value: value.Value, Seq: value.Seq,
		}
	case CommittedListAppendWrite:
		key := physicalKey(value.Namespace, value.Key)
		element := ListElement[any]{Seq: value.Seq, Value: value.Value}
		stored, ok := state.listValues[key]
		if !ok {
			state.listValues[key] = &storedList{address: MustList[any](value.Namespace, value.Key).StoredAddressBase, elements: []ListElement[any]{element}}
			return
		}
		stored.elements = append(stored.elements, element)
	}
}

// AdvanceNextSeq raises the sequence high-water mark.
func (state *InMemoryStorageState) AdvanceNextSeq(nextSeq int64) error {
	if nextSeq < 1 || nextSeq > maxSafeInteger {
		return fmt.Errorf("Invalid storage sequence high-water mark: %d", nextSeq)
	}
	state.nextSeq = max(state.nextSeq, nextSeq)
	return nil
}

// GetEntries returns the requested existing entries.
func (state *InMemoryStorageState) GetEntries(ids []string) map[string]Entry {
	found := map[string]Entry{}
	for _, id := range ids {
		if entry, ok := state.entries[id]; ok {
			found[id] = entry
		}
	}
	return found
}

// GetValue returns one current value, or nil.
func (state *InMemoryStorageState) GetValue(address StoredAddressBase) *StoredValue[any] {
	stored, ok := state.scalarValues[physicalKey(address.Namespace, address.Key)]
	if !ok {
		return nil
	}
	return &stored
}

// ScanValues returns the namespace's values whose key starts with the prefix
// key, in code-point key order.
func (state *InMemoryStorageState) ScanValues(prefix StoredAddressBase) []StoredValue[any] {
	matched := []StoredValue[any]{}
	for _, stored := range state.scalarValues {
		if stored.Address.Namespace == prefix.Namespace && strings.HasPrefix(stored.Address.Key, prefix.Key) {
			matched = append(matched, stored)
		}
	}
	slices.SortFunc(matched, func(left, right StoredValue[any]) int {
		return strings.Compare(left.Address.Key, right.Address.Key)
	})
	return matched
}

// ReadList returns one ordered page of a list.
func (state *InMemoryStorageState) ReadList(address StoredAddressBase, options *ListReadOptions) ([]ListElement[any], error) {
	resolved, err := ResolveListReadOptions(options)
	if err != nil {
		return nil, err
	}
	var elements []ListElement[any]
	if stored, ok := state.listValues[physicalKey(address.Namespace, address.Key)]; ok {
		elements = stored.elements
	}
	page := []ListElement[any]{}
	for index := range elements {
		element := elements[index]
		if resolved.Order == OrderDesc {
			element = elements[len(elements)-1-index]
		}
		if !listElementAfterCursor(element, resolved) {
			continue
		}
		if len(page) == resolved.Limit {
			break
		}
		page = append(page, element)
	}
	return page, nil
}

func listElementAfterCursor(element ListElement[any], options ResolvedListReadOptions) bool {
	if options.Cursor == nil {
		return true
	}
	if options.Order == OrderAsc {
		return element.Seq > options.Cursor.Seq
	}
	return element.Seq < options.Cursor.Seq
}

var errCorruptBranch = errors.New("Corrupt branch: missing parent")

// ScanBranch walks the path from query.Start toward the root, orders it,
// stops inclusively at the first stop match, filters, applies the exclusive
// cursor, then the limit.
func (state *InMemoryStorageState) ScanBranch(query StorageBranchScan) ([]Entry, error) {
	path, err := state.branchPath(query.Start)
	if err != nil {
		return nil, err
	}
	if query.Order == OrderOldestFirst {
		slices.Reverse(path)
	}
	result := []Entry{}
	for _, candidate := range path {
		if branchScanMatches(candidate, query) {
			result = append(result, candidate)
		}
		if candidate.ID == query.StopAtID || (query.StopAtType != "" && candidate.Type == query.StopAtType) {
			break
		}
	}
	if query.Limit != nil {
		result = result[:min(len(result), max(0, *query.Limit))]
	}
	return result, nil
}

func (state *InMemoryStorageState) branchPath(start string) ([]Entry, error) {
	entry, ok := state.entries[start]
	if !ok {
		return nil, fmt.Errorf("Unknown branch start: %s", start)
	}
	path := []Entry{entry}
	for entry.ParentID != nil {
		entry, ok = state.entries[*entry.ParentID]
		if !ok {
			return nil, errCorruptBranch
		}
		path = append(path, entry)
	}
	return path, nil
}

func branchScanMatches(candidate Entry, query StorageBranchScan) bool {
	if query.Type != "" && candidate.Type != query.Type {
		return false
	}
	if query.CustomType != "" && candidate.CustomType != query.CustomType {
		return false
	}
	if query.Cursor == nil {
		return true
	}
	if query.Order == OrderOldestFirst {
		return candidate.Seq > query.Cursor.Seq
	}
	return candidate.Seq < query.Cursor.Seq
}

// ScanBranchStructure is ScanBranch without payload fields.
func (state *InMemoryStorageState) ScanBranchStructure(query StorageBranchScan) ([]EntryStructure, error) {
	entries, err := state.ScanBranch(query)
	if err != nil {
		return nil, err
	}
	structures := make([]EntryStructure, 0, len(entries))
	for _, entry := range entries {
		structures = append(structures, EntryStructureOf(entry))
	}
	return structures, nil
}

// EntryStructureOf strips an entry to its placement fields.
func EntryStructureOf(entry Entry) EntryStructure {
	return EntryStructure{ID: entry.ID, ParentID: entry.ParentID, Seq: entry.Seq, Timestamp: entry.Timestamp, Type: entry.Type, CustomType: entry.CustomType}
}

// ScanEntries scans the session-wide entry inventory in sequence order.
func (state *InMemoryStorageState) ScanEntries(query EntryScan) []Entry {
	limit := math.MaxInt
	if query.Limit != nil {
		limit = max(0, *query.Limit)
	}
	entries := []Entry{}
	count := len(state.entriesBySeq)
	for offset := 0; offset < count && len(entries) < limit; offset++ {
		index := offset
		if query.Order == OrderDesc {
			index = count - 1 - offset
		}
		if entry := state.entriesBySeq[index]; entryScanMatches(entry, query) {
			entries = append(entries, entry)
		}
	}
	return entries
}

func entryScanMatches(entry Entry, query EntryScan) bool {
	return (query.Type == "" || entry.Type == query.Type) &&
		(query.CustomType == "" || entry.CustomType == query.CustomType) &&
		(query.FromSeq == nil || entry.Seq >= *query.FromSeq) &&
		(query.ToSeq == nil || entry.Seq <= *query.ToSeq)
}

// ScanUsage scans the usage ledger in sequence order.
func (state *InMemoryStorageState) ScanUsage(query UsageScan) []UsageRow {
	rows := []UsageRow{}
	for _, row := range state.usage {
		if (query.FromSeq == nil || row.Seq >= *query.FromSeq) && (query.ToSeq == nil || row.Seq <= *query.ToSeq) {
			rows = append(rows, row)
		}
	}
	slices.SortFunc(rows, func(left, right UsageRow) int {
		if query.Order == OrderDesc {
			return int(right.Seq - left.Seq)
		}
		return int(left.Seq - right.Seq)
	})
	if query.Limit != nil {
		rows = rows[:min(len(rows), max(0, *query.Limit))]
	}
	return rows
}

// GetStats returns the maintained totals.
func (state *InMemoryStorageState) GetStats() SessionStats { return state.stats }

// GetNextSeq returns the next sequence to assign.
func (state *InMemoryStorageState) GetNextSeq() int64 { return state.nextSeq }
