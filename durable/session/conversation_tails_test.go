package session_test

// pi: packages/durable/test/session-conversation-tails.test.ts
// pi: packages/durable/src/session/conversation-tails.ts

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

func tailEntry(id, conversationId int64, head ...int64) durable.StorageWrite {
	value := durable.EntryRecord{Id: durable.EntryId(id), ConversationId: durable.ConversationId(conversationId), Kind: "note"}
	if len(head) > 0 {
		h := durable.EntryId(head[0])
		value.Head = &h
	}
	return durable.EntryWrite{Value: value}
}

var errTailRead = errors.New("read")

func tailNever[T any]() (*T, error) { return nil, errTailRead }

func entryIdPtr(id int64) *durable.EntryId {
	value := durable.EntryId(id)
	return &value
}

func expectLatest(t *testing.T, got *durable.EntryId, err error, want int64) {
	t.Helper()
	if err != nil || got == nil || int64(*got) != want {
		t.Fatalf("latest = %v, %v; want %d", got, err, want)
	}
}

func TestConversationTailsReadsOnceThenFollowsCommits(t *testing.T) {
	tails := session.NewConversationTails()
	reads := 0
	read := func() (*durable.EntryId, error) {
		reads++
		return entryIdPtr(5), nil
	}
	got, err := tails.Latest(1, read)
	expectLatest(t, got, err, 5)
	got, err = tails.Latest(1, read)
	expectLatest(t, got, err, 5)
	if reads != 1 {
		t.Fatalf("reads = %d, want 1", reads)
	}
	tails.Apply([]durable.StorageWrite{tailEntry(9, 1)})
	got, err = tails.Latest(1, tailNever[durable.EntryId])
	expectLatest(t, got, err, 9)
}

func TestConversationTailsKnowsNewRootConversationIsEmptyAndForkOnlyOnceReadOrWritten(t *testing.T) {
	tails := session.NewConversationTails()
	tails.Apply([]durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: 2}},
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: 3, Parent: &durable.ConversationParent{ConversationId: 2, At: 4}}},
	})
	if got, err := tails.Latest(2, tailNever[durable.EntryId]); got != nil || err != nil {
		t.Fatalf("latest(2) = %v, %v", got, err)
	}
	if got, err := tails.Marker(2, nil, tailNever[session.HeadMarker]); got != nil || err != nil {
		t.Fatalf("marker(2) = %v, %v", got, err)
	}
	got, err := tails.Latest(3, func() (*durable.EntryId, error) { return entryIdPtr(4), nil })
	expectLatest(t, got, err, 4)
}

func TestConversationTailsAnswersCutoffFromNewestMarkerOnlyAtOrAfterIt(t *testing.T) {
	tails := session.NewConversationTails()
	tails.Apply([]durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: 1}},
		tailEntry(10, 1),
		tailEntry(11, 1, 10),
	})
	marker, err := tails.Marker(1, nil, tailNever[session.HeadMarker])
	if err != nil || marker == nil || marker.Id != 11 || marker.Head == nil {
		t.Fatalf("marker = %+v, %v", marker, err)
	}
	marker, err = tails.Marker(1, entryIdPtr(12), tailNever[session.HeadMarker])
	if err != nil || marker == nil || marker.Id != 11 {
		t.Fatalf("marker = %+v, %v", marker, err)
	}
	// An older cutoff may have an older marker: read, and not kept.
	reads := 0
	older := func() (*session.HeadMarker, error) {
		reads++
		return nil, nil
	}
	for range 2 {
		if marker, err := tails.Marker(1, entryIdPtr(10), older); marker != nil || err != nil {
			t.Fatalf("marker = %+v, %v", marker, err)
		}
	}
	if reads != 2 {
		t.Fatalf("reads = %d, want 2", reads)
	}
	marker, err = tails.Marker(1, nil, tailNever[session.HeadMarker])
	if err != nil || marker == nil || marker.Id != 11 {
		t.Fatalf("marker = %+v, %v", marker, err)
	}
}

