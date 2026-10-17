// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/store"
)

// newestRig is a handler over a conversation whose entries 1..stored are in storage; entryReads counts the reads that load entries.
func newestRig(t *testing.T, stored0 int64) (h *handler, entryReads *int, stored *int64) {
	t.Helper()
	h = &handler{mir: newMirror()}
	conv, _ := jv.Parse([]byte(`{"id":1}`))
	h.mir.convs.put(1, conv.(*jv.Object))
	n := 0
	entryReads = &n
	stored = new(int64)
	*stored = stored0
	record := func(id int64) []abi.Value {
		rec := []byte(`{"id":` + itoa(id) + `,"conversationId":1,"kind":"pi.user","model":[{"role":"user","content":"m` + itoa(id) + `","timestamp":1}]}`)
		return []abi.Value{abi.Int(id), abi.Int(id), {Kind: abi.ValueBlob, Bytes: rec}}
	}
	h.mir.lazy = lazyState{
		read: func(sql uint16, params []abi.Value) ([][]abi.Value, error) {
			switch sql {
			case store.SQLEntryBounds:
				hi := min(params[1].Int, *stored)
				return [][]abi.Value{{abi.Int(hi), {Kind: abi.ValueNull}, {Kind: abi.ValueNull}}}, nil
			case store.SQLEntryCount:
				n++
				return [][]abi.Value{{abi.Int(max(0, min(params[2].Int, *stored)-params[1].Int+1))}}, nil
			case store.SQLEntriesPage:
				n++
				var rows [][]abi.Value
				for id := params[1].Int + 1; id <= min(params[2].Int, *stored); id++ {
					rows = append(rows, record(id))
				}
				return rows, nil
			}
			return nil, nil
		},
		below: stored0 + 1,
	}
	h.mir.nextID = session.ID(stored0 + 1)
	return h, entryReads, stored
}

// commit makes entry id a committed entry of conversation 1, as the Session's commit does: the mirror holds it and the transcript
// store is appended.
func commit(t *testing.T, h *handler, stored *int64, id int64) {
	t.Helper()
	rec, _ := jv.Parse([]byte(`{"id":` + itoa(id) + `,"conversationId":1,"kind":"pi.user","model":[{"role":"user","content":"m` + itoa(id) + `","timestamp":1}]}`))
	obj, _ := rec.(*jv.Object)
	h.mir.entries.put(session.ID(id), 1, session.Seq(id), true, jv.Append(nil, obj))
	if err := h.observeCommit([]session.Write{{Kind: session.WriteEntry, Record: obj}}, session.Seq(id)); err != nil {
		t.Fatal(err)
	}
	// The host applied the commit, and the next step releases the held record: storage has it.
	*stored = id
	h.mir.nextID = session.ID(id + 1)
	h.StepStart()
}

// A context read as of the conversation's newest entry (the turn's own context: the tail it was started at) is the tip read: the
// chain it loads takes the entries the Session commits afterwards, so the next turn's read as of the new newest entry loads nothing.
// Before, the cutoff made the chain bounded, Store.Append dropped every later entry, and the next turn read them back from storage.
func TestAContextReadAsOfTheNewestEntryTakesTheLaterCommits(t *testing.T) {
	h, reads, stored := newestRig(t, 6)
	if v, err := h.contextView(1, 6); err != nil || v.Tail() != 6 {
		t.Fatalf("context as of 6: tail %v, err %v", v, err)
	}
	commit(t, h, stored, 7)
	commit(t, h, stored, 8)
	before := *reads
	v, err := h.contextView(1, 8)
	if err != nil || v.Tail() != 8 {
		t.Fatalf("context as of 8: %v, err %v", v, err)
	}
	if *reads != before {
		t.Fatalf("the read as of the newest entry loaded %d entry pages from storage; the Session committed them itself", *reads-before)
	}
}

// A cutoff below the newest entry still bounds the chain: entries between the bound and a later commit are not loaded.
func TestAContextReadBelowTheNewestEntryStaysBounded(t *testing.T) {
	h, reads, stored := newestRig(t, 6)
	if v, err := h.contextView(1, 4); err != nil || v.Tail() != 4 {
		t.Fatalf("context as of 4: %v, err %v", v, err)
	}
	commit(t, h, stored, 7)
	before := *reads
	v, err := h.contextView(1, 7)
	if err != nil || v.Tail() != 7 {
		t.Fatalf("context as of 7: %v, err %v", v, err)
	}
	if *reads == before {
		t.Fatal("entries 5 and 6 were never loaded: the bounded chain was extended by the later commit")
	}
}

// contextIDs is the entry IDs of conversation 1's context as of at (0: the tail), then its tail.
func contextIDs(t *testing.T, h *handler, at int64) []int64 {
	t.Helper()
	v, err := h.contextView(1, at)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, v.NumEntries()+1)
	for i := range v.NumEntries() {
		ids = append(ids, v.EntryID(i))
	}
	return append(ids, v.Tail())
}

// Whatever reads and commits came before, every context read gives what a fresh handler over the same storage gives. Before the
// newest-entry read was unbounded, a read as of the newest entry left a chain bounded there, and once later commits moved the
// tail, the tail read repeated the old bound's entry at its start ([9 0 1 ... 9] for [6 -7 -8 -9 8 9 0]).
func TestEveryContextReadAfterCommitsMatchesAFreshRead(t *testing.T) {
	// A positive step reads as of that entry, 0 reads the tail, a negative step commits that entry.
	for _, steps := range [][]int64{
		{6, -7, -8, -9, 8, 0},
		{6, -7, -8, -9, 8, 9, 0},
		{-7, -8, -9, 8, 0},
		{4, -7, 7, -8, 8, 0},
		{-7, -8, 7, -9, 8, 9, 0},
		{0, -7, -8, 7, 0},
		{5, -7, 7, 0},
		{4, -7, -8, -9, 8, 0},
		{4, -7, -8, -9, 8, 9, 0},
		{4, -7, 7, -8, -9, 8, 9, 0},
		{3, -7, -8, 5, -9, 8, 0},
	} {
		h, _, stored := newestRig(t, 6)
		newest := int64(6)
		for _, step := range steps {
			if step < 0 {
				commit(t, h, stored, -step)
				newest = -step
				continue
			}
			fresh, _, _ := newestRig(t, newest)
			if got, want := contextIDs(t, h, step), contextIDs(t, fresh, step); !slices.Equal(got, want) {
				t.Fatalf("steps %v: the context as of %d is %v, a fresh read gives %v", steps, step, got, want)
			}
		}
	}
}
