package history

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func evictTranscript(t *testing.T, n, size int) (*transcript, *Store) {
	t.Helper()
	tr := newTranscript(t, 2)
	for range n {
		tr.add("pi.user", `{"role":"user","content":"`+strings.Repeat("u", size)+`","timestamp":1}`, "")
	}
	st := NewStore()
	if _, err := tr.db.context(t, st, 2, 0); err != nil {
		t.Fatal(err)
	}
	return tr, st
}

func (tr *transcript) loader(calls *int) Loader {
	return func(conv, lo, hi int64, fn func(id, seq int64, rec []byte) bool) error {
		*calls++
		for _, row := range tr.db.entries {
			if row.id >= lo && row.id <= hi && !fn(row.id, row.seq, row.rec) {
				return nil
			}
		}
		return nil
	}
}

// TestEvictedRecordsAreReadBackFromStorage: once the store holds more than evictAbove it releases the oldest chunks, keeps
// the newest evictKeep bytes and the chunk an append extends, reads an evicted chunk back through the loader whole and
// byte for byte, holds at most reloadKeep read-back chunks, and reports a failed or mismatching read through Err.
func TestEvictedRecordsAreReadBackFromStorage(t *testing.T) {
	tr, st := evictTranscript(t, 40, 60000)
	ch := st.chainList[0]
	all := ch.ArenaBytes()
	if all <= evictAbove {
		t.Fatalf("test transcript is %d bytes, want more than %d", all, evictAbove)
	}
	calls := 0
	st.Evict() // no loader: nothing is released
	if st.ResidentBytes() != all {
		t.Fatalf("Evict without a loader released text: %d of %d", st.ResidentBytes(), all)
	}
	st.SetLoader(tr.loader(&calls))
	st.Evict()
	if got := st.ResidentBytes(); got > evictKeep+arenaChunk || got != ch.ArenaBytes() {
		t.Fatalf("resident after Evict = %d (arena %d), want at most %d", got, ch.ArenaBytes(), evictKeep+arenaChunk)
	}
	if ch.arena[len(ch.arena)-1] == nil {
		t.Fatal("Evict released the chunk the next append extends")
	}
	evicted := 0
	for _, c := range ch.arena {
		if c == nil {
			evicted++
		}
	}
	if evicted == 0 {
		t.Fatal("Evict released no chunk")
	}
	oldHeld := func() int {
		held := 0
		for k, c := range ch.arena {
			if c != nil && k != len(ch.arena)-1 {
				held++
			}
		}
		return held
	}
	base := oldHeld()
	for i := range ch.ents {
		if got, want := ch.record(i), tr.db.entries[i].rec; !bytes.Equal(got, want) {
			t.Fatalf("record %d reads back %d bytes, stored %d", i, len(got), len(want))
		}
		if held := oldHeld(); held > base+reloadKeep {
			t.Fatalf("after record %d the chain holds %d old chunks, %d after Evict", i, held, base)
		}
	}
	if calls == 0 || calls > len(ch.arena) {
		t.Fatalf("%d loader calls to read %d chunks once", calls, len(ch.arena))
	}
	if st.Err() != nil {
		t.Fatal(st.Err())
	}
	// A message read through the view is the stored one.
	v, err := tr.db.context(t, st, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Message(0); !bytes.Contains(tr.db.entries[0].rec, got) {
		t.Fatalf("message 0 = %.40q is not in the stored record", got)
	}
	// Appending still works and the new record is read as it was written.
	tr.add("pi.user", `{"role":"user","content":"after","timestamp":1}`, "")
	row := tr.db.entries[len(tr.db.entries)-1]
	if err := st.Append(2, row.id, row.seq, row.rec); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ch.record(len(ch.ents)-1), row.rec) {
		t.Fatal("an appended record after Evict does not read back")
	}
}

func TestEvictedReadFailuresAreReported(t *testing.T) {
	for name, load := range map[string]Loader{
		"error":   func(int64, int64, int64, func(int64, int64, []byte) bool) error { return errors.New("storage") },
		"missing": func(int64, int64, int64, func(int64, int64, []byte) bool) error { return nil },
		"changed": func(_, lo, _ int64, fn func(int64, int64, []byte) bool) error {
			fn(lo, 1, []byte(`{"id":1}`))
			return nil
		},
	} {
		_, st := evictTranscript(t, 40, 60000)
		st.SetLoader(load)
		st.Evict()
		ch := st.chainList[0]
		_ = ch.record(0)
		if st.Err() == nil {
			t.Fatalf("%s: a failed read of an evicted chunk was not reported", name)
		}
	}
}

// TestEvictLoadedReleasesAChainWhileItLoads: a load that has added only rows read from storage can release the chain's
// older text without waiting for the next StepStart; the text reads back, and a store without a loader keeps everything.
func TestEvictLoadedReleasesAChainWhileItLoads(t *testing.T) {
	tr, st := evictTranscript(t, 40, 60000)
	ch := st.chainList[0]
	all := ch.ArenaBytes()
	st.EvictLoaded(2)
	if st.ResidentBytes() != all {
		t.Fatalf("EvictLoaded without a loader released text: %d of %d", st.ResidentBytes(), all)
	}
	calls := 0
	st.SetLoader(tr.loader(&calls))
	st.EvictLoaded(99) // no such chain
	st.EvictLoaded(2)
	if got := st.ResidentBytes(); got > evictKeep+arenaChunk || got != ch.ArenaBytes() {
		t.Fatalf("resident after EvictLoaded = %d (arena %d), want at most %d", got, ch.ArenaBytes(), evictKeep+arenaChunk)
	}
	for i := range ch.ents {
		if got, want := ch.record(i), tr.db.entries[i].rec; !bytes.Equal(got, want) {
			t.Fatalf("record %d does not read back", i)
		}
	}
	if st.Err() != nil {
		t.Fatal(st.Err())
	}
}

// A derivation does not read an evicted record back: the text of the tool call ids it pairs lives in the chain's index, so a
// context over a transcript whose older records were released costs no read of storage (each read of a chunk is a host call and
// an allocation of the chunk).
func TestDerivationOverEvictedRecordsReadsNoStorage(t *testing.T) {
	tr := newTranscript(t, 2)
	pad := strings.Repeat("p", 3000)
	for i := range 400 {
		first := tr.turn(i)
		_ = first
		tr.add("pi.user", `{"role":"user","content":"`+pad+`","timestamp":1}`, "")
	}
	st := NewStore()
	calls := 0
	st.SetLoader(tr.loader(&calls))
	if _, err := tr.db.context(t, st, 2, 0); err != nil {
		t.Fatal(err)
	}
	st.Evict()
	ch := st.chainList[0]
	evicted := 0
	for _, c := range ch.arena {
		if c == nil {
			evicted++
		}
	}
	if evicted < 3 {
		t.Fatalf("%d chunks evicted of %d", evicted, len(ch.arena))
	}
	// A commit invalidates the derived context; deriving again pairs every tool call with its result.
	tr.turn(1000)
	last := tr.db.entries[len(tr.db.entries)-1]
	for _, row := range tr.db.entries[len(tr.db.entries)-4:] {
		if err := st.Append(2, row.id, row.seq, row.rec); err != nil {
			t.Fatal(err)
		}
	}
	calls = 0
	v, _, err := st.Context(2, last.id)
	if err != nil || v == nil {
		t.Fatalf("context: %v %v", v, err)
	}
	if v.NumMessages() < 1000 {
		t.Fatalf("%d messages", v.NumMessages())
	}
	if calls != 0 {
		t.Fatalf("the derivation read storage %d times", calls)
	}
}
