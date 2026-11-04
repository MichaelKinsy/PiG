// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// markerRecord is the head marker entry 11 of conversation 4: it keeps the range from entry 10.
func markerRecord() *jv.Object {
	rec := jv.NewObject()
	rec.Set("id", 11.0)
	rec.Set("conversationId", 4.0)
	rec.Set("kind", "note")
	rec.Set("head", 10.0)
	return rec
}

// boundsRig is a handler over a mirror holding conversation 4 with entries 10, 11 (a compaction keeping from 10) and 12, whose Session
// has seen none of them.
func boundsRig(t *testing.T) *handler {
	t.Helper()
	mem := sessiontest.New()
	h := &handler{mir: newMirror()}
	h.sess = session.New(mem, mem.NextID(), func() float64 { return 0 })
	record := func(id session.ID, head session.ID) *jv.Object {
		rec := jv.NewObject()
		rec.Set("id", float64(id))
		rec.Set("conversationId", 4.0)
		rec.Set("kind", "note")
		if head != 0 {
			rec.Set("head", float64(head))
		}
		return rec
	}
	conversation := jv.NewObject()
	conversation.Set("id", 4.0)
	writes := []session.Write{
		{Kind: session.WriteConversation, Record: conversation},
		{Kind: session.WriteEntry, Record: record(10, 0)},
		{Kind: session.WriteEntry, Record: record(11, 10)},
		{Kind: session.WriteEntry, Record: record(12, 0)},
	}
	if _, err := h.mir.Commit(writes, 13, nil); err != nil {
		t.Fatal(err)
	}
	return h
}

