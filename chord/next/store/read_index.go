package store

import (
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/src/store/read-index.ts
//
// The commit index of a store's reads (slice 15 §3): which reads a commit's keyed writes to a table can concern, so
// the commit step skip-tests those alone. A read whose condition fixes own columns with `eq` or `inArray` is filed
// under their values (its bucket) and the keys of its last result's rows; any other is unindexed. A write's
// candidates: the reads holding its key (the row was in their result), those whose bucket the row after the write
// matches (it may be now) and the unindexed ones. A pure speedup: a read that neither held the row nor can hold it
// after is one the skip test would skip.

// maxBucketCombinations is how many value combinations a read's bucket may have (the product of its `inArray` lists'
// lengths); a column whose list would pass it is left out of the bucket (slice 15 §3, decided: 32).
const maxBucketCombinations = 32

// bucketGroup is the reads of one bucket shape on a table: by the identity of their columns' values.
type bucketGroup struct {
	name     string
	columns  []string
	byValues map[any][]*RegisteredRead
}

// readFiling is a filed read's entries: its table's index, its group and value identities there, the key sets it is
// filed by.
type readFiling struct {
	index      *tableIndex
	group      *bucketGroup
	identities []any
	// kept is the key sets the read is currently filed under; nil means no result to rule a write out by (the read is
	// unindexed until it has one).
	kept []map[any]bool
}

// tableIndex is the index of one table: its unindexed reads, its buckets by shape, and the reads by the keys they keep.
type tableIndex struct {
	// key is the table's key columns, known once a read is filed by its bucket (every plan of the table has the same).
	key []string
	// unindexed are the candidates of every write: reads without a bucket here, and filed reads without a result.
	unindexed []*RegisteredRead
	// groups are the bucket groups by their columns, joined; groupOrder preserves insertion order.
	groups     map[string]*bucketGroup
	groupOrder []string
	// kept files reads by the key identities of their last result's rows.
	kept map[any][]*RegisteredRead
	// changes counts changes of groups so lookup SQL is rendered only when groups change.
	changes       int
	lookupChanges int
	lookupSQL     string
	lookupValid   bool
}

func newTableIndex() *tableIndex {
	return &tableIndex{
		groups: map[string]*bucketGroup{},
		kept:   map[any][]*RegisteredRead{},
	}
}

func (idx *tableIndex) group(columns []string) *bucketGroup {
	name := strings.Join(columns, ",")
	g, ok := idx.groups[name]
	if !ok {
		g = &bucketGroup{
			name:     name,
			columns:  slices.Clone(columns),
			byValues: map[any][]*RegisteredRead{},
		}
		idx.groups[name] = g
		idx.groupOrder = append(idx.groupOrder, name)
		idx.changes++
	}
	return g
}

func (idx *tableIndex) drop(g *bucketGroup) {
	delete(idx.groups, g.name)
	idx.groupOrder = without(idx.groupOrder, g.name)
	idx.changes++
}

func (idx *tableIndex) isEmpty() bool {
	return len(idx.unindexed) == 0 && len(idx.groups) == 0 && len(idx.kept) == 0
}

// lookup returns the SELECT statement reading back, by key, the row a step wrote to table in every column a bucket
// fixes; "" when the key holds them all (the written key is the row's values then).
func (idx *tableIndex) lookup(table string) string {
	if idx.lookupValid && idx.lookupChanges == idx.changes {
		return idx.lookupSQL
	}
	var cols []string
	for _, name := range idx.groupOrder {
		for _, col := range idx.groups[name].columns {
			if !slices.Contains(cols, col) {
				cols = append(cols, col)
			}
		}
	}
	allInKey := true
	for _, col := range cols {
		if !slices.Contains(idx.key, col) {
			allInKey = false
			break
		}
	}
	var sql string
	if !allInKey && len(cols) > 0 {
		quotedCols := make([]string, len(cols))
		for i, col := range cols {
			quotedCols[i] = Quote(col)
		}
		whereParts := make([]string, len(idx.key))
		for i, col := range idx.key {
			whereParts[i] = Quote(col) + " = ?"
		}
		sql = "SELECT " + strings.Join(quotedCols, ", ") + " FROM " + Quote(table) + " WHERE " + strings.Join(whereParts, " AND ")
	}
	idx.lookupChanges = idx.changes
	idx.lookupSQL = sql
	idx.lookupValid = true
	return sql
}

// ReadIndex is one store's commit index, kept by its Reads as reads enter and leave, and by the reads themselves as
// their kept keys change (KeptKeysWatcher).
type ReadIndex struct {
	mu      sync.Mutex
	tables  map[string]*tableIndex
	filings map[*RegisteredRead]*readFiling
}

// Add files read under every table it uses: by its bucket on its own table when it has one, else unindexed.
func (ri *ReadIndex) Add(read *RegisteredRead) {
	plan := read.Plan
	cols, ids, ok := bucketOf(read)
	ri.mu.Lock()
	for _, use := range plan.Uses {
		if !ok || use.Table != plan.Table {
			idx := ri.index(use.Table)
			idx.unindexed = addReadSlice(idx.unindexed, read)
		}
	}
	if !ok {
		ri.mu.Unlock()
		return
	}
	idx := ri.index(plan.Table)
	idx.key = plan.Query.Table.Key
	g := idx.group(cols)
	for _, id := range ids {
		g.byValues[id] = addReadSlice(g.byValues[id], read)
	}
	ri.filings[read] = &readFiling{
		index:      idx,
		group:      g,
		identities: ids,
		kept:       []map[any]bool{},
	}
	ri.mu.Unlock()
	read.WatchKept(ri)
	ri.Kept(read)
}

// Delete removes every entry of read.
func (ri *ReadIndex) Delete(read *RegisteredRead) {
	read.WatchKept(nil)
	ri.mu.Lock()
	defer ri.mu.Unlock()
	for _, use := range read.Plan.Uses {
		if idx, ok := ri.tables[use.Table]; ok {
			idx.unindexed = removeReadSlice(idx.unindexed, read)
			if idx.isEmpty() {
				delete(ri.tables, use.Table)
			}
		}
	}
	filing, ok := ri.filings[read]
	if !ok {
		return
	}
	delete(ri.filings, read)
	idx, g := filing.index, filing.group
	for _, id := range filing.identities {
		list := removeReadSlice(g.byValues[id], read)
		if len(list) == 0 {
			delete(g.byValues, id)
		} else {
			g.byValues[id] = list
		}
	}
	if len(g.byValues) == 0 {
		idx.drop(g)
	}
	for _, keys := range filing.kept {
		for key := range keys {
			list := removeReadSlice(idx.kept[key], read)
			if len(list) == 0 {
				delete(idx.kept, key)
			} else {
				idx.kept[key] = list
			}
		}
	}
	if idx.isEmpty() {
		delete(ri.tables, read.Plan.Table)
	}
}

// Kept re-files read by the keys it keeps now, unless they are the same sets (a run adopted after its group's step).
// Only the sets that changed are walked: a set it kept and keeps is filed already, a key of a set it no longer keeps
// leaves unless another set keeps it, a key of a new set is added.
func (ri *ReadIndex) Kept(read *RegisteredRead) {
	kept := read.KeptSets()
	ri.mu.Lock()
	defer ri.mu.Unlock()
	filing, ok := ri.filings[read]
	if !ok {
		return
	}
	before := filing.kept
	if sameKeySets(kept, before) {
		return
	}
	filing.kept = kept
	idx := filing.index
	if kept == nil {
		for _, keys := range before {
			for key := range keys {
				list := removeReadSlice(idx.kept[key], read)
				if len(list) == 0 {
					delete(idx.kept, key)
				} else {
					idx.kept[key] = list
				}
			}
		}
		idx.unindexed = addReadSlice(idx.unindexed, read)
		return
	}
	idx.unindexed = removeReadSlice(idx.unindexed, read)
	for _, keys := range before {
		if containsMapRef(kept, keys) {
			continue
		}
		for key := range keys {
			if !keepsKey(kept, key) {
				list := removeReadSlice(idx.kept[key], read)
				if len(list) == 0 {
					delete(idx.kept, key)
				} else {
					idx.kept[key] = list
				}
			}
		}
	}
	for _, keys := range kept {
		if containsMapRef(before, keys) {
			continue
		}
		for key := range keys {
			idx.kept[key] = addReadSlice(idx.kept[key], read)
		}
	}
}

// Candidates returns the reads of table that changes (its keyed writes in one step, applied) can concern: each indexed
// one with the writes it is a candidate of, added to into (and order appended to order); returns the unindexed ones,
// candidates of every write.
func (ri *ReadIndex) Candidates(
	table string,
	changes []Change,
	step *Step,
	into map[*RegisteredRead][]Change,
	order *[]*RegisteredRead,
) ([]*RegisteredRead, error) {
	ri.mu.Lock()
	idx, ok := ri.tables[table]
	if !ok {
		ri.mu.Unlock()
		return nil, nil
	}
	unindexed := slices.Clone(idx.unindexed)
	key := idx.key
	if key == nil {
		ri.mu.Unlock()
		return unindexed, nil
	}
	lookup := idx.lookup(table)
	hasGroups := len(idx.groups) > 0
	ri.mu.Unlock()

	type cachedRow struct {
		row   sqlite.SqliteRow
		found bool
	}
	after := map[any]cachedRow{}
	for _, change := range changes {
		keyVals := make([]sqlite.SqliteValue, len(key))
		for i, name := range key {
			keyVals[i] = change.Key[name]
		}
		id := IdentityOf(keyVals)

		ri.mu.Lock()
		keptReads := slices.Clone(idx.kept[id])
		ri.mu.Unlock()
		for _, read := range keptReads {
			addCandidateChange(into, order, read, change)
		}
		if change.Kind == ChangeDelete || !hasGroups {
			continue
		}
		cached, seen := after[id]
		if !seen {
			if lookup == "" {
				cached = cachedRow{row: change.Key, found: true}
			} else {
				row, err := step.Get(lookup, keyVals...)
				if err != nil {
					return nil, err
				}
				cached = cachedRow{row: row, found: row != nil}
			}
			after[id] = cached
		}
		if !cached.found {
			continue
		}
		ri.mu.Lock()
		var matched []*RegisteredRead
		for _, gName := range idx.groupOrder {
			g := idx.groups[gName]
			valID := Identity(g.columns, cached.row)
			matched = append(matched, g.byValues[valID]...)
		}
		ri.mu.Unlock()
		for _, read := range matched {
			addCandidateChange(into, order, read, change)
		}
	}
	return unindexed, nil
}

func (ri *ReadIndex) index(table string) *tableIndex {
	idx, ok := ri.tables[table]
	if !ok {
		idx = newTableIndex()
		ri.tables[table] = idx
	}
	return idx
}

func addCandidateChange(into map[*RegisteredRead][]Change, order *[]*RegisteredRead, read *RegisteredRead, change Change) {
	list, ok := into[read]
	if !ok {
		*order = append(*order, read)
		into[read] = []Change{change}
		return
	}
	// A write found by its key and by its bucket is tested once (same change at tail).
	last := list[len(list)-1]
	if last.Table == change.Table && last.Kind == change.Kind && mapsEqualChangeKey(last.Key, change.Key) && slices.Equal(last.Assigned, change.Assigned) {
		return
	}
	into[read] = append(list, change)
}

func mapsEqualChangeKey(a, b map[string]sqlite.SqliteValue) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func addReadSlice(list []*RegisteredRead, read *RegisteredRead) []*RegisteredRead {
	if slices.Contains(list, read) {
		return list
	}
	return append(list, read)
}

func removeReadSlice(list []*RegisteredRead, read *RegisteredRead) []*RegisteredRead {
	at := slices.Index(list, read)
	if at < 0 {
		return list
	}
	return slices.Delete(list, at, at+1)
}

func keepsKey(kept []map[any]bool, key any) bool {
	for _, m := range kept {
		if m[key] {
			return true
		}
	}
	return false
}

// sameKeySets reports whether two slices of key maps are identical (same length and same map instances in order).
func sameKeySets(a, b []map[any]bool) bool {
	if (a == nil) != (b == nil) || len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameMapInstance(a[i], b[i]) {
			return false
		}
	}
	return true
}

