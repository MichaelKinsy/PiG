package sdk

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"runtime"
	"testing"
)

func appendEntry(id, parent string) json.RawMessage {
	p := "null"
	if parent != "" {
		p = fmt.Sprintf("%q", parent)
	}
	return json.RawMessage(fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,"timestamp":"2026-10-11T00:00:00.000Z","message":{"role":"user","content":"text of %s"}}`, id, p, id))
}

func pushEntries(m *sessionMirror, session string, entries []json.RawMessage, total int, leaf string) {
	update, _ := json.Marshal(map[string]any{"sessionId": session, "leafId": leaf, "entriesAppended": entries, "entryCount": total})
	m.applySessionUpdate(update)
}

// referenceBranch is the root→leaf walk from the mirror's log, written without any cache.
func referenceBranch(m *sessionMirror) []string {
	var ids []string
	for current := m.leafID; current != ""; {
		meta, ok := m.index[current]
		if !ok {
			break
		}
		id, _ := extractEntryIDs(m.entries[meta.pos])
		ids = append([]string{id}, ids...)
		current = meta.parentID
	}
	return ids
}

func branchIDs(t *testing.T, m *sessionMirror) (raw, decoded []string) {
	t.Helper()
	for _, r := range m.getBranch() {
		id, _ := extractEntryIDs(r)
		raw = append(raw, id)
	}
	for _, e := range m.getBranchEntries() {
		decoded = append(decoded, e.ID)
	}
	return raw, decoded
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Appends below the leaf, forks from an older entry, leaf moves without
// appends, rewinds and session switches must all read the same branch as an
// uncached walk, whichever of getBranch and getBranchEntries reads first.
func TestIncrementalBranchMatchesUncachedWalkAcrossLeafMoves(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	m := &sessionMirror{}
	m.subscribed.Store(true)
	session, total := "s1", 0
	var ids []string
	leaf := ""
	for step := range 400 {
		switch op := rng.Intn(10); {
		case op < 5 || len(ids) == 0: // append below the leaf
			id := fmt.Sprintf("%s-e%d", session, total)
			pushEntries(m, session, []json.RawMessage{appendEntry(id, leaf)}, total+1, id)
			ids, leaf, total = append(ids, id), id, total+1
		case op < 6: // several entries in one push
			var batch []json.RawMessage
			parent := leaf
			for range 1 + rng.Intn(4) {
				id := fmt.Sprintf("%s-e%d", session, total+len(batch))
				batch = append(batch, appendEntry(id, parent))
				parent = id
			}
			pushEntries(m, session, batch, total+len(batch), parent)
			for range batch {
				ids = append(ids, fmt.Sprintf("%s-e%d", session, total))
				total++
			}
			leaf = parent
		case op < 8: // fork: append below an older entry
			parent := ids[rng.Intn(len(ids))]
			id := fmt.Sprintf("%s-e%d", session, total)
			pushEntries(m, session, []json.RawMessage{appendEntry(id, parent)}, total+1, id)
			ids, leaf, total = append(ids, id), id, total+1
		case op < 9: // leaf moves to an existing entry with nothing appended
			leaf = ids[rng.Intn(len(ids))]
			pushEntries(m, session, nil, total, leaf)
		default: // another session replaces the log
			session = fmt.Sprintf("s%d", step)
			id := fmt.Sprintf("%s-e0", session)
			pushEntries(m, session, []json.RawMessage{appendEntry(id, "")}, 1, id)
			ids, leaf, total = []string{id}, id, 1
		}
		// Alternate which reader runs first so each cache is extended by a miss on its own.
		if step%3 == 0 {
			m.getBranchEntries()
		}
		raw, decoded := branchIDs(t, m)
		want := referenceBranch(m)
		if !equalIDs(raw, want) || !equalIDs(decoded, want) {
			t.Fatalf("step %d: branch raw %v decoded %v, want %v", step, raw, decoded, want)
		}
	}
}

// An entry appended under an unchanged leaf (a side branch) leaves the branch
// as it was; a repeated entry id replaces the log's meaning and is rebuilt.
func TestIncrementalBranchSideEntryAndRepeatedID(t *testing.T) {
	m := &sessionMirror{}
	m.subscribed.Store(true)
	pushEntries(m, "s", []json.RawMessage{appendEntry("a", ""), appendEntry("b", "a")}, 2, "b")
	if _, d := branchIDs(t, m); !equalIDs(d, []string{"a", "b"}) {
		t.Fatalf("branch %v", d)
	}
	first := m.getBranchEntries()
	pushEntries(m, "s", []json.RawMessage{appendEntry("side", "a")}, 3, "b")
	second := m.getBranchEntries()
	if len(second) != 2 || second[0] != first[0] || second[1] != first[1] {
		t.Fatalf("side entry rebuilt the branch: %v", second)
	}
	pushEntries(m, "s", []json.RawMessage{appendEntry("b", "side")}, 4, "b")
	raw, d := branchIDs(t, m)
	if want := []string{"a", "side", "b"}; !equalIDs(raw, want) || !equalIDs(d, want) {
		t.Fatalf("repeated id: raw %v decoded %v, want %v", raw, d, want)
	}
}

// Returned slices are copies: mutating one never reaches the cache that the next append extends.
func TestIncrementalBranchReturnsCopies(t *testing.T) {
	m := &sessionMirror{}
	m.subscribed.Store(true)
	pushEntries(m, "s", []json.RawMessage{appendEntry("a", "")}, 1, "a")
	rawBranch := m.getBranch()
	decoded := m.getBranchEntries()
	rawBranch[0], decoded[0] = nil, nil
	pushEntries(m, "s", []json.RawMessage{appendEntry("b", "a")}, 2, "b")
	if raw, d := branchIDs(t, m); !equalIDs(raw, []string{"a", "b"}) || !equalIDs(d, []string{"a", "b"}) {
		t.Fatalf("raw %v decoded %v", raw, d)
	}
}

func appendReadAllocs(n int) float64 {
	m := &sessionMirror{}
	m.subscribed.Store(true)
	all := make([]json.RawMessage, n)
	for i := range n {
		parent := ""
		if i > 0 {
			parent = fmt.Sprintf("e%d", i-1)
		}
		all[i] = appendEntry(fmt.Sprintf("e%d", i), parent)
	}
	pushEntries(m, "s", all, n, fmt.Sprintf("e%d", n-1))
	m.getBranchEntries()
	next := n
	best := float64(1 << 60)
	for range 5 {
		id := fmt.Sprintf("e%d", next)
		entry := appendEntry(id, fmt.Sprintf("e%d", next-1))
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		pushEntries(m, "s", []json.RawMessage{entry}, next+1, id)
		if len(m.getBranchEntries()) != next+1 {
			panic("branch length")
		}
		runtime.ReadMemStats(&after)
		best = min(best, float64(after.Mallocs-before.Mallocs))
		next++
	}
	return best
}

// The cost of reading the branch after one append is the cost of decoding one
// entry, whatever the length of the branch.
func TestAppendThenReadBranchAllocatesTheSameAtAnyLength(t *testing.T) {
	small, large := appendReadAllocs(2000), appendReadAllocs(16000)
	t.Logf("allocations per append+read: n=2000 %.0f, n=16000 %.0f", small, large)
	if large > small*1.5+8 {
		t.Fatalf("allocations grew with the branch: %.0f at n=2000, %.0f at n=16000", small, large)
	}
}