func TestConversationTailsKeepsBoundedNumberOfConversationsFewerWithMarkers(t *testing.T) {
	tails := session.NewConversationTails()
	for id := int64(1); id <= 5000; id++ {
		tails.Apply([]durable.StorageWrite{tailEntry(2*id, id), tailEntry(2*id+1, id, 2*id)})
	}
	if got := tails.Size(); got != 4096 {
		t.Fatalf("size = %d", got)
	}
	if got := tails.Markers(); got != 256 {
		t.Fatalf("markers = %d", got)
	}
}

func TestConversationTailsPeekIsThreeStated(t *testing.T) {
	tails := session.NewConversationTails()
	if _, state := tails.Peek(1); state != session.TailUnknown {
		t.Fatalf("state = %v, want unknown", state)
	}
	tails.Apply([]durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: 1}}})
	if _, state := tails.Peek(1); state != session.TailNone {
		t.Fatalf("state = %v, want none", state)
	}
	tails.Apply([]durable.StorageWrite{tailEntry(7, 1)})
	if id, state := tails.Peek(1); state != session.TailKnown || id != 7 {
		t.Fatalf("peek = %d, %v", id, state)
	}
	if _, state := tails.Peek(2); state != session.TailUnknown {
		t.Fatalf("state = %v, want unknown", state)
	}
}

func TestConversationTailsReadErrorsAreReturnedAndNothingIsKept(t *testing.T) {
	tails := session.NewConversationTails()
	if _, err := tails.Latest(1, tailNever[durable.EntryId]); !errors.Is(err, errTailRead) {
		t.Fatalf("err = %v", err)
	}
	if _, state := tails.Peek(1); state != session.TailUnknown {
		t.Fatalf("state = %v", state)
	}
	if _, err := tails.Marker(1, nil, tailNever[session.HeadMarker]); !errors.Is(err, errTailRead) {
		t.Fatalf("err = %v", err)
	}
}

// pi: it("freezes the markers it keeps"), written on the same inputs: entry(2, 1, 1) applied to fresh tails. Pi asserts
// Object.isFrozen on the written record; Go has no freeze, so the test holds what the freeze protects: nothing a caller
// does to the record it wrote, or to a marker it was given, changes the marker the tails keep.
func TestConversationTailsFreezesTheMarkersItKeeps(t *testing.T) {
	tails := session.NewConversationTails()
	write := tailEntry(2, 1, 1).(durable.EntryWrite)
	write.Value.Model = []ai.Message{ai.UserMessage{Content: ai.UserText("kept"), Timestamp: 1}}
	tails.Apply([]durable.StorageWrite{write})

	// The caller changes the record it wrote, through the pointer and slice it still holds.
	*write.Value.Head = 99
	write.Value.Model[0] = ai.UserMessage{Content: ai.UserText("changed"), Timestamp: 2}
	write.Value.Kind = "changed"

	want := func(marker *session.HeadMarker, err error) {
		t.Helper()
		if err != nil || marker == nil || marker.Id != 2 || marker.Kind != "note" || marker.Head == nil || *marker.Head != 1 ||
			len(marker.Model) != 1 || marker.Model[0] != (ai.UserMessage{Content: ai.UserText("kept"), Timestamp: 1}) {
			t.Fatalf("kept marker = %+v, %v; want the record as written", marker, err)
		}
	}
	first, err := tails.Marker(1, nil, tailNever[session.HeadMarker])
	want(first, err)

	// The caller changes the marker it was given.
	*first.Head = 77
	first.Model[0] = ai.UserMessage{Content: ai.UserText("changed"), Timestamp: 2}
	first.Kind = "changed"
	second, err := tails.Marker(1, nil, tailNever[session.HeadMarker])
	want(second, err)
}