func containsMapRef(list []map[any]bool, target map[any]bool) bool {
	for _, m := range list {
		if sameMapInstance(m, target) {
			return true
		}
	}
	return false
}

// sameMapInstance compares two maps by header pointer (reflect.ValueOf(m).Pointer() is avoided via fmt/unsafe? No,
// we just check key-set pointer via KeySet wrapper or compare). Here we use a tiny pointer-identity wrapper? No: in
// Go maps are pointers under the hood; comparing length + pointer isn't built-in without reflect. Let's use reflect?
// Actually, KeptRows is a pointer struct, so we can just compare map equality by wrapping or checking reflect.
// Even simpler: ReadIndex is called only when KeptSets changes; reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
// is exact and fast. Let's use *KeptKeys or reflect? Let's keep KeptRows.Keys as *KeySet or use reflect.
// Wait: let's check if we can just use a named struct or reflect. Let's use a tiny helper with reflect.ValueOf?
// Actually no: we can store *KeySet in KeptRows so == works with zero reflect!
func sameMapInstance(a, b map[any]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// bucketOf is the bucket of read on its own table: the columns its condition fixes and the identity of every
// combination of their values; ok=false when it has none, uses its own table twice (an `exists` over it), or counts.
func bucketOf(read *RegisteredRead) (columns []string, identities []any, ok bool) {
	plan := read.Plan
	if read.What.Kind == "count" {
		return nil, nil, false
	}
	own := 0
	for _, use := range plan.Uses {
		if use.Table == plan.Table {
			own++
		}
	}
	if own > 1 {
		return nil, nil, false
	}
	terms := slices.Clone(plan.Equalities)
	if len(terms) > 1 {
		slices.SortFunc(terms, func(a, b EqualityTerm) int {
			return strings.Compare(a.Column, b.Column)
		})
	}
	combinations := [][]sqlite.SqliteValue{{}}
	for _, term := range terms {
		if len(term.Values) == 0 || len(combinations)*len(term.Values) > maxBucketCombinations {
			continue
		}
		columns = append(columns, term.Column)
		next := make([][]sqlite.SqliteValue, 0, len(combinations)*len(term.Values))
		for _, prefix := range combinations {
			for _, value := range term.Values {
				combo := make([]sqlite.SqliteValue, len(prefix)+1)
				copy(combo, prefix)
				combo[len(prefix)] = value
				next = append(next, combo)
			}
		}
		combinations = next
	}
	if len(columns) == 0 {
		return nil, nil, false
	}
	seen := make(map[any]bool, len(combinations))
	identities = make([]any, 0, len(combinations))
	for _, combo := range combinations {
		id := IdentityOf(combo)
		if !seen[id] {
			seen[id] = true
			identities = append(identities, id)
		}
	}
	return columns, identities, true
}
