package views

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// Ports articulated 1.4.0 (Matthew Weidner, MIT: src/id_list.ts, src/saved_id_list.ts, src/element_id_generator.ts): the part of it
// a text room uses (apps/p2/plugins/views/text.ts), as the same observable list: known element IDs in list order, each present or
// deleted (a tombstone), inserted right after a known ID, addressed by index among the present ones, and saved in articulated's
// compact JSON form (which the view kit's own articulated loads: a text room's base carries it). Go forced difference: articulated
// keeps a persistent B+tree of leaves; this keeps the leaves' content, a slice of runs (one bunch, consecutive counters, one
// present-or-deleted state), copied per change. A text room's list is a few hundred runs, not a tree's worth.

// ElementID is articulated's `ElementId`: a bunch and a counter within it.
type ElementID struct {
	BunchID string
	Counter int64
}

type idRun struct {
	bunch   string
	start   int64
	count   int64
	deleted bool
}

// IdList is articulated's IdList. Immutable: every change returns a new list.
type IdList struct {
	runs    []idRun
	present int
}

const maxSafeInteger = 1<<53 - 1

// isSafeInteger is `Number.isSafeInteger`.
func isSafeInteger(value float64) bool {
	return value == math.Trunc(value) && math.Abs(value) <= maxSafeInteger
}

// NewIdList is an empty list.
func NewIdList() *IdList { return &IdList{} }

// Length is the number of present IDs.
func (list *IdList) Length() int { return list.present }

func (list *IdList) find(id ElementID) int {
	for i, run := range list.runs {
		if run.bunch == id.BunchID && run.start <= id.Counter && id.Counter < run.start+run.count {
			return i
		}
	}
	return -1
}

// IsKnown is whether id is known to the list, present or deleted.
func (list *IdList) IsKnown(id ElementID) bool { return list.find(id) >= 0 }

// Has is whether id is present.
func (list *IdList) Has(id ElementID) bool {
	i := list.find(id)
	return i >= 0 && !list.runs[i].deleted
}

func (list *IdList) isAnyKnown(id ElementID, count int64) bool {
	if count == 0 {
		return false
	}
	last := id.Counter + count - 1
	for _, run := range list.runs {
		if run.bunch == id.BunchID && run.start <= last && id.Counter <= run.start+run.count-1 {
			return true
		}
	}
	return false
}

// normalized joins neighbours that continue each other (the same bunch, state and consecutive counters), drops empty runs and
// counts the present IDs.
func normalized(runs []idRun) *IdList {
	merged := make([]idRun, 0, len(runs))
	present := 0
	for _, run := range runs {
		if run.count == 0 {
			continue
		}
		if !run.deleted {
			present += int(run.count)
		}
		if n := len(merged); n > 0 {
			last := &merged[n-1]
			if last.bunch == run.bunch && last.deleted == run.deleted && last.start+last.count == run.start {
				last.count += run.count
				continue
			}
		}
		merged = append(merged, run)
	}
	return &IdList{runs: merged, present: present}
}

// InsertAfter inserts newID (and count-1 more with the same bunch and following counters) immediately after before (known, maybe
// deleted); before nil inserts at the start, left of every known ID. It fails when before is not known or an inserted ID is known.
func (list *IdList) InsertAfter(before *ElementID, newID ElementID, count int64) (*IdList, error) {
	if newID.Counter < 0 {
		return nil, fmt.Errorf("Invalid counter: %d", newID.Counter)
	}
	if count < 0 {
		return nil, fmt.Errorf("Invalid count: %d", count)
	}
	if list.isAnyKnown(newID, count) {
		return nil, errors.New("An inserted id is already known")
	}
	added := idRun{bunch: newID.BunchID, start: newID.Counter, count: count}
	if before == nil {
		if count == 0 {
			return list, nil
		}
		return normalized(append([]idRun{added}, list.runs...)), nil
	}
	at := list.find(*before)
	if at < 0 {
		return nil, errors.New("before is not known")
	}
	if count == 0 {
		return list, nil
	}
	run := list.runs[at]
	head := before.Counter + 1 - run.start
	runs := make([]idRun, 0, len(list.runs)+2)
	runs = append(runs, list.runs[:at]...)
	runs = append(runs, idRun{bunch: run.bunch, start: run.start, count: head, deleted: run.deleted}, added,
		idRun{bunch: run.bunch, start: run.start + head, count: run.count - head, deleted: run.deleted})
	runs = append(runs, list.runs[at+1:]...)
	return normalized(runs), nil
}