// pi: conversation-tails.ts marker(): `freeze(found)` freezes a marker the read returned before it is kept (the same freeze as
// apply's), so what a caller does to it cannot change what the tails keep. The Go twin of the test above for the read path: the
// kept marker is a copy of the one the read returned, and the marker it handed back is the caller's own.
// mutation-checked: keeping the marker the read returned (not a copy) fails it
func TestConversationTailsKeepsACopyOfAMarkerItsReadReturned(t *testing.T) {
	tails := session.NewConversationTails()
	read := tailEntry(7, 1, 3).(durable.EntryWrite).Value
	read.Model = []ai.Message{ai.UserMessage{Content: ai.UserText("kept"), Timestamp: 1}}
	got, err := tails.Marker(1, nil, func() (*session.HeadMarker, error) { return &read, nil })
	if err != nil || got == nil || got.Id != 7 {
		t.Fatalf("marker = %+v, %v", got, err)
	}
	// The caller changes the marker it got, and the record its read still holds.
	*got.Head = 99
	got.Model[0] = ai.UserMessage{Content: ai.UserText("changed"), Timestamp: 2}
	got.Kind = "changed"
	kept, err := tails.Marker(1, nil, tailNever[session.HeadMarker])
	if err != nil || kept == nil || kept.Id != 7 || kept.Kind != "note" || kept.Head == nil || *kept.Head != 3 ||
		len(kept.Model) != 1 || kept.Model[0] != (ai.UserMessage{Content: ai.UserText("kept"), Timestamp: 1}) {
		t.Fatalf("kept marker = %+v, %v; want the record the read returned", kept, err)
	}
}

