package durable

import (
	"context"
	"errors"
	"testing"
)

type entryOnlyRuntime struct {
	TaskRuntime[struct{}, struct{}, struct{}, struct{}]
	entries map[EntryId]*EntryRecord
	err     error
}

func (r entryOnlyRuntime) Entry(_ context.Context, id EntryId) (*EntryRecord, error) {
	return r.entries[id], r.err
}

func asRuntime(r entryOnlyRuntime) TaskRuntime[struct{}, struct{}, struct{}, struct{}] { return r }

// upstream: packages/durable/src/types.ts:219 entry(token, id, context): undefined when the entry is absent, not visible or has another
// kind; the typed record otherwise; a read failure rejects.
func TestTaskEntryNarrowsToTheTokenKind(t *testing.T) {
	type note struct{ Text string }
	noteEntry := DefineEntry[note]("test.task-entry.note")
	other := DefineEntry[Never]("test.task-entry.other")
	data, err := ToJsonValue(note{Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := entryOnlyRuntime{entries: map[EntryId]*EntryRecord{
		1: {Id: 1, Kind: noteEntry.Kind, Data: data},
		2: {Id: 2, Kind: other.Kind},
	}}
	ctx := context.Background()

	got, err := TaskEntry[note](ctx, asRuntime(runtime), noteEntry, 1)
	if err != nil || got == nil || got.TypedData.Text != "hi" || got.Id != 1 {
		t.Fatalf("matching kind = %+v, %v; want the decoded entry", got, err)
	}
	if got, err := TaskEntry[note](ctx, asRuntime(runtime), noteEntry, 2); got != nil || err != nil {
		t.Fatalf("another kind = %+v, %v; want nil", got, err)
	}
	if got, err := TaskEntry[note](ctx, asRuntime(runtime), noteEntry, 9); got != nil || err != nil {
		t.Fatalf("absent entry = %+v, %v; want nil", got, err)
	}
	boom := errors.New("boom")
	runtime.err = boom
	if _, err := TaskEntry[note](ctx, asRuntime(runtime), noteEntry, 1); !errors.Is(err, boom) {
		t.Fatalf("read failure = %v, want it returned", err)
	}
}