// deleteID marks id deleted; nothing for an ID that is not known or already deleted.
func (list *IdList) deleteID(id ElementID) *IdList {
	at := list.find(id)
	if at < 0 || list.runs[at].deleted {
		return list
	}
	run := list.runs[at]
	runs := make([]idRun, 0, len(list.runs)+2)
	runs = append(runs, list.runs[:at]...)
	runs = append(runs, idRun{bunch: run.bunch, start: run.start, count: id.Counter - run.start},
		idRun{bunch: run.bunch, start: id.Counter, count: 1, deleted: true},
		idRun{bunch: run.bunch, start: id.Counter + 1, count: run.start + run.count - id.Counter - 1})
	runs = append(runs, list.runs[at+1:]...)
	return normalized(runs)
}

// DeleteRange deletes the IDs with indexes in [from, to); an index out of bounds is an error.
func (list *IdList) DeleteRange(from, to int) (*IdList, error) {
	ids := make([]ElementID, 0, max(0, to-from))
	for i := from; i < to; i++ {
		id, err := list.At(i)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	next := list
	for _, id := range ids {
		next = next.deleteID(id)
	}
	return next, nil
}

// At is the ID at index among the present ones.
func (list *IdList) At(index int) (ElementID, error) {
	if index < 0 || index >= list.present {
		return ElementID{}, fmt.Errorf("Index out of bounds: %d (length: %d)", index, list.present)
	}
	remaining := int64(index)
	for _, run := range list.runs {
		if run.deleted {
			continue
		}
		if remaining < run.count {
			return ElementID{BunchID: run.bunch, Counter: run.start + remaining}, nil
		}
		remaining -= run.count
	}
	return ElementID{}, errors.New("Internal error")
}

// Bias says what IndexOf answers for a deleted ID: none -1, left the index of the present ID to its left, right of the one to its
// right (the index where it would be if present).
type Bias int

const (
	// BiasNone answers -1 for a deleted ID.
	BiasNone Bias = iota
	// BiasLeft answers the index immediately to the left of a deleted ID.
	BiasLeft
	// BiasRight answers the index immediately to the right of a deleted ID.
	BiasRight
)

// IndexOf is the index of id; id must be known.
func (list *IdList) IndexOf(id ElementID, bias Bias) (int, error) {
	index := int64(0)
	for _, run := range list.runs {
		if run.bunch == id.BunchID && run.start <= id.Counter && id.Counter < run.start+run.count {
			if !run.deleted {
				return int(index + id.Counter - run.start), nil
			}
			switch bias {
			case BiasLeft:
				return int(index) - 1, nil
			case BiasRight:
				return int(index), nil
			}
			return -1, nil
		}
		if !run.deleted {
			index += run.count
		}
	}
	return 0, errors.New("id is not known")
}

// CompactSavedIdList is articulated's `CompactSavedIdList`: the known IDs in list order, run by run, with the counters that follow
// from the previous run of a bunch left out.
type CompactSavedIdList struct {
	BunchIDs           []string
	SignedCounts       []int64
	StartCounterDeltas []int64
	StartCounterValues []int64
}

// SaveCompact is `saveCompact()`.
func (list *IdList) SaveCompact() CompactSavedIdList {
	saved := CompactSavedIdList{BunchIDs: []string{}, SignedCounts: []int64{}, StartCounterDeltas: []int64{}, StartCounterValues: []int64{}}
	lastException := 0
	inferred := map[string]int64{}
	for i, run := range list.runs {
		saved.BunchIDs = append(saved.BunchIDs, run.bunch)
		count := run.count
		if run.deleted && count != 0 {
			count = -count
		}
		saved.SignedCounts = append(saved.SignedCounts, count)
		if inferred[run.bunch] != run.start {
			saved.StartCounterDeltas = append(saved.StartCounterDeltas, int64(i-lastException))
			lastException = i
			saved.StartCounterValues = append(saved.StartCounterValues, run.start)
		}
		inferred[run.bunch] = run.start + run.count
	}
	return saved
}

// LoadCompactIdList is `IdList.loadCompact`: it fails on a saved list that is not valid (a counter or count that is not a safe
// non-negative integer, duplicate IDs).
func LoadCompactIdList(saved CompactSavedIdList) (*IdList, error) {
	if saved.BunchIDs == nil {
		return nil, errors.New("Not a CompactSavedIdList")
	}
	runs := make([]idRun, 0, len(saved.BunchIDs))
	inferred := map[string]int64{}
	nextException := -1
	if len(saved.StartCounterDeltas) > 0 {
		nextException = int(saved.StartCounterDeltas[0])
	}
	exception := 0
	for i, bunch := range saved.BunchIDs {
		signed := saved.SignedCounts[i]
		count := signed
		if count < 0 {
			count = -count
		}
		var start int64
		if i == nextException {
			if exception >= len(saved.StartCounterValues) {
				return nil, errors.New("Invalid startCounter: undefined")
			}
			start = saved.StartCounterValues[exception]
			exception++
			if exception == len(saved.StartCounterDeltas) {
				nextException = -1
			} else {
				nextException += int(saved.StartCounterDeltas[exception])
			}
		} else {
			start = inferred[bunch]
		}
		inferred[bunch] = start + count
		if count > maxSafeInteger {
			return nil, fmt.Errorf("Invalid count: %d", count)
		}
		if start < 0 || start > maxSafeInteger {
			return nil, fmt.Errorf("Invalid startCounter: %d", start)
		}
		runs = append(runs, idRun{bunch: bunch, start: start, count: count, deleted: signed < 0})
	}
	list := normalized(runs)
	// No ID twice: the counter ranges of one bunch do not overlap.
	byBunch := map[string][][2]int64{}
	for _, run := range runs {
		if run.count > 0 {
			byBunch[run.bunch] = append(byBunch[run.bunch], [2]int64{run.start, run.start + run.count})
		}
	}
	for _, ranges := range byBunch {
		sort.Slice(ranges, func(a, b int) bool { return ranges[a][0] < ranges[b][0] })
		for i := 1; i < len(ranges); i++ {
			if ranges[i][0] < ranges[i-1][1] {
				return nil, errors.New("Invalid savedState: duplicate ids")
			}
		}
	}
	return list, nil
}

// ElementIDGenerator is articulated's `ElementIdGenerator`: unique new IDs that extend their bunch where they can.
type ElementIDGenerator struct {
	newBunchID func() string
	next       map[string]int64
}

// NewElementIDGenerator is a generator that makes a new bunch ID with newBunchID.
func NewElementIDGenerator(newBunchID func() string) *ElementIDGenerator {
	return &ElementIDGenerator{newBunchID: newBunchID, next: map[string]int64{}}
}

// GenerateAfter is a unique new ID optimized for insertion after before; count IDs are reserved for a bulk insert.
func (generator *ElementIDGenerator) GenerateAfter(before *ElementID, count int64) ElementID {
	if before != nil {
		if next, known := generator.next[before.BunchID]; known && next == before.Counter+1 {
			generator.next[before.BunchID] = next + count
			return ElementID{BunchID: before.BunchID, Counter: next}
		}
	}
	bunch := generator.newBunchID()
	generator.next[bunch] = count
	return ElementID{BunchID: bunch}
}