// pi: conversation-tails.ts marker() :52-56: the read result is kept only when no newer entry can change it: no cutoff, or
// the newest entry known before the read is at or before the cutoff (a conversation known to have none counts as 0), and a
// found `undefined` is kept as `null`: the conversation has no marker, for every cutoff.
// mutation-checked: keeping every read, keeping none under a cutoff, comparing the cutoff with < instead of <=, treating an unknown newest entry as old enough, or not keeping an absent marker fails it
func TestConversationTailsKeepsAMarkerReadOnlyWhenNoNewerEntryCanChangeIt(t *testing.T) {
	marker := func(id int64) *session.HeadMarker {
		value := tailEntry(id, 1, id-1).(durable.EntryWrite).Value
		return &value
	}
	counted := func(found *session.HeadMarker, reads *int) func() (*session.HeadMarker, error) {
		return func() (*session.HeadMarker, error) {
			*reads++
			return found, nil
		}
	}
	latestRead := func(tails *session.ConversationTails, id int64) {
		t.Helper()
		got, err := tails.Latest(1, func() (*durable.EntryId, error) { return entryIdPtr(id), nil })
		expectLatest(t, got, err, id)
	}
	ask := func(tails *session.ConversationTails, cutoff *durable.EntryId, read func() (*session.HeadMarker, error)) *session.HeadMarker {
		t.Helper()
		got, err := tails.Marker(1, cutoff, read)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	t.Run("the newest entry is not known: a cutoff read is not kept", func(t *testing.T) {
		tails := session.NewConversationTails()
		reads := 0
		for range 2 {
			if got := ask(tails, entryIdPtr(7), counted(marker(4), &reads)); got == nil || got.Id != 4 {
				t.Fatalf("marker = %+v", got)
			}
		}
		if reads != 2 {
			t.Fatalf("reads = %d, want 2", reads)
		}
	})

	t.Run("the newest entry is at or before the cutoff: the read is kept, and answers every cutoff at or after it", func(t *testing.T) {
		tails := session.NewConversationTails()
		latestRead(tails, 5)
		reads := 0
		if got := ask(tails, entryIdPtr(5), counted(marker(4), &reads)); got == nil || got.Id != 4 {
			t.Fatalf("marker = %+v", got)
		}
		if got := ask(tails, entryIdPtr(9), tailNever[session.HeadMarker]); got == nil || got.Id != 4 {
			t.Fatalf("a kept marker did not answer a cutoff after it: %+v", got)
		}
		if got := ask(tails, entryIdPtr(4), tailNever[session.HeadMarker]); got == nil || got.Id != 4 {
			t.Fatalf("a kept marker did not answer a cutoff at it: %+v", got)
		}
		// A cutoff before the kept marker is not answered by it: read again, and kept no more than the first.
		if got := ask(tails, entryIdPtr(3), counted(nil, &reads)); got != nil {
			t.Fatalf("marker = %+v, want the read's absent marker", got)
		}
		if reads != 2 {
			t.Fatalf("reads = %d, want 2", reads)
		}
	})

	t.Run("the newest entry is after the cutoff: the read is not kept", func(t *testing.T) {
		tails := session.NewConversationTails()
		latestRead(tails, 5)
		reads := 0
		for range 2 {
			if got := ask(tails, entryIdPtr(4), counted(marker(2), &reads)); got == nil || got.Id != 2 {
				t.Fatalf("marker = %+v", got)
			}
		}
		if reads != 2 {
			t.Fatalf("reads = %d, want 2", reads)
		}
	})

	t.Run("a conversation known to have no entry is older than any cutoff", func(t *testing.T) {
		tails := session.NewConversationTails()
		got, err := tails.Latest(1, func() (*durable.EntryId, error) { return nil, nil })
		if got != nil || err != nil {
			t.Fatalf("latest = %v, %v", got, err)
		}
		reads := 0
		if got := ask(tails, entryIdPtr(1), counted(nil, &reads)); got != nil {
			t.Fatalf("marker = %+v", got)
		}
		if got := ask(tails, entryIdPtr(1), tailNever[session.HeadMarker]); got != nil {
			t.Fatalf("marker = %+v", got)
		}
		if reads != 1 {
			t.Fatalf("reads = %d, want 1", reads)
		}
	})

	t.Run("an absent marker read without a cutoff is kept for every cutoff", func(t *testing.T) {
		tails := session.NewConversationTails()
		reads := 0
		if got := ask(tails, nil, counted(nil, &reads)); got != nil {
			t.Fatalf("marker = %+v", got)
		}
		for _, cutoff := range []*durable.EntryId{nil, entryIdPtr(1), entryIdPtr(99)} {
			if got := ask(tails, cutoff, tailNever[session.HeadMarker]); got != nil {
				t.Fatalf("cutoff %v: marker = %+v, want none", cutoff, got)
			}
		}
		if reads != 1 {
			t.Fatalf("reads = %d, want 1", reads)
		}
	})
}

// pi: conversation-tails.ts apply() :84-102: an entry without a head leaves the newest marker; an entry with one replaces it;
// a fork's conversation record leaves what is known of the conversation (the parent's entry at the fork point is not its own
// newest); writes of other kinds change nothing.
// mutation-checked: a headless entry clearing the marker, a head entry not replacing it, a fork record resetting the conversation, or a task write being applied fails it
func TestConversationTailsApplyKeepsWhatAWriteDoesNotChange(t *testing.T) {
	tails := session.NewConversationTails()
	tails.Apply([]durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: 1}},
		tailEntry(10, 1),
		tailEntry(11, 1, 10),
		tailEntry(12, 1),
	})
	marker, err := tails.Marker(1, nil, tailNever[session.HeadMarker])
	if err != nil || marker == nil || marker.Id != 11 {
		t.Fatalf("after a headless entry: marker = %+v, %v; want the head entry 11", marker, err)
	}
	got, err := tails.Latest(1, tailNever[durable.EntryId])
	expectLatest(t, got, err, 12)
	tails.Apply([]durable.StorageWrite{tailEntry(13, 1, 12)})
	marker, err = tails.Marker(1, nil, tailNever[session.HeadMarker])
	if err != nil || marker == nil || marker.Id != 13 {
		t.Fatalf("after a head entry: marker = %+v, %v; want 13", marker, err)
	}

	// A fork's record, and a write of another kind, change nothing known of conversation 1 or of a conversation with entries.
	tails.Apply([]durable.StorageWrite{tailEntry(20, 3)})
	tails.Apply([]durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: 3, Parent: &durable.ConversationParent{ConversationId: 1, At: 12}}},
		durable.TaskWrite{Value: taskRecord(1, durable.TaskPending)},
	})
	got, err = tails.Latest(3, tailNever[durable.EntryId])
	expectLatest(t, got, err, 20)
	if tails.Size() != 2 || tails.Markers() != 1 {
		t.Fatalf("size = %d, markers = %d; want 2, 1", tails.Size(), tails.Markers())
	}
}