// context.ts captureContextBounds: the newest entry and the head marker of a conversation the Session's commits wrote are known, so the
// bounds job reads nothing from Storage and costs one await for each.
func TestContextBoundsOfWhatTheSessionWroteReadNothing(t *testing.T) {
	h := boundsRig(t)
	h.sess.NoteLatestEntryID(4, 12)
	h.sess.NoteHeadMarker(4, 0, markerRecord())
	b, err := h.captureBounds(4, 0, turn.KeptRange{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !b.has || b.tail != 12 || b.head != 11 || b.from != 10 {
		t.Fatalf("bounds %+v, want tail 12 under head marker 11 from 10", b)
	}
	if h.job.change != 2*hopcost.KnownTail || h.job.nreads != 0 {
		t.Fatalf("job charged %d ticks and %d reads, want two awaits of known values and no read", h.job.change, h.job.nreads)
	}
	// A cutoff at the newest entry is answered by the same marker.
	h.job = jobStats{}
	if b, err = h.captureBounds(4, 12, turn.KeptRange{Ok: true, Head: 11, From: 10, Tail: 12, Current: true}, false); err != nil || b.tail != 12 || b.head != 11 {
		t.Fatalf("cutoff bounds %+v, %v", b, err)
	}
}

// A conversation the Session has not seen is read from Storage once, and the reading then makes it known.
func TestContextBoundsOfAConversationNotKnownReadStorageOnce(t *testing.T) {
	h := boundsRig(t)
	b, err := h.captureBounds(4, 0, turn.KeptRange{}, false)
	if err != nil || !b.has || b.tail != 12 || b.head != 11 || b.from != 10 {
		t.Fatalf("bounds %+v, %v, want tail 12 under head marker 11 from 10", b, err)
	}
	want := h.readHops(hopcost.ReadScanEntries) + h.sess.ColdEntryAwaits() + h.readHops(hopcost.ReadHeadMarker) + h.sess.ColdMarkerAwaits()
	if h.job.change != want || h.job.nreads != 2 {
		t.Fatalf("the reads cost %d ticks and %d Storage reads, want %d ticks and the entry scan and the head marker read", h.job.change, h.job.nreads, want)
	}
	if id, known := h.sess.LookupLatestEntryID(4); !known || id != 12 {
		t.Fatalf("the Session did not keep the newest entry: %d %v", id, known)
	}
	if marker, known := h.sess.LookupHeadMarker(4, 0); !known || marker == nil {
		t.Fatalf("the Session did not keep the head marker: %v %v", marker, known)
	}
	h.job = jobStats{}
	if _, err := h.captureBounds(4, 0, turn.KeptRange{}, false); err != nil || h.job.change != 2*hopcost.KnownTail || h.job.nreads != 0 {
		t.Fatalf("the second read cost %d ticks and %d reads, %v, want two awaits and no read", h.job.change, h.job.nreads, err)
	}
}

// An entry of the range kept as the read was called is visible without an entry read; any other cutoff reads the entry.
func TestContextBoundsOfACutoffInTheKeptRangeReadNoEntry(t *testing.T) {
	h := boundsRig(t)
	h.sess.NoteLatestEntryID(4, 12)
	kept := turn.KeptRange{Ok: true, Head: 11, From: 10, Tail: 12, Current: true}
	b, err := h.captureBounds(4, 11, kept, false)
	if err != nil || b.tail != 11 || b.head != 11 {
		t.Fatalf("bounds %+v, %v, want tail 11", b, err)
	}
	if want := h.readHops(hopcost.ReadHeadMarker) + h.sess.ColdMarkerAwaits(); h.job.change != want {
		t.Fatalf("the cutoff cost %d ticks, want %d: the head marker read and no entry read", h.job.change, want)
	}
	h.job = jobStats{}
	if _, err := h.captureBounds(4, 11, turn.KeptRange{}, false); err != nil {
		t.Fatal(err)
	}
	if want := h.readHops(hopcost.ReadEntryIn) + h.readHops(hopcost.ReadHeadMarker) + h.sess.ColdMarkerAwaits(); h.job.change != want {
		t.Fatalf("a cutoff with no kept range cost %d ticks, want %d: the entry read and the head marker read", h.job.change, want)
	}
}

// A cutoff at an entry the conversation cannot see is the error context.ts throws, after the entry read.
func TestContextBoundsOfAnInvisibleCutoffFail(t *testing.T) {
	h := boundsRig(t)
	_, err := h.captureBounds(4, 5, turn.KeptRange{Ok: true, From: 1, Tail: 12, Current: true}, false)
	var hidden *history.NotVisibleError
	if !errors.As(err, &hidden) || hidden.Entry != 5 || hidden.Conv != 4 {
		t.Fatalf("error %v, want entry 5 not visible from conversation 4", err)
	}
	if want := h.readHops(hopcost.ReadEntryIn); h.job.change != want {
		t.Fatalf("the entry read cost %d ticks, want %d: the mirror does not hold it, so the kept range cannot vouch for it", h.job.change, want)
	}
}

// rangeCurrent: a read at the newest entry is kept current; one cut off at an entry only when it returns the range it started from.
func TestRangeCurrentOnlyOfAReadAtTheNewestEntryOrTheUnchangedRange(t *testing.T) {
	kept := turn.KeptRange{Ok: true, Head: 3, Tail: 9, Current: true}
	for _, c := range []struct {
		name       string
		at         session.ID
		head, tail session.ID
		prev       turn.KeptRange
		want       bool
	}{
		{"no cutoff", 0, 3, 12, kept, true},
		{"no cutoff, no kept range", 0, 3, 12, turn.KeptRange{}, true},
		{"cutoff at the kept tail", 9, 3, 9, kept, true},
		{"cutoff before the kept tail", 8, 3, 8, kept, false},
		{"cutoff past the kept tail", 12, 3, 12, kept, false},
		{"cutoff under another head marker", 9, 4, 9, kept, false},
		{"cutoff at the tail of a range that is not current", 9, 3, 9, turn.KeptRange{Ok: true, Head: 3, Tail: 9}, false},
	} {
		if got := rangeCurrent(c.at, c.head, c.tail, c.prev); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
