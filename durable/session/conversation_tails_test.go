package session_test

// pi: packages/durable/test/session-conversation-tails.test.ts

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