// pi: conversation-tails.ts use()/put() :95-110: each map drops the least recently used conversation first; a read that finds a
// conversation, and a write to it, make it the most recently used; `peek` does not.
// mutation-checked: a read that does not make the conversation most recently used, a write that does not, a peek that does, or evicting the most recently used fails it
func TestConversationTailsDropTheLeastRecentlyUsedConversationFirst(t *testing.T) {
	const latestCapacity, markerCapacity = 4096, 256
	known := func(tails *session.ConversationTails, id int64) bool {
		_, state := tails.Peek(durable.ConversationId(id))
		return state != session.TailUnknown
	}
	fill := func(tails *session.ConversationTails, count int) {
		for id := int64(1); id <= int64(count); id++ {
			tails.Apply([]durable.StorageWrite{tailEntry(id, id)})
		}
	}

	t.Run("newest entries: Latest and Apply refresh a conversation, Peek does not", func(t *testing.T) {
		tails := session.NewConversationTails()
		fill(tails, latestCapacity)
		if _, state := tails.Peek(1); state != session.TailKnown {
			t.Fatal("peek: conversation 1 is not kept")
		}
		if got, err := tails.Latest(2, tailNever[durable.EntryId]); err != nil || got == nil {
			t.Fatalf("Latest(2) = %v, %v", got, err)
		}
		tails.Apply([]durable.StorageWrite{tailEntry(100000, 3)})
		tails.Apply([]durable.StorageWrite{tailEntry(100001, latestCapacity+1)})
		tails.Apply([]durable.StorageWrite{tailEntry(100002, latestCapacity+2)})
		// 1 was only peeked at, so it is the oldest: it went first; 4 is the oldest of the rest; 2 and 3 were refreshed.
		if known(tails, 1) || known(tails, 4) || !known(tails, 2) || !known(tails, 3) || !known(tails, 5) {
			t.Fatalf("kept: 1=%v 2=%v 3=%v 4=%v 5=%v; want only 2, 3 and 5", known(tails, 1), known(tails, 2), known(tails, 3), known(tails, 4), known(tails, 5))
		}
		if tails.Size() != latestCapacity {
			t.Fatalf("size = %d, want %d", tails.Size(), latestCapacity)
		}
	})

	t.Run("newest head markers: Marker and Apply refresh a conversation", func(t *testing.T) {
		tails := session.NewConversationTails()
		for id := int64(1); id <= markerCapacity; id++ {
			tails.Apply([]durable.StorageWrite{tailEntry(2*id, id, 2*id-1)})
		}
		if got, err := tails.Marker(1, nil, tailNever[session.HeadMarker]); err != nil || got == nil {
			t.Fatalf("Marker(1) = %v, %v", got, err)
		}
		tails.Apply([]durable.StorageWrite{tailEntry(100000, 2, 99999)})
		tails.Apply([]durable.StorageWrite{tailEntry(100001, markerCapacity+1, 99999)})
		tails.Apply([]durable.StorageWrite{tailEntry(100002, markerCapacity+2, 99999)})
		reads := map[int64]int{}
		kept := func(id int64) bool {
			before := reads[id]
			_, err := tails.Marker(durable.ConversationId(id), nil, func() (*session.HeadMarker, error) {
				reads[id]++
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			return reads[id] == before
		}
		// 3 and 4 were the oldest once 1 and 2 were refreshed.
		// Hits first: reading an absent marker keeps it, which itself takes the least recently used place.
		if !kept(1) || !kept(2) || !kept(5) || kept(3) || kept(4) {
			t.Fatalf("marker reads: %v; want only conversations 3 and 4 read (1, 2 and 5 are kept)", reads)
		}
	})
}
