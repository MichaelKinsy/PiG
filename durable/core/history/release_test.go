// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package history

import (
	"bytes"
	"testing"
)

func messagesOf(v *View) [][]byte {
	out := make([][]byte, v.NumMessages())
	for i := range out {
		out[i] = bytes.Clone(v.Message(i))
	}
	return out
}

func sameMessages(t *testing.T, got, want [][]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%d messages, want %d", len(got), len(want))
	}
	for i := range got {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("message %d: %s, want %s", i, got[i], want[i])
		}
	}
}

func loaded(st *Store, conv int64) bool {
	ch := st.convs[conv].chain
	return ch != nil && ch.From != unloadedFrom && len(ch.ents) > 0
}

// Release frees what an idle conversation derived and loaded (Pi drops its kept context range, scheduler.ts
// #settleIdle): the chain's entries go, and the next derivation reads them again and yields the same messages.
func TestReleaseFreesAConversationsTranscript(t *testing.T) {
	tr := newTranscript(t, 2)
	for i := range 20 {
		tr.turn(i)
	}
	st := NewStore()
	v, err := tr.db.context(t, st, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := messagesOf(v)
	st.Release(2)
	if loaded(st, 2) || st.convs[2].chain.arena != nil || st.convs[2].ctx != nil {
		t.Fatal("the released conversation still holds its entries or its context")
	}
	before := tr.db.rowsRead
	v, err = tr.db.context(t, st, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if tr.db.rowsRead-before < 80 {
		t.Fatalf("the derivation after the release read %d rows, not the transcript again", tr.db.rowsRead-before)
	}
	sameMessages(t, messagesOf(v), want)
}

// A chain another conversation's context still reads stays: releasing a fork's parent keeps the parent's entries while
// the fork's context reads them, and releasing the fork then frees both.
func TestReleaseKeepsAChainAnotherContextReads(t *testing.T) {
	tr := newTranscript(t, 2)
	for i := range 10 {
		tr.turn(i)
	}
	at := tr.nextID - 1
	child := tr.nextID
	tr.nextID++
	tr.db.convs[child] = AppendConversation(nil, ConvRecord{ID: child, HasParent: true, ParentConv: 2, ParentAt: at})
	ct := &transcript{t: t, db: tr.db, conv: child, nextID: tr.nextID, seq: tr.seq}
	ct.turn(10)
	st := NewStore()
	v, err := tr.db.context(t, st, child, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := messagesOf(v)
	st.Release(2)
	if !loaded(st, 2) || !loaded(st, child) {
		t.Fatal("releasing the parent freed a chain the fork's context reads")
	}
	before := tr.db.rowsRead
	v, err = tr.db.context(t, st, child, 0)
	if err != nil {
		t.Fatal(err)
	}
	if tr.db.rowsRead != before {
		t.Fatalf("the fork's context read %d rows after the parent's release", tr.db.rowsRead-before)
	}
	sameMessages(t, messagesOf(v), want)
	st.Release(child)
	if loaded(st, 2) || loaded(st, child) {
		t.Fatal("releasing the fork kept a chain no context reads")
	}
	v, err = tr.db.context(t, st, child, 0)
	if err != nil {
		t.Fatal(err)
	}
	sameMessages(t, messagesOf(v), want)
}

// A View aliases chains the store may reset (Release for idleness, a bounded chain's reset, a new load): Valid reports it, so
// a caller that holds a view across an async gap re-derives instead of indexing a reset chain (the compaction run of a
// conversation whose kept range the scheduler dropped while the run waited on its range scan).
func TestAViewReportsWhenItsChainsWereReset(t *testing.T) {
	tr := newTranscript(t, 2)
	for i := range 20 {
		tr.turn(i)
	}
	st := NewStore()
	v, err := tr.db.context(t, st, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Valid() {
		t.Fatal("a fresh view is not valid")
	}
	want := messagesOf(v)
	st.Release(2)
	if v.Valid() {
		t.Fatal("a view whose chain Release reset is still valid")
	}
	v, err = tr.db.context(t, st, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Valid() {
		t.Fatal("the re-derived view is not valid")
	}
	sameMessages(t, messagesOf(v), want)
}

// A message's identity is stable while its chain is: the same message of a later derivation of a growing conversation has
// the same identity, a message of a reset chain does not, and a synthesized result has none.
func TestMessageIdentIsStableForStoredBytes(t *testing.T) {
	tr := newTranscript(t, 2)
	for i := range 8 {
		tr.turn(i)
	}
	st := NewStore()
	v, err := tr.db.context(t, st, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	n := v.NumMessages()
	first := make([]MessageIdent, n)
	for i := range n {
		id, ok := v.MessageIdent(i)
		if !ok {
			t.Fatalf("message %d of a complete transcript is synthesized", i)
		}
		first[i] = id
	}
	for i := range n {
		for j := range i {
			if first[i] == first[j] {
				t.Fatalf("messages %d and %d share one identity", j, i)
			}
		}
	}
	st.Release(2)
	v, err = tr.db.context(t, st, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := v.MessageIdent(0); id == first[0] {
		t.Fatal("a message of a reset chain kept its identity")
	}
}

// With SetTrimAtHead a head marker bounds what the store holds: a long conversation compacted over and over keeps the entries
// of the newest marker's range, not every entry and edit it was fed, and the context at the tail is what an untrimmed store
// derives. An as-of read below the marker loads its range again and yields the same messages as the untrimmed store.
func TestTrimAtHeadBoundsTheChainToTheNewestMarkersRange(t *testing.T) {
	tr := newTranscript(t, 2)
	trim, full := NewStore(), NewStore()
	trim.SetTrimAtHead()
	feed := func(from int) {
		for _, row := range tr.db.entries[from:] {
			for _, st := range []*Store{trim, full} {
				if err := st.Append(2, row.id, row.seq, row.rec); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, st := range []*Store{trim, full} {
		st.AddConversation(ConvRecord{ID: 2}, true)
	}
	var cutoff int64
	for round := range 40 {
		start := len(tr.db.entries)
		var keep int64
		for i := range 5 {
			first := tr.turn(round*5 + i)
			if i == 3 {
				keep = first
			}
		}
		if round == 20 {
			cutoff = tr.nextID - 1
		}
		tr.compact(keep)
		feed(start)
	}
	vt, err := tr.db.context(t, trim, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	vf, err := tr.db.context(t, full, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	sameMessages(t, messagesOf(vt), messagesOf(vf))
	if got, all := trim.chainList[0].Len(), full.chainList[0].Len(); got >= all/4 {
		t.Fatalf("the trimmed chain holds %d of %d entries", got, all)
	}
	if got, all := len(trim.chainList[0].edits), len(full.chainList[0].edits); all > 0 && got >= all/4 {
		t.Fatalf("the trimmed chain indexes %d of %d edits", got, all)
	}
	// An as-of read below the newest marker still derives the context the untrimmed store derives.
	at, err := tr.db.context(t, trim, 2, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	wantAt, err := tr.db.context(t, full, 2, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	sameMessages(t, messagesOf(at), messagesOf(wantAt))
}

// A chain extended by a load (entries committed while the conversation was not appended to) holds head markers that no Append
// saw: EndLoad drops what is below the newest one, so the chain follows the marker's range, not the loads since the first.
func TestEndLoadTrimsTheChainBelowTheNewestMarkersHead(t *testing.T) {
	tr := newTranscript(t, 2)
	st := NewStore()
	st.SetTrimAtHead()
	for i := range 20 {
		tr.turn(i)
	}
	// The core derives at the newest entry's id, a bounded read: the chain it loads is then extended by the next one.
	if _, err := tr.db.context(t, st, 2, tr.nextID-1); err != nil {
		t.Fatal(err)
	}
	var keep int64
	for i := 20; i < 60; i++ {
		first := tr.turn(i)
		if i == 50 {
			keep = first
		}
	}
	tr.compact(keep)
	v, err := tr.db.context(t, st, 2, tr.nextID-1)
	if err != nil {
		t.Fatal(err)
	}
	loaded := st.chainList[0].Len()
	want := messagesOf(v)
	if err := st.EndLoad(2); err != nil {
		t.Fatal(err)
	}
	if got := st.chainList[0].Len(); got >= loaded/2 {
		t.Fatalf("EndLoad left %d of %d entries", got, loaded)
	}
	v, err = tr.db.context(t, st, 2, tr.nextID-1)
	if err != nil {
		t.Fatal(err)
	}
	sameMessages(t, messagesOf(v), want)
}
